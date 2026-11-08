// TermGit: GitHub in the terminal.
package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

const version = "0.1.0"

func main() {
	args := os.Args[1:]
	if len(args) > 0 {
		switch args[0] {
		case "--version", "-v":
			fmt.Println("termgit", version)
			return
		case "--dump":
			// test helper: termgit --dump 140x40 [keys or click:x,y / wheel:x,y,up|down ...]
			dump(args[1:])
			return
		}
	}
	if _, err := tea.NewProgram(newModel()).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "termgit:", err)
		os.Exit(1)
	}
}

// ---- headless test mode: runs the app without a terminal and prints one screen ----

func dump(args []string) {
	w, h := 140, 40
	if len(args) > 0 {
		if p := strings.SplitN(args[0], "x", 2); len(p) == 2 {
			w, _ = strconv.Atoi(p[0])
			h, _ = strconv.Atoi(p[1])
			args = args[1:]
		}
	}
	m := newModel()
	m.dump = true
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	drain(m, m.Init())
	for _, a := range args {
		m.View() // record the clickable zones of the current screen
		drain(m, func() tea.Msg { return parseInput(a) })
	}
	out := m.View().Content
	if os.Getenv("TERMGIT_RAW") == "" {
		out = ansi.Strip(out)
	}
	fmt.Println(out)
}

func drain(m *model, first tea.Cmd) {
	queue := []tea.Cmd{first}
	for steps := 0; len(queue) > 0 && steps < 200; steps++ {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		switch msg := msg.(type) {
		case nil:
		case tea.BatchMsg:
			queue = append(queue, msg...)
		case tea.QuitMsg:
			return
		default:
			_, next := m.Update(msg)
			queue = append(queue, next)
		}
	}
}

func parseInput(a string) tea.Msg {
	if strings.HasPrefix(a, "click:") || strings.HasPrefix(a, "wheel:") {
		p := strings.Split(a[6:], ",")
		x, _ := strconv.Atoi(p[0])
		y, _ := strconv.Atoi(p[1])
		if a[0] == 'c' {
			return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
		}
		b := tea.MouseWheelDown
		if len(p) > 2 && p[2] == "up" {
			b = tea.MouseWheelUp
		}
		return tea.MouseWheelMsg{X: x, Y: y, Button: b}
	}
	named := map[string]rune{
		"enter": tea.KeyEnter, "esc": tea.KeyEscape, "tab": tea.KeyTab, "down": tea.KeyDown,
		"up": tea.KeyUp, "backspace": tea.KeyBackspace,
	}
	if r, ok := named[a]; ok {
		return tea.KeyPressMsg{Code: r}
	}
	if strings.HasPrefix(a, "ctrl+") {
		return tea.KeyPressMsg{Code: rune(a[5]), Mod: tea.ModCtrl}
	}
	r := []rune(a)[0]
	return tea.KeyPressMsg{Code: r, Text: a}
}
