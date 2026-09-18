package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ghCmd is a variable so tests can point it at a stub.
var ghCmd = "gh"

type prRecord struct {
	Number int    `json:"number"`
	State  string `json:"state"`
	Head   string `json:"headRefName"`
}

// PRCache answers "what is the PR state of branch X in repo Y" from a per-repo
// TSV cache, refreshed in bulk from gh at most once per TTL. Rules:
//   - a failed refresh keeps the stale cache (stale beats empty), and marks
//     the repo offline for this run so no per-branch calls are attempted;
//   - freshness is a sidecar .stamp written only on a successful bulk fetch,
//     so per-branch appends cannot extend the TTL;
//   - "no PR" is remembered as NOPR so it costs one call, not one per scan.
type PRCache struct {
	dir     string
	ttl     time.Duration
	enabled bool
	mu      sync.Mutex
	repos   map[string]*repoCache
}

type repoCache struct {
	file    string
	entries map[string]prRecord // headRefName -> record; State NOPR means none
	offline bool
}

func NewPRCache(cfg *Config) *PRCache {
	c := &PRCache{dir: filepath.Join(cfg.CacheDir, "pr"), ttl: cfg.PRTTL, repos: map[string]*repoCache{}}
	if cfg.NoGH {
		return c
	}
	if _, err := exec.LookPath(ghCmd); err == nil {
		c.enabled = true
	}
	return c
}

func (c *PRCache) Enabled() bool { return c != nil && c.enabled }

func (c *PRCache) fileFor(repo string) string {
	h := sha1.Sum([]byte(repo))
	return filepath.Join(c.dir, hex.EncodeToString(h[:8])+".tsv")
}

func (c *PRCache) load(repo string) *repoCache {
	c.mu.Lock()
	defer c.mu.Unlock()
	if rc, ok := c.repos[repo]; ok {
		return rc
	}
	rc := &repoCache{file: c.fileFor(repo), entries: map[string]prRecord{}}
	if f, err := os.Open(rc.file); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			p := strings.Split(sc.Text(), "\t")
			if len(p) < 3 {
				continue
			}
			if _, dup := rc.entries[p[0]]; dup {
				continue // first (newest) wins
			}
			n, _ := strconv.Atoi(p[2])
			rc.entries[p[0]] = prRecord{Head: p[0], State: p[1], Number: n}
		}
		f.Close()
	}
	c.repos[repo] = rc
	return rc
}

func (c *PRCache) fresh(rc *repoCache) bool {
	st, err := os.Stat(rc.file + ".stamp")
	if err != nil {
		return false
	}
	return time.Since(st.ModTime()) < c.ttl
}

// Ensure makes the bulk cache for repo current if gh is usable.
func (c *PRCache) Ensure(repo string) {
	if !c.Enabled() {
		return
	}
	rc := c.load(repo)
	if c.fresh(rc) && len(rc.entries) > 0 {
		return
	}
	cmd := exec.Command(ghCmd, "pr", "list", "--state", "all", "--limit", "400", "--json", "number,state,headRefName")
	cmd.Dir = repo
	out, err := cmd.Output()
	if err != nil {
		rc.offline = true
		return
	}
	var recs []prRecord
	if err := json.Unmarshal(out, &recs); err != nil {
		rc.offline = true
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	// Rebuild from the bulk page but keep sentinels/fallbacks the page lacks.
	fresh := map[string]prRecord{}
	for _, r := range recs {
		if _, dup := fresh[r.Head]; !dup {
			fresh[r.Head] = r
		}
	}
	for k, v := range rc.entries {
		if _, ok := fresh[k]; !ok && v.State != "NOPR" {
			fresh[k] = v
		}
	}
	rc.entries = fresh
	c.persist(rc)
	_ = os.WriteFile(rc.file+".stamp", []byte(strconv.FormatInt(time.Now().Unix(), 10)), 0o644)
}

func (c *PRCache) persist(rc *repoCache) {
	var b strings.Builder
	for _, r := range rc.entries {
		fmt.Fprintf(&b, "%s\t%s\t%d\n", r.Head, r.State, r.Number)
	}
	_ = os.WriteFile(rc.file, []byte(b.String()), 0o644)
}

// Lookup returns MISS when the branch is absent from the cache entirely,
// NOPR when GitHub already said there is no PR, or the PR state and number.
func (c *PRCache) Lookup(repo, branch string) (string, int) {
	if branch == "" {
		return "NOPR", 0
	}
	if !c.Enabled() {
		return "NONE", 0
	}
	rc := c.load(repo)
	c.mu.Lock()
	r, ok := rc.entries[branch]
	c.mu.Unlock()
	if !ok {
		if rc.offline {
			return "NONE", 0
		}
		return "MISS", 0
	}
	return r.State, r.Number
}

// ResolveMisses asks GitHub about each branch the bulk page did not cover,
// in parallel, and records the answer (including "no PR").
func (c *PRCache) ResolveMisses(ws []*Worktree, jobs int) {
	if !c.Enabled() || len(ws) == 0 {
		return
	}
	sem := make(chan struct{}, jobs)
	var wg sync.WaitGroup
	touched := map[*repoCache]bool{}
	var tmu sync.Mutex
	for _, w := range ws {
		w := w
		rc := c.load(w.Repo)
		if rc.offline {
			w.PRState = "NONE"
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			key := w.prKey()
			cmd := exec.Command(ghCmd, "pr", "list", "--head", key, "--state", "all", "--limit", "1", "--json", "number,state,headRefName")
			cmd.Dir = w.Repo
			out, err := cmd.Output()
			rec := prRecord{Head: key, State: "NOPR"}
			if err != nil {
				w.PRState = "NONE" // do not cache a network failure
				return
			}
			var recs []prRecord
			if json.Unmarshal(out, &recs) == nil && len(recs) > 0 {
				rec = recs[0]
				rec.Head = key
			}
			c.mu.Lock()
			rc.entries[key] = rec
			c.mu.Unlock()
			w.PRState, w.PRNumber = rec.State, rec.Number
			tmu.Lock()
			touched[rc] = true
			tmu.Unlock()
		}()
	}
	wg.Wait()
	c.mu.Lock()
	for rc := range touched {
		c.persist(rc)
	}
	c.mu.Unlock()
}
