package main

// Users in Explore: search people and organizations, see a profile (bio, followers,
// pinned repos) and their repos, follow / unfollow, open any of their repos.

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/cli/go-gh/v2/pkg/api"
)

type userHit struct {
	Login       string `json:"login"`
	Name        string `json:"name"`
	Bio         string `json:"bio"`
	Description string `json:"description"`
	Typename    string `json:"__typename"`
	Followers   *struct {
		TotalCount int `json:"totalCount"`
	} `json:"followers"`
}

type userSearch struct {
	items []userHit
	err   string
}

type Profile struct {
	Typename    string `json:"__typename"`
	Login       string `json:"login"`
	URL         string `json:"url"`
	Name        string `json:"name"`
	Bio         string `json:"bio"`
	Description string `json:"description"`
	Company     string `json:"company"`
	Location    string `json:"location"`
	Website     string `json:"websiteUrl"`
	Following   bool   `json:"viewerIsFollowing"`
	Followers   *struct {
		TotalCount int `json:"totalCount"`
	} `json:"followers"`
	FollowingN *struct {
		TotalCount int `json:"totalCount"`
	} `json:"following"`
	Pinned struct {
		Nodes []Repo `json:"nodes"`
	} `json:"pinnedItems"`
	Repositories struct {
		TotalCount int    `json:"totalCount"`
		Nodes      []Repo `json:"nodes"`
	} `json:"repositories"`
	err string
}

func (p *Profile) isOrg() bool { return p.Typename == "Organization" }

func fetchUserSearch(q string) func() func(*model) {
	return func() func(*model) {
		us := &userSearch{}
		c, err := api.DefaultGraphQLClient()
		if err == nil {
			var r struct {
				Search struct{ Nodes []userHit }
			}
			err = c.Do(`query($q: String!) { search(query: $q, type: USER, first: 40) { nodes {
  __typename
  ... on User { login name bio followers { totalCount } }
  ... on Organization { login name description }
} } }`, map[string]interface{}{"q": q}, &r)
			for _, n := range r.Search.Nodes {
				if n.Login != "" {
					us.items = append(us.items, n)
				}
			}
		}
		if err != nil {
			us.err = apiErr(err)
		}
		return func(m *model) { m.expUsers[q] = us }
	}
}

func fetchProfile(login string) func() func(*model) {
	return func() func(*model) {
		p := &Profile{Login: login}
		c, err := api.DefaultGraphQLClient()
		if err == nil {
			var r struct {
				RepositoryOwner *Profile `json:"repositoryOwner"`
			}
			err = c.Do(`query($l: String!) { repositoryOwner(login: $l) {
  __typename login url
  ... on User { name bio company location websiteUrl viewerIsFollowing followers { totalCount } following { totalCount }
    pinnedItems(first: 6, types: REPOSITORY) { nodes { ... on Repository { `+repoFields+` } } } }
  ... on Organization { name description location websiteUrl
    pinnedItems(first: 6, types: REPOSITORY) { nodes { ... on Repository { `+repoFields+` } } } }
  repositories(first: 60, privacy: PUBLIC, orderBy: {field: PUSHED_AT, direction: DESC}) { totalCount nodes { `+repoFields+` } }
} }`, map[string]interface{}{"l": login}, &r)
			if err == nil && r.RepositoryOwner != nil {
				p = r.RepositoryOwner
			} else if err == nil {
				p.err = "No user or organization called " + login
			}
		}
		if err != nil {
			p.err = apiErr(err)
		}
		return func(m *model) { m.profiles[strings.ToLower(login)] = p }
	}
}

// openProfile shows someone's profile and repos (Explore › Users)
func (m *model) openProfile(login string) tea.Cmd {
	m.setTab(tabExplore)
	m.exp.mode, m.exp.user, m.exp.cur, m.exp.off = modeUsers, login, 0, 0
	return m.expSelected()
}

type followMsg struct {
	login  string
	follow bool
	err    error
}

// F on a profile: follow / unfollow
func (m *model) actFollow(p *Profile) tea.Cmd {
	if p == nil || p.isOrg() {
		return m.setStatus("Organizations can't be followed", true)
	}
	login, follow := p.Login, !p.Following
	if m.dump {
		return m.setStatus(fmt.Sprintf("Would follow=%v %s", follow, login), false)
	}
	return func() tea.Msg {
		c, err := api.DefaultRESTClient()
		if err == nil {
			if follow {
				err = c.Put("user/following/"+login, nil, nil)
			} else {
				err = c.Delete("user/following/"+login, nil)
			}
		}
		return followMsg{login, follow, err}
	}
}

func (m *model) onFollow(msg followMsg) tea.Cmd {
	if msg.err != nil {
		e := apiErr(msg.err)
		if strings.Contains(e, "404") || strings.Contains(strings.ToLower(e), "scope") || strings.Contains(e, "Not Found") {
			login, follow := msg.login, msg.follow
			return m.askScope("user:follow", "follow people", func(m *model) tea.Cmd {
				return func() tea.Msg {
					c, err := api.DefaultRESTClient()
					if err == nil {
						if follow {
							err = c.Put("user/following/"+login, nil, nil)
						} else {
							err = c.Delete("user/following/"+login, nil)
						}
					}
					return followMsg{login, follow, err}
				}
			})
		}
		return m.setStatus("Couldn't follow: "+e, true)
	}
	if p := m.profiles[strings.ToLower(msg.login)]; p != nil {
		p.Following = msg.follow
		if p.Followers != nil {
			if msg.follow {
				p.Followers.TotalCount++
			} else {
				p.Followers.TotalCount--
			}
		}
	}
	if msg.follow {
		return m.setStatus("Following "+msg.login, false)
	}
	return m.setStatus("Unfollowed "+msg.login, false)
}

// ---- permissions ----

type scopeDoneMsg struct {
	scope string
	err   error
	retry func(m *model) tea.Cmd
}

// askScope: GitHub needs an extra permission for this; get it (a browser page to approve), then retry
func (m *model) askScope(scope, why string, retry func(m *model) tea.Cmd) tea.Cmd {
	m.prompt = &prompt{label: "To " + why + ", GitHub needs one more permission (" + scope + "). Give it now? A browser page opens to approve it.", confirm: true,
		done: func(m *model, _ string) tea.Cmd {
			if m.dump {
				return m.setStatus("Would run gh auth refresh -s "+scope, false)
			}
			c := ghCmd("auth", "refresh", "-h", "github.com", "-s", scope)
			return tea.ExecProcess(c, func(err error) tea.Msg { return scopeDoneMsg{scope, err, retry} })
		}}
	return nil
}

func (m *model) onScopeDone(msg scopeDoneMsg) tea.Cmd {
	if msg.err != nil {
		return m.setStatus("The permission wasn't given: "+msg.err.Error(), true)
	}
	if msg.retry != nil {
		return tea.Batch(m.setStatus("Permission given, trying again…", false), msg.retry(m))
	}
	return m.setStatus("Permission given", false)
}

// ---- drawing ----

func (m *model) userRow(u *userHit, w int, sel bool) string {
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
	ic := icUser
	if u.Typename == "Organization" {
		ic = icOrg
	}
	left := st.Foreground(cHint).Render(ic) + st.Render(" ") + st.Foreground(nc).Bold(sel).Render(u.Login)
	if u.Name != "" {
		left += st.Foreground(cMut).Render("  " + u.Name)
	}
	right := ""
	if u.Followers != nil {
		right = st.Foreground(cMut).Render(shortNum(u.Followers.TotalCount) + " followers")
	}
	space := w - width(left) - width(right) - 1
	if space < 1 {
		left = trunc(left, w-width(right)-2)
		space = w - width(left) - width(right) - 1
	}
	return left + st.Render(strings.Repeat(" ", max(space, 0)+1)) + right
}

func (m *model) profileLines(p *Profile, w int) []string {
	if p == nil {
		return []string{fg(cMut).Render("Loading the profile…")}
	}
	if p.err != "" {
		return []string{fg(cRed).Render(p.err)}
	}
	ic := icUser
	if p.isOrg() {
		ic = icOrg
	}
	title := p.Login
	if p.Name != "" {
		title = p.Name + "  " + fg(cMut).Render(p.Login)
	}
	out := []string{fg(cAcc).Bold(true).Render(ic+" ") + fg(cAcc).Bold(true).Render(title)}
	bio := p.Bio
	if bio == "" {
		bio = p.Description
	}
	if bio != "" {
		out = append(out, wrapText(bio, w, fg(cTxt))...)
	}
	out = append(out, "")
	var facts []string
	if p.Followers != nil {
		facts = append(facts, fg(cTxt).Render(shortNum(p.Followers.TotalCount))+fg(cMut).Render(" followers"))
	}
	if p.FollowingN != nil {
		facts = append(facts, fg(cTxt).Render(shortNum(p.FollowingN.TotalCount))+fg(cMut).Render(" following"))
	}
	facts = append(facts, fg(cTxt).Render(fmt.Sprint(p.Repositories.TotalCount))+fg(cMut).Render(" public repos"))
	if p.Following {
		facts = append(facts, fg(cGreen).Render(icCheck+" you follow them"))
	}
	out = append(out, wrapJoin(facts, "   ", w)...)
	var where []string
	if p.Company != "" {
		where = append(where, fg(cMut).Render(icOrg+" "+p.Company))
	}
	if p.Location != "" {
		where = append(where, fg(cMut).Render(icGlobe+" "+p.Location))
	}
	if p.Website != "" {
		where = append(where, fg(cAcc).Render(osc8(p.Website, icLink+" "+p.Website)))
	}
	if len(where) > 0 {
		out = append(out, wrapJoin(where, "   ", w)...)
	}
	h := [][2]string{{"j/k", "their repos (list)"}, {"enter", "open a repo"}}
	if !p.isOrg() {
		follow := "follow"
		if p.Following {
			follow = "unfollow"
		}
		h = append(h, [2]string{"F", follow})
	}
	h = append(h, [2]string{"o", "browser"}, [2]string{"esc", "back"})
	out = append(out, "", hints(h), sepLine(w))
	if len(p.Pinned.Nodes) > 0 {
		out = append(out, fg(cHint).Bold(true).Render("Pinned"), "")
		for _, r := range p.Pinned.Nodes {
			line := fg(cAcc).Render(icRepo+" "+r.Name) + fg(cMut).Render(fmt.Sprintf("  %s %s", icStar, shortNum(r.Stars)))
			if l := r.Lang(); l != "" {
				line += fg(cMut).Render("  " + l)
			}
			out = append(out, line)
			if r.Description != "" {
				out = append(out, trunc("  "+fg(cTxt).Render(r.Description), w))
			}
		}
	}
	return out
}
