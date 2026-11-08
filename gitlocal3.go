package main

// Local Git 3: fixing merge conflicts, interactive rebase (reorder / squash / fixup /
// reword / drop), remotes and worktrees.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

func gitRunEnv(dir string, env []string, args ...string) (string, error) {
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0"), env...)
	out, err := c.CombinedOutput()
	return string(out), err
}

// ---- conflicts ----

type conflictBlock struct {
	start, end   int // lines of the <<<<<<< and >>>>>>> markers
	ours, theirs []string
	oursName     string
	theirsName   string
}

func parseConflicts(text string) ([]string, []conflictBlock) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	var blocks []conflictBlock
	var cur *conflictBlock
	part := 0 // 1 ours, 2 base (diff3), 3 theirs
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "<<<<<<<"):
			cur = &conflictBlock{start: i, oursName: strings.TrimSpace(strings.TrimPrefix(l, "<<<<<<<"))}
			part = 1
		case cur != nil && strings.HasPrefix(l, "|||||||"):
			part = 2
		case cur != nil && strings.HasPrefix(l, "======="):
			part = 3
		case cur != nil && strings.HasPrefix(l, ">>>>>>>"):
			cur.end = i
			cur.theirsName = strings.TrimSpace(strings.TrimPrefix(l, ">>>>>>>"))
			blocks = append(blocks, *cur)
			cur, part = nil, 0
		case cur != nil && part == 1:
			cur.ours = append(cur.ours, l)
		case cur != nil && part == 3:
			cur.theirs = append(cur.theirs, l)
		}
	}
	return lines, blocks
}

func (m *model) conflictPreview(c *change, w int) []string {
	fd := m.diffs[m.chDir()+"|"+c.path]
	out := []string{fg(cAcc).Bold(true).Render(c.path), fg(cRed).Render("conflict: Git couldn't combine both versions here")}
	if m.focus == 1 {
		out = append(out, hints([][2]string{{"j/k", "next conflict"}, {"o", "keep the top"}, {"t", "keep the bottom"}, {"b", "keep both"}, {"e", "edit in Nvim"}}))
	} else {
		out = append(out, hints([][2]string{{"enter", "fix it here"}, {"O", "keep all top"}, {"T", "keep all bottom"}, {"e", "edit in Nvim"}}))
	}
	out = append(out, sepLine(w))
	switch {
	case fd == nil:
		return append(out, fg(cMut).Render("Loading…"))
	case fd.err != "":
		return append(out, fg(cRed).Render(fd.err))
	}
	lines, blocks := parseConflicts(fd.newFile)
	m.cfLines = m.cfLines[:0]
	if len(blocks) == 0 {
		return append(out, fg(cGreen).Render("✓ No conflict markers left: space stages the file"))
	}
	m.cfCur = max(0, min(m.cfCur, len(blocks)-1))
	out = append(out, fg(cMut).Render(fmt.Sprintf("%d %s left", len(blocks), plural(len(blocks), "conflict", "conflicts"))), "")
	bi := 0
	for i := 0; i < len(lines); i++ {
		if bi < len(blocks) && i == blocks[bi].start {
			b := blocks[bi]
			sel := bi == m.cfCur
			bar := fg(cDim).Render("│ ")
			if sel {
				bar = fg(cAcc).Render("▌ ")
			}
			m.cfLines = append(m.cfLines, len(out))
			top, bot := "top", "bottom"
			if sel && m.focus == 1 {
				top, bot = "top · o keeps it", "bottom · t keeps it"
			}
			out = append(out, bar+lipgloss.NewStyle().Background(blend(cGreen, 0.25)).Foreground(cGreen).Render(" ▼ "+b.oursName+" ("+top+") "))
			for _, l := range b.ours {
				out = append(out, bar+fg(cGreen).Render(trunc(strings.ReplaceAll(l, "\t", "    "), w-2)))
			}
			out = append(out, bar+lipgloss.NewStyle().Background(blend(cAcc, 0.25)).Foreground(cAcc).Render(" ▼ "+b.theirsName+" ("+bot+") "))
			for _, l := range b.theirs {
				out = append(out, bar+fg(cAcc).Render(trunc(strings.ReplaceAll(l, "\t", "    "), w-2)))
			}
			out = append(out, bar+fg(cDim).Render("▲ end"))
			i = b.end
			bi++
			continue
		}
		out = append(out, fg(cTxt).Render(trunc(strings.ReplaceAll(lines[i], "\t", "    "), w)))
	}
	return out
}

// keys on a file with conflicts
func (m *model) cfKey(s string) (tea.Cmd, bool) {
	c := m.chSel()
	if c == nil || !c.conflict() {
		return nil, false
	}
	dir := m.chDir()
	path := c.path
	if m.focus == 0 {
		switch s {
		case "O", "T":
			side, which := "--ours", "top"
			if s == "T" {
				side, which = "--theirs", "bottom"
			}
			m.prompt = &prompt{label: "Keep the " + which + " version of every conflict in " + path + "?", confirm: true,
				done: func(m *model, _ string) tea.Cmd {
					return func() tea.Msg {
						if out, err := gitRun(dir, "checkout", side, "--", path); err != nil {
							return gitDoneMsg{dir, "", out, err, "resolve"}
						}
						out, err := gitRun(dir, "add", "--", path)
						return gitDoneMsg{dir, "Resolved " + path + " (staged)", out, err, "resolve"}
					}
				}}
			return nil, true
		case "enter":
			m.focus, m.cfCur = 1, 0
			return nil, true
		}
		return nil, false
	}
	fd := m.diffs[dir+"|"+path]
	if fd == nil {
		return nil, false
	}
	lines, blocks := parseConflicts(fd.newFile)
	scroll := func() {
		if m.cfCur < len(m.cfLines) {
			m.pvOff = max(0, m.cfLines[m.cfCur]-2)
		}
	}
	switch s {
	case "j", "down":
		if m.cfCur < len(blocks)-1 {
			m.cfCur++
		}
		scroll()
		return nil, true
	case "k", "up":
		if m.cfCur > 0 {
			m.cfCur--
		}
		scroll()
		return nil, true
	case "o", "t", "b":
		if m.cfCur >= len(blocks) {
			return nil, true
		}
		b := blocks[m.cfCur]
		keep := b.ours
		switch s {
		case "t":
			keep = b.theirs
		case "b":
			keep = append(append([]string{}, b.ours...), b.theirs...)
		}
		var nl []string
		nl = append(nl, lines[:b.start]...)
		nl = append(nl, keep...)
		nl = append(nl, lines[b.end+1:]...)
		text := strings.Join(nl, "\n")
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(path)), []byte(text), 0o644); err != nil {
			return m.setStatus("Couldn't save: "+err.Error(), true), true
		}
		fd.newFile = text
		delete(m.lines, "cf:"+path)
		if len(blocks) == 1 { // that was the last one: stage the file
			m.focus = 0
			return m.git(dir, "All conflicts in "+path+" fixed: staged it. ctrl+p → Continue when every file is done", "resolve", "add", "--", path), true
		}
		return m.setStatus(fmt.Sprintf("Fixed one, %d left in %s", len(blocks)-1, path), false), true
	}
	return nil, false
}

// ---- interactive rebase ----

type planItem struct {
	action  string // pick, squash, fixup, reword, drop
	sha     string
	short   string
	subject string
	msg     string // reword: the new message
}

type rebasePlan struct {
	base  string // the commit everything is replayed on ("" = from the very first commit)
	items []planItem
	cur   int
}

// commits from c (inclusive) up to HEAD, oldest first
func (m *model) planFrom(c *lcommit) (*rebasePlan, bool) {
	ll := m.llogs[m.logKey()]
	if ll == nil {
		return nil, false
	}
	var items []planItem
	for _, x := range ll.items {
		items = append([]planItem{{"pick", x.sha, x.short, x.subject, ""}}, items...)
		if x.sha == c.sha {
			break
		}
	}
	base := ""
	if out, err := gitRun(m.chDir(), "rev-parse", "--verify", "-q", c.sha+"^"); err == nil {
		base = strings.TrimSpace(out)
	}
	return &rebasePlan{base: base, items: items, cur: len(items) - 1}, true
}

func (m *model) startPlan(c *lcommit) tea.Cmd {
	if m.ex.logRef != "" && m.ex.logRef != m.currentBranch() {
		return m.setStatus("Switch to that branch first (Branches, space): rebase works on the branch you're on", true)
	}
	if gitState(m.chDir()) != "" {
		return m.setStatus("Finish or abort what Git is doing first (ctrl+p)", true)
	}
	p, ok := m.planFrom(c)
	if !ok {
		return nil
	}
	m.ex.plan = p
	m.ex.cur[secCommits], m.ex.off[secCommits] = len(p.items)-1, 0 // start on the commit you picked (the oldest, at the bottom)
	return m.setStatus("Plan the rebase: p s f r d set what happens, J / K move, enter runs it, esc cancels", false)
}

// the plan as shown: newest at the top like the history
func (m *model) planItems() []planItem {
	p := m.ex.plan
	out := make([]planItem, len(p.items))
	for i := range p.items {
		out[len(p.items)-1-i] = p.items[i]
	}
	return out
}

func (m *model) planKey(s string) (tea.Cmd, bool) {
	p := m.ex.plan
	if p == nil {
		return nil, false
	}
	n := len(p.items)
	shown := m.ex.cur[secCommits]
	i := n - 1 - shown // index in p.items (oldest first)
	if i < 0 || i >= n {
		return nil, false
	}
	set := func(a string) {
		p.items[i].action = a
	}
	switch s {
	case "p":
		set("pick")
	case "s":
		set("squash")
	case "f":
		set("fixup")
	case "d":
		set("drop")
	case "r":
		it := &p.items[i]
		m.prompt = &prompt{label: "New message for " + it.short + ":", text: it.subject, done: func(m *model, msg string) tea.Cmd {
			if msg != "" {
				it.action, it.msg = "reword", msg
			}
			return nil
		}}
	case "J", "ctrl+j": // move down = older
		if i > 0 {
			p.items[i], p.items[i-1] = p.items[i-1], p.items[i]
			m.ex.cur[secCommits]++
		}
	case "K", "ctrl+k": // move up = newer
		if i < n-1 {
			p.items[i], p.items[i+1] = p.items[i+1], p.items[i]
			m.ex.cur[secCommits]--
		}
	case "esc":
		m.ex.plan = nil
		return m.setStatus("Rebase cancelled: nothing changed", false), true
	case "enter":
		return m.runPlan(), true
	default:
		return nil, false
	}
	return nil, true
}

func (m *model) runPlan() tea.Cmd {
	p := m.ex.plan
	dir := m.chDir()
	if a := p.items[0].action; a == "squash" || a == "fixup" {
		return m.setStatus("The oldest commit in the plan can't be squashed: there's nothing below it to join", true)
	}
	changed, pushed := false, false
	for _, it := range p.items {
		if it.action != "pick" {
			changed = true
		}
	}
	for i, it := range p.items {
		if i < len(p.items) && it.action == "pick" && p.items[i].sha != m.planOrig(i) {
			changed = true
		}
	}
	if ll := m.llogs[m.logKey()]; ll != nil {
		for _, it := range p.items {
			for _, x := range ll.items {
				if x.sha == it.sha && !x.unpushed {
					pushed = true
				}
			}
		}
	}
	if !changed {
		m.ex.plan = nil
		return m.setStatus("Nothing to change: the plan is all pick, same order", false)
	}
	var todo strings.Builder
	for i, it := range p.items {
		switch it.action {
		case "reword":
			f := filepath.Join(os.TempDir(), fmt.Sprintf("termgit-reword-%d.txt", i))
			os.WriteFile(f, []byte(it.msg), 0o644)
			fmt.Fprintf(&todo, "pick %s\nexec git commit --amend -F '%s'\n", it.sha, filepath.ToSlash(f))
		default:
			fmt.Fprintf(&todo, "%s %s\n", it.action, it.sha)
		}
	}
	tf := filepath.Join(os.TempDir(), "termgit-rebase-todo.txt")
	os.WriteFile(tf, []byte(todo.String()), 0o644)
	label := "Run this rebase?"
	if pushed {
		label = "Run this rebase? Some of these commits are already on GitHub: you'll have to force push after (P offers it)."
	}
	base := p.base
	m.prompt = &prompt{label: label, confirm: true, done: func(m *model, _ string) tea.Cmd {
		m.ex.plan = nil
		args := []string{"rebase", "-i", "--autostash"}
		if base == "" {
			args = append(args, "--root")
		} else {
			args = append(args, base)
		}
		if m.dump {
			return m.setStatus("Would rebase with:\n"+todo.String(), false)
		}
		env := []string{"GIT_SEQUENCE_EDITOR=cp '" + filepath.ToSlash(tf) + "'", "GIT_EDITOR=true"}
		return tea.Batch(m.setStatus("Rebasing…", false), func() tea.Msg {
			out, err := gitRunEnv(dir, env, args...)
			return gitDoneMsg{dir, "Rebase done", out, err, "rebase"}
		})
	}}
	return nil
}

// the sha that was at position i when the plan started (to notice reordering)
func (m *model) planOrig(i int) string {
	ll := m.llogs[m.logKey()]
	p := m.ex.plan
	if ll == nil || p == nil {
		return ""
	}
	// the log is newest first; the plan covers the newest len(p.items) commits
	n := len(p.items)
	j := n - 1 - i
	if j < len(ll.items) {
		return ll.items[j].sha
	}
	return ""
}

// quick one-commit rebases from the Commits list
func (m *model) quickRebase(c *lcommit, action string) tea.Cmd {
	cmd := m.startPlan(c)
	p := m.ex.plan
	if p == nil {
		return cmd
	}
	last := len(p.items) - 1 // the picked commit is the oldest = index 0
	_ = last
	switch action {
	case "squash", "fixup":
		// join c into the commit below it: start one further back
		ll := m.llogs[m.logKey()]
		var below *lcommit
		for i := range ll.items {
			if ll.items[i].sha == c.sha && i+1 < len(ll.items) {
				below = &ll.items[i+1]
			}
		}
		if below == nil {
			m.ex.plan = nil
			return m.setStatus("This is the first commit: there's nothing below it to join", true)
		}
		m.ex.plan = nil
		m.startPlan(below)
		if m.ex.plan == nil {
			return nil
		}
		m.ex.plan.items[1].action = action
	case "drop":
		p.items[0].action = "drop"
	case "reword":
		it := &p.items[0]
		m.prompt = &prompt{label: "New message for " + it.short + ":", text: it.subject, done: func(m *model, msg string) tea.Cmd {
			if msg == "" || msg == it.subject {
				m.ex.plan = nil
				return m.setStatus("Message unchanged", false)
			}
			it.action, it.msg = "reword", msg
			return m.runPlan()
		}}
		return nil
	}
	return m.runPlan()
}

func (m *model) planRow(it planItem, w int, sel bool) string {
	st := lipgloss.NewStyle()
	if sel {
		st = st.Background(cDim)
	}
	ac := map[string]lipgloss.Style{"pick": fg(cMut), "squash": fg(cYellow), "fixup": fg(cOrange), "reword": fg(cAcc), "drop": fg(cRed)}[it.action]
	subj := it.subject
	if it.action == "reword" {
		subj = it.msg
	}
	sc := cTxt
	if it.action == "drop" {
		sc = cMut
	}
	left := ac.Inherit(st).Bold(true).Render(fmt.Sprintf("%-7s", it.action)) + st.Render(" ") + st.Foreground(cMut).Render(it.short) + st.Render(" ") + st.Foreground(sc).Strikethrough(it.action == "drop").Render(subj)
	return pad(left, w)
}

func (m *model) planPreview(w int) []string {
	return []string{
		fg(cAcc).Bold(true).Render("Rebase plan"),
		fg(cMut).Render("Newest at the top. Nothing happens until you press enter."), "",
		hints([][2]string{{"p", "pick: keep it"}}),
		hints([][2]string{{"s", "squash: join into the one below, keep both messages"}}),
		hints([][2]string{{"f", "fixup: join into the one below, drop its message"}}),
		hints([][2]string{{"r", "reword: change its message"}}),
		hints([][2]string{{"d", "drop: remove it"}}),
		hints([][2]string{{"J / K", "move it down (older) / up (newer)"}}), "",
		hints([][2]string{{"enter", "run the rebase"}, {"esc", "cancel"}}), "",
		fg(cMut).Render("If it stops on a conflict: fix it in Changes, then ctrl+p → Continue (or Abort)."),
		fg(cMut).Render("Your uncommitted changes are put aside and come back after."),
	}
}

// ---- remotes and worktrees (listed in Branches) ----

type gitRemote struct {
	name, url string
}

type worktree struct {
	path, branch string
	main         bool
}

func fetchRemotesWorktrees(dir string) ([]gitRemote, []worktree) {
	var rs []gitRemote
	seen := map[string]bool{}
	if out, err := gitRun(dir, "remote", "-v"); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
			f := strings.Fields(l)
			if len(f) >= 2 && !seen[f[0]] {
				seen[f[0]] = true
				rs = append(rs, gitRemote{f[0], f[1]})
			}
		}
	}
	var ws []worktree
	if out, err := gitRun(dir, "worktree", "list", "--porcelain"); err == nil {
		var cur *worktree
		for _, l := range strings.Split(out, "\n") {
			switch {
			case strings.HasPrefix(l, "worktree "):
				ws = append(ws, worktree{path: filepath.FromSlash(strings.TrimPrefix(l, "worktree "))})
				cur = &ws[len(ws)-1]
				cur.main = len(ws) == 1
			case strings.HasPrefix(l, "branch ") && cur != nil:
				cur.branch = strings.TrimPrefix(strings.TrimPrefix(l, "branch "), "refs/heads/")
			case l == "detached" && cur != nil:
				cur.branch = "(no branch)"
			}
		}
	}
	return rs, ws
}

func (m *model) remoteKey(s string) (tea.Cmd, bool) {
	dir := m.chDir()
	switch s {
	case "A":
		m.prompt = &prompt{label: "New remote name:", text: "upstream", done: func(m *model, name string) tea.Cmd {
			if name == "" {
				return nil
			}
			m.prompt = &prompt{label: "Its URL (https://github.com/owner/repo.git):", done: func(m *model, url string) tea.Cmd {
				if url == "" {
					return nil
				}
				return m.git(dir, "Added remote "+name, "remote", "remote", "add", name, url)
			}}
			return nil
		}}
		return nil, true
	}
	it, ok := m.exSel()
	if !ok || it.kind != "remote" {
		return nil, false
	}
	r := it.rm
	switch s {
	case "f":
		return tea.Batch(m.setStatus("Fetching "+r.name+"…", false), m.git(dir, "Fetched "+r.name, "fetch", "fetch", r.name, "--prune")), true
	case "R":
		m.prompt = &prompt{label: "Rename remote " + r.name + " to:", text: r.name, done: func(m *model, n string) tea.Cmd {
			if n == "" || n == r.name {
				return nil
			}
			return m.git(dir, "Renamed to "+n, "remote", "remote", "rename", r.name, n)
		}}
		return nil, true
	case "E":
		m.prompt = &prompt{label: "URL of " + r.name + ":", text: r.url, done: func(m *model, u string) tea.Cmd {
			if u == "" || u == r.url {
				return nil
			}
			return m.git(dir, "Changed the URL of "+r.name, "remote", "remote", "set-url", r.name, u)
		}}
		return nil, true
	case "D", "d":
		m.prompt = &prompt{label: "Remove the remote " + r.name + "? (only the link on this PC; nothing on GitHub is deleted)", confirm: true,
			done: func(m *model, _ string) tea.Cmd {
				return m.git(dir, "Removed "+r.name, "remote", "remote", "remove", r.name)
			}}
		return nil, true
	}
	return nil, false
}

func (m *model) worktreeKey(s string) (tea.Cmd, bool) {
	dir := m.chDir()
	if s == "w" {
		if b := m.selLBranch(); b != nil && !b.tag {
			name := b.name
			if b.remote {
				name = name[strings.Index(name, "/")+1:]
			}
			def := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-"+strings.ReplaceAll(name, "/", "-"))
			m.prompt = &prompt{label: "Open " + name + " in its own folder (worktree) at:", text: def, done: func(m *model, p string) tea.Cmd {
				if p == "" {
					return nil
				}
				args := []string{"worktree", "add", p, name}
				if b.remote {
					args = []string{"worktree", "add", "--track", "-b", name, p, b.name}
				}
				return m.git(dir, "Made a worktree for "+name+" at "+tildePath(p)+" (e opens it in Nvim)", "worktree", args...)
			}}
			return nil, true
		}
	}
	it, ok := m.exSel()
	if !ok || it.kind != "worktree" {
		return nil, false
	}
	wt := it.wt
	switch s {
	case "e", "enter":
		return m.runApp("nvim", wt.path), true
	case "L":
		return m.runApp("lazygit", wt.path), true
	case "D", "d":
		if wt.main {
			return m.setStatus("That's the main folder of the repo: it can't be removed here", true), true
		}
		m.prompt = &prompt{label: "Remove the worktree " + tildePath(wt.path) + "? (its branch stays)", confirm: true,
			done: func(m *model, _ string) tea.Cmd {
				return m.git(dir, "Removed the worktree", "worktree", "worktree", "remove", wt.path)
			}}
		return nil, true
	}
	return nil, false
}

func (m *model) remoteRow(r *gitRemote, w int, sel bool) string {
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
	return pad(st.Foreground(cAcc).Render(icGlobe)+st.Render(" ")+st.Foreground(nc).Render(r.name)+st.Render("  ")+st.Foreground(cMut).Render(r.url), w)
}

func (m *model) worktreeRow(t *worktree, w int, sel bool) string {
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
	label := t.branch
	if t.main {
		label += " (main folder)"
	}
	return pad(st.Foreground(cOrange).Render(icFolder)+st.Render(" ")+st.Foreground(nc).Render(label)+st.Render("  ")+st.Foreground(cMut).Render(tildePath(t.path)), w)
}

func (m *model) remotePreview(r *gitRemote, w int) []string {
	return []string{fg(cAcc).Bold(true).Render(icGlobe + " remote " + r.name), fg(cMut).Render(r.url), "",
		fg(cMut).Render("A remote is a link to a copy of the repo somewhere else (origin = yours on GitHub)."), "",
		hints([][2]string{{"f", "fetch it"}, {"E", "change URL"}, {"R", "rename"}, {"D", "remove"}, {"A", "add another"}})}
}

func (m *model) worktreePreview(t *worktree, w int) []string {
	out := []string{fg(cAcc).Bold(true).Render(icFolder + " worktree: " + t.branch), fg(cMut).Render(t.path), "",
		fg(cMut).Render("A worktree is a second folder of the same repo with another branch checked out,"),
		fg(cMut).Render("so you can work on two branches at once without stashing."), ""}
	h := [][2]string{{"e", "open in Nvim"}, {"L", "lazygit"}}
	if !t.main {
		h = append(h, [2]string{"D", "remove"})
	}
	return append(out, hints(h), "", fg(cMut).Render("w on a branch makes a new one."))
}
