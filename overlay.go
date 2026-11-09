package main

// Floating things drawn over the screen: the input popup (like Nvim's cmdline popup),
// y / n dialogs, the command palette and pickers, and toasts for messages.

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// put draws box at x, y over the screen lines
func put(lines []string, x, y int, box []string, w int) {
	for i, b := range box {
		row := y + i
		if row < 0 || row >= len(lines) {
			continue
		}
		base := lines[row]
		left := ansi.Truncate(base, x, "")
		if pad := x - ansi.StringWidth(left); pad > 0 {
			left += strings.Repeat(" ", pad)
		}
		right := ansi.TruncateLeft(base, x+w, "")
		lines[row] = left + "\x1b[0m" + b + "\x1b[0m" + right
	}
}

// popupBox is a rounded box with the title in its top border and room for body lines
func popupBox(title string, w int, body []string, border lipgloss.Style, bg bool) []string {
	inner := w - 2
	top := "─"
	if title != "" {
		top += " " + title + " "
	}
	top = trunc(top, inner)
	top += strings.Repeat("─", max(0, inner-width(top)))
	out := []string{border.Render("╭") + border.Render(top) + border.Render("╮")}
	for _, l := range body {
		out = append(out, border.Render("│")+" "+pad(l, inner-2)+" "+border.Render("│"))
	}
	out = append(out, border.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return out
}

func (m *model) popupWidth(min0, max0 int) int {
	return max(min(min0, m.w-4), min(max0, m.w*60/100))
}

// what's being typed right now (a filter, a search, a name, a message…)
func (m *model) typing() (title, label, text string, ok bool) {
	ex := m.exActive()
	switch {
	case m.prompt != nil && !m.prompt.confirm:
		return "", m.prompt.label, m.prompt.text, true
	case m.filtering:
		return icSearch + " Filter repos", "", m.filter, true
	case ex != nil && ex.filtering:
		what := map[int]string{secCode: "Find a file", secChanges: "Find a changed file", secCommits: "Find a commit", secBranches: "Find a branch",
			secActions: "Find a run", secReleases: "Find a release", secIssues: "Find an issue", secPRs: "Find a PR"}[ex.sec]
		return icSearch + " " + what, "", ex.query[ex.sec], true
	case m.tab == tabPRs && m.prs.filtering:
		return icSearch + " Find a PR", "", m.prs.query, true
	case m.tab == tabIssues && m.iss.filtering:
		return icSearch + " Find an issue", "", m.iss.query, true
	case m.tab == tabActions && m.act.filtering:
		return icSearch + " Find a run", "", m.act.query, true
	case m.tab == tabInbox && m.inbox.filtering:
		return icSearch + " Find a notification", "", m.inbox.query, true
	}
	return "", "", "", false
}

// cmdline: the input popup near the top, like noice.nvim's
func (m *model) cmdlinePopup(lines []string) {
	title, label, text, ok := m.typing()
	if !ok {
		return
	}
	w := m.popupWidth(50, 96)
	inner := w - 4
	if title == "" {
		// a question: short ones become the title, long ones go above the input
		if width(label) <= inner-4 {
			title, label = label, ""
		} else {
			title = "Input"
		}
		if strings.Contains(strings.ToLower(title+label), "search") {
			title = icSearch + " " + strings.TrimSuffix(title, ":")
		} else {
			title = icEdit + " " + strings.TrimSuffix(title, ":")
		}
	}
	var body []string
	if label != "" {
		body = append(body, wrapText(label, inner, fg(cHint))...)
	}
	input := fg(cAcc).Bold(true).Render("❯ ") + fg(cTxt).Render(text) + lipgloss.NewStyle().Background(cAcc).Render(" ")
	body = append(body, trunc(input, inner))
	box := popupBox(fg(cAcc).Bold(true).Render(title), w, body, fg(cAcc), true)
	x := (m.w - w) / 2
	put(lines, x, 2, box, w)
}

func dangerous(label string) bool {
	l := strings.ToLower(label)
	for _, w := range []string{"can't be undone", "delete", "forever", "force", "hard", "throw away", "discard", "lost", "remove", "drop"} {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}

// dialog: a y / n question in the middle of the screen
func (m *model) dialogPopup(lines []string) {
	p := m.prompt
	if p == nil || !p.confirm {
		return
	}
	w := m.popupWidth(46, 76)
	inner := w - 4
	border, title := fg(cAcc), icInfo+" Confirm"
	if dangerous(p.label) {
		border, title = fg(cRed), icWarn+" Careful"
	}
	body := []string{""}
	body = append(body, wrapText(p.label, inner, fg(cTxt))...)
	body = append(body, "")
	yes := lipgloss.NewStyle().Background(cAcc).Foreground(lipgloss.Color("#282c34")).Bold(true).Render("  y  Yes  ")
	if dangerous(p.label) {
		yes = lipgloss.NewStyle().Background(cRed).Foreground(lipgloss.Color("#282c34")).Bold(true).Render("  y  Yes  ")
	}
	noLabel := "  n  No  "
	if p.no != nil {
		noLabel = "  n  " + "No, the other way  "
	}
	no := lipgloss.NewStyle().Background(cChip).Foreground(cTxt).Render(noLabel)
	esc := fg(cMut).Render("   esc cancel")
	buttons := yes + "  " + no + esc
	gap := max(0, (inner-width(buttons))/2)
	body = append(body, strings.Repeat(" ", gap)+buttons, "")
	box := popupBox(border.Bold(true).Render(title), w, body, border, true)
	x, y := (m.w-w)/2, max(2, (m.h-len(box))/2-2)
	put(lines, x, y, box, w)
	by := y + 1 + len(body) - 2
	bx := x + 2 + gap
	m.addZone(x, y, x+w-1, y+len(box)-1, "dlg", 0)
	m.addZone(bx, by, bx+width(yes)-1, by, "dlg-yes", 0)
	m.addZone(bx+width(yes)+2, by, bx+width(yes)+1+width(no), by, "dlg-no", 0)
}

// the command palette and pickers, as a popup
func (m *model) palettePopup(lines []string) {
	if !m.palette {
		return
	}
	list := m.palMatches()
	w := m.popupWidth(60, 100)
	inner := w - 4
	rows := min(12, max(1, m.h-10))
	start := 0
	if m.palCur >= rows {
		start = m.palCur - rows + 1
	}
	title := icCommand + " Commands"
	if m.palTitle != "" {
		title = icCommand + " " + m.palTitle
	}
	input := fg(cAcc).Bold(true).Render("❯ ") + fg(cTxt).Render(m.palQ) + lipgloss.NewStyle().Background(cAcc).Render(" ")
	if m.palQ == "" {
		input += fg(cMut).Render(" type to filter")
	}
	body := []string{input, fg(cDim).Render(strings.Repeat("─", inner))}
	x := (m.w - w) / 2
	y := 2
	m.addZone(x, y, x+w-1, y+rows+4, "palbox", 0)
	if len(list) == 0 {
		body = append(body, fg(cMut).Render("Nothing matches"))
	}
	for i := start; i < len(list) && i < start+rows; i++ {
		c := list[i]
		st := lipgloss.NewStyle()
		nc := cTxt
		mark := "  "
		if i == m.palCur {
			st, nc = st.Background(cDim), cAcc
			mark = fg(cAcc).Inherit(st).Render("▌ ")
		}
		k := st.Foreground(cMut).Render(c.key + " ")
		name := mark + st.Foreground(nc).Render(c.name)
		gap := inner - width(name) - width(k)
		body = append(body, name+st.Render(strings.Repeat(" ", max(gap, 1)))+k)
		ly := y + 1 + len(body) - 1
		m.addZone(x+1, ly, x+w-2, ly, "pal", i)
	}
	note := ""
	if len(list) > rows {
		note = fg(cMut).Render("  " + itoa(len(list)) + " matches · ↑↓ for more")
	}
	if note != "" {
		body = append(body, note)
	}
	box := popupBox(fg(cAcc).Bold(true).Render(title), w, body, fg(cAcc), true)
	put(lines, x, y, box, w)
}

// toast: the latest message, top right, for a few seconds
func (m *model) toastPopup(lines []string) {
	if m.status == "" {
		return
	}
	c, ic := cGreen, icCheck
	if m.statusErr {
		c, ic = cRed, icWarn
	}
	maxW := min(70, m.w-4)
	text := wrapText(m.status, maxW-6, fg(cTxt))
	w := 0
	for _, l := range text {
		w = max(w, width(l))
	}
	w = min(maxW, w+6)
	body := make([]string, len(text))
	for i, l := range text {
		if i == 0 {
			body[i] = fg(c).Render(ic+" ") + l
		} else {
			body[i] = "  " + l
		}
	}
	box := popupBox("", w, body, fg(c), true)
	put(lines, m.w-w-1, 1, box, w)
}

// overlays draws every floating thing that's open, in order
func (m *model) overlays(content string) string {
	lines := strings.Split(content, "\n")
	for len(lines) < m.h {
		lines = append(lines, "")
	}
	m.toastPopup(lines)
	m.palettePopup(lines)
	m.cmdlinePopup(lines)
	m.dialogPopup(lines)
	return strings.Join(lines, "\n")
}
