.PHONY: help setup hooks-install claude-install doctor \
        run serve-bin build test test-cover lint fmt tidy sqlc migrate-up migrate-down migrate-status db-reset upgrade-rehearsal \
        web-install web-dev web-build web-lint web-fmt web-fmt-check web-typecheck web-test \
        server-stop server-restart web-stop web-restart restart servers-status \
        e2e e2e-ci e2e-install e2e-reset e2e-server stack-up stack-up-local stack-down stack-logs stack-ps \
        dev-user start-task check notices

# `make` with no target prints help.
.DEFAULT_GOAL := help

-include .env
export

# Uruni is one Go binary at the repo root (ADR-001) with the React app in web/,
# and SQLite as its only store (ADR-004) - so dev needs no containers. The
# repo-root docker-compose.yml is the *operator* self-host stack; the stack-*
# targets exercise it locally, they are not the dev loop.
COMPOSE := docker-compose.yml

# Background dev-server logs. tail -f to follow.
SERVER_LOG := /tmp/uruni-server.log
WEB_LOG    := /tmp/uruni-web.log

# Process match for *this clone's* vite. Anchored to $(CURDIR): a bare
# `pkill -f 'npm run dev'` would take down every other project's dev server. It
# matches vite's real argv - npm execs the resolved script
# (node .../web/node_modules/vite/bin/vite.js), not the .bin/ shim.
WEB_PROC := $(CURDIR)/web/node_modules

# Port the Go server listens on (the config default); used by the readiness
# poll in `server-restart`. Override by setting PORT in .env.
SERVER_PORT := $(or $(PORT),8080)

# The dev database `db-reset` deletes. Matches the binary's default
# (config.DefaultDBPath); URUNI_DB in .env wins, as it does for `run`.
DEV_DB := $(or $(URUNI_DB),./uruni.db)

# Where dev receipt uploads land. Matches the binary's default
# (config.DefaultUploadsDir); URUNI_UPLOADS_DIR in .env wins. Unlike the SQLite
# file, `serve` refuses to boot if this directory does not exist
# (config.EnsureUploadsDirWritable) - so every target that starts the server
# creates it first.
DEV_UPLOADS_DIR := $(or $(URUNI_UPLOADS_DIR),./uploads)

# Where dev backup dumps land. Matches the binary's default
# (config.DefaultBackupDir); URUNI_BACKUP_DIR in .env wins. `serve` refuses to
# boot if this directory does not exist (config.EnsureBackupDirWritable)
# either.
DEV_BACKUP_DIR := $(or $(URUNI_BACKUP_DIR),./backups)

# E2E (ADR-039). SQLite makes this cheap: throwaway database *files*, so the
# dev DB is never touched. `make e2e` owns none of the values below: Playwright
# builds the binary and gives each worker its own server, database, port,
# backup and uploads directory under the OS temp dir (web/e2e/fixtures.ts).
# These are the one instance `make e2e-server` runs for debugging by hand, on a
# port away from the 8080/5173 dev servers.
E2E_DB   := /tmp/uruni-e2e.db
E2E_PORT := 8099
# Its own backup and uploads directories, not the dev ones: restore writes
# dumps and receipts write photos, so sharing ./backups or ./uploads would mix
# e2e files into the dev server's. Reset with the database.
E2E_BACKUP_DIR := /tmp/uruni-e2e-backups
E2E_UPLOADS_DIR := /tmp/uruni-e2e-uploads
# The request logger writes one info line per request
# (internal/http/middleware.go), which buries Playwright's results in a full
# run. `make e2e` wants quiet; for debugging:
# `make e2e-server E2E_LOG_LEVEL=debug`.
E2E_LOG_LEVEL ?= warn

# The golangci-lint version ci.yml pins (env.GOLANGCI_VERSION there). `doctor`
# compares your local one exactly, not just the major: a v1 binary silently
# ignores the v2 schema in .golangci.yml, and two v2 releases also disagree
# about real code (2.12 and 2.13 ship different staticcheck versions), so
# `make check` would stop meaning what ADR-020 says. Bump both together.
GOLANGCI_CI_VERSION := v2.14.0

# The sqlc version that wrote internal/store (the `sqlc vX` line in every
# generated header) and that ci.yml installs (env.SQLC_VERSION there). Another
# version writes different code, and `sqlc diff` then fails on a tree nobody
# edited. `doctor` compares yours exactly. Bump all three together, by
# regenerating with the new one.
SQLC_CI_VERSION := v1.31.1

# govulncheck runs through `go run` at this exact version, here and in ci.yml
# (env.GOVULNCHECK_VERSION there) - nothing to install, nothing to drift.
GOVULNCHECK_VERSION := v1.9.0

help:
	@echo "uruni - make targets (run 'make <target>')"
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
	@echo "  notices                 regenerate THIRD-PARTY-NOTICES (web build + Go modules)"
	@echo "  sqlc                    regenerate sqlc code"
	@echo "  migrate-up              apply pending migrations"
	@echo "  migrate-down            roll back the last migration"
	@echo "  migrate-status          show migration status"
	@echo "  db-reset                delete $(DEV_DB) and re-migrate - run this after a schema change"
	@echo "  upgrade-rehearsal BACKUP=<zip> [DB=<old file>]  run this tree's rebuild on a copy, in a temp dir"
	@echo ""
	@echo "Web (Vite/React, in web/):"
	@echo "  web-install             npm install"
	@echo "  web-dev                 run the vite dev server in the foreground (:5173)"
	@echo "  web-build               production build to web/dist (embedded by 'build')"
	@echo "  web-lint                oxlint"
	@echo "  web-fmt                 prettier --write over src/, e2e/ and *.config.ts"
	@echo "  web-fmt-check           prettier --check (what make check and CI run)"
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
	@echo "E2E (Playwright; ADR-039):"
	@echo "  e2e                     full run - build the SPA, then the suite on per-worker instances"
	@echo "  e2e-ci                  the CI twin of e2e - no SPA build (the workflow did it), CI=true"
	@echo "  e2e-install             download the browser Playwright drives (once per machine)"
	@echo "  e2e-reset               recreate + migrate + seed $(E2E_DB) (one instance, by hand)"
	@echo "  e2e-server              run the server against $(E2E_DB) (foreground, :$(E2E_PORT))"
	@echo ""
	@echo "Self-host stack (the operator's docker-compose.yml - not the dev loop):"
	@echo "  stack-up                start the compose stack in the background"
	@echo "  stack-up-local          build the image from this tree, then start the stack on it"
	@echo "  stack-down              stop it"
	@echo "  stack-logs              follow its logs"
	@echo "  stack-ps                show service status"
	@echo ""
	@echo "Workflow helpers (terse output):"
	@echo "  start-task              pre-flight: clean tree? GitHub access? then sync main"
	@echo "  check                   pre-push gate: mirrors ci.yml, pass/fail only (logs in /tmp)"
	@echo "  dev-user EMAIL=... PASSWORD=...  create/reset a local login for smoke tests"

# THIRD-PARTY-NOTICES (#521). The web half is read from the *built* bundle and
# service worker, not the lockfile (Workbox ships although it is a dev
# dependency), so the build writes it as JSON when URUNI_WEB_NOTICES_JSON is
# set (web/vite.config.ts). `make check` points its own web build at
# NOTICES_CHECK_JSON and diffs, so it builds once.
NOTICES_JSON       := /tmp/uruni-notices-web.json
NOTICES_CHECK_JSON := /tmp/uruni-check-web-notices.json

# ---- first run -------------------------------------------------------------
# One command from a fresh clone to a runnable, guarded dev loop. Idempotent -
# safe to re-run, and worth re-running after a pull that touches .claude/ or
# .githooks/ (both need a chmod that git alone won't reapply on some clones).
#
# What a Makefile *cannot* do is install or authenticate Claude Code, or grant
# it permissions - `make doctor` reports on those instead.
#
# The generated .env rewrites one line of .env.example, the self-host template
# whose values are production values. URUNI_BASE_URL becomes a loopback http
# origin because the server refuses to start on the template value (the "did
# you configure this instance at all?" gate, ADR-019), and the cookie's Secure
# flag is derived from the scheme (internal/http/session.go): the https://
# template value in local dev would set a Secure cookie on a plain-HTTP origin,
# which the browser declines to keep on any dev origin that is not localhost -
# phone testing over the LAN being the case that matters. Only the scheme is
# read, so the port need not match `make web-dev`'s 5173.
setup: hooks-install claude-install web-install
	@if [ ! -f .env ]; then \
	  sed -e "s|^URUNI_BASE_URL=.*|URUNI_BASE_URL=http://localhost:8080|" \
	    .env.example > .env; \
	  echo "setup: created .env from .env.example (base URL set to loopback)"; \
	fi
	@mkdir -p "$(DEV_UPLOADS_DIR)"
	@mkdir -p "$(DEV_BACKUP_DIR)"
	@echo "ok setup complete - next: make run (API on :$(SERVER_PORT), applies migrations on boot), and make web-dev (Vite on :5173) in a second terminal"

# Point git at the repo's hooks directory and seed the local, gitignored
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
	@echo "ok git hooks installed (core.hooksPath=.githooks); pre-commit pii-guard active"

# Arm the Claude Code hooks. The behaviour is committed in
# .claude/settings.json (portable - scripts addressed via
# $$CLAUDE_PROJECT_DIR, never an absolute path), so per clone only the
# executable bit and your personal settings file remain. Idempotent.
claude-install:
	@chmod +x .claude/hooks/*.sh
	@if [ ! -f .claude/settings.local.json ]; then \
	  cp .claude/settings.local.json.example .claude/settings.local.json; \
	  echo "claude-install: seeded .claude/settings.local.json (gitignored)"; \
	fi
	@if ! command -v jq >/dev/null 2>&1; then \
	  echo "! jq not found - the Claude Code hooks parse tool JSON with it and will" >&2; \
	  echo "  silently no-op until it's installed (brew install jq)." >&2; \
	fi
	@echo "ok Claude Code hooks armed (session-start, pre-push gate, agent gate, format-on-write)"

# Report on what the Makefile can't install for you. Never fails - a status
# readout, not a gate.
doctor:
	@printf '%-16s' 'go';            command -v go            >/dev/null 2>&1 && go version | awk '{print $$3}' || echo 'MISSING'
	@printf '%-16s' 'node';          command -v node          >/dev/null 2>&1 && node --version                 || echo 'MISSING'
	@printf '%-16s' 'jq';            command -v jq            >/dev/null 2>&1 && echo 'ok'                      || echo 'MISSING (Claude Code hooks need it)'
	@printf '%-16s' 'golangci-lint'; if command -v golangci-lint >/dev/null 2>&1; then \
	  v=$$(golangci-lint version 2>/dev/null | sed -n 's/.*has version \([0-9.]*\).*/\1/p'); \
	  if [ "v$$v" = "$(GOLANGCI_CI_VERSION)" ]; then echo "v$$v"; \
	  else echo "v$$v - CI pins $(GOLANGCI_CI_VERSION). Different linter versions disagree about real code; install the pinned one."; fi; \
	else echo 'MISSING (make lint / make check)'; fi
	@printf '%-16s' 'sqlc';          if command -v sqlc >/dev/null 2>&1; then \
	  v=$$(sqlc version 2>/dev/null); \
	  if [ "$$v" = "$(SQLC_CI_VERSION)" ]; then echo "$$v"; \
	  else echo "$$v - CI pins $(SQLC_CI_VERSION). Another version writes different code into internal/store; install the pinned one."; fi; \
	else echo 'MISSING (make sqlc / make check)'; fi
	@printf '%-16s' 'claude';        command -v claude        >/dev/null 2>&1 && echo 'ok'                      || echo 'not on PATH (install separately; the Makefile cannot)'
	@printf '%-16s' 'git hooks';     [ "$$(git config core.hooksPath)" = ".githooks" ] && echo 'armed'          || echo 'NOT armed - run make hooks-install'
	@printf '%-16s' 'pii-patterns';  [ -f .pii-patterns ] && echo 'present'                                     || echo 'MISSING - run make hooks-install'
	@printf '%-16s' 'claude hooks';  [ -x .claude/hooks/session-start.sh ] && echo 'executable'                 || echo 'NOT executable - run make claude-install'
	@printf '%-16s' '.env';          [ -f .env ] && echo 'present'                                              || echo 'MISSING - run make setup'
	@printf '%-16s' 'web deps';      [ -d web/node_modules ] && echo 'installed'                                || echo 'MISSING - run make web-install'
	@printf '%-16s' 'pw browsers';   if [ ! -d web/node_modules/@playwright/test ]; then echo 'unknown - run make web-install first'; \
	  elif ( cd web && node -e 'const{chromium}=require("@playwright/test");process.exit(require("fs").existsSync(chromium.executablePath())?0:1)' ) 2>/dev/null; then echo 'installed'; \
	  else echo 'MISSING - run make e2e-install (make e2e needs it)'; fi

# ---- Go server -------------------------------------------------------------

run:
	@mkdir -p "$(DEV_UPLOADS_DIR)"
	@mkdir -p "$(DEV_BACKUP_DIR)"
	go run ./cmd/uruni serve

# The embed pipeline (ADR-001): the React bundle must exist before the Go build
# so embed.FS picks it up. Never `go build` alone for a shippable binary - it
# would embed a stale (or empty) web/dist.
build: web-build
	go build -o bin/uruni ./cmd/uruni

# Run the built artifact rather than `go run`, with .env exported: .env is a
# Makefile convenience and the binary reads only real environment variables
# (ADR-019), so a bare `./bin/uruni serve` fails on URUNI_BASE_URL. Also
# `go run` binaries carry no VCS record, so `uruni version` and /healthz report
# commit "unknown"; this reports the real SHA, as the image does.
serve-bin: build
	@mkdir -p "$(DEV_UPLOADS_DIR)"
	@mkdir -p "$(DEV_BACKUP_DIR)"
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

# Through 0.x the schema is one file edited in place (ADR-025), and goose
# records applied migrations by *number* - so it cannot tell that 00001 changed
# and reports "up to date" against a stale database. This is the normal move
# after pulling a schema change; there is no data to lose before v1.0.0.
# -wal/-shm go too, or SQLite replays a journal belonging to a file that no
# longer exists.
db-reset:
	@rm -f $(DEV_DB) $(DEV_DB)-wal $(DEV_DB)-shm
	@URUNI_DB="$(DEV_DB)" go run ./cmd/uruni migrate up
	@echo "dev db: $(DEV_DB) recreated from internal/db/migrations/"

# ---- web -------------------------------------------------------------------

web-install:
	( cd web && npm install )

web-dev:
	( cd web && npm run dev )

web-build:
	( cd web && npm run build )

web-lint:
	( cd web && npm run -s lint )

web-fmt:
	( cd web && npm run -s fmt )

web-fmt-check:
	( cd web && npm run -s fmt:check )

web-typecheck:
	( cd web && npm run -s typecheck )

web-test:
	( cd web && npm run -s test )

# ---- background dev servers ------------------------------------------------
# `make restart` kills any running server + vite processes and starts fresh
# ones in the background, logging to files. Useful after a migration change
# (the server runs goose on serve) or when iterating on env vars.
#
# Stops wait for the process to exit; starts poll for real readiness (server
# /healthz; vite's "Local:" line) rather than a blind sleep, and fail loud: the
# poll exits non-zero if the process dies (compile error, panic, port bound) or
# never signals readiness, printing the tail of the log. A failed server-restart
# short-circuits `restart`, so vite isn't restarted against a server that never
# came up.

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
	@mkdir -p "$(DEV_UPLOADS_DIR)"
	@mkdir -p "$(DEV_BACKUP_DIR)"
	@( exec nohup go run ./cmd/uruni serve ) > $(SERVER_LOG) 2>&1 < /dev/null &
	@seen=0; for i in $$(seq 1 100); do \
	  curl -fsS http://localhost:$(SERVER_PORT)/healthz >/dev/null 2>&1 && { echo "server: started (log: $(SERVER_LOG))"; exit 0; }; \
	  if pgrep -f 'go run ./cmd/uruni serve' >/dev/null 2>&1 || pgrep -x uruni >/dev/null 2>&1; then seen=1; elif [ $$seen = 1 ]; then break; fi; \
	  sleep 0.1; \
	done; \
	echo "FAIL server failed to start (died or timed out) - tail of $(SERVER_LOG):" >&2; \
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
	echo "FAIL web failed to start (died or timed out) - tail of $(WEB_LOG):" >&2; \
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

# ---- e2e (Playwright; ADR-039) ---------------------------------------------
# `make e2e` needs no reset step: Playwright's global setup builds the binary
# and seeds a template database, and every spec file resets its worker's
# instance from it (web/playwright.config.ts). The binary embeds web/dist, so
# the SPA is built first - a run never tests a stale bundle. E2E_ARGS forwards
# Playwright flags:
#   make e2e E2E_ARGS='--grep @smoke'

e2e: web-build
	@( cd web && npm run -s test:e2e -- $(E2E_ARGS) )

# What .github/workflows/e2e-run.yml runs, after its own `npm ci` and
# `npm run build` steps have built web/dist. CI=true is what
# playwright.config.ts reads for forbidOnly, one retry and the github reporter.
# To reproduce the PR gate:
#   make e2e-ci E2E_ARGS='--grep @smoke'
e2e-ci:
	@( cd web && CI=true npm run -s test:e2e -- $(E2E_ARGS) )

# `npm ci` installs the Playwright *runner*, never the browser - a separate
# few-hundred-MB download, once per machine. Keeping it out of `make setup`
# keeps a fresh clone fast for contributors who never run e2e; `make doctor`
# tells the rest of us to run this. Chromium alone, because
# playwright.config.ts declares no `projects` and runs the default browser only
# (at a phone viewport).
e2e-install:
	@( cd web && npx playwright install chromium )

# Not part of `make e2e`: this and e2e-server run the one seeded instance by
# hand, e.g. to click through what a spec sees.
e2e-reset:
	@rm -f $(E2E_DB) $(E2E_DB)-wal $(E2E_DB)-shm
	@rm -rf $(E2E_BACKUP_DIR) $(E2E_UPLOADS_DIR) && mkdir -p $(E2E_BACKUP_DIR) $(E2E_UPLOADS_DIR)
	@URUNI_DB="$(E2E_DB)" URUNI_LOG_LEVEL=$(E2E_LOG_LEVEL) go run ./cmd/uruni seed-e2e
	@echo "e2e db: $(E2E_DB) ready"

e2e-server: e2e-reset
	@URUNI_DB="$(E2E_DB)" URUNI_BACKUP_DIR="$(E2E_BACKUP_DIR)" URUNI_UPLOADS_DIR="$(E2E_UPLOADS_DIR)" PORT=$(E2E_PORT) URUNI_LOG_LEVEL=$(E2E_LOG_LEVEL) go run ./cmd/uruni serve

# Rehearse a schema-changing upgrade (#516) before doing it on the real
# instance: this tree's `rebuild` runs against a *copy* of the old database
# (DB, optional - leave it out to rehearse a first install) and a backup the
# old version wrote (BACKUP), with URUNI_DB, URUNI_UPLOADS_DIR and
# URUNI_BACKUP_DIR all in one fresh temp directory, so nothing real is read for
# write or changed. The directory is left in place and printed: it holds the
# rebuilt database and the kept old one.
upgrade-rehearsal:
	@if [ -z "$(BACKUP)" ]; then \
	  echo "usage: make upgrade-rehearsal BACKUP=<backup.zip> [DB=<old uruni.db>]" >&2; exit 1; \
	fi
	@set -e; \
	d=$$(mktemp -d); \
	mkdir "$$d/data" "$$d/uploads" "$$d/backups"; \
	if [ -n "$(DB)" ]; then \
	  cp "$(DB)" "$$d/data/uruni.db"; \
	  for s in -wal -shm; do if [ -f "$(DB)$$s" ]; then cp "$(DB)$$s" "$$d/data/uruni.db$$s"; fi; done; \
	fi; \
	echo "rehearsal directory: $$d"; \
	URUNI_DB="$$d/data/uruni.db" URUNI_UPLOADS_DIR="$$d/uploads" URUNI_BACKUP_DIR="$$d/backups" \
	URUNI_BASE_URL="$${URUNI_BASE_URL:-http://localhost:8080}" \
	go run ./cmd/uruni rebuild --from "$(BACKUP)"; \
	echo "it holds a copy of real data: rm -rf $$d when done"

# ---- self-host stack -------------------------------------------------------
# Exercises docker-compose.yml - the artifact operators run (ADR-010 calls it a
# first-class deliverable, so it should be run locally before every release,
# not only by strangers).

stack-up:
	docker compose -f $(COMPOSE) up -d

# Overrides the image name and tag only for this invocation; .env is untouched.
stack-up-local:
	docker build -t uruni-local:local .
	URUNI_IMAGE=uruni-local URUNI_TAG=local docker compose -f $(COMPOSE) up -d

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
# fast-forward main, so you never branch off a stale local main.
start-task:
	@test -z "$$(git status --porcelain)" || { echo "FAIL working tree dirty - commit or stash first, then re-run"; exit 1; }
	@git ls-remote origin HEAD >/dev/null 2>&1 || { echo "FAIL no GitHub access - unlock the SSH key (ssh-add) or run 'gh auth login'"; exit 1; }
	@git checkout main >/dev/null 2>&1 || { echo "FAIL could not switch to main"; exit 1; }
	@git pull --ff-only >/dev/null 2>&1 || { echo "FAIL pull failed (diverged or no upstream) - resolve manually"; exit 1; }
	@echo "ok on main, up to date @ $$(git rev-parse --short HEAD)"

# Pre-push gate. Mirrors .github/workflows/ci.yml step for step, so green
# locally ~ green in CI (ADR-017). Keep the two in sync when either changes -
# including the golangci-lint and sqlc versions (GOLANGCI_CI_VERSION and
# SQLC_CI_VERSION above; `make doctor` flags a mismatch). govulncheck needs the
# network: it reads the vulnerability database at vuln.go.dev.
#
# The Go steps run on go.mod's `toolchain` line, which is what setup-go
# installs in CI - not whatever Go is on your PATH. A newer local Go would
# change the answer: govulncheck scans the stdlib you build with, and a patch
# release can change export data under a pinned golangci-lint (go1.27.2 did).
# e2e is excluded: it has its own workflow (e2e.yml, ADR-039) and target.
check:
	@fail=0; \
	export GOTOOLCHAIN=$$(sed -n 's/^toolchain //p' go.mod); \
	printf '%-14s' 'golangci-lint'; golangci-lint run                >/tmp/uruni-check-go-lint.log 2>&1 && echo 'ok' || { echo 'FAIL -> /tmp/uruni-check-go-lint.log'; fail=1; }; \
	printf '%-14s' 'go test';       go test ./... -race              >/tmp/uruni-check-go-test.log 2>&1 && echo 'ok' || { echo 'FAIL -> /tmp/uruni-check-go-test.log'; fail=1; }; \
	printf '%-14s' 'go mod tidy';   go mod tidy -diff                >/tmp/uruni-check-go-tidy.log 2>&1 && echo 'ok' || { echo 'FAIL -> /tmp/uruni-check-go-tidy.log (make tidy)'; fail=1; }; \
	printf '%-14s' 'sqlc diff';     sqlc diff                        >/tmp/uruni-check-sqlc.log    2>&1 && echo 'ok' || { echo 'FAIL -> /tmp/uruni-check-sqlc.log (make sqlc; make doctor for the version)'; fail=1; }; \
	printf '%-14s' 'govulncheck';   go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./... >/tmp/uruni-check-go-vuln.log 2>&1 && echo 'ok' || { echo 'FAIL -> /tmp/uruni-check-go-vuln.log'; fail=1; }; \
	printf '%-14s' 'oxlint';        (cd web && npm run -s lint)      >/tmp/uruni-check-web-lint.log 2>&1 && echo 'ok' || { echo 'FAIL -> /tmp/uruni-check-web-lint.log'; fail=1; }; \
	printf '%-14s' 'prettier';      (cd web && npm run -s fmt:check) >/tmp/uruni-check-web-fmt.log  2>&1 && echo 'ok' || { echo 'FAIL -> /tmp/uruni-check-web-fmt.log (make web-fmt)';  fail=1; }; \
	printf '%-14s' 'tsc';           (cd web && npm run -s typecheck) >/tmp/uruni-check-web-tsc.log  2>&1 && echo 'ok' || { echo 'FAIL -> /tmp/uruni-check-web-tsc.log';  fail=1; }; \
	printf '%-14s' 'vitest';        (cd web && npm run -s test)      >/tmp/uruni-check-web-test.log 2>&1 && echo 'ok' || { echo 'FAIL -> /tmp/uruni-check-web-test.log'; fail=1; }; \
	printf '%-14s' 'web build';     (cd web && URUNI_WEB_NOTICES_JSON=$(NOTICES_CHECK_JSON) npm run -s build) >/tmp/uruni-check-web-build.log 2>&1 && echo 'ok' || { echo 'FAIL -> /tmp/uruni-check-web-build.log'; fail=1; }; \
	printf '%-14s' 'notices';       go run ./cmd/uruni-notices -web $(NOTICES_CHECK_JSON) -out /tmp/uruni-check-notices.txt >/tmp/uruni-check-notices.log 2>&1 && diff -u THIRD-PARTY-NOTICES /tmp/uruni-check-notices.txt >>/tmp/uruni-check-notices.log 2>&1 && echo 'ok' || { echo 'FAIL -> /tmp/uruni-check-notices.log (make notices)'; fail=1; }; \
	if [ $$fail -eq 0 ]; then echo 'all green'; else echo 'FAILED - read the FAIL log(s) above'; exit 1; fi

# Regenerate THIRD-PARTY-NOTICES at the repo root and commit the result.
notices:
	( cd web && URUNI_WEB_NOTICES_JSON=$(NOTICES_JSON) npm run -s build )
	go run ./cmd/uruni-notices -web $(NOTICES_JSON) -out THIRD-PARTY-NOTICES

# Create or reset a local login, for curl smoke tests against authenticated
# endpoints. Auth is local email/password (ADR-007), so there is no token to
# mint - you log in and get a session cookie:
#   make dev-user EMAIL=bendahara@example.com PASSWORD=rahasia123
#   curl -c /tmp/uruni.jar -d '{"email":"...","password":"..."}' localhost:8080/api/login
dev-user:
	@if [ -z "$(EMAIL)" ] || [ -z "$(PASSWORD)" ]; then \
	  echo "usage: make dev-user EMAIL=<email> PASSWORD=<password>" >&2; exit 1; \
	fi
	@printf '%s\n' "$(PASSWORD)" | go run ./cmd/uruni create-user "$(EMAIL)"
