# Self-hosting Uruni

Uruni is one small Go binary (API + public report + the embedded web app) behind Caddy for HTTPS, with SQLite as its only store. One community = one instance, and the Uruni project holds none of your data.

> Status: `0.x` pre-release. Alphas are published to `ghcr.io/kerti/uruni`; the recommended tag is the one pinned in `.env.example` (`URUNI_TAG`). Read [Upgrading](#upgrading) before you put real data in.

## Quick start (Docker Compose)

```sh
# 1. Get docker-compose.yml, Caddyfile and .env.example from the release you will run
#    (github.com/kerti/uruni/releases, the assets of your tag) - not from the main branch,
#    which may be ahead of the image you pin.
cp .env.example .env

# 2. Edit .env — set URUNI_BASE_URL to your public https origin, and pin URUNI_TAG to the
#    release you want (the compose file runs ghcr.io/kerti/uruni:$URUNI_TAG
#    and refuses to start if it is unset). Open ports 80, 443/tcp and 443/udp
#    (HTTP/3) on the host firewall.

# 3. Bring it up (Caddy fetches a TLS cert automatically for your domain).
docker compose up -d
```

Then open your domain and register the treasurer's account. A fresh instance asks for it on the first visit, and registration closes once an account exists ([ADR-030](./docs/ADR/030-multi-fund-scoping.md)), so do this before you share the address with anyone. After that, the app walks you through setting up the fund.

## Configuration

| Variable | Purpose |
|---|---|
| `URUNI_TAG` | Pinned image version to run. **Required: compose refuses to start unset.** |
| `URUNI_BASE_URL` | Public base URL — Caddy serves this host and fetches TLS for it — the shareable report link is built from it, and its scheme decides whether the session cookie is `Secure`. **Required: the app refuses to start unset or on the `https://uruni.example.com` placeholder.** |
| `URUNI_LOG_LEVEL` | Optional — `debug`, `info` (default), `warn`, `error`. |
| `URUNI_LOG_FORMAT` | Optional — `text` (default) or `json`. |

The compose file also sets `URUNI_DB`, `URUNI_UPLOADS_DIR` and `URUNI_BACKUP_DIR` to paths on its three volumes, and the app listens on `PORT` (8080); leave those alone unless you run the binary without compose.

It also sets `URUNI_TRUSTED_PROXIES` to the private ranges, so the app believes Caddy's `X-Forwarded-For` when it keys its per-address limits (login, registration and restore confirmation) on the real client. Set it to the address of the proxy in front of the app and nothing wider: with a proxy in front and the variable unset, every visitor looks like the proxy and they all share one limit, and if it trusts addresses a caller can reach directly, that caller can name a fresh address per request and is never counted. The compose default is right while port 8080 stays unpublished, as the compose file ships it. If you run the binary behind a different proxy, set it to that proxy's address. With no proxy, leave it unset.

If a variable is wrong the app exits on boot with one line naming it; `docker compose logs app` shows it.

## Data & backups

Your data lives on Docker volumes (`uruni-data`, `uruni-uploads`, `uruni-backups`). Download a backup from Pengaturan at any time (a zip: the data as JSON plus every receipt photo), and restore one from the same screen. The server also writes a daily dump to the `uruni-backups` volume. There is no Excel export. Keep exported data off any public location — it contains member names and amounts.

## Upgrading

The version is your **upgrade contract**: patch = drop-in; minor = additive migration, applied on boot, drop-in; major = breaking but data survives, **read the release notes** for manual steps. Bump `URUNI_TAG`, then `docker compose pull && docker compose up -d`.

> **The contract starts at `v1.0.0`.** Every `0.x` build is a preview: the schema is a single migration file that is still being edited in place, so an upgrade can require starting your data over ([ADR-025](./docs/ADR/025-one-migration-file-until-1.0.md)). Run `0.x` only on data you are willing to re-enter, and read the release notes before bumping `URUNI_TAG`.

**If the app will not start after a bump.** A build whose schema changed refuses to start on a database made by an older one, and under compose's `restart: unless-stopped` that is a restart loop: `docker compose ps` shows the app restarting and the site is down. `docker compose logs app` ends with `uruni: database schema does not match this version`, naming what differs. Nothing in your data has been touched, so putting the previous `URUNI_TAG` back and running `docker compose up -d` returns you to where you were. To move on to the new tag, first download a backup from Pengaturan on the old version, then `docker compose down`, remove the `uruni-data` volume (`docker volume ls` shows its full name; copy `uruni.db` out of it first if you want a fallback), `up -d`, register, set up a throwaway fund and restore the backup from Pengaturan. The release notes say whether the new build still accepts the old backup format.

To see what you are actually running:

```bash
docker compose exec app /uruni version   # uruni v0.1.0-alpha.1 (commit 1a2b3c4)
curl https://your-domain/healthz         # {"status":"ok","version":"v0.1.0-alpha.1","commit":"1a2b3c4"}
```

`/healthz` is unauthenticated, so the second one works from anywhere and needs no shell on the host — handy for confirming an upgrade actually took.
