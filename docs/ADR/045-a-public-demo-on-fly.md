# ADR-045 - A public demo on Fly.io

**Status:** `draft` - drafted and grilled 2026-10-26 for [#144](https://github.com/kerti/uruni/issues/144), before any code, after a look at how Balances runs its demo; the tag drops in the slice that deploys it · supersedes [ADR-016](./016-deployment-targets-reference-infra.md) and [ADR-017](./017-cicd-github-actions.md) on one point each, named at the foot · amends the [ADR-019](./019-cli-surface-and-runtime-config.md) runtime-config table · [ADR index](./README.md)

**Context.** The only way to see Uruni today is to run it: clone and `make`, or stand up the compose stack on a server. A treasurer deciding whether to try it, or a self-hoster deciding whether to spend an evening on it, has screenshots and a README and nothing to touch. [#8](https://github.com/kerti/uruni/issues/8) asked for a public demo "at the earliest useful opportunity" and named the trigger itself: a record -> home -> reconcile loop worth showing. That loop shipped at M6; `v0.10.0` is the line meant for real use, with two live instances on it. The demo is warranted now in the sense ADR-017 reserved ("add them only when warranted"): there is a finished product people are being asked to adopt, and nowhere to look at it.

Two implemented ADRs say no to this as written. ADR-017: "**no** preview/demo/production environment split" and "There is no `deploy.yml`". ADR-016: deployment-specific config lives "in a private ops note / `.env` outside this repo". A demo the project runs in public, from config anyone can read and copy, is neither.

**The maintainer already runs one.** Balances, a sibling project on the same Fly account, has a public demo at `demo.balances.kerti.dev`, and Afloat plans `demo.afloat.kerti.dev`. This ADR follows what Balances learned, where it applies:
- **Deploy the published image by reference**, never a rebuild: what the demo runs is byte for byte what self-hosters pull.
- **Check the rolled-out version** after a deploy. `flyctl deploy` succeeding proves only that one health check passed.
- **Show the shared credentials on the login screen.** Visitors do not arrive with the README open.
- **Block the one action that would lock every later visitor out of the shared login.**

Uruni has an advantage Balances did not: its only store is a SQLite file. On a machine whose root filesystem is rebuilt from the image at every start, an empty database is the default, and "reset" means "start again".

## Decision

**1. One public demo at `https://demo.uruni.kerti.dev`, run by the project.**
- Fly app `uruni-demo`, in the maintainer's existing Fly account and organization, beside Balances. Region `sin`, closest to Indonesia.
- One `shared-cpu-1x` machine with 256 MB. `auto_stop_machines = "stop"`, `auto_start_machines = true` and `min_machines_running = 0`: it costs close to nothing while nobody is looking at it.
- The hostname is a Cloudflare DNS-only CNAME to `uruni-demo.fly.dev`, with Fly's own Let's Encrypt certificate, as Balances does. No Fly volume.

**2. The demo is the shipped image with one setting: `URUNI_DEMO=true`.** `flyctl deploy --image ghcr.io/kerti/uruni:<tag>` runs no build of its own. Every other instance leaves the setting unset, and with it unset nothing below exists. With it set, `serve` changes exactly five things - the four below, and the uptime limit of decision 3:

- **It seeds an empty database at boot.** No login yet means the demo fixture (decision 5) is written before the server listens.
- **It refuses to boot on someone else's data.** A database whose one login is not the demo login makes `serve` stop with an error naming `URUNI_DEMO`. The setting cannot be turned on over a real fund by accident.
- **The login screen offers the demo credentials.** `GET /api/session`, the unauthenticated probe the login screen already makes, carries the demo email and password beside `authenticated` and `has_account`. The form arrives filled in, under a notice that also spells the credentials out, so a visitor who clears the fields can type them back:
  > **Ini demo Uruni.** Semua datanya rekaan, dan siapa pun yang masuk bisa mengubahnya - jadi jangan simpan data asli di sini. Demo kembali bersih sendiri, paling lama setiap jam. Email `demo@uruni.test` · Kata sandi `coba-uruni`

  The email sits under the reserved `.test` domain, like `seed-e2e`'s. The password must clear the `password_too_common` denylist.
- **The demo login cannot be changed.** `POST /api/user/password` and `POST /api/user/email` are refused with one error code of their own. Both settings screens stay reachable and show their usual form - a visitor deciding whether to adopt Uruni should see that the login can be changed - with the save button disabled under one line: *Di demo, email dan kata sandi tidak bisa diubah supaya pengunjung lain tetap bisa masuk.* Either change would lock every later visitor out of the pre-filled login until the next reset - Uruni's version of Balances' blocked Erasure. Nothing else is blocked:
  - **restore** keeps the live login (`keepLiveUser`, [ADR-012](./012-backup-and-export.md)), so it cannot strand anyone;
  - **regenerating the report link** breaks the README's link to the demo report (decision 5 fixes its slug) until the next reset, which is defacement like any other;
  - every other write is defacement at worst, and the reset (decision 3) undoes it.

Responses also carry `X-Robots-Tag: noindex, nofollow` (decision 6).

**3. Every start is a reset, and no demo lives longer than an hour.** A Fly machine's root filesystem is rebuilt from its image at every start (`persist_rootfs = "never"`, the default). With decision 2, every start is therefore a fresh demo:
- **Idle:** Fly stops the machine a few minutes after the last request, and the next visitor starts a new demo.
- **One hour of uptime:** a machine someone keeps warm never idles, so under `URUNI_DEMO`, `serve` shuts down gracefully after one hour of uptime and exits 0. Fly treats that like an idle stop, and the next request starts a fresh machine. That bounds anything a visitor leaves behind to one hour: spam in the notes the public report shows, a stranger's backup restored over the fund, or 50 failed logins that close the demo account under the identifier ceiling (`login.go`, which lives in memory).

This is a fifth behavior of the switch, chosen over an hourly `flyctl machine restart` from a GitHub `schedule`:
- A scheduled job runs late or not at all under load, is disabled after 60 days without repo activity, and holds a deploy-capable token every hour.
- A restart *starts* a stopped machine, so a careless cron wakes an idle demo 24 times a day.
- The uptime limit only ever stops a machine sooner. The demo runs, and is billed, only while someone is using it, as Balances' does.

Balances rejected an in-process reset because it couples to process uptime. Here that coupling is the property wanted: no demo state outlives an hour. It is tested in Go. That Fly treats exit 0 as a stop and auto-starts cleanly afterward, with no restart loop, is confirmed against the running demo in the PR that ships it.

A reset logs every visitor out, back to a filled-in login form. The fixture's dates are computed at boot, so "this month" is always current without a redeploy. Balances' Erasure argument was about a nightly reset - up to a day of a dead demo. One hour of a locked login would be tolerable, but refusing the change costs one check and spares every visitor that hour.

**4. Deploys follow releases.** `.github/workflows/demo.yml`:
- **Deploy:** on `workflow_run` of `release.yml` succeeding, and on `workflow_dispatch` with a tag input for a redeploy or a rollback. It runs `flyctl deploy --config deploy/fly.toml --image ghcr.io/kerti/uruni:<tag>`, then polls `https://demo.uruni.kerti.dev/healthz` until `version` reads that tag. Every Uruni tag already goes to the maintainer's live instances, so a tag is a build already chosen. Balances needed a manual promote because its tag suffix picked the environment; nothing here does.
- **One credential:** a Fly deploy token scoped to the `uruni-demo` app (`fly tokens create deploy -a uruni-demo`), stored as the repo secret `FLY_API_TOKEN`. Its blast radius is the demo app. Nothing runs on a schedule, so nothing holds it outside a deploy.
- **Never a gate:** the workflow is not a required status check, and a red run blocks nothing. The demo trails the product.

**5. The fixture: one CS shift, fourteen months, every scenario.** It lives in the product, in `internal/demo`. It is written by **replaying history through the real ledger** in date order, with the ledger's clock (`ledger.WithClock`) moved to each entry's day. Every row passes the checks live use does - the fund's date ceiling ([ADR-041](./041-the-funds-time-zone.md)), a reversal's dating against a *cek kas* ([ADR-042](./042-reversing-any-ordinary-transaction.md)), a closed envelope refusing postings ([ADR-031](./031-posting-to-a-closed-incidental.md)) - and a scenario the ledger would refuse fails the seeder's test rather than shipping. Nothing is inserted past the ledger.

**The fund** is *CS Shift Sore*: the customer-service agents on one office afternoon shift. It follows how a real unit names its fund - who they are, not "kas", not "contoh" - and carries the office storyline the tiers imply. WIB. Cash and a bank account. A fixed report slug the README links to, `CsShiftSoreDemoUruni2026`: base62 and at least the schema's 22 characters, like every slug. `SetUpFund` takes it as an optional parameter only the seeder passes; every real fund still gets a random one. The treasurer login `demo@uruni.test` / `coba-uruni`.

**The window moves with the clock.** History covers the current month and the thirteen before it, M-13 to M0, so the twelve-month grid ([ADR-044](./044-twelve-months-of-dues.md)) visibly stops short of where the report's month picker still reaches. Every event sits at a fixed offset from the current month, never on a calendar month. Day-level entries use fixed days of their month; in M0 they are compressed into the 1st through today, and nothing is dated after today. The story reads the same whatever month a reset falls in:

| Month | What happens |
|---|---|
| M-13 | The fund is set up: opening balances on cash and bank, dues members paid before the fund existed settled with an adjusted *cek kas* plug, 11 members on three tiers |
| M-10 | An agent joins from Shift Pagi |
| M-8 | The Supervisor moves to another unit (*pindah*): *Perpisahan Bu Ratna* opened with her as recipient, closed, its leftover rolled into Kas Utama. The Supervisor tier stays empty from here |
| M-6 | Every rate rises (*Mulai bulan* that month) |
| M-5 | An Agen is promoted to Team Leader |
| M-4 | An agent resigns and goes inactive, owing through that month |
| M-3 | A second agent joins |
| M-2 | *Jenguk Pak Hendra* opened and closed, then reopened when late contributions come in |
| M0 | *Kelahiran anak Mbak Sari* open, with a minimum per member and a recipient; a reimbursement still owed; the latest *cek kas*, which matches |

**Tiers and rates:**

| Tier | Before M-6 | From M-6 |
|---|---|---|
| Agen | Rp 25.000 | Rp 30.000 |
| Team Leader | Rp 40.000 | Rp 50.000 |
| Supervisor | Rp 60.000 | Rp 75.000 |

Spread across the window, so every screen has its case:
- **Dues:** mostly paid, some partly paid, two members behind for several months, one paid ahead, some paying two months at once, one dues payment reversed; one rate corrected (*Perbaiki nominal*), one tier corrected, one member *Tanpa golongan* for a stretch.
- **Envelopes:** all three participation states - *Sudah menyumbang*, *Belum menyumbang*, *Kurang dari minimal* - plus an anonymous contribution.
- **Movements:** a pass-through collected and forwarded to Kas Bidang; a reimbursement settled and another still owed; cash deposited at the bank; a *Pindah pos*; a purpose correction; a reversed ordinary expense.
- **Cek kas:** about monthly, all *cocok* but one settled with an adjusted plug.

About 25 entries a month - dues, three to five ordinary expenses, contributions when an envelope is open, one or two movements - roughly 350 in all, so Riwayat pages and a month on the report reads like a real one. Seeding takes well under a second at boot.

**Receipts:** two or three are drawn in Go at seed time - a paper-toned image with a few lines of text - and posted through the ordinary receipt pipeline, on the settled reimbursement and on one expense. The repo carries no binary fixture.

Names are invented and ordinary; the full roster is in the PR that ships the fixture, for the maintainer to check before it ships. Amounts are toy amounts. There is no `seed-demo` subcommand: `serve` seeds, so nothing else needs to. Trying the fixture locally is `URUNI_DEMO=true` against an empty `URUNI_DB`.

**The fixture is tested on a dozen fixed "todays"** - 1 January, 29 February of a leap year, 31 December, the 1st and the last day of an ordinary month, mid-month, and so on. Each must replay with no ledger refusal, end on a matching *cek kas*, and give a grid exactly twelve months wide.

**6. Config, in the repo, all public.** `deploy/fly.toml`, beside `deploy/cloud-init.example.yaml`, holds the app, region, machine size, auto-stop settings and `/healthz` check, plus an `[env]` block:
- `URUNI_DEMO=true`;
- `URUNI_BASE_URL=https://demo.uruni.kerti.dev`;
- `URUNI_TRUSTED_PROXIES`, set to the range Fly's edge connects from, so the login limiter keys on the visitor and not on Fly. The range is confirmed against the running demo in the PR that ships it.

There are no Fly secrets. `noindex` is a header `serve` sets under `URUNI_DEMO`, not a `fly.toml` response-header rule: `serve` is tested here, Fly's header rewriting is not, and a demo found by a search engine is the failure this exists to prevent.

## Consequences

- **The demo does not test what self-hosters run.** The shipped topology is `app + caddy` via compose ([ADR-009](./009-reverse-proxy-caddy.md), [ADR-010](./010-packaging-and-deployment.md)). On Fly, Caddy is absent and Fly's edge terminates TLS. The demo shows the product. It is not a smoke test of the deployment.
- **The product carries a demo setting and a fixture.** That is code no self-hoster uses, the cost of deploying the published image unchanged. The boot refusal in decision 2 keeps it from ever touching a real fund. Its tests run in `make check` like any other.
- **The demo never exercises the upgrade contract** ([ADR-018](./018-release-and-versioning.md)). Each start re-seeds from scratch, so a release with a schema change reaches it with no `rebuild` and no backup.
- **Visitors share one demo.** Two visitors at once see each other's writes. No per-visitor isolation is built, as Balances also decided.
- **The release cut gains no step.** The demo follows the release by itself, and the maintainer holds one more credential (`FLY_API_TOKEN`) and one more Fly app, close to $0 when idle.

## Supersedes

- **ADR-017**, "**no** preview/demo/production environment split" and "There is no `deploy.yml`": there is now one demo environment, deployed by `demo.yml`. The maintainer's own instances are still deployed by hand, from config outside this repo. Nothing else in ADR-017 changes.
- **ADR-016**, "All deployment-specific config ... lives in a private ops note / `.env` outside this repo", for this one target only. The demo's config is public; it holds no secret and no private data. The product stays provider-agnostic.

## Amends

- **ADR-019**: a `URUNI_DEMO` row in the runtime-config table (optional, off by default), on the same maintainer's-call stretch as `URUNI_TRUSTED_PROXIES` (#447). It lands in the PR that ships the setting.
