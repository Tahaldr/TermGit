package main

// Tests for the parts that are easy to get wrong: diffs and line staging, conflict
// parsing, markdown, key hint wrapping, small helpers, and real git in a temp folder.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func tempRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, a := range [][]string{{"init", "-q", "-b", "main"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"}} {
		if out, err := gitRun(dir, a...); err != nil {
			t.Fatalf("git %v: %v %s", a, err, out)
		}
	}
	return dir
}

func write(t *testing.T, dir, name, text string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func commit(t *testing.T, dir, msg string) {
	t.Helper()
	gitRun(dir, "add", "-A")
	if out, err := gitRun(dir, "commit", "-qm", msg); err != nil {
		t.Fatalf("commit: %v %s", err, out)
	}
}

func TestStageOneLineAndUnstageIt(t *testing.T) {
	dir := tempRepo(t)
	write(t, dir, "a.txt", "1\n2\n3\n4\n5\n")
	commit(t, dir, "first")
	write(t, dir, "a.txt", "1\nNEW-A\n2\n3\n4\nNEW-B\n5\n")

	stage := func(line string, reverse bool) {
		args := []string{"diff", "--no-color"}
		if reverse {
			args = append(args, "--cached")
		}
		d, _ := gitRun(dir, args...)
		p := parseDiff(d)
		idx := -1
		for i, l := range p.hunks[0].lines {
			if l == line {
				idx = i
			}
		}
		if idx < 0 {
			t.Fatalf("line %q not in diff:\n%s", line, d)
		}
		f := filepath.Join(t.TempDir(), "p.diff")
		os.WriteFile(f, []byte(p.patchFor(0, idx, reverse)), 0o644)
		a := []string{"apply", "--cached", "--recount"}
		if reverse {
			a = append(a, "-R")
		}
		if out, err := gitRun(dir, append(a, f)...); err != nil {
			t.Fatalf("apply: %v %s", err, out)
		}
	}
	stage("+NEW-B", false)
	staged, _ := gitRun(dir, "diff", "--cached", "--no-color")
	if !strings.Contains(staged, "+NEW-B") || strings.Contains(staged, "+NEW-A") {
		t.Fatalf("only NEW-B should be staged:\n%s", staged)
	}
	stage("+NEW-B", true)
	if staged, _ = gitRun(dir, "diff", "--cached"); strings.TrimSpace(staged) != "" {
		t.Fatalf("nothing should be staged now:\n%s", staged)
	}
}

func TestChangesList(t *testing.T) {
	dir := tempRepo(t)
	write(t, dir, "a.txt", "x\n")
	commit(t, dir, "first")
	write(t, dir, "a.txt", "y\n")
	write(t, dir, "new file.txt", "n\n")
	m := newModel()
	fetchChanges(dir)()(m)
	cl := m.changes[dir]
	if len(cl.items) != 2 {
		t.Fatalf("want 2 changes, got %+v", cl.items)
	}
	if !cl.items[0].unstaged() || !cl.items[1].untracked() || cl.items[1].path != "new file.txt" {
		t.Fatalf("wrong changes: %+v", cl.items)
	}
}

func TestConflictParseAndState(t *testing.T) {
	dir := tempRepo(t)
	write(t, dir, "f.txt", "base\n")
	commit(t, dir, "base")
	gitRun(dir, "switch", "-qc", "other")
	write(t, dir, "f.txt", "theirs\n")
	commit(t, dir, "theirs")
	gitRun(dir, "switch", "-q", "main")
	write(t, dir, "f.txt", "ours\n")
	commit(t, dir, "ours")
	gitRun(dir, "merge", "--no-edit", "other")
	if st := gitState(dir); st != "merge" {
		t.Fatalf("state = %q, want merge", st)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "f.txt"))
	_, blocks := parseConflicts(string(data))
	if len(blocks) != 1 || blocks[0].ours[0] != "ours" || blocks[0].theirs[0] != "theirs" {
		t.Fatalf("bad blocks: %+v", blocks)
	}
}

func TestRebaseTodo(t *testing.T) {
	dir := tempRepo(t)
	for i := 1; i <= 3; i++ {
		write(t, dir, fmt.Sprintf("f%d", i), "x\n")
		commit(t, dir, fmt.Sprintf("c%d", i))
	}
	m := newModel()
	fetchLog(dir, "")()(m)
	ll := m.llogs[dir+"|"]
	base := strings.TrimSpace(func() string { o, _ := gitRun(dir, "rev-parse", ll.items[1].sha+"^"); return o }())
	todo := fmt.Sprintf("pick %s\nsquash %s\n", ll.items[1].sha, ll.items[0].sha)
	tf := filepath.Join(t.TempDir(), "todo")
	os.WriteFile(tf, []byte(todo), 0o644)
	if out, err := gitRunEnv(dir, []string{"GIT_SEQUENCE_EDITOR=cp '" + filepath.ToSlash(tf) + "'", "GIT_EDITOR=true"}, "rebase", "-i", base); err != nil {
		t.Fatalf("rebase: %v %s", err, out)
	}
	log, _ := gitRun(dir, "log", "--format=%s")
	if n := len(strings.Fields(log)); n != 2 {
		t.Fatalf("want 2 commits after squash, got:\n%s", log)
	}
}

func TestMarkdown(t *testing.T) {
	d := renderMD("# Title\n\n- [ ] todo\n- [x] done\n\n> [!NOTE]\n> hi\n\n| a | b |\n|---|---|\n| 1 | 2 |\n", 60)
	text := ansi.Strip(strings.Join(d.lines, "\n"))
	for _, want := range []string{"Title", "□ todo", "■ done", "Note", "hi", "┌", "│ 1"} {
		if !strings.Contains(text, want) {
			t.Errorf("markdown output is missing %q:\n%s", want, text)
		}
	}
	if len(d.heads) != 1 || d.heads[0].text != "Title" {
		t.Errorf("headings = %+v", d.heads)
	}
}

func TestWrapHintsKeepsHelp(t *testing.T) {
	var h []hint
	for i := 0; i < 40; i++ {
		h = append(h, hint{fmt.Sprintf("k%d", i), "some action"})
	}
	h = append(h, hint{"?", "all keys"})
	lines := wrapHints(h, 80)
	if len(lines) > 2 {
		t.Fatalf("more than 2 lines: %d", len(lines))
	}
	last := lines[len(lines)-1]
	if last[len(last)-1][0] != "?" {
		t.Fatalf("? must stay visible, last = %v", last[len(last)-1])
	}
}

func TestHelpers(t *testing.T) {
	cases := map[string]string{"v1.2.3": "v1.2.4", "0.9.9": "0.9.10", "weird": "v1.0.0"}
	for in, want := range cases {
		if got := nextVersion(in); got != want {
			t.Errorf("nextVersion(%q) = %q, want %q", in, got, want)
		}
	}
	if a, b, gone := parseTrack("[ahead 2, behind 3]"); a != 2 || b != 3 || gone {
		t.Errorf("parseTrack = %d %d %v", a, b, gone)
	}
	if got := shortAge("3 days ago"); got != "3d" {
		t.Errorf("shortAge = %q", got)
	}
	if got := ago(time.Now().Add(-2 * time.Hour)); got != "2h" {
		t.Errorf("ago = %q", got)
	}
	if got := spark([]int{0, 1, 8}); len([]rune(got)) != 3 {
		t.Errorf("spark = %q", got)
	}
	n := Notif{}
	n.Subject.URL = "https://api.github.com/repos/o/r/pulls/12"
	if n.number() != 12 || n.webURL() != "https://github.com/o/r/pull/12" {
		t.Errorf("notif number/url = %d %s", n.number(), n.webURL())
	}
}

func TestConfigRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.txt")
	c := defaultConfig()
	c.startTab, c.editor = "repos", "code -w"
	if err := writeDefaultConfig(p, c); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(p)
	if !strings.Contains(string(data), "start_tab = repos") || !strings.Contains(string(data), "editor = code -w") {
		t.Fatalf("config file:\n%s", data)
	}
}
