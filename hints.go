package main

// The key hints at the bottom: every action you can do right now, for the screen and
// the highlighted item. They wrap onto a second line instead of being cut off.

type hint = [2]string

func (m *model) hintItems() []hint {
	var h []hint
	ex := m.exActive()
	switch {
	// typing a search
	case m.filtering, ex != nil && ex.filtering, m.tab == tabPRs && m.prs.filtering,
		m.tab == tabIssues && m.iss.filtering, m.tab == tabActions && m.act.filtering:
		return []hint{{"type", "to find"}, {"↑↓", "move"}, {"enter", "keep it / open"}, {"esc", "clear"}}

	case ex != nil:
		h = m.exHints(ex)
		h = append(h, hint{"esc", "back"}, hint{"[ ]", "sections"})
		if m.focus == 0 {
			h = append(h, hint{"/", "find"})
		}

	case m.tab == tabPRs:
		h = m.prHints()
		h = append(h, hint{"f", "scope"}, hint{"s", "open/closed"}, hint{"/", "find"}, hint{"1-7", "tabs"})
	case m.tab == tabIssues:
		h = m.issueHints()
		h = append(h, hint{"f", "scope"}, hint{"/", "find"}, hint{"1-7", "tabs"})
	case m.tab == tabActions:
		h = m.runHints()
		h = append(h, hint{"f", "all/failed/running"}, hint{"/", "find"}, hint{"1-7", "tabs"})
	case m.tab != tabRepos:
		h = []hint{{"1-7", "tabs"}}

	case m.focus == 1:
		h = []hint{{"j/k", "scroll"}, {"ctrl+d/u", "page"}, {"g/G", "top/end"}, {"v", "full screen"}, {"esc", "back to list"}}
	default:
		h = []hint{{"j/k", "move"}, {"enter", "open repo"}, {"v", "read README"}, {"tab", "scroll README"}, {"/", "filter"},
			{"e", "nvim"}, {"L", "lazygit"}}
		if r := m.selected(); r != nil && m.localPath(r) != "" {
			h = append(h, hint{"p", "pull"}, hint{"P", "push"})
		} else {
			h = append(h, hint{"c", "clone"})
		}
		h = append(h, hint{"o", "browser"}, hint{"y", "copy link"}, hint{"r", "refresh"}, hint{"1-7", "tabs"}, hint{"q", "quit"})
	}
	return append(h, hint{"ctrl+p", "commands"}, hint{"?", "all keys"})
}

func (m *model) exHints(ex *explorer) []hint {
	it, ok := m.exSel()
	kind := ""
	if ok {
		kind = it.kind
	}
	local := m.isLocal()
	switch ex.sec {
	case secCode:
		if m.focus == 1 {
			return []hint{{"j/k", "scroll"}, {"ctrl+d/u", "page"}, {"v", "full screen"}, {"e", "nvim"}, {"o", "browser"}}
		}
		h := []hint{{"j/k", "move"}, {"enter", "open"}, {"v", "full screen"}, {"e", "nvim"}, {"L", "lazygit"}}
		if local {
			h = append(h, hint{"p", "pull"}, hint{"P", "push"})
		} else {
			h = append(h, hint{"c", "clone"})
		}
		return append(h, hint{"o", "browser"}, hint{"y", "copy link"}, hint{"r", "refresh"})

	case secChanges:
		if !local {
			return []hint{{"c", "clone it to see your changes"}}
		}
		c := m.chSel()
		switch {
		case ex.plan != nil:
		case c != nil && c.conflict() && m.focus == 1:
			return []hint{{"j/k", "next conflict"}, {"o", "keep top"}, {"t", "keep bottom"}, {"b", "keep both"}, {"e", "fix in nvim"}}
		case c != nil && c.conflict():
			return []hint{{"enter", "fix conflicts"}, {"O", "keep all top"}, {"T", "keep all bottom"}, {"e", "nvim"}, {"ctrl+p", "continue / abort"}}
		case c != nil && m.focus == 1:
			return []hint{{"j/k", "pick a line"}, {"space", "stage / unstage the line"}, {"a", "the whole hunk"}}
		case kind == "stash":
			return []hint{{"enter", "pop / apply / drop / branch"}, {"d", "drop"}, {"s", "stash"}}
		}
		h := []hint{}
		if c != nil {
			h = append(h, hint{"space", "stage/unstage"}, hint{"enter", "pick lines"}, hint{"d", "discard"}, hint{"e", "nvim"})
		}
		h = append(h, hint{"a", "stage all"}, hint{"c", "commit"}, hint{"C", "commit in nvim"}, hint{"A", "amend"},
			hint{"s", "stash"}, hint{"S", "stash…"}, hint{"p", "pull"}, hint{"P", "push"}, hint{"f", "fetch"})
		if m.chState() != "" {
			h = append(h, hint{"ctrl+p", "continue / abort"})
		}
		return h

	case secCommits:
		if ex.plan != nil {
			return []hint{{"j/k", "move"}, {"p", "pick"}, {"s", "squash"}, {"f", "fixup"}, {"r", "reword"}, {"d", "drop"}, {"J/K", "move it"}, {"enter", "run"}}
		}
		if m.focus == 1 {
			return []hint{{"j/k", "scroll"}, {"ctrl+d/u", "page"}, {"v", "full screen"}}
		}
		if local {
			return []hint{{"enter", "changes"}, {"i", "rebase from here"}, {"S", "squash"}, {"F", "fixup"}, {"R", "reword"}, {"D", "drop"},
				{"C", "cherry-pick"}, {"t", "revert"}, {"X", "reset"}, {"T", "tag"}, {"y", "copy id"}, {"space", "check out"}}
		}
		return []hint{{"j/k", "move"}, {"enter", "changes"}, {"v", "full screen"}, {"o", "browser"}, {"y", "copy link"}}

	case secBranches:
		if !local {
			return []hint{{"j/k", "move"}, {"enter", "browse its code"}, {"o", "browser"}}
		}
		switch kind {
		case "remote":
			return []hint{{"f", "fetch"}, {"E", "change URL"}, {"R", "rename"}, {"D", "remove"}, {"A", "add a remote"}}
		case "worktree":
			return []hint{{"e", "nvim"}, {"L", "lazygit"}, {"D", "remove"}}
		case "lbranch":
			b := it.lb
			switch {
			case b.tag:
				return []hint{{"space", "check out"}, {"P", "push tag"}, {"D", "delete"}}
			case b.remote:
				return []hint{{"space", "get it locally"}, {"enter", "commits"}, {"D", "delete on GitHub"}, {"w", "worktree"}, {"b", "code"}, {"n", "new branch"}, {"A", "add remote"}}
			case b.current:
				return []hint{{"enter", "commits"}, {"n", "new branch"}, {"R", "rename"}, {"u", "follow GitHub branch"}, {"w", "worktree"}, {"b", "code"}, {"P", "push"}, {"A", "add remote"}}
			}
			return []hint{{"space", "switch"}, {"enter", "commits"}, {"M", "merge into yours"}, {"B", "rebase yours onto it"}, {"R", "rename"},
				{"D", "delete"}, {"u", "follow GitHub branch"}, {"w", "worktree"}, {"b", "code"}, {"n", "new branch"}}
		}
		return []hint{{"n", "new branch"}, {"A", "add remote"}}

	case secIssues:
		if m.focus == 1 {
			return []hint{{"j/k", "scroll"}, {"enter", "read"}, {"C", "comment"}, {"x", "close/reopen"}, {"t", "labels"}, {"E", "edit"}}
		}
		return append(m.issueHints(), hint{"s", "open/closed"})
	case secPRs:
		return append(m.prHints(), hint{"s", "open/closed"})
	case secActions:
		return m.runHints()
	case secReleases:
		return []hint{{"j/k", "move"}, {"enter", "read notes"}, {"d", "download"}, {"N", "new release"}, {"o", "browser"}}
	}
	return nil
}

func (m *model) prHints() []hint {
	if m.focus == 1 && m.prs.view == pvComments {
		return []hint{{"j/k", "pick a comment"}, {"r", "reply"}, {"x", "resolve"}, {"+", "react"}, {"enter", "go to the code"}, {"o", "browser"}}
	}
	views := hint{"[ ]", "views"}
	if m.exActive() != nil && m.focus == 0 {
		views = hint{"{ }", "views"}
	}
	h := []hint{{"j/k", "move"}, {"enter", "full screen"}, views}
	if m.focus == 1 {
		h[0] = hint{"j/k", "scroll"}
	}
	if p := m.selPR(); p != nil && p.open() && !p.merged() {
		h = append(h, hint{"m", "merge"}, hint{"R", "review"})
	}
	return append(h, hint{"C", "comment"}, hint{"x", "close/reopen"}, hint{"K", "check out"}, hint{"N", "new PR"}, hint{"o", "browser"})
}

func (m *model) issueHints() []hint {
	return []hint{{"j/k", "move"}, {"enter", "read"}, {"C", "comment"}, {"N", "new"}, {"x", "close/reopen"}, {"t", "labels"}, {"E", "edit"}, {"o", "browser"}}
}

func (m *model) runHints() []hint {
	h := []hint{{"j/k", "move"}, {"enter", "logs"}}
	if r := m.selRun(); r != nil && r.running() {
		h = append(h, hint{"x", "cancel"})
	} else {
		h = append(h, hint{"R", "re-run"})
	}
	return append(h, hint{"w", "start a workflow"}, hint{"o", "browser"}, hint{"r", "refresh"})
}

const hintSep = "  ·  "

// wrapHints splits the hints into at most 2 lines that fit; "?" always stays visible
func wrapHints(h []hint, w int) [][]hint {
	plainW := func(x hint) int { return width(x[0]) + 1 + width(x[1]) }
	var lines [][]hint
	var cur []hint
	cw := 1
	for _, x := range h {
		add := plainW(x)
		if len(cur) > 0 {
			add += width(hintSep)
		}
		if cw+add > w && len(cur) > 0 {
			lines = append(lines, cur)
			cur, cw = nil, 1
			add = plainW(x)
		}
		cur = append(cur, x)
		cw += add
	}
	if len(cur) > 0 {
		lines = append(lines, cur)
	}
	if len(lines) > 2 {
		// too many for 2 lines: keep what fits and end with "?"
		last := lines[1]
		for len(last) > 1 {
			lw := 1
			for i, x := range last {
				if i > 0 {
					lw += width(hintSep)
				}
				lw += plainW(x)
			}
			if lw+width(hintSep)+plainW(hint{"?", "all keys"}) <= w {
				break
			}
			last = last[:len(last)-1]
		}
		lines = [][]hint{lines[0], append(last, hint{"?", "all keys"})}
	}
	return lines
}

// hintRows is how many lines the bottom hints need now
func (m *model) hintRows() int {
	if m.prompt != nil || m.palette {
		return 1
	}
	return max(1, len(wrapHints(m.hintItems(), m.w)))
}

func (m *model) hintLines(y0 int) []string {
	if m.prompt != nil || m.palette {
		return []string{m.hintBar(y0)}
	}
	var out []string
	for li, line := range wrapHints(m.hintItems(), m.w) {
		y := y0 + li
		plain := " "
		for i, p := range line {
			if i > 0 {
				plain += hintSep
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
		out = append(out, trunc(" "+hints(line), m.w))
	}
	return out
}
