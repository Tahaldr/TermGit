package main

// Home: a dashboard (number tiles, contribution graph, "Needs you", activity, recent
// repos) or, with t, the two-panel version. Rows have quick actions so most things
// can be done without leaving Home.

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type homeState struct {
	cur, off int
}

type homeItem struct {
	group string // CI failing, Review requests, Your PRs, Assigned, On this PC, Inbox, Recent repos
	kind  string // ci, review, mypr, assigned, local, inbox, recent
	icon  string
	color lipgloss.Style
	text  string
	right string
	keys  string // quick keys shown on the row
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

func shortRepo(nwo string) string {
	if i := strings.Index(nwo, "/"); i >= 0 {
		return nwo[i+1:]
	}
	return nwo
}

// the newest run of every workflow + branch (what CI looks like right now)
func (m *model) latestRuns() []*Run {
	var all []*Run
	for _, nwo := range m.ciRepos() {
		if rl := m.runs[nwo]; rl != nil {
			for i := range rl.items {
				all = append(all, &rl.items[i])
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].Created.After(all[j].Created) })
	seen := map[string]bool{}
	var out []*Run
	for _, r := range all {
		k := fmt.Sprintf("%s|%d|%s", r.Repo, r.WorkflowID, r.Branch)
		if !seen[k] {
			seen[k] = true
			out = append(out, r)
		}
	}
	return out
}

func (m *model) homeItems() []homeItem {
	var out []homeItem
	for _, r := range m.latestRuns() {
		if r.failed() && time.Since(r.Created) < 30*24*time.Hour {
			out = append(out, homeItem{group: "CI failing", kind: "ci", icon: "✗", color: fg(cRed),
				text: r.Name + " · " + shortRepo(r.Repo), right: ago(r.Created), keys: "R", run: r})
		}
	}
	if pl := m.prLists[qReview]; pl != nil {
		for i := range pl.items {
			p := &pl.items[i]
			out = append(out, homeItem{group: "Review requests", kind: "review", icon: icPR, color: fg(cYellow),
				text: fmt.Sprintf("#%d %s", p.Number, p.Title), right: shortRepo(p.repo()), keys: "R", pr: p})
		}
	}
	if pl := m.prLists[qMyPRs]; pl != nil {
		for i := range pl.items {
			p := &pl.items[i]
			st := ""
			switch p.checks() {
			case "FAILURE", "ERROR":
				st = " ✗"
			case "SUCCESS":
				st = " ✓"
			case "PENDING", "EXPECTED":
				st = " ●"
			}
			if p.ReviewDecision == "APPROVED" {
				st += " approved"
			}
			out = append(out, homeItem{group: "Your PRs", kind: "mypr", icon: icPR, color: fg(cGreen),
				text: fmt.Sprintf("#%d %s%s", p.Number, p.Title, st), right: shortRepo(p.repo()), keys: "m", pr: p})
		}
	}
	if il := m.issueLists[qAssigned]; il != nil {
		for i := range il.items {
			is := &il.items[i]
			out = append(out, homeItem{group: "Assigned to you", kind: "assigned", icon: icIssue, color: fg(cGreen),
				text: fmt.Sprintf("#%d %s", is.Number, is.Title), right: shortRepo(is.repo()), issue: is})
		}
	}
	for _, r := range m.repos {
		li, ok := m.linfo[strings.ToLower(r.NameWithOwner)]
		if !ok {
			continue
		}
		var parts []string
		keys := ""
		if li.dirty > 0 {
			parts = append(parts, fmt.Sprintf("%d changed", li.dirty))
			keys += "c "
		}
		if li.ahead > 0 {
			parts = append(parts, fmt.Sprintf("↑%d", li.ahead))
			keys += "P "
		}
		if li.missing {
			parts = append(parts, "↓ new")
			keys += "p "
		} else if li.behind > 0 {
			parts = append(parts, fmt.Sprintf("↓%d", li.behind))
			keys += "p "
		}
		if len(parts) > 0 {
			out = append(out, homeItem{group: "On this PC", kind: "local", icon: icFolder, color: fg(cOrange),
				text: r.Name + "  " + strings.Join(parts, " · "), keys: strings.TrimSpace(keys), repo: r.NameWithOwner})
		}
	}
	if m.unread > 0 {
		out = append(out, homeItem{group: "Inbox", kind: "inbox", icon: icInbox, color: fg(cAcc),
			text: fmt.Sprintf("%d unread %s", m.unread, plural(m.unread, "notification", "notifications")), keys: "M"})
	}
	for i, nwo := range m.recentRepos() {
		out = append(out, homeItem{group: "Recent repos", kind: "recent", icon: icRepo, color: fg(cHint),
			text: shortRepo(nwo), right: fmt.Sprintf("alt+%d", i+1), repo: nwo})
	}
	return out
}

// the repos you opened in TermGit last, then the ones pushed last (5)
func (m *model) recentRepos() []string {
	var out []string
	seen := map[string]bool{}
	add := func(n string) {
		if n != "" && !seen[strings.ToLower(n)] && len(out) < 5 {
			seen[strings.ToLower(n)] = true
			out = append(out, n)
		}
	}
	for _, n := range m.recent {
		add(n)
	}
	for _, r := range m.repos {
		add(r.NameWithOwner)
	}
	return out
}

func (m *model) rememberRepo(nwo string) {
	list := []string{nwo}
	for _, n := range m.recent {
		if !strings.EqualFold(n, nwo) && len(list) < 10 {
			list = append(list, n)
		}
	}
	m.recent = list
	if !m.dump {
		saveCache("recent.json", list)
	}
}

func (m *model) homeMove(d int) tea.Cmd {
	n := len(m.homeItems())
	if n == 0 {
		return nil
	}
	m.home.cur = max(0, min(n-1, m.home.cur+d))
	return nil
}

func (m *model) homeSel() *homeItem {
	items := m.homeItems()
	if m.home.cur >= 0 && m.home.cur < len(items) {
		return &items[m.home.cur]
	}
	return nil
}

func (m *model) openRecent(nwo string) tea.Cmd {
	from := m.tab
	m.setTab(tabRepos)
	m.backTo = from
	m.filter, m.filtering = "", false
	m.refilter()
	for i, idx := range m.view {
		if m.repos[idx].NameWithOwner == nwo {
			m.cur = i
		}
	}
	m.ex = nil
	return m.openRepo()
}

// enter: go where that item is
func (m *model) homeOpen() tea.Cmd {
	it := m.homeSel()
	if it == nil {
		return nil
	}
	switch it.kind {
	case "ci":
		id := it.run.ID
		return m.gotoRun(it.run.Repo, func(r *Run) bool { return r.ID == id })
	case "review", "mypr":
		return m.gotoPR(it.pr.repo(), it.pr.Number, true)
	case "assigned":
		return m.gotoIssue(it.issue.repo(), it.issue.Number, true)
	case "local":
		return m.gotoItem(it.repo, secChanges, "", func(exItem) bool { return true }, nil)
	case "inbox":
		return m.setTab(tabInbox)
	case "recent":
		return m.openRecent(it.repo)
	}
	return nil
}

// quick actions on the highlighted row
func (m *model) homeQuick(s string) (tea.Cmd, bool) {
	it := m.homeSel()
	if it == nil {
		return nil, false
	}
	dir := ""
	if it.kind == "local" {
		for i := range m.repos {
			if m.repos[i].NameWithOwner == it.repo {
				dir = m.localPath(&m.repos[i])
			}
		}
	}
	switch {
	case s == "R" && it.kind == "ci":
		return m.actRerunFor(it.run), true
	case s == "R" && it.kind == "review":
		return m.prReviewFor(it.pr), true
	case s == "m" && (it.kind == "mypr" || it.kind == "review"):
		return m.prMergeFor(it.pr), true
	case s == "c" && dir != "":
		// commit: open its Changes with the commit question
		cmd := m.gotoItem(it.repo, secChanges, "", func(exItem) bool { return true }, nil)
		fetchChanges(dir)()(m)
		return tea.Batch(cmd, m.chCommit(false)), true
	case s == "p" && dir != "":
		return tea.Batch(m.setStatus("Pulling "+shortRepo(it.repo)+"…", false), m.git(dir, "Pulled "+shortRepo(it.repo), "pull", "pull", "--ff-only")), true
	case s == "P" && dir != "":
		return tea.Batch(m.setStatus("Pushing "+shortRepo(it.repo)+"…", false), m.git(dir, "Pushed "+shortRepo(it.repo), "push", "push")), true
	case s == "M":
		return m.inboxKey("M"), true
	}
	return nil, false
}

func (m *model) homeKey(s string) tea.Cmd {
	if cmd, ok := m.homeQuick(s); ok {
		return cmd
	}
	if strings.HasPrefix(s, "alt+") && len(s) == 5 && s[4] >= '1' && s[4] <= '5' {
		if rr := m.recentRepos(); int(s[4]-'1') < len(rr) {
			return m.openRecent(rr[s[4]-'1'])
		}
		return nil
	}
	switch s {
	case "enter":
		return m.homeOpen()
	case "t":
		m.homeGrid = !m.homeGrid
		m.saveSettings()
		if m.homeGrid {
			m.focus = 0
			return m.setStatus("Dashboard layout (t switches)", false)
		}
		return m.setStatus("Two-panel layout (t switches)", false)
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
		if !m.homeGrid {
			m.focus = 1 - m.focus
		}
	case "l", "right":
		if !m.homeGrid {
			m.focus = 1
		}
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
		if it := m.homeSel(); it != nil {
			switch {
			case it.run != nil:
				return m.openURL(it.run.URL)
			case it.pr != nil:
				return m.openURL(it.pr.URL)
			case it.issue != nil:
				return m.openURL(it.issue.URL)
			case it.repo != "":
				return m.openURL("https://github.com/" + it.repo)
			}
		}
	}
	return nil
}

// ---- numbers ----

type homeStats struct {
	year, streak, best, week int
	days                     []int // the last 7 days
	ciFail, ciPass           int
	myPRs, toReview          int
}

func (m *model) homeNumbers() homeStats {
	var st homeStats
	if cd := m.contrib; cd != nil && cd.err == "" {
		st.year = cd.total
		var all []contribDay
		for _, w := range cd.weeks {
			all = append(all, w...)
		}
		today := time.Now().Format("2006-01-02")
		run := 0
		for _, d := range all {
			if d.Date > today {
				break
			}
			if d.Count > 0 {
				run++
				st.best = max(st.best, run)
			} else {
				run = 0
			}
		}
		// current streak: days with contributions up to today (today may still be empty)
		for i := len(all) - 1; i >= 0; i-- {
			d := all[i]
			if d.Date > today {
				continue
			}
			if d.Count == 0 {
				if d.Date == today {
					continue
				}
				break
			}
			st.streak++
		}
		n := 0
		for i := len(all) - 1; i >= 0 && n < 7; i-- {
			if all[i].Date > today {
				continue
			}
			st.days = append([]int{all[i].Count}, st.days...)
			st.week += all[i].Count
			n++
		}
	}
	for _, r := range m.latestRuns() {
		if time.Since(r.Created) > 30*24*time.Hour {
			continue
		}
		switch {
		case r.failed():
			st.ciFail++
		case r.Conclusion == "success":
			st.ciPass++
		}
	}
	if pl := m.prLists[qMyPRs]; pl != nil {
		st.myPRs = len(pl.items)
	}
	if pl := m.prLists[qReview]; pl != nil {
		st.toReview = len(pl.items)
	}
	return st
}

func spark(vals []int) string {
	bars := []rune("▁▂▃▄▅▆▇█")
	top := 1
	for _, v := range vals {
		top = max(top, v)
	}
	var b strings.Builder
	for _, v := range vals {
		i := 0
		if v > 0 {
			i = 1 + v*(len(bars)-2)/top
		}
		b.WriteRune(bars[min(i, len(bars)-1)])
	}
	return b.String()
}

// tile draws one number box: title in the border, a big line and a small line
func tile(title, big, small string, bigStyle lipgloss.Style, w int) []string {
	return box(title, w, 4, []string{bigStyle.Bold(true).Render(big), small}, false, "")
}

func (m *model) homeTiles(w, y0 int) []string {
	st := m.homeNumbers()
	n := 5
	tw := w / n
	ciBig, ciStyle := "✓ all good", fg(cGreen)
	if st.ciFail > 0 {
		ciBig, ciStyle = fmt.Sprintf("✗ %d failing", st.ciFail), fg(cRed)
	}
	tiles := [][]string{
		tile("This year", fmt.Sprintf("%d", st.year), fg(cMut).Render("contributions"), fg(cGreen), tw),
		tile("Streak", fmt.Sprintf("%d %s", st.streak, plural(st.streak, "day", "days")), fg(cMut).Render(fmt.Sprintf("best %d", st.best)), fg(cYellow), tw),
		tile("This week", fmt.Sprintf("%d", st.week), fg(cGreen).Render(spark(st.days))+fg(cMut).Render(" last 7 days"), fg(cAcc), tw),
		tile("Your PRs", fmt.Sprintf("%d open", st.myPRs), fg(cMut).Render(fmt.Sprintf("%d to review", st.toReview)), fg(cTxt), tw),
		tile("CI", ciBig, fg(cMut).Render(fmt.Sprintf("%d passing", st.ciPass)), ciStyle, w-tw*(n-1)),
	}
	// clicking a tile jumps to its tab
	targets := []int{-1, -1, -1, tabPRs, tabActions}
	x := 0
	for i, t := range targets {
		tw2 := tw
		if i == n-1 {
			tw2 = w - tw*(n-1)
		}
		if t >= 0 {
			m.addZone(x, y0, x+tw2-1, y0+3, "hometile", t)
		}
		x += tw2
	}
	out := make([]string, 4)
	for i := 0; i < 4; i++ {
		for _, t := range tiles {
			out[i] += t[i]
		}
	}
	return out
}

// ---- the grouped "Needs you" list ----

func (m *model) homeRowLines(w, h, y0, x0 int) []string {
	items := m.homeItems()
	m.home.cur = max(0, min(m.home.cur, len(items)-1))
	type row struct {
		head string
		idx  int
	}
	var rows []row
	last := ""
	for i, it := range items {
		if it.group != last {
			n := 0
			for _, x := range items {
				if x.group == it.group {
					n++
				}
			}
			title := strings.ToUpper(it.group)
			if it.kind != "recent" && it.kind != "inbox" {
				title += fmt.Sprintf(" %d", n)
			}
			rows = append(rows, row{head: title, idx: -1})
			last = it.group
		}
		rows = append(rows, row{idx: i})
	}
	// keep the picked row visible (with its header when possible)
	curRow := 0
	for i, r := range rows {
		if r.idx == m.home.cur {
			curRow = i
		}
	}
	if curRow-1 < m.home.off {
		m.home.off = max(0, curRow-1)
	}
	if curRow >= m.home.off+h {
		m.home.off = curRow - h + 1
	}
	var out []string
	for i := m.home.off; i < len(rows) && len(out) < h; i++ {
		r := rows[i]
		if r.idx < 0 {
			out = append(out, fg(cHint).Bold(true).Render(r.head))
			continue
		}
		it := items[r.idx]
		sel := r.idx == m.home.cur
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
		left := st.Render(" ") + it.color.Inherit(st).Render(it.icon) + st.Render(" ") + st.Foreground(nc).Bold(sel).Render(it.text)
		right := ""
		if it.keys != "" && sel {
			right = st.Foreground(cAcc).Render(it.keys) + st.Render("  ")
		}
		right += st.Foreground(cMut).Render(trunc(it.right, 16))
		space := w - width(left) - width(right) - 1
		if space < 1 {
			left = trunc(left, w-width(right)-2)
			space = w - width(left) - width(right) - 1
		}
		out = append(out, left+st.Render(strings.Repeat(" ", max(space, 0)+1))+right)
		y := y0 + len(out) - 1
		m.addZone(x0, y, x0+w-1, y, "homerow", r.idx)
	}
	if len(items) == 0 {
		if m.prLists[qReview] == nil || m.prLists[qMyPRs] == nil {
			out = append(out, "", fg(cMut).Render("Looking at what needs you…"))
		} else {
			out = append(out, "", fg(cGreen).Render(icCheck+" Nothing needs you right now"))
		}
	}
	return out
}

func (m *model) homeListBox(w, h, y0 int) []string {
	m.addZone(0, y0, w-1, y0+h-1, "list", 0)
	body := m.homeRowLines(w-4, h-2, y0+1, 1)
	note := ""
	if n := len(m.homeItems()); n > 0 {
		note = fmt.Sprintf("%d/%d", m.home.cur+1, n)
	}
	return box(icHome+" Needs you", w, h, body, m.focus == 0, note)
}

// activity grouped by day: Today, Yesterday, then dates
func (m *model) activityLines(w, limit int) []string {
	el := m.events
	switch {
	case el == nil:
		return []string{fg(cMut).Render("Loading…")}
	case el.err != "":
		return []string{fg(cRed).Render(el.err)}
	case len(el.items) == 0:
		return []string{fg(cMut).Render("Nothing yet.")}
	}
	var out []string
	last := ""
	today := time.Now().Format("2006-01-02")
	yest := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	for _, e := range el.items {
		if len(out) >= limit {
			break
		}
		d := e.Created.Local().Format("2006-01-02")
		if d != last {
			label := e.Created.Local().Format("Mon 2 Jan")
			switch d {
			case today:
				label = "Today"
			case yest:
				label = "Yesterday"
			}
			out = append(out, fg(cHint).Bold(true).Render(label))
			last = d
		}
		out = append(out, trunc(" "+m.eventLine(e), w))
	}
	return out
}

// ---- the dashboard layout ----

func (m *model) homeGridRender(ph int) []string {
	w := m.w
	var out []string
	hello := "Hi"
	if m.login != "" {
		hello = "Hi " + m.login
	}
	when := time.Now().Format("Mon 2 Jan · 15:04")
	line := " " + fg(cAcc).Bold(true).Render(hello)
	line += strings.Repeat(" ", max(1, w-width(line)-width(when)-1)) + fg(cMut).Render(when)
	out = append(out, line)
	y := 3 // the first line of the body is screen line 2
	out = append(out, m.homeTiles(w, y)...)
	y += 4
	rest := ph - len(out)
	// the graph needs 12 lines; skip it on short windows
	if rest >= 20 {
		g := m.contribLines(w - 4)
		gw := 0
		for _, l := range g[:len(g)-2] {
			gw = max(gw, width(l))
		}
		if padL := (w - 4 - gw) / 2; padL > 0 { // center the graph
			for i := range g {
				g[i] = strings.Repeat(" ", padL) + g[i]
			}
		}
		out = append(out, box(icCalendar+" Contributions", w, len(g)+2, g, false, "")...)
		y += len(g) + 2
		rest = ph - len(out)
	}
	if rest < 4 {
		return out
	}
	lw := w * 55 / 100
	rw := w - lw
	m.addZone(0, y, lw-1, y+rest-1, "list", 0)
	body := m.homeRowLines(lw-4, rest-2, y+1, 1)
	note := ""
	if n := len(m.homeItems()); n > 0 {
		note = fmt.Sprintf("%d/%d", m.home.cur+1, n)
	}
	left := box(icHome+" Needs you", lw, rest, body, true, note)
	act := m.activityLines(rw-4, rest-2)
	right := box(icClock+" Activity", rw, rest, act, false, "")
	for i := 0; i < rest; i++ {
		out = append(out, left[i]+right[i])
	}
	return out
}

// ---- the two-panel layout's right side ----

func (m *model) homePreview(w int) (string, []string) {
	st := m.homeNumbers()
	out := []string{}
	nums := []string{
		fg(cGreen).Bold(true).Render(fmt.Sprint(st.year)) + fg(cMut).Render(" this year"),
		fg(cYellow).Bold(true).Render(fmt.Sprintf("%d %s", st.streak, plural(st.streak, "day", "days"))) + fg(cMut).Render(fmt.Sprintf(" streak (best %d)", st.best)),
		fg(cAcc).Bold(true).Render(fmt.Sprint(st.week)) + fg(cMut).Render(" this week ") + fg(cGreen).Render(spark(st.days)),
		fg(cTxt).Bold(true).Render(fmt.Sprint(st.myPRs)) + fg(cMut).Render(" open "+plural(st.myPRs, "PR", "PRs")),
	}
	if st.ciFail > 0 {
		nums = append(nums, fg(cRed).Bold(true).Render(fmt.Sprintf("✗ %d", st.ciFail))+fg(cMut).Render(" CI failing"))
	} else {
		nums = append(nums, fg(cGreen).Render("✓ CI good"))
	}
	out = append(out, wrapJoin(nums, fg(cDim).Render("  ·  "), w)...)
	out = append(out, "", fg(cHint).Bold(true).Render("Contributions"), "")
	out = append(out, m.contribLines(w)...)
	out = append(out, "", sepLine(w), fg(cHint).Bold(true).Render("Activity"), "")
	out = append(out, m.activityLines(w, 60)...)
	return "Home", out
}

// ---- settings saved between runs ----

type settings struct {
	HomeList bool `json:"homeList"` // true = the two-panel Home
}

func (m *model) saveSettings() {
	if !m.dump {
		saveCache("settings.json", settings{HomeList: !m.homeGrid})
	}
}

func (m *model) loadSettings() {
	var s settings
	m.homeGrid = true
	if loadCache("settings.json", &s) {
		m.homeGrid = !s.HomeList
	}
	var r []string
	if loadCache("recent.json", &r) {
		m.recent = r
	}
}
