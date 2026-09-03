# Self-hosting Uruni

Uruni is one small Go binary (API + public report + the embedded web app) behind Caddy for HTTPS, with SQLite by default. One community = one instance, and the Uruni project holds none of your data.

> Status: pre-release skeleton. Commands and env names may change before the first alpha. Track the pinned recommended tag in `.env.example` (`URUNI_TAG`).

## Quick start (Docker Compose)

```sh
# 1. Get the compose file + example env (from a release, or the repo).
cp .env.example .env

# 2. Edit .env — set URUNI_BASE_URL to your public https origin, and pin URUNI_TAG to the
#    release you want. Point the compose image at ghcr.io/<owner>/uruni.

# 3. Bring it up (Caddy fetches a TLS cert automatically for your domain).
docker compose up -d
```

Then open your domain and sign in as the treasurer.

## Configuration

| Variable | Purpose |
|---|---|
| `URUNI_TAG` | Pinned image version to run. |
| `URUNI_BASE_URL` | Public base URL — Caddy serves this host and fetches TLS for it — the shareable report link is built from it, and its scheme decides whether the session cookie is `Secure`. **Required: the app refuses to start unset or on the `https://uruni.example.com` placeholder.** |
| `SMTP_URL` | Optional — enable emailed periodic backups. |
| `URUNI_LOG_LEVEL` | Optional — `debug`, `info` (default), `warn`, `error`. |
| `URUNI_LOG_FORMAT` | Optional — `text` (default) or `json`. |

If a variable is wrong the app exits on boot with one line naming it; `docker compose logs app` shows it.

## Data & backups

Your data lives on Docker volumes (`uruni-data`, `uruni-uploads`, `uruni-backups`). Take backups from inside the app (JSON export, canonical + restorable; optional Excel), and optionally enable scheduled server-side dumps. Keep exported data off any public location — it contains member names and amounts.

## Upgrading

The version is your **upgrade contract**: patch = drop-in; minor = additive migration, applied on boot, drop-in; major = breaking but data survives, **read the release notes** for manual steps. Bump `URUNI_TAG`, then `docker compose pull && docker compose up -d`.

> **The contract starts at `v1.0.0`.** Every `0.x` build is a preview: the schema is a single migration file that is still being edited in place, so an upgrade can require starting your data over ([ADR-025](./docs/ADR/025-one-migration-file-until-1.0.md)). Run `0.x` only on data you are willing to re-enter, and read the release notes before bumping `URUNI_TAG`.

To see what you are actually running:

```bash
docker compose exec app /uruni version   # uruni v0.1.0-alpha.1 (commit 1a2b3c4)
curl https://your-domain/healthz         # {"status":"ok","version":"v0.1.0-alpha.1","commit":"1a2b3c4"}
```

`/healthz` is unauthenticated, so the second one works from anywhere and needs no shell on the host — handy for confirming an upgrade actually took.

> TODO (filled in as the app is built): exact env var names, first-run setup, migration/boot behavior, and a worked backup/restore example.
