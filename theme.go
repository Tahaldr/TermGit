package main

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Muted onedark, the same values as the hub and NvChad.
var (
	cAcc    = lipgloss.Color("#61afef") // accent: focus, selection, keys
	cTxt    = lipgloss.Color("#abb2bf") // normal text
	cMut    = lipgloss.Color("#565c64") // hints, secondary text
	cHint   = lipgloss.Color("#818792") // a bit brighter than muted
	cDim    = lipgloss.Color("#3e4451") // borders of unfocused panels, selection background
	cChip   = lipgloss.Color("#2d3139") // button background
	cGreen  = lipgloss.Color("#98c379")
	cRed    = lipgloss.Color("#e06c75")
	cYellow = lipgloss.Color("#e5c07b")
	cOrange = lipgloss.Color("#d19a66")
)

func fg(c color.Color) lipgloss.Style { return lipgloss.NewStyle().Foreground(c) }

// Nerd Font icons
const (
	icHome      = ""
	icRepo      = ""
	icIssue     = ""
	icPR        = ""
	icActions   = ""
	icInbox     = ""
	icExplore   = ""
	icLock      = ""
	icFork      = ""
	icStar      = ""
	icBranch    = ""
	icFolder    = ""
	icClock     = ""
	icArchive   = ""
	icGithub    = ""
	icLink      = ""
	icGlobe     = ""
	icDown      = ""
	icRefresh   = ""
	icCheck     = ""
	icWarn      = ""
	icSearch    = ""
	icCode      = ""
	icCommit    = ""
	icFile      = ""
	icUp        = ""
	icNvim      = ""
	icGit       = ""
	icClose     = ""
	icTag       = ""
	icIssueDone = ""
	icIssueNo   = ""
	icComment   = ""
	icMerge     = ""
	icChanges   = ""
	icCloud     = ""
	icCalendar  = ""
	icUser      = ""
	icOrg       = ""
	icEdit      = ""
	icInfo      = ""
	icCommand   = ""
	icRead      = ""
)

// ---- string helpers (ANSI-aware) ----

func width(s string) int { return ansi.StringWidth(s) }

func trunc(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if width(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

// pad cuts or fills s to exactly w columns
func pad(s string, w int) string {
	s = trunc(s, w)
	if n := w - width(s); n > 0 {
		s += strings.Repeat(" ", n)
	}
	return s
}

// box draws a rounded panel of exactly w x h with a title in the top border and
// an optional note in the bottom border. focus colors the border.
func box(title string, w, h int, body []string, focus bool, note string) []string {
	if w < 4 || h < 2 {
		return nil
	}
	bc := cDim
	tc := cHint
	if focus {
		bc, tc = cAcc, cAcc
	}
	b := fg(bc)
	inner := w - 2
	top := "─"
	if title != "" {
		t := trunc(title, inner-4)
		top += " " + fg(tc).Bold(focus).Render(t) + " "
	}
	if n := inner - width(top); n > 0 {
		top += strings.Repeat("─", n)
	}
	out := make([]string, 0, h)
	out = append(out, b.Render("╭")+b.Render(top)+b.Render("╮"))
	for i := 0; i < h-2; i++ {
		line := ""
		if i < len(body) {
			line = body[i]
		}
		out = append(out, b.Render("│")+" "+pad(line, inner-2)+" "+b.Render("│"))
	}
	bot := ""
	if note != "" {
		bot = " " + fg(cMut).Render(note) + " ─"
	}
	fill := inner - width(bot)
	if fill < 0 {
		bot, fill = "", inner
	}
	out = append(out, b.Render("╰"+strings.Repeat("─", fill))+bot+b.Render("╯"))
	return out
}
