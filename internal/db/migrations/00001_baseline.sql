-- M1.3 stands the database path up end to end — open, migrate, query — with no
-- domain tables at all. The PRD §6 entities (Fund, Account, Member,
-- Transaction, …) land at M2, so this migration has nothing to create.
--
-- It exists because goose needs at least one migration to have a version to
-- apply and roll back — an empty migrations filesystem is an error, not an empty
-- set — and rolling back is what proves the pipeline runs in both directions.
-- Applying it creates goose_db_version and stamps version 1: the baseline every
-- later migration builds on.

-- +goose Up
SELECT 'no-op — the PRD §6 schema lands at M2';

-- +goose Down
SELECT 'no-op';
