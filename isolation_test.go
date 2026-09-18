package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsolationGuard(t *testing.T) {
	d := t.TempDir()
	cfg := &Config{ConfigDir: d, CacheDir: d, PathsFile: filepath.Join(d, "paths"), MaxDepth: 7, NoGH: true}
	os.Setenv("WARREN_NO_DEFAULT_ROOTS", "1")
	if r := cfg.roots(); len(r) != 0 {
		t.Fatalf("roots = %v, want none", r)
	}
	if n := len(Scan(cfg, nil, ScanOptions{})); n != 0 {
		t.Fatalf("scan found %d worktrees with no roots", n)
	}
}
