package main

// Settings: a small text file you can edit (ctrl+p → Settings opens it in your editor).
//   %APPDATA%\termgit\config.txt  (key = value, # comments)

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
)

type config struct {
	startTab   string   // home, repos, issues, prs, actions, inbox, explore
	homeLayout string   // dashboard, panels
	cloneInto  string   // where c clones repos
	lookIn     []string // folders searched for repos you have on this PC
	editor     string   // writes comments / commit messages, opens files
	gitTool    string   // the L key
	notify     int      // seconds between notification checks
}

var cfg = defaultConfig()

func defaultConfig() config {
	return config{
		startTab:   "home",
		homeLayout: "dashboard",
		cloneInto:  home("code"),
		lookIn:     []string{home("code"), home("projects"), home("src"), home("repos"), home("Documents", "GitHub")},
		editor:     "nvim",
		gitTool:    "lazygit",
		notify:     60,
	}
}

func configPath() string {
	d, err := os.UserConfigDir()
	if err != nil {
		d = home(".config")
	}
	return filepath.Join(d, "termgit", "config.txt")
}

const configHelp = `# TermGit settings. Edit, save and quit: TermGit reads them again.
# Lines are  key = value ; a # starts a comment.

# The tab TermGit opens on: home, repos, issues, prs, actions, inbox, explore
start_tab = %s

# Home layout: dashboard or panels (t on Home switches too)
home_layout = %s

# Where c clones repos
clone_into = %s

# Folders searched (2 levels deep) for repos you have on this PC, separated by ;
look_in = %s

# The editor for comments, commit messages and opening files (e)
editor = %s

# The git app the L key opens
git_tool = %s

# How often the Inbox checks for new notifications, in seconds
notify_every = %d
`

func writeDefaultConfig(p string, c config) error {
	os.MkdirAll(filepath.Dir(p), 0o755)
	text := fmt.Sprintf(configHelp, c.startTab, c.homeLayout, c.cloneInto, strings.Join(c.lookIn, " ; "), c.editor, c.gitTool, c.notify)
	return os.WriteFile(p, []byte(text), 0o644)
}

// loadConfig reads the settings file (creating it the first time)
func loadConfig() config {
	c := defaultConfig()
	p := configPath()
	f, err := os.Open(p)
	if err != nil {
		writeDefaultConfig(p, c)
		return c
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		switch k {
		case "start_tab":
			c.startTab = strings.ToLower(v)
		case "home_layout":
			c.homeLayout = strings.ToLower(v)
		case "clone_into":
			if v != "" {
				c.cloneInto = expandHome(v)
			}
		case "look_in":
			var dirs []string
			for _, d := range strings.Split(v, ";") {
				if d = strings.TrimSpace(d); d != "" {
					dirs = append(dirs, expandHome(d))
				}
			}
			if len(dirs) > 0 {
				c.lookIn = dirs
			}
		case "editor":
			if v != "" {
				c.editor = v
			}
		case "git_tool":
			if v != "" {
				c.gitTool = v
			}
		case "notify_every":
			if n := atoiDef(v, c.notify); n >= 15 {
				c.notify = n
			}
		}
	}
	return c
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~") {
		return home(strings.TrimPrefix(strings.TrimPrefix(p, "~"), string(os.PathSeparator)))
	}
	return os.ExpandEnv(p)
}

func atoiDef(s string, def int) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return def
		}
		n = n*10 + int(r-'0')
	}
	if s == "" {
		return def
	}
	return n
}

// editorCmd runs your editor on the given files ("code -w" style settings work too)
func editorCmd(args ...string) *exec.Cmd {
	f := strings.Fields(cfg.editor)
	if len(f) == 0 {
		f = []string{"nvim"}
	}
	return exec.Command(f[0], append(f[1:], args...)...)
}

// editorAtEnd opens a file with the cursor at the end (for vim / nvim)
func editorAtEnd(file string) *exec.Cmd {
	if name := strings.ToLower(filepath.Base(strings.Fields(cfg.editor + " nvim")[0])); strings.Contains(name, "vim") {
		return editorCmd("+", file)
	}
	return editorCmd(file)
}

func gitToolCmd() *exec.Cmd {
	f := strings.Fields(cfg.gitTool)
	if len(f) == 0 {
		f = []string{"lazygit"}
	}
	return exec.Command(f[0], f[1:]...)
}

// applyConfig puts the settings into effect
func (m *model) applyConfig() {
	cloneRoot = cfg.cloneInto
	localRoots = cfg.lookIn
	tabsByName := map[string]int{"home": tabHome, "repos": tabRepos, "issues": tabIssues, "prs": tabPRs, "actions": tabActions, "inbox": tabInbox, "explore": tabExplore}
	if t, ok := tabsByName[cfg.startTab]; ok {
		m.tab = t
	}
}

type configDoneMsg struct{ err error }

// ctrl+p → Settings: edit the file in your editor, then use it
func (m *model) editConfig() tea.Cmd {
	p := configPath()
	if _, err := os.Stat(p); err != nil {
		writeDefaultConfig(p, cfg)
	}
	if m.dump {
		return m.setStatus("Would open "+p, false)
	}
	c := editorCmd(p)
	return tea.ExecProcess(c, func(err error) tea.Msg { return configDoneMsg{err} })
}

func (m *model) onConfigDone(msg configDoneMsg) tea.Cmd {
	if msg.err != nil {
		return m.setStatus("The editor stopped: "+msg.err.Error(), true)
	}
	cfg = loadConfig()
	cloneRoot, localRoots = cfg.cloneInto, cfg.lookIn
	m.homeGrid = cfg.homeLayout != "panels"
	m.local = scanLocal(localRoots)
	return tea.Batch(m.setStatus("Settings saved and applied", false), m.scanLocalInfo())
}

// edName / gitName: the editor and git tool as shown in hints and buttons
func edName() string {
	n := filepath.Base(strings.Fields(cfg.editor + " nvim")[0])
	return strings.TrimSuffix(n, filepath.Ext(n))
}

func gitName() string {
	n := filepath.Base(strings.Fields(cfg.gitTool + " lazygit")[0])
	return strings.TrimSuffix(n, filepath.Ext(n))
}
