package main

// Pull requests: the PRs tab (across your repos) and a repo's PRs section.
// Views: Overview (conversation), Commits, Checks, Files (changes).
// Merge (squash / merge / rebase), review, comment, close / reopen, check out locally, create.

import (
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/cli/go-gh/v2/pkg/api"
)

type PR struct {
	Issue
	IsDraft        bool      `json:"isDraft"`
	Merged         time.Time `json:"mergedAt"`
	Head           string    `json:"headRefName"`
	Base           string    `json:"baseRefName"`
	Additions      int       `json:"additions"`
	Deletions      int       `json:"deletions"`
	ChangedFiles   int       `json:"changedFiles"`
	Mergeable      string    `json:"mergeable"`      // MERGEABLE / CONFLICTING / UNKNOWN
	ReviewDecision string    `json:"reviewDecision"` // APPROVED / CHANGES_REQUESTED / REVIEW_REQUIRED
	Last           struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"last"`
}

func (p *PR) merged() bool { return !p.Merged.IsZero() }

// checks: SUCCESS / FAILURE / ERROR / PENDING / EXPECTED / "" (none)
func (p *PR) checks() string {
	if len(p.Last.Nodes) > 0 && p.Last.Nodes[0].Commit.StatusCheckRollup != nil {
		return p.Last.Nodes[0].Commit.StatusCheckRollup.State
	}
	return ""
}

type prList struct {
	items []PR
	err   string
}

type PRReview struct {
	Author *struct {
		Login string `json:"login"`
	} `json:"author"`
	State     string    `json:"state"` // APPROVED / CHANGES_REQUESTED / COMMENTED / DISMISSED
	Body      string    `json:"body"`
	Submitted time.Time `json:"submittedAt"`
}

type Check struct {
	Typename   string    `json:"__typename"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	DetailsURL string    `json:"detailsUrl"`
	Started    time.Time `json:"startedAt"`
	Completed  time.Time `json:"completedAt"`
	Context    string    `json:"context"` // old-style status
	State      string    `json:"state"`
	TargetURL  string    `json:"targetUrl"`
	Suite      *struct {
		Run *struct {
			Workflow struct {
				Name string `json:"name"`
			} `json:"workflow"`
		} `json:"workflowRun"`
	} `json:"checkSuite"`
}

// as status / conclusion like the Actions ones, so the same ✓ ✗ ● marks work
func (c *Check) marks() (string, string) {
	if c.Typename == "StatusContext" {
		switch c.State {
		case "SUCCESS":
			return "completed", "success"
		case "FAILURE", "ERROR":
			return "completed", "failure"
		}
		return "in_progress", ""
	}
	return strings.ToLower(c.Status), strings.ToLower(c.Conclusion)
}

func (c *Check) title() string {
	n := c.Name
	if c.Typename == "StatusContext" {
		n = c.Context
	}
	if c.Suite != nil && c.Suite.Run != nil && c.Suite.Run.Workflow.Name != "" {
		n = c.Suite.Run.Workflow.Name + " / " + n
	}
	return n
}

type prDetail struct {
	comments []IssueComment
	reviews  []PRReview
	commits  []Commit
	checks   []Check
	err      string
}

type prFiles struct {
	files []commitFile
	err   string
}

const prFields = `
        id number title body state url createdAt updatedAt closedAt mergedAt isDraft
        author { login }
        repository { nameWithOwner }
        labels(first: 10) { nodes { name color } }
        comments { totalCount }
        headRefName baseRefName additions deletions changedFiles mergeable reviewDecision
        last: commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }`

const prSearch = `query($q: String!) {
  search(query: $q, type: ISSUE, first: 50) {
    nodes { ... on PullRequest {` + prFields + ` } }
  }
}`

func fetchPRs(q string) func() func(*model) {
	return func() func(*model) {
		pl := &prList{}
		c, err := api.DefaultGraphQLClient()
		if err == nil {
			var r struct {
				Search struct {
					Nodes []PR
				}
			}
			if err = c.Do(prSearch, map[string]interface{}{"q": q}, &r); err == nil {
				for _, n := range r.Search.Nodes {
					if n.Number > 0 {
						pl.items = append(pl.items, n)
					}
				}
			}
		}
		if err != nil {
			pl.err = apiErr(err)
		}
		return func(m *model) { m.prLists[q] = pl }
	}
}

const prDetailQuery = `query($owner: String!, $name: String!, $n: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $n) {
      comments(first: 100) { nodes { author { login } body createdAt } }
      reviews(last: 50) { nodes { author { login } state body submittedAt } }
      commits(first: 100) { nodes { commit { oid abbreviatedOid messageHeadline messageBody committedDate additions deletions changedFilesIfAvailable author { name user { login } } } } }
      last: commits(last: 1) { nodes { commit { statusCheckRollup { contexts(first: 80) { nodes {
        __typename
        ... on CheckRun { name status conclusion detailsUrl startedAt completedAt checkSuite { workflowRun { workflow { name } } } }
        ... on StatusContext { context state targetUrl }
      } } } } } }
    }
  }
}`

func fetchPRDetail(nwo string, n int) func() func(*model) {
	return func() func(*model) {
		d := &prDetail{}
		c, err := api.DefaultGraphQLClient()
		if err == nil {
			owner, name := splitNwo(nwo)
			var r struct {
				Repository struct {
					PullRequest *struct {
						Comments struct{ Nodes []IssueComment }
						Reviews  struct{ Nodes []PRReview }
						Commits  struct {
							Nodes []struct{ Commit Commit }
						}
						Last struct {
							Nodes []struct {
								Commit struct {
									StatusCheckRollup *struct {
										Contexts struct{ Nodes []Check }
									}
								}
							}
						}
					}
				}
			}
			if err = c.Do(prDetailQuery, map[string]interface{}{"owner": owner, "name": name, "n": n}, &r); err == nil && r.Repository.PullRequest != nil {
				p := r.Repository.PullRequest
				d.comments, d.reviews = p.Comments.Nodes, p.Reviews.Nodes
				for _, c := range p.Commits.Nodes {
					d.commits = append(d.commits, c.Commit)
				}
				if len(p.Last.Nodes) > 0 && p.Last.Nodes[0].Commit.StatusCheckRollup != nil {
					d.checks = p.Last.Nodes[0].Commit.StatusCheckRollup.Contexts.Nodes
				}
			}
		}
		if err != nil {
			d.err = apiErr(err)
		}
		key := fmt.Sprintf("%s#%d", nwo, n)
		return func(m *model) { m.prDetails[key] = d }
	}
}

func fetchPRFiles(nwo string, n int) func() func(*model) {
	return func() func(*model) {
		f := &prFiles{}
		c, err := api.DefaultRESTClient()
		if err == nil {
			err = c.Get(fmt.Sprintf("repos/%s/pulls/%d/files?per_page=100", nwo, n), &f.files)
		}
		if err != nil {
			f.err = apiErr(err)
		}
		key := fmt.Sprintf("%s#%d", nwo, n)
		return func(m *model) { m.prFilesMap[key] = f }
	}
}

// ---- which PRs ----

type prState struct {
	cur, off  int
	scope     int
	closed    bool
	query     string
	filtering bool
	view      int // Overview, Commits, Checks, Files
}

var prScopes = []string{"Your repos", "Review requests", "Created", "Involved"}
var prViews = []string{"Overview", "Commits", "Checks", "Files"}

func (m *model) prStateQ() string {
	if m.prs.closed {
		return "is:closed"
	}
	return "is:open"
}

func (m *model) prTabQuery() string {
	scope := "user:" + m.login
	for _, r := range m.repos {
		if !strings.EqualFold(strings.SplitN(r.NameWithOwner, "/", 2)[0], m.login) && len(scope) < 200 {
			scope += " repo:" + r.NameWithOwner
		}
	}
	switch m.prs.scope {
	case 1:
		scope = "review-requested:@me"
	case 2:
		scope = "author:@me"
	case 3:
		scope = "involves:@me"
	}
	if m.login == "" && m.prs.scope == 0 {
		scope = "involves:@me"
	}
	return scope + " is:pr " + m.prStateQ() + " archived:false sort:updated-desc"
}

func (m *model) exPRQuery() string {
	return "repo:" + m.ex.repo.NameWithOwner + " is:pr " + m.prStateQ() + " sort:updated-desc"
}

func (m *model) prItems() []*PR {
	pl := m.prLists[m.prTabQuery()]
	if pl == nil {
		return nil
	}
	q := strings.ToLower(m.prs.query)
	var out []*PR
	for i := range pl.items {
		p := &pl.items[i]
		if q == "" || fuzzy(strings.ToLower(p.repo()+" "+p.Title+" "+p.Head+fmt.Sprintf(" #%d", p.Number)), q) {
			out = append(out, p)
		}
	}
	return out
}

func (m *model) selPR() *PR {
	if m.tab == tabPRs {
		items := m.prItems()
		if m.prs.cur >= 0 && m.prs.cur < len(items) {
			return items[m.prs.cur]
		}
		return nil
	}
	if ex := m.exActive(); ex != nil && ex.sec == secPRs {
		if it, ok := m.exSel(); ok && it.kind == "pr" {
			return it.pr
		}
	}
	return nil
}

func prKey(p *PR) string { return fmt.Sprintf("%s#%d", p.repo(), p.Number) }

func (m *model) loadPRDetail(p *PR, debounce bool) tea.Cmd {
	if p == nil {
		return nil
	}
	k := prKey(p)
	return tea.Batch(m.load("prdetail:"+k, debounce, fetchPRDetail(p.repo(), p.Number)),
		m.load("prfiles:"+k, debounce, fetchPRFiles(p.repo(), p.Number)))
}

func (m *model) prLoad() tea.Cmd {
	q := m.prTabQuery()
	return tea.Batch(m.load("prs:"+q, false, fetchPRs(q)), m.loadPRDetail(m.selPR(), true))
}

func (m *model) prSelected() tea.Cmd {
	m.pvOff = 0
	n := len(m.prItems())
	m.prs.cur = max(0, min(m.prs.cur, n-1))
	rows := m.listRows() - 1
	if m.prs.filtering || m.prs.query != "" {
		rows--
	}
	if m.prs.cur < m.prs.off {
		m.prs.off = m.prs.cur
	}
	if rows > 0 && m.prs.cur >= m.prs.off+rows {
		m.prs.off = m.prs.cur - rows + 1
	}
	return m.prLoad()
}

func (m *model) prMove(d int) tea.Cmd {
	n := len(m.prItems())
	if n == 0 {
		return nil
	}
	old := m.prs.cur
	m.prs.cur = max(0, min(n-1, old+d))
	if m.prs.cur == old {
		return nil
	}
	return m.prSelected()
}

func (m *model) prSetView(v int) tea.Cmd {
	m.prs.view = (v + len(prViews)) % len(prViews)
	m.pvOff = 0
	return nil
}

// ---- keys ----

// PR keys shared by the PRs tab and a repo's PRs section
func (m *model) prKeys(s string) (tea.Cmd, bool) {
	switch s {
	case "m":
		return m.prMerge(), true
	case "R":
		return m.prReview(), true
	case "C":
		return m.prComment(), true
	case "x":
		return m.prToggle(), true
	case "K":
		return m.prCheckout(), true
	case "N":
		return m.prNew(), true
	case "s":
		m.prs.closed = !m.prs.closed
		m.prs.cur, m.prs.off = 0, 0
		if ex := m.exActive(); ex != nil {
			ex.cur[secPRs], ex.off[secPRs] = 0, 0
			return m.exSelected(), true
		}
		return m.prSelected(), true
	case "enter", "v":
		return m.prRead(), true
	case "{", "<":
		return m.prSetView(m.prs.view - 1), true
	case "}", ">":
		return m.prSetView(m.prs.view + 1), true
	}
	if m.focus == 1 || m.tab == tabPRs {
		switch s { // in the right panel (or the PRs tab), [ ] switch the view
		case "[":
			return m.prSetView(m.prs.view - 1), true
		case "]":
			return m.prSetView(m.prs.view + 1), true
		}
	}
	return nil, false
}

func (m *model) prTabKey(s string) tea.Cmd {
	if cmd, ok := m.prKeys(s); ok {
		return cmd
	}
	half := m.listRows() / 2
	switch s {
	case "tab", "shift+tab":
		m.focus = 1 - m.focus
	case "f":
		m.prs.scope = (m.prs.scope + 1) % len(prScopes)
		m.prs.cur, m.prs.off = 0, 0
		return m.prSelected()
	case "/":
		m.prs.filtering, m.focus = true, 0
	case "r":
		m.issForget("", 0)
		if p := m.selPR(); p != nil {
			m.issForget(p.repo(), p.Number)
		}
		return m.prLoad()
	case "o":
		return m.actBrowser()
	case "y":
		return m.actCopy()
	case "e":
		return m.prOpenLocal("nvim")
	case "L":
		return m.prOpenLocal("lazygit")
	case "l", "right":
		m.focus = 1
	case "h", "left":
		m.focus = 0
	case "j", "down":
		if m.focus == 0 {
			return m.prMove(1)
		}
		m.scroll(1)
	case "k", "up":
		if m.focus == 0 {
			return m.prMove(-1)
		}
		m.scroll(-1)
	case "pgdown", "space":
		if m.focus == 0 {
			return m.prMove(half * 2)
		}
		m.scroll(half * 2)
	case "pgup":
		if m.focus == 0 {
			return m.prMove(-half * 2)
		}
		m.scroll(-half * 2)
	case "g", "home":
		if m.focus == 0 {
			return m.prMove(-1 << 20)
		}
		m.pvOff = 0
	case "G", "end":
		if m.focus == 0 {
			return m.prMove(1 << 20)
		}
		m.pvOff = m.pvMax
	}
	return nil
}

func (m *model) prLocal(p *PR) string {
	return m.local[strings.ToLower(p.repo())]
}

func (m *model) prOpenLocal(app string) tea.Cmd {
	p := m.selPR()
	if p == nil {
		return nil
	}
	dir := m.prLocal(p)
	if dir == "" {
		return m.setStatus(p.repo()+" isn't on this PC (open it in Repos and press c to clone)", true)
	}
	return m.runApp(app, dir)
}

// ---- doing things ----

func (m *model) prMerge() tea.Cmd {
	p := m.selPR()
	if p == nil {
		return nil
	}
	switch {
	case !p.open() || p.merged():
		return m.setStatus("This PR isn't open", true)
	case p.IsDraft:
		return m.setStatus("It's a draft: mark it ready first (ctrl+p → Mark ready for review)", true)
	case p.Mergeable == "CONFLICTING":
		return m.setStatus("It has conflicts with "+p.Base+": fix them first (K checks it out)", true)
	}
	nwo, n, head := p.repo(), p.Number, p.Head
	way := func(label, flag string) command {
		return command{label, "", func(m *model) tea.Cmd {
			run := func(m *model, del bool) tea.Cmd {
				args := []string{"pr", "merge", strconv.Itoa(n), "-R", nwo, flag}
				if del {
					args = append(args, "--delete-branch")
				}
				return m.gh(nwo, n, fmt.Sprintf("Merged #%d", n), "", args...)
			}
			m.prompt = &prompt{label: fmt.Sprintf("%s #%d, and delete the branch %s? (y = merge + delete, n = merge + keep it, esc = cancel)", label, n, head), confirm: true,
				done: func(m *model, _ string) tea.Cmd { return run(m, true) },
				no:   func(m *model) tea.Cmd { return run(m, false) }}
			return nil
		}}
	}
	m.openPicker(fmt.Sprintf("Merge #%d %s into %s", n, p.Title, p.Base), []command{
		way("Squash and merge", "--squash"), way("Create a merge commit", "--merge"), way("Rebase and merge", "--rebase"),
	})
	return nil
}

func (m *model) prReview() tea.Cmd {
	p := m.selPR()
	if p == nil {
		return nil
	}
	nwo, n := p.repo(), p.Number
	send := func(m *model, flag, ok, body string) tea.Cmd {
		args := []string{"pr", "review", strconv.Itoa(n), "-R", nwo, flag}
		if body == "" {
			args = append(args, "--body", "")
		}
		return m.gh(nwo, n, ok, body, args...)
	}
	write := func(flag, ok, what string) func(m *model) tea.Cmd {
		return func(m *model) tea.Cmd {
			return m.editInNvim("review", fmt.Sprintf(writeHelp, what), func(m *model, text string) tea.Cmd {
				if text == "" {
					return m.setStatus("Nothing written: review not sent", false)
				}
				m.prompt = &prompt{label: fmt.Sprintf("Send this review on #%d?", n), confirm: true, done: func(m *model, _ string) tea.Cmd {
					return send(m, flag, ok, text)
				}}
				return nil
			})
		}
	}
	m.openPicker(fmt.Sprintf("Review #%d %s", n, p.Title), []command{
		{"Approve", "", func(m *model) tea.Cmd {
			m.prompt = &prompt{label: fmt.Sprintf("Approve #%d. Add a message in Nvim? (n = approve without one)", n), confirm: true,
				no: func(m *model) tea.Cmd { return send(m, "--approve", fmt.Sprintf("Approved #%d", n), "") },
				done: func(m *model, _ string) tea.Cmd {
					return write("--approve", fmt.Sprintf("Approved #%d", n), "Your approval message.")(m)
				}}
			return nil
		}},
		{"Request changes", "", write("--request-changes", fmt.Sprintf("Asked for changes on #%d", n), "What should change?")},
		{"Comment (no approval)", "", write("--comment", fmt.Sprintf("Reviewed #%d", n), "Your review comment.")},
	})
	return nil
}

func (m *model) prComment() tea.Cmd {
	p := m.selPR()
	if p == nil {
		return nil
	}
	nwo, n := p.repo(), p.Number
	return m.editInNvim("comment", fmt.Sprintf(writeHelp, fmt.Sprintf("Your comment on #%d %s.", n, p.Title)), func(m *model, text string) tea.Cmd {
		if text == "" {
			return m.setStatus("Nothing written: no comment sent", false)
		}
		m.prompt = &prompt{label: fmt.Sprintf("Post your comment on #%d?", n), confirm: true, done: func(m *model, _ string) tea.Cmd {
			return m.gh(nwo, n, fmt.Sprintf("Commented on #%d", n), text, "pr", "comment", strconv.Itoa(n), "-R", nwo)
		}}
		return nil
	})
}

func (m *model) prToggle() tea.Cmd {
	p := m.selPR()
	if p == nil {
		return nil
	}
	nwo, n := p.repo(), p.Number
	switch {
	case p.merged():
		return m.setStatus("Already merged", true)
	case p.open():
		m.prompt = &prompt{label: fmt.Sprintf("Close #%d %s without merging?", n, p.Title), confirm: true, done: func(m *model, _ string) tea.Cmd {
			return m.gh(nwo, n, fmt.Sprintf("Closed #%d", n), "", "pr", "close", strconv.Itoa(n), "-R", nwo)
		}}
	default:
		m.prompt = &prompt{label: fmt.Sprintf("Reopen #%d %s?", n, p.Title), confirm: true, done: func(m *model, _ string) tea.Cmd {
			return m.gh(nwo, n, fmt.Sprintf("Reopened #%d", n), "", "pr", "reopen", strconv.Itoa(n), "-R", nwo)
		}}
	}
	return nil
}

func (m *model) prReady() tea.Cmd {
	p := m.selPR()
	if p == nil || !p.IsDraft {
		return m.setStatus("Pick a draft PR first", true)
	}
	nwo, n := p.repo(), p.Number
	m.prompt = &prompt{label: fmt.Sprintf("Mark #%d ready for review?", n), confirm: true, done: func(m *model, _ string) tea.Cmd {
		return m.gh(nwo, n, fmt.Sprintf("#%d is ready for review", n), "", "pr", "ready", strconv.Itoa(n), "-R", nwo)
	}}
	return nil
}

type checkoutMsg struct {
	n   int
	err error
}

// K: switch your local copy to the PR's branch
func (m *model) prCheckout() tea.Cmd {
	p := m.selPR()
	if p == nil {
		return nil
	}
	dir := m.prLocal(p)
	if dir == "" {
		return m.setStatus(p.repo()+" isn't on this PC: clone it first (Repos tab, c)", true)
	}
	n := p.Number
	m.prompt = &prompt{label: fmt.Sprintf("Check out #%d (branch %s) in %s?", n, p.Head, tildePath(dir)), confirm: true, done: func(m *model, _ string) tea.Cmd {
		if m.dump {
			return m.setStatus("Would run gh pr checkout "+strconv.Itoa(n), false)
		}
		c := exec.Command("gh", "pr", "checkout", strconv.Itoa(n))
		c.Dir = dir
		return tea.ExecProcess(c, func(err error) tea.Msg { return checkoutMsg{n, err} })
	}}
	return nil
}

// N: head branch → base → title → description in Nvim (or none) → create
func (m *model) prNew() tea.Cmd {
	start := func(m *model, nwo string) tea.Cmd {
		base, head := "main", ""
		for _, r := range m.repos {
			if r.NameWithOwner == nwo {
				base = r.Branch()
				if li, ok := m.linfo[strings.ToLower(nwo)]; ok && li.branch != base {
					head = li.branch
				}
			}
		}
		m.prompt = &prompt{label: "New PR in " + nwo + ". From branch:", text: head, done: func(m *model, head string) tea.Cmd {
			if head == "" {
				return nil
			}
			m.prompt = &prompt{label: "Into branch:", text: base, done: func(m *model, base string) tea.Cmd {
				if base == "" {
					return nil
				}
				m.prompt = &prompt{label: "Title:", text: strings.ReplaceAll(head, "-", " "), done: func(m *model, title string) tea.Cmd {
					if title == "" {
						return nil
					}
					create := func(m *model, body string) tea.Cmd {
						args := []string{"pr", "create", "-R", nwo, "--head", head, "--base", base, "--title", title}
						if body == "" {
							args = append(args, "--body", "")
						}
						return m.gh(nwo, 0, "Opened \""+title+"\"", body, args...)
					}
					m.prompt = &prompt{label: "Write a description in Nvim? (n = title only)", confirm: true,
						no: func(m *model) tea.Cmd { return create(m, "") },
						done: func(m *model, _ string) tea.Cmd {
							return m.editInNvim("new-pr", fmt.Sprintf(writeHelp, "Describe the PR: "+title+"."), func(m *model, text string) tea.Cmd {
								return create(m, text)
							})
						}}
					return nil
				}}
				return nil
			}}
			return nil
		}}
		return nil
	}
	if ex := m.exActive(); ex != nil {
		return start(m, ex.repo.NameWithOwner)
	}
	guess := ""
	if p := m.selPR(); p != nil {
		guess = p.repo()
	} else if len(m.repos) > 0 {
		guess = m.repos[0].NameWithOwner
	}
	m.prompt = &prompt{label: "New PR in which repo:", text: guess, done: func(m *model, nwo string) tea.Cmd {
		if !strings.Contains(nwo, "/") {
			nwo = m.login + "/" + nwo
		}
		return start(m, nwo)
	}}
	return nil
}

// ---- full screen: the current view ----

var runIDRe = regexp.MustCompile(`/actions/runs/(\d+)`)

type runFetchedMsg struct {
	run *Run
	err error
}

func (m *model) prRead() tea.Cmd {
	p := m.selPR()
	if p == nil {
		return nil
	}
	pr := *p
	k := prKey(p)
	sub := p.repo() + " · " + p.Head + " → " + p.Base
	title := fmt.Sprintf("#%d %s", p.Number, p.Title)
	switch m.prs.view {
	case 0:
		m.openReader(&reader{title: title, sub: sub, icon: icPR,
			mdSrc: func(m *model) (string, bool) { return prConversationMD(&pr, m.prDetails[k]), true }})
	case 1:
		m.openReader(&reader{title: title + " · commits", sub: sub, icon: icCommit,
			content: func(m *model, w int) []string { return m.prCommitsLines(&pr, w) }})
	case 2:
		// a failing (or the first) GitHub Actions check: open its logs
		if d := m.prDetails[k]; d != nil {
			var pick *Check
			for i := range d.checks {
				c := &d.checks[i]
				if !runIDRe.MatchString(c.DetailsURL) {
					continue
				}
				if _, concl := c.marks(); pick == nil || concl == "failure" {
					pick = c
					if concl == "failure" {
						break
					}
				}
			}
			if pick != nil {
				id, _ := strconv.ParseInt(runIDRe.FindStringSubmatch(pick.DetailsURL)[1], 10, 64)
				nwo := p.repo()
				return tea.Batch(m.setStatus("Opening the logs of "+pick.title()+"…", false), func() tea.Msg {
					c, err := api.DefaultRESTClient()
					var r Run
					if err == nil {
						err = c.Get(fmt.Sprintf("repos/%s/actions/runs/%d", nwo, id), &r)
					}
					r.Repo = nwo
					return runFetchedMsg{&r, err}
				})
			}
		}
		m.openReader(&reader{title: title + " · checks", sub: sub, icon: icActions,
			content: func(m *model, w int) []string { return m.prChecksLines(&pr, w) }})
	case 3:
		m.openReader(&reader{title: title + " · files", sub: sub, icon: icFile,
			docFn: func(m *model, w int) (mdDoc, bool) {
				f := m.prFilesMap[k]
				if f == nil {
					return mdDoc{lines: []string{fg(cMut).Render("Loading the changes…")}}, true
				}
				return diffDoc(f.files, w), true
			}})
	}
	return m.loadPRDetail(p, false)
}

// diffDoc is diffLines with every file as a heading (Contents lists the files)
func diffDoc(files []commitFile, w int) mdDoc {
	var d mdDoc
	for i, f := range files {
		one := diffLines([]commitFile{f}, w)
		if i > 0 {
			d.lines = append(d.lines, "")
		}
		d.heads = append(d.heads, mdHeading{2, f.Name, len(d.lines)})
		d.lines = append(d.lines, one...)
	}
	if len(files) == 0 {
		d.lines = []string{fg(cMut).Render("No changed files.")}
	}
	return d
}

func prConversationMD(p *PR, d *prDetail) string {
	type msg struct {
		at   time.Time
		head string
		body string
	}
	body := strings.TrimSpace(p.Body)
	if body == "" {
		body = "_No description._"
	}
	msgs := []msg{{p.Created, fmt.Sprintf("%s opened this %s ago", p.who(), ago(p.Created)), body}}
	if d != nil {
		for _, c := range d.comments {
			msgs = append(msgs, msg{c.Created, fmt.Sprintf("%s · %s ago", c.who(), ago(c.Created)), strings.TrimSpace(c.Body)})
		}
		for _, r := range d.reviews {
			who := "ghost"
			if r.Author != nil {
				who = r.Author.Login
			}
			what := map[string]string{"APPROVED": "✓ approved", "CHANGES_REQUESTED": "✗ asked for changes", "COMMENTED": "reviewed", "DISMISSED": "review dismissed"}[r.State]
			if r.State == "COMMENTED" && strings.TrimSpace(r.Body) == "" {
				continue
			}
			msgs = append(msgs, msg{r.Submitted, fmt.Sprintf("%s %s · %s ago", who, what, ago(r.Submitted)), strings.TrimSpace(r.Body)})
		}
	}
	sort.SliceStable(msgs, func(i, j int) bool { return msgs[i].at.Before(msgs[j].at) })
	var b strings.Builder
	for _, x := range msgs {
		fmt.Fprintf(&b, "## %s\n\n%s\n\n", x.head, x.body)
	}
	switch {
	case d == nil:
		b.WriteString("_Loading comments…_\n")
	case d.err != "":
		b.WriteString("_Couldn't load comments: " + d.err + "_\n")
	}
	if p.merged() {
		fmt.Fprintf(&b, "---\n\n_Merged into %s %s ago._\n", p.Base, ago(p.Merged))
	} else if !p.open() {
		fmt.Fprintf(&b, "---\n\n_Closed %s ago._\n", ago(p.Closed))
	}
	return b.String()
}

// ---- drawing ----

func prMark(p *PR) (string, lipgloss.Style) {
	switch {
	case p.merged():
		return icMerge, fg(lipgloss.Color("#c678dd"))
	case !p.open():
		return icPR, fg(cRed)
	case p.IsDraft:
		return icPR, fg(cMut)
	}
	return icPR, fg(cGreen)
}

func checksMark(state string) string {
	switch state {
	case "SUCCESS":
		return fg(cGreen).Render("✓")
	case "FAILURE", "ERROR":
		return fg(cRed).Render("✗")
	case "PENDING", "EXPECTED":
		return fg(cYellow).Render("●")
	}
	return ""
}

func (m *model) prRow(p *PR, w int, sel, showRepo bool) string {
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
	ic, ist := prMark(p)
	left := ist.Inherit(st).Render(ic) + st.Render(" ")
	if showRepo {
		left += st.Foreground(cMut).Render(strings.SplitN(p.repo(), "/", 2)[1] + " ")
	}
	left += st.Foreground(cMut).Render(fmt.Sprintf("#%d ", p.Number)) + st.Foreground(nc).Bold(sel).Render(p.Title)
	right := ""
	if cm := checksMark(p.checks()); cm != "" {
		ch := map[string]string{"SUCCESS": "✓", "FAILURE": "✗", "ERROR": "✗", "PENDING": "●", "EXPECTED": "●"}[p.checks()]
		c := map[string]lipgloss.Style{"✓": fg(cGreen), "✗": fg(cRed), "●": fg(cYellow)}[ch]
		right = c.Inherit(st).Render(ch) + st.Render(" ")
	}
	right += st.Foreground(cMut).Render(fmt.Sprintf("%4s", ago(p.Updated)))
	space := w - width(left) - width(right) - 1
	if space < 1 {
		left = trunc(left, w-width(right)-2)
		space = w - width(left) - width(right) - 1
	}
	return left + st.Render(strings.Repeat(" ", max(space, 0)+1)) + right
}

// prTitle draws the view chips (Overview  Commits 3  Checks ✓  Files 12) for the panel's top border
func (m *model) prTitle(p *PR, x0, y0 int) string {
	d := m.prDetails[prKey(p)]
	labels := []string{"Overview", "Commits", "Checks", "Files"}
	if d != nil {
		labels[1] += fmt.Sprintf(" %d", len(d.commits))
	}
	if cm := p.checks(); cm != "" {
		labels[2] += " " + map[string]string{"SUCCESS": "✓", "FAILURE": "✗", "ERROR": "✗", "PENDING": "●", "EXPECTED": "●"}[cm]
	}
	labels[3] += fmt.Sprintf(" %d", p.ChangedFiles)
	title := ""
	x := x0 + 3
	for i, l := range labels {
		if i > 0 {
			title += fg(cDim).Render("  ")
			x += 2
		}
		if i == m.prs.view {
			title += fg(cAcc).Bold(true).Render(l)
		} else {
			title += fg(cMut).Render(l)
		}
		m.addZone(x, y0, x+width(l)-1, y0, "prview", i)
		x += width(l)
	}
	return title
}

func (m *model) prPreview(p *PR, w int) []string {
	switch m.prs.view {
	case 1:
		return m.prCommitsLines(p, w)
	case 2:
		return m.prChecksLines(p, w)
	case 3:
		f := m.prFilesMap[prKey(p)]
		switch {
		case f == nil:
			return []string{fg(cMut).Render("Loading the changes…")}
		case f.err != "":
			return []string{fg(cRed).Render("Couldn't load the changes: " + f.err)}
		}
		return m.cachedLines(fmt.Sprintf("prfiles:%s:%d:%d", prKey(p), len(f.files), w), func() []string { return diffDoc(f.files, w).lines })
	}
	return m.prOverview(p, w)
}

func (m *model) prOverview(p *PR, w int) []string {
	out := wrapText(p.Title, w, fg(cAcc).Bold(true))
	ic, ist := prMark(p)
	state := "Open"
	switch {
	case p.merged():
		state = "Merged"
	case !p.open():
		state = "Closed"
	case p.IsDraft:
		state = "Draft"
	}
	meta := []string{
		ist.Render(ic + " " + state),
		fg(cMut).Render(fmt.Sprintf("#%d in %s", p.Number, p.repo())),
		lipgloss.NewStyle().Background(cChip).Foreground(cTxt).Render(" "+p.Head+" ") + fg(cMut).Render(" → ") + lipgloss.NewStyle().Background(cChip).Foreground(cTxt).Render(" "+p.Base+" "),
		fg(cMut).Render(p.who() + " · " + ago(p.Created) + " ago"),
	}
	out = append(out, wrapJoin(meta, "   ", w)...)
	var facts []string
	facts = append(facts, fg(cGreen).Render(fmt.Sprintf("+%d", p.Additions))+" "+fg(cRed).Render(fmt.Sprintf("−%d", p.Deletions))+fg(cMut).Render(fmt.Sprintf(" in %d %s", p.ChangedFiles, plural(p.ChangedFiles, "file", "files"))))
	switch p.checks() {
	case "SUCCESS":
		facts = append(facts, fg(cGreen).Render("✓ checks passed"))
	case "FAILURE", "ERROR":
		facts = append(facts, fg(cRed).Render("✗ checks failed"))
	case "PENDING", "EXPECTED":
		facts = append(facts, fg(cYellow).Render("● checks running"))
	}
	switch p.ReviewDecision {
	case "APPROVED":
		facts = append(facts, fg(cGreen).Render("✓ approved"))
	case "CHANGES_REQUESTED":
		facts = append(facts, fg(cRed).Render("✗ changes requested"))
	case "REVIEW_REQUIRED":
		facts = append(facts, fg(cYellow).Render("needs a review"))
	}
	if p.open() && p.Mergeable == "CONFLICTING" {
		facts = append(facts, fg(cRed).Render(icWarn+" conflicts with "+p.Base))
	}
	out = append(out, wrapJoin(facts, "   ", w)...)
	if len(p.Labels.Nodes) > 0 {
		var chips []string
		for _, l := range p.Labels.Nodes {
			chips = append(chips, labelChip(l))
		}
		out = append(out, wrapJoin(chips, " ", w)...)
	}
	h := [][2]string{{"enter", "read"}, {"[ ]", "views"}}
	if p.open() && !p.merged() {
		h = append(h, [2]string{"m", "merge"}, [2]string{"R", "review"})
	}
	h = append(h, [2]string{"C", "comment"}, [2]string{"K", "check out"})
	out = append(out, "", hints(h), sepLine(w), "")
	d := m.prDetails[prKey(p)]
	md := prConversationMD(p, d)
	return append(out, m.cachedLines(fmt.Sprintf("prov:%s:%d:%d", prKey(p), len(md), w), func() []string { return renderMarkdown(md, w) })...)
}

func (m *model) prCommitsLines(p *PR, w int) []string {
	d := m.prDetails[prKey(p)]
	switch {
	case d == nil:
		return []string{fg(cMut).Render("Loading commits…")}
	case d.err != "":
		return []string{fg(cRed).Render("Couldn't load: " + d.err)}
	}
	var out []string
	for _, c := range d.commits {
		out = append(out, trunc(fg(cMut).Render(c.Short)+" "+fg(cTxt).Render(c.Headline), w-6)+"  "+fg(cMut).Render(ago(c.Date)))
		out = append(out, "  "+fg(cMut).Render(c.who())+"  "+fg(cGreen).Render(fmt.Sprintf("+%d", c.Adds))+" "+fg(cRed).Render(fmt.Sprintf("−%d", c.Dels)))
	}
	if len(out) == 0 {
		return []string{fg(cMut).Render("No commits.")}
	}
	return out
}

func (m *model) prChecksLines(p *PR, w int) []string {
	d := m.prDetails[prKey(p)]
	switch {
	case d == nil:
		return []string{fg(cMut).Render("Loading checks…")}
	case d.err != "":
		return []string{fg(cRed).Render("Couldn't load: " + d.err)}
	case len(d.checks) == 0:
		return []string{fg(cMut).Render("No checks ran on this PR.")}
	}
	var out []string
	for _, c := range d.checks {
		status, concl := c.marks()
		line := statusMark(status, concl) + " " + fg(cTxt).Render(c.title())
		if s := spanDur(c.Started, c.Completed); s != "" && c.Typename == "CheckRun" {
			line += fg(cMut).Render("  " + s)
		}
		out = append(out, trunc(line, w))
	}
	return append(out, "", hints([][2]string{{"enter", "logs of the failing check"}, {"o", "open on GitHub"}}))
}

// ---- the PRs tab panels ----

func (m *model) prListBox(w, h, y0 int) []string {
	m.addZone(0, y0, w-1, y0+h-1, "list", 0)
	title := ""
	x := 3
	full := 0
	for _, n := range prScopes {
		full += width(n) + 2
	}
	for i, n := range prScopes {
		label := n
		if full > w-6 && i != m.prs.scope {
			label = strings.Fields(n)[0] // narrow: first word only
		}
		if i > 0 {
			title += fg(cDim).Render("  ")
			x += 2
		}
		if i == m.prs.scope {
			title += fg(cAcc).Bold(true).Render(label)
		} else {
			title += fg(cMut).Render(label)
		}
		m.addZone(x, y0, x+width(label)-1, y0, "prscope", i)
		x += width(label)
	}
	inner := w - 4
	var body []string
	rowY := y0 + 1
	if m.prs.filtering || m.prs.query != "" {
		cur := ""
		if m.prs.filtering {
			cur = fg(cAcc).Render("▏")
		}
		line := fg(cAcc).Render(icSearch+" ") + fg(cTxt).Render(m.prs.query) + cur
		if m.prs.query == "" {
			line += fg(cMut).Render(" find a PR")
		}
		body = append(body, line)
		rowY++
	}
	pl := m.prLists[m.prTabQuery()]
	items := m.prItems()
	rows := h - 2 - len(body)
	switch {
	case pl == nil:
		body = append(body, "", fg(cMut).Render("Loading pull requests…"))
	case pl.err != "":
		body = append(body, "", fg(cRed).Render("Couldn't load PRs:"), fg(cMut).Render(pl.err))
	case len(items) == 0 && m.prs.query != "":
		body = append(body, "", fg(cMut).Render("Nothing matches \""+m.prs.query+"\""))
	case len(items) == 0 && m.prs.closed:
		body = append(body, "", fg(cMut).Render("No closed or merged PRs here."))
	case len(items) == 0:
		body = append(body, "", fg(cGreen).Render(icCheck+" No open pull requests"), fg(cMut).Render("N opens one, s shows closed / merged."))
	}
	for i := m.prs.off; i < len(items) && i < m.prs.off+rows; i++ {
		body = append(body, m.prRow(items[i], inner, i == m.prs.cur, true))
		y := rowY + (i - m.prs.off)
		m.addZone(1, y, w-2, y, "prrow", i)
	}
	state := "open"
	if m.prs.closed {
		state = "closed / merged"
	}
	note := state
	if len(items) > 0 {
		note = fmt.Sprintf("%s · %d/%d", state, m.prs.cur+1, len(items))
	}
	return box(title, w, h, body, m.focus == 0, note)
}

func (m *model) prPanel(w, x0 int) (string, []string) {
	p := m.selPR()
	if p == nil {
		return "Pull request", []string{fg(cMut).Render("Pick a pull request on the left.")}
	}
	return m.prTitle(p, x0, 2), m.prPreview(p, w)
}

func (m *model) prButtons() []button {
	inPRs := m.tab == tabPRs || m.exActive() != nil && m.ex.sec == secPRs
	if !inPRs {
		return nil
	}
	var b []button
	if p := m.selPR(); p != nil {
		b = append(b, button{icRead + " Read", "enter", "prread"})
		if p.open() && !p.merged() {
			b = append(b, button{icMerge + " Merge", "m", "prmerge"}, button{icCheck + " Review", "R", "prreview"})
		}
		b = append(b, button{icComment + " Comment", "C", "prcomment"}, button{icBranch + " Check out", "K", "prcheckout"})
	}
	shown := "closed"
	if m.prs.closed {
		shown = "open"
	}
	return append(b, button{icPR + " New", "N", "prnew"}, button{"Show " + shown, "s", "prstate"},
		button{icGlobe + " Browser", "o", "browser"}, button{icRefresh + " Refresh", "r", "refresh"})
}
