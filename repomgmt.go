package main

// Repo management (Repos tab): create, rename, change visibility, edit the
// description, archive, delete (you type the name to confirm).

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type repoDoneMsg struct {
	ok  string
	err error
}

func (m *model) ghRepo(ok string, args ...string) tea.Cmd {
	if m.dump {
		return m.setStatus("Would run gh "+strings.Join(args, " "), false)
	}
	return tea.Batch(m.setStatus("Working…", false), func() tea.Msg {
		out, err := exec.Command("gh", args...).CombinedOutput()
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if strings.Contains(msg, "delete_repo") {
				return scopeNeededMsg{"delete_repo", "delete repos", ok, args}
			}
			err = fmt.Errorf("%s", msg)
		}
		return repoDoneMsg{ok, err}
	})
}

func (m *model) onRepoDone(msg repoDoneMsg) tea.Cmd {
	if msg.err != nil {
		return m.setStatus(msg.err.Error(), true)
	}
	m.loading = true
	m.local = scanLocal(localRoots)
	return tea.Batch(m.setStatus(msg.ok, false), fetchRepos(), m.scanLocalInfo())
}

// N: name → private / public → description → empty, empty + clone, or upload a folder
func (m *model) repoNew() tea.Cmd {
	m.prompt = &prompt{label: "New repo name:", done: func(m *model, name string) tea.Cmd {
		name = strings.ReplaceAll(strings.TrimSpace(name), " ", "-")
		if name == "" {
			return nil
		}
		m.prompt = &prompt{label: "Make " + name + " private? (n = public)", confirm: true,
			done: func(m *model, _ string) tea.Cmd { return m.repoNewDesc(name, "--private") },
			no:   func(m *model) tea.Cmd { return m.repoNewDesc(name, "--public") }}
		return nil
	}}
	return nil
}

func (m *model) repoNewDesc(name, vis string) tea.Cmd {
	m.prompt = &prompt{label: "Description (optional):", done: func(m *model, desc string) tea.Cmd {
		base := []string{"repo", "create", name, vis}
		if desc != "" {
			base = append(base, "--description", desc)
		}
		dest := filepath.Join(cloneRoot, name)
		m.openPicker("Create "+name, []command{
			{"Empty repo on GitHub", "", func(m *model) tea.Cmd { return m.ghRepo("Created "+name, base...) }},
			{"Empty repo, and clone it into " + tildePath(dest), "", func(m *model) tea.Cmd {
				if m.dump {
					return m.setStatus("Would create and clone "+name, false)
				}
				return tea.Batch(m.setStatus("Creating…", false), func() tea.Msg {
					args := append(append([]string{}, base...), "--add-readme")
					out, err := exec.Command("gh", args...).CombinedOutput()
					if err == nil {
						var o2 []byte
						o2, err = exec.Command("gh", "repo", "clone", name, dest).CombinedOutput()
						out = append(out, o2...)
					}
					if err != nil {
						err = fmt.Errorf("%s", strings.TrimSpace(string(out)))
					}
					return repoDoneMsg{"Created " + name + " and cloned it into " + tildePath(dest), err}
				})
			}},
			{"Upload a folder from this PC (it becomes the repo)", "", func(m *model) tea.Cmd {
				m.prompt = &prompt{label: "Folder:", text: dest, done: func(m *model, dir string) tea.Cmd {
					if dir == "" {
						return nil
					}
					if _, err := os.Stat(dir); err != nil {
						return m.setStatus("No such folder: "+dir, true)
					}
					if _, err := os.Stat(dir + string(os.PathSeparator) + ".git"); err != nil {
						// not a git repo yet: make it one with a first commit
						if _, err := gitRun(dir, "init", "-b", "main"); err == nil {
							gitRun(dir, "add", "-A")
							gitRun(dir, "commit", "-m", "First commit")
						}
					}
					args := append(append([]string{}, base...), "--source", dir, "--push")
					return m.ghRepo("Created "+name+" from "+tildePath(dir), args...)
				}}
				return nil
			}},
		})
		return nil
	}}
	return nil
}

func (m *model) repoMgmtKey(s string) (tea.Cmd, bool) {
	if s == "N" {
		return m.repoNew(), true
	}
	r := m.selected()
	if r == nil || m.ex != nil {
		return nil, false
	}
	nwo := r.NameWithOwner
	mine := strings.EqualFold(strings.SplitN(nwo, "/", 2)[0], m.login)
	switch s {
	case "R":
		m.prompt = &prompt{label: "Rename " + nwo + " to:", text: r.Name, done: func(m *model, nn string) tea.Cmd {
			if nn == "" || nn == r.Name {
				return nil
			}
			cmd := m.ghRepo("Renamed to "+nn, "repo", "rename", nn, "-R", nwo, "--yes")
			if dir := m.localPath(r); dir != "" { // keep your copy pointing at the new name
				owner := strings.SplitN(nwo, "/", 2)[0]
				gitRun(dir, "remote", "set-url", "origin", "https://github.com/"+owner+"/"+nn+".git")
			}
			return cmd
		}}
		return nil, true
	case "V":
		to := "private"
		if r.IsPrivate {
			to = "public"
		}
		warn := ""
		if to == "public" {
			warn = " Everyone will be able to see its code."
		} else {
			warn = " Stars and watchers from others will be lost."
		}
		m.prompt = &prompt{label: "Make " + nwo + " " + to + "?" + warn, confirm: true, done: func(m *model, _ string) tea.Cmd {
			return m.ghRepo(nwo+" is "+to+" now", "repo", "edit", nwo, "--visibility", to, "--accept-visibility-change-consequences")
		}}
		return nil, true
	case "E":
		m.prompt = &prompt{label: "Description of " + nwo + ":", text: r.Description, done: func(m *model, d string) tea.Cmd {
			if d == r.Description {
				return nil
			}
			return m.ghRepo("Description saved", "repo", "edit", nwo, "--description", d)
		}}
		return nil, true
	case "A":
		if r.IsArchived {
			m.prompt = &prompt{label: "Unarchive " + nwo + "? (it becomes editable again)", confirm: true, done: func(m *model, _ string) tea.Cmd {
				return m.ghRepo("Unarchived "+nwo, "repo", "unarchive", nwo, "--yes")
			}}
		} else {
			m.prompt = &prompt{label: "Archive " + nwo + "? (read-only; you can undo it)", confirm: true, done: func(m *model, _ string) tea.Cmd {
				return m.ghRepo("Archived "+nwo, "repo", "archive", nwo, "--yes")
			}}
		}
		return nil, true
	case "D":
		if !mine {
			return m.setStatus("You can only delete your own repos here", true), true
		}
		m.prompt = &prompt{label: "Delete " + nwo + " FOREVER? Type its name (" + r.Name + ") to confirm:", done: func(m *model, typed string) tea.Cmd {
			if typed != r.Name {
				return m.setStatus("Name didn't match: nothing deleted", false)
			}
			return m.ghRepo("Deleted "+nwo+" (your copy on this PC, if any, is untouched)", "repo", "delete", nwo, "--yes")
		}}
		return nil, true
	case "s":
		return m.actStar(r), true
	case "F":
		return m.actFork(r), true
	}
	return nil, false
}

type scopeNeededMsg struct {
	scope, why, ok string
	args           []string
}

func (m *model) onScopeNeeded(msg scopeNeededMsg) tea.Cmd {
	return m.askScope(msg.scope, msg.why, func(m *model) tea.Cmd { return m.ghRepo(msg.ok, msg.args...) })
}
