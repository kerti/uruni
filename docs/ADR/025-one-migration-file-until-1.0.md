# ADR-025 — One migration file, edited in place, until v1.0.0

**Status:** Accepted · implemented — change only by adding a superseding ADR · [ADR index](./README.md) · **supersedes the migrations bullet of [ADR-018](./018-release-and-versioning.md)**

**Context.** [ADR-018](./018-release-and-versioning.md) set the migration rule before any schema existed: goose files renumbered at merge so apply-order matches merge-order, squashing allowed only in resettable environments, immutability beginning at the first production deploy. That is the right rule for a project with data to protect.

Uruni has none. There is no instance anywhere holding a row a human would miss, and there will not be one until a real treasurer's fund goes live at `v1.0.0` — the moment [`ROADMAP.md`](../ROADMAP.md) already calls *production*. Every environment between here and there is a file someone can delete: a dev `uruni.db`, `/tmp/uruni-e2e.db`, a pilot instance whose whole purpose is to be thrown away and re-seeded.

M2 discovered this in miniature. The data-model epic (#21) put its four slices behind a long-lived branch precisely so `00001_schema.sql` could be edited in place until the schema was finished, on the reasoning that a correcting migration stacked on a migration nobody ever ran is a lie about history. That reasoning does not stop at the epic boundary. Every milestone through M9 will want the same thing, and the branch was scaffolding built to buy locally what a decision can grant globally.

The alternative is what M3–M9 look like under ADR-018: a `00002_add_x.sql` that alters a table shipped last week, then `00003` fixing `00002`'s `CHECK`, and a `00001` that no longer describes the schema anyone reads. The archaeology is real, and none of it records anything that ever happened to anyone's money.

## Decision

**`internal/db/migrations/00001_schema.sql` is the only migration through the whole `0.x` ramp.** Every schema change edits it in place. No second file is added, for any reason, before `v1.0.0`.

**The tree is the schema.** `00001_schema.sql` always reads as the current, complete schema — top to bottom, no forward references, no correcting statements. That property is now a rule rather than an M2 nicety, and it is what makes the file worth reading at all.

**Renumber-at-merge is retired.** With one file there is nothing to renumber and no apply-order to keep in step with merge-order. ADR-018's other bullets — GitHub Flow, batched tag-driven releases, label-driven notes, the operator upgrade contract, `URUNI_TAG` discipline — are untouched and still binding.

**Editing in place means resetting, not migrating.** goose records applied versions by number, so a database that already ran `00001` will not notice that `00001` changed. It stays on the old schema silently, which is a worse failure than the one this ADR removes. The paired mechanic is therefore mandatory, not advisory: **`make db-reset`** deletes the dev database and re-migrates from empty, and it is the normal move after pulling a schema change. `make e2e` already resets its own throwaway file every run.

**Long-lived epic branches are no longer needed to protect migrations.** M2's `epic/m2-data-model` existed for exactly that reason and is grandfathered — it finishes as planned. From M3 on, slices branch off `main` and PR into `main` like every other change. An epic branch may still be justified by something *else*, but "the migration is not finished yet" has stopped being a reason.

**`v1.0.0` freezes the file.** At the first real deployment `00001_schema.sql` becomes the permanent baseline: immutable, and every change after it a new, numbered, additive migration. That is not a new rule — it is [ADR-018](./018-release-and-versioning.md)'s "immutability begins at the first production deploy", now with a single well-formed file to begin from instead of a stack of amendments.

## Consequences

**Upgrading a `0.x` instance can require throwing the data away.** This is the cost, and it is the point: the operator upgrade contract does not apply below `v1.0.0`. Anyone running a `0.x` build is running a preview, and [`SELF-HOSTING.md`](../../SELF-HOSTING.md) says so. The pilot at `v0.6.x` is the one place this will bite — a schema change mid-pilot means re-entering whatever the treasurer recorded, or restoring through [ADR-012](./012-backup-and-export.md)'s canonical JSON once M8 exists. Worth knowing before the pilot starts, not after.

**A stale local database is now the likeliest confusing failure.** Symptoms are a `no such column` or a `CHECK` that fires on a row the schema plainly allows. The answer is always `make db-reset`; `make doctor` does not detect it, because goose genuinely believes it is up to date.

**`v1.0.0` gets a clean starting point.** A first-deploy operator applies one file that reads like a schema, instead of replaying nine milestones of the project changing its mind.
