# Self-hosting Uruni

Uruni is one small Go binary (API + public report + the embedded web app) behind Caddy for HTTPS, with SQLite by default. One community = one instance, and the Uruni project holds none of your data.

> Status: pre-release skeleton. Commands and env names may change before the first alpha. Track the pinned recommended tag in `.env.example` (`URUNI_TAG`).

## Quick start (Docker Compose)

```sh
# 1. Get the compose file + example env (from a release, or the repo).
cp .env.example .env

# 2. Edit .env — set your domain, a strong URUNI_SESSION_SECRET, and pin URUNI_TAG
#    to the release you want. Point the compose image at ghcr.io/<owner>/uruni.

# 3. Bring it up (Caddy fetches a TLS cert automatically for your domain).
docker compose up -d
```

Then open your domain and sign in as the treasurer.

## Configuration

| Variable | Purpose |
|---|---|
| `URUNI_TAG` | Pinned image version to run. |
| `URUNI_DOMAIN` | Domain Caddy serves + fetches TLS for. |
| `URUNI_BASE_URL` | Public base URL (used in the shareable report link). |
| `URUNI_SESSION_SECRET` | Long random secret for session cookies. |
| `DATABASE_URL` | Optional — use Postgres instead of the default SQLite. |
| `SMTP_URL` | Optional — enable emailed periodic backups. |

## Data & backups

Your data lives on Docker volumes (`uruni-data`, `uruni-uploads`, `uruni-backups`). Take backups from inside the app (JSON export, canonical + restorable; optional Excel), and optionally enable scheduled server-side dumps. Keep exported data off any public location — it contains member names and amounts.

## Upgrading

The version is your **upgrade contract**: patch = drop-in; minor = additive migration, applied on boot, drop-in; major = breaking but data survives, **read the release notes** for manual steps. Bump `URUNI_TAG`, then `docker compose pull && docker compose up -d`.

> TODO (filled in as the app is built): exact env var names, first-run setup, migration/boot behavior, and a worked backup/restore example.
