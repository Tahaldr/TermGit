package main

// Links you can ctrl+click: markdown links (marked with OSC 8 while drawing) and any
// http(s):// address on screen. GitHub links open inside TermGit (repo, file, issue,
// PR, profile); links to other files in a repo open that file; the rest the browser.

import (
	"net/url"
	"os/exec"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

func ghCmd(args ...string) *exec.Cmd { return exec.Command("gh", args...) }

// osc8 marks text as a link to u (taken off again before the screen is drawn)
func osc8(u, text string) string {
	if u == "" {
		return text
	}
	return "\x1b]8;;" + u + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}

type linkZone struct {
	x0, x1, y int
	url       string
}

var rawURL = regexp.MustCompile(`https?://[^\s<>"'` + "`" + `)\]│]+`)

// takeLinks finds the links on the screen, remembers where they are, and removes the
// OSC 8 marks (so the terminal doesn't also act on them)
func takeLinks(content string) (string, []linkZone) {
	var zones []linkZone
	lines := strings.Split(content, "\n")
	for y, line := range lines {
		if !strings.Contains(line, "\x1b]8;") && !strings.Contains(line, "://") {
			continue
		}
		var b strings.Builder
		col, open, start := 0, "", 0
		i := 0
		for i < len(line) {
			if strings.HasPrefix(line[i:], "\x1b]8;") {
				end := strings.Index(line[i:], "\x1b\\")
				if end < 0 {
					break
				}
				seq := line[i+4 : i+end] // params;url
				u := ""
				if k := strings.Index(seq, ";"); k >= 0 {
					u = seq[k+1:]
				}
				if u != "" {
					open, start = u, col
				} else if open != "" {
					zones = append(zones, linkZone{start, col - 1, y, open})
					open = ""
				}
				i += end + 2
				continue
			}
			if line[i] == 0x1b { // other escape sequences: copy, no width
				j := i + 1
				if j < len(line) && line[j] == '[' {
					j++
					for j < len(line) && (line[j] < 0x40 || line[j] > 0x7e) {
						j++
					}
					j++
				}
				b.WriteString(line[i:min(j, len(line))])
				i = j
				continue
			}
			// one character
			r, size := utf8.DecodeRuneInString(line[i:])
			b.WriteString(line[i : i+size])
			col += ansi.StringWidth(string(r))
			i += size
		}
		if open != "" {
			zones = append(zones, linkZone{start, col - 1, y, open})
		}
		clean := b.String()
		lines[y] = clean
		// plain addresses in the text
		plain := ansi.Strip(clean)
		for _, loc := range rawURL.FindAllStringIndex(plain, -1) {
			x0 := ansi.StringWidth(plain[:loc[0]])
			x1 := x0 + ansi.StringWidth(plain[loc[0]:loc[1]]) - 1
			zones = append(zones, linkZone{x0, x1, y, strings.TrimRight(plain[loc[0]:loc[1]], ".,;:")})
		}
	}
	return strings.Join(lines, "\n"), zones
}

func (m *model) linkAt(x, y int) string {
	for _, z := range m.linkZones {
		if z.y == y && x >= z.x0 && x <= z.x1 {
			return z.url
		}
	}
	return ""
}

// followLink opens a link: GitHub ones inside TermGit, files of the same repo, else the browser
func (m *model) followLink(link string) tea.Cmd {
	rd := m.rd
	m.rd = nil // GitHub links open a page in TermGit: leave full screen
	cmd := m.followLink2(link)
	if !m.homeLeft {
		m.rd = rd // it went to the browser: stay where you were
	}
	return cmd
}

func (m *model) followLink2(link string) tea.Cmd {
	m.homeLeft = false
	if strings.HasPrefix(link, "#") || strings.HasPrefix(link, "mailto:") {
		return m.setStatus("That link points inside the page", false)
	}
	u, err := url.Parse(link)
	if err != nil {
		return m.openURL(link)
	}
	if u.Scheme == "" { // a link to another file in this repo
		if ex := m.ex; ex != nil {
			base := ex.dir
			if it, ok := m.exSel(); ok && it.kind == "file" {
				base = parentDir(it.ent.Path)
			}
			target := strings.TrimPrefix(path.Clean(path.Join(base, u.Path)), "/")
			m.homeLeft = true
			return m.gotoFile(ex.repo.NameWithOwner, target)
		}
		return m.setStatus("Can't tell which repo "+link+" belongs to", true)
	}
	if u.Host != "github.com" && u.Host != "www.github.com" {
		return m.openURL(link)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	switch {
	case len(parts) == 1 && parts[0] != "":
		m.homeLeft = true
		return m.openProfile(parts[0])
	case len(parts) >= 4 && parts[2] == "issues":
		n, _ := strconv.Atoi(parts[3])
		m.homeLeft = true
		return m.gotoIssue(parts[0]+"/"+parts[1], n, true)
	case len(parts) >= 4 && parts[2] == "pull":
		n, _ := strconv.Atoi(parts[3])
		m.homeLeft = true
		return m.gotoPR(parts[0]+"/"+parts[1], n, true)
	case len(parts) >= 5 && (parts[2] == "blob" || parts[2] == "tree"):
		m.homeLeft = true
		return m.gotoFile(parts[0]+"/"+parts[1], strings.Join(parts[4:], "/"))
	case len(parts) == 2:
		m.homeLeft = true
		return m.gotoItem(parts[0]+"/"+parts[1], secCode, "", func(exItem) bool { return true }, nil)
	}
	return m.openURL(link)
}

// gotoFile opens repo nwo's Code at that file (or folder)
func (m *model) gotoFile(nwo, p string) tea.Cmd {
	cmd := m.gotoItem(nwo, secCode, p, func(it exItem) bool { return (it.kind == "file" || it.kind == "dir") && it.ent.Path == p }, nil)
	if m.ex != nil {
		if m.ex.ref == "" {
			m.ex.ref = "HEAD"
		}
		m.ex.dir = parentDir(p)
		return tea.Batch(cmd, m.exSelected())
	}
	return cmd
}
