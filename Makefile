.PHONY: help setup hooks-install claude-install doctor \
        run serve-bin build test test-cover lint fmt tidy sqlc migrate-up migrate-down migrate-status \
        web-install web-dev web-build web-lint web-typecheck web-test \
        server-stop server-restart web-stop web-restart restart servers-status \
        e2e e2e-reset e2e-server stack-up stack-down stack-logs stack-ps \
        dev-user start-task check

# `make` with no target prints help.
.DEFAULT_GOAL := help

-include .env
export

# Uruni is one Go binary at the repo root (ADR-001) with the React app in web/,
# and SQLite as its only store (ADR-004) — so dev needs no containers at all. The
# repo-root docker-compose.yml is the *operator* self-host stack; the stack-*
# targets below exercise it locally, they are not the dev loop.
COMPOSE := docker-compose.yml

# Background dev-server logs. tail -f to follow.
SERVER_LOG := /tmp/uruni-server.log
WEB_LOG    := /tmp/uruni-web.log

# Process match for *this clone's* vite. Anchored to $(CURDIR) on purpose: a bare
# `pkill -f 'npm run dev'` would take down every other project's dev server on
# the machine. It also matches vite's real argv — npm execs the resolved script
# (node .../web/node_modules/vite/bin/vite.js), not the .bin/ shim.
WEB_PROC := $(CURDIR)/web/node_modules

# Port the Go server listens on (matches the config default); used by the
# readiness poll in `server-restart`. Override by setting PORT in .env.
SERVER_PORT := $(or $(PORT),8080)

# E2E (ADR-015). SQLite makes this cheap: a throwaway database *file*, deleted
# and re-migrated each run, so the dev DB is never touched and there is no
# container to exec into. Playwright owns the e2e server + vite on dedicated
# ports, so the 8080/5173 dev servers are never disturbed.
E2E_DB   := /tmp/uruni-e2e.db
E2E_PORT := 8099

# The golangci-lint version ci.yml pins (env.GOLANGCI_VERSION there). `doctor`
# compares your local one against it — a v1 binary silently ignores the v2
# schema in .golangci.yml, which is exactly the local/CI drift `check` exists to
# prevent. Bump both together.
GOLANGCI_CI_VERSION := v2.12.2

help:
	@echo "uruni — make targets (run 'make <target>')"
	@echo ""
	@echo "First run:"
	@echo "  setup                   fresh-clone entry point: hooks + Claude Code + deps + .env"
	@echo "  hooks-install           enable the pre-commit pii-guard (git hooks)"
	@echo "  claude-install          arm the Claude Code hooks + seed personal settings"
	@echo "  doctor                  report what's installed and what's missing"
	@echo ""
	@echo "Go server (repo root):"
	@echo "  run                     run the server in the foreground (:$(SERVER_PORT))"
	@echo "  build                   build web/dist, then the embedding binary to bin/uruni"
	@echo "  serve-bin               build, then run bin/uruni itself (real version stamp)"
	@echo "  test                    run all Go tests"
	@echo "  test-cover              go test with race + coverage profile (as CI runs it)"
	@echo "  lint                    golangci-lint"
	@echo "  fmt                     gofmt -w the tree"
	@echo "  tidy                    go mod tidy"
	@echo "  sqlc                    regenerate sqlc code"
	@echo "  migrate-up              apply pending migrations"
	@echo "  migrate-down            roll back the last migration"
	@echo "  migrate-status          show migration status"
	@echo ""
	@echo "Web (Vite/React, in web/):"
	@echo "  web-install             npm install"
	@echo "  web-dev                 run the vite dev server in the foreground (:5173)"
	@echo "  web-build               production build to web/dist (embedded by 'build')"
	@echo "  web-lint                oxlint"
	@echo "  web-typecheck           tsc"
	@echo "  web-test                vitest"
	@echo ""
	@echo "Background dev servers:"
	@echo "  server-restart          restart the background Go server (log: $(SERVER_LOG))"
	@echo "  server-stop             stop the background Go server"
	@echo "  web-restart             restart the background vite server (log: $(WEB_LOG))"
	@echo "  web-stop                stop the background vite server"
	@echo "  restart                 restart both"
	@echo "  servers-status          show which dev servers are running"
	@echo ""
	@echo "E2E (Playwright; ADR-015):"
	@echo "  e2e                     full run — reset the throwaway DB, run the suite"
	@echo "  e2e-reset               recreate + migrate + seed $(E2E_DB)"
	@echo "  e2e-server              run the server against $(E2E_DB) (foreground, :$(E2E_PORT))"
	@echo ""
	@echo "Self-host stack (the operator's docker-compose.yml — not the dev loop):"
	@echo "  stack-up                start the compose stack in the background"
	@echo "  stack-down              stop it"
	@echo "  stack-logs              follow its logs"
	@echo "  stack-ps                show service status"
	@echo ""
	@echo "Workflow helpers (terse output):"
	@echo "  start-task              pre-flight: clean tree? GitHub access? then sync main"
	@echo "  check                   pre-push gate: mirrors ci.yml, pass/fail only (logs in /tmp)"
	@echo "  dev-user EMAIL=... PASSWORD=...  create/reset a local login for smoke tests"

# ---- first run -------------------------------------------------------------
# One command from a fresh clone to a runnable, guarded dev loop. Idempotent —
# safe to re-run, and worth re-running after a pull that touches .claude/ or
# .githooks/ (both need a chmod that git alone won't reapply on some clones).
#
# Everything here is deterministic. What a Makefile *cannot* do is install or
# authenticate Claude Code itself, or grant it permissions — `make doctor`
# reports on those instead of pretending to fix them.
setup: hooks-install claude-install web-install
	@if [ ! -f .env ]; then \
	  sed "s|^URUNI_SESSION_SECRET=.*|URUNI_SESSION_SECRET=$$(openssl rand -base64 48 | tr -d '\n')|" \
	    .env.example > .env; \
	  echo "setup: created .env from .env.example (session secret generated)"; \
	fi
	@echo "✓ setup complete — next: make migrate-up && make run"

# Point git at the repo's own hooks directory and seed the local, gitignored
# .pii-patterns denylist from the template + your git identity, so the
# pre-commit pii-guard protects commits out of the box. Idempotent.
hooks-install:
	@git config core.hooksPath .githooks
	@chmod +x .githooks/pre-commit
	@if [ ! -f .pii-patterns ]; then \
	  cp .pii-patterns.example .pii-patterns; \
	  { git config user.name; git config user.email; } \
	    | sed 's/[][\\.^$$*+?(){}|]/\\&/g' >> .pii-patterns; \
	  echo "hooks-install: seeded .pii-patterns (gitignored) from template + git identity"; \
	fi
	@echo "✓ git hooks installed (core.hooksPath=.githooks); pre-commit pii-guard active"

# Arm the Claude Code hooks. The behaviour itself is committed in
# .claude/settings.json (portable — it addresses scripts via
# $$CLAUDE_PROJECT_DIR, never an absolute path), so all that's left per clone is
# the executable bit and your personal settings file. Idempotent.
claude-install:
	@chmod +x .claude/hooks/*.sh
	@if [ ! -f .claude/settings.local.json ]; then \
	  cp .claude/settings.local.json.example .claude/settings.local.json; \
	  echo "claude-install: seeded .claude/settings.local.json (gitignored)"; \
	fi
	@if ! command -v jq >/dev/null 2>&1; then \
	  echo "⚠ jq not found — the Claude Code hooks parse tool JSON with it and will" >&2; \
	  echo "  silently no-op until it's installed (brew install jq)." >&2; \
	fi
	@echo "✓ Claude Code hooks armed (session-start, pre-push gate, format-on-write)"

# Report on the parts of the environment the Makefile can't install for you.
# Never fails — it's a status readout, not a gate.
doctor:
	@printf '%-16s' 'go';            command -v go            >/dev/null 2>&1 && go version | awk '{print $$3}' || echo 'MISSING'
	@printf '%-16s' 'node';          command -v node          >/dev/null 2>&1 && node --version                 || echo 'MISSING'
	@printf '%-16s' 'jq';            command -v jq            >/dev/null 2>&1 && echo 'ok'                      || echo 'MISSING (Claude Code hooks need it)'
	@printf '%-16s' 'golangci-lint'; if command -v golangci-lint >/dev/null 2>&1; then \
	  v=$$(golangci-lint version 2>/dev/null | sed -n 's/.*has version \([0-9.]*\).*/\1/p'); \
	  case "$$v" in 2.*) echo "v$$v";; \
	    *) echo "v$$v — .golangci.yml is schema v2; CI pins $(GOLANGCI_CI_VERSION). Upgrade.";; esac; \
	else echo 'MISSING (make lint / make check)'; fi
	@printf '%-16s' 'sqlc';          command -v sqlc          >/dev/null 2>&1 && echo 'ok'                      || echo 'MISSING (make sqlc)'
	@printf '%-16s' 'claude';        command -v claude        >/dev/null 2>&1 && echo 'ok'                      || echo 'not on PATH (install separately; the Makefile cannot)'
	@printf '%-16s' 'git hooks';     [ "$$(git config core.hooksPath)" = ".githooks" ] && echo 'armed'          || echo 'NOT armed — run make hooks-install'
	@printf '%-16s' 'pii-patterns';  [ -f .pii-patterns ] && echo 'present'                                     || echo 'MISSING — run make hooks-install'
	@printf '%-16s' 'claude hooks';  [ -x .claude/hooks/session-start.sh ] && echo 'executable'                 || echo 'NOT executable — run make claude-install'
	@printf '%-16s' '.env';          [ -f .env ] && echo 'present'                                              || echo 'MISSING — run make setup'
	@printf '%-16s' 'web deps';      [ -d web/node_modules ] && echo 'installed'                                || echo 'MISSING — run make web-install'

# ---- Go server -------------------------------------------------------------

run:
	go run ./cmd/uruni serve

# The embed pipeline (ADR-001): the React bundle must exist before the Go build
# so embed.FS picks it up. Never `go build` alone when you want a shippable
# binary — you'll embed a stale (or empty) web/dist.
build: web-build
	go build -o bin/uruni ./cmd/uruni

# Run the built artifact rather than `go run`, with .env exported for you — the
# gap that makes a bare `./bin/uruni serve` fail on URUNI_SESSION_SECRET, since
# .env is a Makefile convenience and the binary reads only real environment
# variables (ADR-019: env vars only, no config file).
#
# Worth having separately from `run` because `go run` binaries carry no VCS
# record, so `uruni version` and /healthz report commit "unknown" under it. This
# reports the real SHA, which is what the image does.
serve-bin: build
	./bin/uruni serve

test:
	go test ./...

test-cover:
	go test ./... -race -covermode=atomic -coverprofile=coverage.out

lint:
	golangci-lint run

fmt:
	gofmt -w .

tidy:
	go mod tidy

sqlc:
	sqlc generate

migrate-up:
	go run ./cmd/uruni migrate up

migrate-down:
	go run ./cmd/uruni migrate down

migrate-status:
	go run ./cmd/uruni migrate status

# ---- web -------------------------------------------------------------------

web-install:
	( cd web && npm install )

web-dev:
	( cd web && npm run dev )

web-build:
	( cd web && npm run build )

web-lint:
	( cd web && npm run -s lint )

web-typecheck:
	( cd web && npm run -s typecheck )

web-test:
	( cd web && npm run -s test )

# ---- background dev servers ------------------------------------------------
# `make restart` kills any running server + vite processes and starts fresh
# ones in the background, logging to files. Useful after a migration change
# (the server runs goose on serve) or when iterating on env vars.
#
# Stops wait for the process to actually exit; starts poll for real readiness
# (server /healthz; vite's "Local:" line) rather than a blind sleep. Starts fail
# loud: the poll exits non-zero if the process dies (compile error, panic, port
# already bound) or never signals readiness, printing the tail of the log. A
# failed server-restart short-circuits `restart`, so vite isn't restarted
# against a server that never came up.

server-stop:
	@pkill -f 'go run ./cmd/uruni' 2>/dev/null || true
	@pkill -x uruni 2>/dev/null || true
	@for i in $$(seq 1 50); do \
	  pgrep -x uruni >/dev/null 2>&1 || pgrep -f 'go run ./cmd/uruni' >/dev/null 2>&1 || break; \
	  sleep 0.1; \
	done
	@pkill -9 -x uruni 2>/dev/null || true
	@echo "server: stopped"

server-restart: server-stop
	@( exec nohup go run ./cmd/uruni serve ) > $(SERVER_LOG) 2>&1 < /dev/null &
	@seen=0; for i in $$(seq 1 100); do \
	  curl -fsS http://localhost:$(SERVER_PORT)/healthz >/dev/null 2>&1 && { echo "server: started (log: $(SERVER_LOG))"; exit 0; }; \
	  if pgrep -f 'go run ./cmd/uruni serve' >/dev/null 2>&1 || pgrep -x uruni >/dev/null 2>&1; then seen=1; elif [ $$seen = 1 ]; then break; fi; \
	  sleep 0.1; \
	done; \
	echo "✗ server failed to start (died or timed out) — tail of $(SERVER_LOG):" >&2; \
	tail -n 20 $(SERVER_LOG) >&2; \
	exit 1

web-stop:
	@pkill -f '$(WEB_PROC)' 2>/dev/null || true
	@for i in $$(seq 1 50); do \
	  pgrep -f '$(WEB_PROC)' >/dev/null 2>&1 || break; \
	  sleep 0.1; \
	done
	@echo "web: stopped"

web-restart: web-stop
	@: > $(WEB_LOG)
	@( cd web && exec nohup npm run dev ) > $(WEB_LOG) 2>&1 < /dev/null &
	@seen=0; for i in $$(seq 1 150); do \
	  grep -q 'Local:' $(WEB_LOG) 2>/dev/null && { echo "web: started (log: $(WEB_LOG))"; exit 0; }; \
	  if pgrep -f '$(WEB_PROC)' >/dev/null 2>&1; then seen=1; elif [ $$seen = 1 ]; then break; fi; \
	  sleep 0.1; \
	done; \
	echo "✗ web failed to start (died or timed out) — tail of $(WEB_LOG):" >&2; \
	tail -n 20 $(WEB_LOG) >&2; \
	exit 1

restart: server-restart web-restart
	@echo "both servers restarted"

servers-status:
	@if pgrep -f 'cmd/uruni serve' >/dev/null; then \
	  echo "server: running (pid $$(pgrep -f 'cmd/uruni serve' | head -1))"; \
	else \
	  echo "server: stopped"; \
	fi
	@if pgrep -f '$(WEB_PROC)' >/dev/null; then \
	  echo "web:    running (pid $$(pgrep -f '$(WEB_PROC)' | head -1))"; \
	else \
	  echo "web:    stopped"; \
	fi

# ---- e2e (Playwright; ADR-015) ---------------------------------------------
# e2e-reset runs synchronously before Playwright, so the file is fully migrated
# and seeded by the time Playwright's server boots (auto-migrate becomes a
# no-op, no race). E2E_ARGS forwards Playwright flags:
#   make e2e E2E_ARGS='--grep @smoke'

e2e: e2e-reset
	@( cd web && URUNI_DB="$(E2E_DB)" npm run -s test:e2e -- $(E2E_ARGS) )

e2e-reset:
	@rm -f $(E2E_DB) $(E2E_DB)-wal $(E2E_DB)-shm
	@URUNI_DB="$(E2E_DB)" go run ./cmd/uruni seed-e2e
	@echo "e2e db: $(E2E_DB) ready"

e2e-server: e2e-reset
	@URUNI_DB="$(E2E_DB)" PORT=$(E2E_PORT) go run ./cmd/uruni serve

# ---- self-host stack -------------------------------------------------------
# Exercises docker-compose.yml — the artifact operators actually run (ADR-010
# calls it a first-class deliverable, so it deserves to be run locally before
# every release, not only by strangers).

stack-up:
	docker compose -f $(COMPOSE) up -d

stack-down:
	docker compose -f $(COMPOSE) down

stack-logs:
	docker compose -f $(COMPOSE) logs -f

stack-ps:
	docker compose -f $(COMPOSE) ps

# ---- workflow helpers ------------------------------------------------------
# Each emits a single status line per step so they're cheap to read in an
# agent's context; verbose output goes to a /tmp log, read only on failure.

# Pre-task pre-flight: refuse on a dirty tree, verify GitHub access, then
# fast-forward main. Run before starting any new work so you never branch off a
# stale local main.
start-task:
	@test -z "$$(git status --porcelain)" || { echo "✗ working tree dirty — commit or stash first, then re-run"; exit 1; }
	@git ls-remote origin HEAD >/dev/null 2>&1 || { echo "✗ no GitHub access — unlock the SSH key (ssh-add) or run 'gh auth login'"; exit 1; }
	@git checkout main >/dev/null 2>&1 || { echo "✗ could not switch to main"; exit 1; }
	@git pull --ff-only >/dev/null 2>&1 || { echo "✗ pull failed (diverged or no upstream) — resolve manually"; exit 1; }
	@echo "✓ on main, up to date @ $$(git rev-parse --short HEAD)"

# Pre-push gate. Deliberately mirrors .github/workflows/ci.yml step for step, so
# green locally ≈ green in CI (ADR-017). Keep the two in sync when either
# changes — including the golangci-lint version (see GOLANGCI_CI_VERSION above;
# `make doctor` flags a mismatch). e2e is excluded — run `make e2e` separately
# (slow and verbose).
#
# The go.mod / web/package.json guards mirror ci.yml's `preflight` job: the
# tooling predates the code (ADR-019), so until M1 lands there is nothing to
# lint. Without them this target fails for the entire pre-scaffold window, which
# would (a) stop mirroring CI and (b) make the PreToolUse hook deny every single
# `git push`. Both guards go permanently inert once M1 lands.
check:
	@fail=0; \
	if [ -f go.mod ]; then \
	  printf '%-14s' 'golangci-lint'; golangci-lint run                >/tmp/uruni-check-go-lint.log 2>&1 && echo '✓' || { echo '✗ → /tmp/uruni-check-go-lint.log'; fail=1; }; \
	  printf '%-14s' 'go test';       go test ./... -race              >/tmp/uruni-check-go-test.log 2>&1 && echo '✓' || { echo '✗ → /tmp/uruni-check-go-test.log'; fail=1; }; \
	else \
	  printf '%-14s' 'go'; echo '– skipped (no go.mod yet — ci.yml skips too)'; \
	fi; \
	if [ -f web/package.json ]; then \
	  printf '%-14s' 'oxlint';        (cd web && npm run -s lint)      >/tmp/uruni-check-web-lint.log 2>&1 && echo '✓' || { echo '✗ → /tmp/uruni-check-web-lint.log'; fail=1; }; \
	  printf '%-14s' 'tsc';           (cd web && npm run -s typecheck) >/tmp/uruni-check-web-tsc.log  2>&1 && echo '✓' || { echo '✗ → /tmp/uruni-check-web-tsc.log';  fail=1; }; \
	  printf '%-14s' 'vitest';        (cd web && npm run -s test)      >/tmp/uruni-check-web-test.log 2>&1 && echo '✓' || { echo '✗ → /tmp/uruni-check-web-test.log'; fail=1; }; \
	  printf '%-14s' 'web build';     (cd web && npm run -s build)     >/tmp/uruni-check-web-build.log 2>&1 && echo '✓' || { echo '✗ → /tmp/uruni-check-web-build.log'; fail=1; }; \
	else \
	  printf '%-14s' 'web'; echo '– skipped (no web/package.json yet — ci.yml skips too)'; \
	fi; \
	if [ $$fail -eq 0 ]; then echo 'all green'; else echo 'FAILED — read the ✗ log(s) above'; exit 1; fi

# Create or reset a local login, for curl smoke tests against authenticated
# endpoints. Auth is local email/password (ADR-007), so there is no token to
# mint — you log in and get a session cookie:
#   make dev-user EMAIL=bendahara@example.com PASSWORD=rahasia123
#   curl -c /tmp/uruni.jar -d '{"email":"...","password":"..."}' localhost:8080/api/login
dev-user:
	@if [ -z "$(EMAIL)" ] || [ -z "$(PASSWORD)" ]; then \
	  echo "usage: make dev-user EMAIL=<email> PASSWORD=<password>" >&2; exit 1; \
	fi
	@go run ./cmd/uruni create-user "$(EMAIL)" "$(PASSWORD)"
