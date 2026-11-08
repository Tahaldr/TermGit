package main

// The Comments view of a pull request: every comment as a card you can pick
// (j / k in the right panel, or a click), then reply, resolve, react or jump to the code.

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/cli/go-gh/v2/pkg/api"
)

const (
	pvOverview = iota
	pvComments
	pvCommits
	pvChecks
	pvFiles
)

type Reaction struct {
	Content string `json:"content"` // THUMBS_UP, HEART…
	Mine    bool   `json:"viewerHasReacted"`
	Users   struct {
		TotalCount int `json:"totalCount"`
	} `json:"reactors"`
}

var reactions = []struct{ content, emoji, name string }{
	{"THUMBS_UP", "👍", "thumbs up"}, {"THUMBS_DOWN", "👎", "thumbs down"}, {"LAUGH", "😄", "laugh"},
	{"HOORAY", "🎉", "hooray"}, {"CONFUSED", "😕", "confused"}, {"HEART", "❤️", "heart"},
	{"ROCKET", "🚀", "rocket"}, {"EYES", "👀", "eyes"},
}

func emojiFor(content string) string {
	for _, r := range reactions {
		if r.content == content {
			return r.emoji
		}
	}
	return content
}

type cardReply struct {
	who  string
	at   time.Time
	body string
}

// one comment card: a PR comment, a review, or a conversation on a line of code
type card struct {
	kind      string // comment, review, thread
	who       string
	at        time.Time
	title     string // review: "approved", thread: "on file.go:12"
	body      string
	url       string
	subject   string // what reactions go on
	reactions []Reaction
	threadID  string
	path      string
	line      int
	resolved  bool
	outdated  bool
	hunk      string
	replies   []cardReply
}

func loginOf(a *struct {
	Login string `json:"login"`
}) string {
	if a != nil {
		return a.Login
	}
	return "ghost"
}

func (m *model) prCards(p *PR) []card {
	d := m.prDetails[prKey(p)]
	if d == nil {
		return nil
	}
	var out []card
	for _, c := range d.comments {
		out = append(out, card{kind: "comment", who: c.who(), at: c.Created, body: c.Body, url: c.URL, subject: c.ID, reactions: c.Reactions})
	}
	for _, r := range d.reviews {
		what := map[string]string{"APPROVED": "✓ approved", "CHANGES_REQUESTED": "✗ asked for changes", "COMMENTED": "reviewed", "DISMISSED": "review dismissed"}[r.State]
		if r.State == "COMMENTED" && strings.TrimSpace(r.Body) == "" {
			continue // just the wrapper of line comments
		}
		out = append(out, card{kind: "review", who: loginOf(r.Author), at: r.Submitted, title: what, body: r.Body, url: r.URL, subject: r.ID, reactions: r.Reactions})
	}
	for _, t := range d.threads {
		if len(t.Comments.Nodes) == 0 {
			continue
		}
		f := t.Comments.Nodes[0]
		where := t.Path
		if t.Line > 0 {
			where += fmt.Sprintf(":%d", t.Line)
		}
		c := card{kind: "thread", who: loginOf(f.Author), at: f.Created, title: "on " + where, body: f.Body, url: f.URL,
			subject: f.ID, reactions: f.Reactions, threadID: t.ID, path: t.Path, line: t.Line, resolved: t.IsResolved,
			outdated: t.IsOutdated, hunk: lastLines(f.DiffHunk, 6)}
		for _, r := range t.Comments.Nodes[1:] {
			c.replies = append(c.replies, cardReply{loginOf(r.Author), r.Created, r.Body})
		}
		out = append(out, c)
	}
	sortCards(out)
	return out
}

func sortCards(c []card) {
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && c[j].at.Before(c[j-1].at); j-- {
			c[j], c[j-1] = c[j-1], c[j]
		}
	}
}

func (m *model) selCard() (*PR, *card) {
	p := m.selPR()
	if p == nil {
		return nil, nil
	}
	cards := m.prCards(p)
	if m.prs.cmt < 0 || m.prs.cmt >= len(cards) {
		return p, nil
	}
	return p, &cards[m.prs.cmt]
}

// ---- drawing the cards ----

func (m *model) prCommentsLines(p *PR, w int) []string {
	d := m.prDetails[prKey(p)]
	switch {
	case d == nil:
		return []string{fg(cMut).Render("Loading comments…")}
	case d.err != "":
		return []string{fg(cRed).Render("Couldn't load: " + d.err)}
	}
	cards := m.prCards(p)
	if len(cards) == 0 {
		return []string{fg(cMut).Render("No comments yet."), "", hints([][2]string{{"C", "write the first one"}})}
	}
	m.prs.cmt = max(0, min(m.prs.cmt, len(cards)-1))
	var out []string
	m.cardLines = m.cardLines[:0]
	open := 0
	for _, c := range cards {
		if c.kind == "thread" && !c.resolved {
			open++
		}
	}
	summary := fmt.Sprintf("%d %s", len(cards), plural(len(cards), "comment", "comments"))
	if open > 0 {
		summary += fmt.Sprintf(" · %d open on code", open)
	}
	out = append(out, fg(cMut).Render(summary), hints([][2]string{{"j/k", "pick (in this panel)"}, {"r", "reply"}, {"x", "resolve"}, {"+", "react"}, {"enter", "go to the code"}}), "")
	for i, c := range cards {
		sel := i == m.prs.cmt && m.focus == 1
		start := len(out)
		m.cardLines = append(m.cardLines, start)
		body := m.cardBody(c, w-2)
		bar := fg(cDim).Render("│ ")
		if sel {
			bar = fg(cAcc).Render("▌ ")
		} else if i == m.prs.cmt {
			bar = fg(cHint).Render("│ ")
		}
		for _, l := range body {
			out = append(out, bar+l)
		}
		out = append(out, "")
	}
	m.cardLines = append(m.cardLines, len(out))
	return out
}

func (m *model) cardBody(c card, w int) []string {
	head := fg(cTxt).Bold(true).Render(c.who)
	if c.title != "" {
		st := fg(cHint)
		switch {
		case strings.HasPrefix(c.title, "✓"):
			st = fg(cGreen)
		case strings.HasPrefix(c.title, "✗"):
			st = fg(cRed)
		}
		head += " " + st.Render(c.title)
	}
	head += fg(cMut).Render(" · " + ago(c.at) + " ago")
	switch {
	case c.resolved:
		head += "  " + fg(cGreen).Render("✓ resolved")
	case c.outdated:
		head += "  " + fg(cMut).Render("outdated")
	case c.kind == "thread":
		head += "  " + fg(cYellow).Render("open")
	}
	out := []string{trunc(head, w)}
	if c.hunk != "" {
		for _, l := range strings.Split(c.hunk, "\n") {
			st := fg(cMut)
			switch {
			case strings.HasPrefix(l, "+"):
				st = fg(cGreen)
			case strings.HasPrefix(l, "-"):
				st = fg(cRed)
			case strings.HasPrefix(l, "@@"):
				st = fg(cAcc).Faint(true)
			}
			out = append(out, lipgloss.NewStyle().Background(cCodeBg).Render(pad(st.Inherit(lipgloss.NewStyle().Background(cCodeBg)).Render(trunc(" "+l, w)), w)))
		}
	}
	body := strings.TrimSpace(c.body)
	if body == "" {
		body = "_No text._"
	}
	out = append(out, m.cachedLines(fmt.Sprintf("card:%s:%d:%d", c.subject, len(c.body), w), func() []string { return renderMarkdown(body, w) })...)
	for _, r := range c.replies {
		out = append(out, "", fg(cHint).Render("↳ ")+fg(cTxt).Bold(true).Render(r.who)+fg(cMut).Render(" · "+ago(r.at)+" ago"))
		for _, l := range m.cachedLines(fmt.Sprintf("reply:%s:%d:%d:%s", c.subject, len(r.body), w, r.at), func() []string { return renderMarkdown(strings.TrimSpace(r.body), w-2) }) {
			out = append(out, "  "+l)
		}
	}
	var rs []string
	for _, r := range c.reactions {
		if r.Users.TotalCount == 0 {
			continue
		}
		s := fmt.Sprintf("%s %d", emojiFor(r.Content), r.Users.TotalCount)
		if r.Mine {
			rs = append(rs, lipgloss.NewStyle().Background(blend(cAcc, 0.25)).Foreground(cTxt).Render(" "+s+" "))
		} else {
			rs = append(rs, lipgloss.NewStyle().Background(cChip).Foreground(cTxt).Render(" "+s+" "))
		}
	}
	if len(rs) > 0 {
		out = append(out, "", strings.Join(rs, " "))
	}
	return out
}

// keep the picked card on screen
func (m *model) showCard() {
	if m.prs.cmt+1 >= len(m.cardLines) {
		return
	}
	_, ph := m.layout()
	rows := ph - 2
	start, end := m.cardLines[m.prs.cmt], m.cardLines[m.prs.cmt+1]
	if start < m.pvOff || end-start > rows {
		m.pvOff = max(0, start-1)
	} else if end > m.pvOff+rows {
		m.pvOff = end - rows
	}
}

// ---- keys in the Comments view (right panel focused) ----

func (m *model) cardKey(s string) (tea.Cmd, bool) {
	if m.prs.view != pvComments || m.focus != 1 {
		return nil, false
	}
	p := m.selPR()
	if p == nil {
		return nil, false
	}
	n := len(m.prCards(p))
	switch s {
	case "j", "down":
		if m.prs.cmt < n-1 {
			m.prs.cmt++
		}
	case "k", "up":
		if m.prs.cmt > 0 {
			m.prs.cmt--
		}
	case "g", "home":
		m.prs.cmt = 0
	case "G", "end":
		m.prs.cmt = max(0, n-1)
	case "r":
		return m.cardReply(), true
	case "x":
		return m.cardResolve(), true
	case "+":
		return m.cardReact(), true
	case "enter":
		return m.cardGoto(), true
	case "o":
		if _, c := m.selCard(); c != nil && c.url != "" {
			return m.openURL(c.url), true
		}
		return nil, false
	default:
		return nil, false
	}
	m.prCommentsLines(p, m.previewInner()) // refresh card positions
	m.showCard()
	return nil, true
}

func (m *model) previewInner() int {
	lw, _ := m.layout()
	return m.w - lw - 4
}

func gql(q string, vars map[string]interface{}) error {
	c, err := api.DefaultGraphQLClient()
	if err != nil {
		return err
	}
	var out map[string]interface{}
	return c.Do(q, vars, &out)
}

func (m *model) mutate(nwo string, n int, ok, q string, vars map[string]interface{}) tea.Cmd {
	if m.dump {
		return m.setStatus("Would send: "+ok, false)
	}
	return tea.Batch(m.setStatus("Sending…", false), func() tea.Msg {
		err := gql(q, vars)
		if err != nil {
			err = fmt.Errorf("%s", apiErr(err))
		}
		return issueDoneMsg{nwo, n, ok, err}
	})
}

func quote(who, body string) string {
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) > 6 {
		lines = append(lines[:6], "…")
	}
	return "> @" + who + " wrote:\n> " + strings.Join(lines, "\n> ") + "\n\n"
}

// r: a reply in the conversation on that code line, or a new comment quoting a normal one
func (m *model) cardReply() tea.Cmd {
	p, c := m.selCard()
	if c == nil {
		return nil
	}
	nwo, n, prID := p.repo(), p.Number, p.ID
	card := *c
	what := "Your reply to " + card.who + "."
	return m.editInNvim("reply", fmt.Sprintf(writeHelp, what), func(m *model, text string) tea.Cmd {
		if text == "" {
			return m.setStatus("Nothing written: no reply sent", false)
		}
		m.prompt = &prompt{label: "Send your reply to " + card.who + "?", confirm: true, done: func(m *model, _ string) tea.Cmd {
			if card.threadID != "" {
				return m.mutate(nwo, n, "Replied to "+card.who, `mutation($id: ID!, $b: String!) {
  addPullRequestReviewThreadReply(input: {pullRequestReviewThreadId: $id, body: $b}) { comment { id } } }`,
					map[string]interface{}{"id": card.threadID, "b": text})
			}
			return m.mutate(nwo, n, "Replied to "+card.who, `mutation($id: ID!, $b: String!) {
  addComment(input: {subjectId: $id, body: $b}) { clientMutationId } }`,
				map[string]interface{}{"id": prID, "b": quote(card.who, card.body) + text})
		}}
		return nil
	})
}

// x: resolve / unresolve a conversation on code
func (m *model) cardResolve() tea.Cmd {
	p, c := m.selCard()
	if c == nil {
		return nil
	}
	if c.threadID == "" {
		return m.setStatus("Only comments on code can be resolved", true)
	}
	nwo, n, id := p.repo(), p.Number, c.threadID
	if c.resolved {
		return m.mutate(nwo, n, "Marked as unresolved", `mutation($id: ID!) { unresolveReviewThread(input: {threadId: $id}) { thread { id } } }`, map[string]interface{}{"id": id})
	}
	return m.mutate(nwo, n, "Resolved", `mutation($id: ID!) { resolveReviewThread(input: {threadId: $id}) { thread { id } } }`, map[string]interface{}{"id": id})
}

// +: pick a reaction (picking one you already gave takes it back)
func (m *model) cardReact() tea.Cmd {
	p, c := m.selCard()
	if c == nil || c.subject == "" {
		return nil
	}
	nwo, n, id := p.repo(), p.Number, c.subject
	mine := map[string]bool{}
	for _, r := range c.reactions {
		if r.Mine {
			mine[r.Content] = true
		}
	}
	var items []command
	for _, r := range reactions {
		r := r
		label := r.emoji + "  " + r.name
		q, ok := `mutation($id: ID!, $c: ReactionContent!) { addReaction(input: {subjectId: $id, content: $c}) { clientMutationId } }`, "Reacted "+r.emoji
		if mine[r.content] {
			label = r.emoji + "  " + r.name + "  (yours: pick to take it back)"
			q, ok = `mutation($id: ID!, $c: ReactionContent!) { removeReaction(input: {subjectId: $id, content: $c}) { clientMutationId } }`, "Took back "+r.emoji
		}
		items = append(items, command{label, "", func(m *model) tea.Cmd {
			return m.mutate(nwo, n, ok, q, map[string]interface{}{"id": id, "c": r.content})
		}})
	}
	m.openPicker("React to "+c.who+"'s comment", items)
	return nil
}

// enter: show the file this comment is about, in the Files view
func (m *model) cardGoto() tea.Cmd {
	p, c := m.selCard()
	if c == nil {
		return nil
	}
	if c.path == "" {
		return m.setStatus("This comment isn't on a line of code", false)
	}
	m.prs.view, m.pvOff = pvFiles, 0
	if f := m.prFilesMap[prKey(p)]; f != nil {
		d := diffDoc(f.files, m.previewInner())
		for _, h := range d.heads {
			if h.text == c.path {
				m.pvOff = h.line
			}
		}
	}
	return m.setStatus(fmt.Sprintf("%s, line %d", c.path, c.line), false)
}
