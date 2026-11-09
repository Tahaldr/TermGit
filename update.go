package main

// Updates: `termgit update` downloads the newest release for this system and replaces
// itself; TermGit checks at most once a day and shows when a newer version is out.
// Settings and saved data live in other folders, so updating never touches them.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

const repoSlug = "Tahaldr/TermGit" // where releases are published

type ghRelease struct {
	Tag    string `json:"tag_name"`
	URL    string `json:"html_url"`
	Assets []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int    `json:"size"`
	} `json:"assets"`
}

func latestRelease() (*ghRelease, error) {
	c := &http.Client{Timeout: 15 * time.Second}
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/"+repoSlug+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "termgit/"+version)
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub answered %s", resp.Status)
	}
	var r ghRelease
	return &r, json.NewDecoder(resp.Body).Decode(&r)
}

// newer: is version a newer than b? ("v2.1.0" > "2.0.9")
func newer(a, b string) bool {
	pa, pb := verParts(a), verParts(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func verParts(v string) [3]int {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	for i, p := range strings.SplitN(v, ".", 3) {
		out[i], _ = strconv.Atoi(p)
	}
	return out
}

func assetName() string {
	n := "termgit_" + runtime.GOOS + "_" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		n += ".exe"
	}
	return n
}

// cleanOldBinary removes what an update left behind (Windows can't delete a running program)
func cleanOldBinary() {
	if exe, err := os.Executable(); err == nil {
		os.Remove(exe + ".old")
	}
}

// selfUpdate: termgit update [--check]
func selfUpdate(checkOnly bool) {
	fmt.Println("Looking for a newer TermGit…")
	rel, err := latestRelease()
	if err != nil {
		fail(fmt.Errorf("couldn't reach GitHub: %w", err))
	}
	saveCache("update.json", updateCheck{At: time.Now(), Latest: rel.Tag})
	if !newer(rel.Tag, version) {
		fmt.Printf("You have the newest version (%s).\n", version)
		return
	}
	fmt.Printf("TermGit %s is out (you have %s).\n  What's new: %s\n", strings.TrimPrefix(rel.Tag, "v"), version, rel.URL)
	if checkOnly {
		fmt.Println("Run  termgit update  to get it.")
		return
	}
	url := ""
	for _, a := range rel.Assets {
		if a.Name == assetName() {
			url = a.URL
		}
	}
	if url == "" {
		fail(fmt.Errorf("this release has no program for %s/%s yet; get it from %s", runtime.GOOS, runtime.GOARCH, rel.URL))
	}
	exe, err := os.Executable()
	if err != nil {
		fail(err)
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	fmt.Println("Downloading " + assetName() + "…")
	tmp := exe + ".new"
	if err := download(url, tmp); err != nil {
		os.Remove(tmp)
		fail(fmt.Errorf("download failed: %w", err))
	}
	os.Chmod(tmp, 0o755)
	// swap: the running program is moved aside (Windows allows renaming it), the new one takes its place
	os.Remove(exe + ".old")
	if err := os.Rename(exe, exe+".old"); err != nil {
		os.Remove(tmp)
		fail(fmt.Errorf("couldn't replace %s: %w (try running the update from a terminal with permission to write there)", exe, err))
	}
	if err := os.Rename(tmp, exe); err != nil {
		os.Rename(exe+".old", exe) // put the old one back
		fail(fmt.Errorf("couldn't put the new version in place: %w", err))
	}
	if runtime.GOOS != "windows" {
		os.Remove(exe + ".old")
	}
	fmt.Printf("Updated to %s. Your settings and data weren't touched.\n", strings.TrimPrefix(rel.Tag, "v"))
}

func download(url, dest string) error {
	c := &http.Client{Timeout: 5 * time.Minute}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "termgit/"+version)
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("GitHub answered %s", resp.Status)
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, resp.Body)
	f.Close()
	if err == nil && n < 1<<20 {
		err = fmt.Errorf("the file is too small (%d bytes)", n)
	}
	return err
}

// ---- the daily check inside TermGit ----

type updateCheck struct {
	At     time.Time `json:"at"`
	Latest string    `json:"latest"`
}

type updateMsg struct{ latest string }

// checkUpdate: at most once a day, in the background
func (m *model) checkUpdate() tea.Cmd {
	var uc updateCheck
	if loadCache("update.json", &uc) && time.Since(uc.At) < 24*time.Hour {
		if newer(uc.Latest, version) {
			m.newVersion = uc.Latest
		}
		return nil
	}
	return func() tea.Msg {
		rel, err := latestRelease()
		if err != nil {
			return nil
		}
		saveCache("update.json", updateCheck{At: time.Now(), Latest: rel.Tag})
		return updateMsg{rel.Tag}
	}
}

func (m *model) onUpdate(msg updateMsg) tea.Cmd {
	if !newer(msg.latest, version) {
		return nil
	}
	m.newVersion = msg.latest
	return m.setStatus("TermGit "+strings.TrimPrefix(msg.latest, "v")+" is out: run  termgit update  (or click the arrow at the top right)", false)
}

type updatedMsg struct{ err error }

// clicking the update chip: run termgit update, then ask to restart
func (m *model) askUpdate() tea.Cmd {
	v := strings.TrimPrefix(m.newVersion, "v")
	m.prompt = &prompt{label: "Update TermGit to " + v + " now? Your settings and data stay as they are. TermGit closes after; just open it again.", confirm: true,
		done: func(m *model, _ string) tea.Cmd {
			if m.dump {
				return m.setStatus("Would update to "+v, false)
			}
			exe, err := os.Executable()
			if err != nil {
				return m.setStatus(err.Error(), true)
			}
			c := exec.Command(exe, "update")
			return tea.ExecProcess(c, func(err error) tea.Msg { return updatedMsg{err} })
		}}
	return nil
}

func (m *model) onUpdated(msg updatedMsg) tea.Cmd {
	if msg.err != nil {
		return m.setStatus("The update didn't work: "+msg.err.Error(), true)
	}
	m.exitMsg = "TermGit was updated. Open it again to use the new version."
	return tea.Quit // the new version starts next time
}
