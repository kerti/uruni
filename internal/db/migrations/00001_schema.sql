-- M2.1 lays down the first three tables of the PRD §6 data model: fund, the
-- location money physically sits in (account), and the tag every transaction
-- carries (purpose). The remaining M2 tables — member, dues_tier, transaction,
-- reconciliation, incidental, and the rest — land in later slices (#23, #24,
-- #25) and are deliberately absent here.
--
-- Conventions below follow docs/ADR/024-schema-conventions.md; see it for the
-- reasoning, not restated here.

-- +goose Up

CREATE TABLE fund (
  id          INTEGER PRIMARY KEY,
  name        TEXT    NOT NULL CHECK (length(trim(name)) > 0),
  currency    TEXT    NOT NULL DEFAULT 'IDR' CHECK (currency = 'IDR'),
  -- Unguessability for the public report link lives here, not in an id scheme
  -- (ADR-024): 22+ chars is roughly a UUID's worth of entropy in base62.
  report_slug TEXT    NOT NULL UNIQUE CHECK (length(report_slug) >= 22),
  created_at  INTEGER NOT NULL
) STRICT; -- rejects "1000.50" landing in an INTEGER column; see ADR-024, ADR-006

CREATE TABLE account (                    -- location: where money physically sits
  id         INTEGER PRIMARY KEY,
  fund_id    INTEGER NOT NULL REFERENCES fund(id),
  kind       TEXT    NOT NULL CHECK (kind IN ('cash','bank')),
  name       TEXT    NOT NULL CHECK (length(trim(name)) > 0),
  created_at INTEGER NOT NULL,
  -- Not a second key: (fund_id, id) is what a later fund-scoped child (e.g. a
  -- transaction) references instead of id alone, so SQLite itself rejects a
  -- row whose parent belongs to another fund (ADR-024's composite-FK rule).
  UNIQUE (fund_id, id)
) STRICT;

CREATE TABLE purpose (                    -- the tag every transaction carries
  id         INTEGER PRIMARY KEY,
  fund_id    INTEGER NOT NULL REFERENCES fund(id),
  kind       TEXT    NOT NULL CHECK (kind IN ('main','incidental','pass_through')),
  name       TEXT    NOT NULL CHECK (length(trim(name)) > 0),
  created_at INTEGER NOT NULL,
  UNIQUE (fund_id, id) -- see account.id above: enables composite FKs from children
) STRICT;

-- One routine purpose ("Kas Utama") per fund. Partial so it only constrains
-- kind='main' rows — incidental and pass_through purposes are unrestricted.
CREATE UNIQUE INDEX purpose_single_main ON purpose(fund_id) WHERE kind = 'main';

-- +goose Down

DROP INDEX purpose_single_main;
DROP TABLE purpose;
DROP TABLE account;
DROP TABLE fund;
