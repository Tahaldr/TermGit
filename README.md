# TermGit

GitHub in the terminal: usable fully by mouse or fully by keyboard, in muted onedark colors.
Logs in through the GitHub CLI (`gh auth login`); TermGit never stores a token.

## Status

| Batch | What | State |
| --- | --- | --- |
| 1 | Base: layout, tabs, mouse, command palette, bottom-bar prompts, Repos list + README | done |
| 2 | Repo explorer: code, commits, branches, Nvim, lazygit, local badges | next |
| 3 | Actions + Releases | |
| 4 | Issues | |
| 5 | PRs | |
| 6 | Home + Inbox | |
| 7 | Explore + repo management | |
| 8 | Polish: settings, help page, tests | |

## Keys

| Key | Does |
| --- | --- |
| `1`–`7` | switch tab (or click it) |
| `ctrl+p` / `:` | command palette |
| `?` | all keys |
| `j`/`k`, arrows, wheel | move / scroll |
| `enter` / double-click | read the README |
| `tab` | switch between list and preview |
| `/` | filter |
| `ctrl+d` / `ctrl+u` | scroll the right panel |
| `o` / `y` / `c` / `r` | browser / copy link / clone / refresh |
| `esc` | back |
| `q` | quit |

## Build

```
go build -o termgit.exe .
copy termgit.exe %USERPROFILE%\.local\bin\
```

Test without a terminal: `termgit --dump 140x40 j enter click:10,5` prints one screen.

Data is cached in `%LOCALAPPDATA%\termgit`.
