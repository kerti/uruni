# Self-hosting Uruni

Uruni is one small Go binary (API + public report + the embedded web app) behind Caddy for HTTPS, with SQLite as its only store. One community = one instance, and the Uruni project holds none of your data.

> Status: `0.x` pre-release. Alphas are published to `ghcr.io/kerti/uruni`; the recommended tag is the one pinned in `.env.example` (`URUNI_TAG`). Read [Upgrading](#upgrading) before you put real data in.

## Before you start

**The host.** Any Linux machine with Docker Engine and Compose v2 (`docker compose`, not `docker-compose`), amd64 or arm64. A domain name whose DNS `A` (and `AAAA`, if the host has IPv6) record points at it, and ports 80, 443/tcp and 443/udp reachable from the internet: Caddy needs 80 and 443 to fetch the TLS certificate. The smallest VPS most providers sell (1 vCPU, 1 GB RAM) is plenty; the app's worst moment is four password hashes at once, about 80 MB.

**Disk.** Budget the database plus about **11 times the receipt photos**: the live photos, and a copy of every one inside each of the 7 daily and 3 pre-restore backups the app keeps (see [Data & backups](#data--backups)). The database itself is small; three pre-upgrade copies of it are kept as well.

**Host checklist.** None of this is Uruni's, all of it matters:

- a firewall that lets in SSH, 80/tcp, 443/tcp and 443/udp, and nothing else
- unattended security upgrades
- Docker log rotation for every container (the compose file rotates its own two)
- a little swap, so a memory spike slows the host instead of killing the app
- off-site copies of the backups (see [Copy them off the machine](#data--backups))

[`deploy/cloud-init.example.yaml`](./deploy/cloud-init.example.yaml) does the first four on a fresh Ubuntu 24.04 VPS and fetches the release's files into `/opt/uruni`: paste it as the provider's "user data" when you create the machine.

## First run

```sh
# 1. Get docker-compose.yml, Caddyfile and env.example from the release you will run
#    (github.com/kerti/uruni/releases, the assets of your tag) - not from the main branch,
#    which may be ahead of the image you pin. (In a repo checkout the file is .env.example.)
cp env.example .env

# 2. Edit .env - set URUNI_BASE_URL to your public https origin. URUNI_TAG is already
#    pinned to the release the file came from; the compose file runs
#    ghcr.io/kerti/uruni:$URUNI_TAG and refuses to start if it is unset.

# 3. Start only the app. Caddy is not up yet, so nobody can reach the instance.
docker compose up -d app

# 4. Create the treasurer's login. It asks for the password twice.
docker compose exec app /uruni create-user treasurer@example.org

# 5. Now open it to the world (Caddy fetches a TLS cert for your domain).
docker compose up -d
```

Then open your domain, sign in, and the app walks you through setting up the fund.

**Why the login comes first.** A fresh instance with no account lets the first visitor register as its treasurer, and registration closes once one account exists ([ADR-030](./docs/ADR/030-multi-fund-scoping.md)). Bringing Caddy up before step 4 leaves that door open to anyone who finds the address first; creating the login while only the app runs closes it before it opens.

**If someone else got there first** (you started the whole stack and `create-user` answers `holds one login and it is not ...`): the instance belongs to a stranger and holds nothing of yours. Close it and start over:

```sh
docker compose down
docker volume rm uruni_uruni-data uruni_uruni-uploads uruni_uruni-backups
docker compose up -d app
docker compose exec app /uruni create-user treasurer@example.org
docker compose up -d
```

Volume names are prefixed with the compose project, which is the directory's name: `docker volume ls | grep uruni` shows yours. Leave `caddy-data` alone; it holds the certificate.

## Getting back in

A forgotten password, or a login you need to hand to a new treasurer, is reset on the host:

```sh
docker compose exec app /uruni create-user treasurer@example.org
```

With the email of the existing login it sets the new password and signs out every session; with a different email it refuses, because there is only ever one login. The new treasurer can then change the email and password in Pengaturan. A password must be at least 8 characters and not one of the common passwords on a built-in list (the 10,000 most common, of which about two thousand are long enough to matter); `create-user` says so and writes nothing when it refuses one.

`create-user` asks for the password when you run it in a terminal, and reads the first line of standard input otherwise (`docker compose exec -T app /uruni create-user treasurer@example.org < password.txt`). It still accepts the password as a second argument, but warns: an argument is kept in your shell history and is visible to every user on the host in `ps`.

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
| `uruni-<date>-<time>-daily-...zip` | Once a day (in the fund's time zone, WIB unless the treasurer chose WITA or WIT), only if something changed since the last one, and once at boot when no dump in the current format exists | the newest 7 |
| `uruni-<date>-<time>-pre-restore-...zip` | Just before a restore (or `migrate down`) changes anything | the newest 3 |
| `uruni-pre-upgrade-<date>-<time>_from-<v>_to-<v>.sqlite` | The first time a new version starts, before it migrates: a raw copy of the database the previous version can open (stamped in WIB, whatever the fund's zone: no fund can be read that early) | the newest 3 |

A dump's name is stamped in the fund's time zone. A fund in WITA or WIT has `utc8` or `utc9` after the time (`uruni-20261002-003000-utc8-daily-...zip`); a name with no such token is Asia/Jakarta, so every name a WIB instance wrote keeps its meaning and its shape. The same token is why changing the zone never re-dates an older file.

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

**Treat every backup as sensitive.** A zip holds every member's name and amount, the treasurer's password hash, and the public report's link (`report_slug`). The link is the only thing guarding the report, so anyone holding a backup can read the live report. Keep copies encrypted and off any public location. If one leaks, renew the link in Pengaturan (Buat tautan baru) and set a new treasurer password (see [Getting back in](#getting-back-in)).

**Replacing the database file by hand.** Stop the app first (`docker compose stop app`). A file swapped under a running server is detected and the app reports itself unhealthy ([#278](https://github.com/kerti/uruni/issues/278)), but anything written in between is lost. When you put a file in place, delete `uruni.db-wal` and `uruni.db-shm` beside it if they exist, or SQLite may replay the old file's pending writes onto it.

## Upgrading

The version is your **upgrade contract**: patch = drop-in; minor = additive migration, applied on boot, drop-in; major = breaking but data survives, **read the release notes** for manual steps. Bump `URUNI_TAG`, then `docker compose pull && docker compose up -d`.

> **The contract starts at `v1.0.0`.** Every `0.x` build is a preview: the schema is a single migration file that is still being edited in place, so an upgrade can require starting your data over ([ADR-025](./docs/ADR/025-one-migration-file-until-1.0.md)). Run `0.x` only on data you are willing to re-enter, and read the release notes before bumping `URUNI_TAG`.

**Upgrading across a schema change.** A build whose schema changed refuses to start on a database made by an older one, and under compose's `restart: unless-stopped` that is a restart loop: `docker compose ps` shows the app restarting and the site is down. `docker compose logs app` ends with `uruni: database schema does not match this version`, naming what differs. Nothing in your data has been touched, so putting the previous `URUNI_TAG` back and running `docker compose up -d` returns you to where you were. The release notes say when a release changes the schema, so you can do it in this order rather than find out:

1. On the old version, download a backup from Pengaturan > Cadangan, as the last thing anyone does there. Copy it to the host, readable by the container (`chmod 644 backup.zip`).
2. `docker compose down`, then set the new `URUNI_TAG` in `.env`.
3. `docker compose run --rm -v "$PWD:/in:ro" app rebuild --from /in/backup.zip`. It refuses a backup that is older than the database (or another instance's), builds a new database of the new schema with your login carried over, checks that every fund's balance came out the same, and keeps the old file next to it as `uruni.db.pre-<version>-<date>-<time>`.
4. `docker compose up -d`, and sign in again.

`make upgrade-rehearsal BACKUP=<backup.zip>` in a source checkout rehearses it on a copy first.

**Backups across a format change.** A backup file carries a `format_version`. A build reads its own and every older one it has an upgrade step for, converting it as it reads (the in-app restore, the stored-dump list and `rebuild` all do), and refuses a newer one; the release notes say which formats a build reads. The release that moved the backup to format 2 (member tiers by month, the fund's time zone) reads the previous release's format-1 backups, so step 1 above works unchanged. It writes only format 2, which the previous release cannot restore: keep the backup from step 1 until you are sure of the upgrade.

**Never run `uruni migrate down` on `0.x`.** There is one migration, so one step down is the whole schema: it empties the database. It takes a backup zip first and refuses while the app runs, but it is not a way back from an upgrade; the two below are.

### Rolling back

- **An upgrade without a schema change** left your data in a shape the previous version reads: put the previous `URUNI_TAG` back in `.env` and `docker compose up -d`.
- **After a rebuild**, the old database is still beside the new one. The image has no shell, so move files with a throwaway container (`65532` is the app's user, which must own the database):

  ```sh
  docker compose down
  docker run --rm -v uruni_uruni-data:/data -v uruni_uruni-backups:/backups alpine:3 sh -c '
    cd /data && rm -f uruni.db-wal uruni.db-shm &&
    mv uruni.db uruni.db.rolled-back && mv uruni.db.pre-<version>-<date>-<time> uruni.db &&
    chown 65532:65532 uruni.db && rm -f /backups/uruni-boot-version'
  # set the previous URUNI_TAG in .env, then
  docker compose up -d
  ```

- **If that file is gone**, the newest `uruni-pre-upgrade-...sqlite` in the backups volume is the same thing: the database as it was the moment before the new version first touched it. Copy it to `/data/uruni.db` the same way, with the same `rm` of `-wal`, `-shm` and `uruni-boot-version`.

Either way, anything entered on the new version is lost and must be entered again.

## Disaster recovery

The host is gone, and you have an off-site backup zip:

1. Prepare a new host as in [Before you start](#before-you-start), with the same domain pointed at it and the same `URUNI_TAG` as the backup came from, or a later one whose release notes say it reads that backup.
2. `cp env.example .env`, set `URUNI_BASE_URL`, and copy the backup next to it (`chmod 644`).
3. Rebuild the database from it before anything starts: `docker compose run --rm -v "$PWD:/in:ro" app rebuild --from /in/backup.zip`. On an empty volume there is nothing to compare against, so it simply builds the database, the treasurer's login included, and the instance is never open to a first registration.
4. `docker compose up -d`, sign in with the password the treasurer had when the backup was taken, and check the balances against the bank.

If you would rather not use `rebuild`: start only the app, run `create-user` as in [First run](#first-run), then `docker compose up -d`, sign in, and restore the zip from Pengaturan > Cadangan. That keeps the new login instead of the backup's.

## Checking what runs

To see what you are actually running:

```bash
docker compose exec app /uruni version   # uruni v0.1.0-alpha.1 (commit 1a2b3c4)
curl https://your-domain/healthz         # {"status":"ok","version":"v0.1.0-alpha.1","commit":"1a2b3c4"}
```

`/healthz` is unauthenticated, so the second one works from anywhere and needs no shell on the host — handy for confirming an upgrade actually took.
