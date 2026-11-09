package main

// Home: what needs you (failing CI, review requests, your open PRs, assigned issues,
// repos on this PC with work to push or pull, unread notifications) on the left;
// your contribution graph, numbers and recent activity on the right.

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/cli/go-gh/v2/pkg/api"
)

type contribDay struct {
	Date  string `json:"date"`
	Count int    `json:"contributionCount"`
}

type contribData struct {
	total int
	weeks [][]contribDay
	err   string
}

const contribQuery = `query {
  viewer { contributionsCollection { contributionCalendar {
    totalContributions
    weeks { contributionDays { date contributionCount } }
  } } }
}`

func fetchContrib() func() func(*model) {
	return func() func(*model) {
		cd := &contribData{}
		c, err := api.DefaultGraphQLClient()
		if err == nil {
			var r struct {
				Viewer struct {
					ContributionsCollection struct {
						ContributionCalendar struct {
							TotalContributions int
							Weeks              []struct {
								ContributionDays []contribDay
							}
						}
					}
				}
			}
			if err = c.Do(contribQuery, nil, &r); err == nil {
				cal := r.Viewer.ContributionsCollection.ContributionCalendar
				cd.total = cal.TotalContributions
				for _, w := range cal.Weeks {
					cd.weeks = append(cd.weeks, w.ContributionDays)
				}
			}
		}
		if err != nil {
			cd.err = apiErr(err)
		}
		return func(m *model) { m.contrib = cd }
	}
}

type Event struct {
	Type    string    `json:"type"`
	Created time.Time `json:"created_at"`
	Repo    struct {
		Name string `json:"name"`
	} `json:"repo"`
	Payload struct {
		Action  string `json:"action"`
		Ref     string `json:"ref"`
		RefType string `json:"ref_type"`
		Size    int    `json:"size"`
		Commits []struct {
			Message string `json:"message"`
		} `json:"commits"`
		Issue *struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
		} `json:"issue"`
		PullRequest *struct {
			Number int    `json:"number"`
			Title  string `json:"title"`
		} `json:"pull_request"`
		Release *struct {
			TagName string `json:"tag_name"`
		} `json:"release"`
	} `json:"payload"`
}

type eventList struct {
	items []Event
	err   string
}

func fetchEvents(login string) func() func(*model) {
	return func() func(*model) {
		el := &eventList{}
		c, err := api.DefaultRESTClient()
		if err == nil {
			err = c.Get("users/"+login+"/events?per_page=40", &el.items)
		}
		if err != nil {
			el.err = apiErr(err)
		}
		return func(m *model) { m.events = el }
	}
}

// ---- the "needs you" list ----

type homeItem struct {
	kind  string // ci, review, mypr, assigned, local, inbox
	icon  string
	color lipgloss.Style
	text  string
	right string
	run   *Run
	pr    *PR
	issue *Issue
	repo  string
}

const (
	qReview   = "review-requested:@me is:pr is:open archived:false sort:updated-desc"
	qMyPRs    = "author:@me is:pr is:open archived:false sort:updated-desc"
	qAssigned = "assignee:@me is:issue is:open archived:false sort:updated-desc"
)

func (m *model) homeLoad() tea.Cmd {
	cmds := []tea.Cmd{
		m.load("contrib", false, fetchContrib()),
		m.load("prs:"+qReview, false, fetchPRs(qReview)),
		m.load("prs:"+qMyPRs, false, fetchPRs(qMyPRs)),
		m.load("issues:"+qAssigned, false, fetchIssues(qAssigned)),
		m.load("notifs:false", false, fetchNotifs(false)),
		m.actLoadAll(),
	}
	if m.login != "" {
		cmds = append(cmds, m.load("events", false, fetchEvents(m.login)))
	}
	return tea.Batch(cmds...)
}

func (m *model) homeItems() []homeItem {
	var out []homeItem
	// failing CI: the newest run of a workflow on a branch failed
	var failing []*Run
	for _, nwo := range m.ciRepos() {
		if rl := m.runs[nwo]; rl != nil {
			for i := range rl.items {
				failing = append(failing, &rl.items[i])
			}
		}
	}
	runsOrder(failing)
	newest := map[string]bool{}
	for _, r := range failing {
		k := fmt.Sprintf("%s|%d|%s", r.Repo, r.WorkflowID, r.Branch)
		if newest[k] {
			continue
		}
		newest[k] = true
		if r.failed() && time.Since(r.Created) < 30*24*time.Hour {
			out = append(out, homeItem{kind: "ci", icon: "✗", color: fg(cRed), text: r.Name + " failed in " + shortRepo(r.Repo), right: ago(r.Created), run: r})
		}
	}
	if pl := m.prLists[qReview]; pl != nil {
		for i := range pl.items {
			p := &pl.items[i]
			out = append(out, homeItem{kind: "review", icon: icPR, color: fg(cYellow), text: fmt.Sprintf("Review #%d %s", p.Number, p.Title), right: shortRepo(p.repo()), pr: p})
		}
	}
	if pl := m.prLists[qMyPRs]; pl != nil {
		for i := range pl.items {
			p := &pl.items[i]
			st := ""
			switch p.checks() {
			case "FAILURE", "ERROR":
				st = " · checks ✗"
			case "SUCCESS":
				st = " · ✓"
			}
			if p.ReviewDecision == "APPROVED" {
				st += " · approved"
			}
			out = append(out, homeItem{kind: "mypr", icon: icPR, color: fg(cGreen), text: fmt.Sprintf("Your PR #%d %s%s", p.Number, p.Title, st), right: shortRepo(p.repo()), pr: p})
		}
	}
	if il := m.issueLists[qAssigned]; il != nil {
		for i := range il.items {
			is := &il.items[i]
			out = append(out, homeItem{kind: "assigned", icon: icIssue, color: fg(cGreen), text: fmt.Sprintf("Assigned #%d %s", is.Number, is.Title), right: shortRepo(is.repo()), issue: is})
		}
	}
	for _, r := range m.repos {
		li, ok := m.linfo[strings.ToLower(r.NameWithOwner)]
		if !ok {
			continue
		}
		var parts []string
		if li.dirty > 0 {
			parts = append(parts, fmt.Sprintf("%d changed", li.dirty))
		}
		if li.ahead > 0 {
			parts = append(parts, fmt.Sprintf("↑%d to push", li.ahead))
		}
		if li.missing {
			parts = append(parts, "↓ new on GitHub")
		} else if li.behind > 0 {
			parts = append(parts, fmt.Sprintf("↓%d to pull", li.behind))
		}
		if len(parts) > 0 {
			out = append(out, homeItem{kind: "local", icon: icFolder, color: fg(cOrange), text: r.Name + ": " + strings.Join(parts, ", "), right: "on this PC", repo: r.NameWithOwner})
		}
	}
	if m.unread > 0 {
		out = append(out, homeItem{kind: "inbox", icon: icInbox, color: fg(cAcc), text: fmt.Sprintf("%d unread %s", m.unread, plural(m.unread, "notification", "notifications")), right: "Inbox"})
	}
	return out
}

func shortRepo(nwo string) string {
	if i := strings.Index(nwo, "/"); i >= 0 {
		return nwo[i+1:]
	}
	return nwo
}

func (m *model) homeMove(d int) tea.Cmd {
	n := len(m.homeItems())
	if n == 0 {
		return nil
	}
	m.home.cur = max(0, min(n-1, m.home.cur+d))
	return nil
}

type homeState struct{ cur, off int }

// enter: go where that item is
func (m *model) homeOpen() tea.Cmd {
	items := m.homeItems()
	if m.home.cur >= len(items) {
		return nil
	}
	it := items[m.home.cur]
	switch it.kind {
	case "ci":
		id := it.run.ID
		return m.gotoRun(it.run.Repo, func(r *Run) bool { return r.ID == id })
	case "review", "mypr":
		return m.gotoPR(it.pr.repo(), it.pr.Number, true)
	case "assigned":
		return m.gotoIssue(it.issue.repo(), it.issue.Number, true)
	case "local":
		m.setTab(tabRepos)
		m.filter, m.filtering = "", false
		m.refilter()
		for i, idx := range m.view {
			if m.repos[idx].NameWithOwner == it.repo {
				m.cur = i
			}
		}
		m.ex = nil
		cmd := m.openRepo()
		if m.ex != nil {
			return tea.Batch(cmd, m.exSetSec(secChanges))
		}
		return cmd
	case "inbox":
		return m.setTab(tabInbox)
	}
	return nil
}

func (m *model) homeKey(s string) tea.Cmd {
	switch s {
	case "enter":
		return m.homeOpen()
	case "j", "down":
		if m.focus == 0 {
			return m.homeMove(1)
		}
		m.scroll(1)
	case "k", "up":
		if m.focus == 0 {
			return m.homeMove(-1)
		}
		m.scroll(-1)
	case "g", "home":
		m.home.cur = 0
	case "G", "end":
		m.home.cur = max(0, len(m.homeItems())-1)
	case "tab", "shift+tab":
		m.focus = 1 - m.focus
	case "l", "right":
		m.focus = 1
	case "h", "left":
		m.focus = 0
	case "r":
		for _, k := range []string{"contrib", "events", "prs:" + qReview, "prs:" + qMyPRs, "issues:" + qAssigned} {
			delete(m.loaded, k)
		}
		m.inboxForget()
		for k := range m.loaded {
			if strings.HasPrefix(k, "runs:") {
				delete(m.loaded, k)
			}
		}
		return tea.Batch(m.homeLoad(), m.scanLocalInfo())
	case "o":
		items := m.homeItems()
		if m.home.cur < len(items) {
			it := items[m.home.cur]
			switch {
			case it.run != nil:
				return m.openURL(it.run.URL)
			case it.pr != nil:
				return m.openURL(it.pr.URL)
			case it.issue != nil:
				return m.openURL(it.issue.URL)
			}
		}
	}
	return nil
}

// ---- drawing ----

func (m *model) homeListBox(w, h, y0 int) []string {
	m.addZone(0, y0, w-1, y0+h-1, "list", 0)
	items := m.homeItems()
	inner := w - 4
	var body []string
	if len(items) == 0 {
		loading := m.prLists[qReview] == nil || m.prLists[qMyPRs] == nil
		if loading {
			body = append(body, "", fg(cMut).Render("Looking at what needs you…"))
		} else {
			body = append(body, "", fg(cGreen).Render(icCheck+" Nothing needs you right now"), "",
				fg(cMut).Render("No failing CI, no reviews waiting,"), fg(cMut).Render("nothing to push or pull."))
		}
	}
	m.home.cur = max(0, min(m.home.cur, len(items)-1))
	rows := h - 2
	if m.home.cur < m.home.off {
		m.home.off = m.home.cur
	}
	if m.home.cur >= m.home.off+rows {
		m.home.off = m.home.cur - rows + 1
	}
	for i := m.home.off; i < len(items) && i < m.home.off+rows; i++ {
		it := items[i]
		sel := i == m.home.cur
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
		left := it.color.Inherit(st).Render(it.icon) + st.Render(" ") + st.Foreground(nc).Bold(sel).Render(it.text)
		right := st.Foreground(cMut).Render(trunc(it.right, 16))
		space := inner - width(left) - width(right) - 1
		if space < 1 {
			left = trunc(left, inner-width(right)-2)
			space = inner - width(left) - width(right) - 1
		}
		body = append(body, left+st.Render(strings.Repeat(" ", max(space, 0)+1))+right)
		y := y0 + 1 + (i - m.home.off)
		m.addZone(1, y, w-2, y, "homerow", i)
	}
	note := ""
	if len(items) > 0 {
		note = fmt.Sprintf("%d/%d", m.home.cur+1, len(items))
	}
	return box(icHome+" Needs you", w, h, body, m.focus == 0, note)
}

func contribColor(n, max int) lipgloss.Style {
	if n == 0 {
		return fg(cDim)
	}
	a := 0.35
	switch {
	case n*4 > max*3:
		a = 1
	case n*2 > max:
		a = 0.75
	case n*4 > max:
		a = 0.55
	}
	return fg(blend(cGreen, a))
}

func (m *model) contribLines(w int) []string {
	cd := m.contrib
	switch {
	case cd == nil:
		return []string{fg(cMut).Render("Loading your contributions…")}
	case cd.err != "":
		return []string{fg(cRed).Render("Couldn't load contributions: " + cd.err)}
	}
	// as many recent weeks as fit (2 columns each, after the day names)
	weeks := cd.weeks
	fit := (w - 4) / 2
	if len(weeks) > fit {
		weeks = weeks[len(weeks)-fit:]
	}
	most := 1
	for _, wk := range weeks {
		for _, d := range wk {
			most = max(most, d.Count)
		}
	}
	// month names above the weeks where a month starts
	months := []rune(strings.Repeat(" ", len(weeks)*2+4))
	last := ""
	for i, wk := range weeks {
		if len(wk) == 0 {
			continue
		}
		t, err := time.Parse("2006-01-02", wk[0].Date)
		if err != nil {
			continue
		}
		if mo := t.Format("Jan"); mo != last {
			last = mo
			if pos := 4 + i*2; pos+3 <= len(months) {
				copy(months[pos:], []rune(mo))
			}
		}
	}
	out := []string{fg(cMut).Render(strings.TrimRight(string(months), " "))}
	names := []string{"   ", "Mon", "   ", "Wed", "   ", "Fri", "   "}
	for day := 0; day < 7; day++ {
		line := fg(cMut).Render(names[day]) + " "
		for _, wk := range weeks {
			// GitHub weeks start on Sunday; the first week may be short
			idx := day - (7 - len(wk))
			if len(wk) == 7 {
				idx = day
			}
			if idx < 0 || idx >= len(wk) {
				line += "  "
				continue
			}
			line += contribColor(wk[idx].Count, most).Render("■") + " "
		}
		out = append(out, line)
	}
	legend := fg(cMut).Render("less ")
	for _, n := range []int{0, 1, most/3 + 1, most/2 + 1, most} {
		legend += contribColor(n, most).Render("■") + " "
	}
	out = append(out, "", fg(cTxt).Render(fmt.Sprintf("%d contributions in the last year", cd.total))+"   "+legend+fg(cMut).Render("more"))
	return out
}

func (m *model) eventLine(e Event) string {
	repo := shortRepo(e.Repo.Name)
	p := e.Payload
	var ic, text string
	st := fg(cHint)
	switch e.Type {
	case "PushEvent":
		n := p.Size
		if n == 0 {
			n = len(p.Commits)
		}
		ic, st = icCommit, fg(cGreen)
		text = fmt.Sprintf("pushed %d %s to %s in %s", n, plural(n, "commit", "commits"), strings.TrimPrefix(p.Ref, "refs/heads/"), repo)
		if n == 0 { // GitHub no longer always says how many
			text = fmt.Sprintf("pushed to %s in %s", strings.TrimPrefix(p.Ref, "refs/heads/"), repo)
		}
		if len(p.Commits) > 0 {
			text += fg(cMut).Render(": " + firstLine(p.Commits[len(p.Commits)-1].Message))
		}
	case "PullRequestEvent":
		ic, st = icPR, fg(cGreen)
		if p.PullRequest != nil {
			text = fmt.Sprintf("%s PR #%d %s in %s", p.Action, p.PullRequest.Number, p.PullRequest.Title, repo)
		}
	case "PullRequestReviewEvent":
		ic = icCheck
		if p.PullRequest != nil {
			text = fmt.Sprintf("reviewed PR #%d in %s", p.PullRequest.Number, repo)
		}
	case "IssuesEvent":
		ic, st = icIssue, fg(cGreen)
		if p.Issue != nil {
			text = fmt.Sprintf("%s issue #%d %s in %s", p.Action, p.Issue.Number, p.Issue.Title, repo)
		}
	case "IssueCommentEvent", "PullRequestReviewCommentEvent":
		ic = icComment
		n := 0
		if p.Issue != nil {
			n = p.Issue.Number
		} else if p.PullRequest != nil {
			n = p.PullRequest.Number
		}
		text = fmt.Sprintf("commented on #%d in %s", n, repo)
	case "CreateEvent":
		ic, st = icBranch, fg(cAcc)
		if p.RefType == "repository" {
			text = "created the repo " + repo
		} else {
			text = fmt.Sprintf("created %s %s in %s", p.RefType, p.Ref, repo)
		}
	case "DeleteEvent":
		ic, st = icClose, fg(cMut)
		text = fmt.Sprintf("deleted %s %s in %s", p.RefType, p.Ref, repo)
	case "ReleaseEvent":
		ic, st = icTag, fg(cAcc)
		if p.Release != nil {
			text = fmt.Sprintf("released %s of %s", p.Release.TagName, repo)
		}
	case "WatchEvent":
		ic, st = icStar, fg(cYellow)
		text = "starred " + e.Repo.Name
	case "ForkEvent":
		ic = icFork
		text = "forked " + e.Repo.Name
	default:
		ic = "·"
		text = strings.TrimSuffix(e.Type, "Event") + " in " + repo
	}
	return st.Render(ic) + " " + fg(cTxt).Render(text) + fg(cMut).Render("  "+ago(e.Created))
}

func (m *model) homePreview(w int) (string, []string) {
	hello := "Hi"
	if m.login != "" {
		hello = "Hi " + m.login
	}
	out := []string{fg(cAcc).Bold(true).Render(hello)}
	// numbers
	repos, private, local := len(m.repos), 0, 0
	for _, r := range m.repos {
		if r.IsPrivate {
			private++
		}
		if m.localPath(&r) != "" {
			local++
		}
	}
	nums := []string{
		fg(cTxt).Render(fmt.Sprint(repos)) + fg(cMut).Render(fmt.Sprintf(" repos (%d private)", private)),
		fg(cTxt).Render(fmt.Sprint(local)) + fg(cMut).Render(" on this PC"),
	}
	if pl := m.prLists[qMyPRs]; pl != nil {
		nums = append(nums, fg(cTxt).Render(fmt.Sprint(len(pl.items)))+fg(cMut).Render(" open "+plural(len(pl.items), "PR", "PRs")))
	}
	if m.unread > 0 {
		nums = append(nums, fg(cAcc).Render(fmt.Sprint(m.unread))+fg(cMut).Render(" unread"))
	}
	out = append(out, wrapJoin(nums, fg(cDim).Render("  ·  "), w)...)
	out = append(out, "", fg(cHint).Bold(true).Render("Contributions"), "")
	out = append(out, m.contribLines(w)...)
	out = append(out, "", sepLine(w), fg(cHint).Bold(true).Render("Recent activity"), "")
	el := m.events
	switch {
	case el == nil:
		out = append(out, fg(cMut).Render("Loading…"))
	case el.err != "":
		out = append(out, fg(cRed).Render(el.err))
	case len(el.items) == 0:
		out = append(out, fg(cMut).Render("Nothing yet."))
	}
	if el != nil {
		for _, e := range el.items {
			out = append(out, trunc(m.eventLine(e), w))
		}
	}
	return "Home", out
}
