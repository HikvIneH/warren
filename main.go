// warren keeps the maze of Claude Code worktrees under control.
//
// Claude Code drops every isolated task into <repo>/.claude/worktrees/<name>
// and never cleans up. warren finds them across every repo, works out which
// ones still hold work you would miss, and removes only the rest.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

func banner() {
	fmt.Println(cGreen + ` __      __` + cReset)
	fmt.Println(cGreen + ` \ \    / /_ _ _ _ _ _ ___ _ _` + cReset)
	fmt.Println(cGreen + `  \ \/\/ / _' | '_| '_/ -_) ' \ ` + cReset + cBlue + " Claude worktrees, tidied." + cReset)
	fmt.Println(cGreen + `   \_/\_/\__,_|_| |_| \___|_||_|` + cReset + cGrey + "  v" + version + cReset)
	fmt.Println()
}

func usage() {
	banner()
	g, r, k := cGreen, cReset, cGrey
	fmt.Printf("%sCOMMANDS%s\n", cBold, r)
	fmt.Printf("  %swarren%s                    Interactive menu\n", g, r)
	fmt.Printf("  %swarren list%s               Every worktree, with a keep-or-drop verdict\n", g, r)
	fmt.Printf("  %swarren analyze%s            Disk usage by repo and by worktree\n", g, r)
	fmt.Printf("  %swarren clean%s              Remove worktrees whose PR is merged or closed\n", g, r)
	fmt.Printf("  %swarren prune%s              Fix stale registrations and orphan directories\n", g, r)
	fmt.Printf("  %swarren path <name>%s        Print the path of a worktree (for cd)\n", g, r)
	fmt.Printf("  %swarren paths%s              Show or edit the directories to scan\n", g, r)
	fmt.Printf("  %swarren history%s            What warren has removed\n", g, r)
	fmt.Printf("  %swarren completion%s         Shell tab completion\n", g, r)
	fmt.Printf("\n%sOPTIONS%s\n", cBold, r)
	fmt.Printf("  %s--here%s                    Only the repo you are in  %s(works inside a worktree)%s\n", g, r, k, r)
	fmt.Printf("  %s--repo <path>%s             Only the repo owning <path>\n", g, r)
	fmt.Printf("  %s--json%s                    Machine-readable output  %s(list, analyze, clean, prune)%s\n", g, r, k, r)
	fmt.Printf("  %s--dry-run, -n%s             Preview; never delete  %s(clean, prune)%s\n", g, r, k, r)
	fmt.Printf("  %s--yes, -y%s                 Skip the confirmation prompt\n", g, r)
	fmt.Printf("  %s--idle%s                    Also drop pushed worktrees that have no PR  %s(clean)%s\n", g, r, k, r)
	fmt.Printf("  %s--branches%s                Delete the local branch too  %s(clean)%s\n", g, r, k, r)
	fmt.Printf("  %s--allow-ignored%s           Do not hold worktrees just for gitignored files  %s(clean)%s\n", g, r, k, r)
	fmt.Printf("  %s--only <verdict>%s          Show one verdict; --hold/--reclaim/--orphan shorthand  %s(list)%s\n", g, r, k, r)
	fmt.Printf("  %s--size, -s%s                Measure sizes  %s(list)%s\n", g, r, k, r)
	fmt.Printf("\n%sVERDICTS%s\n", cBold, r)
	fmt.Printf("  %sreclaim%s  PR merged or closed, tree clean, everything pushed — safe to drop\n", g, r)
	fmt.Printf("  %shold%s     uncommitted changes, commits on no remote, an ignored file you'd miss,\n           locked, a live session, or an open PR\n", cYellow, r)
	fmt.Printf("  %sidle%s     clean and pushed, but no PR — your call\n", cBlue, r)
	fmt.Printf("  %sorphan%s   directory git no longer tracks\n", cRed, r)
	fmt.Printf("\n%sENVIRONMENT%s\n", cBold, r)
	fmt.Printf("  %sWARREN_NO_GH=true%s         Skip GitHub; rely on git alone (unmerged reads as idle)\n", g, r)
	fmt.Printf("  %sWARREN_JOBS=N%s             Parallel git calls (default: number of CPUs)\n", g, r)
	fmt.Printf("  %sWARREN_PR_TTL=S%s           PR cache lifetime in seconds (default 21600)\n", g, r)
	fmt.Println()
}

func completion() {
	fmt.Print(`# warren completion — add to ~/.zshrc or ~/.bashrc:
#   eval "$(warren completion)"
_warren_complete() {
    local cmds="list analyze clean prune path paths history completion --help --version"
    if [ -n "$ZSH_VERSION" ]; then reply=(${=cmds}); else COMPREPLY=($(compgen -W "$cmds" -- "${COMP_WORDS[COMP_CWORD]}")); fi
}
if [ -n "$ZSH_VERSION" ]; then compctl -K _warren_complete warren wr; else complete -F _warren_complete warren wr; fi
`)
}

func menu(cfg *Config) int {
	in := bufio.NewReader(os.Stdin)
	for {
		banner()
		for _, l := range []string{
			"1  list      every worktree and whether it is safe to drop",
			"2  analyze   where the disk went",
			"3  clean     remove merged and closed worktrees (asks first)",
			"4  clean -n  preview a clean, change nothing",
			"5  prune     fix stale registrations and orphan directories",
			"6  hold      show only what is being protected, and why",
			"7  paths     which directories are scanned",
			"8  history   what warren has removed",
			"q  quit",
		} {
			fmt.Printf("  %s%s%s  %s%s%s\n", cGreen, l[:1], cReset, cGrey, l[3:], cReset)
		}
		fmt.Printf("\n%sChoose:%s ", cBold, cReset)
		line, err := in.ReadString('\n')
		if err != nil {
			return 0
		}
		fmt.Println()
		switch strings.TrimSpace(line) {
		case "1":
			cmdList(cfg, nil)
		case "2":
			cmdAnalyze(cfg, nil)
		case "3":
			cmdClean(cfg, nil)
		case "4":
			cmdClean(cfg, []string{"--dry-run"})
		case "5":
			cmdPrune(cfg, nil)
		case "6":
			cmdList(cfg, []string{"--hold"})
		case "7":
			cmdPaths(cfg, nil)
		case "8":
			cmdHistory(cfg)
		case "q", "Q", "":
			return 0
		default:
			warn("Not an option: " + strings.TrimSpace(line))
		}
		fmt.Printf("\n%sPress return for the menu…%s", cDim, cReset)
		if _, err := in.ReadString('\n'); err != nil {
			return 0
		}
		fmt.Println()
	}
}

func main() {
	initColors()
	cfg := loadConfig()
	cmd, args := "menu", []string(nil)
	if len(os.Args) > 1 {
		cmd, args = os.Args[1], os.Args[2:]
	}
	var rc int
	switch cmd {
	case "menu":
		if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			rc = menu(cfg)
		} else {
			rc = cmdList(cfg, nil)
		}
	case "list", "ls", "status":
		rc = cmdList(cfg, args)
	case "analyze", "disk":
		rc = cmdAnalyze(cfg, args)
	case "clean":
		rc = cmdClean(cfg, args)
	case "prune":
		rc = cmdPrune(cfg, args)
	case "path", "cd":
		rc = cmdPath(cfg, args)
	case "paths":
		rc = cmdPaths(cfg, args)
	case "history", "log":
		rc = cmdHistory(cfg)
	case "completion":
		completion()
	case "-h", "--help", "help":
		usage()
	case "-v", "--version", "version":
		fmt.Println("warren " + version)
	default:
		fail("Unknown command: " + cmd)
		fmt.Println()
		usage()
		rc = 1
	}
	os.Exit(rc)
}
