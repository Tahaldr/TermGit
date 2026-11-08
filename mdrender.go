package main

// Markdown drawn the way render-markdown.nvim shows it in your Nvim:
// heading bars with icons, □ / ■ checkboxes (done ones faded), ● ○ ◆ ◇ bullets,
// code blocks on a dark background with the language on top, ▋ quotes, boxed tables.

import (
	"fmt"
	"image/color"
	"regexp"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/text"
)

type mdHeading struct {
	level int
	text  string
	line  int // line number in the drawn output
}

type mdDoc struct {
	lines []string
	heads []mdHeading
	jump  int // line to open at (first error in a log), -1 or 0 = top
}

var (
	mdParser = goldmark.New(goldmark.WithExtensions(extension.GFM)).Parser()

	cBg     = lipgloss.Color("#282c34") // onedark background, used to blend the heading bars
	cCodeBg = lipgloss.Color("#2d3139")
	cBold   = lipgloss.Color("#c8ccd4")

	headColors = []color.Color{
		lipgloss.Color("#61afef"), lipgloss.Color("#e5c07b"), lipgloss.Color("#98c379"),
		lipgloss.Color("#56b6c2"), lipgloss.Color("#c678dd"), lipgloss.Color("#e06c75"),
	}
	// the same icons render-markdown uses: 󰲡 󰲣 󰲥 󰲧 󰲩 󰲫
	headIcons = []string{"\U000F0CA1", "\U000F0CA3", "\U000F0CA5", "\U000F0CA7", "\U000F0CA9", "\U000F0CAB"}
	bullets   = []string{"●", "○", "◆", "◇"}

	icLinkMd  = "\U000F0339" // 󰌹
	icImage   = "\U000F0976" // 󰥶
	calloutRe = regexp.MustCompile(`(?mi)^([ \t]*>[ \t]*)\[!(NOTE|TIP|IMPORTANT|WARNING|CAUTION)\][ \t]*$`)
)

type callout struct {
	icon, title string
	c           color.Color
}

var callouts = map[string]callout{
	"NOTE":      {"\U000F02FD", "Note", lipgloss.Color("#61afef")},
	"TIP":       {"\U000F0336", "Tip", lipgloss.Color("#98c379")},
	"IMPORTANT": {"\U000F017E", "Important", lipgloss.Color("#c678dd")},
	"WARNING":   {"\U000F002A", "Warning", lipgloss.Color("#e5c07b")},
	"CAUTION":   {"\U000F0CE6", "Caution", lipgloss.Color("#e06c75")},
}

// blend mixes c into the background (a = how much of c)
func blend(c color.Color, a float64) color.Color {
	r1, g1, b1, _ := c.RGBA()
	r2, g2, b2, _ := cBg.RGBA()
	mix := func(x, y uint32) uint8 { return uint8((float64(x>>8)*a + float64(y>>8)*(1-a))) }
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", mix(r1, r2), mix(g1, g2), mix(b1, b2)))
}

type mdr struct {
	src   []byte
	depth int // list nesting
	fade  bool
}

// renderMD draws markdown for a width of w columns
func renderMD(md string, w int) mdDoc {
	md = strings.ReplaceAll(md, "\r\n", "\n")
	// GitHub callouts: put the marker on its own paragraph so the box title is easy to find
	md = calloutRe.ReplaceAllString(md, "$1⟦$2⟧\n$1")
	src := []byte(md)
	r := &mdr{src: src}
	doc := mdParser.Parse(text.NewReader(src))
	var d mdDoc
	first := true
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		lines := r.block(n, w)
		if len(lines) == 0 {
			continue
		}
		if !first {
			d.lines = append(d.lines, "")
		}
		first = false
		if h, ok := n.(*ast.Heading); ok {
			d.heads = append(d.heads, mdHeading{h.Level, ansi.Strip(r.inline(h, lipgloss.NewStyle())), len(d.lines)})
		}
		d.lines = append(d.lines, lines...)
	}
	return d
}

func renderMarkdown(md string, w int) []string { return renderMD(md, w).lines }

// children draws the blocks inside a container; tight = no blank line between them
func (r *mdr) children(n ast.Node, w int, tight bool) []string {
	var out []string
	first := true
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		lines := r.block(c, w)
		if len(lines) == 0 {
			continue
		}
		if !first && !tight {
			out = append(out, "")
		}
		first = false
		out = append(out, lines...)
	}
	return out
}

func (r *mdr) base() lipgloss.Style {
	if r.fade {
		return fg(cMut)
	}
	return fg(cTxt)
}

func wrapLines(s string, w int) []string {
	if w < 4 {
		w = 4
	}
	var out []string
	for _, l := range strings.Split(ansi.Wrap(s, w, ""), "\n") {
		out = append(out, strings.TrimRight(l, " "))
	}
	return out
}

func (r *mdr) block(n ast.Node, w int) []string {
	switch n := n.(type) {
	case *ast.Heading:
		lvl := min(max(n.Level, 1), 6) - 1
		c := headColors[lvl]
		bg := lipgloss.NewStyle().Background(blend(c, 0.22))
		st := bg.Foreground(c).Bold(true)
		title := r.inline(n, st)
		line := st.Render(" "+headIcons[lvl]+" ") + title
		var out []string
		for _, l := range wrapLines(line, w-1) {
			out = append(out, l+bg.Render(strings.Repeat(" ", max(w-width(l), 0))))
		}
		return out
	case *ast.Paragraph, *ast.TextBlock:
		return wrapLines(r.inline(n, r.base()), w)
	case *ast.ThematicBreak:
		return []string{fg(cDim).Render(strings.Repeat("─", w))}
	case *ast.FencedCodeBlock:
		return r.code(string(n.Language(r.src)), n.Lines(), w)
	case *ast.CodeBlock:
		return r.code("", n.Lines(), w)
	case *ast.Blockquote:
		return r.quote(n, w)
	case *ast.List:
		return r.list(n, w)
	case *ast.HTMLBlock:
		return r.html(n, w)
	case *east.Table:
		return r.table(n, w)
	}
	if n.HasChildren() {
		return r.children(n, w, false)
	}
	return nil
}

func langIcon(lang string) string {
	switch strings.ToLower(lang) {
	case "go":
		return ""
	case "lua":
		return ""
	case "js", "javascript", "jsx":
		return ""
	case "ts", "typescript", "tsx":
		return ""
	case "py", "python":
		return ""
	case "sh", "bash", "shell", "zsh", "console":
		return ""
	case "ps1", "powershell", "pwsh":
		return ""
	case "json", "jsonc":
		return ""
	case "md", "markdown":
		return ""
	case "html":
		return ""
	case "css", "scss":
		return ""
	case "rust", "rs":
		return ""
	case "php":
		return ""
	case "yaml", "yml", "toml":
		return ""
	}
	return icCode
}

// code block: dark background across the width, language on top, colored code
func (r *mdr) code(lang string, segs *text.Segments, w int) []string {
	var b strings.Builder
	for i := 0; i < segs.Len(); i++ {
		s := segs.At(i)
		b.Write(s.Value(r.src))
	}
	bg := lipgloss.NewStyle().Background(cCodeBg)
	fill := func(l string) string {
		l = trunc(l, w)
		return l + bg.Render(strings.Repeat(" ", max(w-width(l), 0)))
	}
	label := " "
	if lang != "" {
		label = " " + langIcon(lang) + " " + lang
	}
	out := []string{fill(bg.Foreground(cHint).Render(label))}
	var lexer chroma.Lexer
	if lang != "" {
		lexer = lexers.Get(lang)
	}
	for _, l := range colorCode(lexer, strings.TrimRight(b.String(), "\n"), cCodeBg) {
		out = append(out, fill(bg.Render(" ")+l))
	}
	return append(out, fill(""))
}

func (r *mdr) quote(n *ast.Blockquote, w int) []string {
	inner := r.children(n, w-2, false)
	bar, title := fg(cMut), ""
	if len(inner) > 0 {
		first := strings.TrimSpace(ansi.Strip(inner[0]))
		if strings.HasPrefix(first, "⟦") && strings.HasSuffix(first, "⟧") {
			if c, ok := callouts[strings.Trim(first, "⟦⟧")]; ok {
				bar = fg(c.c)
				title = bar.Bold(true).Render(c.icon + " " + c.title)
				inner = inner[1:]
				if len(inner) > 0 && strings.TrimSpace(ansi.Strip(inner[0])) == "" {
					inner = inner[1:]
				}
			}
		}
	}
	var out []string
	if title != "" {
		out = append(out, bar.Render("▋ ")+title)
	}
	for _, l := range inner {
		out = append(out, bar.Render("▋ ")+l)
	}
	return out
}

func (r *mdr) list(n *ast.List, w int) []string {
	var out []string
	num := n.Start
	if num == 0 {
		num = 1
	}
	r.depth++
	defer func() { r.depth-- }()
	for it := n.FirstChild(); it != nil; it = it.NextSibling() {
		if !n.IsTight && len(out) > 0 {
			out = append(out, "")
		}
		var marker string
		checked, isTask := taskState(it)
		switch {
		case isTask && checked:
			marker = fg(cGreen).Render("■") + " "
		case isTask:
			marker = fg(cAcc).Render("□") + " "
		case n.IsOrdered():
			marker = fg(cAcc).Render(strconv.Itoa(num)+".") + " "
		default:
			marker = fg(cAcc).Render(bullets[(r.depth-1)%len(bullets)]) + " "
		}
		num++
		mw := width(marker)
		old := r.fade
		if isTask && checked {
			r.fade = true // done items fade out, like in your notebook
		}
		body := r.children(it, w-mw, n.IsTight)
		r.fade = old
		if len(body) == 0 {
			body = []string{""}
		}
		for i, l := range body {
			if i == 0 {
				out = append(out, marker+l)
			} else {
				out = append(out, strings.Repeat(" ", mw)+l)
			}
		}
	}
	return out
}

func taskState(item ast.Node) (checked, ok bool) {
	if p := item.FirstChild(); p != nil {
		if cb, is := p.FirstChild().(*east.TaskCheckBox); is {
			return cb.IsChecked, true
		}
	}
	return false, false
}

var (
	tagRe = regexp.MustCompile(`(?s)<[^>]*>`)
	imgRe = regexp.MustCompile(`(?i)<img[^>]*?alt\s*=\s*"([^"]*)"`)
)

// raw HTML (centered logos, badges…): show image names and the text, dimmed
func (r *mdr) html(n *ast.HTMLBlock, w int) []string {
	var b strings.Builder
	for i := 0; i < n.Lines().Len(); i++ {
		s := n.Lines().At(i)
		b.Write(s.Value(r.src))
	}
	if n.HasClosure() {
		b.Write(n.ClosureLine.Value(r.src))
	}
	raw := b.String()
	var out []string
	for _, m := range imgRe.FindAllStringSubmatch(raw, -1) {
		if strings.TrimSpace(m[1]) != "" {
			out = append(out, fg(cMut).Render(icImage+" "+m[1]))
		}
	}
	for _, l := range strings.Split(tagRe.ReplaceAllString(raw, "\n"), "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "<!--") {
			out = append(out, wrapLines(fg(cMut).Render(l), w)...)
		}
	}
	return out
}

func (r *mdr) table(t *east.Table, w int) []string {
	var rows [][]string
	header := 0
	for row := t.FirstChild(); row != nil; row = row.NextSibling() {
		var cells []string
		_, isHead := row.(*east.TableHeader)
		for c := row.FirstChild(); c != nil; c = c.NextSibling() {
			st := r.base()
			if isHead {
				st = fg(cBold).Bold(true)
			}
			cells = append(cells, r.inline(c, st))
		}
		if isHead {
			header = 1
		}
		rows = append(rows, cells)
	}
	cols := len(t.Alignments)
	for _, row := range rows {
		cols = max(cols, len(row))
	}
	if cols == 0 {
		return nil
	}
	widths := make([]int, cols)
	for _, row := range rows {
		for i, c := range row {
			widths[i] = max(widths[i], width(c))
		}
	}
	// shrink the widest columns until the table fits
	avail := w - (3*cols + 1)
	for {
		total, wi := 0, 0
		for i, x := range widths {
			total += x
			if x > widths[wi] {
				wi = i
			}
		}
		if total <= avail || widths[wi] <= 4 {
			break
		}
		widths[wi]--
	}
	b := fg(cDim)
	line := func(l, m, rr string) string {
		parts := make([]string, cols)
		for i, x := range widths {
			parts[i] = strings.Repeat("─", x+2)
		}
		return b.Render(l + strings.Join(parts, m) + rr)
	}
	out := []string{line("┌", "┬", "┐")}
	for ri, row := range rows {
		// long cells wrap onto more lines
		cells := make([][]string, cols)
		height := 1
		for i := 0; i < cols; i++ {
			if i < len(row) && row[i] != "" {
				cells[i] = wrapLines(row[i], widths[i])
			}
			height = max(height, len(cells[i]))
		}
		for ln := 0; ln < height; ln++ {
			s := b.Render("│")
			for i := 0; i < cols; i++ {
				c := ""
				if ln < len(cells[i]) {
					c = trunc(cells[i][ln], widths[i])
				}
				gap := widths[i] - width(c)
				al := east.AlignNone
				if i < len(t.Alignments) {
					al = t.Alignments[i]
				}
				switch al {
				case east.AlignRight:
					c = strings.Repeat(" ", gap) + c
				case east.AlignCenter:
					c = strings.Repeat(" ", gap/2) + c + strings.Repeat(" ", gap-gap/2)
				default:
					c += strings.Repeat(" ", gap)
				}
				s += " " + c + " " + b.Render("│")
			}
			out = append(out, s)
		}
		if ri == header-1 {
			out = append(out, line("├", "┼", "┤"))
		}
	}
	return append(out, line("└", "┴", "┘"))
}

// inline draws the text inside a block with st as the base style
func (r *mdr) inline(n ast.Node, st lipgloss.Style) string {
	var b strings.Builder
	afterBox := false
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		box := afterBox
		afterBox = false
		switch c := c.(type) {
		case *ast.Text:
			t := string(c.Segment.Value(r.src))
			if box {
				t = strings.TrimLeft(t, " ")
			}
			b.WriteString(st.Render(t))
			switch {
			case c.HardLineBreak():
				b.WriteString("\n")
			case c.SoftLineBreak():
				b.WriteString(st.Render(" "))
			}
		case *ast.String:
			b.WriteString(st.Render(string(c.Value)))
		case *ast.CodeSpan:
			b.WriteString(lipgloss.NewStyle().Background(cCodeBg).Foreground(cOrange).Render(ansi.Strip(r.inline(c, lipgloss.NewStyle()))))
		case *ast.Emphasis:
			s := st
			if c.Level >= 2 {
				s = s.Bold(true)
				if !r.fade {
					s = s.Foreground(cBold)
				}
			} else {
				s = s.Italic(true)
			}
			b.WriteString(r.inline(c, s))
		case *ast.Link:
			s := st.Foreground(cAcc)
			if r.fade {
				s = st
			}
			b.WriteString(s.Render(icLinkMd+" ") + r.inline(c, s))
		case *ast.AutoLink:
			s := st.Foreground(cAcc)
			b.WriteString(s.Render(icLinkMd + " " + string(c.Label(r.src))))
		case *ast.Image:
			alt := ansi.Strip(r.inline(c, lipgloss.NewStyle()))
			if alt == "" {
				alt = "image"
			}
			b.WriteString(st.Foreground(cMut).Render(icImage + " " + alt))
		case *ast.RawHTML:
			var raw string
			for i := 0; i < c.Segments.Len(); i++ {
				s := c.Segments.At(i)
				raw += string(s.Value(r.src))
			}
			if strings.HasPrefix(strings.ToLower(raw), "<br") {
				b.WriteString("\n")
			}
		case *east.Strikethrough:
			b.WriteString(r.inline(c, st.Strikethrough(true).Foreground(cMut)))
		case *east.TaskCheckBox:
			afterBox = true // drawn by the list as □ / ■
		default:
			b.WriteString(r.inline(c, st))
		}
	}
	return strings.TrimLeft(b.String(), " ")
}
