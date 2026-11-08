package main

// The repo explorer: open a repo (enter) to browse its code, commits and branches,
// like the repo page on github.com.

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

const (
	secCode = iota
	secChanges
	secIssues
	secPRs
	secCommits
	secBranches
	secActions
	secReleases
)

var secNames = []string{"Code", "Changes", "Issues", "PRs", "Commits", "Branches", "Actions", "Releases"}
var secIcons = []string{icCode, icChanges, icIssue, icPR, icCommit, icBranch, icActions, icTag}

type explorer struct {
	repo      Repo
	ref       string // branch being viewed
	refOid    string
	sec       int
	dir       string // folder shown in Code ("" = top)
	cur, off  [8]int
	query     [8]string
	filtering bool
	dirCur    map[string]int // where the cursor was in each folder, for going back
	logRef    string         // local repo: whose commits the Commits section shows ("" = the current branch)
	plan      *rebasePlan    // an interactive rebase being planned in Commits
}

type exItem struct {
	kind    string // up, dir, file, sub, commit, branch
	ent     treeEntry
	commit  *Commit
	branch  *Branch
	run     *Run
	release *Release
	issue   *Issue
	pr      *PR
	ch      *change
	lb      *lbranch
	lc      *lcommit
	st      *stashEntry
	rm      *gitRemote
	wt      *worktree
	plan    *planItem
}

type appDoneMsg struct {
	app string
	err error
}

func (m *model) exActive() *explorer {
	if m.tab == tabRepos && m.ex != nil {
		return m.ex
	}
	return nil
}

// curRepo is the repo the buttons and keys act on
func (m *model) curRepo() *Repo {
	if ex := m.exActive(); ex != nil {
		return &ex.repo
	}
	return m.selected()
}

func (m *model) openRepo() tea.Cmd {
	r := m.selected()
	if r == nil {
		return nil
	}
	ex, ok := m.exSaved[r.NameWithOwner]
	if !ok {
		ex = &explorer{repo: *r, ref: r.Branch(), refOid: r.HeadOid(), dirCur: map[string]int{}}
	}
	m.ex, m.focus, m.help = ex, 0, false
	return m.exSelected()
}

func (m *model) leaveRepo() tea.Cmd {
	if m.ex != nil {
		m.exSaved[m.ex.repo.NameWithOwner] = m.ex
	}
	m.ex, m.focus = nil, 0
	return m.onSelect()
}

func (m *model) treeKey() string { return m.ex.repo.NameWithOwner + "@" + m.ex.ref }

// ---- the list on the left ----

func (m *model) exItems() []exItem {
	ex := m.ex
	q := strings.ToLower(ex.query[ex.sec])
	var out []exItem
	switch ex.sec {
	case secCode:
		td := m.trees[m.treeKey()]
		if td == nil {
			return nil
		}
		if q != "" { // search every file in the repo by its path
			for _, e := range td.ents {
				if e.Type == "blob" && fuzzy(strings.ToLower(e.Path), q) {
					out = append(out, exItem{kind: "file", ent: e})
					if len(out) >= 500 {
						break
					}
				}
			}
			return out
		}
		if ex.dir != "" {
			out = append(out, exItem{kind: "up"})
		}
		for _, e := range td.children[ex.dir] {
			k := "file"
			switch e.Type {
			case "tree":
				k = "dir"
			case "commit":
				k = "sub"
			}
			out = append(out, exItem{kind: k, ent: e})
		}
	case secCommits:
		if dir := m.localPath(&ex.repo); dir != "" && ex.plan != nil {
			items := m.planItems()
			for i := range items {
				out = append(out, exItem{kind: "plan", plan: &items[i]})
			}
			return out
		}
		if dir := m.localPath(&ex.repo); dir != "" {
			if ll := m.llogs[dir+"|"+ex.logRef]; ll != nil {
				for i := range ll.items {
					c := &ll.items[i]
					if q == "" || fuzzy(strings.ToLower(c.subject+" "+c.author+" "+c.short+" "+c.refs), q) {
						out = append(out, exItem{kind: "lcommit", lc: c})
					}
				}
			}
			return out
		}
		if cl := m.clists[m.treeKey()]; cl != nil {
			for i := range cl.items {
				c := &cl.items[i]
				if q == "" || fuzzy(strings.ToLower(c.Headline+" "+c.who()+" "+c.Short), q) {
					out = append(out, exItem{kind: "commit", commit: c})
				}
			}
		}
	case secChanges:
		dir := m.localPath(&ex.repo)
		if cl := m.changes[dir]; cl != nil {
			for i := range cl.items {
				c := &cl.items[i]
				if q == "" || fuzzy(strings.ToLower(c.path), q) {
					out = append(out, exItem{kind: "change", ch: c})
				}
			}
		}
		st := m.stashes[dir]
		for i := range st {
			if q == "" || fuzzy(strings.ToLower(st[i].msg), q) {
				out = append(out, exItem{kind: "stash", st: &st[i]})
			}
		}
	case secIssues:
		if il := m.issueLists[m.exIssueQuery()]; il != nil {
			for i := range il.items {
				is := &il.items[i]
				if q == "" || fuzzy(strings.ToLower(is.Title+fmt.Sprintf(" #%d", is.Number)), q) {
					out = append(out, exItem{kind: "issue", issue: is})
				}
			}
		}
	case secPRs:
		if pl := m.prLists[m.exPRQuery()]; pl != nil {
			for i := range pl.items {
				p := &pl.items[i]
				if q == "" || fuzzy(strings.ToLower(p.Title+" "+p.Head+fmt.Sprintf(" #%d", p.Number)), q) {
					out = append(out, exItem{kind: "pr", pr: p})
				}
			}
		}
	case secActions:
		if rl := m.runs[ex.repo.NameWithOwner]; rl != nil {
			for i := range rl.items {
				r := &rl.items[i]
				if q == "" || fuzzy(strings.ToLower(r.Name+" "+r.Title+" "+r.Branch), q) {
					out = append(out, exItem{kind: "run", run: r})
				}
			}
		}
	case secReleases:
		if rl := m.releases[ex.repo.NameWithOwner]; rl != nil {
			for i := range rl.items {
				r := &rl.items[i]
				if q == "" || fuzzy(strings.ToLower(r.title()+" "+r.Tag), q) {
					out = append(out, exItem{kind: "release", release: r})
				}
			}
		}
	case secBranches:
		if dir := m.localPath(&ex.repo); dir != "" {
			if bl := m.lbranches[dir]; bl != nil {
				for i := range bl.items {
					b := &bl.items[i]
					if q == "" || fuzzy(strings.ToLower(b.name), q) {
						out = append(out, exItem{kind: "lbranch", lb: b})
					}
				}
				for i := range bl.remotes {
					if q == "" || fuzzy(strings.ToLower(bl.remotes[i].name+" "+bl.remotes[i].url), q) {
						out = append(out, exItem{kind: "remote", rm: &bl.remotes[i]})
					}
				}
				if len(bl.worktrees) > 1 { // only worth showing when there's more than the main folder
					for i := range bl.worktrees {
						out = append(out, exItem{kind: "worktree", wt: &bl.worktrees[i]})
					}
				}
			}
			return out
		}
		if bl := m.blists[ex.repo.NameWithOwner]; bl != nil {
			for i := range bl.items {
				b := &bl.items[i]
				if q == "" || fuzzy(strings.ToLower(b.Name), q) {
					out = append(out, exItem{kind: "branch", branch: b})
				}
			}
		}
	}
	return out
}

func (m *model) exSel() (exItem, bool) {
	items := m.exItems()
	c := m.ex.cur[m.ex.sec]
	if c < 0 || c >= len(items) {
		return exItem{}, false
	}
	return items[c], true
}

func (m *model) exRows() int {
	n := m.listRows()
	if m.ex.filtering || m.ex.query[m.ex.sec] != "" {
		n--
	}
	return n
}

func (m *model) exClamp() {
	ex := m.ex
	s := ex.sec
	n := len(m.exItems())
	if ex.cur[s] >= n {
		ex.cur[s] = n - 1
	}
	if ex.cur[s] < 0 {
		ex.cur[s] = 0
	}
	rows := m.exRows()
	if rows < 1 {
		return
	}
	if ex.cur[s] < ex.off[s] {
		ex.off[s] = ex.cur[s]
	}
	if ex.cur[s] >= ex.off[s]+rows {
		ex.off[s] = ex.cur[s] - rows + 1
	}
	if ex.off[s] < 0 {
		ex.off[s] = 0
	}
}

// exLoad fetches what the current section and the highlighted item need
func (m *model) exLoad() tea.Cmd {
	ex := m.ex
	nwo := ex.repo.NameWithOwner
	if ex.ref == "" {
		return nil // empty repo
	}
	var cmds []tea.Cmd
	switch ex.sec {
	case secCode:
		oid := ""
		if strings.Contains(ex.ref, "/") {
			oid = ex.refOid // branch names with a slash are fetched by commit id
		}
		cmds = append(cmds, m.load("tree:"+m.treeKey(), false, fetchTree(nwo, ex.ref, oid)))
	case secCommits:
		if dir := m.localPath(&ex.repo); dir != "" {
			cmds = append(cmds, m.load("llog:"+dir+"|"+ex.logRef, false, fetchLog(dir, ex.logRef)))
		} else {
			cmds = append(cmds, m.load("commits:"+m.treeKey(), false, fetchCommits(nwo, ex.ref)))
		}
	case secBranches:
		if dir := m.localPath(&ex.repo); dir != "" {
			cmds = append(cmds, m.load("lbranches:"+dir, false, fetchLBranches(dir)))
		} else {
			cmds = append(cmds, m.load("branches:"+nwo, false, fetchBranches(nwo, ex.repo.Branch())))
		}
	case secChanges:
		if dir := m.localPath(&ex.repo); dir != "" {
			cmds = append(cmds, m.load("changes:"+dir, false, fetchChanges(dir)), m.load("stash:"+dir, false, fetchStash(dir)))
		}
	case secIssues:
		q := m.exIssueQuery()
		cmds = append(cmds, m.load("issues:"+q, false, fetchIssues(q)))
	case secPRs:
		q := m.exPRQuery()
		cmds = append(cmds, m.load("prs:"+q, false, fetchPRs(q)))
	case secActions:
		cmds = append(cmds, m.load("runs:"+nwo, false, fetchRuns(nwo)))
	case secReleases:
		cmds = append(cmds, m.load("releases:"+nwo, false, fetchReleases(nwo)))
	}
	if it, ok := m.exSel(); ok {
		switch it.kind {
		case "file":
			if it.ent.Size <= maxFile && !isImage(it.ent.Path) {
				cmds = append(cmds, m.load("blob:"+it.ent.Sha, true, fetchBlob(nwo, it.ent.Sha)))
			}
		case "dir":
			if rd := m.dirReadme(it.ent.Path); rd != nil {
				cmds = append(cmds, m.load("blob:"+rd.Sha, true, fetchBlob(nwo, rd.Sha)))
			}
		case "commit":
			cmds = append(cmds, m.load("commit:"+nwo+"@"+it.commit.Oid, true, fetchCommitDetail(nwo, it.commit.Oid)))
		case "run":
			cmds = append(cmds, m.loadJobs(it.run, true))
		case "issue":
			cmds = append(cmds, m.loadComments(it.issue, true))
		case "change":
			dir := m.localPath(&ex.repo)
			cmds = append(cmds, m.load("gitdiff:"+dir+"|"+it.ch.path, true, fetchFileDiff(dir, *it.ch)))
		case "lbranch":
			dir := m.localPath(&ex.repo)
			if !it.lb.tag {
				cmds = append(cmds, m.load("llog:"+dir+"|"+it.lb.name, true, fetchLog(dir, it.lb.name)))
			}
		case "lcommit":
			dir := m.localPath(&ex.repo)
			cmds = append(cmds, m.load("show:"+dir+"|"+it.lc.sha, true, fetchShow(dir, it.lc.sha, false)))
		case "stash":
			dir := m.localPath(&ex.repo)
			cmds = append(cmds, m.load("show:"+dir+"|"+it.st.ref, true, fetchShow(dir, it.st.ref, true)))
		case "pr":
			cmds = append(cmds, m.loadPRDetail(it.pr, true))
		}
	}
	return tea.Batch(cmds...)
}

// called whenever the highlighted item changes
func (m *model) exSelected() tea.Cmd {
	m.pvOff = 0
	m.exClamp()
	return m.exLoad()
}

func (m *model) exMove(d int) tea.Cmd {
	ex := m.ex
	n := len(m.exItems())
	if n == 0 {
		return nil
	}
	old := ex.cur[ex.sec]
	ex.cur[ex.sec] = max(0, min(n-1, old+d))
	if ex.cur[ex.sec] == old {
		return nil
	}
	return m.exSelected()
}

func (m *model) exSetSec(s int) tea.Cmd {
	ex := m.ex
	ex.sec = (s + len(secNames)) % len(secNames)
	if ex.sec == secChanges || ex.sec == secCommits || ex.sec == secBranches {
		m.chForget(m.localPath(&ex.repo)) // always read the folder again
	}
	ex.filtering = false
	m.focus = 0
	return m.exSelected()
}

func (m *model) exGoDir(dir string) tea.Cmd {
	ex := m.ex
	ex.dirCur[ex.dir] = ex.cur[secCode]
	ex.dir = dir
	ex.cur[secCode], ex.off[secCode] = ex.dirCur[dir], 0
	ex.sec, m.focus = secCode, 0
	return m.exSelected()
}

func parentDir(p string) string {
	d := path.Dir(p)
	if d == "." || d == "/" {
		return ""
	}
	return d
}

func (m *model) exUp() tea.Cmd {
	ex := m.ex
	child := ex.dir
	ex.dir = parentDir(child)
	ex.cur[secCode], ex.off[secCode] = 0, 0
	for i, it := range m.exItems() {
		if it.ent.Path == child {
			ex.cur[secCode] = i
		}
	}
	return m.exSelected()
}

// enter / double-click on the highlighted item
func (m *model) exActivate() tea.Cmd {
	ex := m.ex
	it, ok := m.exSel()
	if !ok {
		return nil
	}
	switch it.kind {
	case "up":
		return m.exUp()
	case "dir":
		return m.exGoDir(it.ent.Path)
	case "file":
		if isMarkdown(it.ent.Path) && ex.query[secCode] == "" {
			return m.actRead() // markdown opens straight in the reader
		}
		if ex.query[secCode] != "" { // a search result: jump to its folder, then show it
			ex.query[secCode], ex.filtering = "", false
			ex.dir = parentDir(it.ent.Path)
			ex.off[secCode] = 0
			for i, x := range m.exItems() {
				if x.ent.Path == it.ent.Path {
					ex.cur[secCode] = i
				}
			}
			cmd := m.exSelected()
			m.focus = 1
			return cmd
		}
		m.focus = 1
	case "sub", "commit":
		m.focus = 1
	case "run":
		return m.actLogs()
	case "change", "lcommit":
		m.focus = 1
		m.chCur = m.firstDiffLine()
	case "issue":
		return m.issRead()
	case "pr":
		return m.prRead()
	case "release":
		return m.actRead()
	case "branch":
		ex.ref, ex.refOid = it.branch.Name, it.branch.Commit.Oid
		ex.dir, ex.cur[secCode], ex.off[secCode], ex.cur[secCommits], ex.off[secCommits] = "", 0, 0, 0, 0
		ex.dirCur = map[string]int{}
		ex.query = [8]string{}
		cmd := m.exSetSec(secCode)
		return tea.Batch(cmd, m.setStatus("Showing branch "+ex.ref, false))
	}
	return nil
}

// esc / backspace: one step back
func (m *model) exBack() tea.Cmd {
	ex := m.ex
	switch {
	case ex.plan != nil && ex.sec == secCommits:
		ex.plan = nil
		return m.setStatus("Rebase cancelled: nothing changed", false)
	case m.focus == 1:
		m.focus = 0
	case ex.filtering || ex.query[ex.sec] != "":
		ex.filtering, ex.query[ex.sec] = false, ""
		ex.cur[ex.sec], ex.off[ex.sec] = 0, 0
		return m.exSelected()
	case ex.sec != secCode:
		// one crumb back: "Repos › repo › main › Commits" → "Repos › repo › main" (the repo's top)
		if ex.dir != "" {
			ex.dirCur[ex.dir] = ex.cur[secCode]
			ex.dir = ""
			ex.cur[secCode], ex.off[secCode] = ex.dirCur[""], 0
		}
		return m.exSetSec(secCode)
	case ex.sec == secCode && ex.dir != "":
		return m.exUp()
	default:
		return m.leaveRepo()
	}
	return nil
}

func (m *model) exRefresh() tea.Cmd {
	nwo := m.ex.repo.NameWithOwner
	for k := range m.loaded {
		for _, p := range []string{"tree:", "commits:", "branches:", "runs:", "releases:", "jobs:", "comments:"} {
			if strings.HasPrefix(k, p+nwo) {
				delete(m.loaded, k)
			}
		}
	}
	m.issForget("", 0)
	m.loading = true
	m.local = scanLocal(localRoots)
	return tea.Batch(m.exLoad(), fetchRepos(), m.scanLocalInfo())
}

func (m *model) exKey(s string) tea.Cmd {
	ex := m.ex
	if ex.sec == secActions || ex.sec == secReleases {
		if cmd, ok := m.ciKey(s); ok {
			return cmd
		}
	}
	if ex.sec == secChanges && m.isLocal() {
		if cmd, ok := m.cfKey(s); ok {
			return cmd
		}
		if cmd, ok := m.chLineKey(s); ok {
			return cmd
		}
		if cmd, ok := m.stashKey(s); ok {
			return cmd
		}
		if cmd, ok := m.chKey(s); ok {
			return cmd
		}
	}
	if ex.sec == secBranches && m.isLocal() && m.focus == 0 {
		if cmd, ok := m.remoteKey(s); ok {
			return cmd
		}
		if cmd, ok := m.worktreeKey(s); ok {
			return cmd
		}
		if cmd, ok := m.lbKey(s); ok {
			return cmd
		}
	}
	if ex.sec == secCommits && m.isLocal() && ex.plan != nil {
		if cmd, ok := m.planKey(s); ok {
			return cmd
		}
	}
	if ex.sec == secCommits && m.isLocal() {
		if cmd, ok := m.lcKey(s); ok {
			return cmd
		}
	}
	if ex.sec == secIssues {
		if cmd, ok := m.issueKey(s); ok {
			return cmd
		}
	}
	if ex.sec == secPRs {
		if cmd, ok := m.prKeys(s); ok {
			return cmd
		}
	}
	half := m.exRows() / 2
	switch s {
	case "tab", "shift+tab":
		m.focus = 1 - m.focus
	case "[":
		return m.exSetSec(ex.sec - 1)
	case "]":
		return m.exSetSec(ex.sec + 1)
	case "/":
		ex.filtering, m.focus = true, 0
	case "r":
		return m.exRefresh()
	case "o":
		return m.actBrowser()
	case "y":
		return m.actCopy()
	case "c":
		return m.actClone("")
	case "e":
		return m.actOpen("nvim")
	case "L":
		return m.actOpen("lazygit")
	case "p":
		return m.chPull()
	case "P":
		return m.chPush()
	case "v":
		return m.actRead()
	case "enter", "l", "right":
		if m.focus == 0 {
			return m.exActivate()
		}
	case "h", "left", "backspace":
		return m.exBack()
	case "j", "down":
		if m.focus == 0 {
			return m.exMove(1)
		}
		m.scroll(1)
	case "k", "up":
		if m.focus == 0 {
			return m.exMove(-1)
		}
		m.scroll(-1)
	case "pgdown", "space":
		if m.focus == 0 {
			return m.exMove(half * 2)
		}
		m.scroll(half * 2)
	case "pgup":
		if m.focus == 0 {
			return m.exMove(-half * 2)
		}
		m.scroll(-half * 2)
	case "g", "home":
		if m.focus == 0 {
			return m.exMove(-1 << 20)
		}
		m.pvOff = 0
	case "G", "end":
		if m.focus == 0 {
			return m.exMove(1 << 20)
		}
		m.pvOff = m.pvMax
	}
	return nil
}

// ---- opening in Nvim / lazygit ----

// the file highlighted in Code, as a repo path ("" if none)
func (m *model) exFile() string {
	if c := m.chSel(); c != nil {
		return c.path
	}
	if ex := m.exActive(); ex != nil && ex.sec == secCode {
		if it, ok := m.exSel(); ok {
			switch it.kind {
			case "file":
				return it.ent.Path
			case "dir":
				return it.ent.Path
			}
		}
		return ex.dir
	}
	return ""
}

func (m *model) actOpen(app string) tea.Cmd {
	r := m.curRepo()
	if r == nil {
		return nil
	}
	p := m.localPath(r)
	if p == "" {
		return m.actClone(app)
	}
	return m.runApp(app, p)
}

func appName(app string) string {
	if app == "nvim" {
		return "Nvim"
	}
	return app
}

func (m *model) runApp(app, dir string) tea.Cmd {
	var c *exec.Cmd
	switch app {
	case "nvim":
		target := "."
		if f := m.exFile(); f != "" {
			if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err == nil {
				target = filepath.FromSlash(f)
			}
		}
		c = exec.Command("nvim", target)
	case "lazygit":
		c = exec.Command("lazygit")
	default:
		return nil
	}
	c.Dir = dir
	if m.dump {
		return m.setStatus("Would run "+app+" in "+dir+" ("+strings.Join(c.Args[1:], " ")+")", false)
	}
	return tea.ExecProcess(c, func(err error) tea.Msg { return appDoneMsg{app, err} })
}

// ---- links ----

func escPath(p string) string {
	parts := strings.Split(p, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

// curURL is the GitHub page for what's on screen
func (m *model) curURL() string {
	if m.tab == tabActions {
		if r := m.selRun(); r != nil {
			return r.URL
		}
		return ""
	}
	if m.tab == tabIssues {
		if is := m.selIssue(); is != nil {
			return is.URL
		}
		return ""
	}
	if m.tab == tabPRs {
		if p := m.selPR(); p != nil {
			return p.URL
		}
		return ""
	}
	r := m.curRepo()
	if r == nil {
		return ""
	}
	ex := m.exActive()
	if ex == nil || ex.ref == "" {
		return r.URL
	}
	it, ok := m.exSel()
	ref := escPath(ex.ref)
	switch ex.sec {
	case secCode:
		switch {
		case ok && it.kind == "file":
			return r.URL + "/blob/" + ref + "/" + escPath(it.ent.Path)
		case ok && it.kind == "dir":
			return r.URL + "/tree/" + ref + "/" + escPath(it.ent.Path)
		case ex.dir != "":
			return r.URL + "/tree/" + ref + "/" + escPath(ex.dir)
		}
		return r.URL + "/tree/" + ref
	case secCommits:
		if ok && it.kind == "lcommit" {
			return r.URL + "/commit/" + it.lc.sha
		}
		if ok {
			return r.URL + "/commit/" + it.commit.Oid
		}
		return r.URL + "/commits/" + ref
	case secBranches:
		if ok && it.kind == "lbranch" {
			n := it.lb.name
			if it.lb.remote {
				n = n[strings.Index(n, "/")+1:]
			}
			return r.URL + "/tree/" + escPath(n)
		}
		if ok {
			return r.URL + "/tree/" + escPath(it.branch.Name)
		}
		return r.URL + "/branches"
	case secChanges:
		return r.URL + "/tree/" + ref
	case secIssues:
		if ok {
			return it.issue.URL
		}
		return r.URL + "/issues"
	case secPRs:
		if ok {
			return it.pr.URL
		}
		return r.URL + "/pulls"
	case secActions:
		if ok {
			return it.run.URL
		}
		return r.URL + "/actions"
	case secReleases:
		if ok {
			return it.release.URL
		}
		return r.URL + "/releases"
	}
	return r.URL
}

// ---- drawing ----

func (m *model) exPathBar() string {
	ex := m.ex
	sepTxt := " › "
	sep := fg(cMut).Render(sepTxt)
	out := " " + fg(cHint).Render("Repos")
	x := 1
	m.addZone(x, 1, x+4, 1, "crumb", 0)
	x += 5
	add := func(label string, c lipgloss.Style, id string, idx int) {
		out += sep + c.Render(label)
		x += width(sepTxt)
		m.addZone(x, 1, x+width(label)-1, 1, id, idx)
		x += width(label)
	}
	add(ex.repo.NameWithOwner, fg(cTxt), "crumb-repo", 0)
	if ex.ref != "" {
		add(icBranch+" "+ex.ref, fg(cAcc), "crumb-branch", 0)
	}
	add(secNames[ex.sec], fg(cHint), "crumb-sec", 0)
	if ex.sec == secCode && ex.dir != "" {
		for i, seg := range strings.Split(ex.dir, "/") {
			add(seg, fg(cTxt), "crumb-dir", i)
		}
	}
	return out
}

func (m *model) exListBox(w, h, y0 int) []string {
	ex := m.ex
	m.addZone(0, y0, w-1, y0+h-1, "list", 0)
	// section tabs in the top border: Code  Commits  Branches
	title := ""
	x := 3
	full := 0
	for i, n := range secNames {
		full += width(secIcons[i]+" "+n) + 2
	}
	compact := full > w-6 // narrow panel: only the open section shows its name
	for i, n := range secNames {
		label := secIcons[i] + " " + n
		if compact && i != ex.sec {
			label = secIcons[i]
		}
		if i > 0 {
			title += fg(cDim).Render("  ")
			x += 2
		}
		if i == ex.sec {
			title += fg(cAcc).Bold(true).Render(label)
		} else {
			title += fg(cMut).Render(label)
		}
		m.addZone(x, y0, x+width(label)-1, y0, "sec", i)
		x += width(label)
	}
	inner := w - 4
	var body []string
	rowY := y0 + 1
	if ex.filtering || ex.query[ex.sec] != "" {
		cursor := ""
		if ex.filtering {
			cursor = fg(cAcc).Render("▏")
		}
		what := map[int]string{secCode: "find a file", secCommits: "find a commit", secBranches: "find a branch", secActions: "find a run", secReleases: "find a release", secIssues: "find an issue", secPRs: "find a PR", secChanges: "find a changed file"}[ex.sec]
		q := fg(cTxt).Render(ex.query[ex.sec])
		if ex.query[ex.sec] == "" {
			q = ""
		}
		line := fg(cAcc).Render(icSearch+" ") + q + cursor
		if ex.query[ex.sec] == "" {
			line += fg(cMut).Render(" " + what)
		}
		body = append(body, line)
		rowY++
	}
	items := m.exItems()
	m.exClamp()
	empty := ""
	switch {
	case m.isLocal() && (ex.sec == secBranches || ex.sec == secCommits) && len(items) > 0:
		// local lists: nothing to explain
	case ex.ref == "":
		empty = "This repo is empty."
	case ex.sec == secCode && m.trees[m.treeKey()] == nil:
		empty = "Loading files…"
	case ex.sec == secCode && m.trees[m.treeKey()].err != "":
		empty = m.trees[m.treeKey()].err
	case ex.sec == secCommits && m.clists[m.treeKey()] == nil:
		empty = "Loading commits…"
	case ex.sec == secCommits && m.clists[m.treeKey()].err != "":
		empty = m.clists[m.treeKey()].err
	case ex.sec == secChanges && m.localPath(&ex.repo) == "":
		empty = "Not on this PC. c clones it."
	case ex.sec == secChanges && m.changes[m.localPath(&ex.repo)] == nil:
		empty = "Reading your changes…"
	case ex.sec == secChanges && m.changes[m.localPath(&ex.repo)].err != "":
		empty = m.changes[m.localPath(&ex.repo)].err
	case ex.sec == secChanges && len(items) == 0 && ex.query[ex.sec] == "" && len(m.stashes[m.localPath(&ex.repo)]) == 0:
		empty = "✓ Nothing changed: everything is committed."
	case ex.sec == secCommits && m.localPath(&ex.repo) != "" && m.llogs[m.localPath(&ex.repo)+"|"+ex.logRef] == nil:
		empty = "Reading the history…"
	case ex.sec == secBranches && m.localPath(&ex.repo) != "" && m.lbranches[m.localPath(&ex.repo)] == nil:
		empty = "Reading your branches…"
	case ex.sec == secIssues && m.issueLists[m.exIssueQuery()] == nil:
		empty = "Loading issues…"
	case ex.sec == secIssues && m.issueLists[m.exIssueQuery()].err != "":
		empty = m.issueLists[m.exIssueQuery()].err
	case ex.sec == secIssues && len(items) == 0 && ex.query[ex.sec] == "" && m.iss.closed:
		empty = "No closed issues. s shows open ones."
	case ex.sec == secIssues && len(items) == 0 && ex.query[ex.sec] == "":
		empty = "No open issues. N makes one, s shows closed."
	case ex.sec == secPRs && m.prLists[m.exPRQuery()] == nil:
		empty = "Loading pull requests…"
	case ex.sec == secPRs && m.prLists[m.exPRQuery()].err != "":
		empty = m.prLists[m.exPRQuery()].err
	case ex.sec == secPRs && len(items) == 0 && ex.query[ex.sec] == "" && m.prs.closed:
		empty = "No closed or merged PRs. s shows open ones."
	case ex.sec == secPRs && len(items) == 0 && ex.query[ex.sec] == "":
		empty = "No open PRs. N opens one, s shows closed."
	case ex.sec == secActions && m.runs[ex.repo.NameWithOwner] == nil:
		empty = "Loading runs…"
	case ex.sec == secActions && m.runs[ex.repo.NameWithOwner].err != "":
		empty = m.runs[ex.repo.NameWithOwner].err
	case ex.sec == secActions && len(items) == 0 && ex.query[ex.sec] == "":
		empty = "No CI runs yet. w starts a workflow."
	case ex.sec == secReleases && m.releases[ex.repo.NameWithOwner] == nil:
		empty = "Loading releases…"
	case ex.sec == secReleases && m.releases[ex.repo.NameWithOwner].err != "":
		empty = m.releases[ex.repo.NameWithOwner].err
	case ex.sec == secReleases && len(items) == 0 && ex.query[ex.sec] == "":
		empty = "No releases yet. N makes one."
	case ex.sec == secBranches && m.blists[ex.repo.NameWithOwner] == nil:
		empty = "Loading branches…"
	case ex.sec == secBranches && m.blists[ex.repo.NameWithOwner].err != "":
		empty = m.blists[ex.repo.NameWithOwner].err
	case len(items) == 0 && ex.query[ex.sec] != "":
		empty = "Nothing matches \"" + ex.query[ex.sec] + "\""
	case len(items) == 0:
		empty = "Nothing here."
	}
	if empty != "" {
		body = append(body, "", fg(cMut).Render(empty))
	}
	rows := m.exRows()
	off := ex.off[ex.sec]
	for i := off; empty == "" && i < len(items) && i < off+rows; i++ {
		body = append(body, m.exRow(items[i], inner, i == ex.cur[ex.sec]))
		y := rowY + (i - off)
		m.addZone(1, y, w-2, y, "exrow", i)
	}
	note := ""
	if len(items) > 0 {
		note = fmt.Sprintf("%d/%d", ex.cur[ex.sec]+1, len(items))
	}
	if ex.sec == secChanges && m.localPath(&ex.repo) != "" {
		note = m.changesNote() + "  " + note
	}
	if ex.sec == secCommits && m.localPath(&ex.repo) != "" {
		ref := ex.logRef
		if ref == "" {
			ref = m.currentBranch()
		}
		note = icBranch + " " + ref + " · on this PC  " + note
	}
	if st := gitState(m.localPath(&ex.repo)); st != "" && m.localPath(&ex.repo) != "" && (ex.sec == secChanges || ex.sec == secBranches || ex.sec == secCommits) {
		note = strings.ToUpper(st) + " in progress · ctrl+p Abort/Continue  " + note
	}
	if td := m.trees[m.treeKey()]; ex.sec == secCode && td != nil && td.truncated {
		note = "big repo: some files missing · " + note
	}
	return box(title, w, h, body, m.focus == 0, note)
}

func (m *model) exRow(it exItem, w int, sel bool) string {
	switch it.kind {
	case "change":
		return m.changeRow(it.ch, w, sel)
	case "lbranch":
		return m.lbranchRow(it.lb, w, sel)
	case "lcommit":
		return m.lcommitRow(it.lc, w, sel)
	case "plan":
		return m.planRow(*it.plan, w, sel)
	case "remote":
		return m.remoteRow(it.rm, w, sel)
	case "worktree":
		return m.worktreeRow(it.wt, w, sel)
	case "stash":
		return m.stashRow(it.st, w, sel)
	case "issue":
		return m.issueRow(it.issue, w, sel, false)
	case "pr":
		return m.prRow(it.pr, w, sel, false)
	case "run":
		return m.runRow(it.run, w, sel, false)
	case "release":
		return m.releaseRow(it.release, w, sel)
	}
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
	var left, right string
	switch it.kind {
	case "up":
		left = st.Foreground(cMut).Render(icUp) + st.Render(" ") + st.Foreground(nc).Render("..")
	case "dir":
		left = st.Foreground(cAcc).Render(icFolder) + st.Render(" ") + st.Foreground(nc).Bold(sel).Render(it.ent.name())
	case "sub":
		left = st.Foreground(cMut).Render(icRepo) + st.Render(" ") + st.Foreground(nc).Bold(sel).Render(it.ent.name())
	case "file":
		name := it.ent.name()
		if m.ex.query[secCode] != "" {
			name = it.ent.Path
		}
		left = st.Foreground(cMut).Render(icFile) + st.Render(" ") + st.Foreground(nc).Bold(sel).Render(name)
		right = st.Foreground(cMut).Render(shortSize(it.ent.Size))
	case "commit":
		c := it.commit
		left = st.Foreground(cMut).Render(c.Short[:min(7, len(c.Short))]) + st.Render(" ") + st.Foreground(nc).Bold(sel).Render(c.Headline)
		right = st.Foreground(cMut).Render(fmt.Sprintf("%4s", ago(c.Date)))
	case "branch":
		b := it.branch
		ic := cMut
		if b.Name == m.ex.ref {
			ic = cGreen
		}
		left = st.Foreground(ic).Render(icBranch) + st.Render(" ") + st.Foreground(nc).Bold(sel).Render(b.Name)
		if b.Name == m.ex.repo.Branch() {
			left += st.Foreground(cMut).Render("  default")
		}
		right = st.Foreground(cMut).Render(fmt.Sprintf("%4s", ago(b.Commit.Date)))
	}
	space := w - width(left) - width(right) - 1
	if space < 1 {
		left = trunc(left, w-width(right)-2)
		space = w - width(left) - width(right) - 1
	}
	return left + st.Render(strings.Repeat(" ", max(space, 0)+1)) + right
}

func (m *model) cachedLines(key string, make func() []string) []string {
	if l, ok := m.lines[key]; ok {
		return l
	}
	l := make()
	m.lines[key] = l
	return l
}

func (m *model) dirReadme(dir string) *treeEntry {
	td := m.trees[m.treeKey()]
	if td == nil {
		return nil
	}
	for i, e := range td.children[dir] {
		if e.Type == "blob" && strings.HasPrefix(strings.ToLower(e.name()), "readme") {
			return &td.children[dir][i]
		}
	}
	return nil
}

func isImage(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".ico", ".webp", ".avif", ".tiff":
		return true
	}
	return false
}

func sepLine(w int) string { return fg(cDim).Render(strings.Repeat("─", w)) }

func wrapText(s string, w int, c lipgloss.Style) []string {
	var out []string
	for _, l := range strings.Split(lipgloss.NewStyle().Width(w).Render(s), "\n") {
		out = append(out, c.Render(strings.TrimRight(l, " ")))
	}
	return out
}

// exPreview is the right panel inside a repo: a file, a folder, a commit or a branch
func (m *model) exPreview(w int) (string, []string) {
	ex := m.ex
	it, ok := m.exSel()
	if !ok {
		switch ex.sec {
		case secCommits:
			return "Commit", nil
		case secBranches:
			return "Branch", nil
		case secChanges:
			return "Changes", nil
		case secIssues:
			return "Issue", nil
		case secPRs:
			return "Pull request", nil
		case secActions:
			return "Run", nil
		case secReleases:
			return "Release", nil
		}
		return "Code", nil
	}
	switch it.kind {
	case "up":
		p := parentDir(ex.dir)
		if p == "" {
			p = ex.repo.Name
		}
		return "..", []string{fg(cMut).Render("Go back to " + p)}
	case "dir":
		return it.ent.name(), m.dirPreview(it.ent.Path, w)
	case "sub":
		return it.ent.name(), []string{
			fg(cAcc).Bold(true).Render(it.ent.Path),
			"", fg(cTxt).Render("A submodule: a link to another repo,"),
			fg(cTxt).Render("pinned at commit " + it.ent.Sha[:7] + "."),
			"", fg(cMut).Render("o opens it on GitHub"),
		}
	case "file":
		return it.ent.name(), m.filePreview(it.ent, w)
	case "commit":
		return "Commit " + it.commit.Short, m.commitPreview(it.commit, w)
	case "branch":
		return "Branch", m.branchPreview(it.branch, w)
	case "change":
		return path.Base(it.ch.path), m.changePreview(it.ch, w)
	case "lbranch":
		return "Branch", m.lbranchPreview(it.lb, w)
	case "lcommit":
		return "Commit " + it.lc.short, m.lcommitPreview(it.lc, w)
	case "plan":
		return "Rebase plan", m.planPreview(w)
	case "remote":
		return "Remote", m.remotePreview(it.rm, w)
	case "worktree":
		return "Worktree", m.worktreePreview(it.wt, w)
	case "stash":
		return "Stash", m.stashPreview(it.st, w)
	case "issue":
		return fmt.Sprintf("Issue #%d", it.issue.Number), m.issuePreview(it.issue, w)
	case "pr":
		lw, _ := m.layout()
		return m.prTitle(it.pr, lw, 2), m.prPreview(it.pr, w)
	case "run":
		return fmt.Sprintf("Run #%d", it.run.Number), m.runPreview(it.run, w)
	case "release":
		return "Release", m.releasePreview(it.release, w)
	}
	return "", nil
}

func (m *model) dirPreview(dir string, w int) []string {
	td := m.trees[m.treeKey()]
	out := []string{fg(cAcc).Bold(true).Render(dir + "/")}
	kids := td.children[dir]
	files, dirs := 0, 0
	for _, e := range kids {
		if e.Type == "tree" {
			dirs++
		} else {
			files++
		}
	}
	out = append(out, fg(cMut).Render(fmt.Sprintf("%d %s · %d %s    enter to go in", dirs, plural(dirs, "folder", "folders"), files, plural(files, "file", "files"))), "")
	for i, e := range kids {
		if i >= 30 {
			out = append(out, fg(cMut).Render(fmt.Sprintf("  … and %d more", len(kids)-30)))
			break
		}
		if e.Type == "tree" {
			out = append(out, fg(cAcc).Render(icFolder)+" "+fg(cTxt).Render(e.name()))
		} else {
			out = append(out, fg(cMut).Render(icFile)+" "+fg(cTxt).Render(e.name()))
		}
	}
	if rd := m.dirReadme(dir); rd != nil {
		out = append(out, "", sepLine(w), fg(cHint).Render(rd.name()), "")
		out = append(out, m.blobLines(*rd, w)...)
	}
	return out
}

func (m *model) filePreview(e treeEntry, w int) []string {
	out := []string{fg(cAcc).Bold(true).Render(e.Path)}
	meta := humanSize(e.Size)
	if b := m.blobs[e.Sha]; b != nil && b.err == "" && !b.binary && !isMarkdown(e.Path) {
		meta += fmt.Sprintf(" · %d lines", strings.Count(strings.TrimRight(b.text, "\n"), "\n")+1)
	}
	out = append(out, fg(cMut).Render(meta+" · "+icBranch+" "+m.ex.ref), sepLine(w))
	return append(out, m.blobLines(e, w)...)
}

// blobLines draws a file's contents: markdown rendered, code colored
func (m *model) blobLines(e treeEntry, w int) []string {
	switch {
	case e.Size > maxFile:
		return []string{"", fg(cMut).Render("This file is too big to show here (" + humanSize(e.Size) + ")."), fg(cMut).Render("Press o to open it on GitHub.")}
	case isImage(e.Path):
		return []string{"", fg(cMut).Render("An image (" + humanSize(e.Size) + "). Press o to see it on GitHub.")}
	}
	b := m.blobs[e.Sha]
	switch {
	case b == nil:
		return []string{fg(cMut).Render("Loading…")}
	case b.err != "":
		return []string{fg(cRed).Render("Couldn't load the file:"), fg(cMut).Render(b.err)}
	case b.binary:
		return []string{"", fg(cMut).Render("A binary file (" + humanSize(e.Size) + "): nothing to read here.")}
	case strings.TrimSpace(b.text) == "":
		return []string{"", fg(cMut).Render("This file is empty.")}
	case isMarkdown(e.Path):
		return m.cachedLines(fmt.Sprintf("md:%s:%d", e.Sha, w), func() []string { return renderMarkdown(b.text, w) })
	}
	return m.cachedLines("hl:"+e.Sha, func() []string { return highlight(e.name(), b.text) })
}

func (m *model) commitPreview(c *Commit, w int) []string {
	var out []string
	out = append(out, wrapText(c.Headline, w, fg(cAcc).Bold(true))...)
	if body := strings.TrimSpace(c.Body); body != "" {
		out = append(out, "")
		for _, p := range strings.Split(body, "\n") {
			out = append(out, wrapText(p, w, fg(cTxt))...)
		}
	}
	out = append(out, "")
	meta := []string{
		fg(cTxt).Render(c.who()),
		fg(cMut).Render(icClock + " " + c.Date.Local().Format("2 Jan 2006 15:04") + " (" + ago(c.Date) + " ago)"),
		fg(cMut).Render(c.Short),
	}
	out = append(out, wrapJoin(meta, "   ", w)...)
	out = append(out, fg(cGreen).Render(fmt.Sprintf("+%d", c.Adds))+" "+fg(cRed).Render(fmt.Sprintf("−%d", c.Dels))+
		fg(cMut).Render(fmt.Sprintf("  in %d %s", c.Files, plural(c.Files, "file", "files"))), sepLine(w), "")
	key := "commit:" + m.ex.repo.NameWithOwner + "@" + c.Oid
	d := m.cdetail[m.ex.repo.NameWithOwner+"@"+c.Oid]
	switch {
	case d == nil:
		out = append(out, fg(cMut).Render("Loading the changes…"))
	case d.err != "":
		out = append(out, fg(cRed).Render("Couldn't load the changes:"), fg(cMut).Render(d.err))
	default:
		out = append(out, m.cachedLines(fmt.Sprintf("%s:%d", key, w), func() []string { return diffLines(d.files, w) })...)
	}
	return out
}

func (m *model) branchPreview(b *Branch, w int) []string {
	ex := m.ex
	out := []string{fg(cAcc).Bold(true).Render(icBranch + " " + b.Name)}
	def := ex.repo.Branch()
	switch {
	case b.Name == def:
		out = append(out, fg(cMut).Render("the default branch"))
	case b.Ahead == 0 && b.Behind == 0:
		out = append(out, fg(cMut).Render("same as "+def))
	default:
		var parts []string
		if b.Ahead > 0 {
			parts = append(parts, fg(cGreen).Render(fmt.Sprintf("%d commits ahead of %s", b.Ahead, def)))
		}
		if b.Behind > 0 {
			parts = append(parts, fg(cYellow).Render(fmt.Sprintf("%d behind", b.Behind)))
		}
		out = append(out, strings.Join(parts, fg(cMut).Render(", ")))
	}
	if b.Name == ex.ref {
		out = append(out, fg(cGreen).Render(icCheck+" you're viewing this branch"))
	}
	out = append(out, "", fg(cHint).Render("Last commit"))
	out = append(out, wrapText(b.Commit.Headline, w, fg(cTxt))...)
	out = append(out, fg(cMut).Render(b.Commit.who()+"  ·  "+ago(b.Commit.Date)+" ago  ·  "+b.Commit.Short), "")
	if b.Name != ex.ref {
		out = append(out, hints([][2]string{{"enter", "browse this branch's code and commits"}}))
	}
	out = append(out, hints([][2]string{{"o", "open it on GitHub"}}))
	return out
}

// localLine says how your copy on this PC compares with GitHub
func (m *model) localLine(r *Repo, w int) []string {
	li, ok := m.linfo[strings.ToLower(r.NameWithOwner)]
	if !ok {
		return nil
	}
	var parts []string
	if li.branch != "" && li.branch != r.Branch() {
		parts = append(parts, fg(cMut).Render(icBranch+" on "+li.branch))
	}
	if li.dirty > 0 {
		parts = append(parts, fg(cOrange).Render(fmt.Sprintf("● %d changed %s", li.dirty, plural(li.dirty, "file", "files"))))
	}
	if li.ahead > 0 {
		parts = append(parts, fg(cYellow).Render(fmt.Sprintf("↑ %d not pushed", li.ahead)))
	}
	switch {
	case li.missing:
		parts = append(parts, fg(cAcc).Render("↓ GitHub has new commits"))
	case li.behind > 0:
		parts = append(parts, fg(cAcc).Render(fmt.Sprintf("↓ %d new on GitHub", li.behind)))
	}
	if li.dirty == 0 && li.ahead == 0 && li.behind == 0 && !li.missing {
		parts = append(parts, fg(cGreen).Render(icCheck+" in sync with GitHub"))
		return wrapJoin(parts, "   ", w)
	}
	h := [][2]string{{"L", "lazygit to commit or push"}}
	if li.behind > 0 || li.missing {
		h = [][2]string{{"p", "pull"}, {"L", "lazygit"}}
	}
	return append(wrapJoin(parts, "   ", w), hints(h))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// ---- pull ----

type pulledMsg struct {
	name string
	out  string
	err  error
}

// P: git pull --ff-only in your copy on this PC (never merges or rewrites anything)
func (m *model) actPull() tea.Cmd {
	r := m.curRepo()
	if r == nil {
		return nil
	}
	dir := m.localPath(r)
	if dir == "" {
		return m.setStatus(r.Name+" isn't on this PC: press c to clone it", true)
	}
	li := m.linfo[strings.ToLower(r.NameWithOwner)]
	what := "Pull from GitHub into " + r.Name
	switch {
	case li.behind > 0:
		what = fmt.Sprintf("Pull %d new %s into %s", li.behind, plural(li.behind, "commit", "commits"), r.Name)
	case li.missing:
		what = "Pull the new commits into " + r.Name
	}
	if li.branch != "" {
		what += " (" + li.branch + ")"
	}
	name := r.Name
	m.prompt = &prompt{label: what + "?", confirm: true, done: func(m *model, _ string) tea.Cmd {
		if m.dump {
			return m.setStatus("Would run git pull --ff-only in "+dir, false)
		}
		return tea.Batch(m.setStatus("Pulling…", false), func() tea.Msg {
			c := exec.Command("git", "-C", dir, "pull", "--ff-only")
			c.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0") // never hang on a password prompt
			out, err := c.CombinedOutput()
			return pulledMsg{name, string(out), err}
		})
	}}
	return nil
}

func (m *model) onPulled(msg pulledMsg) tea.Cmd {
	scan := m.scanLocalInfo()
	out := strings.TrimSpace(msg.out)
	if msg.err != nil {
		why := lastLine(out)
		switch {
		case strings.Contains(out, "would be overwritten"):
			why = "your uncommitted changes touch the same files: commit or stash them first (L)"
		case strings.Contains(out, "Not possible to fast-forward") || strings.Contains(out, "diverging"):
			why = "you have commits GitHub doesn't: pull in lazygit to combine them (L, then p)"
		case strings.Contains(out, "no tracking information"):
			why = "this branch isn't linked to GitHub: push it once in lazygit (L, then P)"
		case strings.Contains(out, "Authentication") || strings.Contains(out, "terminal prompts disabled"):
			why = "git needs your login: pull once in lazygit or a terminal"
		}
		return tea.Batch(scan, m.setStatus("Couldn't pull "+msg.name+": "+why, true))
	}
	if strings.Contains(out, "Already up to date") {
		return tea.Batch(scan, m.setStatus(msg.name+" was already up to date", false))
	}
	sum := "done"
	for _, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "changed") && (strings.Contains(l, "file") || strings.Contains(l, "insertion")) {
			sum = strings.TrimSpace(l)
		}
	}
	return tea.Batch(scan, m.setStatus("Pulled "+msg.name+": "+sum, false))
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}
