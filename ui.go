package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	"github.com/cli/go-gh/v2/pkg/browser"
)

type tabInfo struct {
	name, icon string
	batch      int // the batch that brings this screen (0 = ready)
}

var tabs = []tabInfo{
	{"Home", icHome, 6}, {"Repos", icRepo, 0}, {"Issues", icIssue, 4}, {"PRs", icPR, 5},
	{"Actions", icActions, 3}, {"Inbox", icInbox, 6}, {"Explore", icExplore, 7},
}

const tabRepos = 1

// a clickable area of the screen, recorded while drawing
type zone struct {
	x0, y0, x1, y1 int
	id             string
	idx            int
}

// question or text box in the bottom bar (like Nvim's command line)
type prompt struct {
	label   string
	confirm bool // y / n question; otherwise a line of text
	text    string
	done    func(m *model, value string) tea.Cmd
}

type command struct {
	name, key string
	run       func(m *model) tea.Cmd
}

type readmeEntry struct {
	md   string
	none bool
}

type button struct{ label, key, id string }

// ---- messages ----
type readmeTick struct {
	seq int
	nwo string
}
type statusClear struct{ seq int }
type clonedMsg struct {
	nwo, dest string
	err       error
}

type model struct {
	w, h  int
	dump  bool
	tab   int
	focus int // 0 list, 1 preview

	login    string
	repos    []Repo
	reposAt  time.Time
	loading  bool
	err      string
	local    map[string]string
	view     []int // indexes of repos shown (after the filter)
	cur, off int

	filter    string
	filtering bool

	readme    map[string]readmeEntry
	readmeErr map[string]string
	fresh     map[string]bool
	fetching  map[string]bool
	fetchSeq  int
	mdCache   map[string][]string
	mdWidth   map[string]int
	pvOff     int
	pvMax     int

	help    bool
	prompt  *prompt
	palette bool
	palQ    string
	palCur  int

	status    string
	statusErr bool
	statusSeq int

	zones     []zone
	lastClick time.Time
	lastIdx   int
}

func newModel() *model {
	m := &model{
		tab: tabRepos, readme: map[string]readmeEntry{}, readmeErr: map[string]string{},
		fresh: map[string]bool{}, fetching: map[string]bool{}, mdCache: map[string][]string{}, mdWidth: map[string]int{},
		local: scanLocal(localRoots), lastIdx: -1,
	}
	var c reposCache
	if loadCache("repos.json", &c) {
		m.login, m.repos, m.reposAt = c.Login, c.Repos, c.At
	}
	m.refilter()
	return m
}

func (m *model) Init() tea.Cmd {
	m.loading = true
	return tea.Batch(fetchRepos(), m.onSelect())
}

// ---------------------------------------------------------------------------
// data helpers
// ---------------------------------------------------------------------------

func (m *model) selected() *Repo {
	if m.tab != tabRepos || m.cur < 0 || m.cur >= len(m.view) {
		return nil
	}
	return &m.repos[m.view[m.cur]]
}

func (m *model) localPath(r *Repo) string {
	return m.local[strings.ToLower(r.NameWithOwner)]
}

func (m *model) refilter() {
	m.view = m.view[:0]
	terms := strings.Fields(strings.ToLower(m.filter))
	for i, r := range m.repos {
		hay := strings.ToLower(r.NameWithOwner + " " + r.Description + " " + r.Lang())
		ok := true
		for _, t := range terms {
			if !strings.Contains(hay, t) {
				ok = false
				break
			}
		}
		if ok {
			m.view = append(m.view, i)
		}
	}
	if m.cur >= len(m.view) {
		m.cur = len(m.view) - 1
	}
	if m.cur < 0 {
		m.cur = 0
	}
}

// called whenever the selected repo changes: show its README (saved copy first, then fetch)
func (m *model) onSelect() tea.Cmd {
	m.pvOff = 0
	m.ensureVisible()
	r := m.selected()
	if r == nil {
		return nil
	}
	nwo := r.NameWithOwner
	if m.fresh[nwo] || m.fetching[nwo] {
		return nil
	}
	if _, ok := m.readme[nwo]; !ok {
		if md, none, age, ok := loadReadme(nwo); ok {
			m.readme[nwo] = readmeEntry{md, none}
			if age < time.Hour {
				m.fresh[nwo] = true
				return nil
			}
		}
	}
	m.fetchSeq++
	if m.dump {
		m.fetching[nwo] = true
		return fetchReadme(nwo)
	}
	seq := m.fetchSeq
	// wait a moment so scrolling fast through the list doesn't fetch every repo
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return readmeTick{seq, nwo} })
}

func (m *model) setStatus(s string, isErr bool) tea.Cmd {
	m.status, m.statusErr = s, isErr
	m.statusSeq++
	if m.dump {
		return nil
	}
	seq := m.statusSeq
	return tea.Tick(4*time.Second, func(time.Time) tea.Msg { return statusClear{seq} })
}

// ---------------------------------------------------------------------------
// layout
// ---------------------------------------------------------------------------

func (m *model) palMatches() []command {
	q := strings.ToLower(m.palQ)
	var out []command
	for _, c := range m.commands() {
		if fuzzy(strings.ToLower(c.name), q) {
			out = append(out, c)
		}
	}
	return out
}

func fuzzy(s, q string) bool {
	i := 0
	for _, r := range s {
		if i < len(q) && byte(r) == q[i] {
			i++
		}
	}
	return i == len(q)
}

func (m *model) palRows() int {
	if !m.palette {
		return 0
	}
	n := len(m.palMatches())
	if n > 8 {
		n = 8
	}
	if n < 1 {
		n = 1
	}
	return n + 2
}

// panel sizes: list width, panel height
func (m *model) layout() (lw, ph int) {
	lw = m.w * 36 / 100
	if lw < 32 {
		lw = 32
	}
	if lw > 64 {
		lw = 64
	}
	ph = m.h - 4 - m.palRows()
	return
}

func (m *model) listRows() int {
	_, ph := m.layout()
	return ph - 2
}

func (m *model) ensureVisible() {
	n := m.listRows()
	if n < 1 {
		return
	}
	if m.cur < m.off {
		m.off = m.cur
	}
	if m.cur >= m.off+n {
		m.off = m.cur - n + 1
	}
	if m.off < 0 {
		m.off = 0
	}
}

// ---------------------------------------------------------------------------
// update
// ---------------------------------------------------------------------------

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.ensureVisible()
	case reposMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err.Error()
			return m, m.setStatus("Couldn't reach GitHub, showing saved data", true)
		}
		m.err = ""
		var keep string
		if r := m.selected(); r != nil {
			keep = r.NameWithOwner
		}
		m.login, m.repos, m.reposAt = msg.login, msg.repos, time.Now()
		m.refilter()
		for i, idx := range m.view {
			if m.repos[idx].NameWithOwner == keep {
				m.cur = i
			}
		}
		if r := m.selected(); r != nil && r.NameWithOwner != keep {
			return m, m.onSelect()
		}
		return m, m.onSelect()
	case readmeTick:
		if msg.seq == m.fetchSeq && !m.fetching[msg.nwo] {
			m.fetching[msg.nwo] = true
			return m, fetchReadme(msg.nwo)
		}
	case readmeMsg:
		m.fetching[msg.nwo] = false
		if msg.err != nil {
			m.readmeErr[msg.nwo] = msg.err.Error()
			return m, nil
		}
		m.fresh[msg.nwo] = true
		m.readme[msg.nwo] = readmeEntry{msg.md, msg.none}
		delete(m.mdCache, msg.nwo)
	case statusClear:
		if msg.seq == m.statusSeq {
			m.status = ""
		}
	case clonedMsg:
		m.local = scanLocal(localRoots)
		if msg.err != nil {
			return m, m.setStatus("Clone failed: "+msg.err.Error(), true)
		}
		return m, m.setStatus("Cloned into "+msg.dest, false)
	case tea.KeyPressMsg:
		return m, m.key(msg)
	case tea.MouseClickMsg:
		return m, m.click(msg.Mouse())
	case tea.MouseWheelMsg:
		return m, m.wheel(msg.Mouse())
	}
	return m, nil
}

func (m *model) move(d int) tea.Cmd {
	if len(m.view) == 0 {
		return nil
	}
	old := m.cur
	m.cur += d
	if m.cur < 0 {
		m.cur = 0
	}
	if m.cur >= len(m.view) {
		m.cur = len(m.view) - 1
	}
	if m.cur == old {
		return nil
	}
	return m.onSelect()
}

func (m *model) scroll(d int) {
	m.pvOff += d
	if m.pvOff > m.pvMax {
		m.pvOff = m.pvMax
	}
	if m.pvOff < 0 {
		m.pvOff = 0
	}
}

func (m *model) setTab(i int) tea.Cmd {
	if i < 0 || i >= len(tabs) {
		return nil
	}
	m.tab, m.focus, m.help, m.pvOff = i, 0, false, 0
	if i == tabRepos {
		return m.onSelect()
	}
	return nil
}

// text typed into the filter / prompt / palette; returns false when the key isn't text editing
func editText(s *string, k tea.KeyPressMsg) bool {
	switch k.String() {
	case "backspace":
		if r := []rune(*s); len(r) > 0 {
			*s = string(r[:len(r)-1])
		}
		return true
	case "ctrl+u":
		*s = ""
		return true
	}
	if k.Text != "" && k.Mod&(tea.ModCtrl|tea.ModAlt) == 0 {
		*s += k.Text
		return true
	}
	return false
}

func (m *model) key(k tea.KeyPressMsg) tea.Cmd {
	s := k.String()
	if s == "ctrl+c" {
		return tea.Quit
	}

	// bottom-bar question / text
	if p := m.prompt; p != nil {
		if p.confirm {
			switch strings.ToLower(s) {
			case "y", "enter":
				m.prompt = nil
				return p.done(m, "y")
			case "n", "esc", "q":
				m.prompt = nil
			}
			return nil
		}
		switch s {
		case "esc":
			m.prompt = nil
		case "enter":
			m.prompt = nil
			return p.done(m, strings.TrimSpace(p.text))
		default:
			editText(&p.text, k)
		}
		return nil
	}

	// command palette
	if m.palette {
		list := m.palMatches()
		switch s {
		case "esc", "ctrl+p":
			m.palette = false
		case "enter":
			m.palette = false
			if m.palCur < len(list) {
				return list[m.palCur].run(m)
			}
		case "down", "ctrl+n", "ctrl+j", "tab":
			if m.palCur < len(list)-1 {
				m.palCur++
			}
		case "up", "ctrl+k", "shift+tab":
			if m.palCur > 0 {
				m.palCur--
			}
		default:
			if editText(&m.palQ, k) {
				m.palCur = 0
			}
		}
		return nil
	}

	// typing a filter
	if m.filtering {
		switch s {
		case "esc":
			m.filtering, m.filter = false, ""
			m.refilter()
			return m.onSelect()
		case "enter":
			m.filtering = false
			return nil
		case "down", "ctrl+n", "ctrl+j":
			return m.move(1)
		case "up", "ctrl+k":
			return m.move(-1)
		}
		if editText(&m.filter, k) {
			m.cur, m.off = 0, 0
			m.refilter()
			return m.onSelect()
		}
		return nil
	}

	half := m.listRows() / 2
	switch s {
	case "q":
		return tea.Quit
	case "ctrl+p", ":":
		m.palette, m.palQ, m.palCur = true, "", 0
		return nil
	case "?":
		m.help = !m.help
		m.pvOff = 0
		return nil
	case "1", "2", "3", "4", "5", "6", "7":
		i, _ := strconv.Atoi(s)
		return m.setTab(i - 1)
	case "esc":
		switch {
		case m.help:
			m.help = false
		case m.focus == 1:
			m.focus = 0
		case m.filter != "":
			m.filter = ""
			m.refilter()
			return m.onSelect()
		}
		return nil
	case "ctrl+d":
		m.scroll(half)
		return nil
	case "ctrl+u":
		m.scroll(-half)
		return nil
	}
	if m.tab != tabRepos {
		return nil
	}
	switch s {
	case "tab", "shift+tab":
		m.focus = 1 - m.focus
	case "left", "h":
		m.focus = 0
	case "right", "l":
		m.focus = 1
	case "enter":
		if m.focus == 0 && m.selected() != nil {
			m.focus = 1
		}
	case "/":
		m.filtering, m.focus = true, 0
	case "r":
		return m.refresh()
	case "o":
		return m.actBrowser()
	case "y":
		return m.actCopy()
	case "c":
		return m.actClone()
	case "j", "down":
		if m.focus == 0 {
			return m.move(1)
		}
		m.scroll(1)
	case "k", "up":
		if m.focus == 0 {
			return m.move(-1)
		}
		m.scroll(-1)
	case "pgdown", "space":
		if m.focus == 0 {
			return m.move(half * 2)
		}
		m.scroll(half * 2)
	case "pgup":
		if m.focus == 0 {
			return m.move(-half * 2)
		}
		m.scroll(-half * 2)
	case "g", "home":
		if m.focus == 0 {
			return m.move(-len(m.view))
		}
		m.pvOff = 0
	case "G", "end":
		if m.focus == 0 {
			return m.move(len(m.view))
		}
		m.pvOff = m.pvMax
	}
	return nil
}

// ---- mouse ----

func (m *model) zoneAt(x, y int) *zone {
	for i := len(m.zones) - 1; i >= 0; i-- {
		z := &m.zones[i]
		if x >= z.x0 && x <= z.x1 && y >= z.y0 && y <= z.y1 {
			return z
		}
	}
	return nil
}

func (m *model) click(ms tea.Mouse) tea.Cmd {
	if ms.Button != tea.MouseLeft {
		return nil
	}
	z := m.zoneAt(ms.X, ms.Y)
	if z == nil {
		return nil
	}
	switch z.id {
	case "tab":
		return m.setTab(z.idx)
	case "crumb":
		m.focus, m.help = 0, false
	case "row":
		m.focus = 0
		double := z.idx == m.lastIdx && time.Since(m.lastClick) < 400*time.Millisecond
		m.lastIdx, m.lastClick = z.idx, time.Now()
		var cmd tea.Cmd
		if z.idx != m.cur {
			m.cur = z.idx
			cmd = m.onSelect()
		}
		if double {
			m.focus = 1
		}
		return cmd
	case "list":
		m.focus = 0
	case "preview":
		if m.tab == tabRepos {
			m.focus = 1
		}
	case "btn":
		return m.runButton(z.idx)
	case "pal":
		list := m.palMatches()
		m.palette = false
		if z.idx < len(list) {
			return list[z.idx].run(m)
		}
	case "hint-help":
		m.help = !m.help
	case "hint-pal":
		m.palette, m.palQ, m.palCur = true, "", 0
	}
	return nil
}

func (m *model) wheel(ms tea.Mouse) tea.Cmd {
	d := 0
	switch ms.Button {
	case tea.MouseWheelDown:
		d = 1
	case tea.MouseWheelUp:
		d = -1
	default:
		return nil
	}
	z := m.zoneAt(ms.X, ms.Y)
	if z == nil {
		return nil
	}
	switch z.id {
	case "row", "list":
		return m.move(d)
	case "preview":
		m.scroll(d * 3)
	case "pal":
		m.palCur += d
		if n := len(m.palMatches()); m.palCur >= n {
			m.palCur = n - 1
		}
		if m.palCur < 0 {
			m.palCur = 0
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// actions
// ---------------------------------------------------------------------------

func (m *model) refresh() tea.Cmd {
	m.loading = true
	cmds := []tea.Cmd{fetchRepos()}
	if r := m.selected(); r != nil {
		m.fresh[r.NameWithOwner] = false
		m.fetching[r.NameWithOwner] = true
		cmds = append(cmds, fetchReadme(r.NameWithOwner))
	}
	m.local = scanLocal(localRoots)
	return tea.Batch(cmds...)
}

func (m *model) actBrowser() tea.Cmd {
	r := m.selected()
	if r == nil {
		return nil
	}
	if err := browser.New("", io.Discard, io.Discard).Browse(r.URL); err != nil {
		return m.setStatus("Couldn't open the browser: "+err.Error(), true)
	}
	return m.setStatus("Opened "+r.Name+" in the browser", false)
}

func (m *model) actCopy() tea.Cmd {
	r := m.selected()
	if r == nil {
		return nil
	}
	if err := clipboard.WriteAll(r.URL); err != nil {
		return m.setStatus("Couldn't copy: "+err.Error(), true)
	}
	return m.setStatus("Copied "+r.URL, false)
}

func (m *model) actClone() tea.Cmd {
	r := m.selected()
	if r == nil {
		return nil
	}
	if p := m.localPath(r); p != "" {
		return m.setStatus("Already on this PC: "+p, false)
	}
	dest := filepath.Join(cloneRoot, r.Name)
	if _, err := os.Stat(dest); err == nil {
		return m.setStatus("A folder named "+r.Name+" already exists in "+cloneRoot, true)
	}
	nwo := r.NameWithOwner
	m.prompt = &prompt{
		label:   "Clone " + nwo + " into " + dest + "?",
		confirm: true,
		done: func(m *model, _ string) tea.Cmd {
			c := exec.Command("gh", "repo", "clone", nwo, dest)
			return tea.ExecProcess(c, func(err error) tea.Msg { return clonedMsg{nwo, dest, err} })
		},
	}
	return nil
}

func (m *model) buttons() []button {
	r := m.selected()
	if r == nil {
		return nil
	}
	b := []button{{icGlobe + " Browser", "o", "browser"}, {icLink + " Copy link", "y", "copy"}}
	if m.localPath(r) == "" {
		b = append(b, button{icDown + " Clone", "c", "clone"})
	}
	b = append(b, button{icRefresh + " Refresh", "r", "refresh"})
	return b
}

func (m *model) runButton(i int) tea.Cmd {
	b := m.buttons()
	if i >= len(b) {
		return nil
	}
	switch b[i].id {
	case "browser":
		return m.actBrowser()
	case "copy":
		return m.actCopy()
	case "clone":
		return m.actClone()
	case "refresh":
		return m.refresh()
	}
	return nil
}

func (m *model) commands() []command {
	var cs []command
	for i, t := range tabs {
		i := i
		name := "Go to " + t.name
		if t.batch > 0 {
			name += fmt.Sprintf(" (batch %d)", t.batch)
		}
		cs = append(cs, command{name, strconv.Itoa(i + 1), func(m *model) tea.Cmd { return m.setTab(i) }})
	}
	cs = append(cs,
		command{"Filter repos", "/", func(m *model) tea.Cmd { m.setTab(tabRepos); m.filtering = true; return nil }},
		command{"Open repo in browser", "o", func(m *model) tea.Cmd { return m.actBrowser() }},
		command{"Copy repo link", "y", func(m *model) tea.Cmd { return m.actCopy() }},
		command{"Clone repo", "c", func(m *model) tea.Cmd { return m.actClone() }},
		command{"Refresh", "r", func(m *model) tea.Cmd { return m.refresh() }},
		command{"Show all keys", "?", func(m *model) tea.Cmd { m.help = true; m.pvOff = 0; return nil }},
		command{"Quit", "q", func(m *model) tea.Cmd { return tea.Quit }},
	)
	return cs
}

// ---------------------------------------------------------------------------
// view
// ---------------------------------------------------------------------------

func (m *model) View() tea.View {
	m.zones = m.zones[:0]
	var content string
	if m.w < 70 || m.h < 16 {
		content = fg(cMut).Render("TermGit needs a bigger window (at least 70 x 16).")
	} else {
		content = m.render()
	}
	v := tea.NewView(content)
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.WindowTitle = "TermGit"
	return v
}

func (m *model) addZone(x0, y0, x1, y1 int, id string, idx int) {
	m.zones = append(m.zones, zone{x0, y0, x1, y1, id, idx})
}

func (m *model) render() string {
	lw, ph := m.layout()
	pw := m.w - lw
	lines := []string{m.tabsBar(), m.pathBar()}
	left := m.listBox(lw, ph, 2)
	right := m.previewBox(pw, ph, lw, 2)
	for i := 0; i < ph; i++ {
		lines = append(lines, left[i]+right[i])
	}
	if m.palette {
		lines = append(lines, m.paletteBox(2+ph)...)
	}
	lines = append(lines, m.buttonsBar(m.h-2), m.hintBar(m.h-1))
	return strings.Join(lines, "\n")
}

func (m *model) tabsBar() string {
	out := " "
	x := 1
	for i, t := range tabs {
		label := " " + t.icon + " " + t.name + " "
		var s string
		switch {
		case i == m.tab:
			s = lipgloss.NewStyle().Foreground(cAcc).Background(cDim).Bold(true).Render(label)
		case t.batch > 0:
			s = fg(cMut).Render(label)
		default:
			s = fg(cHint).Render(label)
		}
		w := width(label)
		m.addZone(x, 0, x+w-1, 0, "tab", i)
		out += s + " "
		x += w + 1
	}
	right := ""
	if m.login != "" {
		right = fg(cMut).Render(icGithub+" "+m.login) + " "
	}
	gap := m.w - width(out) - width(right)
	if gap < 1 {
		return pad(out, m.w)
	}
	return out + strings.Repeat(" ", gap) + right
}

func (m *model) pathBar() string {
	sep := fg(cMut).Render(" › ")
	root := tabs[m.tab].name
	out := " " + fg(cHint).Render(root)
	m.addZone(1, 1, width(root), 1, "crumb", 0)
	if r := m.selected(); r != nil {
		out += sep + fg(cTxt).Render(r.NameWithOwner)
		if m.help {
			out += sep + fg(cTxt).Render("Keys")
		} else if m.focus == 1 {
			out += sep + fg(cTxt).Render("README")
		}
	}
	var right string
	switch {
	case m.status != "" && m.statusErr:
		right = fg(cRed).Render(icWarn + " " + m.status)
	case m.status != "":
		right = fg(cGreen).Render(icCheck + " " + m.status)
	case m.loading:
		right = fg(cMut).Render(icRefresh + " updating…")
	case !m.reposAt.IsZero():
		if a := ago(m.reposAt); a == "now" {
			right = fg(cMut).Render("updated just now")
		} else {
			right = fg(cMut).Render("updated " + a + " ago")
		}
	}
	gap := m.w - width(out) - width(right) - 1
	if gap < 1 {
		return pad(out+"  "+right, m.w)
	}
	return out + strings.Repeat(" ", gap) + right + " "
}

func (m *model) listBox(w, h, y0 int) []string {
	t := tabs[m.tab]
	m.addZone(0, y0, w-1, y0+h-1, "list", 0)
	if m.tab != tabRepos {
		body := []string{"", fg(cMut).Render(fmt.Sprintf("Coming in batch %d", t.batch))}
		return box(t.icon+" "+t.name, w, h, body, false, "")
	}
	title := t.icon + " Repos"
	if m.filtering || m.filter != "" {
		cursor := ""
		if m.filtering {
			cursor = "▏"
		}
		title += "  " + icSearch + " " + m.filter + cursor
	}
	inner := w - 4
	rows := h - 2
	var body []string
	switch {
	case len(m.repos) == 0 && m.loading:
		body = []string{"", fg(cMut).Render("Loading your repos…")}
	case len(m.repos) == 0 && m.err != "":
		body = []string{"", fg(cRed).Render("Couldn't load repos:"), fg(cMut).Render(m.err)}
	case len(m.view) == 0:
		body = []string{"", fg(cMut).Render("Nothing matches \"" + m.filter + "\"")}
	}
	if body == nil {
		for i := m.off; i < len(m.view) && i < m.off+rows; i++ {
			body = append(body, m.repoRow(&m.repos[m.view[i]], inner, i == m.cur))
			y := y0 + 1 + (i - m.off)
			m.addZone(1, y, w-2, y, "row", i)
		}
	}
	note := ""
	if len(m.view) > 0 {
		note = fmt.Sprintf("%d/%d", m.cur+1, len(m.view))
	}
	return box(title, w, h, body, m.focus == 0, note)
}

func (m *model) repoRow(r *Repo, w int, sel bool) string {
	st := lipgloss.NewStyle()
	if sel {
		bg := cChip
		if m.focus == 0 {
			bg = cDim
		}
		st = st.Background(bg)
	}
	icon, ic := icRepo, cMut
	switch {
	case r.IsArchived:
		icon = icArchive
	case r.IsFork:
		icon = icFork
	case r.IsPrivate:
		icon, ic = icLock, cOrange
	}
	name := r.Name
	if m.login != "" && !strings.EqualFold(strings.SplitN(r.NameWithOwner, "/", 2)[0], m.login) {
		name = r.NameWithOwner
	}
	nc := cTxt
	if sel {
		nc = cAcc
	}
	right := ""
	if m.localPath(r) != "" {
		right += st.Foreground(cGreen).Render(icFolder) + st.Render(" ")
	}
	right += st.Foreground(cMut).Render(fmt.Sprintf("%4s", ago(r.PushedAt))) // "3d", "11mo"
	left := st.Foreground(ic).Render(icon) + st.Render(" ") + st.Foreground(nc).Bold(sel).Render(name)
	space := w - width(left) - width(right) - 1
	if space < 1 {
		left = trunc(left, w-width(right)-2)
		space = w - width(left) - width(right) - 1
	}
	if space < 0 {
		space = 0
	}
	return left + st.Render(strings.Repeat(" ", space+1)) + right
}

func (m *model) previewBox(w, h, x0, y0 int) []string {
	m.addZone(x0, y0, x0+w-1, y0+h-1, "preview", 0)
	inner := w - 4
	var lines []string
	title := "Preview"
	switch {
	case m.help:
		title, lines = "Keys", helpLines(inner)
	case m.tab != tabRepos:
		t := tabs[m.tab]
		title = t.name
		lines = []string{
			"", fg(cHint).Render(t.icon + "  " + t.name + " isn't built yet."),
			"", fg(cMut).Render(fmt.Sprintf("It comes in batch %d of the plan.", t.batch)),
			fg(cMut).Render("Press 2 (or click Repos) to go back to your repos."),
		}
	default:
		if r := m.selected(); r != nil {
			title = "README"
			lines = m.repoPreview(r, inner)
		}
	}
	rows := h - 2
	m.pvMax = len(lines) - rows
	if m.pvMax < 0 {
		m.pvMax = 0
	}
	if m.pvOff > m.pvMax {
		m.pvOff = m.pvMax
	}
	note := ""
	if m.pvMax > 0 {
		note = fmt.Sprintf("%d%%", (m.pvOff*100)/m.pvMax)
	}
	return box(title, w, h, lines[min(m.pvOff, len(lines)):], m.focus == 1, note)
}

func (m *model) repoPreview(r *Repo, w int) []string {
	var out []string
	out = append(out, fg(cAcc).Bold(true).Render(r.NameWithOwner))
	if r.Description != "" {
		for _, l := range strings.Split(lipgloss.NewStyle().Width(w).Render(r.Description), "\n") {
			out = append(out, fg(cTxt).Render(strings.TrimRight(l, " ")))
		}
	} else {
		out = append(out, fg(cMut).Render("No description"))
	}
	out = append(out, "")
	var meta []string
	add := func(icon, s string, c ...lipgloss.Style) {
		st := fg(cMut)
		if len(c) > 0 {
			st = c[0]
		}
		meta = append(meta, st.Render(icon+" "+s))
	}
	if r.IsPrivate {
		add(icLock, "private", fg(cOrange))
	} else {
		add(icGlobe, "public")
	}
	if l := r.Lang(); l != "" {
		dot := fg(cMut)
		if r.PrimaryLanguage.Color != "" {
			dot = fg(lipgloss.Color(r.PrimaryLanguage.Color))
		}
		meta = append(meta, dot.Render("●")+" "+fg(cMut).Render(l))
	}
	if b := r.Branch(); b != "" {
		add(icBranch, b)
	}
	add(icStar, strconv.Itoa(r.Stars))
	add(icFork, strconv.Itoa(r.Forks))
	if !r.PushedAt.IsZero() {
		add(icClock, "pushed "+ago(r.PushedAt)+" ago")
	}
	if r.IsArchived {
		add(icArchive, "archived", fg(cYellow))
	}
	out = append(out, wrapJoin(meta, "   ", w)...)
	if p := m.localPath(r); p != "" {
		out = append(out, fg(cGreen).Render(icFolder+" "+p))
	} else {
		out = append(out, fg(cMut).Render(icFolder+" not on this PC  (c to clone)"))
	}
	out = append(out, fg(cDim).Render(strings.Repeat("─", w)), "")

	e, ok := m.readme[r.NameWithOwner]
	switch {
	case ok && (e.none || strings.TrimSpace(e.md) == ""):
		out = append(out, fg(cMut).Render("This repo has no README."))
	case ok:
		key := r.NameWithOwner
		cached, hit := m.mdCache[key]
		if !hit || len(cached) == 0 || m.mdWidth[key] != w {
			cached = renderMarkdown(e.md, w)
			m.mdCache[key] = cached
			m.mdWidth[key] = w
		}
		out = append(out, cached...)
	case m.readmeErr[r.NameWithOwner] != "":
		out = append(out, fg(cRed).Render("Couldn't load the README:"), fg(cMut).Render(m.readmeErr[r.NameWithOwner]))
	default:
		out = append(out, fg(cMut).Render("Loading README…"))
	}
	return out
}

// joins items with sep, wrapping onto new lines so nothing is cut off
func wrapJoin(items []string, sep string, w int) []string {
	var out []string
	cur := ""
	for _, it := range items {
		switch {
		case cur == "":
			cur = it
		case width(cur)+width(sep)+width(it) <= w:
			cur += sep + it
		default:
			out = append(out, cur)
			cur = it
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func (m *model) paletteBox(y0 int) []string {
	list := m.palMatches()
	n := m.palRows() - 2
	start := 0
	if m.palCur >= n {
		start = m.palCur - n + 1
	}
	inner := m.w - 4
	var body []string
	if len(list) == 0 {
		body = []string{fg(cMut).Render("No command matches")}
	}
	for i := start; i < len(list) && i < start+n; i++ {
		c := list[i]
		st := lipgloss.NewStyle()
		nc := cTxt
		if i == m.palCur {
			st, nc = st.Background(cDim), cAcc
		}
		k := st.Foreground(cMut).Render(c.key + " ")
		name := st.Foreground(nc).Render(" " + c.name)
		gap := inner - width(name) - width(k)
		body = append(body, name+st.Render(strings.Repeat(" ", max(gap, 1)))+k)
		y := y0 + 1 + (i - start)
		m.addZone(1, y, m.w-2, y, "pal", i)
	}
	return box("Commands", m.w, n+2, body, true, "")
}

func chip(key, label string) string {
	st := lipgloss.NewStyle().Background(cChip)
	return st.Render(" ") + st.Foreground(cTxt).Render(label) + st.Render(" ") + st.Foreground(cAcc).Render(key) + st.Render(" ")
}

func (m *model) buttonsBar(y int) string {
	if m.prompt != nil || m.palette {
		return ""
	}
	out := " "
	x := 1
	for i, b := range m.buttons() {
		c := chip(b.key, b.label)
		w := width(c)
		m.addZone(x, y, x+w-1, y, "btn", i)
		out += c + " "
		x += w + 1
	}
	return out
}

func hints(list [][2]string) string {
	var parts []string
	for _, h := range list {
		parts = append(parts, fg(cAcc).Render(h[0])+" "+fg(cMut).Render(h[1]))
	}
	return strings.Join(parts, fg(cDim).Render("  ·  "))
}

func (m *model) hintBar(y int) string {
	if p := m.prompt; p != nil {
		if p.confirm {
			return " " + fg(cYellow).Render(p.label) + "  " + fg(cAcc).Render("y") + fg(cMut).Render(" yes  ") + fg(cAcc).Render("n") + fg(cMut).Render(" no")
		}
		return " " + fg(cYellow).Render(p.label) + " " + fg(cTxt).Render(p.text) + fg(cAcc).Render("▏")
	}
	if m.palette {
		return " " + fg(cAcc).Render(":") + " " + fg(cTxt).Render(m.palQ) + fg(cAcc).Render("▏") + "   " +
			hints([][2]string{{"↑↓", "choose"}, {"enter", "run"}, {"esc", "close"}})
	}
	if m.filtering {
		return " " + hints([][2]string{{"type", "to filter"}, {"↑↓", "move"}, {"enter", "keep filter"}, {"esc", "clear"}})
	}
	var h [][2]string
	switch {
	case m.tab != tabRepos:
		h = [][2]string{{"1-7", "tabs"}, {"ctrl+p", "commands"}, {"?", "keys"}, {"q", "quit"}}
	case m.focus == 1:
		h = [][2]string{{"j/k", "scroll"}, {"ctrl+d/u", "page"}, {"g/G", "top/end"}, {"esc", "back to list"}, {"ctrl+p", "commands"}, {"?", "keys"}, {"q", "quit"}}
	default:
		h = [][2]string{{"j/k", "move"}, {"enter", "read"}, {"/", "filter"}, {"tab", "switch panel"}, {"1-7", "tabs"}, {"ctrl+p", "commands"}, {"?", "keys"}, {"q", "quit"}}
	}
	out := " " + hints(h)
	// the "?" and "ctrl+p" hints are clickable too
	plain := " "
	for i, p := range h {
		if i > 0 {
			plain += "  ·  "
		}
		start := width(plain)
		plain += p[0] + " " + p[1]
		switch p[0] {
		case "?":
			m.addZone(start, y, width(plain)-1, y, "hint-help", 0)
		case "ctrl+p":
			m.addZone(start, y, width(plain)-1, y, "hint-pal", 0)
		}
	}
	return trunc(out, m.w)
}

func helpLines(w int) []string {
	sec := func(t string) string { return fg(cAcc).Bold(true).Render(t) }
	row := func(k, d string) string { return "  " + fg(cTxt).Render(fmt.Sprintf("%-12s", k)) + fg(cMut).Render(d) }
	return []string{
		sec("Everywhere"),
		row("1 - 7", "switch tab (or click it)"),
		row("ctrl+p  :", "command palette: type what you want to do"),
		row("?", "show / hide this list"),
		row("ctrl+d/u", "scroll the right panel"),
		row("esc", "go back"),
		row("q", "quit"),
		"",
		sec("Repo list"),
		row("j/k  ↑/↓", "move (or mouse wheel)"),
		row("enter", "read the README (or double-click)"),
		row("tab", "switch between list and README"),
		row("/", "filter by name, description or language"),
		row("g / G", "first / last"),
		"",
		sec("Selected repo"),
		row("o", "open in the browser"),
		row("y", "copy its link"),
		row("c", "clone into " + cloneRoot),
		row("r", "refresh from GitHub"),
		"",
		sec("Mouse"),
		row("click", "tabs, repos, buttons, commands"),
		row("wheel", "scroll the panel under the pointer"),
	}
}

func ago(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/24/30))
	}
	return fmt.Sprintf("%dy", int(d.Hours()/24/365))
}
