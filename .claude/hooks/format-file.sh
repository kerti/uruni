#!/bin/sh
# PostToolUse(Edit|Write) hook — format the file that was just written, so
# formatting never shows up as review noise or as a `make check` failure.
#
# Go   → gofmt -w
# TS/JS → prettier --write, then eslint --fix   (skipped until web deps exist)
#
# Best-effort and silent: any missing tool is a no-op, never a failed edit.
set -eu

cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0
command -v jq >/dev/null 2>&1 || exit 0

f=$(jq -r '.tool_input.file_path // ""' 2>/dev/null) || exit 0
[ -n "$f" ] && [ -f "$f" ] || exit 0

case "$f" in
  */.claude/*) exit 0 ;;
esac

case "$f" in
  *.go)
    command -v gofmt >/dev/null 2>&1 && gofmt -w "$f" >/dev/null 2>&1 || true
    ;;
  *.ts|*.tsx|*.js|*.jsx|*.css)
    # Call the installed binaries directly. `npx --prefix web <tool>` does NOT
    # redirect npx's local-bin resolution, so on a miss npx falls through to
    # fetching the package from the registry — a silent network install (and a
    # possible hang) inside an edit hook.
    [ -x web/node_modules/.bin/prettier ] &&
      web/node_modules/.bin/prettier --write "$f" >/dev/null 2>&1 || true
    case "$f" in
      *.css) ;;
      *)
        [ -x web/node_modules/.bin/eslint ] &&
          web/node_modules/.bin/eslint --fix "$f" >/dev/null 2>&1 || true
        ;;
    esac
    ;;
esac
exit 0
