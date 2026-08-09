#!/bin/sh
# PreToolUse(Bash) hook — refuse to push a red tree.
#
# `make check` mirrors ci.yml step for step, so this turns "I pushed and CI went
# red ten minutes later" into an immediate local failure. Only `git push` is
# gated; every other Bash command passes straight through.
#
# Reads the tool call as JSON on stdin; emits a permissionDecision of "deny"
# (with the reason) when check fails, and nothing at all when it passes.
set -eu

cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0
command -v jq >/dev/null 2>&1 || exit 0

cmd=$(jq -r '.tool_input.command // ""' 2>/dev/null) || exit 0

# Decide whether one command segment invokes `git push`. Matching the raw string
# is wrong in both directions: a prefix-only test (`"git push"*`) lets
# `make check && git push` through, while a plain substring test fires on
# `git commit -m "add push notification"` and on any command that merely mentions
# the phrase. So: confirm the segment's first word is `git`, walk past git's own
# options (`-c key=val`, `-C dir`, …), and require the subcommand itself to be
# `push`.
is_git_push() {
  set -f            # no globbing while we word-split
  # shellcheck disable=SC2086
  set -- $1
  set +f
  [ "${1:-}" = "git" ] || return 1
  shift
  while [ $# -gt 0 ]; do
    case "$1" in
      # git options that consume the next word
      -c|-C|--git-dir|--work-tree|--namespace|--exec-path|--super-prefix)
        [ $# -ge 2 ] || return 1
        shift 2
        ;;
      -*) shift ;;                    # any other option: skip
      push) return 0 ;;               # first bare word is the subcommand
      *) return 1 ;;
    esac
  done
  return 1
}

# Split on shell separators so `cd web && git push` is seen as its own segment.
segments=$(printf '%s' "$cmd" | tr '\n' ';' | sed 's/&&/;/g; s/||/;/g; s/|/;/g')
gated=0
old_ifs=$IFS
IFS=';'
for seg in $segments; do
  IFS=$old_ifs
  if is_git_push "$seg"; then gated=1; break; fi
  IFS=';'
done
IFS=$old_ifs

[ "$gated" = 1 ] || exit 0

if output=$(make check 2>&1); then
  exit 0
fi

jq -n --arg r "make check failed — fix before pushing:

$output" \
  '{hookSpecificOutput:{hookEventName:"PreToolUse",permissionDecision:"deny",permissionDecisionReason:$r}}'
