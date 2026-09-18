package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// Belt and braces with newFixture's paths file: with no roots configured
	// warren finds nothing, instead of walking the developer's home directory.
	os.Setenv("WARREN_NO_DEFAULT_ROOTS", "1")
	os.Setenv("WARREN_NO_GH", "true")
	os.Exit(m.Run())
}

// --- fixture ------------------------------------------------------------------

type fixture struct {
	t    *testing.T
	root string // parent of repo; use as a scan root
	repo string // physical path
	cfg  *Config
}

func run(t *testing.T, dir string, name string, args ...string) string {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v in %s: %v\n%s", name, args, dir, err, out)
	}
	return string(out)
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	base := t.TempDir()
	root := filepath.Join(base, "work")
	bare := filepath.Join(base, "origin.git")
	repo := filepath.Join(root, "repo")
	run(t, base, "git", "init", "-q", "--bare", bare)
	run(t, base, "git", "clone", "-q", bare, repo)
	run(t, repo, "git", "config", "user.email", "t@example.com")
	run(t, repo, "git", "config", "user.name", "t")
	run(t, repo, "git", "symbolic-ref", "HEAD", "refs/heads/main")
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("base\n"), 0o644)
	os.WriteFile(filepath.Join(repo, ".gitignore"), []byte(".env\n*.log\n*.local.json\n.claude/\n"), 0o644)
	run(t, repo, "git", "add", "-A")
	run(t, repo, "git", "commit", "-qm", "base")
	run(t, repo, "git", "push", "-q", "-u", "origin", "main")
	os.MkdirAll(filepath.Join(repo, ".claude", "worktrees"), 0o755)
	if p, err := filepath.EvalSymlinks(repo); err == nil {
		repo = p
	}
	cfgDir := filepath.Join(base, "cfg")
	cfg := &Config{ConfigDir: cfgDir, CacheDir: filepath.Join(base, "cache"), PathsFile: filepath.Join(cfgDir, "paths"),
		LogFile: filepath.Join(cfgDir, "history.log"), PRTTL: 6 * time.Hour, MaxDepth: 7, NoGH: true}
	os.MkdirAll(cfgDir, 0o755)
	os.MkdirAll(filepath.Join(cfg.CacheDir, "pr"), 0o755)
	// Never let a command-level test fall back to the default roots: that
	// would scan — and could clean — the developer's real repositories.
	if err := cfg.writeRoots([]string{root}); err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, root: root, repo: repo, cfg: cfg}
}

func (f *fixture) wt(name string) string { return filepath.Join(f.repo, ".claude", "worktrees", name) }

// addPushed creates a worktree on a new branch with one commit, pushed.
func (f *fixture) addPushed(name string) string {
	p := f.wt(name)
	run(f.t, f.repo, "git", "worktree", "add", "-q", "-b", "br-"+name, p)
	os.WriteFile(filepath.Join(p, name+".txt"), []byte("x\n"), 0o644)
	run(f.t, p, "git", "add", "-A")
	run(f.t, p, "git", "commit", "-qm", name)
	run(f.t, p, "git", "push", "-q", "-u", "origin", "br-"+name)
	return p
}

func (f *fixture) scan() map[string]*Worktree {
	return f.scanWith(nil)
}

func (f *fixture) scanWith(gh *PRCache) map[string]*Worktree {
	out := map[string]*Worktree{}
	for _, w := range Scan(f.cfg, gh, ScanOptions{Repos: []string{f.repo}}) {
		out[w.Name] = w
	}
	return out
}

func want(t *testing.T, w *Worktree, v Verdict, reasonContains string) {
	t.Helper()
	if w == nil {
		t.Fatalf("worktree missing from scan")
	}
	if w.Verdict != v {
		t.Errorf("%s: verdict %s, want %s (reason %q)", w.Name, w.Verdict, v, w.Reason)
	}
	if reasonContains != "" && !strings.Contains(w.Reason, reasonContains) {
		t.Errorf("%s: reason %q, want it to mention %q", w.Name, w.Reason, reasonContains)
	}
}

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// --- classification -------------------------------------------------------------

func TestVerdicts(t *testing.T) {
	f := newFixture(t)
	f.addPushed("pushed")

	dirty := f.addPushed("dirty")
	os.WriteFile(filepath.Join(dirty, "scratch.txt"), []byte("wip\n"), 0o644)

	local := f.addPushed("local")
	os.WriteFile(filepath.Join(local, "more.txt"), []byte("y\n"), 0o644)
	run(t, local, "git", "add", "-A")
	run(t, local, "git", "commit", "-qm", "local-only")

	os.MkdirAll(f.wt("orphan"), 0o755)
	os.WriteFile(filepath.Join(f.wt("orphan"), "junk"), []byte("j\n"), 0o644)

	run(t, f.repo, "git", "worktree", "add", "-q", "--detach", f.wt("detached"), "main")

	locked := f.addPushed("locked")
	run(t, f.repo, "git", "worktree", "lock", locked)

	env := f.addPushed("env")
	os.WriteFile(filepath.Join(env, ".env"), []byte("DB_PASSWORD=hunter2\n"), 0o644)

	logs := f.addPushed("logs")
	os.WriteFile(filepath.Join(logs, "debug.log"), []byte("noise\n"), 0o644)

	local2 := f.addPushed("localcfg")
	os.WriteFile(filepath.Join(local2, "settings.local.json"), []byte("{}\n"), 0o644)

	ws := f.scan()
	want(t, ws["pushed"], Idle, "no PR")
	want(t, ws["dirty"], Hold, "uncommitted")
	want(t, ws["local"], Hold, "unpushed")
	want(t, ws["orphan"], Orphan, "unregistered")
	want(t, ws["detached"], Idle, "")
	want(t, ws["locked"], Hold, "locked")
	want(t, ws["env"], Hold, "ignored file: .env")
	want(t, ws["logs"], Idle, "") // *.log is residue, not something you'd miss
	want(t, ws["localcfg"], Hold, "ignored file: settings.local.json")
	if ws["env"].Ignored != ".env" {
		t.Errorf("Ignored = %q, want .env", ws["env"].Ignored)
	}
}

func TestAllowIgnoredFlag(t *testing.T) {
	f := newFixture(t)
	env := f.addPushed("env")
	os.WriteFile(filepath.Join(env, ".env"), []byte("x\n"), 0o644)
	f.cfg.AllowIgnored = true
	want(t, f.scan()["env"], Idle, "")
}

// A git failure in one worktree must not lose the others in the same repo.
func TestBrokenWorktreeDoesNotDropSiblings(t *testing.T) {
	f := newFixture(t)
	broken := f.addPushed("aaa-broken")
	f.addPushed("bbb-fine")
	f.addPushed("ccc-fine")
	os.WriteFile(filepath.Join(broken, ".git"), []byte("gitdir: /nonexistent/gitdir\n"), 0o644)
	ws := f.scan()
	if len(ws) != 3 {
		t.Fatalf("got %d worktrees, want 3: %v", len(ws), ws)
	}
	want(t, ws["aaa-broken"], Hold, "cannot read")
	want(t, ws["bbb-fine"], Idle, "")
	want(t, ws["ccc-fine"], Idle, "")
}

func TestInUseIsExactNotPrefix(t *testing.T) {
	if _, err := exec.LookPath("lsof"); err != nil {
		t.Skip("lsof not installed")
	}
	f := newFixture(t)
	f.addPushed("wt-a")
	ab := f.addPushed("wt-ab")
	sleep := exec.Command("sleep", "60")
	sleep.Dir = ab
	if err := sleep.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { sleep.Process.Kill(); sleep.Wait() }()
	time.Sleep(300 * time.Millisecond)
	ws := f.scan()
	want(t, ws["wt-ab"], Hold, "session active")
	want(t, ws["wt-a"], Idle, "")
}

func TestClassifyPrecedence(t *testing.T) {
	w := &Worktree{Dirty: 2, Unpushed: 1, Locked: true, InUse: true, PRState: "MERGED", PRNumber: 9}
	classify(w)
	if w.Verdict != Hold || w.Reason != "2 uncommitted, 1 unpushed" {
		t.Errorf("got %s %q", w.Verdict, w.Reason)
	}
	w = &Worktree{PRState: "MERGED", PRNumber: 9}
	classify(w)
	if w.Verdict != Reclaim || w.Reason != "PR #9 merged" {
		t.Errorf("got %s %q", w.Verdict, w.Reason)
	}
	w = &Worktree{PRState: "OPEN", PRNumber: 3, Locked: true}
	classify(w)
	if w.Reason != "locked" {
		t.Errorf("locked should outrank PR state, got %q", w.Reason)
	}
}

func TestPRKeyPrefersUpstream(t *testing.T) {
	w := &Worktree{Branch: "local-name", Upstream: "origin/remote-name"}
	if k := w.prKey(); k != "remote-name" {
		t.Errorf("prKey = %q", k)
	}
	w = &Worktree{Branch: "(detached)"}
	if k := w.prKey(); k != "" {
		t.Errorf("detached prKey = %q, want empty", k)
	}
}

// --- discovery ---------------------------------------------------------------------

func TestFindReposHandlesSpacesAndPrunes(t *testing.T) {
	f := newFixture(t)
	f.addPushed("x")
	spaced := filepath.Join(t.TempDir(), "my work")
	os.MkdirAll(spaced, 0o755)
	// a decoy inside node_modules must not be walked
	decoy := filepath.Join(spaced, "node_modules", "pkg", ".claude", "worktrees", "wt")
	os.MkdirAll(decoy, 0o755)
	os.Symlink(f.repo, filepath.Join(spaced, "repo-link"))
	got := findRepos([]string{spaced, f.root}, 7)
	if len(got) != 1 || got[0] != f.repo {
		t.Errorf("findRepos = %v, want [%s]", got, f.repo)
	}
}

func TestRepoOwningFromInsideWorktree(t *testing.T) {
	f := newFixture(t)
	p := f.addPushed("inner")
	got, err := repoOwning(p)
	if err != nil || got != f.repo {
		t.Errorf("repoOwning(%s) = %q, %v; want %s", p, got, err, f.repo)
	}
}

// --- output contract ------------------------------------------------------------

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	fn()
	w.Close()
	os.Stdout = old
	var b bytes.Buffer
	io.Copy(&b, r)
	return b.String()
}

func TestListJSONEmptyIsArray(t *testing.T) {
	f := newFixture(t)
	empty := filepath.Join(t.TempDir(), "nothing")
	os.MkdirAll(empty, 0o755)
	f.cfg.writeRoots([]string{empty})
	out := captureStdout(t, func() { cmdList(f.cfg, []string{"--json"}) })
	if strings.TrimSpace(out) != "[]" {
		t.Errorf("stdout = %q, want []", out)
	}
}

func TestJSONEscapesQuotesInBranchNames(t *testing.T) {
	f := newFixture(t)
	p := f.wt("quote")
	run(t, f.repo, "git", "worktree", "add", "-q", "-b", `feat/say-"hi"`, p)
	run(t, p, "git", "push", "-q", "-u", "origin", `feat/say-"hi"`)
	out := captureStdout(t, func() { emitJSON(Scan(f.cfg, nil, ScanOptions{Repos: []string{f.repo}})) })
	var back []Worktree
	if err := json.Unmarshal([]byte(out), &back); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if back[0].Branch != `feat/say-"hi"` {
		t.Errorf("branch round-trip = %q", back[0].Branch)
	}
}

func TestCleanJSONRefusesWithoutYes(t *testing.T) {
	f := newFixture(t)
	f.addPushed("x")
	var rc int
	out := captureStdout(t, func() { rc = cmdClean(f.cfg, []string{"--json"}) })
	if rc != 2 || !strings.Contains(out, "refusing") {
		t.Errorf("rc=%d out=%q", rc, out)
	}
	if !exists(f.wt("x")) {
		t.Error("worktree was removed by a refused call")
	}
}

func TestCleanPlanReportsIdleAndHeld(t *testing.T) {
	f := newFixture(t)
	f.addPushed("idle-one")
	d := f.addPushed("held-one")
	os.WriteFile(filepath.Join(d, "wip"), []byte("w\n"), 0o644)
	os.MkdirAll(f.wt("orphan-one"), 0o755)
	out := captureStdout(t, func() { cmdClean(f.cfg, []string{"--dry-run", "--json", "--no-size"}) })
	var res cleanResult
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, out)
	}
	if res.Planned != 1 || res.Held != 1 || res.Idle != 1 || len(res.IdleWorktrees) != 1 || len(res.HeldWorktrees) != 1 {
		t.Errorf("plan = %+v", res)
	}
	if exists(f.wt("orphan-one")) == false {
		t.Error("dry run removed something")
	}
}

// --- removal -------------------------------------------------------------------------

func TestRemoveGuards(t *testing.T) {
	f := newFixture(t)
	dirty := f.addPushed("dirty")
	os.WriteFile(filepath.Join(dirty, "scratch"), []byte("s\n"), 0o644)
	local := f.addPushed("local")
	os.WriteFile(filepath.Join(local, "c"), []byte("c\n"), 0o644)
	run(t, local, "git", "add", "-A")
	run(t, local, "git", "commit", "-qm", "local-only")
	locked := f.addPushed("locked")
	run(t, f.repo, "git", "worktree", "lock", locked)
	f.addPushed("idle")
	os.MkdirAll(f.wt("orphan"), 0o755)
	os.WriteFile(filepath.Join(f.wt("orphan"), "j"), []byte("j\n"), 0o644)
	nested := f.wt("nested")
	os.MkdirAll(nested, 0o755)
	run(t, nested, "git", "init", "-q")
	os.WriteFile(filepath.Join(nested, "uncommitted.txt"), []byte("secret\n"), 0o644)

	ws := f.scan()
	for _, c := range []struct {
		name, outcome string
		survives      bool
	}{
		{"dirty", "skipped", true},
		{"local", "skipped", true},
		{"locked", "failed", true}, // git refuses; no rm -rf fallback
		{"nested", "skipped", true},
		{"orphan", "removed", false},
		{"idle", "removed", false},
	} {
		w := ws[c.name]
		if w == nil {
			t.Fatalf("%s missing", c.name)
		}
		got, detail := removeOne(w, false)
		if got != c.outcome {
			t.Errorf("%s: outcome %s (%s), want %s", c.name, got, detail, c.outcome)
		}
		if exists(w.Path) != c.survives {
			t.Errorf("%s: on disk = %v, want %v", c.name, exists(w.Path), c.survives)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(nested, "uncommitted.txt")); string(b) != "secret\n" {
		t.Error("nested repo's uncommitted file was lost")
	}
}

func TestCleanIdleRemovesOnlyTheSafeOnes(t *testing.T) {
	f := newFixture(t)
	f.addPushed("idle")
	d := f.addPushed("dirty")
	os.WriteFile(filepath.Join(d, "wip"), []byte("w\n"), 0o644)
	var rc int
	out := captureStdout(t, func() { rc = cmdClean(f.cfg, []string{"--idle", "--yes", "--json", "--no-size"}) })
	var res cleanResult
	if err := json.Unmarshal([]byte(out), &res); err != nil || rc != 0 {
		t.Fatalf("rc=%d err=%v out=%s", rc, err, out)
	}
	if res.Removed != 1 || exists(f.wt("idle")) || !exists(f.wt("dirty")) {
		t.Errorf("removed=%d idle exists=%v dirty exists=%v", res.Removed, exists(f.wt("idle")), exists(f.wt("dirty")))
	}
}

// --- GitHub cache -----------------------------------------------------------------

// ghStub writes a fake gh that prints `bulk` for `pr list --state all` and
// `head` for `pr list --head ...`; empty strings mean exit 1 (offline).
func ghStub(t *testing.T, bulk, head string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "gh")
	script := "#!/bin/sh\ncase \"$*\" in\n*--head*) " + stubBranch(head) + " ;;\n*) " + stubBranch(bulk) + " ;;\nesac\n"
	os.WriteFile(p, []byte(script), 0o755)
	return p
}

func stubBranch(payload string) string {
	if payload == "" {
		return "exit 1"
	}
	return "printf '%s' '" + payload + "'"
}

func TestPRStatesFromBulk(t *testing.T) {
	f := newFixture(t)
	f.cfg.NoGH = false
	f.addPushed("merged")
	f.addPushed("open")
	f.addPushed("closed")
	f.addPushed("nopr")
	old := ghCmd
	ghCmd = ghStub(t, `[{"number":1,"state":"MERGED","headRefName":"br-merged"},{"number":2,"state":"OPEN","headRefName":"br-open"},{"number":3,"state":"CLOSED","headRefName":"br-closed"}]`, `[]`)
	defer func() { ghCmd = old }()
	gh := NewPRCache(f.cfg)
	if !gh.Enabled() {
		t.Fatal("stub gh not found")
	}
	ws := f.scanWith(gh)
	want(t, ws["merged"], Reclaim, "PR #1 merged")
	want(t, ws["open"], Hold, "PR #2 open")
	want(t, ws["closed"], Reclaim, "PR #3 closed")
	want(t, ws["nopr"], Idle, "no PR")
	if !exists(gh.fileFor(f.repo) + ".stamp") {
		t.Error("no stamp written after a successful bulk fetch")
	}
	// second scan must not call --head again for nopr: prove by making --head fail
	ghCmd = ghStub(t, `[]`, ``)
	gh2 := NewPRCache(f.cfg)
	want(t, f.scanWith(gh2)["nopr"], Idle, "")
	if s, _ := gh2.Lookup(f.repo, "br-nopr"); s != "NOPR" {
		t.Errorf("no-PR answer not remembered, got %s", s)
	}
}

func TestPRFallbackForBranchMissingFromBulk(t *testing.T) {
	f := newFixture(t)
	f.cfg.NoGH = false
	f.addPushed("late")
	old := ghCmd
	ghCmd = ghStub(t, `[]`, `[{"number":44,"state":"OPEN","headRefName":"br-late"}]`)
	defer func() { ghCmd = old }()
	want(t, f.scanWith(NewPRCache(f.cfg))["late"], Hold, "PR #44 open")
}

func TestGHFailureKeepsStaleCache(t *testing.T) {
	f := newFixture(t)
	f.cfg.NoGH = false
	f.addPushed("merged")
	old := ghCmd
	defer func() { ghCmd = old }()
	// seed a valid-but-expired cache
	gh := NewPRCache(f.cfg)
	file := gh.fileFor(f.repo)
	os.WriteFile(file, []byte("br-merged\tMERGED\t7\n"), 0o644)
	stale := time.Now().Add(-48 * time.Hour)
	os.WriteFile(file+".stamp", []byte("0"), 0o644)
	os.Chtimes(file+".stamp", stale, stale)
	ghCmd = ghStub(t, ``, ``) // offline
	ws := f.scanWith(NewPRCache(f.cfg))
	want(t, ws["merged"], Reclaim, "PR #7 merged")
	if b, _ := os.ReadFile(file); !strings.Contains(string(b), "MERGED") {
		t.Errorf("cache was wiped on failure: %q", b)
	}
}
