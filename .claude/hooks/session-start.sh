#!/bin/sh
# SessionStart hook — orient the agent, and sync main when (and only when) that
# is safe.
#
# Deliberate difference from the Balances version this is adapted from: that one
# ran `make start-task` on any clean tree, and start-task ends in
# `git checkout main`. Starting a session on a clean *feature* branch therefore
# yanked you off your branch silently. Here, main is only fast-forwarded when
# you are already on main; on any other branch the hook just reports where you
# are and touches nothing.
#
# Emits JSON on stdout: additionalContext goes into the agent's context,
# systemMessage is shown to the human. Never fails the session — worst case it
# prints nothing.
set -eu

cd "${CLAUDE_PROJECT_DIR:-.}" || exit 0
command -v jq >/dev/null 2>&1 || exit 0

emit() { # emit <additionalContext> [systemMessage]
  if [ $# -ge 2 ] && [ -n "$2" ]; then
    jq -n --arg ac "$1" --arg sm "$2" \
      '{hookSpecificOutput:{hookEventName:"SessionStart",additionalContext:$ac},systemMessage:$sm}'
  else
    jq -n --arg ac "$1" \
      '{hookSpecificOutput:{hookEventName:"SessionStart",additionalContext:$ac}}'
  fi
  exit 0
}

branch=$(git branch --show-current 2>/dev/null || echo "")
[ -n "$branch" ] || exit 0
dirty=$(git status --porcelain 2>/dev/null | grep -c . || true)

if [ "$dirty" -gt 0 ]; then
  sm="⚠️ Working tree dirty ($dirty change(s)) on '$branch' — commit, stash, or clean before syncing."
  if ! git ls-remote origin HEAD >/dev/null 2>&1; then
    sm="$sm  Also: no GitHub access — run: ssh-add --apple-load-keychain"
  fi
  emit "On branch '$branch', working tree dirty ($dirty change(s)) — start-task skipped." "$sm"
fi

if [ "$branch" != "main" ]; then
  emit "On feature branch '$branch', clean tree @ $(git rev-parse --short HEAD). Left alone — start-task only syncs when you are on main."
fi

if output=$(make start-task 2>&1); then
  emit "start-task: $output"
else
  emit "start-task FAILED: $output" "⚠️ start-task failed — the repo needs attention before proceeding: $output"
fi
