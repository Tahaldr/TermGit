package main

// The reader: full-screen view of a README, a markdown file, a code file or a commit.
// Markdown gets a Contents panel (headings) to jump around. `termgit notes.md` opens
// any file on this PC straight in the reader and reloads it when the file changes.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type reader struct {
	title, sub string
	icon       string
	mdSrc      func(m *model) (string, bool)       // markdown text, if this is markdown and it's loaded
	docFn      func(m *model, w int) (mdDoc, bool) // ready-made lines + headings (CI logs)
	autoJump   bool                                // open at doc.jump once it's there
	content    func(m *model, w int) []string      // anything else (code, commit, loading message)
	path       string                              // a file on this PC (termgit file.md)
	text       string
	mtime      time.Time

	off     int
	max     int
	outline bool
	sel     int  // heading picked in Contents
	pinned  bool // sel was picked by a jump/click (else it follows the scroll)

	cacheW   int
	cacheSrc string
	doc      mdDoc

	find string
	hits []int
	hit  int
}

type watchTick struct{}

func itoa(n int) string { return strconv.Itoa(n) }

func (m *model) rdLines(w int) ([]string, []mdHeading) {
	rd := m.rd
	if rd.docFn != nil {
		if d, ok := rd.docFn(m, w); ok {
			if rd.autoJump && d.jump > 0 {
				rd.off, rd.autoJump = max(0, d.jump-3), false
			}
			return d.lines, d.heads
		}
	}
	if rd.mdSrc != nil {
		if src, ok := rd.mdSrc(m); ok && strings.TrimSpace(src) != "" {
			if rd.cacheW != w || rd.cacheSrc != src {
				rd.doc = renderMD(src, w)
				rd.cacheW, rd.cacheSrc = w, src
			}
			return rd.doc.lines, rd.doc.heads
		}
	}
	if rd.content != nil {
		return rd.content(m, w), nil
	}
	return []string{fg(cMut).Render("Loading…")}, nil
}

func (m *model) openReader(rd *reader) tea.Cmd {
	if rd.icon == "" {
		rd.icon = icFile
	}
	rd.outline = m.w >= 110
	m.rd, m.help, m.palette = rd, false, false
	return nil
}

// readerFor builds the reader for what's on screen now
func (m *model) readerFor() *reader {
	if ex := m.exActive(); ex != nil {
		it, ok := m.exSel()
		if !ok {
			return nil
		}
		nwo := ex.repo.NameWithOwner
		if it.kind == "release" {
			rel := it.release
			return &reader{title: rel.title(), sub: nwo + " · " + rel.Tag, icon: icTag,
				mdSrc:   func(m *model) (string, bool) { return rel.Body, true },
				content: func(m *model, w int) []string { return []string{fg(cMut).Render("No release notes.")} }}
		}
		var e *treeEntry
		switch it.kind {
		case "file":
			e = &it.ent
		case "dir":
			e = m.dirReadme(it.ent.Path)
		}
		if e != nil && isMarkdown(e.Path) {
			sha := e.Sha
			return &reader{title: e.Path, sub: nwo + " · " + ex.ref, icon: "",
				mdSrc: func(m *model) (string, bool) {
					if b := m.blobs[sha]; b != nil && b.err == "" {
						return b.text, true
					}
					return "", false
				},
				content: func(m *model, w int) []string { return m.blobLines(*e, w) }}
		}
		title, _ := m.exPreview(40)
		return &reader{title: title, sub: nwo + " · " + ex.ref,
			content: func(m *model, w int) []string { _, l := m.exPreview(w); return l }}
	}
	r := m.selected()
	if r == nil {
		return nil
	}
	nwo := r.NameWithOwner
	return &reader{title: "README", sub: nwo, icon: "",
		mdSrc: func(m *model) (string, bool) {
			e, ok := m.readme[nwo]
			return e.md, ok && !e.none && strings.TrimSpace(e.md) != ""
		},
		content: func(m *model, w int) []string {
			if e, ok := m.readme[nwo]; ok && (e.none || strings.TrimSpace(e.md) == "") {
				return []string{fg(cMut).Render("This repo has no README.")}
			}
			return []string{fg(cMut).Render("Loading README…")}
		}}
}

func (m *model) actRead() tea.Cmd {
	if m.selRun() != nil {
		return m.actLogs()
	}
	rd := m.readerFor()
	if rd == nil {
		return nil
	}
	return m.openReader(rd)
}

// ---- a file on this PC ----

func loadFileReader(p string) (*reader, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	rd := &reader{title: filepath.Base(abs), sub: tildePath(filepath.Dir(abs)), path: abs, text: string(data), mtime: st.ModTime()}
	if isMarkdown(abs) {
		rd.icon = ""
		rd.mdSrc = func(m *model) (string, bool) { return m.rd.text, true }
	} else {
		name := filepath.Base(abs)
		rd.content = func(m *model, w int) []string { return highlight(name, m.rd.text) }
	}
	return rd, nil
}

func watch() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return watchTick{} })
}

// reload the file when it changed on disk (you saved it in Nvim)
func (m *model) rdReload() {
	rd := m.rd
	if rd == nil || rd.path == "" {
		return
	}
	st, err := os.Stat(rd.path)
	if err != nil || st.ModTime().Equal(rd.mtime) {
		return
	}
	if data, err := os.ReadFile(rd.path); err == nil {
		rd.text, rd.mtime = string(data), st.ModTime()
	}
}

// ---- keys and mouse ----

func (m *model) closeReader() tea.Cmd {
	if m.solo {
		return tea.Quit
	}
	m.rd = nil
	return nil
}

func (m *model) rdScroll(d int) {
	rd := m.rd
	old := rd.off
	rd.off = max(0, min(rd.max, rd.off+d))
	if rd.off != old {
		rd.pinned = false
	}
}

// rdSelect picks heading i: scrolls to it, and keeps it highlighted even when the
// page can't scroll that far (headings near the end of the file)
func (m *model) rdSelect(i int) {
	_, heads := m.rdLines(m.rdWidth())
	if i < 0 || i >= len(heads) {
		return
	}
	rd := m.rd
	rd.sel, rd.pinned = i, true
	rd.off = max(0, min(heads[i].line, rd.max))
}

func (m *model) rdRows() int { return m.h - 3 }

func (m *model) rdJumpHeading(dir int) {
	_, heads := m.rdLines(m.rdWidth())
	if len(heads) == 0 {
		m.rdScroll(dir * (m.rdRows() - 2))
		return
	}
	cur := m.rdCurHead(heads)
	switch {
	case dir > 0 && cur < len(heads)-1:
		m.rdSelect(cur + 1)
	case dir < 0 && cur > 0:
		m.rdSelect(cur - 1)
	case dir < 0:
		m.rd.off, m.rd.pinned = 0, false
	}
}

// rdCurHead is the heading to highlight: the picked one, or the one you scrolled to
func (m *model) rdCurHead(heads []mdHeading) int {
	rd := m.rd
	if rd.pinned && rd.sel < len(heads) {
		return rd.sel
	}
	cur := -1
	for i, h := range heads {
		if h.line <= rd.off+2 {
			cur = i
		}
	}
	return cur
}

func (m *model) rdFind(q string) tea.Cmd {
	rd := m.rd
	rd.find, rd.hits, rd.hit = q, nil, 0
	if q == "" {
		return nil
	}
	lines, _ := m.rdLines(m.rdWidth())
	lq := strings.ToLower(q)
	for i, l := range lines {
		if strings.Contains(strings.ToLower(ansi.Strip(l)), lq) {
			rd.hits = append(rd.hits, i)
		}
	}
	if len(rd.hits) == 0 {
		return m.setStatus("\""+q+"\" isn't in this page", true)
	}
	rd.hit = 0
	for i, h := range rd.hits {
		if h >= rd.off {
			rd.hit = i
			break
		}
	}
	return m.rdShowHit()
}

func (m *model) rdShowHit() tea.Cmd {
	rd := m.rd
	if len(rd.hits) == 0 {
		return nil
	}
	rd.off = max(0, min(rd.max, rd.hits[rd.hit]-2))
	return m.setStatus("\""+rd.find+"\" "+itoa(rd.hit+1)+" of "+itoa(len(rd.hits)), false)
}

func (m *model) rdKey(k tea.KeyPressMsg) tea.Cmd {
	rd := m.rd
	s := k.String()
	page := m.rdRows()
	switch s {
	case "esc", "q", "backspace", "h", "left", "?":
		if rd.find != "" && s == "esc" {
			rd.find, rd.hits = "", nil
			return nil
		}
		return m.closeReader()
	case "j", "down":
		m.rdScroll(1)
	case "k", "up":
		m.rdScroll(-1)
	case "ctrl+d":
		m.rdScroll(page / 2)
	case "ctrl+u":
		m.rdScroll(-page / 2)
	case "space", "pgdown", "f":
		m.rdScroll(page - 2)
	case "pgup", "b":
		m.rdScroll(-(page - 2))
	case "g", "home":
		rd.off, rd.pinned = 0, false
	case "G", "end":
		rd.off, rd.pinned = rd.max, false
	case "]", "}":
		m.rdJumpHeading(1)
	case "[", "{":
		m.rdJumpHeading(-1)
	case "t":
		rd.outline = !rd.outline
	case "/":
		m.prompt = &prompt{label: "Find:", done: func(m *model, v string) tea.Cmd { return m.rdFind(v) }}
	case "n":
		if len(rd.hits) > 0 {
			rd.hit = (rd.hit + 1) % len(rd.hits)
			return m.rdShowHit()
		}
	case "N":
		if len(rd.hits) > 0 {
			rd.hit = (rd.hit - 1 + len(rd.hits)) % len(rd.hits)
			return m.rdShowHit()
		}
	case "e":
		if rd.path != "" {
			c := editorCmd(rd.path)
			c.Dir = filepath.Dir(rd.path)
			if m.dump {
				return m.setStatus("Would run nvim "+rd.path, false)
			}
			return tea.ExecProcess(c, func(err error) tea.Msg { return appDoneMsg{"nvim", err} })
		}
		return m.actOpen("nvim")
	case "o":
		if rd.path == "" {
			return m.actBrowser()
		}
	case "y":
		if rd.path == "" {
			return m.actCopy()
		}
	case "r":
		if rd.path != "" {
			rd.mtime = time.Time{}
			m.rdReload()
			return m.setStatus("Reloaded", false)
		}
	}
	return nil
}

func (m *model) rdClick(z *zone) tea.Cmd {
	switch z.id {
	case "toc":
		m.rdSelect(z.idx)
	case "rd-close":
		return m.closeReader()
	case "rd-toc":
		m.rd.outline = !m.rd.outline
	}
	return nil
}

// ---- drawing ----

func (m *model) tocWidth() int {
	if !m.rd.outline {
		return 0
	}
	aj := m.rd.autoJump // only counting headings here: don't jump yet
	m.rd.autoJump = false
	_, heads := m.rdLines(80)
	m.rd.autoJump = aj
	if len(heads) < 2 {
		return 0
	}
	return max(24, min(36, m.w/4))
}

// rdWidth is the width of the text column: the whole window, from the left
func (m *model) rdWidth() int {
	return max(20, m.w-m.tocWidth()-4)
}

func (m *model) rdRender() string {
	rd := m.rd
	tw := m.tocWidth()
	cw := m.rdWidth()
	lines, heads := m.rdLines(cw)
	rows := m.rdRows()
	rd.max = max(0, len(lines)-rows)
	rd.off = max(0, min(rd.off, rd.max))

	// header: icon, name, where it's from, position
	pct := "all"
	if rd.max > 0 {
		pct = itoa(rd.off*100/rd.max) + "%"
	}
	closeLbl := " " + icClose + " "
	left := " " + fg(cAcc).Render(rd.icon) + " " + fg(cAcc).Bold(true).Render(rd.title) + "  " + fg(cMut).Render(rd.sub)
	right := fg(cMut).Render(pct) + "  " + lipgloss.NewStyle().Background(cChip).Foreground(cHint).Render(closeLbl) + " "
	gap := m.w - width(left) - width(right)
	if gap < 1 {
		left = trunc(left, m.w-width(right)-1)
		gap = m.w - width(left) - width(right)
	}
	if m.status != "" {
		c, ic := cGreen, icCheck
		if m.statusErr {
			c, ic = cRed, icWarn
		}
		right = fg(c).Render(ic+" "+m.status) + "  " + right
	}
	gap = m.w - width(left) - width(right)
	if gap < 1 {
		left = trunc(left, max(m.w-width(right)-1, 0))
		gap = m.w - width(left) - width(right)
	}
	m.addZone(m.w-width(closeLbl)-1, 0, m.w-2, 0, "rd-close", 0)
	out := []string{left + strings.Repeat(" ", max(gap, 0)) + right, fg(cDim).Render(strings.Repeat("─", m.w))}

	// which heading are we in
	curHead := m.rdCurHead(heads)
	var toc []string
	if tw > 0 {
		var body []string
		start := 0
		if curHead >= rows-2 {
			start = curHead - (rows - 2) + 1
		}
		for i := start; i < len(heads) && len(body) < rows-2; i++ {
			h := heads[i]
			st := fg(cHint)
			if h.level == 1 {
				st = fg(cTxt)
			}
			mark := "  "
			if i == curHead {
				st = fg(cAcc).Bold(true)
				mark = fg(cAcc).Render("▌ ")
			}
			label := strings.Repeat("  ", min(h.level-1, 4)) + h.text
			body = append(body, mark+st.Render(trunc(label, tw-6)))
			y := 2 + 1 + len(body) - 1
			m.addZone(0, y, tw-1, y, "toc", i)
		}
		toc = box("Contents", tw, rows, body, false, "t hide")
	}

	area := m.w - tw
	margin := 2 // text starts at the left, like a normal file
	for i := 0; i < rows; i++ {
		l := ""
		if tw > 0 {
			l = toc[i]
		}
		text := ""
		if j := rd.off + i; j < len(lines) {
			text = lines[j]
			if rd.find != "" && len(rd.hits) > 0 && j == rd.hits[rd.hit] {
				text = fg(cYellow).Render("▶") + text
			}
		}
		// a bar next to the picked heading, so you see where you jumped
		if rd.pinned && curHead >= 0 && rd.off+i == heads[curHead].line && margin >= 2 {
			l += strings.Repeat(" ", margin-2) + fg(cAcc).Render("▌ ") + pad(text, area-margin)
			out = append(out, l)
			continue
		}
		l += strings.Repeat(" ", margin) + pad(text, area-margin)
		out = append(out, l)
	}
	m.addZone(tw, 2, m.w-1, 2+rows-1, "rd-body", 0)
	out = append(out, m.rdHints(m.h-1))
	return strings.Join(out, "\n")
}

func (m *model) rdHints(y int) string {
	if m.prompt != nil {
		return m.hintBar(y)
	}
	h := [][2]string{{"j/k", "scroll"}, {"space", "page"}, {"] [", "next/prev heading"}, {"t", "contents"}, {"/", "find"}}
	if len(m.rd.hits) > 0 {
		h = append(h, [2]string{"n/N", "next/prev match"})
	}
	h = append(h, [2]string{"e", "nvim"})
	if m.rd.path == "" {
		h = append(h, [2]string{"o", "browser"})
	}
	if m.solo {
		h = append(h, [2]string{"q", "quit"})
	} else {
		h = append(h, [2]string{"esc", "close"})
	}
	return trunc(" "+hints(h), m.w)
}

func newSoloModel(rd *reader) *model {
	m := newModel()
	m.solo = true
	m.rd = rd
	rd.outline = true
	return m
}

// tildePath shortens C:\Users\you\x to ~\x
func tildePath(p string) string {
	if h, err := os.UserHomeDir(); err == nil && strings.HasPrefix(strings.ToLower(p), strings.ToLower(h)) {
		return "~" + p[len(h):]
	}
	return p
}
