package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/cli/go-gh/v2/pkg/api"
)

type Repo struct {
	ID               string    `json:"id"`
	ViewerHasStarred bool      `json:"viewerHasStarred"`
	NameWithOwner    string    `json:"nameWithOwner"`
	Name             string    `json:"name"`
	Description      string    `json:"description"`
	IsPrivate        bool      `json:"isPrivate"`
	IsFork           bool      `json:"isFork"`
	IsArchived       bool      `json:"isArchived"`
	Stars            int       `json:"stargazerCount"`
	Forks            int       `json:"forkCount"`
	PushedAt         time.Time `json:"pushedAt"`
	URL              string    `json:"url"`
	DefaultBranchRef *struct {
		Name   string `json:"name"`
		Target *struct {
			Oid string `json:"oid"`
		} `json:"target"`
	} `json:"defaultBranchRef"`
	PrimaryLanguage *struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	} `json:"primaryLanguage"`
}

func (r Repo) Branch() string {
	if r.DefaultBranchRef != nil {
		return r.DefaultBranchRef.Name
	}
	return ""
}

// HeadOid is the newest commit on the default branch on GitHub
func (r Repo) HeadOid() string {
	if r.DefaultBranchRef != nil && r.DefaultBranchRef.Target != nil {
		return r.DefaultBranchRef.Target.Oid
	}
	return ""
}

func (r Repo) Lang() string {
	if r.PrimaryLanguage != nil {
		return r.PrimaryLanguage.Name
	}
	return ""
}

// ---- messages ----

type reposMsg struct {
	login string
	repos []Repo
	err   error
}

type readmeMsg struct {
	nwo  string
	md   string
	none bool
	err  error
}

const reposQuery = `query($after: String) {
  viewer {
    login
    repositories(first: 100, after: $after, ownerAffiliations: [OWNER, COLLABORATOR, ORGANIZATION_MEMBER],
                 orderBy: {field: PUSHED_AT, direction: DESC}) {
      pageInfo { hasNextPage endCursor }
      nodes {
        id viewerHasStarred nameWithOwner name description isPrivate isFork isArchived stargazerCount forkCount pushedAt url
        defaultBranchRef { name target { oid } }
        primaryLanguage { name color }
      }
    }
  }
}`

func fetchRepos() tea.Cmd {
	return func() tea.Msg {
		c, err := api.DefaultGraphQLClient()
		if err != nil {
			return reposMsg{err: err}
		}
		var all []Repo
		login := ""
		var after *string
		for page := 0; page < 5; page++ {
			var resp struct {
				Viewer struct {
					Login        string
					Repositories struct {
						PageInfo struct {
							HasNextPage bool
							EndCursor   string
						}
						Nodes []Repo
					}
				}
			}
			if err := c.Do(reposQuery, map[string]interface{}{"after": after}, &resp); err != nil {
				return reposMsg{err: err}
			}
			login = resp.Viewer.Login
			all = append(all, resp.Viewer.Repositories.Nodes...)
			if !resp.Viewer.Repositories.PageInfo.HasNextPage {
				break
			}
			cur := resp.Viewer.Repositories.PageInfo.EndCursor
			after = &cur
		}
		saveCache("repos.json", reposCache{Login: login, Repos: all, At: time.Now()})
		return reposMsg{login: login, repos: all}
	}
}

func fetchReadme(nwo string) tea.Cmd {
	return func() tea.Msg {
		c, err := api.DefaultRESTClient()
		if err != nil {
			return readmeMsg{nwo: nwo, err: err}
		}
		var r struct {
			Content  string `json:"content"`
			Encoding string `json:"encoding"`
		}
		err = c.Get("repos/"+nwo+"/readme", &r)
		var he *api.HTTPError
		if errors.As(err, &he) && he.StatusCode == 404 {
			saveText(readmePath(nwo), "\x00none")
			return readmeMsg{nwo: nwo, none: true}
		}
		if err != nil {
			return readmeMsg{nwo: nwo, err: err}
		}
		data, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(r.Content, "\n", ""))
		if err != nil {
			return readmeMsg{nwo: nwo, err: err}
		}
		saveText(readmePath(nwo), string(data))
		return readmeMsg{nwo: nwo, md: string(data)}
	}
}

// ---- cache (so the app opens instantly; refreshed in the background) ----

type reposCache struct {
	Login string    `json:"login"`
	Repos []Repo    `json:"repos"`
	At    time.Time `json:"at"`
}

func cacheDir() string {
	d, err := os.UserCacheDir()
	if err != nil {
		d = os.TempDir()
	}
	d = filepath.Join(d, "termgit")
	os.MkdirAll(filepath.Join(d, "readme"), 0o755)
	return d
}

func saveCache(name string, v any) {
	data, err := json.Marshal(v)
	if err == nil {
		os.WriteFile(filepath.Join(cacheDir(), name), data, 0o644)
	}
}

func loadCache(name string, v any) bool {
	data, err := os.ReadFile(filepath.Join(cacheDir(), name))
	return err == nil && json.Unmarshal(data, v) == nil
}

func readmePath(nwo string) string {
	return filepath.Join(cacheDir(), "readme", strings.ReplaceAll(nwo, "/", "__")+".md")
}

func saveText(p, s string) { os.WriteFile(p, []byte(s), 0o644) }

// loadReadme returns the saved README, whether there is one, and how old it is
func loadReadme(nwo string) (md string, none bool, age time.Duration, ok bool) {
	p := readmePath(nwo)
	st, err := os.Stat(p)
	if err != nil {
		return "", false, 0, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", false, 0, false
	}
	if string(data) == "\x00none" {
		return "", true, time.Since(st.ModTime()), true
	}
	return string(data), false, time.Since(st.ModTime()), true
}

// ---- local clones: which of your repos are already on this PC ----

var remoteRe = regexp.MustCompile(`(?m)^\s*url\s*=\s*\S*github\.com[:/]([^/\s]+)/([^/\s]+?)(?:\.git)?/?\s*$`)

func scanLocal(roots []string) map[string]string {
	out := map[string]string{}
	var visit func(dir string, depth int)
	visit = func(dir string, depth int) {
		if data, err := os.ReadFile(filepath.Join(dir, ".git", "config")); err == nil {
			for _, m := range remoteRe.FindAllStringSubmatch(string(data), -1) {
				key := strings.ToLower(m[1] + "/" + m[2])
				if _, seen := out[key]; !seen {
					out[key] = dir
				}
			}
			return
		}
		if depth == 0 {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			n := e.Name()
			if e.IsDir() && !strings.HasPrefix(n, ".") && n != "node_modules" {
				visit(filepath.Join(dir, n), depth-1)
			}
		}
	}
	for _, r := range roots {
		visit(r, 2)
	}
	return out
}

func home(p ...string) string {
	h, _ := os.UserHomeDir()
	return filepath.Join(append([]string{h}, p...)...)
}

// Where clones go and where local repos are looked for (settings come in a later batch)
var (
	cloneRoot  = home("code")
	localRoots = []string{home("code")}
)
