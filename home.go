package main

// Home: what needs you (failing CI, review requests, your open PRs, assigned issues,
// repos on this PC with work to push or pull, unread notifications) on the left;
// your contribution graph, numbers and recent activity on the right.

import (
	"fmt"
	"sort"
	"strings"
	"time"

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
		sort.SliceStable(el.items, func(i, j int) bool { return el.items[i].Created.After(el.items[j].Created) })
		return func(m *model) { m.events = el }
	}
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
			text = strings.ReplaceAll(fmt.Sprintf("%s PR #%d %s in %s", p.Action, p.PullRequest.Number, p.PullRequest.Title, repo), "  ", " ")
		}
	case "PullRequestReviewEvent":
		ic = icCheck
		if p.PullRequest != nil {
			text = fmt.Sprintf("reviewed PR #%d in %s", p.PullRequest.Number, repo)
		}
	case "IssuesEvent":
		ic, st = icIssue, fg(cGreen)
		if p.Issue != nil {
			text = strings.ReplaceAll(fmt.Sprintf("%s issue #%d %s in %s", p.Action, p.Issue.Number, p.Issue.Title, repo), "  ", " ")
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
