#!/bin/bash
# Builds a throwaway set of repos and worktrees, with a stubbed `gh`, and renders
# `warren list` against them to docs/demo.png. Run it from a terminal (it uses
# `script` to keep colour). Needs `freeze`
# (go install github.com/charmbracelet/freeze@latest). Touches nothing real.
set -euo pipefail
DOCS=$(cd -P "$(dirname "${BASH_SOURCE[0]}")" && pwd)
D=$(mktemp -d); D=$(cd -P "$D" && pwd); trap 'rm -rf "$D"' EXIT
DEV="$D/Developer"; mkdir -p "$DEV" "$D/origins" "$D/bin" "$D/cfg"
export GIT_AUTHOR_NAME=demo GIT_AUTHOR_EMAIL=demo@example.com GIT_COMMITTER_NAME=demo GIT_COMMITTER_EMAIL=demo@example.com
g() { git -C "$1" "${@:2}" >/dev/null 2>&1; }

repo() { # repo <name>
	git init -q --bare "$D/origins/$1.git"
	git clone -q "$D/origins/$1.git" "$DEV/$1" 2>/dev/null
	g "$DEV/$1" symbolic-ref HEAD refs/heads/main
	printf '.env\n.claude/\n' >"$DEV/$1/.gitignore"; echo base >"$DEV/$1/README"
	g "$DEV/$1" add -A; g "$DEV/$1" commit -m base; g "$DEV/$1" push -u origin main
}
wt() { # wt <repo> <name> <days ago>: a worktree with one pushed commit
	local p="$DEV/$1/.claude/worktrees/$2"
	export GIT_COMMITTER_DATE="$(($(date +%s) - $3 * 86400)) +0000"
	g "$DEV/$1" worktree add -b "$2" "$p"
	echo "$2" >"$p/$2.txt"; g "$p" add -A; g "$p" commit -m "$2"; g "$p" push -u origin "$2"
}

repo acme-api
wt acme-api fix-login-redirect 12
wt acme-api bump-go-1-25 34
wt acme-api add-retry-logic 1;    echo wip >"$DEV/acme-api/.claude/worktrees/add-retry-logic/retry.go"
wt acme-api rate-limit-headers 3
wt acme-api spike-caching 21
repo web-app
wt web-app dark-mode-toggle 9
wt web-app checkout-copy 2;       p="$DEV/web-app/.claude/worktrees/checkout-copy"; echo more >>"$p/checkout-copy.txt"; g "$p" commit -am local
wt web-app onboarding-v2 5;       echo 'STRIPE_KEY=sk_test_x' >"$DEV/web-app/.claude/worktrees/onboarding-v2/.env"
mkdir -p "$DEV/web-app/.claude/worktrees/old-experiment"; echo x >"$DEV/web-app/.claude/worktrees/old-experiment/notes"

cat >"$D/bin/gh" <<'GH'
#!/bin/bash
case "$*" in *--head*) echo '[]'; exit ;; esac
case "$(basename "$PWD")" in
acme-api) echo '[{"number":412,"state":"MERGED","headRefName":"fix-login-redirect"},{"number":415,"state":"CLOSED","headRefName":"bump-go-1-25"},{"number":418,"state":"OPEN","headRefName":"rate-limit-headers"}]' ;;
web-app)  echo '[{"number":88,"state":"MERGED","headRefName":"dark-mode-toggle"}]' ;;
*) echo '[]' ;;
esac
GH
chmod +x "$D/bin/gh"

( cd "$DEV" && go build -C "$DOCS/.." -o "$D/bin/warren" . )
echo "$DEV" >"$D/cfg/paths"
export PATH="$D/bin:$PATH" WARREN_CONFIG_DIR="$D/cfg" WARREN_CACHE_DIR="$D/cache" WARREN_PATHS_FILE="$D/cfg/paths"
script -q "$D/out" warren list >/dev/null
sed -e "s#$DEV#~/Developer#g" -e 's/\r$//' -e '/^\^D/d' "$D/out" | sed 's/^\x04\x08\x08//' >"$D/out.ansi"
cat "$D/out.ansi"
freeze --language ansi --window --padding 20,40 --output "$DOCS/demo.png" "$D/out.ansi" >/dev/null
