package main

// Issues: the Issues tab (across your repos) and a repo's Issues section.
// Read the conversation, comment, create, close / reopen, labels, edit.
// Longer text is written in Nvim.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/cli/go-gh/v2/pkg/api"
)

type Label struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

type Issue struct {
	ID          string    `json:"id"`
	Number      int       `json:"number"`
	Title       string    `json:"title"`
	Body        string    `json:"body"`
	State       string    `json:"state"` // OPEN / CLOSED
	StateReason string    `json:"stateReason"`
	URL         string    `json:"url"`
	Created     time.Time `json:"createdAt"`
	Updated     time.Time `json:"updatedAt"`
	Closed      time.Time `json:"closedAt"`
	Author      *struct {
		Login string `json:"login"`
	} `json:"author"`
	Repository struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	Labels struct {
		Nodes []Label `json:"nodes"`
	} `json:"labels"`
	Comments struct {
		TotalCount int `json:"totalCount"`
	} `json:"comments"`
}

func (i *Issue) repo() string { return i.Repository.NameWithOwner }
func (i *Issue) open() bool   { return i.State == "OPEN" }
func (i *Issue) who() string {
	if i.Author != nil {
		return i.Author.Login
	}
	return "ghost"
}

type IssueComment struct {
	ID     string `json:"id"`
	URL    string `json:"url"`
	Author *struct {
		Login string `json:"login"`
	} `json:"author"`
	Body      string     `json:"body"`
	Created   time.Time  `json:"createdAt"`
	Reactions []Reaction `json:"reactionGroups"`
}

func (c *IssueComment) who() string {
	if c.Author != nil {
		return c.Author.Login
	}
	return "ghost"
}

type issueList struct {
	items []Issue
	err   string
}

type commentList struct {
	items []IssueComment
	err   string
}

const issueSearch = `query($q: String!) {
  search(query: $q, type: ISSUE, first: 60) {
    nodes {
      ... on Issue {
        id number title body state stateReason url createdAt updatedAt closedAt
        author { login }
        repository { nameWithOwner }
        labels(first: 10) { nodes { name color } }
        comments { totalCount }
      }
    }
  }
}`

func fetchIssues(q string) func() func(*model) {
	return func() func(*model) {
		il := &issueList{}
		c, err := api.DefaultGraphQLClient()
		if err == nil {
			var r struct {
				Search struct {
					Nodes []Issue
				}
			}
			if err = c.Do(issueSearch, map[string]interface{}{"q": q}, &r); err == nil {
				for _, n := range r.Search.Nodes {
					if n.Number > 0 { // pull requests come back as empty nodes
						il.items = append(il.items, n)
					}
				}
			}
		}
		if err != nil {
			il.err = apiErr(err)
		}
		return func(m *model) { m.issueLists[q] = il }
	}
}

const commentsQuery = `query($owner: String!, $name: String!, $n: Int!) {
  repository(owner: $owner, name: $name) {
    issue(number: $n) { comments(first: 100) { nodes { author { login } body createdAt } } }
  }
}`

func fetchComments(nwo string, n int) func() func(*model) {
	return func() func(*model) {
		cl := &commentList{}
		c, err := api.DefaultGraphQLClient()
		if err == nil {
			owner, name := splitNwo(nwo)
			var r struct {
				Repository struct {
					Issue *struct {
						Comments struct {
							Nodes []IssueComment
						}
					}
				}
			}
			if err = c.Do(commentsQuery, map[string]interface{}{"owner": owner, "name": name, "n": n}, &r); err == nil && r.Repository.Issue != nil {
				cl.items = r.Repository.Issue.Comments.Nodes
			}
		}
		if err != nil {
			cl.err = apiErr(err)
		}
		key := fmt.Sprintf("%s#%d", nwo, n)
		return func(m *model) { m.comments[key] = cl }
	}
}

// ---- which issues ----

type issState struct {
	cur, off  int
	scope     int
	closed    bool // show closed issues instead of open ones
	query     string
	filtering bool
}

var issScopes = []string{"Your repos", "Involved", "Assigned", "Created"}

func (m *model) stateQ() string {
	if m.iss.closed {
		return "is:closed"
	}
	return "is:open"
}

// the GitHub search for the Issues tab
func (m *model) issTabQuery() string {
	// your own repos, plus the org / shared repos in your list (search ORs these)
	scope := "user:" + m.login
	for _, r := range m.repos {
		if !strings.EqualFold(strings.SplitN(r.NameWithOwner, "/", 2)[0], m.login) && len(scope) < 200 {
			scope += " repo:" + r.NameWithOwner
		}
	}
	switch m.iss.scope {
	case 1:
		scope = "involves:@me"
	case 2:
		scope = "assignee:@me"
	case 3:
		scope = "author:@me"
	}
	if m.login == "" && m.iss.scope == 0 {
		scope = "involves:@me"
	}
	return scope + " is:issue " + m.stateQ() + " archived:false sort:updated-desc"
}

func (m *model) exIssueQuery() string {
	return "repo:" + m.ex.repo.NameWithOwner + " is:issue " + m.stateQ() + " sort:updated-desc"
}

func (m *model) issItems() []*Issue {
	il := m.issueLists[m.issTabQuery()]
	if il == nil {
		return nil
	}
	q := strings.ToLower(m.iss.query)
	var out []*Issue
	for i := range il.items {
		is := &il.items[i]
		if q == "" || fuzzy(strings.ToLower(is.repo()+" "+is.Title+fmt.Sprintf(" #%d", is.Number)), q) {
			out = append(out, is)
		}
	}
	return out
}

func (m *model) selIssue() *Issue {
	if m.tab == tabIssues {
		items := m.issItems()
		if m.iss.cur >= 0 && m.iss.cur < len(items) {
			return items[m.iss.cur]
		}
		return nil
	}
	if ex := m.exActive(); ex != nil && ex.sec == secIssues {
		if it, ok := m.exSel(); ok && it.kind == "issue" {
			return it.issue
		}
	}
	return nil
}

func (m *model) loadComments(is *Issue, debounce bool) tea.Cmd {
	if is == nil {
		return nil
	}
	return m.load(fmt.Sprintf("comments:%s#%d", is.repo(), is.Number), debounce, fetchComments(is.repo(), is.Number))
}

func (m *model) issLoad() tea.Cmd {
	q := m.issTabQuery()
	return tea.Batch(m.load("issues:"+q, false, fetchIssues(q)), m.loadComments(m.selIssue(), true))
}

func (m *model) issSelected() tea.Cmd {
	m.pvOff = 0
	n := len(m.issItems())
	m.iss.cur = max(0, min(m.iss.cur, n-1))
	rows := m.listRows() - 1
	if m.iss.filtering || m.iss.query != "" {
		rows--
	}
	if m.iss.cur < m.iss.off {
		m.iss.off = m.iss.cur
	}
	if rows > 0 && m.iss.cur >= m.iss.off+rows {
		m.iss.off = m.iss.cur - rows + 1
	}
	return m.issLoad()
}

func (m *model) issMove(d int) tea.Cmd {
	n := len(m.issItems())
	if n == 0 {
		return nil
	}
	old := m.iss.cur
	m.iss.cur = max(0, min(n-1, old+d))
	if m.iss.cur == old {
		return nil
	}
	return m.issSelected()
}

// forget loaded issue lists (and one conversation) so they're fetched again
func (m *model) issForget(nwo string, n int) {
	for k := range m.loaded {
		if strings.HasPrefix(k, "issues:") || strings.HasPrefix(k, "prs:") {
			delete(m.loaded, k)
		}
	}
	for _, p := range []string{"comments:", "prdetail:", "prfiles:"} {
		delete(m.loaded, fmt.Sprintf("%s%s#%d", p, nwo, n))
	}
}

func (m *model) issReload() tea.Cmd {
	if m.tab == tabIssues {
		return m.issLoad()
	}
	if m.tab == tabPRs {
		return m.prLoad()
	}
	if m.exActive() != nil {
		return m.exLoad()
	}
	return nil
}

// ---- keys ----

// issue keys shared by the Issues tab and a repo's Issues section
func (m *model) issueKey(s string) (tea.Cmd, bool) {
	switch s {
	case "C":
		return m.issComment(), true
	case "N":
		return m.issNew(), true
	case "x":
		return m.issToggle(), true
	case "t":
		return m.issLabels(), true
	case "E":
		return m.issEdit(), true
	case "s":
		m.iss.closed = !m.iss.closed
		m.iss.cur, m.iss.off = 0, 0
		if ex := m.exActive(); ex != nil {
			ex.cur[secIssues], ex.off[secIssues] = 0, 0
			return m.exSelected(), true
		}
		return m.issSelected(), true
	case "enter", "v":
		return m.issRead(), true
	}
	return nil, false
}

func (m *model) issTabKey(s string) tea.Cmd {
	if cmd, ok := m.issueKey(s); ok {
		return cmd
	}
	half := m.listRows() / 2
	switch s {
	case "tab", "shift+tab":
		m.focus = 1 - m.focus
	case "f":
		m.iss.scope = (m.iss.scope + 1) % len(issScopes)
		m.iss.cur, m.iss.off = 0, 0
		return m.issSelected()
	case "/":
		m.iss.filtering, m.focus = true, 0
	case "r":
		m.issForget("", 0)
		if is := m.selIssue(); is != nil {
			m.issForget(is.repo(), is.Number)
		}
		return m.issLoad()
	case "o":
		return m.actBrowser()
	case "y":
		return m.actCopy()
	case "l", "right":
		m.focus = 1
	case "h", "left":
		m.focus = 0
	case "j", "down":
		if m.focus == 0 {
			return m.issMove(1)
		}
		m.scroll(1)
	case "k", "up":
		if m.focus == 0 {
			return m.issMove(-1)
		}
		m.scroll(-1)
	case "pgdown", "space":
		if m.focus == 0 {
			return m.issMove(half * 2)
		}
		m.scroll(half * 2)
	case "pgup":
		if m.focus == 0 {
			return m.issMove(-half * 2)
		}
		m.scroll(-half * 2)
	case "g", "home":
		if m.focus == 0 {
			return m.issMove(-1 << 20)
		}
		m.pvOff = 0
	case "G", "end":
		if m.focus == 0 {
			return m.issMove(1 << 20)
		}
		m.pvOff = m.pvMax
	}
	return nil
}

// ---- writing in Nvim ----

type editedMsg struct {
	text string
	err  error
	then func(m *model, text string) tea.Cmd
}

var htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// editInNvim opens Nvim on a temp file; then() gets what you wrote (without the <!-- help --> line)
func (m *model) editInNvim(name, initial string, then func(m *model, text string) tea.Cmd) tea.Cmd {
	f := filepath.Join(os.TempDir(), "termgit-"+name+".md")
	os.WriteFile(f, []byte(initial), 0o644)
	if m.dump {
		return func() tea.Msg { return editedMsg{"(written in Nvim)", nil, then} }
	}
	c := exec.Command("nvim", "+", f) // + = start at the end of the file
	return tea.ExecProcess(c, func(err error) tea.Msg {
		data, _ := os.ReadFile(f)
		return editedMsg{strings.TrimSpace(htmlComment.ReplaceAllString(string(data), "")), err, then}
	})
}

func (m *model) onEdited(msg editedMsg) tea.Cmd {
	if msg.err != nil {
		return m.setStatus("Nvim stopped: "+msg.err.Error(), true)
	}
	return msg.then(m, msg.text)
}

type issueDoneMsg struct {
	nwo string
	n   int
	msg string
	err error
}

// gh runs gh in the background; body (if any) is passed as a file
func (m *model) gh(nwo string, n int, ok, body string, args ...string) tea.Cmd {
	if body != "" {
		f := filepath.Join(os.TempDir(), "termgit-body.md")
		os.WriteFile(f, []byte(body), 0o644)
		args = append(args, "--body-file", f)
	}
	if m.dump {
		return m.setStatus("Would run gh "+strings.Join(args, " "), false)
	}
	return tea.Batch(m.setStatus("Sending…", false), func() tea.Msg {
		out, err := exec.Command("gh", args...).CombinedOutput()
		if err != nil {
			err = fmt.Errorf("%s", strings.TrimSpace(string(out)))
		}
		return issueDoneMsg{nwo, n, ok, err}
	})
}

type issReloadMsg struct{}

func (m *model) onIssueDone(msg issueDoneMsg) tea.Cmd {
	if msg.err != nil {
		return m.setStatus(msg.err.Error(), true)
	}
	m.issForget(msg.nwo, msg.n)
	// GitHub's search takes a moment to see changes: load now and again in 3 seconds
	return tea.Batch(m.setStatus(msg.msg, false), m.issReload(),
		tea.Tick(3*time.Second, func(time.Time) tea.Msg { return issReloadMsg{} }))
}

const writeHelp = "<!-- %s Save and quit (:wq) to continue. Leave it empty to cancel. -->\n\n"

func (m *model) issComment() tea.Cmd {
	is := m.selIssue()
	if is == nil {
		return nil
	}
	nwo, n := is.repo(), is.Number
	return m.editInNvim("comment", fmt.Sprintf(writeHelp, fmt.Sprintf("Your comment on #%d %s.", n, is.Title)), func(m *model, text string) tea.Cmd {
		if text == "" {
			return m.setStatus("Nothing written: no comment sent", false)
		}
		m.prompt = &prompt{label: fmt.Sprintf("Post your comment on #%d?", n), confirm: true, done: func(m *model, _ string) tea.Cmd {
			return m.gh(nwo, n, fmt.Sprintf("Commented on #%d", n), text, "issue", "comment", fmt.Sprint(n), "-R", nwo)
		}}
		return nil
	})
}

func (m *model) issEdit() tea.Cmd {
	is := m.selIssue()
	if is == nil {
		return nil
	}
	nwo, n := is.repo(), is.Number
	return m.editInNvim("issue", fmt.Sprintf(writeHelp, fmt.Sprintf("The description of #%d.", n))+is.Body, func(m *model, text string) tea.Cmd {
		if strings.TrimSpace(text) == strings.TrimSpace(is.Body) {
			return m.setStatus("No changes", false)
		}
		m.prompt = &prompt{label: fmt.Sprintf("Save the new description of #%d?", n), confirm: true, done: func(m *model, _ string) tea.Cmd {
			return m.gh(nwo, n, fmt.Sprintf("Updated #%d", n), text, "issue", "edit", fmt.Sprint(n), "-R", nwo)
		}}
		return nil
	})
}

func (m *model) issToggle() tea.Cmd {
	is := m.selIssue()
	if is == nil {
		return nil
	}
	nwo, n := is.repo(), is.Number
	if is.open() {
		m.prompt = &prompt{label: fmt.Sprintf("Close #%d %s?", n, is.Title), confirm: true, done: func(m *model, _ string) tea.Cmd {
			return m.gh(nwo, n, fmt.Sprintf("Closed #%d", n), "", "issue", "close", fmt.Sprint(n), "-R", nwo)
		}}
	} else {
		m.prompt = &prompt{label: fmt.Sprintf("Reopen #%d %s?", n, is.Title), confirm: true, done: func(m *model, _ string) tea.Cmd {
			return m.gh(nwo, n, fmt.Sprintf("Reopened #%d", n), "", "issue", "reopen", fmt.Sprint(n), "-R", nwo)
		}}
	}
	return nil
}

// N: repo (in the Issues tab) → title → description in Nvim (or none) → create
func (m *model) issNew() tea.Cmd {
	ask := func(m *model, nwo string) tea.Cmd {
		m.prompt = &prompt{label: "New issue in " + nwo + ", title:", done: func(m *model, title string) tea.Cmd {
			if title == "" {
				return nil
			}
			create := func(m *model, body string) tea.Cmd {
				args := []string{"issue", "create", "-R", nwo, "--title", title}
				if body == "" {
					args = append(args, "--body", "")
				}
				return m.gh(nwo, 0, "Created \""+title+"\"", body, args...)
			}
			m.prompt = &prompt{label: "Write a description in Nvim? (n = create it with the title only)", confirm: true,
				no: func(m *model) tea.Cmd { return create(m, "") },
				done: func(m *model, _ string) tea.Cmd {
					return m.editInNvim("new-issue", fmt.Sprintf(writeHelp, "Describe the issue: "+title+"."), func(m *model, text string) tea.Cmd {
						return create(m, text)
					})
				}}
			return nil
		}}
		return nil
	}
	if ex := m.exActive(); ex != nil {
		return ask(m, ex.repo.NameWithOwner)
	}
	guess := ""
	if is := m.selIssue(); is != nil {
		guess = is.repo()
	} else if len(m.repos) > 0 {
		guess = m.repos[0].NameWithOwner
	}
	m.prompt = &prompt{label: "New issue in which repo:", text: guess, done: func(m *model, nwo string) tea.Cmd {
		if !strings.Contains(nwo, "/") {
			nwo = m.login + "/" + nwo
		}
		return ask(m, nwo)
	}}
	return nil
}

type repoLabelsMsg struct {
	nwo  string
	n    int
	have map[string]bool
	all  []Label
	err  error
}

// t: tick labels on / off in the picker
func (m *model) issLabels() tea.Cmd {
	is := m.selIssue()
	if is == nil {
		return nil
	}
	nwo, n := is.repo(), is.Number
	have := map[string]bool{}
	for _, l := range is.Labels.Nodes {
		have[l.Name] = true
	}
	return func() tea.Msg {
		var all []Label
		c, err := api.DefaultRESTClient()
		if err == nil {
			err = c.Get("repos/"+nwo+"/labels?per_page=100", &all)
		}
		return repoLabelsMsg{nwo, n, have, all, err}
	}
}

func (m *model) onRepoLabels(msg repoLabelsMsg) tea.Cmd {
	if msg.err != nil {
		return m.setStatus("Couldn't list labels: "+apiErr(msg.err), true)
	}
	var items []command
	for _, l := range msg.all {
		l := l
		mark, flag, ok := "  ", "--add-label", "Added "+l.Name
		if msg.have[l.Name] {
			mark, flag, ok = "✓ ", "--remove-label", "Removed "+l.Name
		}
		nwo, n := msg.nwo, msg.n
		items = append(items, command{mark + l.Name, "", func(m *model) tea.Cmd {
			return m.gh(nwo, n, ok, "", "issue", "edit", fmt.Sprint(n), "-R", nwo, flag, l.Name)
		}})
	}
	if len(items) == 0 {
		return m.setStatus(msg.nwo+" has no labels", true)
	}
	m.openPicker(fmt.Sprintf("Labels of #%d (enter adds / removes one)", msg.n), items)
	return nil
}

// ---- reading the conversation full screen ----

func (m *model) issRead() tea.Cmd { return m.issReadFor(m.selIssue()) }

func (m *model) issReadFor(is *Issue) tea.Cmd {
	if is == nil {
		return nil
	}
	issue := *is
	key := fmt.Sprintf("%s#%d", is.repo(), is.Number)
	rd := &reader{title: fmt.Sprintf("#%d %s", is.Number, is.Title), sub: is.repo(), icon: icIssue,
		mdSrc: func(m *model) (string, bool) { return conversationMD(&issue, m.comments[key]), true }}
	m.openReader(rd)
	return m.loadComments(is, false)
}

// the issue and its comments as one markdown page; every message is a heading (Contents jumps between them)
func conversationMD(is *Issue, cl *commentList) string {
	var b strings.Builder
	body := strings.TrimSpace(is.Body)
	if body == "" {
		body = "_No description._"
	}
	fmt.Fprintf(&b, "## %s opened this %s ago\n\n%s\n\n", is.who(), ago(is.Created), body)
	switch {
	case cl == nil:
		b.WriteString("_Loading comments…_\n")
	case cl.err != "":
		b.WriteString("_Couldn't load comments: " + cl.err + "_\n")
	}
	if cl != nil {
		for _, c := range cl.items {
			fmt.Fprintf(&b, "## %s · %s ago\n\n%s\n\n", c.who(), ago(c.Created), strings.TrimSpace(c.Body))
		}
	}
	if !is.open() {
		fmt.Fprintf(&b, "---\n\n_Closed %s ago._\n", ago(is.Closed))
	}
	return b.String()
}

// ---- drawing ----

func issueMark(is *Issue) (string, lipgloss.Style) {
	switch {
	case is.open():
		return icIssue, fg(cGreen)
	case is.StateReason == "NOT_PLANNED":
		return icIssueNo, fg(cMut)
	}
	return icIssueDone, fg(lipgloss.Color("#c678dd"))
}

func labelChip(l Label) string {
	c := lipgloss.Color("#" + l.Color)
	if l.Color == "" {
		c = cHint
	}
	return lipgloss.NewStyle().Foreground(c).Background(blend(c, 0.18)).Render(" " + l.Name + " ")
}

func (m *model) issueRow(is *Issue, w int, sel, showRepo bool) string {
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
	ic, ist := issueMark(is)
	left := ist.Inherit(st).Render(ic) + st.Render(" ")
	if showRepo {
		left += st.Foreground(cMut).Render(strings.SplitN(is.repo(), "/", 2)[1] + " ")
	}
	left += st.Foreground(cMut).Render(fmt.Sprintf("#%d ", is.Number)) + st.Foreground(nc).Bold(sel).Render(is.Title)
	right := ""
	if is.Comments.TotalCount > 0 {
		right = st.Foreground(cMut).Render(fmt.Sprintf("%s %d ", icComment, is.Comments.TotalCount))
	}
	right += st.Foreground(cMut).Render(fmt.Sprintf("%4s", ago(is.Updated)))
	space := w - width(left) - width(right) - 1
	if space < 1 {
		left = trunc(left, w-width(right)-2)
		space = w - width(left) - width(right) - 1
	}
	return left + st.Render(strings.Repeat(" ", max(space, 0)+1)) + right
}

func (m *model) issuePreview(is *Issue, w int) []string {
	out := wrapText(is.Title, w, fg(cAcc).Bold(true))
	ic, ist := issueMark(is)
	state := "Open"
	if !is.open() {
		state = "Closed"
		if is.StateReason == "NOT_PLANNED" {
			state = "Closed as not planned"
		}
	}
	meta := []string{
		ist.Render(ic + " " + state),
		fg(cMut).Render(fmt.Sprintf("#%d in %s", is.Number, is.repo())),
		fg(cMut).Render(is.who() + " opened it " + ago(is.Created) + " ago"),
	}
	out = append(out, wrapJoin(meta, "   ", w)...)
	if len(is.Labels.Nodes) > 0 {
		var chips []string
		for _, l := range is.Labels.Nodes {
			chips = append(chips, labelChip(l))
		}
		out = append(out, wrapJoin(chips, " ", w)...)
	}
	toggle := "close"
	if !is.open() {
		toggle = "reopen"
	}
	out = append(out, "", hints([][2]string{{"enter", "read"}, {"C", "comment"}, {"x", toggle}, {"t", "labels"}, {"E", "edit"}}), sepLine(w), "")
	body := strings.TrimSpace(is.Body)
	if body == "" {
		out = append(out, fg(cMut).Render("No description."))
	} else {
		out = append(out, m.cachedLines(fmt.Sprintf("iss:%s:%d:%d", is.ID, len(is.Body), w), func() []string { return renderMarkdown(body, w) })...)
	}
	key := fmt.Sprintf("%s#%d", is.repo(), is.Number)
	cl := m.comments[key]
	switch {
	case is.Comments.TotalCount == 0:
		return out
	case cl == nil:
		return append(out, "", fg(cMut).Render(fmt.Sprintf("Loading %d comments…", is.Comments.TotalCount)))
	case cl.err != "":
		return append(out, "", fg(cRed).Render("Couldn't load comments: "+cl.err))
	}
	for i, c := range cl.items {
		head := " " + c.who() + " · " + ago(c.Created) + " ago "
		out = append(out, "", fg(cHint).Render("──"+head+strings.Repeat("─", max(w-width(head)-2, 0))))
		out = append(out, m.cachedLines(fmt.Sprintf("cmt:%s:%d:%d:%d", key, i, len(c.Body), w), func() []string { return renderMarkdown(c.Body, w) })...)
	}
	return out
}

// ---- the Issues tab panels ----

func (m *model) issListBox(w, h, y0 int) []string {
	m.addZone(0, y0, w-1, y0+h-1, "list", 0)
	title := ""
	x := 3
	for i, n := range issScopes {
		if i > 0 {
			title += fg(cDim).Render("  ")
			x += 2
		}
		if i == m.iss.scope {
			title += fg(cAcc).Bold(true).Render(n)
		} else {
			title += fg(cMut).Render(n)
		}
		m.addZone(x, y0, x+width(n)-1, y0, "issscope", i)
		x += width(n)
	}
	inner := w - 4
	var body []string
	rowY := y0 + 1
	if m.iss.filtering || m.iss.query != "" {
		cur := ""
		if m.iss.filtering {
			cur = fg(cAcc).Render("▏")
		}
		line := fg(cAcc).Render(icSearch+" ") + fg(cTxt).Render(m.iss.query) + cur
		if m.iss.query == "" {
			line += fg(cMut).Render(" find an issue")
		}
		body = append(body, line)
		rowY++
	}
	il := m.issueLists[m.issTabQuery()]
	items := m.issItems()
	rows := h - 2 - len(body)
	switch {
	case il == nil:
		body = append(body, "", fg(cMut).Render("Loading issues…"))
	case il.err != "":
		body = append(body, "", fg(cRed).Render("Couldn't load issues:"), fg(cMut).Render(il.err))
	case len(items) == 0 && m.iss.query != "":
		body = append(body, "", fg(cMut).Render("Nothing matches \""+m.iss.query+"\""))
	case len(items) == 0 && m.iss.closed:
		body = append(body, "", fg(cMut).Render("No closed issues here."))
	case len(items) == 0:
		body = append(body, "", fg(cGreen).Render(icCheck+" No open issues"), fg(cMut).Render("N makes one, s shows closed ones."))
	}
	for i := m.iss.off; i < len(items) && i < m.iss.off+rows; i++ {
		body = append(body, m.issueRow(items[i], inner, i == m.iss.cur, true))
		y := rowY + (i - m.iss.off)
		m.addZone(1, y, w-2, y, "issrow", i)
	}
	state := "open"
	if m.iss.closed {
		state = "closed"
	}
	note := state
	if len(items) > 0 {
		note = fmt.Sprintf("%s · %d/%d", state, m.iss.cur+1, len(items))
	}
	return box(title, w, h, body, m.focus == 0, note)
}

func (m *model) issPreview(w int) (string, []string) {
	is := m.selIssue()
	if is == nil {
		return "Issue", []string{fg(cMut).Render("Pick an issue on the left.")}
	}
	return fmt.Sprintf("Issue #%d", is.Number), m.issuePreview(is, w)
}

// buttons for the Issues tab and a repo's Issues section
func (m *model) issButtons() []button {
	inIssues := m.tab == tabIssues || m.exActive() != nil && m.ex.sec == secIssues
	if !inIssues {
		return nil
	}
	var b []button
	if is := m.selIssue(); is != nil {
		toggle := button{icClose + " Close", "x", "isstoggle"}
		if !is.open() {
			toggle = button{icRefresh + " Reopen", "x", "isstoggle"}
		}
		b = append(b, button{icRead + " Read", "enter", "issread"}, button{icComment + " Comment", "C", "isscomment"},
			toggle, button{icTag + " Labels", "t", "isslabels"})
	}
	shown := "Closed"
	if m.iss.closed {
		shown = "Open"
	}
	b = append(b, button{icIssue + " New", "N", "issnew"}, button{"Show " + strings.ToLower(shown), "s", "issstate"},
		button{icGlobe + " Browser", "o", "browser"}, button{icRefresh + " Refresh", "r", "refresh"})
	return b
}
