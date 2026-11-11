package main

// Quick setup: what GitHub shows on a new, empty repo — its HTTPS / SSH addresses and the
// commands to start a project in it or push one you have — plus TermGit doing those steps for you.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
)

func httpsURL(nwo string) string { return "https://github.com/" + nwo + ".git" }
func sshURL(nwo string) string   { return "git@github.com:" + nwo + ".git" }

// isEmptyRepo: the repo has no commits yet (nothing to browse)
func (m *model) isEmptyRepo() bool {
	ex := m.exActive()
	if ex == nil {
		return false
	}
	if ex.ref == "" {
		return true
	}
	td := m.trees[m.treeKey()]
	return td != nil && td.err == "This repo is empty."
}

func quickSetupMD(nwo string) string {
	name := shortRepo(nwo)
	return fmt.Sprintf(`# Quick setup

This repo is empty. Get started by cloning it, or by pushing a project you already have.

| Use | Address |
|---|---|
| **HTTPS** | %s |
| **SSH** | %s |
| **GitHub CLI** | gh repo clone %s |

> [!TIP]
> TermGit can do the steps below for you: **U** pushes a folder from this PC to this repo
> (it sets git up if the folder isn't a repo yet), **c** clones the empty repo into a folder you pick.

## Start a new project on the command line

%s
echo "# %s" >> README.md
git init
git add README.md
git commit -m "first commit"
git branch -M main
git remote add origin %s
git push -u origin main
%s

## Push an existing project from the command line

%s
git remote add origin %s
git branch -M main
git push -u origin main
%s

## Import code from another repository

Initialize this repo with code from a Subversion, Mercurial or TFS project on
[GitHub's importer](https://github.com/%s/import).
`, httpsURL(nwo), sshURL(nwo), nwo, "```sh", name, httpsURL(nwo), "```", "```sh", httpsURL(nwo), "```", nwo)
}

func (m *model) quickSetupLines(nwo string, w int) []string {
	md := quickSetupMD(nwo)
	head := hints([][2]string{{"y", "copy HTTPS"}, {"Y", "copy SSH"}, {"U", "push a folder here"}, {"c", "clone"}, {"o", "browser"}})
	return append([]string{head, ""}, m.cachedLines(fmt.Sprintf("qs:%s:%d", nwo, w), func() []string { return renderMarkdown(md, w) })...)
}

type pushedMsg struct {
	nwo, dir string
	out      string
	err      error
}

// U: push a folder from this PC into the empty repo (git init + first commit if needed, remote, push)
func (m *model) pushFolder() tea.Cmd {
	nwo := m.ex.repo.NameWithOwner
	def := filepath.Join(cloneRoot, shortRepo(nwo))
	m.prompt = &prompt{label: "Push which folder to " + nwo + "? (a new folder is created with a README)", text: def, done: func(m *model, dir string) tea.Cmd {
		dir = expandHome(dir)
		if st, err := os.Stat(dir); err == nil && !st.IsDir() {
			return m.setStatus(dir+" is a file, not a folder", true)
		}
		m.prompt = &prompt{label: "Push " + tildePath(dir) + " to " + nwo + " (branch main)? If it isn't a git repo yet, it becomes one with a first commit of everything in it.", confirm: true,
			done: func(m *model, _ string) tea.Cmd {
				if m.dump {
					return m.setStatus("Would push "+dir+" to "+nwo, false)
				}
				return tea.Batch(m.setStatus("Pushing…", false), func() tea.Msg {
					if err := os.MkdirAll(dir, 0o755); err != nil {
						return pushedMsg{nwo, dir, "", err}
					}
					var log strings.Builder
					step := func(args ...string) error {
						out, err := gitRun(dir, args...)
						log.WriteString(out)
						if err != nil {
							return fmt.Errorf("git %s: %s", strings.Join(args, " "), lastLine(out))
						}
						return nil
					}
					if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
						if err := step("init", "-b", "main"); err != nil {
							return pushedMsg{nwo, dir, log.String(), err}
						}
					}
					if out, _ := gitRun(dir, "rev-parse", "--verify", "HEAD"); strings.TrimSpace(out) == "" || strings.Contains(out, "fatal") {
						// no commit yet: commit everything (a README if the folder is empty)
						if entries, _ := os.ReadDir(dir); len(entries) <= 1 {
							os.WriteFile(filepath.Join(dir, "README.md"), []byte("# "+shortRepo(nwo)+"\n"), 0o644)
						}
						step("add", "-A")
						if err := step("commit", "-m", "first commit"); err != nil {
							return pushedMsg{nwo, dir, log.String(), err}
						}
					}
					if _, err := gitRun(dir, "remote", "get-url", "origin"); err == nil {
						step("remote", "set-url", "origin", httpsURL(nwo))
					} else if err := step("remote", "add", "origin", httpsURL(nwo)); err != nil {
						return pushedMsg{nwo, dir, log.String(), err}
					}
					step("branch", "-M", "main")
					err := step("push", "-u", "origin", "main")
					return pushedMsg{nwo, dir, log.String(), err}
				})
			}}
		return nil
	}}
	return nil
}

func (m *model) onPushed(msg pushedMsg) tea.Cmd {
	if msg.err != nil {
		return m.setStatus("Couldn't push: "+msg.err.Error(), true)
	}
	m.local = scanLocal(localRoots)
	m.local[strings.ToLower(msg.nwo)] = msg.dir
	// the repo isn't empty anymore: read it again
	for k := range m.loaded {
		if strings.HasPrefix(k, "tree:"+msg.nwo) || k == "branches:"+msg.nwo {
			delete(m.loaded, k)
		}
	}
	if m.ex != nil && m.ex.repo.NameWithOwner == msg.nwo && m.ex.ref == "" {
		m.ex.ref = "main"
	}
	m.loading = true
	return tea.Batch(m.setStatus("Pushed "+tildePath(msg.dir)+" to "+msg.nwo, false), fetchRepos(), m.scanLocalInfo(), m.exLoad())
}

type createdRepoMsg struct {
	nwo     string
	private bool
	err     error
}

// the new empty repo opens right away, on its Quick setup
func (m *model) onCreatedRepo(msg createdRepoMsg) tea.Cmd {
	if msg.err != nil {
		return m.setStatus(msg.err.Error(), true)
	}
	m.loading = true
	r := Repo{NameWithOwner: msg.nwo, Name: shortRepo(msg.nwo), URL: "https://github.com/" + msg.nwo, IsPrivate: msg.private}
	cmd := m.openRepoObj(r)
	m.backTo = -1
	return tea.Batch(m.setStatus("Created "+msg.nwo+": here's how to start", false), fetchRepos(), cmd)
}
