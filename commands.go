package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// scope holds the flags every scanning command shares.
type scope struct {
	here bool
	repo string
	json bool
}

func (s *scope) bind(fs *flag.FlagSet) {
	fs.BoolVar(&s.here, "here", false, "only the repo you are in (works inside a worktree)")
	fs.StringVar(&s.repo, "repo", "", "only the repo owning this path")
	fs.BoolVar(&s.json, "json", false, "machine-readable output")
}

func (s *scope) repos() ([]string, string, error) {
	p := s.repo
	if s.here {
		p, _ = os.Getwd()
	}
	if p == "" {
		return nil, "all", nil
	}
	r, err := repoOwning(p)
	if err != nil {
		return nil, "", fmt.Errorf("%s is not inside a git repository", p)
	}
	if _, err := os.Stat(filepath.Join(r, ".claude", "worktrees")); err != nil {
		return []string{}, r, nil // scoped, but nothing there
	}
	return []string{r}, r, nil
}

func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

func emitJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func doScan(cfg *Config, sc *scope, wantSize bool) ([]*Worktree, string, error) {
	repos, label, err := sc.repos()
	if err != nil {
		return nil, "", err
	}
	if label != "all" && len(repos) == 0 {
		return []*Worktree{}, label, nil
	}
	if !sc.json {
		info("Scanning for Claude worktrees…")
	}
	return Scan(cfg, NewPRCache(cfg), ScanOptions{Repos: repos, WantSize: wantSize}), label, nil
}

// --- list --------------------------------------------------------------------

func cmdList(cfg *Config, args []string) int {
	fs := newFlags("list")
	var sc scope
	sc.bind(fs)
	size := fs.Bool("size", false, "measure disk usage too (slower)")
	only := fs.String("only", "", "show one verdict: hold, reclaim, idle, orphan")
	for _, v := range []string{"hold", "reclaim", "orphan"} {
		v := v
		fs.BoolFunc(v, "shorthand for --only "+v, func(string) error { *only = v; return nil })
	}
	if fs.Parse(args) != nil {
		return 2
	}
	all, _, err := doScan(cfg, &sc, *size)
	if err != nil {
		fail(err.Error())
		return 1
	}
	filter := Verdict(strings.ToUpper(*only))
	var shown []*Worktree
	for _, w := range all {
		if filter == "" || w.Verdict == filter {
			shown = append(shown, w)
		}
	}
	if sc.json {
		if shown == nil {
			shown = []*Worktree{}
		}
		emitJSON(shown)
		return 0
	}
	if len(all) == 0 {
		warn("No Claude worktrees found under: " + strings.Join(cfg.roots(), " "))
		return 0
	}
	prev := ""
	for _, w := range shown {
		if w.RepoName != prev {
			heading(fmt.Sprintf("  %s%s  %s%s", w.RepoName, cGrey, w.Repo, cReset))
			prev = w.RepoName
		}
		fmt.Printf("   %s  %-34.34s %s%-30.30s%s %5s  %s%s%s", paint(w.Verdict), w.Name, cGrey, w.Branch, cReset, humanAge(w.LastCommit), cDim, w.Reason, cReset)
		if w.SizeKB >= 0 {
			fmt.Printf("  %s", humanKB(w.SizeKB))
		}
		fmt.Println()
	}
	summary(all, *size)
	return 0
}

func summary(all []*Worktree, withSize bool) {
	counts := map[Verdict]int{}
	kb := map[Verdict]int64{}
	repos := map[string]bool{}
	var total int64
	for _, w := range all {
		counts[w.Verdict]++
		repos[w.Repo] = true
		if w.SizeKB > 0 {
			kb[w.Verdict] += w.SizeKB
			total += w.SizeKB
		}
	}
	fmt.Println()
	rule(78)
	fmt.Printf("%s%d worktrees across %d repos%s", cBold, len(all), len(repos), cReset)
	if withSize && total > 0 {
		fmt.Printf("  ·  %s on disk", humanKB(total))
	}
	fmt.Println()
	fmt.Printf("  %sreclaim %d%s   %shold %d%s   %sidle %d%s", cGreen, counts[Reclaim], cReset, cYellow, counts[Hold], cReset, cBlue, counts[Idle], cReset)
	if counts[Orphan] > 0 {
		fmt.Printf("   %sorphan %d%s", cRed, counts[Orphan], cReset)
	}
	fmt.Println()
	if r := kb[Reclaim] + kb[Orphan]; withSize && r > 0 {
		fmt.Printf("  %s%s reclaimable%s  ·  run %swarren clean%s\n", cGreen, humanKB(r), cReset, cBold, cReset)
	}
}

// --- analyze -----------------------------------------------------------------

func cmdAnalyze(cfg *Config, args []string) int {
	fs := newFlags("analyze")
	var sc scope
	sc.bind(fs)
	top := fs.Int("top", 15, "how many of the largest worktrees to show")
	if fs.Parse(args) != nil {
		return 2
	}
	if !sc.json {
		info("Measuring worktree sizes (this walks the trees)…")
	}
	sc2 := sc
	sc2.json = true // suppress the second banner
	all, _, err := doScan(cfg, &sc2, true)
	if err != nil {
		fail(err.Error())
		return 1
	}
	if sc.json {
		emitJSON(all)
		return 0
	}
	if len(all) == 0 {
		warn("No Claude worktrees found.")
		return 0
	}
	type agg struct {
		kb  int64
		n   int
		key string
	}
	byRepo := map[string]*agg{}
	for _, w := range all {
		a := byRepo[w.Repo]
		if a == nil {
			a = &agg{key: w.Repo}
			byRepo[w.Repo] = a
		}
		a.n++
		if w.SizeKB > 0 {
			a.kb += w.SizeKB
		}
	}
	var rs []*agg
	for _, a := range byRepo {
		rs = append(rs, a)
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].kb > rs[j].kb })
	heading("By repository")
	for _, a := range rs {
		fmt.Printf("   %8s  %s%3d wt%s  %s\n", humanKB(a.kb), cGrey, a.n, cReset, a.key)
	}
	sorted := append([]*Worktree(nil), all...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].SizeKB > sorted[j].SizeKB })
	heading("Largest worktrees")
	for i, w := range sorted {
		if i >= *top {
			break
		}
		fmt.Printf("   %8s  %s  %-20.20s %-30.30s %s%s%s\n", humanKB(w.SizeKB), paint(w.Verdict), w.RepoName, w.Name, cDim, w.Reason, cReset)
	}
	var r, idle, held int64
	var rc, ic, hc int
	for _, w := range all {
		switch w.Verdict {
		case Reclaim, Orphan:
			r += max64(w.SizeKB, 0)
			rc++
		case Idle:
			idle += max64(w.SizeKB, 0)
			ic++
		default:
			held += max64(w.SizeKB, 0)
			hc++
		}
	}
	heading("Reclaimable")
	fmt.Printf("   %s%9s%s  %d worktrees  safe to remove now (warren clean)\n", cGreen, humanKB(r), cReset, rc)
	fmt.Printf("   %9s  %d worktrees  idle, pushed, no PR (warren clean --idle)\n", humanKB(idle), ic)
	fmt.Printf("   %s%9s%s  %d worktrees  held: unsaved work, live session, or open PR\n", cYellow, humanKB(held), cReset, hc)
	fmt.Println()
	return 0
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// --- clean -------------------------------------------------------------------

type cleanResult struct {
	DryRun        bool        `json:"dry_run"`
	Scope         string      `json:"scope"`
	ReclaimableKB int64       `json:"reclaimable_kb"`
	ReclaimedKB   int64       `json:"reclaimed_kb"`
	Planned       int         `json:"planned"`
	Removed       int         `json:"removed"`
	Failed        int         `json:"failed"`
	Skipped       int         `json:"skipped"`
	Held          int         `json:"held"`
	Idle          int         `json:"idle"`
	Worktrees     []cleanItem `json:"worktrees"`
	HeldWorktrees []brief     `json:"held_worktrees"`
	IdleWorktrees []brief     `json:"idle_worktrees"`
}

type cleanItem struct {
	Repo     string  `json:"repo"`
	Worktree string  `json:"worktree"`
	Path     string  `json:"path"`
	Branch   string  `json:"branch"`
	Verdict  Verdict `json:"verdict"`
	Reason   string  `json:"reason"`
	SizeKB   int64   `json:"size_kb"`
	Outcome  string  `json:"outcome"`
	Detail   string  `json:"detail,omitempty"`
}

type brief struct {
	Repo     string `json:"repo"`
	Worktree string `json:"worktree"`
	Reason   string `json:"reason"`
}

func cmdClean(cfg *Config, args []string) int {
	fs := newFlags("clean")
	var sc scope
	sc.bind(fs)
	dry := fs.Bool("dry-run", false, "preview; never delete")
	fs.BoolVar(dry, "n", false, "shorthand for --dry-run")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	fs.BoolVar(yes, "y", false, "shorthand for --yes")
	idle := fs.Bool("idle", false, "also remove pushed worktrees that have no PR")
	branches := fs.Bool("branches", false, "delete the local branch too")
	noSize := fs.Bool("no-size", false, "skip measuring sizes")
	fs.BoolVar(&cfg.AllowIgnored, "allow-ignored", false, "do not hold worktrees just for gitignored files")
	if fs.Parse(args) != nil {
		return 2
	}
	if sc.json && !*dry && !*yes {
		emitJSON(map[string]string{"error": "refusing to remove without --yes", "hint": "use --dry-run to plan, or add --yes to execute"})
		return 2
	}
	all, label, err := doScan(cfg, &sc, !*noSize)
	if err != nil {
		fail(err.Error())
		return 1
	}
	res := cleanResult{DryRun: *dry, Scope: label, Worktrees: []cleanItem{}, HeldWorktrees: []brief{}, IdleWorktrees: []brief{}}
	var cands []*Worktree
	for _, w := range all {
		switch w.Verdict {
		case Reclaim, Orphan:
			cands = append(cands, w)
		case Idle:
			res.Idle++
			res.IdleWorktrees = append(res.IdleWorktrees, brief{w.RepoName, w.Name, w.Reason})
			if *idle {
				cands = append(cands, w)
			}
		case Hold:
			res.Held++
			res.HeldWorktrees = append(res.HeldWorktrees, brief{w.RepoName, w.Name, w.Reason})
		}
	}
	for _, w := range cands {
		res.ReclaimableKB += max64(w.SizeKB, 0)
	}
	res.Planned = len(cands)

	if !sc.json {
		if len(cands) == 0 {
			good("Nothing to reclaim — every worktree is holding work.")
			if !*idle && res.Idle > 0 {
				fmt.Printf("%s(%d idle worktrees excluded; add --idle to include them)%s\n", cDim, res.Idle, cReset)
			}
			return 0
		}
		heading("Removable worktrees")
		for _, w := range cands {
			fmt.Printf("   %s  %-22.22s %-30.30s %s%s%s", paint(w.Verdict), w.RepoName, w.Name, cDim, w.Reason, cReset)
			if w.SizeKB >= 0 {
				fmt.Printf("  %s", humanKB(w.SizeKB))
			}
			fmt.Println()
		}
		fmt.Println()
		rule(78)
		fmt.Printf("%s%d worktrees%s", cBold, len(cands), cReset)
		if res.ReclaimableKB > 0 {
			fmt.Printf("  ·  %s%s to reclaim%s", cGreen, humanKB(res.ReclaimableKB), cReset)
		}
		fmt.Println()
		if res.Held > 0 {
			fmt.Printf("  %s%d held back (unsaved work, ignored files, locked, live session, or open PR)%s\n", cYellow, res.Held, cReset)
		}
		if !*idle && res.Idle > 0 {
			fmt.Printf("  %s%d idle, not included (add --idle)%s\n", cBlue, res.Idle, cReset)
		}
		if *dry {
			fmt.Printf("\n%sDry run — nothing was removed.%s\n", cCyan, cReset)
			return 0
		}
		fmt.Println()
		if !confirm(fmt.Sprintf("Remove these %d worktrees? [y/N]", len(cands)), *yes) {
			fmt.Println("Cancelled.")
			return 0
		}
		fmt.Println()
	}

	for _, w := range cands {
		item := cleanItem{w.RepoName, w.Name, w.Path, w.Branch, w.Verdict, w.Reason, w.SizeKB, "planned", ""}
		if !*dry {
			item.Outcome, item.Detail = removeOne(w, *branches)
			switch item.Outcome {
			case "removed":
				res.Removed++
				res.ReclaimedKB += max64(w.SizeKB, 0)
				cfg.log("removed %s (%s)", w.Path, w.Reason)
				if !sc.json {
					good(fmt.Sprintf("   removed  %s/%s", w.RepoName, w.Name))
				}
			case "skipped":
				res.Skipped++
				if !sc.json {
					warn(fmt.Sprintf("   skipped  %s/%s — %s", w.RepoName, w.Name, item.Detail))
				}
			default:
				res.Failed++
				if !sc.json {
					fail(fmt.Sprintf("   failed   %s/%s — %s", w.RepoName, w.Name, item.Detail))
				}
			}
		}
		res.Worktrees = append(res.Worktrees, item)
	}
	if sc.json {
		emitJSON(res)
	} else if !*dry {
		fmt.Println()
		good(fmt.Sprintf("Removed %d worktree(s).", res.Removed))
		if res.Skipped > 0 {
			warn(fmt.Sprintf("%d skipped — they changed since the scan.", res.Skipped))
		}
		if res.Failed > 0 {
			fail(fmt.Sprintf("%d could not be removed.", res.Failed))
		}
		if res.ReclaimedKB > 0 {
			good("Reclaimed about " + humanKB(res.ReclaimedKB) + ".")
		}
	}
	if res.Failed > 0 {
		return 1
	}
	return 0
}

// --- prune -------------------------------------------------------------------

func cmdPrune(cfg *Config, args []string) int {
	fs := newFlags("prune")
	var sc scope
	sc.bind(fs)
	dry := fs.Bool("dry-run", false, "preview; never delete")
	fs.BoolVar(dry, "n", false, "shorthand for --dry-run")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	fs.BoolVar(yes, "y", false, "shorthand for --yes")
	if fs.Parse(args) != nil {
		return 2
	}
	repos, _, err := sc.repos()
	if err != nil {
		fail(err.Error())
		return 1
	}
	if len(repos) == 0 && !sc.here && sc.repo == "" {
		repos = findRepos(cfg.roots(), cfg.MaxDepth)
	}
	type staleEntry struct {
		Repo  string `json:"repo"`
		Lines string `json:"lines"`
	}
	var stale []staleEntry
	for _, r := range repos {
		out, _ := git(r, "worktree", "prune", "--verbose", "--dry-run")
		if s := strings.TrimSpace(out); s != "" {
			stale = append(stale, staleEntry{r, s})
			if !*dry {
				_, _ = git(r, "worktree", "prune")
			}
		}
	}
	all := Scan(cfg, nil, ScanOptions{Repos: repos})
	var orphans []*Worktree
	for _, w := range all {
		if w.Verdict == Orphan {
			orphans = append(orphans, w)
		}
	}
	if sc.json {
		if !*dry && !*yes {
			emitJSON(map[string]string{"error": "refusing to remove without --yes"})
			return 2
		}
		type out struct {
			DryRun  bool         `json:"dry_run"`
			Stale   []staleEntry `json:"stale_registrations"`
			Orphans []cleanItem  `json:"orphans"`
		}
		o := out{DryRun: *dry, Stale: stale, Orphans: []cleanItem{}}
		if o.Stale == nil {
			o.Stale = []staleEntry{}
		}
		for _, w := range orphans {
			it := cleanItem{w.RepoName, w.Name, w.Path, w.Branch, w.Verdict, w.Reason, w.SizeKB, "planned", ""}
			if !*dry {
				it.Outcome, it.Detail = removeOne(w, false)
				if it.Outcome == "removed" {
					cfg.log("pruned orphan %s", w.Path)
				}
			}
			o.Orphans = append(o.Orphans, it)
		}
		emitJSON(o)
		return 0
	}
	heading("Stale git registrations")
	if len(stale) == 0 {
		fmt.Printf("   %snone%s\n", cDim, cReset)
	}
	for _, s := range stale {
		fmt.Printf("   %s\n", filepath.Base(s.Repo))
		for _, l := range strings.Split(s.Lines, "\n") {
			fmt.Printf("      %s\n", l)
		}
	}
	heading("Directories git no longer tracks (orphans)")
	if len(orphans) == 0 {
		fmt.Printf("   %snone%s\n", cDim, cReset)
		fmt.Println()
		return 0
	}
	for _, w := range orphans {
		fmt.Printf("   %s%s/%s%s  %s\n", cRed, w.RepoName, w.Name, cReset, w.Path)
	}
	fmt.Println()
	if *dry {
		fmt.Printf("%sDry run — nothing removed.%s\n", cCyan, cReset)
		return 0
	}
	if !confirm(fmt.Sprintf("Delete these %d orphaned directories? [y/N]", len(orphans)), *yes) {
		fmt.Println("Cancelled.")
		return 0
	}
	rc := 0
	for _, w := range orphans {
		o, d := removeOne(w, false)
		switch o {
		case "removed":
			good(fmt.Sprintf("   removed  %s/%s", w.RepoName, w.Name))
			cfg.log("pruned orphan %s", w.Path)
		case "skipped":
			warn(fmt.Sprintf("   skipped  %s/%s — %s", w.RepoName, w.Name, d))
		default:
			fail(fmt.Sprintf("   failed   %s/%s — %s", w.RepoName, w.Name, d))
			rc = 1
		}
	}
	fmt.Println()
	return rc
}

// --- path / paths / history --------------------------------------------------

func cmdPath(cfg *Config, args []string) int {
	fs := newFlags("path")
	var sc scope
	sc.bind(fs)
	if fs.Parse(args) != nil || fs.NArg() != 1 {
		fail("usage: warren path <worktree-name> [--here|--repo <path>]")
		return 2
	}
	q := fs.Arg(0)
	sc.json = true
	all, _, err := doScan(cfg, &sc, false)
	if err != nil {
		fail(err.Error())
		return 1
	}
	var hits []*Worktree
	for _, w := range all {
		if w.Name == q {
			hits = []*Worktree{w}
			break
		}
		if strings.Contains(w.Name, q) || strings.Contains(w.RepoName+"/"+w.Name, q) {
			hits = append(hits, w)
		}
	}
	switch len(hits) {
	case 0:
		fail("No worktree matching: " + q)
		return 1
	case 1:
		fmt.Println(hits[0].Path)
		return 0
	}
	fail(fmt.Sprintf("%d worktrees match %q:", len(hits), q))
	for _, w := range hits {
		fmt.Fprintf(os.Stderr, "  %s/%s\n", w.RepoName, w.Name)
	}
	return 1
}

func cmdPaths(cfg *Config, args []string) int {
	roots := cfg.roots()
	if len(args) == 0 {
		heading("Directories warren scans")
		for _, r := range roots {
			fmt.Printf("   %s\n", r)
		}
		if _, err := os.Stat(cfg.PathsFile); err == nil {
			fmt.Printf("\n%sfrom %s%s\n", cDim, cfg.PathsFile, cReset)
		} else {
			fmt.Printf("\n%sdefault — set with: warren paths add <dir>%s\n", cDim, cReset)
		}
		return 0
	}
	switch args[0] {
	case "add":
		if len(args) != 2 {
			fail("usage: warren paths add <dir>")
			return 2
		}
		abs, _ := filepath.Abs(expandHome(args[1]))
		for _, r := range roots {
			if r == abs {
				fmt.Println("already listed:", abs)
				return 0
			}
		}
		if err := cfg.writeRoots(append(roots, abs)); err != nil {
			fail(err.Error())
			return 1
		}
		good("added " + abs)
	case "remove", "rm":
		if len(args) != 2 {
			fail("usage: warren paths remove <dir>")
			return 2
		}
		abs, _ := filepath.Abs(expandHome(args[1]))
		var keep []string
		for _, r := range roots {
			if r != abs {
				keep = append(keep, r)
			}
		}
		if err := cfg.writeRoots(keep); err != nil {
			fail(err.Error())
			return 1
		}
		good("removed " + abs)
	case "reset":
		_ = os.Remove(cfg.PathsFile)
		good("back to defaults")
	default:
		fail("usage: warren paths [add <dir> | remove <dir> | reset]")
		return 2
	}
	return 0
}

func cmdHistory(cfg *Config) int {
	heading("Removal history")
	b, err := os.ReadFile(cfg.LogFile)
	if err != nil || len(b) == 0 {
		fmt.Printf("   %snothing removed yet%s\n\n", cDim, cReset)
		return 0
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) > 50 {
		lines = lines[len(lines)-50:]
	}
	for _, l := range lines {
		if i := strings.IndexByte(l, '\t'); i > 0 {
			fmt.Printf("   %s%s%s  %s\n", cGrey, l[:i], cReset, l[i+1:])
		}
	}
	fmt.Println()
	return 0
}
