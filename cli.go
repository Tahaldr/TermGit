package main

// The termgit command: what it can open, login, doctor, config, help.

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/cli/go-gh/v2/pkg/auth"
)

const usage = `TermGit %s — GitHub and Git in your terminal.

Usage:
  termgit                    open TermGit (on the start tab from your settings)
  termgit TAB                open on a tab: home, repos, issues, prs, actions, inbox, explore
  termgit .                  open the GitHub repo of this folder (its Changes if it has some)
  termgit OWNER/REPO         open a repo, e.g. termgit charmbracelet/bubbletea
  termgit FILE               read a file full screen (markdown or code); reloads when it changes
  termgit search QUERY       search GitHub repos (GitHub's search words work)
  termgit user LOGIN         open someone's profile

  termgit login              log in to GitHub (through the GitHub CLI) with what TermGit needs
  termgit logout             log out
  termgit doctor             check that everything TermGit uses is set up
  termgit config             open the settings file in your editor
  termgit config path        print where the settings file is
  termgit version            print the version
  termgit help               this help (also -h, --help)

Inside TermGit press ? for every key, ctrl+p for the command palette.
`

// scopes TermGit uses: repo (code, issues, PRs), read:org (org repos), workflow (CI, pushing
// workflow files), delete_repo (D on a repo), user:follow (F on a profile), notifications
var loginScopes = "repo,read:org,workflow,delete_repo,user:follow,notifications,gist"

var nwoRe = regexp.MustCompile(`^[A-Za-z0-9-]+/[A-Za-z0-9._-]+$`)

func runCLI(args []string) {
	if len(args) == 0 {
		start(nil)
		return
	}
	a := strings.ToLower(args[0])
	switch a {
	case "-h", "--help", "help":
		fmt.Printf(usage, version)
	case "-v", "--version", "version":
		fmt.Println("termgit", version)
	case "--dump":
		dump(args[1:])
	case "login":
		login()
	case "logout":
		run("gh", "auth", "logout", "-h", "github.com")
	case "doctor":
		doctor()
	case "config", "--config":
		if len(args) > 1 && args[1] == "path" || a == "--config" {
			fmt.Println(configPath())
			return
		}
		if _, err := os.Stat(configPath()); err != nil {
			writeDefaultConfig(configPath(), cfg)
		}
		c := editorCmd(configPath())
		c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := c.Run(); err != nil {
			fail(err)
		}
	case "home", "repos", "issues", "prs", "actions", "inbox", "explore":
		cfg.startTab = a
		start(nil)
	case "search":
		q := strings.Join(args[1:], " ")
		start(func(m *model) tea.Cmd {
			m.setTab(tabExplore)
			m.exp.mode, m.exp.query[1] = 1, q
			return m.expSelected()
		})
	case "user":
		if len(args) < 2 {
			fail(fmt.Errorf("which user? termgit user LOGIN"))
		}
		login := args[1]
		start(func(m *model) tea.Cmd { return m.openProfile(login) })
	case ".":
		dir, _ := os.Getwd()
		nwo := remoteOf(dir)
		if nwo == "" {
			fail(fmt.Errorf("this folder isn't a clone of a GitHub repo (no github.com remote)"))
		}
		start(func(m *model) tea.Cmd {
			m.local[strings.ToLower(nwo)] = dir
			sec := secCode
			if out, _ := gitRun(dir, "status", "--porcelain"); strings.TrimSpace(out) != "" {
				sec = secChanges
			}
			return tea.Batch(m.scanLocalInfo(), m.gotoItem(nwo, sec, "", func(exItem) bool { return true }, nil))
		})
	default:
		if nwoRe.MatchString(args[0]) {
			if _, err := os.Stat(args[0]); err != nil { // not a file: a repo
				nwo := args[0]
				start(func(m *model) tea.Cmd { return m.gotoItem(nwo, secCode, "", func(exItem) bool { return true }, nil) })
				return
			}
		}
		if strings.HasPrefix(args[0], "-") {
			fmt.Printf(usage, version)
			os.Exit(2)
		}
		rd, err := loadFileReader(args[0])
		if err != nil {
			fail(err)
		}
		if _, err := tea.NewProgram(newSoloModel(rd)).Run(); err != nil {
			crashed(err)
		}
	}
}

// start runs TermGit; first (if any) runs once the window size is known
func start(first func(m *model) tea.Cmd) {
	if !ready() {
		return
	}
	m := newModel()
	m.applyConfig()
	m.first = first
	if _, err := tea.NewProgram(m).Run(); err != nil {
		crashed(err)
	}
}

func remoteOf(dir string) string {
	out, err := gitRun(dir, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	if m := remoteRe.FindStringSubmatch("url = " + strings.TrimSpace(out)); m != nil {
		return m[1] + "/" + m[2]
	}
	return ""
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "termgit:", err)
	os.Exit(1)
}

func run(name string, args ...string) error {
	c := exec.Command(name, args...)
	c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
	return c.Run()
}

func ask(q string) bool {
	fmt.Print(q + " [Y/n] ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	line = strings.ToLower(strings.TrimSpace(line))
	return line == "" || line == "y" || line == "yes"
}

func haveGh() bool {
	_, err := exec.LookPath("gh")
	return err == nil
}

func loggedIn() bool {
	tok, _ := auth.TokenForHost("github.com")
	return tok != ""
}

const installGh = `TermGit logs in through the GitHub CLI ("gh"), which isn't installed yet.
Install it, then run  termgit login :

  Windows   winget install --id GitHub.cli
  macOS     brew install gh
  Linux     see https://github.com/cli/cli#installation
`

// ready checks the login before opening TermGit, and offers to log in
func ready() bool {
	if loggedIn() {
		return true
	}
	if !haveGh() {
		fmt.Print(installGh)
		os.Exit(1)
	}
	fmt.Println("TermGit needs you to log in to GitHub first (it uses the GitHub CLI; TermGit never stores a token).")
	if !ask("Log in now?") {
		os.Exit(1)
	}
	login()
	return loggedIn()
}

func login() {
	if !haveGh() {
		fmt.Print(installGh)
		os.Exit(1)
	}
	if loggedIn() {
		fmt.Println("Already logged in. Adding the permissions TermGit uses (a browser page may open)…")
		if err := run("gh", "auth", "refresh", "-h", "github.com", "-s", loginScopes); err != nil {
			fail(err)
		}
		return
	}
	if err := run("gh", "auth", "login", "-h", "github.com", "-p", "https", "-w", "-s", loginScopes); err != nil {
		fail(err)
	}
	run("gh", "auth", "setup-git") // so git pull / push use the same login
	fmt.Println("Logged in. Run  termgit  to start.")
}

func doctor() {
	ok := func(good bool, what, fix string) {
		if good {
			fmt.Println("  ✓ " + what)
		} else {
			fmt.Println("  ✗ " + what + "\n      " + fix)
		}
	}
	fmt.Printf("TermGit %s\n\n", version)
	ok(haveGh(), "GitHub CLI (gh) installed", "install it: winget install --id GitHub.cli · brew install gh · https://github.com/cli/cli#installation")
	ok(loggedIn(), "logged in to GitHub", "run  termgit login")
	_, err := exec.LookPath("git")
	ok(err == nil, "git installed (for repos on this PC)", "install it: https://git-scm.com/downloads")
	if haveGh() && loggedIn() {
		out, _ := exec.Command("gh", "auth", "status", "-h", "github.com").CombinedOutput()
		s := string(out)
		for _, sc := range []string{"repo", "read:org", "workflow", "delete_repo", "user:follow"} {
			ok(strings.Contains(s, "'"+sc+"'"), "permission "+sc, "run  termgit login  to add it")
		}
	}
	ed := strings.Fields(cfg.editor + " nvim")[0]
	_, err = exec.LookPath(ed)
	ok(err == nil, "editor ("+ed+") found", "install it, or set  editor =  in the settings (termgit config)")
	gt := strings.Fields(cfg.gitTool + " lazygit")[0]
	_, err = exec.LookPath(gt)
	ok(err == nil, "git tool ("+gt+", the L key) found", "optional: install it, or set  git_tool =  in the settings")
	fmt.Println("\n  settings: " + configPath())
	fmt.Println("  cache:    " + cacheDir())
	fmt.Println("  clones:   " + cfg.cloneInto)
	fmt.Println("\nIcons need a Nerd Font in your terminal: https://www.nerdfonts.com")
}
