package main

// GitHub data for the repo explorer (files, commits, branches) and the state of
// local clones (unpushed commits, changed files, behind GitHub).

import (
	"encoding/base64"
	"errors"
	"net/url"
	"os/exec"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/cli/go-gh/v2/pkg/api"
)

// ---- generic loader: fetch once, keep the result, debounce while scrolling ----

// loadedMsg carries a finished fetch; apply stores the result in the model
type loadedMsg struct {
	key   string
	apply func(m *model)
}

type loadTick struct {
	seq int
	key string
}

// load runs fn in the background unless key is loaded or loading already.
// With debounce it waits a moment, so moving fast through a list fetches only where you stop.
func (m *model) load(key string, debounce bool, fn func() func(*model)) tea.Cmd {
	if m.loaded[key] || m.inflight[key] {
		return nil
	}
	run := func() tea.Msg { return loadedMsg{key, fn()} }
	if !debounce || m.dump {
		m.inflight[key] = true
		return run
	}
	m.loadSeq++
	seq := m.loadSeq
	m.pending[key] = run
	return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return loadTick{seq, key} })
}

func splitNwo(nwo string) (string, string) {
	p := strings.SplitN(nwo, "/", 2)
	if len(p) < 2 {
		return nwo, ""
	}
	return p[0], p[1]
}

func apiErr(err error) string {
	var he *api.HTTPError
	if errors.As(err, &he) {
		if he.Message != "" {
			return he.Message
		}
		return "GitHub said " + strconv.Itoa(he.StatusCode)
	}
	return err.Error()
}

// ---- files ----

type treeEntry struct {
	Path string `json:"path"`
	Type string `json:"type"` // blob (file), tree (folder), commit (submodule)
	Sha  string `json:"sha"`
	Size int    `json:"size"`
}

func (e treeEntry) name() string { return path.Base(e.Path) }

type treeData struct {
	ents      []treeEntry
	children  map[string][]treeEntry // folder path ("" = top) -> its entries, folders first
	truncated bool
	err       string
}

func fetchTree(nwo, ref, oid string) func() func(*model) {
	return func() func(*model) {
		td := &treeData{children: map[string][]treeEntry{}}
		c, err := api.DefaultRESTClient()
		if err == nil {
			target := oid
			if target == "" {
				target = url.PathEscape(ref)
			}
			var r struct {
				Tree      []treeEntry `json:"tree"`
				Truncated bool        `json:"truncated"`
			}
			err = c.Get("repos/"+nwo+"/git/trees/"+target+"?recursive=1", &r)
			td.ents, td.truncated = r.Tree, r.Truncated
		}
		var he *api.HTTPError
		switch {
		case errors.As(err, &he) && he.StatusCode == 409:
			td.err = "This repo is empty."
		case err != nil:
			td.err = apiErr(err)
		}
		for _, e := range td.ents {
			dir := path.Dir(e.Path)
			if dir == "." {
				dir = ""
			}
			td.children[dir] = append(td.children[dir], e)
		}
		for _, list := range td.children {
			sort.Slice(list, func(i, j int) bool {
				if (list[i].Type == "tree") != (list[j].Type == "tree") {
					return list[i].Type == "tree"
				}
				return strings.ToLower(list[i].name()) < strings.ToLower(list[j].name())
			})
		}
		key := nwo + "@" + ref
		return func(m *model) { m.trees[key] = td }
	}
}

type blobData struct {
	text   string
	binary bool
	err    string
}

const maxFile = 1 << 20 // files bigger than 1 MB are only offered in the browser

func fetchBlob(nwo, sha string) func() func(*model) {
	return func() func(*model) {
		b := &blobData{}
		c, err := api.DefaultRESTClient()
		if err == nil {
			var r struct {
				Content string `json:"content"`
			}
			if err = c.Get("repos/"+nwo+"/git/blobs/"+sha, &r); err == nil {
				var data []byte
				data, err = base64.StdEncoding.DecodeString(strings.ReplaceAll(r.Content, "\n", ""))
				head := data
				if len(head) > 8000 {
					head = head[:8000]
				}
				b.binary = strings.ContainsRune(string(head), 0)
				b.text = string(data)
			}
		}
		if err != nil {
			b.err = apiErr(err)
		}
		return func(m *model) { m.blobs[sha] = b }
	}
}

// ---- commits ----

type Commit struct {
	Oid      string    `json:"oid"`
	Short    string    `json:"abbreviatedOid"`
	Headline string    `json:"messageHeadline"`
	Body     string    `json:"messageBody"`
	Date     time.Time `json:"committedDate"`
	Adds     int       `json:"additions"`
	Dels     int       `json:"deletions"`
	Files    int       `json:"changedFilesIfAvailable"`
	Author   struct {
		Name string `json:"name"`
		User *struct {
			Login string `json:"login"`
		} `json:"user"`
	} `json:"author"`
}

func (c Commit) who() string {
	if c.Author.User != nil {
		return c.Author.User.Login
	}
	return c.Author.Name
}

type commitList struct {
	items []Commit
	err   string
}

const commitsQuery = `query($owner: String!, $name: String!, $ref: String!) {
  repository(owner: $owner, name: $name) {
    ref(qualifiedName: $ref) {
      target {
        ... on Commit {
          history(first: 100) {
            nodes {
              oid abbreviatedOid messageHeadline messageBody committedDate additions deletions changedFilesIfAvailable
              author { name user { login } }
            }
          }
        }
      }
    }
  }
}`

func fetchCommits(nwo, ref string) func() func(*model) {
	return func() func(*model) {
		cl := &commitList{}
		c, err := api.DefaultGraphQLClient()
		if err == nil {
			owner, name := splitNwo(nwo)
			var r struct {
				Repository struct {
					Ref *struct {
						Target struct {
							History struct {
								Nodes []Commit
							}
						}
					}
				}
			}
			err = c.Do(commitsQuery, map[string]interface{}{"owner": owner, "name": name, "ref": "refs/heads/" + ref}, &r)
			if err == nil && r.Repository.Ref != nil {
				cl.items = r.Repository.Ref.Target.History.Nodes
			}
		}
		if err != nil {
			cl.err = apiErr(err)
		}
		key := nwo + "@" + ref
		return func(m *model) { m.clists[key] = cl }
	}
}

type commitFile struct {
	Name   string `json:"filename"`
	Prev   string `json:"previous_filename"`
	Status string `json:"status"`
	Adds   int    `json:"additions"`
	Dels   int    `json:"deletions"`
	Patch  string `json:"patch"`
}

type commitDetail struct {
	files []commitFile
	err   string
}

func fetchCommitDetail(nwo, oid string) func() func(*model) {
	return func() func(*model) {
		d := &commitDetail{}
		c, err := api.DefaultRESTClient()
		if err == nil {
			var r struct {
				Files []commitFile `json:"files"`
			}
			if err = c.Get("repos/"+nwo+"/commits/"+oid, &r); err == nil {
				d.files = r.Files
			}
		}
		if err != nil {
			d.err = apiErr(err)
		}
		key := nwo + "@" + oid
		return func(m *model) { m.cdetail[key] = d }
	}
}

// ---- branches ----

type Branch struct {
	Name   string
	Commit Commit
	Ahead  int // commits this branch has that the default branch doesn't
	Behind int // commits the default branch has that this branch doesn't
}

type branchList struct {
	items []Branch
	err   string
}

const branchesQuery = `query($owner: String!, $name: String!, $base: String!) {
  repository(owner: $owner, name: $name) {
    refs(refPrefix: "refs/heads/", first: 100, orderBy: {field: TAG_COMMIT_DATE, direction: DESC}) {
      nodes {
        name
        compare(headRef: $base) { aheadBy behindBy }
        target { ... on Commit { oid abbreviatedOid messageHeadline committedDate author { name user { login } } } }
      }
    }
  }
}`

func fetchBranches(nwo, base string) func() func(*model) {
	return func() func(*model) {
		bl := &branchList{}
		c, err := api.DefaultGraphQLClient()
		if err == nil {
			owner, name := splitNwo(nwo)
			var r struct {
				Repository struct {
					Refs struct {
						Nodes []struct {
							Name    string
							Compare *struct {
								AheadBy  int
								BehindBy int
							}
							Target Commit
						}
					}
				}
			}
			err = c.Do(branchesQuery, map[string]interface{}{"owner": owner, "name": name, "base": base}, &r)
			for _, n := range r.Repository.Refs.Nodes {
				b := Branch{Name: n.Name, Commit: n.Target}
				if n.Compare != nil {
					// compare runs from this branch to the default one:
					// "ahead" there means the default branch is ahead of this one
					b.Ahead, b.Behind = n.Compare.BehindBy, n.Compare.AheadBy
				}
				bl.items = append(bl.items, b)
			}
			// default branch first
			sort.SliceStable(bl.items, func(i, j int) bool { return bl.items[i].Name == base && bl.items[j].Name != base })
		}
		if err != nil {
			bl.err = apiErr(err)
		}
		return func(m *model) { m.blists[nwo] = bl }
	}
}

// ---- local clones ----

type localInfo struct {
	branch  string
	dirty   int  // changed files not committed yet
	ahead   int  // commits not pushed to GitHub
	behind  int  // commits on GitHub you don't have
	missing bool // GitHub has commits this PC hasn't even downloaded (count unknown until a fetch)
}

type localMsg map[string]localInfo

var aheadRe = regexp.MustCompile(`ahead (\d+)`)
var behindRe = regexp.MustCompile(`behind (\d+)`)

func git(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).Output()
	return strings.TrimSpace(string(out)), err
}

func gitInfo(dir, ghBranch, ghOid string) localInfo {
	var li localInfo
	out, err := git(dir, "status", "--porcelain=v1", "-b")
	if err != nil {
		return li
	}
	for i, line := range strings.Split(out, "\n") {
		if i == 0 && strings.HasPrefix(line, "## ") {
			head := strings.TrimPrefix(line, "## ")
			head = strings.TrimPrefix(head, "No commits yet on ")
			li.branch = strings.Fields(strings.SplitN(head, "...", 2)[0] + " ")[0]
			if m := aheadRe.FindStringSubmatch(head); m != nil {
				li.ahead, _ = strconv.Atoi(m[1])
			}
			if m := behindRe.FindStringSubmatch(head); m != nil {
				li.behind, _ = strconv.Atoi(m[1])
			}
			continue
		}
		if strings.TrimSpace(line) != "" {
			li.dirty++
		}
	}
	// on the default branch, compare with what GitHub has right now (no fetch needed)
	if ghOid != "" && li.branch == ghBranch {
		if _, err := git(dir, "cat-file", "-e", ghOid+"^{commit}"); err != nil {
			li.missing = true
		} else {
			if s, err := git(dir, "rev-list", "--count", "HEAD.."+ghOid); err == nil {
				li.behind, _ = strconv.Atoi(s)
			}
			if s, err := git(dir, "rev-list", "--count", ghOid+"..HEAD"); err == nil {
				li.ahead, _ = strconv.Atoi(s)
			}
		}
	}
	return li
}

func (m *model) scanLocalInfo() tea.Cmd {
	type job struct{ key, dir, branch, oid string }
	var jobs []job
	for _, r := range m.repos {
		k := strings.ToLower(r.NameWithOwner)
		if p, ok := m.local[k]; ok {
			jobs = append(jobs, job{k, p, r.Branch(), r.HeadOid()})
		}
	}
	return func() tea.Msg {
		out := localMsg{}
		for _, j := range jobs {
			out[j.key] = gitInfo(j.dir, j.branch, j.oid)
		}
		return out
	}
}
