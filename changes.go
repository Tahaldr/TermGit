package main

// The Changes section (repos on this PC): what you changed, the diff, stage / unstage /
// discard, commit (message in the bottom bar or in Nvim), amend, pull / push / fetch.
// Keys follow lazygit: space stage, a all, d discard, c commit, A amend, p pull, P push, f fetch.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type change struct {
	x, y byte // staged / not-staged status, like git status --short
	path string
	orig string // renamed from
}

func (c *change) staged() bool    { return c.x != ' ' && c.x != '?' }
func (c *change) unstaged() bool  { return c.y != ' ' }
func (c *change) untracked() bool { return c.x == '?' }
func (c *change) conflict() bool {
	return c.x == 'U' || c.y == 'U' || (c.x == 'A' && c.y == 'A') || (c.x == 'D' && c.y == 'D')
}

type changeList struct {
	items []change
	err   string
}

type fileDiff struct {
	staged, unstaged string
	newFile          string // untracked: the whole file
	binary           bool
	err              string
}

func gitRun(dir string, args ...string) (string, error) {
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	out, err := c.CombinedOutput()
	return string(out), err
}

func fetchChanges(dir string) func() func(*model) {
	return func() func(*model) {
		cl := &changeList{}
		out, err := gitRun(dir, "status", "--porcelain=v1", "-z", "-uall")
		if err != nil {
			cl.err = strings.TrimSpace(out)
		}
		parts := strings.Split(out, "\x00")
		for i := 0; i < len(parts); i++ {
			e := parts[i]
			if len(e) < 4 {
				continue
			}
			c := change{x: e[0], y: e[1], path: e[3:]}
			if c.x == 'R' || c.x == 'C' {
				if i+1 < len(parts) {
					c.orig = parts[i+1]
					i++
				}
			}
			cl.items = append(cl.items, c)
		}
		sort.Slice(cl.items, func(i, j int) bool { return strings.ToLower(cl.items[i].path) < strings.ToLower(cl.items[j].path) })
		return func(m *model) { m.changes[dir] = cl }
	}
}

func fetchFileDiff(dir string, c change) func() func(*model) {
	return func() func(*model) {
		fd := &fileDiff{}
		if c.untracked() {
			data, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(c.path)))
			switch {
			case err != nil:
				fd.err = err.Error()
			case strings.ContainsRune(string(data[:min(len(data), 8000)]), 0):
				fd.binary = true
			default:
				fd.newFile = string(data)
			}
		} else {
			fd.unstaged, _ = gitRun(dir, "diff", "--no-color", "--", c.path)
			fd.staged, _ = gitRun(dir, "diff", "--no-color", "--cached", "--", c.path)
		}
		key := dir + "|" + c.path
		return func(m *model) { m.diffs[key] = fd }
	}
}

// ---- in the explorer ----

func (m *model) chDir() string {
	if ex := m.exActive(); ex != nil {
		return m.localPath(&ex.repo)
	}
	return ""
}

func (m *model) chSel() *change {
	if ex := m.exActive(); ex != nil && ex.sec == secChanges {
		if it, ok := m.exSel(); ok && it.kind == "change" {
			return it.ch
		}
	}
	return nil
}

// forget what we know about this folder so it's read again
func (m *model) chForget(dir string) {
	if dir == "" {
		return
	}
	for _, k := range []string{"changes:", "stash:", "lbranches:"} {
		delete(m.loaded, k+dir)
	}
	for k := range m.loaded {
		if strings.HasPrefix(k, "gitdiff:"+dir+"|") || strings.HasPrefix(k, "llog:"+dir+"|") || strings.HasPrefix(k, "show:"+dir+"|stash@") {
			delete(m.loaded, k)
		}
	}
	for k := range m.shows {
		if strings.HasPrefix(k, dir+"|stash@") {
			delete(m.shows, k) // stash numbers shift when one is added or removed
		}
	}
}

type gitDoneMsg struct {
	dir, ok string
	out     string
	err     error
	what    string // push / pull / commit…: to explain a failure
}

// git runs a git command in the background, then refreshes the Changes list
func (m *model) git(dir, ok, what string, args ...string) tea.Cmd {
	if m.dump {
		return m.setStatus("Would run git "+strings.Join(args, " "), false)
	}
	return func() tea.Msg {
		out, err := gitRun(dir, args...)
		return gitDoneMsg{dir, ok, out, err, what}
	}
}

func (m *model) onGitDone(msg gitDoneMsg) tea.Cmd {
	m.chForget(msg.dir)
	cmds := []tea.Cmd{m.scanLocalInfo()}
	if m.exActive() != nil {
		cmds = append(cmds, m.exLoad())
	}
	out := strings.TrimSpace(msg.out)
	if msg.err == nil {
		return tea.Batch(append(cmds, m.setStatus(msg.ok, false))...)
	}
	if strings.Contains(out, "CONFLICT") {
		return tea.Batch(append(cmds, m.setStatus("Conflicts: fix the files marked U in Changes (e opens Nvim), stage them, then ctrl+p → Continue (or Abort)", true))...)
	}
	if strings.HasPrefix(msg.what, "branchdel:") && strings.Contains(out, "not fully merged") {
		name, dir := strings.TrimPrefix(msg.what, "branchdel:"), msg.dir
		m.prompt = &prompt{label: name + " has commits that aren't merged anywhere. Delete it anyway? They'd be lost.", confirm: true,
			done: func(m *model, _ string) tea.Cmd {
				return m.git(dir, "Deleted "+name, "branchdel", "branch", "-D", name)
			}}
		return tea.Batch(cmds...)
	}
	switch msg.what {
	case "push":
		switch {
		case strings.Contains(out, "has no upstream"):
			// first push of this branch: link it to GitHub
			return tea.Batch(append(cmds, m.setStatus("Pushing a new branch…", false),
				m.git(msg.dir, "Pushed the new branch to GitHub", "push-new", "push", "-u", "origin", "HEAD"))...)
		case strings.Contains(out, "rejected") || strings.Contains(out, "fetch first") || strings.Contains(out, "non-fast-forward"):
			dir := msg.dir
			m.prompt = &prompt{label: "GitHub has commits you don't. Pull first (n), or force push and overwrite them (y)?", confirm: true,
				no: func(m *model) tea.Cmd { return m.chPull() },
				done: func(m *model, _ string) tea.Cmd {
					return m.git(dir, "Force pushed", "push", "push", "--force-with-lease")
				}}
			return tea.Batch(cmds...)
		}
	case "commit":
		if strings.Contains(out, "nothing to commit") || strings.Contains(out, "no changes added") {
			return tea.Batch(append(cmds, m.setStatus("Nothing staged to commit (space stages a file, a stages all)", true))...)
		}
	case "pull":
		why := lastLine(out)
		switch {
		case strings.Contains(out, "would be overwritten"):
			why = "your uncommitted changes touch the same files: commit them first (c)"
		case strings.Contains(out, "Not possible to fast-forward") || strings.Contains(out, "diverging"):
			why = "you and GitHub both have new commits: they need to be combined (merge / rebase comes in the next batch, or lazygit L)"
		case strings.Contains(out, "no tracking information"):
			why = "this branch isn't on GitHub yet: push it (P)"
		}
		return tea.Batch(append(cmds, m.setStatus("Couldn't pull: "+why, true))...)
	}
	return tea.Batch(append(cmds, m.setStatus(lastLine(out), true))...)
}

func (m *model) chKey(s string) (tea.Cmd, bool) {
	dir := m.chDir()
	if dir == "" {
		return nil, false
	}
	c := m.chSel()
	switch s {
	case "space":
		if c == nil {
			return nil, true
		}
		if c.unstaged() || c.untracked() {
			return m.git(dir, "Staged "+c.path, "stage", "add", "--", c.path), true
		}
		return m.git(dir, "Unstaged "+c.path, "unstage", "restore", "--staged", "--", c.path), true
	case "a":
		cl := m.changes[dir]
		if cl == nil || len(cl.items) == 0 {
			return m.setStatus("Nothing changed", false), true
		}
		for _, x := range cl.items {
			if x.unstaged() || x.untracked() {
				return m.git(dir, "Staged everything", "stage", "add", "-A"), true
			}
		}
		return m.git(dir, "Unstaged everything", "unstage", "reset", "-q"), true
	case "d":
		if c == nil {
			return nil, true
		}
		path := c.path
		label := "Throw away your changes to " + path + "? This can't be undone."
		args := []string{"restore", "--staged", "--worktree", "--", path}
		if c.untracked() {
			label = "Delete the new file " + path + "? This can't be undone."
			args = []string{"clean", "-f", "--", path}
		} else if c.x == 'A' {
			label = "Remove the new file " + path + "? This can't be undone."
			args = []string{"rm", "-f", "--", path}
		}
		m.prompt = &prompt{label: label, confirm: true, done: func(m *model, _ string) tea.Cmd {
			return m.git(dir, "Discarded "+path, "discard", args...)
		}}
		return nil, true
	case "c":
		return m.chCommit(false), true
	case "C":
		return m.chCommit(true), true
	case "A":
		return m.chAmend(), true
	case "p":
		return m.chPull(), true
	case "P":
		return m.chPush(), true
	case "f":
		return tea.Batch(m.setStatus("Fetching…", false), m.git(dir, "Fetched from GitHub", "fetch", "fetch", "--all", "--prune")), true
	}
	return nil, false
}

func (m *model) stagedCount(dir string) (staged, other int) {
	if cl := m.changes[dir]; cl != nil {
		for _, c := range cl.items {
			if c.staged() {
				staged++
			} else {
				other++
			}
		}
	}
	return
}

// c: message in the bottom bar; C: message in Nvim
func (m *model) chCommit(nvim bool) tea.Cmd {
	dir := m.chDir()
	staged, other := m.stagedCount(dir)
	do := func(m *model, all bool) tea.Cmd {
		commit := func(m *model, msg string) tea.Cmd {
			if strings.TrimSpace(msg) == "" {
				return m.setStatus("No message: nothing committed", false)
			}
			f := filepath.Join(os.TempDir(), "termgit-commit.txt")
			os.WriteFile(f, []byte(msg), 0o644)
			if m.dump {
				return m.setStatus("Would commit: "+firstLine(msg), false)
			}
			return func() tea.Msg {
				if all {
					if out, err := gitRun(dir, "add", "-A"); err != nil {
						return gitDoneMsg{dir, "", out, err, "stage"}
					}
				}
				out, err := gitRun(dir, "commit", "-F", f)
				return gitDoneMsg{dir, "Committed: " + firstLine(msg), out, err, "commit"}
			}
		}
		if nvim {
			return m.editInNvim("commit", "<!-- The first line is the title. Leave a blank line, then details if you want. Save and quit (:wq) to commit; leave it empty to cancel. -->\n\n", commit)
		}
		m.prompt = &prompt{label: "Commit message:", done: commit}
		return nil
	}
	switch {
	case staged == 0 && other == 0:
		return m.setStatus("Nothing to commit: no changes", false)
	case staged == 0:
		m.prompt = &prompt{label: fmt.Sprintf("Nothing is staged. Stage all %d changes and commit them?", other), confirm: true,
			done: func(m *model, _ string) tea.Cmd { return do(m, true) }}
		return nil
	}
	return do(m, false)
}

func firstLine(s string) string {
	return strings.TrimSpace(strings.SplitN(strings.TrimSpace(s), "\n", 2)[0])
}

// A: add the staged changes to the last commit (y keeps its message, n edits it in Nvim)
func (m *model) chAmend() tea.Cmd {
	dir := m.chDir()
	last, _ := gitRun(dir, "log", "-1", "--format=%B")
	last = strings.TrimSpace(last)
	if last == "" {
		return m.setStatus("No commit to amend yet", true)
	}
	m.prompt = &prompt{label: "Amend \"" + firstLine(last) + "\"? y = keep its message, n = edit it in Nvim", confirm: true,
		done: func(m *model, _ string) tea.Cmd {
			return m.git(dir, "Amended the last commit", "commit", "commit", "--amend", "--no-edit")
		},
		no: func(m *model) tea.Cmd {
			return m.editInNvim("commit", last+"\n", func(m *model, msg string) tea.Cmd {
				if msg == "" {
					return m.setStatus("Empty message: not amended", false)
				}
				f := filepath.Join(os.TempDir(), "termgit-commit.txt")
				os.WriteFile(f, []byte(msg), 0o644)
				return m.git(dir, "Amended: "+firstLine(msg), "commit", "commit", "--amend", "-F", f)
			})
		}}
	return nil
}

func (m *model) chPull() tea.Cmd {
	dir := m.chDir()
	if dir == "" {
		return m.actPull()
	}
	return tea.Batch(m.setStatus("Pulling…", false), m.git(dir, "Pulled from GitHub", "pull", "pull", "--ff-only"))
}

func (m *model) chPush() tea.Cmd {
	dir := m.chDir()
	if dir == "" {
		return m.actPush()
	}
	return tea.Batch(m.setStatus("Pushing…", false), m.git(dir, "Pushed to GitHub", "push", "push"))
}

// P from the repo list (or a repo's other sections)
func (m *model) actPush() tea.Cmd {
	r := m.curRepo()
	if r == nil {
		return nil
	}
	dir := m.localPath(r)
	if dir == "" {
		return m.setStatus(r.Name+" isn't on this PC", true)
	}
	li := m.linfo[strings.ToLower(r.NameWithOwner)]
	label := "Push " + r.Name + " to GitHub?"
	if li.ahead > 0 {
		label = fmt.Sprintf("Push %d %s of %s to GitHub?", li.ahead, plural(li.ahead, "commit", "commits"), r.Name)
	}
	m.prompt = &prompt{label: label, confirm: true, done: func(m *model, _ string) tea.Cmd {
		return tea.Batch(m.setStatus("Pushing…", false), m.git(dir, "Pushed "+r.Name, "push", "push"))
	}}
	return nil
}

// ---- drawing ----

func statusLetter(b byte, staged bool) string {
	if b == ' ' {
		return " "
	}
	c := cYellow
	switch {
	case b == '?':
		c = cGreen
	case b == 'U':
		c = cRed
	case staged:
		c = cGreen
	case b == 'D':
		c = cRed
	}
	return fg(c).Render(string(b))
}

func (m *model) changeRow(c *change, w int, sel bool) string {
	st := lipgloss.NewStyle()
	if sel {
		bg := cChip
		if m.focus == 0 {
			bg = cDim
		}
		st = st.Background(bg)
	}
	x, y := c.x, c.y
	if c.untracked() {
		x, y = ' ', '?'
	}
	marks := statusLetter(x, true) + statusLetter(y, false)
	name := filepath.Base(c.path)
	dir := filepath.Dir(filepath.FromSlash(c.path))
	nc := cTxt
	if sel {
		nc = cAcc
	}
	if c.staged() && !c.unstaged() {
		nc = cGreen
	}
	left := marks + st.Render(" ") + st.Foreground(nc).Bold(sel).Render(name)
	if dir != "." {
		left += st.Foreground(cMut).Render("  " + dir)
	}
	return pad(left, w)
}

func gitDiffLines(diff string, w int) []string {
	var out []string
	inHead := true
	for _, l := range strings.Split(strings.TrimRight(strings.ReplaceAll(diff, "\t", "    "), "\n"), "\n") {
		if strings.HasPrefix(l, "diff --git") {
			inHead = true
			continue
		}
		if inHead {
			if strings.HasPrefix(l, "@@") {
				inHead = false
			} else {
				continue
			}
		}
		switch {
		case strings.HasPrefix(l, "@@"):
			out = append(out, fg(cAcc).Faint(true).Render(l))
		case strings.HasPrefix(l, "+"):
			out = append(out, fg(cGreen).Render(l))
		case strings.HasPrefix(l, "-"):
			out = append(out, fg(cRed).Render(l))
		case strings.HasPrefix(l, `\`):
			out = append(out, fg(cMut).Render(l))
		default:
			out = append(out, fg(cTxt).Render(l))
		}
	}
	return out
}

func (m *model) changePreview(c *change, w int) []string {
	dir := m.chDir()
	out := []string{fg(cAcc).Bold(true).Render(c.path)}
	var what []string
	switch {
	case c.conflict():
		what = append(what, fg(cRed).Render("conflict: open it in Nvim (e) and fix it"))
	case c.untracked():
		what = append(what, fg(cGreen).Render("new file, not tracked yet"))
	default:
		if c.staged() {
			what = append(what, fg(cGreen).Render("staged"))
		}
		if c.unstaged() {
			what = append(what, fg(cYellow).Render("not staged"))
		}
	}
	if c.orig != "" {
		what = append(what, fg(cMut).Render("renamed from "+c.orig))
	}
	out = append(out, strings.Join(what, fg(cMut).Render(" · ")))
	toggle := "stage"
	if c.staged() && !c.unstaged() {
		toggle = "unstage"
	}
	out = append(out, hints([][2]string{{"space", toggle}, {"d", "discard"}, {"c", "commit"}, {"e", "edit in Nvim"}}), sepLine(w))
	fd := m.diffs[dir+"|"+c.path]
	switch {
	case fd == nil:
		return append(out, fg(cMut).Render("Loading…"))
	case fd.err != "":
		return append(out, fg(cRed).Render(fd.err))
	case fd.binary:
		return append(out, fg(cMut).Render("A binary file."))
	case c.untracked():
		lines := highlight(filepath.Base(c.path), fd.newFile)
		if len(lines) > 3000 {
			lines = append(lines[:3000], fg(cMut).Render("… (cut here)"))
		}
		return append(out, lines...)
	}
	// every diff line remembers where it comes from, so j / k / space can stage single lines
	m.chMap = make([]diffLine, len(out))
	side := func(name, title string, st lipgloss.Style, diff string) {
		if strings.TrimSpace(diff) == "" {
			return
		}
		out = append(out, st.Bold(true).Render(title), "")
		m.chMap = append(m.chMap, diffLine{}, diffLine{})
		for h, hk := range parseDiff(diff).hunks {
			out = append(out, fg(cAcc).Faint(true).Render(hk.header))
			m.chMap = append(m.chMap, diffLine{name, h, -1})
			for i, l := range hk.lines {
				l = strings.ReplaceAll(l, "\t", "    ")
				ls := fg(cTxt)
				switch {
				case strings.HasPrefix(l, "+"):
					ls = fg(cGreen)
				case strings.HasPrefix(l, "-"):
					ls = fg(cRed)
				case strings.HasPrefix(l, "\\"):
					ls = fg(cMut)
				}
				sel := m.focus == 1 && len(m.chMap) == m.chCur
				if sel {
					ls = ls.Background(cDim)
					l = pad(l, w)
				}
				out = append(out, ls.Render(l))
				mark := diffLine{name, h, i}
				if !strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "-") {
					mark.side = "" // context lines can't be staged on their own
				}
				m.chMap = append(m.chMap, mark)
			}
		}
		out = append(out, "")
		m.chMap = append(m.chMap, diffLine{})
	}
	side("unstaged", "Not staged", fg(cYellow), fd.unstaged)
	side("staged", "Staged", fg(cGreen), fd.staged)
	if m.focus == 1 {
		out[2] = hints([][2]string{{"j/k", "pick a line"}, {"space", "stage / unstage it"}, {"a", "the whole hunk"}, {"esc", "back"}})
	}
	if strings.TrimSpace(fd.unstaged+fd.staged) == "" {
		out = append(out, fg(cMut).Render("No line changes (mode or rename only)."))
	}
	return out
}

// the Changes list's bottom note: branch, ahead / behind, staged count
func (m *model) changesNote() string {
	ex := m.ex
	li := m.linfo[strings.ToLower(ex.repo.NameWithOwner)]
	staged, other := m.stagedCount(m.chDir())
	note := icBranch + " " + li.branch
	if li.ahead > 0 {
		note += fmt.Sprintf(" ↑%d", li.ahead)
	}
	if li.missing {
		note += " ↓"
	} else if li.behind > 0 {
		note += fmt.Sprintf(" ↓%d", li.behind)
	}
	if staged+other > 0 {
		note += fmt.Sprintf(" · %d staged · %d not", staged, other)
	}
	return note
}

func (m *model) chButtons() []button {
	if m.exActive() == nil || m.ex.sec != secChanges || m.chDir() == "" {
		return nil
	}
	var b []button
	if c := m.chSel(); c != nil {
		if c.staged() && !c.unstaged() {
			b = append(b, button{icCheck + " Unstage", "space", "stage"})
		} else {
			b = append(b, button{icCheck + " Stage", "space", "stage"})
		}
		b = append(b, button{icClose + " Discard", "d", "discard"})
	}
	b = append(b, button{"Stage all", "a", "stageall"}, button{icCommit + " Commit", "c", "commit"},
		button{icDown + " Pull", "p", "pull"}, button{icUp + " Push", "P", "push"}, button{icNvim + " Nvim", "e", "nvim"})
	return b
}

// the first line in the diff panel that can be staged
func (m *model) firstDiffLine() int {
	for i, d := range m.chMap {
		if d.side != "" && d.line >= 0 {
			return i
		}
	}
	return 0
}
