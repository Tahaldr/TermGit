# TermGit

GitHub in the terminal: usable fully by mouse or fully by keyboard, in muted onedark colors.
Logs in through the GitHub CLI (`gh auth login`); TermGit never stores a token.

## Status

| Batch | What | State |
| --- | --- | --- |
| 1 | Base: layout, tabs, mouse, command palette, bottom-bar prompts, Repos list + README | done |
| 2 | Repo explorer: code, commits, branches, Nvim, lazygit, local badges | done |
| 3 | Markdown reader: Nvim-style markdown, full-screen reader, Contents, `termgit file.md` | done |
| 4 | Actions + Releases | done |
| 5 | Issues | done |
| 6 | Pull requests | done |
| 7 | Local Git 1: Changes (stage, commit, amend, pull, push, fetch) + PR Comments (reply, resolve, react) | done |
| 8 | Local Git 2: branches, stash, graph, cherry-pick, revert, reset, tags, line staging | done |
| 9 | Local Git 3: conflicts, interactive rebase, remotes, worktrees | next |
| 10 | Home + Inbox | |
| 11 | Explore + repo management | |
| 12 | Polish: settings, help page, tests | |

TermGit aims to do everything lazygit does; lazygit stays one key away (`L`).

## Keys

| Key | Does |
| --- | --- |
| `1`–`7` | switch tab (or click it) |
| `ctrl+p` / `:` | command palette |
| `?` | all keys |
| `j`/`k`, arrows, wheel | move / scroll |
| `enter` / double-click | open the repo (code, commits, branches) |
| `tab` | switch between list and preview (README) |
| `[` / `]` | inside a repo: Code, Commits, Branches |
| `esc` / `h` / backspace | back: up a folder, then to the repo list |
| `e` / `L` | open in Nvim / lazygit (asks to clone first if needed) |
| `p` / `P` | pull (fast-forward only, never merges) / push |
| `/` | filter repos; inside a repo: find a file, commit or branch |
| `ctrl+d` / `ctrl+u` | scroll the right panel |
| `o` / `y` / `c` / `r` | browser / copy link (of what's on screen) / clone / refresh |
| `v` | read full screen (README, file, commit) |
| `esc` | back |
| `q` | quit |

## Changes (local Git)

Open a repo that's on this PC, then the **Changes** section (`]`): every changed file, its diff on the right.
Same keys as lazygit: `space` stage / unstage, `a` all, `d` discard, `c` commit (`C` in Nvim), `A` amend,
`p` pull, `P` push (offers force-with-lease if GitHub has commits you don't), `f` fetch, `e` open in Nvim.

## Branches, history, stash (repos on this PC)

For a repo on this PC, **Branches** and **Commits** show your local Git (GitHub-only branches too, marked with a cloud):

- Branches: `space` switch, `enter` its commits, `n` new, `M` merge into yours, `B` rebase yours onto it, `R` rename, `D` delete, `u` follow a GitHub branch, `P` push a tag, `b` browse its code.
- Commits (with the graph, `↑` = not pushed): `enter` changes, `C` cherry-pick, `t` revert, `X` reset (soft / mixed / hard), `T` tag, `y` copy id, `space` check out.
- Changes: `enter` on a file, then `j` / `k` pick a line and `space` stages just that line (`a` the hunk). `s` stashes your changes, `S` chooses what; stashes sit at the bottom (`enter`: pop / apply / drop / branch).
- When a merge or rebase stops on conflicts: `ctrl+p` → Continue / Abort.

## Pull requests

- **PRs tab** (`4`): PRs in your repos. `f` switches Your repos / Review requests / Created / Involved, `s` open / closed+merged.
- Inside a repo: the **PRs** section.
- **Comments** view: every comment, review and comment on code as a card. Focus the panel (`tab`), `j` / `k` pick one, `r` reply, `x` resolve, `+` react, `enter` jump to the code.
- Views on the right: Overview, Comments, Commits, Checks, Files: `[` / `]` (in a repo `{` / `}`), or click them.
- `enter` shows the view full screen (Files: every file is a heading; Checks: the failing check's logs).
- `m` merge (squash / merge commit / rebase, keep or delete the branch), `R` review, `C` comment, `x` close / reopen, `K` check out locally, `N` new PR.

## Issues

- **Issues tab** (`3`): issues in your repos (and the shared / org repos in your list). `f` switches Your repos / Involved / Assigned / Created, `s` open / closed.
- Inside a repo: the **Issues** section.
- `enter` reads the whole conversation full screen (each message is a heading in Contents).
- `C` comment, `N` new issue, `E` edit the description: written in Nvim (save and quit, then confirm).
- `x` close / reopen, `t` labels (pick one to add or remove it).

## Back (esc)

`esc` always goes one step back along the path at the top: preview → list, search → cleared,
a section (Issues, Commits…) → the repo's Code, a folder → its parent, the repo → the repo list.

## Actions and Releases

- **Actions tab** (`5`): CI runs of your recent repos. Running ones first, then the ones still broken, then the rest. `f` switches All / Failed / Running.
- Inside a repo: the **Actions** and **Releases** sections (`[` / `]`).
- `enter` on a run opens its logs in the reader: jobs and steps are headings, it opens at the first error.
- `R` re-run (failed jobs only if it failed), `x` cancel, `w` start a workflow by hand.
- Running runs update by themselves every 5 seconds.
- Releases: `enter` reads the notes, `d` downloads files to Downloads, `N` makes a new release (notes written in Nvim or made from the commits).

## Reader

Markdown is drawn like render-markdown.nvim: heading bars with icons, `□` / `■` checkboxes
(done ones faded), code blocks with the language on top, tables, quotes and GitHub callouts.

- `termgit notes.md` opens any file on this PC full screen; it reloads when you save the file.
- `]` / `[` next / previous heading, `t` Contents panel (click a heading), `/` find, `n` / `N` matches, `e` edit in Nvim.

## Build

```
go build -o termgit.exe .
copy termgit.exe %USERPROFILE%\.local\bin\
```

Test without a terminal: `termgit --dump 140x40 j enter click:10,5` prints one screen.

Badges on repos cloned on this PC: folder = cloned, `●` uncommitted changes, `↑2` not pushed, `↓3` new commits on GitHub.

Data is cached in `%LOCALAPPDATA%\termgit`.
