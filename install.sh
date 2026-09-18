#!/bin/bash
# Builds warren and puts `warren` and `wr` on your PATH, plus the Claude Code skill.
set -euo pipefail
SRC=$(cd -P "$(dirname "${BASH_SOURCE[0]}")" && pwd)
command -v go >/dev/null || { echo "go is required: https://go.dev/dl/"; exit 1; }

if   [ -w /opt/homebrew/bin ]; then PREFIX=/opt/homebrew/bin
elif [ -w /usr/local/bin ];    then PREFIX=/usr/local/bin
else PREFIX="$HOME/.local/bin"; mkdir -p "$PREFIX"; fi

( cd "$SRC" && go build -trimpath -ldflags='-s -w' -o "$PREFIX/warren" . )
ln -sfn "$PREFIX/warren" "$PREFIX/wr"

SKILLS="$HOME/.claude/skills"; SKILL_MSG="  (no ~/.claude found; skill not installed)"
if [ -d "$HOME/.claude" ]; then
    mkdir -p "$SKILLS"
    if [ -d "$SKILLS/warren" ] && [ ! -L "$SKILLS/warren" ]; then
        SKILL_MSG="  $SKILLS/warren already exists as a directory; not replaced"
    else
        ln -sfn "$SRC/skills/warren" "$SKILLS/warren"
        SKILL_MSG="  $SKILLS/warren -> skills/warren"
    fi
fi
printf 'Installed:\n  %s/warren\n  %s/wr\n%s\n\n' "$PREFIX" "$PREFIX" "$SKILL_MSG"
case ":$PATH:" in *":$PREFIX:"*) echo "Try:  warren list" ;; *) printf 'Add to your shell profile:\n  export PATH="%s:$PATH"\n' "$PREFIX" ;; esac
