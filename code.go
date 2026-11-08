package main

// Drawing file contents (with syntax colors) and commit changes (diffs).

import (
	"fmt"
	"path"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

var codeStyle = styles.Get("onedark")

// highlight colors source code and returns one string per line, with line numbers
func highlight(name, text string) []string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\t", "    ")
	text = strings.TrimRight(text, "\n")
	lexer := lexers.Match(name)
	if lexer == nil {
		lexer = lexers.Analyse(text)
	}
	if lexer == nil {
		lexer = lexers.Fallback
	}
	lexer = chroma.Coalesce(lexer)

	var lines []string
	cur := ""
	if it, err := lexer.Tokenise(nil, text); err == nil {
		for _, tok := range it.Tokens() {
			st := lipgloss.NewStyle().Foreground(cTxt)
			if e := codeStyle.Get(tok.Type); e.Colour.IsSet() {
				st = st.Foreground(lipgloss.Color(e.Colour.String()))
			}
			parts := strings.Split(tok.Value, "\n")
			for i, p := range parts {
				if i > 0 {
					lines = append(lines, cur)
					cur = ""
				}
				if p != "" {
					cur += st.Render(p)
				}
			}
		}
		lines = append(lines, cur)
	} else {
		lines = strings.Split(text, "\n")
	}
	digits := len(fmt.Sprint(len(lines)))
	num := fg(cDim)
	for i := range lines {
		lines[i] = num.Render(fmt.Sprintf("%*d  ", digits, i+1)) + lines[i]
	}
	return lines
}

func isMarkdown(name string) bool {
	switch strings.ToLower(path.Ext(name)) {
	case ".md", ".markdown", ".mdx":
		return true
	}
	return false
}

func humanSize(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprintf("%d B", n)
	case n < 1000*1000:
		return fmt.Sprintf("%.1f KB", float64(n)/1000)
	}
	return fmt.Sprintf("%.1f MB", float64(n)/1000/1000)
}

// short size for list rows: "812", "4.2k", "1.3M"
func shortSize(n int) string {
	switch {
	case n < 1000:
		return fmt.Sprint(n)
	case n < 1000*1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1000/1000)
}

// fileStatus is a one-letter mark for a changed file, like git status
func fileStatus(s string) string {
	switch s {
	case "added":
		return fg(cGreen).Render("A")
	case "removed":
		return fg(cRed).Render("D")
	case "renamed":
		return fg(cAcc).Render("R")
	case "copied":
		return fg(cAcc).Render("C")
	}
	return fg(cYellow).Render("M")
}

// diffLines draws the changes of one commit, file after file
func diffLines(files []commitFile, w int) []string {
	var out []string
	for i, f := range files {
		if i > 0 {
			out = append(out, "")
		}
		name := f.Name
		if f.Prev != "" && f.Prev != f.Name {
			name = f.Prev + " → " + f.Name
		}
		head := fileStatus(f.Status) + " " + fg(cTxt).Bold(true).Render(name) + "  " +
			fg(cGreen).Render(fmt.Sprintf("+%d", f.Adds)) + " " + fg(cRed).Render(fmt.Sprintf("−%d", f.Dels))
		out = append(out, head, fg(cDim).Render(strings.Repeat("─", w)))
		if f.Patch == "" {
			out = append(out, fg(cMut).Render("  (no text changes to show: binary or very large file)"))
			continue
		}
		for _, l := range strings.Split(strings.ReplaceAll(f.Patch, "\t", "    "), "\n") {
			switch {
			case strings.HasPrefix(l, "@@"):
				out = append(out, fg(cAcc).Faint(true).Render(l))
			case strings.HasPrefix(l, "+"):
				out = append(out, fg(cGreen).Render(l))
			case strings.HasPrefix(l, "-"):
				out = append(out, fg(cRed).Render(l))
			case strings.HasPrefix(l, `\`):
				out = append(out, fg(cMut).Render(l))
			default:
				out = append(out, fg(cTxt).Render(l))
			}
		}
	}
	return out
}
