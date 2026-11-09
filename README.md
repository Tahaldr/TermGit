# TermGit

GitHub and Git in one terminal app: your repos, code, changes, branches, history, issues,
pull requests, CI, releases, notifications and GitHub search. Every action has a key and a
clickable button, and the keys for what you're looking at are always in the bar at the bottom.
Muted onedark colors, Nerd Font icons.

TermGit does everything lazygit does for your repos on this PC; lazygit stays one key away (`L`).

- [Install](#install)
- [The tabs](#the-tabs)
- [Inside a repo](#inside-a-repo)
- [Git on this PC](#git-on-this-pc)
- [Issues and pull requests](#issues-and-pull-requests)
- [Actions and releases](#actions-and-releases)
- [Reader](#reader)
- [Keys](#keys)
- [Settings](#settings)
- [Build and test](#build-and-test)

## Install

1. Install the [GitHub CLI](https://cli.github.com) and log in once: `gh auth login`.
   TermGit uses that login and never stores a token.
2. Install a [Nerd Font](https://www.nerdfonts.com) and pick it in your terminal (for the icons).
3. Build TermGit (see [Build](#build-and-test)) and put `termgit.exe` on your PATH.
4. Run `termgit`. Optional: Nvim and lazygit (both can be changed in the [settings](#settings)).

## The tabs

| Tab | What's there |
| --- | --- |
| **1 Home** | Number tiles (contributions, streak, this week, your PRs, CI), your contribution graph, **Needs you** (failing CI, reviews asked of you, your PRs, assigned issues, repos on this PC with work to commit / push / pull, unread notifications, recent repos) and your activity by day. Rows have quick keys: `R` re-run / review, `m` merge, `c` `p` `P` commit / pull / push, `M` mark all read, `alt+1–5` recent repos. `t` switches to a two-panel layout. |
| **2 Repos** | Your repos (yours, shared and org ones) with badges for the ones on this PC: folder, `●` changes, `↑` to push, `↓` to pull. README preview. `N` new repo, `R` rename, `E` description, `V` visibility, `A` archive, `D` delete (type the name), `c` clone, `s` star, `F` fork. |
| **3 Issues** | Issues across your repos: your repos / involved / assigned / created (`f`), open or closed (`s`). |
| **4 PRs** | Pull requests across your repos: your repos / review requests / created / involved. |
| **5 Actions** | CI runs: running first, then still-broken ones, then the rest (`f`: all / failed / running). |
| **6 Inbox** | Notifications (the tab shows the unread count). `enter` opens the item's page in TermGit and marks it read, `v` reads it full screen, `m` / `M` mark read, `x` unsubscribe. |
| **7 Explore** | Your starred repos, or search all of GitHub for repos, issues or code (`/`, with GitHub's search words like `language:go stars:>500`). Open any repo, star, fork, clone. |

From Home and the Inbox, `enter` jumps to the item's normal page (its repo, section, item highlighted); `esc` comes back.

## Inside a repo

`enter` on a repo opens it. Sections (`[` `]` or click): **Code · Changes · Issues · PRs · Commits · Branches · Actions · Releases**.

- **Code**: folders and files with syntax colors, markdown rendered; `/` finds a file anywhere; `e` opens it in your editor.
- The path at the top is clickable, and `esc` goes one step back along it.

## Git on this PC

For repos cloned on this PC the sections show your local Git (same keys as lazygit):

- **Changes**: `space` stage / unstage, `a` all, `enter` then `j`/`k` + `space` stages single lines (`a` the hunk), `d` discard, `c` commit (`C` in your editor), `A` amend, `s` / `S` stash (stashes are listed at the bottom), `p` pull (fast-forward only), `P` push (first push links the branch; offers force-with-lease when needed), `f` fetch.
- **Conflicts** (files marked `U`): `enter`, then `o` keeps the top, `t` the bottom, `b` both; `O` / `T` for the whole file; then Continue or Abort (buttons, or `ctrl+p`).
- **Commits**: your history with the graph (`↑` = not pushed). `i` interactive rebase from a commit (pick / squash / fixup / reword / drop / move, nothing runs until `enter`), `S` `F` `R` `D` quick squash / fixup / reword / drop, `C` cherry-pick, `t` revert, `X` reset (soft / mixed / hard), `T` tag, `y` copy id.
- **Branches**: local branches, GitHub-only ones (cloud icon), tags, remotes and worktrees. `space` switch, `n` new, `M` merge into yours, `B` rebase yours onto it, `R` rename, `D` delete, `u` follow a GitHub branch, `w` worktree, `A` add a remote.

Anything that can't be undone asks first, in the bottom bar.

## Issues and pull requests

- Issues: `enter` reads the conversation full screen, `C` comment, `N` new, `x` close / reopen, `t` labels, `E` edit. Text is written in your editor: save and quit, then confirm.
- PRs: views **Overview · Comments · Commits · Checks · Files** (`[` `]`, or `{` `}` inside a repo). `m` merge (squash / merge / rebase, keep or delete the branch), `R` review (approve / request changes / comment), `C` comment, `K` check out on this PC, `x` close / reopen, `N` new PR.
- The **Comments** view has every comment, review and comment on code as a card: `tab` into it, `j`/`k` pick, `r` reply, `x` resolve, `+` react, `enter` jump to the code.

## Actions and releases

- Runs: jobs and steps on the right (running runs update every 5 seconds). `enter` opens the logs with jobs and steps as headings, at the first error. `R` re-run (failed jobs only if it failed), `x` cancel, `w` start a workflow by hand.
- Releases: `enter` reads the notes, `d` downloads its files, `N` new release (notes in your editor or made from the commits).

## Reader

Full screen (`v`, or `enter` on markdown): markdown drawn like render-markdown.nvim (heading bars,
`□` / `■` checkboxes, code blocks, tables, callouts), a **Contents** panel (`t`, click to jump),
`]` / `[` next / previous heading, `/` find, `e` edit. `termgit notes.md` opens any file on this PC
and reloads it when you save it.

## Keys

`?` shows every key, by screen, full screen with Contents. The basics:

| Key | Does |
| --- | --- |
| `1`–`7` | tabs (or click) |
| `ctrl+p` / `:` | command palette: type what you want to do |
| `j` `k`, arrows, wheel | move / scroll |
| `enter`, double-click | open |
| `tab` `h` `l` | list ↔ right panel |
| `esc` | back one step |
| `v` | full screen |
| `/` | filter / find / search |
| `o` / `y` | browser / copy the link |
| `r` | refresh |
| `q` | quit |

## Settings

`ctrl+p → Settings` opens the settings file in your editor; saving applies it. It lives at
`%APPDATA%\termgit\config.txt` (`termgit --config` prints the path):

```
start_tab = home          # home, repos, issues, prs, actions, inbox, explore
home_layout = dashboard   # or panels
clone_into = ~\Documents\Coding
look_in = ~\Documents\Coding ; ~\apps    # where your clones are found
editor = nvim             # e.g. code -w
git_tool = lazygit        # the L key
notify_every = 60         # seconds
```

Data from GitHub is cached in `%LOCALAPPDATA%\termgit`, so TermGit opens instantly and refreshes in the background.

## Build and test

```
go build -o termgit.exe .
copy termgit.exe %USERPROFILE%\.local\bin\
go test ./...
```

`termgit --dump 140x40 2 enter click:10,5` runs TermGit without a terminal and prints one screen (keys, `click:x,y`, `wheel:x,y`).
Every batch is tagged (`batch-1` … `batch-12`), so any of them can be rolled back.
