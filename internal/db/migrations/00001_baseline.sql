-- M1.3 stands the database path up end to end — open, migrate, query — with no
-- domain tables at all. The PRD §6 entities (Fund, Account, Member,
-- Transaction, …) land at M2, so this migration has nothing to create.
--
-- It exists because goose needs at least one migration to have a version to
-- apply and roll back — an empty migrations filesystem is an error, not an empty
-- set — and rolling back is what proves the pipeline runs in both directions.
-- Applying it creates goose_db_version and stamps version 1.
--
-- DELETE THIS FILE AT M2. It is scaffolding, not history: the moment the real
-- domain entities land, M2's schema becomes 00001 and this no-op goes away
-- rather than sitting at the bottom of the ledger forever. That is safe because
-- nothing is deployed yet — migrations only become immutable at the first live
-- treasurer's instance (docs/ROADMAP.md). Whoever does it: drop your local
-- database file (or `make migrate-down` first), because goose will otherwise
-- find version 1 applied with no migration on disk to match it.

-- +goose Up
SELECT 'no-op — the PRD §6 schema lands at M2';

-- +goose Down
SELECT 'no-op';
