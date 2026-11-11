# TermGit

**GitHub and Git in one terminal app.** Browse your repos and code, stage and commit, rebase,
review and merge pull requests, read CI logs, answer issues, clear your notifications and search
all of GitHub — without leaving the terminal and without remembering commands.

- Works **fully by keyboard or fully by mouse**: every action has a key *and* a clickable button.
- The keys for whatever you're looking at are **always shown at the bottom** (`?` lists all of them).
- Questions and inputs appear in **popups** (like Nvim's command line); anything risky asks first.
- Does everything **lazygit** does for repos on your computer, and everything you'd normally open
  **github.com** for.
- One small program. Logs in through the official GitHub CLI and **never stores a token**.

```
 1 󰋜 Home   2  Repos   3  Issues   4  PRs   5  Actions   6  Inbox 3   7  Explore
 Repos › you/project ›  main › Changes                                     ⠋ loading…
╭─  Code   Changes   Issues   PRs  …  ───────╮╭─ app.go ──────────────────────────────────╮
│ M  app.go                                     ││ app.go · not staged                       │
│  ? notes.md                                   ││ space stage · d discard · c commit        │
│ M  ui/view.go                                 ││ ────────────────────────────────────────  │
│                                               ││ @@ -12,6 +12,7 @@ func main() {           │
│                                               ││ +    loadConfig()                         │
╰─────────────────────────  main ↑1 · 0 staged ─╯╰───────────────────────────────────────────╯
  Stage space   Discard d   Commit c   Pull p   Push P
 space stage/unstage · enter pick lines · d discard · c commit · s stash · p pull · P push · ? all keys
```

## Contents

- [Install](#install)
- [First start](#first-start)
- [What you can do](#what-you-can-do)
- [The command line](#the-command-line)
- [Updating](#updating)
- [Keys](#keys)
- [Settings](#settings)
- [FAQ](#faq)
- [Building from source](#building-from-source)

## Install

You need:

| What | Why | Get it |
| --- | --- | --- |
| **GitHub CLI** (`gh`) | TermGit logs in through it | Windows `winget install --id GitHub.cli` · macOS `brew install gh` · [Linux](https://github.com/cli/cli#installation) |
| **git** | for repos on your computer | [git-scm.com](https://git-scm.com/downloads) |
| a **Nerd Font** in your terminal | the icons | [nerdfonts.com](https://www.nerdfonts.com) (e.g. JetBrainsMono Nerd Font) |
| an editor *(optional)* | writing comments, commit messages | Nvim by default, any editor works ([settings](#settings)) |

Then get TermGit:

- **Download** the program for your system from the [Releases](../../releases) page
  (`termgit_windows_amd64.exe`, `termgit_darwin_arm64`, `termgit_linux_amd64`, …), rename it to
  `termgit` (`termgit.exe` on Windows) and put it in a folder on your `PATH`.
- **Or with Go** (1.27+): `go install github.com/Tahaldr/TermGit/v2@latest`

## First start

```
termgit login     # logs you in to GitHub with the permissions TermGit uses (a browser page opens)
termgit doctor    # checks that everything is in place
termgit           # start
```

If you run `termgit` without being logged in, it offers to log you in. TermGit uses the same login
as `gh`, so if you already use the GitHub CLI there's nothing to do. `GH_TOKEN` / `GITHUB_TOKEN`
environment variables work too.

## What you can do

**Home** — a dashboard: your contribution graph and streak, and **Needs you**: failing CI, reviews
asked of you, your open PRs, issues assigned to you, repos on your computer with changes to commit,
push or pull, and unread notifications. Quick keys act right there (`R` re-run, `m` merge, `c` commit,
`p` / `P` pull / push). `enter` opens the item's page.

**Repos** — all your repos (yours, shared, organizations), with badges for the ones cloned on your
computer: changes not committed `●`, commits to push `↑`, commits to pull `↓`. Create, rename,
describe, change visibility, archive or delete a repo; star, fork, clone.

**Inside a repo** — sections you switch with `[` `]`:

| Section | |
| --- | --- |
| **Code** | browse folders, read files with syntax colors, markdown rendered; find any file with `/` |
| **Changes** | every changed file and its diff; stage files or **single lines**; discard; commit (message in a popup or your editor); amend; stash; pull, push, fetch; fix **merge conflicts** (keep top / bottom / both) |
| **Issues** | read conversations, comment, create, close / reopen, labels, edit |
| **PRs** | Overview · **Comments** (reply, resolve, react) · Commits · Checks · Files; merge (squash / merge / rebase), review, check out locally, create |
| **Commits** | the history with a graph; **interactive rebase** (pick / squash / fixup / reword / drop / reorder), cherry-pick, revert, reset, tag |
| **Branches** | switch, create, rename, delete, merge, rebase, follow a GitHub branch; tags; remotes; worktrees |
| **Actions** | CI runs live; logs with jobs and steps as headings, opening at the first error; re-run, cancel, start a workflow |
| **Releases** | read notes, download files, publish a release |

**Issues / PRs / Actions tabs** — the same, across all your repos.

**Inbox** — your notifications; `enter` opens the issue, PR or CI run inside TermGit and marks it read.

**Explore** — your stars, or search all of GitHub for **repos, issues, code or users** (GitHub's search
words work: `language:go stars:>500`, `user:name`, …). Open any repo, see anyone's profile and repos,
follow people.

**Reader** — full screen (`v`) for READMEs, files, conversations and logs, with a Contents panel to jump
between headings and `/` to find. `termgit notes.md` reads any file on your computer and reloads it
when you save.

**Links** — ctrl+click any link: GitHub links open inside TermGit (repos, files, issues, PRs, profiles),
others in your browser.

## The command line

```
termgit                    open TermGit
termgit TAB                open on a tab: home, repos, issues, prs, actions, inbox, explore
termgit .                  open the GitHub repo of this folder (its Changes if it has some)
termgit OWNER/REPO         open a repo, e.g. termgit charmbracelet/bubbletea
termgit FILE               read a file full screen
termgit search QUERY       search GitHub repos
termgit user LOGIN         open someone's profile

termgit login              log in (adds the permissions TermGit uses if you're already logged in)
termgit logout             log out
termgit update             update to the newest version (--check only checks)
termgit doctor             check your setup
termgit config             open the settings in your editor
termgit config path        print where the settings file is
termgit version            print the version
termgit help               all of this (-h, --help)
```

## Updating

```
termgit update            # downloads the newest version for your system and replaces the program
termgit update --check    # only tells you if there's a newer one
```

TermGit also checks once a day and shows a green arrow with the new version at the top right — click
it to update. If you installed with Go, `go install github.com/Tahaldr/TermGit/v2@latest` works too.

**Your settings and data are never touched by an update**: they live in their own folders (see
[Settings](#settings)), apart from the program.

## Keys

You never need to learn these: the bar at the bottom always shows the keys for the current screen and
the highlighted item, `?` shows all of them (with search), and `ctrl+p` opens a command palette where
you type what you want to do.

| Key | Does |
| --- | --- |
| `1` – `7` | tabs |
| `j` `k` / arrows / mouse wheel | move, scroll |
| `enter` / double-click | open |
| `tab` `h` `l` | list ↔ right panel |
| `esc` | one step back |
| `/` | filter, find, search |
| `v` | full screen |
| `e` / `L` | open in your editor / git tool |
| `o` / `y` | open in the browser / copy the link |
| `r` | refresh |
| `ctrl+p` | command palette |
| `?` | every key |
| `q` | quit |

## Settings

`termgit config` (or `ctrl+p` → Settings) opens the settings file; it's applied as soon as you save.

```
start_tab = home          # home, repos, issues, prs, actions, inbox, explore
home_layout = dashboard   # or panels (t on Home switches)
clone_into = ~/code       # where repos get cloned
look_in = ~/code ; ~/projects ; ~/src ; ~/repos ; ~/Documents/GitHub   # where your clones are found
editor = nvim             # e.g.  code -w  ·  vim  ·  hx  ·  notepad
git_tool = lazygit        # what L opens
notify_every = 60         # seconds between notification checks
```

The file is `%APPDATA%\termgit\config.txt` on Windows, `~/.config/termgit/config.txt` on Linux and
`~/Library/Application Support/termgit/config.txt` on macOS. GitHub data is cached so TermGit opens
instantly and refreshes in the background.

## FAQ

**Is my token safe?** TermGit never sees or saves it; the GitHub CLI keeps it in your system's
credential store and TermGit asks it for each request.

**Why does it ask for extra permissions?** Deleting repos (`delete_repo`) and following people
(`user:follow`) need permissions a default `gh` login doesn't have. TermGit asks the first time you use
them, or run `termgit login` once.

**The icons are boxes / question marks.** Pick a Nerd Font in your terminal settings.

**Do I still need lazygit?** No. Everything is in TermGit; `L` opens lazygit (or the git tool you set)
if you like to have it.

**My clones don't show the folder badge.** Add the folder that holds them to `look_in` in the settings.

**Something broke.** If TermGit stops on an error, the message stays on screen and is saved to
`crash.log` in its cache folder (`termgit doctor` shows where). Please open an issue with it.

## Building from source

```
git clone https://github.com/Tahaldr/TermGit
cd TermGit
go build -o termgit .          # termgit.exe on Windows
go test ./...
```

`termgit --dump 140x40 2 enter` runs TermGit without a terminal and prints one screen (keys,
`click:x,y`, `ctrlclick:x,y`, `wheel:x,y`) — handy for tests. Pushing a `v*` tag builds the release
programs for every system (`.github/workflows/release.yml`).

Built with [Bubble Tea](https://github.com/charmbracelet/bubbletea), [Lip Gloss](https://github.com/charmbracelet/lipgloss),
[go-gh](https://github.com/cli/go-gh), [goldmark](https://github.com/yuin/goldmark) and
[Chroma](https://github.com/alecthomas/chroma). MIT license.
