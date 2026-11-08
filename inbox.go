package main

// Inbox: your GitHub notifications. Read an issue / PR conversation inside TermGit
// (it gets marked as read), mark read, mark all read, unsubscribe.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/cli/go-gh/v2/pkg/api"
)

type Notif struct {
	ID      string    `json:"id"`
	Unread  bool      `json:"unread"`
	Reason  string    `json:"reason"`
	Updated time.Time `json:"updated_at"`
	Subject struct {
		Title string `json:"title"`
		URL   string `json:"url"`
		Type  string `json:"type"` // Issue, PullRequest, Release, Commit, CheckSuite, Discussion
	} `json:"subject"`
	Repository struct {
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
	} `json:"repository"`
}

var numRe = regexp.MustCompile(`/(issues|pulls)/(\d+)$`)

// the issue / PR number this notification is about (0 if none)
func (n *Notif) number() int {
	if m := numRe.FindStringSubmatch(n.Subject.URL); m != nil {
		v, _ := strconv.Atoi(m[2])
		return v
	}
	return 0
}

func (n *Notif) webURL() string {
	u := n.Subject.URL
	if u == "" {
		return n.Repository.HTMLURL
	}
	u = strings.Replace(u, "https://api.github.com/repos/", "https://github.com/", 1)
	u = strings.Replace(u, "/pulls/", "/pull/", 1)
	if strings.Contains(u, "/commits/") {
		u = strings.Replace(u, "/commits/", "/commit/", 1)
	}
	if strings.Contains(u, "/releases/") {
		return n.Repository.HTMLURL + "/releases"
	}
	return u
}

var reasons = map[string]string{
	"assign": "you were assigned", "author": "you opened it", "comment": "you commented",
	"ci_activity": "a CI run finished", "invitation": "you were invited", "manual": "you subscribed",
	"mention": "you were mentioned", "review_requested": "your review was asked for",
	"security_alert": "security alert", "state_change": "it was closed or reopened",
	"subscribed": "you watch this repo", "team_mention": "your team was mentioned",
	"approval_requested": "your approval was asked for",
}

type notifList struct {
	items []Notif
	err   string
}

func fetchNotifs(all bool) func() func(*model) {
	return func() func(*model) {
		nl := &notifList{}
		c, err := api.DefaultRESTClient()
		if err == nil {
			err = c.Get(fmt.Sprintf("notifications?all=%v&per_page=50", all), &nl.items)
		}
		if err != nil {
			nl.err = apiErr(err)
		}
		return func(m *model) {
			m.notifs[all] = nl
			if !all {
				m.unread = len(nl.items)
			}
		}
	}
}

type inboxState struct {
	cur, off  int
	all       bool
	query     string
	filtering bool
}

func (m *model) inboxItems() []*Notif {
	nl := m.notifs[m.inbox.all]
	if nl == nil {
		return nil
	}
	q := strings.ToLower(m.inbox.query)
	var out []*Notif
	for i := range nl.items {
		n := &nl.items[i]
		if q == "" || fuzzy(strings.ToLower(n.Repository.FullName+" "+n.Subject.Title), q) {
			out = append(out, n)
		}
	}
	return out
}

func (m *model) selNotif() *Notif {
	items := m.inboxItems()
	if m.tab == tabInbox && m.inbox.cur >= 0 && m.inbox.cur < len(items) {
		return items[m.inbox.cur]
	}
	return nil
}

func (m *model) inboxLoad() tea.Cmd {
	k := "notifs:" + strconv.FormatBool(m.inbox.all)
	return tea.Batch(m.load(k, false, fetchNotifs(m.inbox.all)), m.loadSubject(m.selNotif(), true))
}

func (m *model) inboxForget() {
	delete(m.loaded, "notifs:true")
	delete(m.loaded, "notifs:false")
}

func (m *model) inboxSelected() tea.Cmd {
	m.pvOff = 0
	n := len(m.inboxItems())
	m.inbox.cur = max(0, min(m.inbox.cur, n-1))
	rows := m.listRows() - 1
	if m.inbox.cur < m.inbox.off {
		m.inbox.off = m.inbox.cur
	}
	if rows > 0 && m.inbox.cur >= m.inbox.off+rows {
		m.inbox.off = m.inbox.cur - rows + 1
	}
	return m.inboxLoad()
}

func (m *model) inboxMove(d int) tea.Cmd {
	n := len(m.inboxItems())
	if n == 0 {
		return nil
	}
	old := m.inbox.cur
	m.inbox.cur = max(0, min(n-1, old+d))
	if m.inbox.cur == old {
		return nil
	}
	return m.inboxSelected()
}

// ---- the issue / PR behind a notification ----

const subjectQuery = `query($o: String!, $n: String!, $num: Int!) {
  repository(owner: $o, name: $n) {
    issueOrPullRequest(number: $num) {
      __typename
      ... on Issue { id number title body state stateReason url createdAt updatedAt closedAt author { login } repository { nameWithOwner } labels(first: 10) { nodes { name color } } comments { totalCount } }
      ... on PullRequest {` + prFields + ` }
    }
  }
}`

type subject struct {
	isPR bool
	pr   PR // for an issue only the Issue part is filled
	err  string
}

func fetchSubject(nwo string, num int) func() func(*model) {
	return func() func(*model) {
		s := &subject{}
		c, err := api.DefaultGraphQLClient()
		if err == nil {
			o, n := splitNwo(nwo)
			var r struct {
				Repository struct {
					IssueOrPullRequest *struct {
						Typename string `json:"__typename"`
						PR
					} `json:"issueOrPullRequest"`
				}
			}
			if err = c.Do(subjectQuery, map[string]interface{}{"o": o, "n": n, "num": num}, &r); err == nil && r.Repository.IssueOrPullRequest != nil {
				s.isPR = r.Repository.IssueOrPullRequest.Typename == "PullRequest"
				s.pr = r.Repository.IssueOrPullRequest.PR
			}
		}
		if err != nil {
			s.err = apiErr(err)
		}
		key := fmt.Sprintf("%s#%d", nwo, num)
		return func(m *model) { m.subjects[key] = s }
	}
}

func (m *model) loadSubject(n *Notif, debounce bool) tea.Cmd {
	if n == nil || n.number() == 0 {
		return nil
	}
	return m.load(fmt.Sprintf("subject:%s#%d", n.Repository.FullName, n.number()), debounce, fetchSubject(n.Repository.FullName, n.number()))
}

func markRead(id string) error {
	c, err := api.DefaultRESTClient()
	if err != nil {
		return err
	}
	return c.Patch("notifications/threads/"+id, nil, nil)
}

type inboxDoneMsg struct {
	ok  string
	err error
}

func (m *model) inboxDo(ok string, fn func() error) tea.Cmd {
	if m.dump {
		return m.setStatus("Would: "+ok, false)
	}
	return func() tea.Msg {
		err := fn()
		if err != nil {
			err = fmt.Errorf("%s", apiErr(err))
		}
		return inboxDoneMsg{ok, err}
	}
}

func (m *model) onInboxDone(msg inboxDoneMsg) tea.Cmd {
	if msg.err != nil {
		return m.setStatus(msg.err.Error(), true)
	}
	m.inboxForget()
	return tea.Batch(m.setStatus(msg.ok, false), m.inboxLoad(), m.load("notifs:false", false, fetchNotifs(false)))
}

// enter: read it inside TermGit (and mark it read)
func (m *model) inboxOpen() tea.Cmd {
	n := m.selNotif()
	if n == nil {
		return nil
	}
	var cmds []tea.Cmd
	if n.Unread {
		id := n.ID
		n.Unread = false
		m.unread = max(0, m.unread-1)
		cmds = append(cmds, m.inboxDo("Marked as read", func() error { return markRead(id) }))
	}
	s := m.subjects[fmt.Sprintf("%s#%d", n.Repository.FullName, n.number())]
	switch {
	case n.number() == 0 || s == nil || s.err != "":
		cmds = append(cmds, m.openURL(n.webURL()))
	case s.isPR:
		p := s.pr
		m.prs.view = pvOverview
		cmds = append(cmds, m.prReadFor(&p))
	default:
		is := s.pr.Issue
		cmds = append(cmds, m.issReadFor(&is))
	}
	return tea.Batch(cmds...)
}

func (m *model) inboxKey(s string) tea.Cmd {
	half := m.listRows() / 2
	switch s {
	case "enter", "v":
		return m.inboxOpen()
	case "m":
		if n := m.selNotif(); n != nil && n.Unread {
			id := n.ID
			return m.inboxDo("Marked as read", func() error { return markRead(id) })
		}
	case "M":
		m.prompt = &prompt{label: "Mark every notification as read?", confirm: true, done: func(m *model, _ string) tea.Cmd {
			return m.inboxDo("All marked as read", func() error {
				c, err := api.DefaultRESTClient()
				if err != nil {
					return err
				}
				return c.Put("notifications", strings.NewReader(`{"read":true}`), nil)
			})
		}}
	case "x":
		if n := m.selNotif(); n != nil {
			id := n.ID
			m.prompt = &prompt{label: "Stop notifications for \"" + n.Subject.Title + "\"?", confirm: true, done: func(m *model, _ string) tea.Cmd {
				return m.inboxDo("Unsubscribed", func() error {
					c, err := api.DefaultRESTClient()
					if err != nil {
						return err
					}
					markRead(id)
					return c.Delete("notifications/threads/"+id+"/subscription", nil)
				})
			}}
		}
	case "a":
		m.inbox.all = !m.inbox.all
		m.inbox.cur, m.inbox.off = 0, 0
		return m.inboxSelected()
	case "/":
		m.inbox.filtering, m.focus = true, 0
	case "r":
		m.inboxForget()
		return m.inboxLoad()
	case "o":
		if n := m.selNotif(); n != nil {
			return m.openURL(n.webURL())
		}
	case "y":
		if n := m.selNotif(); n != nil {
			return m.copyText(n.webURL(), "Copied the link")
		}
	case "tab", "shift+tab":
		m.focus = 1 - m.focus
	case "l", "right":
		m.focus = 1
	case "h", "left":
		m.focus = 0
	case "j", "down":
		if m.focus == 0 {
			return m.inboxMove(1)
		}
		m.scroll(1)
	case "k", "up":
		if m.focus == 0 {
			return m.inboxMove(-1)
		}
		m.scroll(-1)
	case "pgdown", "space":
		if m.focus == 0 {
			return m.inboxMove(half * 2)
		}
		m.scroll(half * 2)
	case "pgup":
		if m.focus == 0 {
			return m.inboxMove(-half * 2)
		}
		m.scroll(-half * 2)
	case "g", "home":
		if m.focus == 0 {
			return m.inboxMove(-1 << 20)
		}
		m.pvOff = 0
	case "G", "end":
		if m.focus == 0 {
			return m.inboxMove(1 << 20)
		}
		m.pvOff = m.pvMax
	}
	return nil
}

// ---- drawing ----

func notifIcon(n *Notif) (string, lipgloss.Style) {
	switch n.Subject.Type {
	case "PullRequest":
		return icPR, fg(cGreen)
	case "Issue":
		return icIssue, fg(cGreen)
	case "Release":
		return icTag, fg(cAcc)
	case "CheckSuite":
		return icActions, fg(cYellow)
	case "Commit":
		return icCommit, fg(cHint)
	case "Discussion":
		return icComment, fg(cAcc)
	}
	return icInbox, fg(cHint)
}

func (m *model) notifRow(n *Notif, w int, sel bool) string {
	st := lipgloss.NewStyle()
	if sel {
		bg := cChip
		if m.focus == 0 {
			bg = cDim
		}
		st = st.Background(bg)
	}
	nc := cHint
	if n.Unread {
		nc = cTxt
	}
	if sel {
		nc = cAcc
	}
	ic, ist := notifIcon(n)
	dot := st.Render("  ")
	if n.Unread {
		dot = st.Foreground(cAcc).Render("●") + st.Render(" ")
	}
	repo := n.Repository.FullName
	if i := strings.Index(repo, "/"); i >= 0 {
		repo = repo[i+1:]
	}
	left := dot + ist.Inherit(st).Render(ic) + st.Render(" ") + st.Foreground(cMut).Render(repo+" ") + st.Foreground(nc).Bold(n.Unread || sel).Render(n.Subject.Title)
	right := st.Foreground(cMut).Render(fmt.Sprintf("%4s", ago(n.Updated)))
	space := w - width(left) - width(right) - 1
	if space < 1 {
		left = trunc(left, w-width(right)-2)
		space = w - width(left) - width(right) - 1
	}
	return left + st.Render(strings.Repeat(" ", max(space, 0)+1)) + right
}

func (m *model) inboxListBox(w, h, y0 int) []string {
	m.addZone(0, y0, w-1, y0+h-1, "list", 0)
	title := ""
	x := 3
	for i, n := range []string{"Unread", "All"} {
		if i > 0 {
			title += fg(cDim).Render("  ")
			x += 2
		}
		on := (i == 1) == m.inbox.all
		if on {
			title += fg(cAcc).Bold(true).Render(n)
		} else {
			title += fg(cMut).Render(n)
		}
		m.addZone(x, y0, x+width(n)-1, y0, "inboxall", i)
		x += width(n)
	}
	inner := w - 4
	var body []string
	rowY := y0 + 1
	if m.inbox.filtering || m.inbox.query != "" {
		cur := ""
		if m.inbox.filtering {
			cur = fg(cAcc).Render("▏")
		}
		line := fg(cAcc).Render(icSearch+" ") + fg(cTxt).Render(m.inbox.query) + cur
		if m.inbox.query == "" {
			line += fg(cMut).Render(" find a notification")
		}
		body = append(body, line)
		rowY++
	}
	nl := m.notifs[m.inbox.all]
	items := m.inboxItems()
	rows := h - 2 - len(body)
	switch {
	case nl == nil:
		body = append(body, "", fg(cMut).Render("Loading notifications…"))
	case nl.err != "":
		body = append(body, "", fg(cRed).Render("Couldn't load notifications:"), fg(cMut).Render(nl.err))
	case len(items) == 0 && !m.inbox.all:
		body = append(body, "", fg(cGreen).Render(icCheck+" All caught up"), fg(cMut).Render("a shows the ones you already read."))
	case len(items) == 0:
		body = append(body, "", fg(cMut).Render("No notifications."))
	}
	for i := m.inbox.off; i < len(items) && i < m.inbox.off+rows; i++ {
		body = append(body, m.notifRow(items[i], inner, i == m.inbox.cur))
		y := rowY + (i - m.inbox.off)
		m.addZone(1, y, w-2, y, "notifrow", i)
	}
	note := ""
	if len(items) > 0 {
		note = fmt.Sprintf("%d/%d", m.inbox.cur+1, len(items))
	}
	return box(title, w, h, body, m.focus == 0, note)
}

func (m *model) inboxPreview(w int) (string, []string) {
	n := m.selNotif()
	if n == nil {
		return "Notification", []string{fg(cMut).Render("Pick a notification on the left.")}
	}
	ic, ist := notifIcon(n)
	out := wrapText(n.Subject.Title, w, fg(cAcc).Bold(true))
	why := reasons[n.Reason]
	if why == "" {
		why = strings.ReplaceAll(n.Reason, "_", " ")
	}
	kind := map[string]string{"PullRequest": "pull request", "Issue": "issue", "CheckSuite": "CI run", "Release": "release", "Commit": "commit", "Discussion": "discussion"}[n.Subject.Type]
	if kind == "" {
		kind = strings.ToLower(n.Subject.Type)
	}
	meta := []string{ist.Render(ic + " " + kind), fg(cMut).Render(n.Repository.FullName), fg(cHint).Render("why: " + why), fg(cMut).Render(ago(n.Updated) + " ago")}
	if n.Unread {
		meta = append(meta, fg(cAcc).Render("● unread"))
	}
	out = append(out, wrapJoin(meta, "   ", w)...)
	open := "read it here"
	if n.number() == 0 {
		open = "open on GitHub"
	}
	out = append(out, "", hints([][2]string{{"enter", open}, {"m", "mark read"}, {"x", "unsubscribe"}, {"o", "browser"}}), sepLine(w), "")
	if n.number() == 0 {
		return "Notification", append(out, fg(cMut).Render("GitHub doesn't give more details for this kind: enter opens it in the browser."))
	}
	s := m.subjects[fmt.Sprintf("%s#%d", n.Repository.FullName, n.number())]
	switch {
	case s == nil:
		return "Notification", append(out, fg(cMut).Render("Loading…"))
	case s.err != "":
		return "Notification", append(out, fg(cRed).Render(s.err))
	case s.isPR:
		p := s.pr
		return fmt.Sprintf("PR #%d", p.Number), append(out, m.prOverview(&p, w)...)
	}
	is := s.pr.Issue
	return fmt.Sprintf("Issue #%d", is.Number), append(out, m.issuePreview(&is, w)...)
}
