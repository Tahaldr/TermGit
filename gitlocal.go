package main

// Local Git 2: for repos on this PC the Branches and Commits sections show your local
// branches, tags and history (graph), and Changes also lists your stashes.
// Switch / create / rename / delete / merge / rebase branches, cherry-pick, revert,
// reset, tag, stash, and stage single lines or hunks.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// ---- data ----

type lbranch struct {
	name     string
	current  bool
	remote   bool // only on GitHub (origin/x), no local copy
	tag      bool
	upstream string
	ahead    int
	behind   int
	gone     bool // upstream deleted on GitHub
	sha      string
	subject  string
	age      string
}

type lbranchList struct {
	items []lbranch
	err   string
}

type lcommit struct {
	graph    string // the drawn graph in front (│ ├ *)
	sha      string
	short    string
	subject  string
	author   string
	age      string
	refs     string
	unpushed bool
}

type logList struct {
	items []lcommit
	err   string
}

type stashEntry struct {
	ref, msg, age string
}

type showData struct {
	body, diff string
	err        string
}

func parseTrack(t string) (ahead, behind int, gone bool) {
	if strings.Contains(t, "gone") {
		return 0, 0, true
	}
	if m := aheadRe.FindStringSubmatch(t); m != nil {
		ahead, _ = strconv.Atoi(m[1])
	}
	if m := behindRe.FindStringSubmatch(t); m != nil {
		behind, _ = strconv.Atoi(m[1])
	}
	return
}

func fetchLBranches(dir string) func() func(*model) {
	return func() func(*model) {
		bl := &lbranchList{}
		out, err := gitRun(dir, "for-each-ref", "--sort=-creatordate",
			"--format=%(HEAD)%1f%(refname)%1f%(refname:short)%1f%(upstream:short)%1f%(upstream:track)%1f%(objectname:short)%1f%(subject)%1f%(creatordate:relative)",
			"refs/heads", "refs/remotes", "refs/tags")
		if err != nil {
			bl.err = strings.TrimSpace(out)
		}
		local := map[string]bool{}
		var heads, remotes, tags []lbranch
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			f := strings.Split(line, "\x1f")
			if len(f) < 8 {
				continue
			}
			b := lbranch{current: f[0] == "*", name: f[2], upstream: f[3], sha: f[5], subject: f[6], age: shortAge(f[7])}
			b.ahead, b.behind, b.gone = parseTrack(f[4])
			switch {
			case strings.HasPrefix(f[1], "refs/heads/"):
				local[b.name] = true
				heads = append(heads, b)
			case strings.HasPrefix(f[1], "refs/remotes/"):
				if strings.HasSuffix(f[1], "/HEAD") {
					continue
				}
				b.remote = true
				remotes = append(remotes, b)
			default:
				b.tag = true
				tags = append(tags, b)
			}
		}
		// current branch first, then the others, then GitHub-only branches, then tags
		for i, b := range heads {
			if b.current && i > 0 {
				heads = append([]lbranch{b}, append(heads[:i:i], heads[i+1:]...)...)
				break
			}
		}
		bl.items = append(bl.items, heads...)
		for _, r := range remotes {
			if short := r.name[strings.Index(r.name, "/")+1:]; !local[short] {
				bl.items = append(bl.items, r)
			}
		}
		bl.items = append(bl.items, tags...)
		return func(m *model) { m.lbranches[dir] = bl }
	}
}

// "3 days ago" → "3d"
func shortAge(s string) string {
	f := strings.Fields(s)
	if len(f) < 2 {
		return s
	}
	n := f[0]
	switch {
	case strings.HasPrefix(f[1], "second"):
		return "now"
	case strings.HasPrefix(f[1], "minute"):
		return n + "m"
	case strings.HasPrefix(f[1], "hour"):
		return n + "h"
	case strings.HasPrefix(f[1], "day"):
		return n + "d"
	case strings.HasPrefix(f[1], "week"):
		return n + "w"
	case strings.HasPrefix(f[1], "month"):
		return n + "mo"
	case strings.HasPrefix(f[1], "year"):
		return n + "y"
	}
	return s
}

func fetchLog(dir, ref string) func() func(*model) {
	return func() func(*model) {
		ll := &logList{}
		target := ref
		if target == "" {
			target = "HEAD"
		}
		out, err := gitRun(dir, "log", "--graph", "--format=%x1f%H%x1f%h%x1f%s%x1f%an%x1f%cr%x1f%D", "-n", "300", target, "--")
		if err != nil {
			ll.err = strings.TrimSpace(out)
		}
		unpushed := map[string]bool{}
		if up, err := gitRun(dir, "rev-list", target+"@{u}.."+target); err == nil {
			for _, s := range strings.Fields(up) {
				unpushed[s] = true
			}
		}
		for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			f := strings.Split(line, "\x1f")
			if len(f) < 7 {
				continue // only graph lines
			}
			ll.items = append(ll.items, lcommit{graph: strings.TrimRight(f[0], " "), sha: f[1], short: f[2], subject: f[3],
				author: f[4], age: shortAge(f[5]), refs: f[6], unpushed: unpushed[f[1]]})
		}
		key := dir + "|" + ref
		return func(m *model) { m.llogs[key] = ll }
	}
}

func fetchStash(dir string) func() func(*model) {
	return func() func(*model) {
		var out []stashEntry
		s, err := gitRun(dir, "stash", "list", "--format=%gd%x1f%s%x1f%cr")
		if err == nil {
			for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
				if f := strings.Split(line, "\x1f"); len(f) == 3 {
					out = append(out, stashEntry{f[0], f[1], shortAge(f[2])})
				}
			}
		}
		return func(m *model) { m.stashes[dir] = out }
	}
}

func fetchShow(dir, ref string, stash bool) func() func(*model) {
	return func() func(*model) {
		sd := &showData{}
		var out string
		var err error
		if stash {
			out, err = gitRun(dir, "stash", "show", "-p", "--include-untracked", "--no-color", ref)
			if err != nil { // older git: no --include-untracked for show
				out, err = gitRun(dir, "stash", "show", "-p", "--no-color", ref)
			}
			sd.diff = out
		} else {
			out, err = gitRun(dir, "show", "--no-color", "--format=%B%x1e", ref)
			if i := strings.Index(out, "\x1e"); i >= 0 {
				sd.body, sd.diff = strings.TrimSpace(out[:i]), out[i+1:]
			} else {
				sd.diff = out
			}
		}
		if err != nil {
			sd.err = strings.TrimSpace(out)
		}
		key := dir + "|" + ref
		return func(m *model) { m.shows[key] = sd }
	}
}

// what git is in the middle of (merge / rebase / cherry-pick / revert), "" if nothing
func gitState(dir string) string {
	gd, err := gitRun(dir, "rev-parse", "--git-dir")
	if err != nil {
		return ""
	}
	g := strings.TrimSpace(gd)
	if !filepath.IsAbs(g) {
		g = filepath.Join(dir, g)
	}
	exists := func(p string) bool { _, err := os.Stat(filepath.Join(g, p)); return err == nil }
	switch {
	case exists("rebase-merge") || exists("rebase-apply"):
		return "rebase"
	case exists("MERGE_HEAD"):
		return "merge"
	case exists("CHERRY_PICK_HEAD"):
		return "cherry-pick"
	case exists("REVERT_HEAD"):
		return "revert"
	}
	return ""
}

// ---- which items ----

func (m *model) isLocal() bool { return m.exActive() != nil && m.chDir() != "" }

func (m *model) logKey() string { return m.chDir() + "|" + m.ex.logRef }

func (m *model) selLBranch() *lbranch {
	if it, ok := m.exSel(); ok && it.kind == "lbranch" {
		return it.lb
	}
	return nil
}

func (m *model) selLCommit() *lcommit {
	if it, ok := m.exSel(); ok && it.kind == "lcommit" {
		return it.lc
	}
	return nil
}

func (m *model) selStash() *stashEntry {
	if it, ok := m.exSel(); ok && it.kind == "stash" {
		return it.st
	}
	return nil
}

func (m *model) currentBranch() string {
	if bl := m.lbranches[m.chDir()]; bl != nil {
		for _, b := range bl.items {
			if b.current {
				return b.name
			}
		}
	}
	return m.linfo[strings.ToLower(m.ex.repo.NameWithOwner)].branch
}

// ---- branches keys ----

func (m *model) lbKey(s string) (tea.Cmd, bool) {
	dir := m.chDir()
	b := m.selLBranch()
	cur := m.currentBranch()
	switch s {
	case "n":
		m.prompt = &prompt{label: "New branch from " + cur + ", name:", done: func(m *model, name string) tea.Cmd {
			if name == "" {
				return nil
			}
			return m.git(dir, "Created and switched to "+name, "switch", "switch", "-c", name)
		}}
		return nil, true
	}
	if b == nil {
		return nil, false
	}
	name := b.name
	switch s {
	case "space":
		switch {
		case b.current:
			return m.setStatus("You're already on "+name, false), true
		case b.tag:
			m.prompt = &prompt{label: "Check out tag " + name + "? (you'll be on no branch: n creates one if you commit)", confirm: true,
				done: func(m *model, _ string) tea.Cmd {
					return m.git(dir, "Checked out "+name, "switch", "switch", "--detach", name)
				}}
			return nil, true
		case b.remote:
			return m.git(dir, "Switched to "+name[strings.Index(name, "/")+1:], "switch", "switch", "--track", name), true
		}
		return m.git(dir, "Switched to "+name, "switch", "switch", name), true
	case "enter":
		if b.tag {
			return nil, false
		}
		m.ex.logRef = name
		if b.current {
			m.ex.logRef = ""
		}
		m.ex.cur[secCommits], m.ex.off[secCommits] = 0, 0
		return tea.Batch(m.exSetSec(secCommits), m.setStatus("Commits of "+name, false)), true
	case "b":
		short := name
		if b.remote {
			short = name[strings.Index(name, "/")+1:]
		}
		m.ex.ref, m.ex.refOid, m.ex.dir = short, "", ""
		m.ex.dirCur = map[string]int{}
		return tea.Batch(m.exSetSec(secCode), m.setStatus("Code of "+short+" on GitHub", false)), true
	case "R":
		if b.remote || b.tag {
			return m.setStatus("Only your own branches can be renamed", true), true
		}
		m.prompt = &prompt{label: "Rename " + name + " to:", text: name, done: func(m *model, nn string) tea.Cmd {
			if nn == "" || nn == name {
				return nil
			}
			return m.git(dir, "Renamed to "+nn, "rename", "branch", "-m", name, nn)
		}}
		return nil, true
	case "D", "d":
		switch {
		case b.current:
			return m.setStatus("Switch to another branch first: you're on "+name, true), true
		case b.tag:
			m.prompt = &prompt{label: "Delete tag " + name + "? y = here and on GitHub, n = only here", confirm: true,
				done: func(m *model, _ string) tea.Cmd {
					return func() tea.Msg {
						gitRun(dir, "tag", "-d", name)
						out, err := gitRun(dir, "push", "origin", "--delete", "refs/tags/"+name)
						return gitDoneMsg{dir, "Deleted tag " + name + " here and on GitHub", out, err, "tagdel"}
					}
				},
				no: func(m *model) tea.Cmd { return m.git(dir, "Deleted tag "+name, "tagdel", "tag", "-d", name) }}
		case b.remote:
			short := name[strings.Index(name, "/")+1:]
			m.prompt = &prompt{label: "Delete the branch " + short + " on GitHub? This can't be undone.", confirm: true,
				done: func(m *model, _ string) tea.Cmd {
					return m.git(dir, "Deleted "+short+" on GitHub", "push", "push", "origin", "--delete", short)
				}}
		default:
			m.prompt = &prompt{label: "Delete the branch " + name + " on this PC?", confirm: true,
				done: func(m *model, _ string) tea.Cmd {
					return m.git(dir, "Deleted "+name, "branchdel:"+name, "branch", "-d", name)
				}}
		}
		return nil, true
	case "M":
		if b.current {
			return m.setStatus("Pick another branch to merge into "+cur, true), true
		}
		m.prompt = &prompt{label: "Merge " + name + " into " + cur + "?", confirm: true,
			done: func(m *model, _ string) tea.Cmd {
				return m.git(dir, "Merged "+name+" into "+cur, "merge", "merge", "--no-edit", name)
			}}
		return nil, true
	case "B":
		if b.current {
			return m.setStatus("Pick another branch to rebase "+cur+" onto", true), true
		}
		m.prompt = &prompt{label: "Rebase " + cur + " onto " + name + "? (your commits get replayed on top of it)", confirm: true,
			done: func(m *model, _ string) tea.Cmd {
				return m.git(dir, "Rebased "+cur+" onto "+name, "rebase", "rebase", name)
			}}
		return nil, true
	case "P":
		if b.tag {
			return tea.Batch(m.setStatus("Pushing the tag…", false), m.git(dir, "Pushed tag "+name, "push", "push", "origin", "refs/tags/"+name)), true
		}
	case "u":
		if b.remote || b.tag {
			return nil, false
		}
		m.prompt = &prompt{label: "Link " + name + " to which GitHub branch:", text: "origin/" + name, done: func(m *model, up string) tea.Cmd {
			if up == "" {
				return nil
			}
			return m.git(dir, name+" now follows "+up, "upstream", "branch", "--set-upstream-to="+up, name)
		}}
		return nil, true
	}
	return nil, false
}

// ---- commits keys ----

func (m *model) lcKey(s string) (tea.Cmd, bool) {
	dir := m.chDir()
	c := m.selLCommit()
	if c == nil {
		return nil, false
	}
	sha, short, subj := c.sha, c.short, c.subject
	cur := m.currentBranch()
	switch s {
	case "C":
		m.prompt = &prompt{label: "Copy \"" + subj + "\" (" + short + ") onto " + cur + "? (cherry-pick)", confirm: true,
			done: func(m *model, _ string) tea.Cmd {
				return m.git(dir, "Cherry-picked "+short+" onto "+cur, "cherry-pick", "cherry-pick", sha)
			}}
		return nil, true
	case "t":
		m.prompt = &prompt{label: "Undo \"" + subj + "\" with a new commit? (revert: history stays, nothing is lost)", confirm: true,
			done: func(m *model, _ string) tea.Cmd {
				return m.git(dir, "Reverted "+short, "revert", "revert", "--no-edit", sha)
			}}
		return nil, true
	case "X":
		way := func(label, flag, warn string) command {
			return command{label, "", func(m *model) tea.Cmd {
				m.prompt = &prompt{label: "Reset " + cur + " to " + short + " (" + flag + ")?" + warn, confirm: true,
					done: func(m *model, _ string) tea.Cmd { return m.git(dir, "Reset to "+short, "reset", "reset", flag, sha) }}
				return nil
			}}
		}
		m.openPicker("Reset "+cur+" to "+short+" "+subj, []command{
			way("Soft: the newer commits' changes stay staged", "--soft", ""),
			way("Mixed: they stay as changes, not staged", "--mixed", ""),
			way("Hard: throw everything newer away", "--hard", " This deletes your uncommitted changes too and can't be undone."),
		})
		return nil, true
	case "T":
		m.prompt = &prompt{label: "Tag " + short + " as:", done: func(m *model, name string) tea.Cmd {
			if name == "" {
				return nil
			}
			return m.git(dir, "Tagged "+short+" as "+name+" (P on the tag in Branches pushes it)", "tag", "tag", name, sha)
		}}
		return nil, true
	case "y":
		return m.copyText(sha, "Copied "+short), true
	case "space":
		m.prompt = &prompt{label: "Check out " + short + "? (you'll be on no branch until you switch back)", confirm: true,
			done: func(m *model, _ string) tea.Cmd {
				return m.git(dir, "Checked out "+short, "switch", "switch", "--detach", sha)
			}}
		return nil, true
	}
	return nil, false
}

// ---- stash ----

// s: stash all changes (message asked in the bottom bar); S: choose what to stash
func (m *model) stashKey(s string) (tea.Cmd, bool) {
	dir := m.chDir()
	switch s {
	case "s":
		m.prompt = &prompt{label: "Stash all changes (put them aside). Name it:", done: func(m *model, msg string) tea.Cmd {
			args := []string{"stash", "push", "--include-untracked"}
			if msg != "" {
				args = append(args, "-m", msg)
			}
			return m.git(dir, "Stashed your changes (they're at the bottom of the list)", "stash", args...)
		}}
		return nil, true
	case "S":
		opt := func(label, ok string, args ...string) command {
			return command{label, "", func(m *model) tea.Cmd { return m.git(dir, ok, "stash", args...) }}
		}
		m.openPicker("Stash", []command{
			opt("All changes, new files too", "Stashed everything", "stash", "push", "--include-untracked"),
			opt("All changes, not new files", "Stashed changes", "stash", "push"),
			opt("Only what's staged", "Stashed the staged changes", "stash", "push", "--staged"),
			opt("Only what's NOT staged (keep the staged part)", "Stashed the unstaged changes", "stash", "push", "--keep-index"),
		})
		return nil, true
	}
	st := m.selStash()
	if st == nil {
		return nil, false
	}
	ref := st.ref
	switch s {
	case "enter", "space":
		m.openPicker("Stash "+ref+": "+st.msg, []command{
			{"Pop: bring the changes back and remove the stash", "", func(m *model) tea.Cmd { return m.git(dir, "Popped "+ref, "stash", "stash", "pop", ref) }},
			{"Apply: bring them back, keep the stash", "", func(m *model) tea.Cmd { return m.git(dir, "Applied "+ref, "stash", "stash", "apply", ref) }},
			{"Drop: delete the stash", "", func(m *model) tea.Cmd {
				m.prompt = &prompt{label: "Delete " + ref + "? Its changes are lost.", confirm: true,
					done: func(m *model, _ string) tea.Cmd { return m.git(dir, "Dropped "+ref, "stash", "stash", "drop", ref) }}
				return nil
			}},
			{"New branch from it", "", func(m *model) tea.Cmd {
				m.prompt = &prompt{label: "Branch name:", done: func(m *model, n string) tea.Cmd {
					if n == "" {
						return nil
					}
					return m.git(dir, "Made branch "+n+" from "+ref, "stash", "stash", "branch", n, ref)
				}}
				return nil
			}},
		})
		return nil, true
	case "d":
		m.prompt = &prompt{label: "Delete " + ref + "? Its changes are lost.", confirm: true,
			done: func(m *model, _ string) tea.Cmd { return m.git(dir, "Dropped "+ref, "stash", "stash", "drop", ref) }}
		return nil, true
	}
	return nil, false
}

// abort a merge / rebase / cherry-pick / revert that stopped on conflicts
func (m *model) gitAbort() tea.Cmd {
	dir := m.chDir()
	st := gitState(dir)
	if st == "" {
		return m.setStatus("Nothing to abort", false)
	}
	m.prompt = &prompt{label: "Abort the " + st + "? Everything goes back to how it was before it.", confirm: true,
		done: func(m *model, _ string) tea.Cmd { return m.git(dir, "Aborted the "+st, "abort", st, "--abort") }}
	return nil
}

func (m *model) gitContinue() tea.Cmd {
	dir := m.chDir()
	st := gitState(dir)
	if st == "" {
		return m.setStatus("Nothing to continue", false)
	}
	return m.git(dir, "Continued the "+st, "continue", "-c", "core.editor=true", st, "--continue")
}

// ---- line / hunk staging ----

type hunk struct {
	header string
	lines  []string
}

type parsedDiff struct {
	head  []string // diff --git, index, ---, +++
	hunks []hunk
}

func parseDiff(d string) parsedDiff {
	var p parsedDiff
	for _, l := range strings.Split(strings.TrimRight(d, "\n"), "\n") {
		switch {
		case strings.HasPrefix(l, "@@"):
			p.hunks = append(p.hunks, hunk{header: l})
		case len(p.hunks) == 0:
			p.head = append(p.head, l)
		default:
			h := &p.hunks[len(p.hunks)-1]
			h.lines = append(h.lines, l)
		}
	}
	return p
}

// patchFor builds a patch with only hunk h (all of it, or only line `only`).
// reverse = it will be applied with -R (unstaging), so the rules for the other lines swap.
func (p parsedDiff) patchFor(h, only int, reverse bool) string {
	var b strings.Builder
	for _, l := range p.head {
		b.WriteString(l + "\n")
	}
	hk := p.hunks[h]
	b.WriteString(hk.header + "\n")
	kept := true
	for i, l := range hk.lines {
		if l == "" {
			l = " "
		}
		switch {
		case only < 0 || i == only || l[0] == ' ':
			b.WriteString(l + "\n")
			kept = true
		case l[0] == '\\':
			if kept {
				b.WriteString(l + "\n")
			}
		case (l[0] == '-') != reverse:
			// a change we leave out that exists on the side git compares against: keep it as context
			b.WriteString(" " + l[1:] + "\n")
			kept = true
		default:
			kept = false // a change we leave out: drop it
		}
	}
	return b.String()
}

// where each line of the Changes diff panel comes from, so j / k / space know what to stage
type diffLine struct {
	side       string // "unstaged" / "staged" / ""
	hunk, line int    // -1 = the hunk header
}

func (m *model) chLineKey(s string) (tea.Cmd, bool) {
	if m.focus != 1 || m.chSel() == nil || len(m.chMap) == 0 {
		return nil, false
	}
	pick := func(d int) {
		for i := m.chCur + d; i >= 0 && i < len(m.chMap); i += d {
			if dl := m.chMap[i]; dl.side != "" && dl.line >= 0 {
				m.chCur = i
				break
			}
		}
		_, ph := m.layout()
		rows := ph - 2
		if m.chCur < m.pvOff {
			m.pvOff = max(0, m.chCur-2)
		} else if m.chCur >= m.pvOff+rows {
			m.pvOff = m.chCur - rows + 3
		}
	}
	switch s {
	case "j", "down":
		pick(1)
		return nil, true
	case "k", "up":
		pick(-1)
		return nil, true
	case "space", "a":
		if m.chCur < 0 || m.chCur >= len(m.chMap) || m.chMap[m.chCur].side == "" {
			return nil, true
		}
		dl := m.chMap[m.chCur]
		c := m.chSel()
		dir := m.chDir()
		fd := m.diffs[dir+"|"+c.path]
		if fd == nil {
			return nil, true
		}
		src := fd.unstaged
		reverse := dl.side == "staged"
		if reverse {
			src = fd.staged
		}
		only := dl.line
		what := "line"
		if s == "a" {
			only, what = -1, "hunk"
		}
		patch := parseDiff(src).patchFor(dl.hunk, only, reverse)
		f := filepath.Join(os.TempDir(), "termgit-patch.diff")
		os.WriteFile(f, []byte(patch), 0o644)
		args := []string{"apply", "--cached", "--recount", "--whitespace=nowarn"}
		ok := "Staged the " + what
		if reverse {
			args = append(args, "-R")
			ok = "Unstaged the " + what
		}
		return m.git(dir, ok, "apply", append(args, f)...), true
	}
	return nil, false
}

// ---- drawing ----

func (m *model) lbranchRow(b *lbranch, w int, sel bool) string {
	st := lipgloss.NewStyle()
	if sel {
		bg := cChip
		if m.focus == 0 {
			bg = cDim
		}
		st = st.Background(bg)
	}
	nc := cTxt
	if sel {
		nc = cAcc
	}
	icon, ic := icBranch, cMut
	switch {
	case b.current:
		ic = cGreen
	case b.remote:
		icon = icCloud
	case b.tag:
		icon = icTag
	}
	left := st.Foreground(ic).Render(icon) + st.Render(" ") + st.Foreground(nc).Bold(sel || b.current).Render(b.name)
	if b.current {
		left += st.Foreground(cGreen).Render("  ✓ here")
	}
	right := ""
	switch {
	case b.gone:
		right = st.Foreground(cMut).Render("gone on GitHub ")
	case b.ahead > 0 || b.behind > 0:
		if b.ahead > 0 {
			right += st.Foreground(cYellow).Render(fmt.Sprintf("↑%d", b.ahead)) + st.Render(" ")
		}
		if b.behind > 0 {
			right += st.Foreground(cAcc).Render(fmt.Sprintf("↓%d", b.behind)) + st.Render(" ")
		}
	case !b.remote && !b.tag && b.upstream == "":
		right = st.Foreground(cMut).Render("not on GitHub ")
	}
	right += st.Foreground(cMut).Render(fmt.Sprintf("%3s", b.age))
	space := w - width(left) - width(right) - 1
	if space < 1 {
		left = trunc(left, w-width(right)-2)
		space = w - width(left) - width(right) - 1
	}
	return left + st.Render(strings.Repeat(" ", max(space, 0)+1)) + right
}

func (m *model) lcommitRow(c *lcommit, w int, sel bool) string {
	st := lipgloss.NewStyle()
	if sel {
		bg := cChip
		if m.focus == 0 {
			bg = cDim
		}
		st = st.Background(bg)
	}
	nc := cTxt
	if sel {
		nc = cAcc
	}
	left := st.Foreground(cAcc).Faint(true).Render(c.graph) + st.Render(" ") + st.Foreground(cMut).Render(c.short) + st.Render(" ")
	if c.unpushed {
		left += st.Foreground(cYellow).Render("↑") + st.Render(" ")
	}
	if c.refs != "" {
		refs := strings.ReplaceAll(c.refs, "HEAD -> ", "")
		left += st.Foreground(cOrange).Render("("+refs+")") + st.Render(" ")
	}
	left += st.Foreground(nc).Bold(sel).Render(c.subject)
	right := st.Foreground(cMut).Render(fmt.Sprintf("%3s", c.age))
	space := w - width(left) - width(right) - 1
	if space < 1 {
		left = trunc(left, w-width(right)-2)
		space = w - width(left) - width(right) - 1
	}
	return left + st.Render(strings.Repeat(" ", max(space, 0)+1)) + right
}

func (m *model) stashRow(s *stashEntry, w int, sel bool) string {
	st := lipgloss.NewStyle()
	if sel {
		bg := cChip
		if m.focus == 0 {
			bg = cDim
		}
		st = st.Background(bg)
	}
	nc := cHint
	if sel {
		nc = cAcc
	}
	left := st.Foreground(cOrange).Render(icArchive) + st.Render(" ") + st.Foreground(cMut).Render(s.ref) + st.Render(" ") + st.Foreground(nc).Render(s.msg)
	right := st.Foreground(cMut).Render(fmt.Sprintf("%3s", s.age))
	space := w - width(left) - width(right) - 1
	if space < 1 {
		left = trunc(left, w-width(right)-2)
		space = w - width(left) - width(right) - 1
	}
	return left + st.Render(strings.Repeat(" ", max(space, 0)+1)) + right
}

func (m *model) lbranchPreview(b *lbranch, w int) []string {
	cur := m.currentBranch()
	kind := "branch on this PC"
	switch {
	case b.remote:
		kind = "branch on GitHub only (space makes a local copy)"
	case b.tag:
		kind = "tag"
	}
	out := []string{fg(cAcc).Bold(true).Render(b.name), fg(cMut).Render(kind)}
	switch {
	case b.current:
		out = append(out, fg(cGreen).Render("✓ you're on this branch"))
	case b.gone:
		out = append(out, fg(cMut).Render("its GitHub branch was deleted"))
	}
	if b.upstream != "" {
		line := fg(cMut).Render("follows " + b.upstream)
		if b.ahead > 0 {
			line += "  " + fg(cYellow).Render(fmt.Sprintf("↑%d to push", b.ahead))
		}
		if b.behind > 0 {
			line += "  " + fg(cAcc).Render(fmt.Sprintf("↓%d to pull", b.behind))
		}
		out = append(out, line)
	}
	out = append(out, "")
	var h [][2]string
	switch {
	case b.tag:
		h = [][2]string{{"space", "check out"}, {"P", "push the tag"}, {"D", "delete"}}
	case b.current:
		h = [][2]string{{"enter", "commits"}, {"n", "new branch"}, {"R", "rename"}, {"u", "follow a GitHub branch"}, {"b", "code on GitHub"}}
	default:
		h = [][2]string{{"space", "switch to it"}, {"enter", "commits"}, {"M", "merge into " + cur}, {"B", "rebase " + cur + " onto it"}, {"D", "delete"}, {"R", "rename"}}
	}
	out = append(out, wrapJoin([]string{hints(h)}, "", w)...)
	out = append(out, sepLine(w), fg(cHint).Render("Latest commits"), "")
	ll := m.llogs[m.chDir()+"|"+b.name]
	switch {
	case ll == nil:
		out = append(out, fg(cMut).Render("Loading…"))
	case ll.err != "":
		out = append(out, fg(cRed).Render(ll.err))
	default:
		for i, c := range ll.items {
			if i == 15 {
				break
			}
			out = append(out, trunc(fg(cMut).Render(c.short)+" "+fg(cTxt).Render(c.subject)+fg(cMut).Render("  "+c.author+" · "+c.age), w))
		}
	}
	return out
}

func (m *model) lcommitPreview(c *lcommit, w int) []string {
	sd := m.shows[m.chDir()+"|"+c.sha]
	out := wrapText(c.subject, w, fg(cAcc).Bold(true))
	if sd != nil && sd.body != "" {
		if rest := strings.TrimSpace(strings.TrimPrefix(sd.body, c.subject)); rest != "" {
			out = append(out, "")
			for _, p := range strings.Split(rest, "\n") {
				out = append(out, wrapText(p, w, fg(cTxt))...)
			}
		}
	}
	meta := []string{fg(cTxt).Render(c.author), fg(cMut).Render(icClock + " " + c.age + " ago"), fg(cMut).Render(c.short)}
	if c.unpushed {
		meta = append(meta, fg(cYellow).Render("↑ not pushed yet"))
	}
	if c.refs != "" {
		meta = append(meta, fg(cOrange).Render(c.refs))
	}
	out = append(out, "")
	out = append(out, wrapJoin(meta, "   ", w)...)
	out = append(out, hints([][2]string{{"C", "cherry-pick onto " + m.currentBranch()}, {"t", "revert"}, {"X", "reset to here"}, {"T", "tag"}, {"y", "copy id"}}), sepLine(w), "")
	switch {
	case sd == nil:
		out = append(out, fg(cMut).Render("Loading the changes…"))
	case sd.err != "":
		out = append(out, fg(cRed).Render(sd.err))
	default:
		out = append(out, m.cachedLines(fmt.Sprintf("lshow:%s:%d", c.sha, w), func() []string { return gitDiffFiles(sd.diff, w) })...)
	}
	return out
}

func (m *model) stashPreview(s *stashEntry, w int) []string {
	out := []string{fg(cAcc).Bold(true).Render(s.msg), fg(cMut).Render(s.ref + " · " + s.age + " ago"), "",
		hints([][2]string{{"enter", "pop / apply / drop"}, {"d", "drop"}}), sepLine(w), ""}
	sd := m.shows[m.chDir()+"|"+s.ref]
	switch {
	case sd == nil:
		return append(out, fg(cMut).Render("Loading…"))
	case sd.err != "":
		return append(out, fg(cRed).Render(sd.err))
	}
	return append(out, gitDiffFiles(sd.diff, w)...)
}

// gitDiffFiles is gitDiffLines with a header for every file
func gitDiffFiles(diff string, w int) []string {
	var out []string
	var cur []string
	flush := func() {
		if len(cur) > 0 {
			out = append(out, gitDiffLines(strings.Join(cur, "\n"), w)...)
			cur = nil
		}
	}
	for _, l := range strings.Split(diff, "\n") {
		if strings.HasPrefix(l, "diff --git ") {
			flush()
			name := l[strings.LastIndex(l, " b/")+3:]
			if len(out) > 0 {
				out = append(out, "")
			}
			out = append(out, fg(cTxt).Bold(true).Render(name), fg(cDim).Render(strings.Repeat("─", w)))
		}
		cur = append(cur, l)
	}
	flush()
	if len(out) == 0 {
		return []string{fg(cMut).Render("No changes.")}
	}
	return out
}
