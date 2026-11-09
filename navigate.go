package main

// Jumping to a thing's normal page inside TermGit (the repo, the right section, that
// item highlighted, all its keys available), from Home or the Inbox. esc comes back.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// what to highlight once the section's list has loaded
type want struct {
	sec     int
	match   func(it exItem) bool
	what    string // for the "couldn't find" message
	flipped bool   // already tried the other open / closed list
	flip    func(m *model)
}

// gotoItem opens repo nwo at section sec and highlights the item match finds
func (m *model) gotoItem(nwo string, sec int, what string, match func(exItem) bool, flip func(*model)) tea.Cmd {
	from := m.tab
	m.setTab(tabRepos)
	m.backTo = from
	m.filter, m.filtering = "", false
	m.refilter()
	var repo *Repo
	for i, idx := range m.view {
		if strings.EqualFold(m.repos[idx].NameWithOwner, nwo) {
			m.cur, repo = i, &m.repos[idx]
		}
	}
	if repo == nil { // not one of your repos: open it anyway
		repo = &Repo{NameWithOwner: nwo, Name: shortRepo(nwo), URL: "https://github.com/" + nwo}
	}
	ex, ok := m.exSaved[repo.NameWithOwner]
	if !ok {
		ex = &explorer{repo: *repo, ref: repo.Branch(), refOid: repo.HeadOid(), dirCur: map[string]int{}}
	}
	ex.query[sec], ex.filtering, ex.plan = "", false, nil
	m.ex, m.focus, m.help = ex, 0, false
	ex.want = &want{sec: sec, match: match, what: what, flip: flip}
	cmd := m.exSetSec(sec)
	m.applyWant()
	return cmd
}

// applyWant highlights the wanted item if its list is there now
func (m *model) applyWant() tea.Cmd {
	ex := m.exActive()
	if ex == nil || ex.want == nil || ex.sec != ex.want.sec {
		return nil
	}
	w := ex.want
	items := m.exItems()
	for i, it := range items {
		if w.match(it) {
			ex.cur[ex.sec] = i
			ex.want = nil
			m.exClamp()
			return m.exSelected()
		}
	}
	if !m.sectionLoaded() {
		return nil // still loading: try again when it arrives
	}
	if !w.flipped && w.flip != nil {
		w.flipped = true
		w.flip(m) // look in the closed (or open) list too
		ex.cur[ex.sec], ex.off[ex.sec] = 0, 0
		return m.exSelected()
	}
	ex.want = nil
	return m.setStatus("Couldn't find "+w.what+" in the list", true)
}

func (m *model) sectionLoaded() bool {
	ex := m.ex
	nwo := ex.repo.NameWithOwner
	switch ex.sec {
	case secIssues:
		return m.issueLists[m.exIssueQuery()] != nil
	case secPRs:
		return m.prLists[m.exPRQuery()] != nil
	case secActions:
		return m.runs[nwo] != nil
	case secReleases:
		return m.releases[nwo] != nil
	}
	return true
}

func flipPRs(m *model)    { m.prs.closed = !m.prs.closed }
func flipIssues(m *model) { m.iss.closed = !m.iss.closed }

func (m *model) gotoPR(nwo string, n int, open bool) tea.Cmd {
	m.prs.closed, m.prs.view = !open, pvOverview
	return m.gotoItem(nwo, secPRs, fmt.Sprintf("PR #%d", n), func(it exItem) bool { return it.kind == "pr" && it.pr.Number == n }, flipPRs)
}

func (m *model) gotoIssue(nwo string, n int, open bool) tea.Cmd {
	m.iss.closed = !open
	return m.gotoItem(nwo, secIssues, fmt.Sprintf("issue #%d", n), func(it exItem) bool { return it.kind == "issue" && it.issue.Number == n }, flipIssues)
}

func (m *model) gotoRun(nwo string, match func(r *Run) bool) tea.Cmd {
	return m.gotoItem(nwo, secActions, "that run", func(it exItem) bool { return it.kind == "run" && match(it.run) }, nil)
}

// a notification's page in TermGit
func (m *model) gotoNotif(n *Notif) tea.Cmd {
	nwo := n.Repository.FullName
	s := m.subjects[fmt.Sprintf("%s#%d", nwo, n.number())]
	switch n.Subject.Type {
	case "PullRequest":
		open := s == nil || s.pr.open()
		return m.gotoPR(nwo, n.number(), open)
	case "Issue":
		open := s == nil || s.pr.open()
		return m.gotoIssue(nwo, n.number(), open)
	case "CheckSuite":
		// "Inbox email sync workflow run failed for main branch": the newest run of it
		title := strings.ToLower(n.Subject.Title)
		return m.gotoRun(nwo, func(r *Run) bool {
			return strings.Contains(title, strings.ToLower(r.Name)) && strings.Contains(title, strings.ToLower(r.Branch))
		})
	case "Release":
		t := n.Subject.Title
		return m.gotoItem(nwo, secReleases, "that release", func(it exItem) bool {
			return it.kind == "release" && (it.release.title() == t || it.release.Tag == t)
		}, nil)
	case "Commit":
		return m.gotoItem(nwo, secCommits, "", func(exItem) bool { return true }, nil)
	}
	return m.gotoItem(nwo, secCode, "", func(exItem) bool { return true }, nil)
}
