package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"
)

// Colors are on only when stdout is a terminal and NO_COLOR is unset.
var (
	cReset, cBold, cDim, cRed, cGreen, cYellow, cBlue, cCyan, cGrey string
)

func initColors() {
	if os.Getenv("NO_COLOR") != "" {
		return
	}
	fi, err := os.Stdout.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return
	}
	cReset, cBold, cDim = "\033[0m", "\033[1m", "\033[2m"
	cRed, cGreen, cYellow = "\033[0;31m", "\033[0;32m", "\033[0;33m"
	cBlue, cCyan, cGrey = "\033[0;34m", "\033[0;36m", "\033[0;90m"
}

func info(s string)    { fmt.Println(cCyan + s + cReset) }
func good(s string)    { fmt.Println(cGreen + s + cReset) }
func warn(s string)    { fmt.Fprintln(os.Stderr, cYellow+s+cReset) }
func fail(s string)    { fmt.Fprintln(os.Stderr, cRed+s+cReset) }
func heading(s string) { fmt.Printf("\n%s%s%s\n", cBold, s, cReset) }
func rule(w int)       { fmt.Println(cGrey + strings.Repeat("─", w) + cReset) }

func paint(v Verdict) string {
	switch v {
	case Hold:
		return cYellow + "hold   " + cReset
	case Reclaim:
		return cGreen + "reclaim" + cReset
	case Idle:
		return cBlue + "idle   " + cReset
	case Orphan:
		return cRed + "orphan " + cReset
	}
	return string(v)
}

func humanKB(kb int64) string {
	switch {
	case kb < 0:
		return "-"
	case kb >= 1<<20:
		return fmt.Sprintf("%.1fG", float64(kb)/(1<<20))
	case kb >= 1<<10:
		return fmt.Sprintf("%.0fM", float64(kb)/(1<<10))
	}
	return fmt.Sprintf("%dK", kb)
}

func humanAge(epoch int64) string {
	if epoch <= 0 {
		return "?"
	}
	d := time.Since(time.Unix(epoch, 0))
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dw", int(d.Hours()/24/7))
	}
	return fmt.Sprintf("%dmo", int(d.Hours()/24/30))
}

// confirm asks on a terminal; without one it declines, so a piped or agent
// invocation can never accidentally approve.
func confirm(prompt string, assumeYes bool) bool {
	if assumeYes {
		return true
	}
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	fmt.Printf("%s%s%s ", cBold, prompt, cReset)
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	switch strings.TrimSpace(strings.ToLower(line)) {
	case "y", "yes":
		return true
	}
	return false
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
