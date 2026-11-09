package main

// Explore: search all of GitHub (repos, issues, code) or look through your stars.
// Open any repo in the explorer, star / unstar, fork, clone.

import (
	"fmt"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/cli/go-gh/v2/pkg/api"
)

const repoFields = `id nameWithOwner name description isPrivate isFork isArchived stargazerCount forkCount pushedAt url viewerHasStarred
        defaultBranchRef { name target { oid } }
        primaryLanguage { name color }`

var expModes = []string{"Starred", "Repos", "Issues", "Code", "Users"}

const modeUsers = 4

type expState struct {
	mode     int
	query    [5]string
	user     string // Users: the profile being looked at
	cur, off int
	jumped   string // the code result we already scrolled to its first match
}

type repoSearch struct {
	items []Repo
	err   string
}

type codeHit struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Sha  string `json:"sha"`
	URL  string `json:"html_url"`
	Repo struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
}

type codeSearch struct {
	items []codeHit
	err   string
}

func fetchStarred() func() func(*model) {
	return func() func(*model) {
		rs := &repoSearch{}
		c, err := api.DefaultGraphQLClient()
		if err == nil {
			var r struct {
				Viewer struct {
					StarredRepositories struct{ Nodes []Repo }
				}
			}
			err = c.Do(`query { viewer { starredRepositories(first: 60, orderBy: {field: STARRED_AT, direction: DESC}) { nodes { `+repoFields+` } } } }`, nil, &r)
			rs.items = r.Viewer.StarredRepositories.Nodes
		}
		if err != nil {
			rs.err = apiErr(err)
		}
		return func(m *model) { m.expRepos["starred"] = rs }
	}
}

func fetchRepoSearch(q string) func() func(*model) {
	return func() func(*model) {
		rs := &repoSearch{}
		c, err := api.DefaultGraphQLClient()
		if err == nil {
			var r struct {
				Search struct{ Nodes []Repo }
			}
			err = c.Do(`query($q: String!) { search(query: $q, type: REPOSITORY, first: 40) { nodes { ... on Repository { `+repoFields+` } } } }`,
				map[string]interface{}{"q": q}, &r)
			rs.items = r.Search.Nodes
		}
		if err != nil {
			rs.err = apiErr(err)
		}
		return func(m *model) { m.expRepos["q:"+q] = rs }
	}
}

func fetchCodeSearch(q string) func() func(*model) {
	return func() func(*model) {
		cs := &codeSearch{}
		c, err := api.DefaultRESTClient()
		if err == nil {
			var r struct {
				Items []codeHit `json:"items"`
			}
			err = c.Get("search/code?per_page=40&q="+urlQuery(q), &r)
			cs.items = r.Items
		}
		if err != nil {
			cs.err = apiErr(err)
		}
		return func(m *model) { m.expCode[q] = cs }
	}
}

func urlQuery(q string) string {
	r := strings.NewReplacer(" ", "+", "#", "%23", "&", "%26", "?", "%3F", ":", "%3A", "/", "%2F", "\"", "%22")
	return r.Replace(q)
}

// ---- which items ----

type expItem struct {
	repo    *Repo
	issue   *Issue
	code    *codeHit
	user    *userHit
	profile *Profile // the first row of a profile: the person themselves
}

func (m *model) expIssueQuery() string {
	q := m.exp.query[2]
	if !strings.Contains(q, "is:") {
		q += " is:issue"
	}
	return q
}

func (m *model) expItems() []expItem {
	var out []expItem
	switch m.exp.mode {
	case 0, 1:
		key := "starred"
		if m.exp.mode == 1 {
			key = "q:" + m.exp.query[1]
		}
		if rs := m.expRepos[key]; rs != nil {
			for i := range rs.items {
				out = append(out, expItem{repo: &rs.items[i]})
			}
		}
	case 2:
		if il := m.issueLists[m.expIssueQuery()]; il != nil && m.exp.query[2] != "" {
			for i := range il.items {
				out = append(out, expItem{issue: &il.items[i]})
			}
		}
	case 3:
		if cs := m.expCode[m.exp.query[3]]; cs != nil {
			for i := range cs.items {
				out = append(out, expItem{code: &cs.items[i]})
			}
		}
	case modeUsers:
		if m.exp.user != "" {
			if p := m.profiles[strings.ToLower(m.exp.user)]; p != nil {
				out = append(out, expItem{profile: p})
				for i := range p.Repositories.Nodes {
					out = append(out, expItem{repo: &p.Repositories.Nodes[i]})
				}
			}
			return out
		}
		if us := m.expUsers[m.exp.query[modeUsers]]; us != nil {
			for i := range us.items {
				out = append(out, expItem{user: &us.items[i]})
			}
		}
	}
	return out
}

func (m *model) expSel() (expItem, bool) {
	items := m.expItems()
	if m.exp.cur >= 0 && m.exp.cur < len(items) {
		return items[m.exp.cur], true
	}
	return expItem{}, false
}

func (m *model) expLoad() tea.Cmd {
	q := m.exp.query[m.exp.mode]
	var cmds []tea.Cmd
	switch m.exp.mode {
	case 0:
		cmds = append(cmds, m.load("starred", false, fetchStarred()))
	case 1:
		if q != "" {
			cmds = append(cmds, m.load("reposearch:"+q, false, fetchRepoSearch(q)))
		}
	case 2:
		if q != "" {
			iq := m.expIssueQuery()
			cmds = append(cmds, m.load("issues:"+iq, false, fetchIssues(iq)))
		}
	case 3:
		if q != "" {
			cmds = append(cmds, m.load("codesearch:"+q, false, fetchCodeSearch(q)))
		}
	case modeUsers:
		if m.exp.user != "" {
			cmds = append(cmds, m.load("profile:"+strings.ToLower(m.exp.user), false, fetchProfile(m.exp.user)))
		} else if q != "" {
			cmds = append(cmds, m.load("usersearch:"+q, false, fetchUserSearch(q)))
		}
	}
	if it, ok := m.expSel(); ok {
		switch {
		case it.repo != nil:
			nwo := it.repo.NameWithOwner
			if _, have := m.readme[nwo]; !have && !m.fetching[nwo] {
				m.fetching[nwo] = true
				cmds = append(cmds, fetchReadme(nwo))
			}
		case it.issue != nil:
			cmds = append(cmds, m.loadComments(it.issue, true))
		case it.code != nil:
			cmds = append(cmds, m.load("blob:"+it.code.Sha, true, fetchBlob(it.code.Repo.FullName, it.code.Sha)))
		case it.user != nil:
			cmds = append(cmds, m.load("profile:"+strings.ToLower(it.user.Login), true, fetchProfile(it.user.Login)))
		}
	}
	return tea.Batch(cmds...)
}

func (m *model) expSelected() tea.Cmd {
	m.pvOff = 0
	n := len(m.expItems())
	m.exp.cur = max(0, min(m.exp.cur, n-1))
	rows := m.listRows()
	if m.exp.cur < m.exp.off {
		m.exp.off = m.exp.cur
	}
	if rows > 0 && m.exp.cur >= m.exp.off+rows {
		m.exp.off = m.exp.cur - rows + 1
	}
	return m.expLoad()
}

func (m *model) expMove(d int) tea.Cmd {
	n := len(m.expItems())
	if n == 0 {
		return nil
	}
	old := m.exp.cur
	m.exp.cur = max(0, min(n-1, old+d))
	if old == m.exp.cur {
		return nil
	}
	return m.expSelected()
}

func (m *model) expSetMode(i int) tea.Cmd {
	m.exp.mode = (i + len(expModes)) % len(expModes)
	m.exp.cur, m.exp.off, m.focus = 0, 0, 0
	return m.expSelected()
}

// /: the search, typed in the bottom bar (GitHub search syntax works: language:go stars:>100 …)
func (m *model) expAsk() tea.Cmd {
	if m.exp.mode == 0 {
		m.exp.mode = 1
	}
	mode := m.exp.mode
	label := map[int]string{1: "Search GitHub repos:", 2: "Search GitHub issues:", 3: "Search code on GitHub:", modeUsers: "Search GitHub users:"}[mode]
	if mode == modeUsers {
		m.exp.user = ""
	}
	m.prompt = &prompt{label: label, text: m.exp.query[mode], done: func(m *model, q string) tea.Cmd {
		if q == "" {
			return nil
		}
		m.exp.query[mode], m.exp.cur, m.exp.off = q, 0, 0
		return m.expSelected()
	}}
	return nil
}

// ---- doing things ----

// open a repo found here in the explorer
func (m *model) openRepoObj(r Repo) tea.Cmd {
	from := m.tab
	m.setTab(tabRepos)
	m.backTo = from
	ex, ok := m.exSaved[r.NameWithOwner]
	if !ok {
		ex = &explorer{repo: r, ref: r.Branch(), refOid: r.HeadOid(), dirCur: map[string]int{}}
	}
	m.ex, m.focus, m.help = ex, 0, false
	m.rememberRepo(r.NameWithOwner)
	return m.exSetSec(secCode)
}

func (m *model) expOpen() tea.Cmd {
	it, ok := m.expSel()
	if !ok {
		return nil
	}
	switch {
	case it.user != nil:
		m.exp.user, m.exp.cur, m.exp.off = it.user.Login, 0, 0
		return m.expSelected()
	case it.profile != nil:
		m.focus = 1
		return nil
	case it.repo != nil:
		return m.openRepoObj(*it.repo)
	case it.issue != nil:
		return m.gotoIssue(it.issue.repo(), it.issue.Number, it.issue.open())
	case it.code != nil:
		p := it.code.Path
		r := Repo{NameWithOwner: it.code.Repo.FullName, Name: shortRepo(it.code.Repo.FullName), URL: "https://github.com/" + it.code.Repo.FullName}
		for _, x := range m.repos {
			if strings.EqualFold(x.NameWithOwner, r.NameWithOwner) {
				r = x
			}
		}
		cmd := m.gotoItem(r.NameWithOwner, secCode, p, func(it exItem) bool { return it.kind == "file" && it.ent.Path == p }, nil)
		if m.ex != nil {
			if m.ex.ref == "" {
				m.ex.ref = "HEAD"
			}
			m.ex.dir = parentDir(p)
			return tea.Batch(cmd, m.exSelected())
		}
		return cmd
	}
	return nil
}

type starredMsg struct {
	nwo  string
	star bool
	err  error
}

// s: star / unstar
func (m *model) actStar(r *Repo) tea.Cmd {
	if r == nil || r.ID == "" {
		return m.setStatus("Refresh first (r): this repo's id isn't known yet", true)
	}
	id, nwo, star := r.ID, r.NameWithOwner, !r.ViewerHasStarred
	q := `mutation($id: ID!) { addStar(input: {starrableId: $id}) { clientMutationId } }`
	if !star {
		q = `mutation($id: ID!) { removeStar(input: {starrableId: $id}) { clientMutationId } }`
	}
	if m.dump {
		return m.setStatus(fmt.Sprintf("Would star=%v %s", star, nwo), false)
	}
	return func() tea.Msg { return starredMsg{nwo, star, gql(q, map[string]interface{}{"id": id})} }
}

func (m *model) onStarred(msg starredMsg) tea.Cmd {
	if msg.err != nil {
		return m.setStatus("Couldn't star: "+apiErr(msg.err), true)
	}
	// update every copy of that repo we hold
	fix := func(list []Repo) {
		for i := range list {
			if list[i].NameWithOwner == msg.nwo {
				list[i].ViewerHasStarred = msg.star
				if msg.star {
					list[i].Stars++
				} else {
					list[i].Stars--
				}
			}
		}
	}
	fix(m.repos)
	for _, rs := range m.expRepos {
		fix(rs.items)
	}
	delete(m.loaded, "starred")
	if msg.star {
		return m.setStatus("Starred "+msg.nwo, false)
	}
	return m.setStatus("Unstarred "+msg.nwo, false)
}

// F: fork to your account
func (m *model) actFork(r *Repo) tea.Cmd {
	if r == nil {
		return nil
	}
	nwo := r.NameWithOwner
	m.prompt = &prompt{label: "Fork " + nwo + " to your account?", confirm: true, done: func(m *model, _ string) tea.Cmd {
		if m.dump {
			return m.setStatus("Would fork "+nwo, false)
		}
		return tea.Batch(m.setStatus("Forking…", false), func() tea.Msg {
			out, err := exec.Command("gh", "repo", "fork", nwo, "--clone=false").CombinedOutput()
			if err != nil {
				err = fmt.Errorf("%s", strings.TrimSpace(string(out)))
			}
			return repoDoneMsg{"Forked " + nwo + " (it's in your Repos now)", err}
		})
	}}
	return nil
}

func (m *model) expKey(s string) tea.Cmd {
	it, _ := m.expSel()
	half := m.listRows() / 2
	switch s {
	case "enter":
		return m.expOpen()
	case "/":
		return m.expAsk()
	case "f", "]":
		return m.expSetMode(m.exp.mode + 1)
	case "[":
		return m.expSetMode(m.exp.mode - 1)
	case "s":
		return m.actStar(it.repo)
	case "F":
		if it.profile != nil {
			return m.actFollow(it.profile)
		}
		if it.user != nil {
			return m.actFollow(m.profiles[strings.ToLower(it.user.Login)])
		}
		return m.actFork(it.repo)
	case "u":
		if it.repo != nil {
			return m.openProfile(strings.SplitN(it.repo.NameWithOwner, "/", 2)[0])
		}
	case "c":
		if it.repo != nil {
			return m.cloneRepo(it.repo)
		}
	case "v":
		if it.issue != nil {
			return m.issReadFor(it.issue)
		}
		if it.repo != nil {
			nwo := it.repo.NameWithOwner
			return m.openReader(&reader{title: "README", sub: nwo, icon: "",
				mdSrc: func(m *model) (string, bool) {
					e, ok := m.readme[nwo]
					return e.md, ok && !e.none && strings.TrimSpace(e.md) != ""
				},
				content: func(m *model, w int) []string { return []string{fg(cMut).Render("No README.")} }})
		}
	case "o":
		switch {
		case it.repo != nil:
			return m.openURL(it.repo.URL)
		case it.issue != nil:
			return m.openURL(it.issue.URL)
		case it.code != nil:
			return m.openURL(it.code.URL)
		}
	case "y":
		switch {
		case it.repo != nil:
			return m.copyText(it.repo.URL, "Copied the link")
		case it.issue != nil:
			return m.copyText(it.issue.URL, "Copied the link")
		case it.code != nil:
			return m.copyText(it.code.URL, "Copied the link")
		}
	case "r":
		for k := range m.loaded {
			if k == "starred" || strings.HasPrefix(k, "reposearch:") || strings.HasPrefix(k, "codesearch:") {
				delete(m.loaded, k)
			}
		}
		m.issForget("", 0)
		return m.expLoad()
	case "tab", "shift+tab":
		m.focus = 1 - m.focus
	case "l", "right":
		m.focus = 1
	case "h", "left":
		m.focus = 0
	case "j", "down":
		if m.focus == 0 {
			return m.expMove(1)
		}
		m.scroll(1)
	case "k", "up":
		if m.focus == 0 {
			return m.expMove(-1)
		}
		m.scroll(-1)
	case "pgdown", "space":
		if m.focus == 0 {
			return m.expMove(half * 2)
		}
		m.scroll(half * 2)
	case "pgup":
		if m.focus == 0 {
			return m.expMove(-half * 2)
		}
		m.scroll(-half * 2)
	case "g", "home":
		if m.focus == 0 {
			return m.expMove(-1 << 20)
		}
		m.pvOff = 0
	case "G", "end":
		if m.focus == 0 {
			return m.expMove(1 << 20)
		}
		m.pvOff = m.pvMax
	}
	return nil
}

// cloneRepo clones any repo (not only yours)
func (m *model) cloneRepo(r *Repo) tea.Cmd {
	if p := m.localPath(r); p != "" {
		return m.setStatus("Already on this PC: "+p, false)
	}
	dest := home("Documents", "Coding", r.Name)
	nwo := r.NameWithOwner
	m.prompt = &prompt{label: "Clone " + nwo + " into " + dest + "?", confirm: true, done: func(m *model, _ string) tea.Cmd {
		c := exec.Command("gh", "repo", "clone", nwo, dest)
		return tea.ExecProcess(c, func(err error) tea.Msg { return clonedMsg{nwo, dest, "", err} })
	}}
	return nil
}

// ---- drawing ----

func shortNum(n int) string {
	switch {
	case n >= 1000000:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprint(n)
}

func (m *model) expRepoRow(r *Repo, w int, sel bool) string {
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
	ic := st.Foreground(cMut).Render(icRepo)
	if r.ViewerHasStarred {
		ic = st.Foreground(cYellow).Render(icStar)
	}
	left := ic + st.Render(" ") + st.Foreground(nc).Bold(sel).Render(r.NameWithOwner)
	right := ""
	if l := r.Lang(); l != "" {
		right += st.Foreground(cMut).Render(l) + st.Render("  ")
	}
	right += st.Foreground(cYellow).Render(icStar) + st.Foreground(cMut).Render(" "+fmt.Sprintf("%5s", shortNum(r.Stars)))
	space := w - width(left) - width(right) - 1
	if space < 1 {
		left = trunc(left, w-width(right)-2)
		space = w - width(left) - width(right) - 1
	}
	return left + st.Render(strings.Repeat(" ", max(space, 0)+1)) + right
}

func (m *model) codeRow(c *codeHit, w int, sel bool) string {
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
	return pad(st.Foreground(cMut).Render(icFile)+st.Render(" ")+st.Foreground(nc).Bold(sel).Render(c.Path)+st.Render("  ")+st.Foreground(cMut).Render(c.Repo.FullName), w)
}

func (m *model) expListBox(w, h, y0 int) []string {
	m.addZone(0, y0, w-1, y0+h-1, "list", 0)
	title := ""
	x := 3
	for i, n := range expModes {
		if i > 0 {
			title += fg(cDim).Render("  ")
			x += 2
		}
		if i == m.exp.mode {
			title += fg(cAcc).Bold(true).Render(n)
		} else {
			title += fg(cMut).Render(n)
		}
		m.addZone(x, y0, x+width(n)-1, y0, "expmode", i)
		x += width(n)
	}
	inner := w - 4
	var body []string
	q := m.exp.query[m.exp.mode]
	if m.exp.mode > 0 && q != "" {
		qs := "  " + icSearch + " " + q
		title += fg(cTxt).Render(qs)
		m.addZone(x, y0, x+width(qs)-1, y0, "expsearch", 0)
	}
	if m.exp.mode == modeUsers && m.exp.user != "" {
		body = append(body, fg(cMut).Render("← ")+fg(cAcc).Render(m.exp.user)+fg(cMut).Render("  (esc: back to the results)"))
	}
	items := m.expItems()
	errText, loading := "", false
	switch m.exp.mode {
	case 0, 1:
		key := "starred"
		if m.exp.mode == 1 {
			key = "q:" + q
		}
		rs := m.expRepos[key]
		loading = rs == nil && (m.exp.mode == 0 || q != "")
		if rs != nil {
			errText = rs.err
		}
	case 2:
		il := m.issueLists[m.expIssueQuery()]
		loading = il == nil && q != ""
		if il != nil {
			errText = il.err
		}
	case 3:
		cs := m.expCode[q]
		loading = cs == nil && q != ""
		if cs != nil {
			errText = cs.err
		}
	case modeUsers:
		if m.exp.user != "" {
			p := m.profiles[strings.ToLower(m.exp.user)]
			loading = p == nil
			if p != nil {
				errText = p.err
			}
		} else {
			us := m.expUsers[q]
			loading = us == nil && q != ""
			if us != nil {
				errText = us.err
			}
		}
	}
	switch {
	case loading:
		body = append(body, "", fg(cMut).Render("Searching…"))
	case errText != "":
		body = append(body, "", fg(cRed).Render(errText))
	case len(items) == 0 && m.exp.mode == 0:
		body = append(body, "", fg(cMut).Render("You haven't starred anything yet."), fg(cMut).Render("/ searches GitHub; s stars a repo."))
	case len(items) == 0 && q != "":
		body = append(body, "", fg(cMut).Render("Nothing found."))
	case len(items) == 0:
		body = append(body, "", fg(cTxt).Render("Press / to search GitHub."), "", fg(cMut).Render("Examples: termgit, language:go stars:>500,"), fg(cMut).Render("bubbletea user:charmbracelet"))
	}
	rows := h - 2 - len(body)
	rowY := y0 + 1 + len(body)
	if m.exp.cur < m.exp.off {
		m.exp.off = m.exp.cur
	}
	if rows > 0 && m.exp.cur >= m.exp.off+rows {
		m.exp.off = m.exp.cur - rows + 1
	}
	for i := m.exp.off; i < len(items) && i < m.exp.off+rows; i++ {
		it := items[i]
		sel := i == m.exp.cur
		switch {
		case it.repo != nil:
			body = append(body, m.expRepoRow(it.repo, inner, sel))
		case it.issue != nil:
			body = append(body, m.issueRow(it.issue, inner, sel, true))
		case it.code != nil:
			body = append(body, m.codeRow(it.code, inner, sel))
		case it.user != nil:
			body = append(body, m.userRow(it.user, inner, sel))
		case it.profile != nil:
			st := fg(cAcc).Bold(true)
			if sel {
				st = st.Background(cDim)
			}
			body = append(body, pad(st.Render(icUser+" "+it.profile.Login+"'s profile"), inner))
		}
		y := rowY + (i - m.exp.off)
		m.addZone(1, y, w-2, y, "exprow", i)
	}
	note := ""
	if len(items) > 0 {
		note = fmt.Sprintf("%d/%d", m.exp.cur+1, len(items))
	}
	return box(title, w, h, body, m.focus == 0, note)
}

func (m *model) expPreview(w int) (string, []string) {
	it, ok := m.expSel()
	if !ok {
		return "Explore", []string{
			fg(cAcc).Bold(true).Render("Explore GitHub"), "",
			fg(cMut).Render("Starred: the repos you starred."),
			fg(cMut).Render("Repos / Issues / Code: search all of GitHub (f or click to switch, / to type)."), "",
			fg(cMut).Render("GitHub's search words work: language:go  stars:>1000  user:name  in:readme  is:closed …"),
		}
	}
	switch {
	case it.repo != nil:
		r := it.repo
		out := m.repoPreview(r, w)
		star := "star"
		if r.ViewerHasStarred {
			star = "unstar"
		}
		h := hints([][2]string{{"enter", "open"}, {"s", star}, {"F", "fork"}, {"c", "clone"}, {"v", "read README"}})
		return "Repo", append([]string{h, ""}, out...)
	case it.user != nil:
		return "Profile", append([]string{hints([][2]string{{"enter", "their profile and repos"}, {"o", "browser"}}), ""}, m.profileLines(m.profiles[strings.ToLower(it.user.Login)], w)...)
	case it.profile != nil:
		return "Profile", m.profileLines(it.profile, w)
	case it.issue != nil:
		return fmt.Sprintf("Issue #%d", it.issue.Number), m.issuePreview(it.issue, w)
	case it.code != nil:
		c := it.code
		out := []string{fg(cAcc).Bold(true).Render(c.Path), fg(cMut).Render(c.Repo.FullName), hints([][2]string{{"enter", "open in its repo"}, {"o", "browser"}}), sepLine(w)}
		b := m.blobs[c.Sha]
		switch {
		case b == nil:
			return "Code", append(out, fg(cMut).Render("Loading…"))
		case b.err != "":
			return "Code", append(out, fg(cRed).Render(b.err))
		case b.binary:
			return "Code", append(out, fg(cMut).Render("A binary file."))
		}
		lines := m.cachedLines("hl:"+c.Sha, func() []string { return highlight(c.Name, b.text) })
		// mark the lines with what you searched for, and start at the first one
		words := strings.Fields(strings.ToLower(m.exp.query[3]))
		first := -1
		raw := strings.Split(strings.ReplaceAll(b.text, "\r\n", "\n"), "\n")
		var marked []string
		for i, l := range lines {
			hit := false
			if i < len(raw) {
				low := strings.ToLower(raw[i])
				for _, wd := range words {
					if !strings.Contains(wd, ":") && strings.Contains(low, wd) {
						hit = true
					}
				}
			}
			if hit {
				if first < 0 {
					first = i
				}
				l = fg(cYellow).Render("▌") + l
			} else {
				l = " " + l
			}
			marked = append(marked, l)
		}
		if first > 3 && m.exp.jumped != c.Sha {
			m.exp.jumped = c.Sha
			m.pvOff = first + len(out) - 3
		}
		return "Code", append(out, marked...)
	}
	return "Explore", nil
}

// esc in Explore: one step back (profile → results → clear search → Starred)
func (m *model) expBack() tea.Cmd {
	switch {
	case m.focus == 1:
		m.focus = 0
		return nil
	case m.exp.mode == modeUsers && m.exp.user != "":
		m.exp.user, m.exp.cur, m.exp.off = "", 0, 0
	case m.exp.query[m.exp.mode] != "":
		m.exp.query[m.exp.mode], m.exp.cur, m.exp.off = "", 0, 0
	case m.exp.mode != 0:
		m.exp.mode, m.exp.cur, m.exp.off = 0, 0, 0
	default:
		return nil
	}
	return m.expSelected()
}
