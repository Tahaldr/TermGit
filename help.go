package main

// All the keys, by screen. `?` shows them full screen (Contents jumps between screens,
// / finds a key); the same list feeds the right-panel help.

import (
	"fmt"
	"strings"
)

type helpSec struct {
	title string
	rows  []hint
}

func helpData() []helpSec {
	return []helpSec{
		{"Everywhere", []hint{
			{"1 – 7", "switch tab: Home, Repos, Issues, PRs, Actions, Inbox, Explore (or click it)"},
			{"ctrl+p  :", "command palette: type what you want to do"},
			{"?", "this page"},
			{"j / k  ↑ ↓", "move (or the mouse wheel)"},
			{"tab  h  l", "switch between the list and the right panel"},
			{"ctrl+d / ctrl+u", "scroll the right panel"},
			{"g / G", "first / last"},
			{"enter", "open (a double-click too)"},
			{"v", "full screen"},
			{"o  y", "open in the browser / copy the link"},
			{"r", "refresh from GitHub"},
			{"esc", "back one step (the path at the top); from a jump, back where you came from"},
			{"q  ctrl+c", "quit"},
		}},
		{"Home (1)", []hint{
			{"enter", "go to the item's page in TermGit"},
			{"R", "on failing CI: re-run · on a review request: review"},
			{"m", "on a PR: merge"},
			{"c  p  P", "on a repo on this PC: commit / pull / push"},
			{"M", "mark all notifications read"},
			{"alt+1 – 5", "open a recent repo"},
			{"t", "dashboard / two-panel layout"},
			{"click a tile", "Your PRs → PRs tab, CI → failing runs"},
		}},
		{"Repos (2)", []hint{
			{"enter", "open the repo"},
			{"v", "read its README full screen"},
			{"/", "filter by name, description or language"},
			{"e  L", "open it in your editor / git tool (offers to clone first)"},
			{"c", "clone it"},
			{"p  P", "pull / push (repos on this PC)"},
			{"N", "new repo: empty (opens its Quick setup), empty + clone, or upload a folder"},
			{"empty repo", "Quick setup: y / Y copy the HTTPS / SSH address, U pushes a folder from this PC into it"},
			{"R  E", "rename / edit the description"},
			{"V", "make private / public"},
			{"A", "archive / unarchive"},
			{"D", "delete (you type its name to confirm)"},
			{"s  F", "star / fork"},
			{"badges", "folder = on this PC · ● changes not committed · ↑ to push · ↓ to pull"},
		}},
		{"Inside a repo", []hint{
			{"[  ]", "sections: Code, Changes, Issues, PRs, Commits, Branches, Actions, Releases (or click)"},
			{"enter  l", "open a folder / file / item"},
			{"esc  h  ⌫", "back: preview → list → section → Code → up a folder → repo list"},
			{"/", "find a file anywhere in the repo, or an item in the section"},
			{"crumbs", "click the path at the top to jump back"},
			{"e  L", "editor (the highlighted file) / git tool"},
			{"p  P", "pull / push"},
		}},
		{"Changes (repos on this PC)", []hint{
			{"space", "stage / unstage the file"},
			{"a", "stage / unstage everything"},
			{"enter", "pick single lines: j / k, space stages one, a the whole hunk"},
			{"d", "discard the file's changes (asks first)"},
			{"c  C", "commit: message in the bottom bar / in your editor"},
			{"A", "amend the last commit"},
			{"s  S", "stash your changes / choose what to stash"},
			{"enter on a stash", "pop / apply / drop / make a branch"},
			{"f", "fetch"},
			{"conflicts", "files marked U: enter, then o top · t bottom · b both; O / T for the whole file"},
			{"ctrl+p", "Continue / Abort a merge, rebase or cherry-pick"},
		}},
		{"Commits (repos on this PC: your history and graph)", []hint{
			{"enter", "what changed"},
			{"i", "interactive rebase from here: p pick · s squash · f fixup · r reword · d drop · J/K move · enter run"},
			{"S  F", "squash / fixup into the commit below"},
			{"R  D", "reword / drop"},
			{"C", "cherry-pick onto your branch"},
			{"t", "revert"},
			{"X", "reset to here: soft / mixed / hard"},
			{"T", "tag it"},
			{"y", "copy its id"},
			{"space", "check it out"},
		}},
		{"Branches (repos on this PC)", []hint{
			{"space", "switch to it (a GitHub-only branch gets a local copy)"},
			{"enter", "its commits"},
			{"n", "new branch"},
			{"M  B", "merge it into yours / rebase yours onto it"},
			{"R  D", "rename / delete"},
			{"u", "follow a GitHub branch"},
			{"w", "open it in its own folder (worktree)"},
			{"b", "browse its code on GitHub"},
			{"P", "on a tag: push it"},
			{"A", "add a remote · on a remote: f fetch, E URL, R rename, D remove"},
			{"worktree", "e editor · L git tool · D remove"},
		}},
		{"Issues (3, or a repo's Issues)", []hint{
			{"enter", "read the conversation full screen"},
			{"C", "comment (written in your editor)"},
			{"N", "new issue"},
			{"x", "close / reopen"},
			{"t", "labels"},
			{"E", "edit the description"},
			{"s  f", "open / closed · scope"},
		}},
		{"Pull requests (4, or a repo's PRs)", []hint{
			{"[  ]", "views: Overview, Comments, Commits, Checks, Files ({ } inside a repo)"},
			{"enter", "the view full screen (Checks: the failing check's logs)"},
			{"m", "merge: squash / merge commit / rebase, keep or delete the branch"},
			{"R", "review: approve / request changes / comment"},
			{"C", "comment"},
			{"K", "check it out on this PC"},
			{"x", "close / reopen"},
			{"N", "new PR"},
			{"Comments", "tab into the panel: j / k pick · r reply · x resolve · + react · enter go to the code"},
		}},
		{"Actions (5, or a repo's Actions)", []hint{
			{"enter", "logs: jobs and steps are headings, opens at the first error"},
			{"R", "re-run (only the failed jobs if it failed)"},
			{"x", "cancel"},
			{"w", "start a workflow by hand"},
			{"f", "all / failed / running"},
		}},
		{"Releases (a repo's Releases)", []hint{
			{"enter  v", "read the notes"},
			{"d", "download its files"},
			{"N", "new release: tag, title, notes"},
		}},
		{"Inbox (6)", []hint{
			{"enter", "open it in TermGit (marked read)"},
			{"v", "read the conversation full screen"},
			{"m  M", "mark read / mark all read"},
			{"x", "unsubscribe"},
			{"a", "unread / all"},
		}},
		{"Explore (7)", []hint{
			{"/", "search GitHub (language:go stars:>500 user:x repo:owner/name …)"},
			{"f  [  ]", "Starred / Repos / Issues / Code / Users"},
			{"enter", "open the repo (an issue in its repo, a code hit at its file, a user's profile)"},
			{"s  F  c", "star / fork / clone"},
			{"u", "on a repo: its owner's profile"},
			{"F", "on a profile: follow / unfollow"},
			{"esc", "back: profile → results → clear the search → Starred"},
		}},
		{"Full screen (reader)", []hint{
			{"j / k  space", "scroll a line / a page"},
			{"]  [", "next / previous heading"},
			{"t", "show / hide Contents (click a heading)"},
			{"/  n  N", "find, next / previous match"},
			{"e", "edit in your editor"},
			{"esc  q", "close"},
			{"termgit file.md", "read any file on this PC (reloads when you save it)"},
		}},
		{"Mouse", []hint{
			{"ctrl+click", "a link: GitHub links open in TermGit (repo, file, issue, PR, profile), others in the browser"},
			{"click", "tabs, sections, rows, buttons, crumbs, views, tiles, Contents"},
			{"double-click", "open"},
			{"wheel", "scroll whatever is under the pointer"},
		}},
		{"Settings", []hint{
			{"ctrl+p → Settings", "opens " + tildePath(configPath()) + " in your editor; saved = applied"},
		}},
	}
}

// for the right panel
func helpLines(w int) []string {
	var out []string
	for i, s := range helpData() {
		if i > 0 {
			out = append(out, "")
		}
		out = append(out, fg(cAcc).Bold(true).Render(s.title))
		for _, r := range s.rows {
			out = append(out, "  "+fg(cTxt).Render(fmt.Sprintf("%-18s", r[0]))+fg(cMut).Render(r[1]))
		}
	}
	return append(out, "", fg(cMut).Render("Full screen with Contents: press ? again outside a panel, or ctrl+p → All keys"))
}

// for the full-screen help: one heading per screen, a table of keys
func helpMarkdown() string {
	var b strings.Builder
	b.WriteString("# TermGit keys\n\nEvery screen also lists its keys in the bar at the bottom.\n\n")
	for _, s := range helpData() {
		fmt.Fprintf(&b, "## %s\n\n| Key | Does |\n|---|---|\n", s.title)
		for _, r := range s.rows {
			fmt.Fprintf(&b, "| `%s` | %s |\n", strings.ReplaceAll(r[0], "|", "\\|"), strings.ReplaceAll(r[1], "|", "\\|"))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func (m *model) openHelp() {
	md := helpMarkdown()
	m.openReader(&reader{title: "All keys", sub: "TermGit " + version, icon: icRead,
		mdSrc: func(m *model) (string, bool) { return md, true }})
	m.rd.outline = true
}
