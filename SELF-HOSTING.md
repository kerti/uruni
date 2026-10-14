# Self-hosting Uruni

Uruni is one small Go binary (API + public report + the embedded web app) behind Caddy for HTTPS, with SQLite as its only store. One community = one instance, and the Uruni project holds none of your data.

> Status: `0.x` pre-release. Alphas are published to `ghcr.io/kerti/uruni`; the recommended tag is the one pinned in `.env.example` (`URUNI_TAG`). Read [Upgrading](#upgrading) before you put real data in.

## Quick start (Docker Compose)

```sh
# 1. Get docker-compose.yml, Caddyfile and env.example from the release you will run
#    (github.com/kerti/uruni/releases, the assets of your tag) - not from the main branch,
#    which may be ahead of the image you pin. (In a repo checkout the file is .env.example.)
cp env.example .env

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

The app expects the proxy to pass the browser's `Host` header through unchanged, as the bundled `Caddyfile` does; a proxy that rewrites it breaks saving from older browsers.

If a variable is wrong the app exits on boot with one line naming it; `docker compose logs app` shows it.

## Data & backups

Your data lives on three Docker volumes: `uruni-data` (the database), `uruni-uploads` (receipt photos) and `uruni-backups`. Everything below concerns the last one, which the app writes to on its own.

**What the app writes.** All of these go to `uruni-backups`. Each zip holds the data as JSON plus every receipt photo, the same file as a download from Pengaturan.

| File | When | Kept |
|---|---|---|
| `uruni-<date>-<time>-daily-...zip` | Once a day (Asia/Jakarta), only if something changed since the last one, and once at boot when no dump in the current format exists | the newest 7 |
| `uruni-<date>-<time>-pre-restore-...zip` | Just before a restore (or `migrate down`) changes anything | the newest 3 |
| `uruni-pre-upgrade-<date>-<time>_from-<v>_to-<v>.sqlite` | The first time a new version starts, before it migrates: a raw copy of the database the previous version can open | the newest 3 |

The dump list in Pengaturan > Cadangan shows the zips; download one there, and **restore happens in the app** from the same screen. The pre-upgrade copies are for rolling an upgrade back (see [Upgrading](#upgrading)) and do not appear in the app.

**Copy them off the machine.** The backups volume sits on the same disk as the database, so a dead disk or a deleted server takes both. Uruni deliberately does not ship backups anywhere ([ADR-012](./docs/ADR/012-backup-and-export.md)); syncing `uruni-backups` off the host is your job. Downloading a backup from Pengaturan and keeping it somewhere safe also counts as an off-site copy, just a manual one. A worked example with [restic](https://restic.net) to any S3-compatible bucket, run from the host's root crontab:

```sh
#!/bin/sh
# /usr/local/bin/uruni-offsite.sh (chmod 700)
# /etc/uruni-backup.env (chmod 600): RESTIC_REPOSITORY=s3:https://<endpoint>/<bucket>/uruni,
# RESTIC_PASSWORD=..., AWS_ACCESS_KEY_ID=..., AWS_SECRET_ACCESS_KEY=...
# Find the volume's real name with: docker volume ls | grep uruni-backups
set -a; . /etc/uruni-backup.env; set +a
DIR=$(docker volume inspect -f '{{ .Mountpoint }}' uruni_uruni-backups)
restic backup "$DIR" \
  && restic forget --keep-daily 14 --keep-weekly 8 --keep-monthly 12 --prune \
  && curl -fsS -m 10 --retry 3 https://hc-ping.com/<your-check-uuid>
```

```cron
30 3 * * * /usr/local/bin/uruni-offsite.sh
```

- **restic encrypts the copy**, and that matters (see below). Keep `RESTIC_PASSWORD` somewhere other than this server too: without it the copy cannot be read, and a copy whose only key died with the server is no copy. With rclone instead, use a `crypt` remote.
- **The last line pings a dead-man's switch** (for example healthchecks.io) only when the backup and prune succeed, so a backup that silently stops landing raises an alert.
- **Aim for** losing at most a day of entries (RPO 24 h) and being back within four hours (RTO 4 h). Once a quarter, restore the newest off-site dump into a throwaway instance and check its balances match the live one; a backup you have never restored is a hope.

**Treat every backup as sensitive.** A zip holds every member's name and amount, the treasurer's password hash, and the public report's link (`report_slug`). The link is the only thing guarding the report, so anyone holding a backup can read the live report. Keep copies encrypted and off any public location. If one leaks, renew the link in Pengaturan (Buat tautan baru) and set a new treasurer password with `docker compose exec app /uruni create-user <their email> <new password>`, which resets the password of an existing account.

**Replacing the database file by hand.** Stop the app first (`docker compose stop app`). A file swapped under a running server is detected and the app reports itself unhealthy ([#278](https://github.com/kerti/uruni/issues/278)), but anything written in between is lost. When you put a file in place, delete `uruni.db-wal` and `uruni.db-shm` beside it if they exist, or SQLite may replay the old file's pending writes onto it.

## Upgrading

The version is your **upgrade contract**: patch = drop-in; minor = additive migration, applied on boot, drop-in; major = breaking but data survives, **read the release notes** for manual steps. Bump `URUNI_TAG`, then `docker compose pull && docker compose up -d`.

> **The contract starts at `v1.0.0`.** Every `0.x` build is a preview: the schema is a single migration file that is still being edited in place, so an upgrade can require starting your data over ([ADR-025](./docs/ADR/025-one-migration-file-until-1.0.md)). Run `0.x` only on data you are willing to re-enter, and read the release notes before bumping `URUNI_TAG`.

**If the app will not start after a bump.** A build whose schema changed refuses to start on a database made by an older one, and under compose's `restart: unless-stopped` that is a restart loop: `docker compose ps` shows the app restarting and the site is down. `docker compose logs app` ends with `uruni: database schema does not match this version`, naming what differs. Nothing in your data has been touched, so putting the previous `URUNI_TAG` back and running `docker compose up -d` returns you to where you were. To move on to the new tag, first download a backup from Pengaturan on the old version, then `docker compose down` and run `uruni rebuild --from <backup.zip>` with the new tag, e.g. `docker compose run --rm -v "$PWD:/in:ro" app rebuild --from /in/backup.zip`. It refuses a backup that is older than the database (or another instance's), builds a new database of the new schema with your login carried over, and keeps the old file next to it as `uruni.db.pre-<version>-<date>-<time>`; then `up -d`. `make upgrade-rehearsal BACKUP=<backup.zip>` in a source checkout rehearses it on a copy first. The release notes say whether the new build still accepts the old backup format.

To see what you are actually running:

```bash
docker compose exec app /uruni version   # uruni v0.1.0-alpha.1 (commit 1a2b3c4)
curl https://your-domain/healthz         # {"status":"ok","version":"v0.1.0-alpha.1","commit":"1a2b3c4"}
```

`/healthz` is unauthenticated, so the second one works from anywhere and needs no shell on the host — handy for confirming an upgrade actually took.
