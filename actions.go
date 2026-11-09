package main

// Actions (CI runs: jobs, steps, logs, re-run, cancel, start a workflow) and
// Releases (notes, download files, create a release). Shown in the Actions tab
// (all your repos) and as sections inside a repo.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/color"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/cli/go-gh/v2/pkg/api"
)

// ---- data ----

type Run struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"` // workflow name
	Title      string    `json:"display_title"`
	Number     int       `json:"run_number"`
	Attempt    int       `json:"run_attempt"`
	Event      string    `json:"event"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	Branch     string    `json:"head_branch"`
	Sha        string    `json:"head_sha"`
	URL        string    `json:"html_url"`
	Created    time.Time `json:"created_at"`
	Updated    time.Time `json:"updated_at"`
	Started    time.Time `json:"run_started_at"`
	WorkflowID int64     `json:"workflow_id"`
	Actor      struct {
		Login string `json:"login"`
	} `json:"actor"`
	Repo string `json:"-"`
}

func (r *Run) running() bool { return r.Status != "completed" }
func (r *Run) failed() bool {
	return r.Status == "completed" && (r.Conclusion == "failure" || r.Conclusion == "timed_out" || r.Conclusion == "startup_failure")
}

type Step struct {
	Name       string    `json:"name"`
	Number     int       `json:"number"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	Started    time.Time `json:"started_at"`
	Completed  time.Time `json:"completed_at"`
}

type Job struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	Conclusion string    `json:"conclusion"`
	Started    time.Time `json:"started_at"`
	Completed  time.Time `json:"completed_at"`
	URL        string    `json:"html_url"`
	Steps      []Step    `json:"steps"`
}

type runList struct {
	items []Run
	err   string
}

type jobList struct {
	items []Job
	err   string
}

type Workflow struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Path  string `json:"path"`
	State string `json:"state"`
}

type Asset struct {
	Name      string `json:"name"`
	Size      int    `json:"size"`
	Downloads int    `json:"download_count"`
	URL       string `json:"browser_download_url"`
}

type Release struct {
	ID         int64     `json:"id"`
	Tag        string    `json:"tag_name"`
	Name       string    `json:"name"`
	Body       string    `json:"body"`
	Draft      bool      `json:"draft"`
	Prerelease bool      `json:"prerelease"`
	Published  time.Time `json:"published_at"`
	Created    time.Time `json:"created_at"`
	URL        string    `json:"html_url"`
	Author     struct {
		Login string `json:"login"`
	} `json:"author"`
	Assets []Asset `json:"assets"`
	Latest bool    `json:"-"`
}

func (r *Release) title() string {
	if strings.TrimSpace(r.Name) != "" {
		return r.Name
	}
	return r.Tag
}

func (r *Release) date() time.Time {
	if !r.Published.IsZero() {
		return r.Published
	}
	return r.Created
}

type releaseList struct {
	items []Release
	err   string
}

// ---- fetching ----

func fetchRuns(nwo string) func() func(*model) {
	return func() func(*model) {
		rl := &runList{}
		c, err := api.DefaultRESTClient()
		if err == nil {
			var r struct {
				Runs []Run `json:"workflow_runs"`
			}
			if err = c.Get("repos/"+nwo+"/actions/runs?per_page=20", &r); err == nil {
				for i := range r.Runs {
					r.Runs[i].Repo = nwo
				}
				rl.items = r.Runs
			}
		}
		if err != nil {
			rl.err = apiErr(err)
		}
		return func(m *model) {
			m.runs[nwo] = rl
			m.saveRuns()
		}
	}
}

func fetchJobs(nwo string, id int64) func() func(*model) {
	return func() func(*model) {
		jl := &jobList{}
		c, err := api.DefaultRESTClient()
		if err == nil {
			var r struct {
				Jobs []Job `json:"jobs"`
			}
			if err = c.Get(fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=50", nwo, id), &r); err == nil {
				jl.items = r.Jobs
			}
		}
		if err != nil {
			jl.err = apiErr(err)
		}
		key := fmt.Sprintf("%s#%d", nwo, id)
		return func(m *model) { m.jobs[key] = jl }
	}
}

func fetchReleases(nwo string) func() func(*model) {
	return func() func(*model) {
		rl := &releaseList{}
		c, err := api.DefaultRESTClient()
		if err == nil {
			err = c.Get("repos/"+nwo+"/releases?per_page=30", &rl.items)
			for i := range rl.items {
				if !rl.items[i].Draft && !rl.items[i].Prerelease {
					rl.items[i].Latest = true
					break
				}
			}
		}
		if err != nil {
			rl.err = apiErr(err)
		}
		return func(m *model) { m.releases[nwo] = rl }
	}
}

// runs are saved so the Actions tab opens instantly next time
func (m *model) saveRuns() {
	if m.dump {
		return
	}
	out := map[string][]Run{}
	for k, v := range m.runs {
		if v.err == "" {
			out[k] = v.items
		}
	}
	saveCache("runs.json", out)
}

func (m *model) loadRuns() {
	var saved map[string][]Run
	if loadCache("runs.json", &saved) {
		for k, v := range saved {
			for i := range v {
				v[i].Repo = k
			}
			m.runs[k] = &runList{items: v}
		}
	}
}

// ---- status marks ----

func markParts(status, conclusion string) (string, color.Color) {
	switch {
	case status == "queued" || status == "waiting" || status == "pending" || status == "requested":
		return "○", cYellow
	case status != "completed":
		return "●", cYellow
	}
	switch conclusion {
	case "success":
		return "✓", cGreen
	case "failure", "timed_out", "startup_failure":
		return "✗", cRed
	case "cancelled":
		return "⊘", cMut
	case "skipped", "neutral":
		return "–", cMut
	}
	return "·", cMut
}

func statusMark(status, conclusion string) string {
	ch, c := markParts(status, conclusion)
	return fg(c).Render(ch)
}

func statusWord(status, conclusion string) string {
	switch {
	case status == "in_progress":
		return fg(cYellow).Render("running")
	case status != "completed":
		return fg(cYellow).Render(strings.ReplaceAll(status, "_", " "))
	}
	switch conclusion {
	case "success":
		return fg(cGreen).Render("passed")
	case "failure":
		return fg(cRed).Render("failed")
	case "timed_out":
		return fg(cRed).Render("timed out")
	case "startup_failure":
		return fg(cRed).Render("couldn't start")
	}
	return fg(cMut).Render(strings.ReplaceAll(conclusion, "_", " "))
}

func dur(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < 0:
		return ""
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm %ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
}

func runDur(r *Run) string {
	start := r.Started
	if start.IsZero() {
		start = r.Created
	}
	if r.running() {
		return dur(time.Since(start))
	}
	return dur(r.Updated.Sub(start))
}

func spanDur(a, b time.Time) string {
	if a.IsZero() {
		return ""
	}
	if b.IsZero() {
		return dur(time.Since(a))
	}
	return dur(b.Sub(a))
}

// ---- the Actions tab (all repos) ----

type actState struct {
	cur, off  int
	filter    int // 0 all, 1 failed, 2 running
	query     string
	filtering bool
}

var actFilters = []string{"All", "Failed", "Running"}

// repos worth checking for CI: pushed in the last 6 months, newest first
func (m *model) ciRepos() []string {
	var out []string
	for _, r := range m.repos {
		if r.IsArchived || time.Since(r.PushedAt) > 183*24*time.Hour {
			continue
		}
		out = append(out, r.NameWithOwner)
		if len(out) >= 25 {
			break
		}
	}
	return out
}

func (m *model) actLoadAll() tea.Cmd {
	var cmds []tea.Cmd
	for _, nwo := range m.ciRepos() {
		cmds = append(cmds, m.load("runs:"+nwo, false, fetchRuns(nwo)))
	}
	return tea.Batch(cmds...)
}

// runsOrder: running first, then still-broken ones (the newest run of that workflow
// on that branch failed), then the rest; newest first in each
func runsOrder(list []*Run) {
	newest := map[string]*Run{}
	for _, r := range list {
		k := fmt.Sprintf("%s|%d|%s", r.Repo, r.WorkflowID, r.Branch)
		if n := newest[k]; n == nil || r.Created.After(n.Created) {
			newest[k] = r
		}
	}
	rank := func(r *Run) int {
		switch {
		case r.running():
			return 0
		case r.failed() && newest[fmt.Sprintf("%s|%d|%s", r.Repo, r.WorkflowID, r.Branch)] == r:
			return 1
		}
		return 2
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if rank(a) != rank(b) {
			return rank(a) < rank(b)
		}
		return a.Created.After(b.Created)
	})
}

func (m *model) actItems() []*Run {
	q := strings.ToLower(m.act.query)
	var out []*Run
	for _, nwo := range m.ciRepos() {
		rl := m.runs[nwo]
		if rl == nil {
			continue
		}
		for i := range rl.items {
			r := &rl.items[i]
			if m.act.filter == 1 && !r.failed() || m.act.filter == 2 && !r.running() {
				continue
			}
			if q != "" && !fuzzy(strings.ToLower(r.Repo+" "+r.Name+" "+r.Title+" "+r.Branch), q) {
				continue
			}
			out = append(out, r)
		}
	}
	runsOrder(out)
	return out
}

func (m *model) actLoading() (done, total int) {
	for _, nwo := range m.ciRepos() {
		total++
		if m.loaded["runs:"+nwo] || m.runs[nwo] != nil {
			done++
		}
	}
	return
}

// selRun is the run the keys act on (Actions tab or a repo's Actions section)
func (m *model) selRun() *Run {
	if m.tab == tabActions {
		items := m.actItems()
		if m.act.cur >= 0 && m.act.cur < len(items) {
			return items[m.act.cur]
		}
		return nil
	}
	if ex := m.exActive(); ex != nil && ex.sec == secActions {
		if it, ok := m.exSel(); ok && it.kind == "run" {
			return it.run
		}
	}
	return nil
}

func (m *model) selRelease() *Release {
	if ex := m.exActive(); ex != nil && ex.sec == secReleases {
		if it, ok := m.exSel(); ok && it.kind == "release" {
			return it.release
		}
	}
	return nil
}

// the repo for "start a workflow" / "new release"
func (m *model) actRepo() string {
	if r := m.selRun(); r != nil {
		return r.Repo
	}
	if ex := m.exActive(); ex != nil {
		return ex.repo.NameWithOwner
	}
	return ""
}

func (m *model) loadJobs(r *Run, debounce bool) tea.Cmd {
	if r == nil {
		return nil
	}
	return m.load(fmt.Sprintf("jobs:%s#%d", r.Repo, r.ID), debounce, fetchJobs(r.Repo, r.ID))
}

func (m *model) actSelected() tea.Cmd {
	m.pvOff = 0
	n := len(m.actItems())
	m.act.cur = max(0, min(m.act.cur, n-1))
	rows := m.listRows() - 1
	if m.act.filtering || m.act.query != "" {
		rows--
	}
	if m.act.cur < m.act.off {
		m.act.off = m.act.cur
	}
	if rows > 0 && m.act.cur >= m.act.off+rows {
		m.act.off = m.act.cur - rows + 1
	}
	return m.loadJobs(m.selRun(), true)
}

func (m *model) actMove(d int) tea.Cmd {
	n := len(m.actItems())
	if n == 0 {
		return nil
	}
	old := m.act.cur
	m.act.cur = max(0, min(n-1, old+d))
	if m.act.cur == old {
		return nil
	}
	return m.actSelected()
}

func (m *model) actRefresh() tea.Cmd {
	for k := range m.loaded {
		if strings.HasPrefix(k, "runs:") || strings.HasPrefix(k, "jobs:") {
			delete(m.loaded, k)
		}
	}
	return tea.Batch(m.actLoadAll(), m.loadJobs(m.selRun(), false))
}

// keys shared by the Actions tab and a repo's Actions / Releases sections
func (m *model) ciKey(s string) (tea.Cmd, bool) {
	switch s {
	case "R":
		return m.actRerun(), true
	case "x":
		return m.actCancel(), true
	case "w":
		return m.actWorkflows(), true
	case "d":
		return m.actDownload(), true
	case "N":
		return m.actNewRelease(), true
	}
	return nil, false
}

func (m *model) actKey(s string) tea.Cmd {
	if cmd, ok := m.ciKey(s); ok {
		return cmd
	}
	half := m.listRows() / 2
	switch s {
	case "tab", "shift+tab":
		m.focus = 1 - m.focus
	case "f":
		m.act.filter = (m.act.filter + 1) % len(actFilters)
		m.act.cur, m.act.off = 0, 0
		return m.actSelected()
	case "/":
		m.act.filtering, m.focus = true, 0
	case "r":
		return m.actRefresh()
	case "o":
		return m.actBrowser()
	case "y":
		return m.actCopy()
	case "enter", "v":
		return m.actLogs()
	case "l", "right":
		m.focus = 1
	case "h", "left":
		m.focus = 0
	case "j", "down":
		if m.focus == 0 {
			return m.actMove(1)
		}
		m.scroll(1)
	case "k", "up":
		if m.focus == 0 {
			return m.actMove(-1)
		}
		m.scroll(-1)
	case "pgdown", "space":
		if m.focus == 0 {
			return m.actMove(half * 2)
		}
		m.scroll(half * 2)
	case "pgup":
		if m.focus == 0 {
			return m.actMove(-half * 2)
		}
		m.scroll(-half * 2)
	case "g", "home":
		if m.focus == 0 {
			return m.actMove(-1 << 20)
		}
		m.pvOff = 0
	case "G", "end":
		if m.focus == 0 {
			return m.actMove(1 << 20)
		}
		m.pvOff = m.pvMax
	}
	return nil
}

// ---- doing things ----

type ciDoneMsg struct {
	nwo  string
	msg  string
	err  error
	what string // "runs" or "releases": what to reload
}

func post(path string, body any) error {
	c, err := api.DefaultRESTClient()
	if err != nil {
		return err
	}
	var rd io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		rd = bytes.NewReader(data)
	}
	return c.Post(path, rd, nil)
}

func (m *model) actRerun() tea.Cmd { return m.actRerunFor(m.selRun()) }

func (m *model) actRerunFor(r *Run) tea.Cmd {
	if r == nil {
		return nil
	}
	if r.running() {
		return m.setStatus("This run is still going: cancel it first (x)", true)
	}
	path, what := fmt.Sprintf("repos/%s/actions/runs/%d/rerun", r.Repo, r.ID), "Re-run all jobs of"
	if r.failed() {
		path, what = path+"-failed-jobs", "Re-run the failed jobs of"
	}
	nwo, label := r.Repo, fmt.Sprintf("%s #%d", r.Name, r.Number)
	m.prompt = &prompt{label: what + " " + label + "?", confirm: true, done: func(m *model, _ string) tea.Cmd {
		return func() tea.Msg { return ciDoneMsg{nwo, "Started again: " + label, apiWrap(post(path, nil)), "runs"} }
	}}
	return nil
}

func (m *model) actCancel() tea.Cmd {
	r := m.selRun()
	if r == nil {
		return nil
	}
	if !r.running() {
		return m.setStatus("This run already finished", true)
	}
	nwo, label := r.Repo, fmt.Sprintf("%s #%d", r.Name, r.Number)
	path := fmt.Sprintf("repos/%s/actions/runs/%d/cancel", r.Repo, r.ID)
	m.prompt = &prompt{label: "Cancel " + label + "?", confirm: true, done: func(m *model, _ string) tea.Cmd {
		return func() tea.Msg { return ciDoneMsg{nwo, "Cancelled " + label, apiWrap(post(path, nil)), "runs"} }
	}}
	return nil
}

func apiWrap(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", apiErr(err))
}

type workflowsMsg struct {
	nwo, branch string
	list        []Workflow
	err         error
}

// w: pick a workflow in the palette, then start it on the default branch
func (m *model) actWorkflows() tea.Cmd {
	nwo := m.actRepo()
	if nwo == "" {
		return nil
	}
	branch := "main"
	for _, r := range m.repos {
		if r.NameWithOwner == nwo && r.Branch() != "" {
			branch = r.Branch()
		}
	}
	return tea.Batch(m.setStatus("Looking up workflows…", false), func() tea.Msg {
		c, err := api.DefaultRESTClient()
		var r struct {
			Workflows []Workflow `json:"workflows"`
		}
		if err == nil {
			err = c.Get("repos/"+nwo+"/actions/workflows", &r)
		}
		return workflowsMsg{nwo, branch, r.Workflows, err}
	})
}

func (m *model) onWorkflows(msg workflowsMsg) tea.Cmd {
	if msg.err != nil {
		return m.setStatus("Couldn't list workflows: "+apiErr(msg.err), true)
	}
	var items []command
	for _, w := range msg.list {
		if w.State != "active" {
			continue
		}
		w := w
		nwo, branch := msg.nwo, msg.branch
		items = append(items, command{"Start " + w.Name + "  (" + filepath.Base(w.Path) + ")", "", func(m *model) tea.Cmd {
			m.prompt = &prompt{label: "Start " + w.Name + " on " + branch + "?", confirm: true, done: func(m *model, _ string) tea.Cmd {
				return func() tea.Msg {
					err := apiWrap(post(fmt.Sprintf("repos/%s/actions/workflows/%d/dispatches", nwo, w.ID), map[string]string{"ref": branch}))
					if err != nil && strings.Contains(err.Error(), "workflow_dispatch") {
						err = fmt.Errorf("%s can't be started by hand: its file needs `on: workflow_dispatch`", w.Name)
					}
					return ciDoneMsg{nwo, "Started " + w.Name + " on " + branch, err, "runs"}
				}
			}}
			return nil
		}})
	}
	if len(items) == 0 {
		return m.setStatus(msg.nwo+" has no workflows", true)
	}
	m.status = ""
	m.openPicker("Start a workflow in "+msg.nwo, items)
	return nil
}

// ---- logs: every job's log in the reader, jobs and steps as headings ----

type logsMsg struct {
	key  string
	text map[int64]string
	err  error
}

var (
	tsRe    = regexp.MustCompile(`^\x{feff}?\d{4}-\d\d-\d\dT[\d:.]+Z ?`)
	logDocs = map[string]map[int]mdDoc{}
)

func (m *model) actLogs() tea.Cmd { return m.actLogsFor(m.selRun()) }

func (m *model) actLogsFor(r *Run) tea.Cmd {
	if r == nil {
		return nil
	}
	key := fmt.Sprintf("%s#%d", r.Repo, r.ID)
	run := *r
	rd := &reader{title: fmt.Sprintf("%s #%d", r.Name, r.Number), sub: r.Repo + " · " + r.Branch, icon: icActions, autoJump: true}
	rd.docFn = func(m *model, w int) (mdDoc, bool) {
		jl := m.jobs[key]
		logs, ok := m.logs[key]
		if jl == nil || !ok {
			return mdDoc{lines: []string{fg(cMut).Render("Loading the logs…")}}, true
		}
		if d, ok := logDocs[key][w]; ok {
			return d, true
		}
		d := buildLogDoc(&run, jl.items, logs, w)
		if logDocs[key] == nil {
			logDocs[key] = map[int]mdDoc{}
		}
		logDocs[key][w] = d
		return d, true
	}
	m.openReader(rd)
	return tea.Batch(m.loadJobs(r, false), m.fetchLogs(r))
}

func (m *model) fetchLogs(r *Run) tea.Cmd {
	key := fmt.Sprintf("%s#%d", r.Repo, r.ID)
	if _, ok := m.logs[key]; ok && !r.running() {
		return nil
	}
	nwo, id := r.Repo, r.ID
	return func() tea.Msg {
		c, err := api.DefaultRESTClient()
		if err != nil {
			return logsMsg{key, nil, err}
		}
		var jr struct {
			Jobs []Job `json:"jobs"`
		}
		if err := c.Get(fmt.Sprintf("repos/%s/actions/runs/%d/jobs?per_page=50", nwo, id), &jr); err != nil {
			return logsMsg{key, nil, err}
		}
		out := map[int64]string{}
		for _, j := range jr.Jobs {
			if j.Status != "completed" {
				continue
			}
			resp, err := c.Request("GET", fmt.Sprintf("repos/%s/actions/jobs/%d/logs", nwo, j.ID), nil)
			if err != nil {
				out[j.ID] = "\x00" + apiErr(err)
				continue
			}
			data, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			out[j.ID] = string(data)
		}
		return logsMsg{key, out, nil}
	}
}

func headBar(level int, text string, w int) string {
	lvl := min(max(level, 1), 6) - 1
	c := headColors[lvl]
	bg := lipgloss.NewStyle().Background(blend(c, 0.22))
	l := trunc(bg.Foreground(c).Bold(true).Render(" "+headIcons[lvl]+" "+text), w)
	return l + bg.Render(strings.Repeat(" ", max(w-width(l), 0)))
}

func buildLogDoc(r *Run, jobs []Job, logs map[int64]string, w int) mdDoc {
	var d mdDoc
	d.jump = -1
	add := func(l string) { d.lines = append(d.lines, l) }
	for ji, j := range jobs {
		if ji > 0 {
			add("")
		}
		name := ansi.Strip(statusMark(j.Status, j.Conclusion)) + " " + j.Name
		if s := spanDur(j.Started, j.Completed); s != "" {
			name += "  (" + s + ")"
		}
		d.heads = append(d.heads, mdHeading{1, name, len(d.lines)})
		add(headBar(1, name, w))
		text, ok := logs[j.ID]
		switch {
		case j.Status != "completed":
			add(fg(cYellow).Render("● still running: its log shows up here when it finishes."))
			for _, s := range j.Steps {
				add("  " + statusMark(s.Status, s.Conclusion) + " " + fg(cTxt).Render(s.Name))
			}
			continue
		case !ok:
			add(fg(cMut).Render("No log."))
			continue
		case strings.HasPrefix(text, "\x00"):
			add(fg(cRed).Render("Couldn't load this log: " + text[1:]))
			continue
		}
		// mark each step's heading with how it ended
		stepMark := map[string]string{}
		for _, s := range j.Steps {
			stepMark[strings.ToLower(s.Name)] = ansi.Strip(statusMark(s.Status, s.Conclusion))
		}
		group, groupName := -1, ""
		for _, raw := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
			l := ansi.Strip(tsRe.ReplaceAllString(raw, ""))
			switch {
			case strings.HasPrefix(l, "##[group]"):
				t := strings.TrimPrefix(l, "##[group]")
				mark := stepMark[strings.ToLower(t)]
				if mark == "" {
					mark = "▸"
				}
				group, groupName = len(d.heads), t
				d.heads = append(d.heads, mdHeading{2, mark + " " + t, len(d.lines)})
				st := fg(cAcc).Bold(true)
				if mark == "✗" {
					st = fg(cRed).Bold(true)
				}
				add(st.Render(trunc(mark+" "+t, w)))
				continue
			case strings.HasPrefix(l, "##[endgroup]"):
				continue
			}
			st := fg(cTxt)
			prefix := "  "
			switch {
			case strings.HasPrefix(l, "##[error]"):
				l, st, prefix = strings.TrimPrefix(l, "##[error]"), fg(cRed), fg(cRed).Render("✗ ")
				if d.jump < 0 {
					d.jump = len(d.lines)
				}
				// the step this error is in gets a red ✗ (in Contents too)
				if group >= 0 && !strings.HasPrefix(d.heads[group].text, "✗") {
					d.heads[group].text = "✗ " + groupName
					d.lines[d.heads[group].line] = fg(cRed).Bold(true).Render(trunc("✗ "+groupName, w))
				}
			case strings.HasPrefix(l, "##[warning]"):
				l, st, prefix = strings.TrimPrefix(l, "##[warning]"), fg(cYellow), fg(cYellow).Render("! ")
			case strings.HasPrefix(l, "##[notice]"):
				l, st = strings.TrimPrefix(l, "##[notice]"), fg(cAcc)
			case strings.HasPrefix(l, "##[debug]"):
				l, st = strings.TrimPrefix(l, "##[debug]"), fg(cMut)
			case strings.HasPrefix(l, "##[command]"):
				l, st = strings.TrimPrefix(l, "##[command]"), fg(cHint)
			}
			for i, wl := range wrapLines(st.Render(l), w-2) {
				if i == 0 {
					add(prefix + wl)
				} else {
					add("  " + wl)
				}
			}
		}
	}
	if len(jobs) == 0 {
		add(fg(cMut).Render("This run has no jobs."))
	}
	return d
}

// ---- releases ----

func (m *model) actDownload() tea.Cmd {
	rel := m.selRelease()
	if rel == nil {
		return nil
	}
	nwo := m.ex.repo.NameWithOwner
	if len(rel.Assets) == 0 {
		return m.setStatus("This release has no files (o opens it on GitHub for the source code)", true)
	}
	dir := filepath.Join(home("Downloads"), m.ex.repo.Name+"-"+rel.Tag)
	get := func(pattern, what string) func(m *model) tea.Cmd {
		return func(m *model) tea.Cmd {
			m.prompt = &prompt{label: "Download " + what + " to " + tildePath(dir) + "?", confirm: true, done: func(m *model, _ string) tea.Cmd {
				args := []string{"release", "download", rel.Tag, "-R", nwo, "-D", dir, "--clobber"}
				if pattern != "" {
					args = append(args, "-p", pattern)
				}
				return tea.Batch(m.setStatus("Downloading…", false), func() tea.Msg {
					out, err := exec.Command("gh", args...).CombinedOutput()
					if err != nil {
						err = fmt.Errorf("%s", strings.TrimSpace(string(out)))
					}
					return ciDoneMsg{nwo, "Downloaded to " + tildePath(dir), err, ""}
				})
			}}
			return nil
		}
	}
	total := 0
	for _, a := range rel.Assets {
		total += a.Size
	}
	items := []command{{fmt.Sprintf("All %d files (%s)", len(rel.Assets), humanSize(total)), "", get("", "all files")}}
	for _, a := range rel.Assets {
		items = append(items, command{a.Name + "  (" + humanSize(a.Size) + ")", "", get(a.Name, a.Name)})
	}
	if len(rel.Assets) == 1 {
		return items[1].run(m)
	}
	m.openPicker("Download from "+rel.title(), items)
	return nil
}

var semverRe = regexp.MustCompile(`^(v?)(\d+)\.(\d+)\.(\d+)$`)

func nextVersion(tag string) string {
	p := semverRe.FindStringSubmatch(tag)
	if p == nil {
		return "v1.0.0"
	}
	n, _ := strconv.Atoi(p[4])
	return fmt.Sprintf("%s%s.%s.%d", p[1], p[2], p[3], n+1)
}

type notesDoneMsg struct {
	nwo, tag, title, target, file string
	err                           error
}

// N: tag → title → notes (written in Nvim, or made from the commits) → publish
func (m *model) actNewRelease() tea.Cmd {
	ex := m.exActive()
	if ex == nil {
		return m.setStatus("Open a repo first (its Releases section)", true)
	}
	nwo, target := ex.repo.NameWithOwner, ex.repo.Branch()
	suggest := "v1.0.0"
	if rl := m.releases[nwo]; rl != nil && len(rl.items) > 0 {
		suggest = nextVersion(rl.items[0].Tag)
	}
	m.prompt = &prompt{label: "New release tag:", text: suggest, done: func(m *model, tag string) tea.Cmd {
		if tag == "" {
			return nil
		}
		m.prompt = &prompt{label: "Title:", text: tag, done: func(m *model, title string) tea.Cmd {
			if title == "" {
				title = tag
			}
			m.prompt = &prompt{label: "Write the notes in Nvim? (n = make them from the commits)", confirm: true, no: func(m *model) tea.Cmd {
				return m.confirmRelease(nwo, tag, title, target, "")
			}, done: func(m *model, _ string) tea.Cmd {
				f := filepath.Join(os.TempDir(), "termgit-release-notes.md")
				os.WriteFile(f, []byte("## What's new\n\n- \n"), 0o644)
				c := exec.Command("nvim", f)
				if m.dump {
					return m.confirmRelease(nwo, tag, title, target, f)
				}
				return tea.ExecProcess(c, func(err error) tea.Msg { return notesDoneMsg{nwo, tag, title, target, f, err} })
			}}
			return nil
		}}
		return nil
	}}
	return nil
}

func (m *model) confirmRelease(nwo, tag, title, target, notes string) tea.Cmd {
	how := "notes made from the commits"
	if notes != "" {
		how = "your notes"
	}
	m.prompt = &prompt{label: "Publish " + title + " (" + tag + " on " + target + ", " + how + ")?", confirm: true, done: func(m *model, _ string) tea.Cmd {
		args := []string{"release", "create", tag, "-R", nwo, "--title", title, "--target", target}
		if notes != "" {
			args = append(args, "--notes-file", notes)
		} else {
			args = append(args, "--generate-notes")
		}
		return tea.Batch(m.setStatus("Publishing…", false), func() tea.Msg {
			out, err := exec.Command("gh", args...).CombinedOutput()
			if err != nil {
				err = fmt.Errorf("%s", strings.TrimSpace(string(out)))
			}
			return ciDoneMsg{nwo, "Published " + title, err, "releases"}
		})
	}}
	return nil
}

// ---- drawing: rows and previews ----

func (m *model) runRow(r *Run, w int, sel, showRepo bool) string {
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
	name := r.Title
	if showRepo {
		name = strings.SplitN(r.Repo, "/", 2)[1] + " · " + r.Name
	}
	ch, c := markParts(r.Status, r.Conclusion)
	left := st.Foreground(c).Render(ch) + st.Render(" ") + st.Foreground(nc).Bold(sel).Render(name)
	right := st.Foreground(cMut).Render(fmt.Sprintf("%4s", ago(r.Created)))
	space := w - width(left) - width(right) - 1
	if space < 1 {
		left = trunc(left, w-width(right)-2)
		space = w - width(left) - width(right) - 1
	}
	return left + st.Render(strings.Repeat(" ", max(space, 0)+1)) + right
}

func (m *model) runPreview(r *Run, w int) []string {
	var out []string
	out = append(out, wrapText(r.Title, w, fg(cAcc).Bold(true))...)
	out = append(out, fg(cTxt).Render(fmt.Sprintf("%s #%d", r.Name, r.Number))+fg(cMut).Render("  in "+r.Repo), "")
	meta := []string{
		statusMark(r.Status, r.Conclusion) + " " + statusWord(r.Status, r.Conclusion),
		fg(cMut).Render(icBranch + " " + r.Branch),
		fg(cMut).Render(icClock + " " + ago(r.Created) + " ago · took " + runDur(r)),
		fg(cMut).Render(strings.ReplaceAll(r.Event, "_", " ") + " by " + r.Actor.Login),
	}
	if r.Attempt > 1 {
		meta = append(meta, fg(cMut).Render(fmt.Sprintf("attempt %d", r.Attempt)))
	}
	out = append(out, wrapJoin(meta, "   ", w)...)
	out = append(out, sepLine(w), "")
	key := fmt.Sprintf("%s#%d", r.Repo, r.ID)
	jl := m.jobs[key]
	switch {
	case jl == nil:
		out = append(out, fg(cMut).Render("Loading jobs…"))
	case jl.err != "":
		out = append(out, fg(cRed).Render("Couldn't load the jobs: "+jl.err))
	case len(jl.items) == 0:
		out = append(out, fg(cMut).Render("No jobs."))
	}
	if jl != nil {
		for _, j := range jl.items {
			d := spanDur(j.Started, j.Completed)
			head := statusMark(j.Status, j.Conclusion) + " " + fg(cTxt).Bold(true).Render(j.Name)
			if d != "" {
				head += fg(cMut).Render("  " + d)
			}
			out = append(out, head)
			for _, s := range j.Steps {
				line := "  " + statusMark(s.Status, s.Conclusion) + " " + fg(cHint).Render(s.Name)
				if s.Status == "completed" && s.Conclusion != "skipped" {
					if d := spanDur(s.Started, s.Completed); d != "" && d != "0s" {
						line += fg(cMut).Render("  " + d)
					}
				}
				out = append(out, trunc(line, w))
			}
			out = append(out, "")
		}
	}
	h := [][2]string{{"enter", "logs"}}
	if r.running() {
		h = append(h, [2]string{"x", "cancel"})
		out = append(out, fg(cYellow).Render("● running: this panel updates by itself every few seconds"))
	} else {
		h = append(h, [2]string{"R", "re-run"})
	}
	h = append(h, [2]string{"w", "start a workflow"}, [2]string{"o", "open on GitHub"})
	return append(out, hints(h))
}

func (m *model) releaseRow(r *Release, w int, sel bool) string {
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
	ic := cMut
	if r.Latest {
		ic = cGreen
	}
	left := st.Foreground(ic).Render(icTag) + st.Render(" ") + st.Foreground(nc).Bold(sel).Render(r.title())
	switch {
	case r.Draft:
		left += st.Foreground(cMut).Render("  draft")
	case r.Prerelease:
		left += st.Foreground(cYellow).Render("  pre")
	case r.Latest:
		left += st.Foreground(cGreen).Render("  latest")
	}
	right := st.Foreground(cMut).Render(fmt.Sprintf("%4s", ago(r.date())))
	space := w - width(left) - width(right) - 1
	if space < 1 {
		left = trunc(left, w-width(right)-2)
		space = w - width(left) - width(right) - 1
	}
	return left + st.Render(strings.Repeat(" ", max(space, 0)+1)) + right
}

func (m *model) releasePreview(r *Release, w int) []string {
	out := wrapText(r.title(), w, fg(cAcc).Bold(true))
	var tags []string
	tags = append(tags, lipgloss.NewStyle().Background(cChip).Foreground(cTxt).Render(" "+icTag+" "+r.Tag+" "))
	switch {
	case r.Draft:
		tags = append(tags, fg(cMut).Render("draft"))
	case r.Prerelease:
		tags = append(tags, fg(cYellow).Render("pre-release"))
	case r.Latest:
		tags = append(tags, fg(cGreen).Render("latest"))
	}
	tags = append(tags, fg(cMut).Render("by "+r.Author.Login+" · "+r.date().Local().Format("2 Jan 2006")+" ("+ago(r.date())+" ago)"))
	out = append(out, wrapJoin(tags, "  ", w)...)
	out = append(out, "")
	if len(r.Assets) > 0 {
		out = append(out, fg(cHint).Render(fmt.Sprintf("Files (%d)", len(r.Assets))))
		for i, a := range r.Assets {
			if i == 8 {
				out = append(out, fg(cMut).Render(fmt.Sprintf("  … and %d more (d to pick)", len(r.Assets)-8)))
				break
			}
			out = append(out, trunc("  "+fg(cAcc).Render(icDown)+" "+fg(cTxt).Render(a.Name)+fg(cMut).Render(fmt.Sprintf("  %s · %d downloads", humanSize(a.Size), a.Downloads)), w))
		}
		out = append(out, "")
	}
	out = append(out, hints([][2]string{{"d", "download"}, {"v", "read the notes"}, {"N", "new release"}, {"o", "open on GitHub"}}), sepLine(w), "")
	if strings.TrimSpace(r.Body) == "" {
		return append(out, fg(cMut).Render("No release notes."))
	}
	return append(out, m.cachedLines(fmt.Sprintf("rel:%d:%d:%d", r.ID, len(r.Body), w), func() []string { return renderMarkdown(r.Body, w) })...)
}

// ---- the Actions tab panels ----

func (m *model) actListBox(w, h, y0 int) []string {
	m.addZone(0, y0, w-1, y0+h-1, "list", 0)
	// filter chips in the top border: All  Failed  Running
	title := ""
	x := 3
	for i, n := range actFilters {
		if i > 0 {
			title += fg(cDim).Render("  ")
			x += 2
		}
		if i == m.act.filter {
			title += fg(cAcc).Bold(true).Render(n)
		} else {
			title += fg(cMut).Render(n)
		}
		m.addZone(x, y0, x+width(n)-1, y0, "actfilter", i)
		x += width(n)
	}
	inner := w - 4
	var body []string
	rowY := y0 + 1
	if m.act.filtering || m.act.query != "" {
		cur := ""
		if m.act.filtering {
			cur = fg(cAcc).Render("▏")
		}
		line := fg(cAcc).Render(icSearch+" ") + fg(cTxt).Render(m.act.query) + cur
		if m.act.query == "" {
			line += fg(cMut).Render(" find a run")
		}
		body = append(body, line)
		rowY++
	}
	items := m.actItems()
	done, total := m.actLoading()
	rows := h - 2 - len(body)
	switch {
	case len(items) == 0 && done < total:
		body = append(body, "", fg(cMut).Render(fmt.Sprintf("Checking your repos for CI… %d/%d", done, total)))
	case len(items) == 0 && m.act.filter == 1:
		body = append(body, "", fg(cGreen).Render(icCheck+" Nothing failed"))
	case len(items) == 0 && m.act.filter == 2:
		body = append(body, "", fg(cMut).Render("Nothing running right now."))
	case len(items) == 0:
		body = append(body, "", fg(cMut).Render("No CI runs in your recent repos."),
			fg(cMut).Render("Runs show up here when a repo has a"), fg(cMut).Render(".github/workflows file."))
	}
	m.act.cur = max(0, min(m.act.cur, len(items)-1))
	for i := m.act.off; i < len(items) && i < m.act.off+rows; i++ {
		body = append(body, m.runRow(items[i], inner, i == m.act.cur, true))
		y := rowY + (i - m.act.off)
		m.addZone(1, y, w-2, y, "actrow", i)
	}
	note := ""
	if len(items) > 0 {
		note = fmt.Sprintf("%d/%d", m.act.cur+1, len(items))
	}
	if done < total && len(items) > 0 {
		note = fmt.Sprintf("checking %d/%d · ", done, total) + note
	}
	return box(title, w, h, body, m.focus == 0, note)
}

func (m *model) actPreview(w int) (string, []string) {
	r := m.selRun()
	if r == nil {
		return "Run", []string{fg(cMut).Render("Pick a run on the left.")}
	}
	return fmt.Sprintf("Run #%d", r.Number), m.runPreview(r, w)
}

// ---- live updates while something runs ----

type liveTick struct{}

func live() tea.Cmd {
	return tea.Tick(5*time.Second, func(time.Time) tea.Msg { return liveTick{} })
}

func (m *model) onLive() tea.Cmd {
	cmds := []tea.Cmd{live()}
	var repos []string
	switch {
	case m.tab == tabActions:
		seen := map[string]bool{}
		for _, r := range m.actItems() {
			if r.running() && !seen[r.Repo] {
				seen[r.Repo] = true
				repos = append(repos, r.Repo)
			}
		}
	case m.exActive() != nil && m.ex.sec == secActions:
		if rl := m.runs[m.ex.repo.NameWithOwner]; rl != nil {
			for _, r := range rl.items {
				if r.running() {
					repos = append(repos, r.Repo)
					break
				}
			}
		}
	}
	for _, nwo := range repos {
		delete(m.loaded, "runs:"+nwo)
		cmds = append(cmds, m.load("runs:"+nwo, false, fetchRuns(nwo)))
	}
	if r := m.selRun(); r != nil && r.running() {
		key := fmt.Sprintf("jobs:%s#%d", r.Repo, r.ID)
		delete(m.loaded, key)
		cmds = append(cmds, m.loadJobs(r, false))
	}
	return tea.Batch(cmds...)
}

func (m *model) onCIDone(msg ciDoneMsg) tea.Cmd {
	if msg.err != nil {
		return m.setStatus(msg.err.Error(), true)
	}
	cmds := []tea.Cmd{m.setStatus(msg.msg, false)}
	switch msg.what {
	case "runs":
		// GitHub needs a moment to create the new run
		nwo := msg.nwo
		cmds = append(cmds, tea.Tick(2*time.Second, func(time.Time) tea.Msg { return reloadMsg{"runs:" + nwo} }))
	case "releases":
		delete(m.loaded, "releases:"+msg.nwo)
		cmds = append(cmds, m.load("releases:"+msg.nwo, false, fetchReleases(msg.nwo)))
	}
	return tea.Batch(cmds...)
}

type reloadMsg struct{ key string }

func (m *model) onReload(key string) tea.Cmd {
	delete(m.loaded, key)
	nwo := strings.TrimPrefix(key, "runs:")
	return m.load(key, false, fetchRuns(nwo))
}

// buttons for the Actions tab and a repo's Actions / Releases sections
func (m *model) ciButtons() []button {
	inActions := m.tab == tabActions || m.exActive() != nil && m.ex.sec == secActions
	inReleases := m.exActive() != nil && m.ex.sec == secReleases
	var b []button
	switch {
	case inActions:
		if r := m.selRun(); r != nil {
			b = append(b, button{icRead + " Logs", "enter", "logs"})
			if r.running() {
				b = append(b, button{icClose + " Cancel", "x", "cancel"})
			} else {
				b = append(b, button{icRefresh + " Re-run", "R", "rerun"})
			}
		}
		b = append(b, button{icActions + " Start workflow", "w", "workflow"})
	case inReleases:
		if m.selRelease() != nil {
			b = append(b, button{icRead + " Notes", "enter", "notes"}, button{icDown + " Download", "d", "download"})
		}
		b = append(b, button{icTag + " New release", "N", "newrel"})
	default:
		return nil
	}
	return append(b, button{icGlobe + " Browser", "o", "browser"}, button{icLink + " Copy link", "y", "copy"}, button{icRefresh + " Refresh", "r", "refresh"})
}
