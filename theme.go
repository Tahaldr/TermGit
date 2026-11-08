package main

import (
	"image/color"
	"strings"

	"charm.land/glamour/v2"
	gansi "charm.land/glamour/v2/ansi"
	"charm.land/glamour/v2/styles"
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
	icHome    = ""
	icRepo    = ""
	icIssue   = ""
	icPR      = ""
	icActions = ""
	icInbox   = ""
	icExplore = ""
	icLock    = ""
	icFork    = ""
	icStar    = ""
	icBranch  = ""
	icFolder  = ""
	icClock   = ""
	icArchive = ""
	icGithub  = ""
	icLink    = ""
	icGlobe   = ""
	icDown    = ""
	icRefresh = ""
	icCheck   = ""
	icWarn    = ""
	icSearch  = ""
	icCode    = ""
	icCommit  = ""
	icFile    = ""
	icUp      = ""
	icNvim    = ""
	icGit     = ""
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

// ---- markdown (READMEs) ----

func sp(s string) *string { return &s }
func up(u uint) *uint     { return &u }
func bp(b bool) *bool     { return &b }

func mdStyle() gansi.StyleConfig {
	s := styles.DarkStyleConfig
	s.Document.Color = sp("#abb2bf")
	s.Document.Margin = up(0)
	s.Document.BlockPrefix = ""
	s.Heading.Color = sp("#61afef")
	s.H1.Color = sp("#61afef")
	s.H1.BackgroundColor = nil
	s.H1.Prefix = "# "
	s.H1.Suffix = ""
	s.Link.Color = sp("#61afef")
	s.LinkText.Color = sp("#61afef")
	s.LinkText.Bold = bp(false)
	s.Image.Color = sp("#565c64")
	s.ImageText.Color = sp("#565c64")
	s.Code.Color = sp("#d19a66")
	s.Code.BackgroundColor = sp("#2d3139")
	s.CodeBlock.Theme = "onedark"
	s.CodeBlock.Chroma = nil
	s.CodeBlock.Margin = up(0)
	s.BlockQuote.Color = sp("#818792")
	s.HorizontalRule.Color = sp("#3e4451")
	s.Strong.Color = sp("#c8ccd4")
	return s
}

var mdRenderers = map[int]*glamour.TermRenderer{}

func renderMarkdown(md string, w int) []string {
	r, ok := mdRenderers[w]
	if !ok {
		var err error
		r, err = glamour.NewTermRenderer(glamour.WithStyles(mdStyle()), glamour.WithWordWrap(w))
		if err != nil {
			return strings.Split(md, "\n")
		}
		mdRenderers[w] = r
	}
	out, err := r.Render(md)
	if err != nil {
		return strings.Split(md, "\n")
	}
	lines := strings.Split(strings.TrimRight(out, "\n "), "\n")
	// drop leading blank lines
	for len(lines) > 0 && strings.TrimSpace(ansi.Strip(lines[0])) == "" {
		lines = lines[1:]
	}
	return lines
}
