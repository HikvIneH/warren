package main

import (
	"bufio"
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
)

type Verdict string

const (
	Hold    Verdict = "HOLD"
	Reclaim Verdict = "RECLAIM"
	Idle    Verdict = "IDLE"
	Orphan  Verdict = "ORPHAN"
)

// Worktree is one <repo>/.claude/worktrees/<name> and everything warren
// learned about it. JSON tags are the public contract used by the skill.
type Worktree struct {
	Repo       string  `json:"repo_path"`
	RepoName   string  `json:"repo"`
	Path       string  `json:"path"`
	Name       string  `json:"worktree"`
	Branch     string  `json:"branch"`
	Upstream   string  `json:"-"`
	Dirty      int     `json:"dirty"`
	Unpushed   int     `json:"unpushed"`
	Ignored    string  `json:"ignored,omitempty"`
	InUse      bool    `json:"in_use"`
	Locked     bool    `json:"locked"`
	Unreadable bool    `json:"unreadable"`
	Registered bool    `json:"-"`
	LastCommit int64   `json:"last_commit"`
	PRState    string  `json:"pr_state"`
	PRNumber   int     `json:"pr"`
	Verdict    Verdict `json:"verdict"`
	Reason     string  `json:"reason"`
	SizeKB     int64   `json:"size_kb"`
}

// gitCmd is a variable so tests can point it at a stub.
var gitCmd = "git"

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command(gitCmd, args...)
	cmd.Dir = dir
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// --- discovery ---------------------------------------------------------------

var pruneDirs = map[string]bool{
	"node_modules": true, ".git": true, "Library": true, "vendor": true,
	".venv": true, "venv": true, "target": true, "dist": true, "build": true,
	".next": true, "Pods": true, "DerivedData": true, ".cache": true, ".Trash": true,
}

// findRepos returns every repo under roots that has a non-empty
// .claude/worktrees, as physical (symlink-resolved) paths.
func findRepos(roots []string, maxDepth int) []string {
	seen := map[string]bool{}
	var out []string
	for _, root := range roots {
		root = filepath.Clean(root)
		base := strings.Count(root, string(os.PathSeparator))
		_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			name := d.Name()
			if p != root && pruneDirs[name] {
				return filepath.SkipDir
			}
			if strings.Count(p, string(os.PathSeparator))-base > maxDepth {
				return filepath.SkipDir
			}
			if name == "worktrees" && filepath.Base(filepath.Dir(p)) == ".claude" {
				if ents, err := os.ReadDir(p); err == nil && len(ents) > 0 {
					repo := filepath.Dir(filepath.Dir(p))
					if r, err := filepath.EvalSymlinks(repo); err == nil {
						repo = r
					}
					if !seen[repo] {
						seen[repo] = true
						out = append(out, repo)
					}
				}
				return filepath.SkipDir
			}
			return nil
		})
	}
	sort.Strings(out)
	return out
}

// repoOwning resolves the main repository that owns a path, even from inside
// one of its worktrees.
func repoOwning(p string) (string, error) {
	out, err := git(p, "rev-parse", "--git-common-dir")
	if err != nil {
		return "", err
	}
	common := strings.TrimSpace(out)
	if !filepath.IsAbs(common) {
		common = filepath.Join(p, common)
	}
	repo := filepath.Dir(common)
	if r, err := filepath.EvalSymlinks(repo); err == nil {
		repo = r
	}
	return repo, nil
}

// --- live sessions -----------------------------------------------------------

// inUsePaths returns the cwd of every process owned by this user that sits
// inside a .claude/worktrees directory. One lsof call; without lsof the set
// is empty and warren simply cannot see live sessions.
func inUsePaths() map[string]bool {
	set := map[string]bool{}
	if _, err := exec.LookPath("lsof"); err != nil {
		return set
	}
	out, err := exec.Command("lsof", "-u", strconv.Itoa(os.Getuid()), "-d", "cwd", "-Fn", "-w", "-P", "-n").Output()
	if err != nil && len(out) == 0 {
		return set
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "n") && strings.Contains(line, "/.claude/worktrees/") {
			set[line[1:]] = true
		}
	}
	return set
}

func isInUse(wt string, cwds map[string]bool) bool {
	for c := range cwds {
		if c == wt || strings.HasPrefix(c, wt+"/") {
			return true
		}
	}
	return false
}

// --- per-repo facts ----------------------------------------------------------

type registry struct {
	locked map[string]bool // physical path -> locked
	dates  map[string]int64
}

func readRegistry(repo string) registry {
	r := registry{locked: map[string]bool{}, dates: map[string]int64{}}
	if out, err := git(repo, "worktree", "list", "--porcelain"); err == nil {
		var cur string
		for _, line := range strings.Split(out, "\n") {
			switch {
			case strings.HasPrefix(line, "worktree "):
				cur = line[9:]
				if p, err := filepath.EvalSymlinks(cur); err == nil {
					cur = p
				}
				r.locked[cur] = false
			case line == "locked" || strings.HasPrefix(line, "locked "):
				if cur != "" {
					r.locked[cur] = true
				}
			}
		}
	}
	if out, err := git(repo, "for-each-ref", "--format=%(refname:short)\t%(committerdate:unix)", "refs/heads"); err == nil {
		for _, line := range strings.Split(out, "\n") {
			if i := strings.IndexByte(line, '\t'); i > 0 {
				if ts, err := strconv.ParseInt(line[i+1:], 10, 64); err == nil {
					r.dates[line[:i]] = ts
				}
			}
		}
	}
	return r
}

// junkDirs are cache and dependency directories; nothing inside one is
// something a person would miss.
var junkDirs = map[string]bool{
	"node_modules": true, ".venv": true, "venv": true, "__pycache__": true, ".pytest_cache": true,
	".mypy_cache": true, ".ruff_cache": true, ".cache": true, "dist": true, "build": true, "target": true,
	".next": true, ".nuxt": true, ".turbo": true, ".parcel-cache": true, "coverage": true, ".nyc_output": true,
	"cdk.out": true, ".gradle": true, ".idea": true, "Pods": true, "DerivedData": true, ".terraform": true,
	"vendor": true, ".tox": true, ".eggs": true, ".serverless": true, ".cdk.staging": true,
}

// preciousIgnored reports whether an ignored path is the kind of thing a
// person would miss: secrets, local config, credentials, local databases.
// Build artefacts, caches and logs are ignored for a reason and never count;
// nor does anything that is actually a directory. The list is an allowlist
// on purpose — a denylist of residue will always miss the next build tool.
func preciousIgnored(wt, rel string) bool {
	rel = strings.TrimSuffix(rel, "/")
	for _, part := range strings.Split(filepath.Dir(rel), "/") {
		if junkDirs[part] {
			return false
		}
	}
	if st, err := os.Lstat(filepath.Join(wt, rel)); err != nil || st.IsDir() {
		return false
	}
	base := strings.ToLower(filepath.Base(rel))
	switch base {
	case ".env", ".envrc", ".npmrc", ".pypirc", ".netrc", "kubeconfig", ".pgpass", "terraform.tfstate":
		return true
	}
	if strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".env") || strings.HasPrefix(base, "id_rsa") ||
		strings.HasPrefix(base, "id_ed25519") || strings.HasPrefix(base, "terraform.tfstate") {
		return true
	}
	for _, sfx := range []string{".local", ".local.json", ".local.yml", ".local.yaml", ".local.toml", ".pem", ".key",
		".p12", ".pfx", ".jks", ".keystore", ".sqlite", ".sqlite3", ".db", ".tfvars", ".kubeconfig"} {
		if strings.HasSuffix(base, sfx) {
			return true
		}
	}
	for _, sub := range []string{"secret", "credential", "password", "token"} {
		if strings.Contains(base, sub) {
			return true
		}
	}
	return false
}

// factsFor fills in the git facts for a registered worktree. A git failure
// marks it unreadable and returns; it never panics the whole scan.
func factsFor(w *Worktree, reg registry, allowIgnored bool) {
	out, err := git(w.Path, "status", "--porcelain=v2", "--branch", "--ignored=matching")
	if err != nil {
		w.Unreadable = true
		return
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 64<<10), 1<<24)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "# branch.head "):
			w.Branch = line[14:]
		case strings.HasPrefix(line, "# branch.upstream "):
			w.Upstream = line[18:]
		case strings.HasPrefix(line, "#"):
		case strings.HasPrefix(line, "! "):
			name := line[2:]
			if !allowIgnored && w.Ignored == "" && preciousIgnored(w.Path, name) {
				w.Ignored = name
			}
		case line != "":
			w.Dirty++
		}
	}
	if w.Branch == "" {
		w.Branch = "(detached)"
	}
	out, err = git(w.Path, "rev-list", "--count", "HEAD", "--not", "--remotes")
	if err != nil {
		w.Unreadable = true
		return
	}
	w.Unpushed, _ = strconv.Atoi(strings.TrimSpace(out))
	if ts, ok := reg.dates[w.Branch]; ok {
		w.LastCommit = ts
	} else if out, err := git(w.Path, "log", "-1", "--format=%ct"); err == nil {
		w.LastCommit, _ = strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	}
}

// prKey is the branch name GitHub would know: the upstream if there is one.
func (w *Worktree) prKey() string {
	if w.Branch == "(detached)" {
		return ""
	}
	if w.Upstream != "" {
		if i := strings.IndexByte(w.Upstream, '/'); i > 0 {
			return w.Upstream[i+1:]
		}
	}
	return w.Branch
}

func classify(w *Worktree) {
	switch {
	case w.Verdict == Orphan:
		w.Reason = "unregistered"
	case w.Unreadable:
		w.Verdict, w.Reason = Hold, "git cannot read it"
	case w.Dirty > 0 && w.Unpushed > 0:
		w.Verdict, w.Reason = Hold, fmt.Sprintf("%d uncommitted, %d unpushed", w.Dirty, w.Unpushed)
	case w.Dirty > 0:
		w.Verdict, w.Reason = Hold, fmt.Sprintf("%d uncommitted", w.Dirty)
	case w.Unpushed > 0:
		w.Verdict, w.Reason = Hold, fmt.Sprintf("%d unpushed", w.Unpushed)
	case w.Ignored != "":
		w.Verdict, w.Reason = Hold, "ignored file: "+w.Ignored
	case w.Locked:
		w.Verdict, w.Reason = Hold, "locked"
	case w.InUse:
		w.Verdict, w.Reason = Hold, "session active"
	case w.PRState == "OPEN":
		w.Verdict, w.Reason = Hold, fmt.Sprintf("PR #%d open", w.PRNumber)
	case w.PRState == "MERGED":
		w.Verdict, w.Reason = Reclaim, fmt.Sprintf("PR #%d merged", w.PRNumber)
	case w.PRState == "CLOSED":
		w.Verdict, w.Reason = Reclaim, fmt.Sprintf("PR #%d closed", w.PRNumber)
	default:
		w.Verdict, w.Reason = Idle, "no PR, fully pushed"
	}
}

func sizeKB(p string) int64 {
	out, err := exec.Command("du", "-sk", p).Output()
	if err != nil {
		return -1
	}
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return -1
	}
	n, err := strconv.ParseInt(f[0], 10, 64)
	if err != nil {
		return -1
	}
	return n
}

// --- the scan ----------------------------------------------------------------

type ScanOptions struct {
	Repos    []string // explicit; if empty, discover from roots
	WantSize bool
}

// Scan produces every worktree, classified, across all repos. Repos are
// walked concurrently and worktrees within them concurrently again, bounded
// by cfg.Jobs (default: number of CPUs).
func Scan(cfg *Config, gh *PRCache, opt ScanOptions) []*Worktree {
	repos := opt.Repos
	if len(repos) == 0 {
		repos = findRepos(cfg.roots(), cfg.MaxDepth)
	}
	jobs := cfg.Jobs
	if jobs <= 0 {
		jobs = runtime.NumCPU()
	}
	cwds := inUsePaths()
	sem := make(chan struct{}, jobs)
	var mu sync.Mutex
	var wg sync.WaitGroup
	var all []*Worktree

	for _, repo := range repos {
		repo := repo
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			reg := readRegistry(repo)
			if gh != nil {
				gh.Ensure(repo)
			}
			<-sem
			ents, err := os.ReadDir(filepath.Join(repo, ".claude", "worktrees"))
			if err != nil {
				return
			}
			var rwg sync.WaitGroup
			for _, e := range ents {
				if !e.IsDir() {
					continue
				}
				w := &Worktree{Repo: repo, RepoName: filepath.Base(repo), Name: e.Name(), SizeKB: -1}
				w.Path = filepath.Join(repo, ".claude", "worktrees", e.Name())
				if p, err := filepath.EvalSymlinks(w.Path); err == nil {
					w.Path = p
				}
				rwg.Add(1)
				go func() {
					defer rwg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()
					locked, registered := reg.locked[w.Path]
					w.Registered = registered
					w.Locked = locked
					if !registered {
						w.Verdict, w.Branch, w.PRState = Orphan, "(none)", "NONE"
					} else {
						factsFor(w, reg, cfg.AllowIgnored)
						w.InUse = isInUse(w.Path, cwds)
						w.PRState = "NONE"
						if gh != nil {
							w.PRState, w.PRNumber = gh.Lookup(repo, w.prKey())
						}
					}
					if opt.WantSize {
						w.SizeKB = sizeKB(w.Path)
					}
					mu.Lock()
					all = append(all, w)
					mu.Unlock()
				}()
			}
			rwg.Wait()
		}()
	}
	wg.Wait()

	// Branches the bulk PR page did not cover: ask GitHub about each, once.
	if gh != nil {
		var misses []*Worktree
		for _, w := range all {
			if w.PRState == "MISS" {
				misses = append(misses, w)
			}
		}
		gh.ResolveMisses(misses, jobs)
	}
	for _, w := range all {
		if w.PRState == "MISS" || w.PRState == "NOPR" {
			w.PRState = "NONE"
		}
		classify(w)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].RepoName != all[j].RepoName {
			return all[i].RepoName < all[j].RepoName
		}
		return all[i].Name < all[j].Name
	})
	return all
}
