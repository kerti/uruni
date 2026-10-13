<!--
Label this PR when you open it with exactly one of: enhancement / bug /
documentation / dependencies, and assign its milestone. The auto-generated
release notes are built from those labels, so an unlabeled PR disappears from
the changelog (.github/release.yml, ADR-018).
-->

Closes #

## What changed

## Why

## Checklist

- [ ] `make check` is green (mirrors CI step for step)
- [ ] Tests cover the change — **money and reconciliation logic is the trust core and must be covered**
- [ ] No scope creep: no new screen, setting, or concept that `docs/PRD.md` doesn't require
- [ ] User-facing copy is Bahasa Indonesia, sentence case, warm (`docs/Design-System.md`)
- [ ] Uses the vocabulary in `CONTEXT.md` — no new synonyms for existing concepts
- [ ] Amounts are `int64` integer rupiah end to end; formatting only at the display edge
- [ ] No real names, real figures, or absolute local paths in the diff
- [ ] A schema change edits `internal/db/migrations/00001_schema.sql` in place and adds no second migration file (ADR-025); `Decisions.md` updated if anything was actually decided
- [ ] Docs that describe this behaviour still say true things
