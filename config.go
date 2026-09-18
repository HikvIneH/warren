package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const version = "0.1.0"

// Config is everything that can come from the environment or the paths file.
type Config struct {
	ConfigDir string
	CacheDir  string
	PathsFile string
	LogFile   string
	PRTTL     time.Duration
	MaxDepth  int
	Jobs      int
	NoGH      bool
	// AllowIgnored turns off the "ignored file present" hold.
	AllowIgnored bool
}

func loadConfig() *Config {
	home, _ := os.UserHomeDir()
	xdgConf := envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	xdgCache := envOr("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	c := &Config{
		ConfigDir: envOr("WARREN_CONFIG_DIR", filepath.Join(xdgConf, "warren")),
		CacheDir:  envOr("WARREN_CACHE_DIR", filepath.Join(xdgCache, "warren")),
		PRTTL:     6 * time.Hour,
		MaxDepth:  7,
		Jobs:      0,
		NoGH:      os.Getenv("WARREN_NO_GH") == "true",
	}
	c.PathsFile = envOr("WARREN_PATHS_FILE", filepath.Join(c.ConfigDir, "paths"))
	c.LogFile = filepath.Join(c.ConfigDir, "history.log")
	if v, err := strconv.Atoi(os.Getenv("WARREN_PR_TTL")); err == nil && v > 0 {
		c.PRTTL = time.Duration(v) * time.Second
	}
	if v, err := strconv.Atoi(os.Getenv("WARREN_MAXDEPTH")); err == nil && v > 0 {
		c.MaxDepth = v
	}
	if v, err := strconv.Atoi(os.Getenv("WARREN_JOBS")); err == nil && v > 0 {
		c.Jobs = v
	}
	_ = os.MkdirAll(c.ConfigDir, 0o755)
	_ = os.MkdirAll(filepath.Join(c.CacheDir, "pr"), 0o755)
	return c
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// roots returns the directories to search for repos.
func (c *Config) roots() []string {
	if f, err := os.Open(c.PathsFile); err == nil {
		defer f.Close()
		var out []string
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			out = append(out, expandHome(line))
		}
		if len(out) > 0 {
			return out
		}
	}
	if os.Getenv("WARREN_NO_DEFAULT_ROOTS") != "" {
		return nil
	}
	home, _ := os.UserHomeDir()
	for _, d := range []string{"Developer", "Projects", "code", "src"} {
		p := filepath.Join(home, d)
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return []string{p}
		}
	}
	return []string{home}
}

func (c *Config) writeRoots(roots []string) error {
	var b strings.Builder
	b.WriteString("# Directories warren scans for <repo>/.claude/worktrees. One per line.\n")
	for _, r := range roots {
		b.WriteString(r + "\n")
	}
	return os.WriteFile(c.PathsFile, []byte(b.String()), 0o644)
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		home, _ := os.UserHomeDir()
		return filepath.Join(home, p[2:])
	}
	return p
}

func (c *Config) log(format string, a ...any) {
	f, err := os.OpenFile(c.LogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s\t%s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, a...))
}
