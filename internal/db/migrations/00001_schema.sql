-- The PRD §6 data model, built up one M2 slice at a time in a single migration
-- file — nothing is deployed yet, so the schema stays editable until the epic
-- reaches main (#21). So far: fund, the location money physically sits in
-- (account), the tag every transaction carries (purpose), and who owes dues
-- (dues_tier, dues_rate, member). The ledger itself — transaction, transfer,
-- reimbursement, receipt — and the snapshots land in #24 and #25.
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

CREATE TABLE dues_tier (                  -- a table, not an enum: the treasurer names these
  id         INTEGER PRIMARY KEY,
  fund_id    INTEGER NOT NULL REFERENCES fund(id),
  name       TEXT    NOT NULL CHECK (length(trim(name)) > 0),
  created_at INTEGER NOT NULL,
  UNIQUE (fund_id, id),                   -- see account.id above: enables composite FKs from children
  UNIQUE (fund_id, name)
) STRICT;

CREATE TABLE dues_rate (                  -- effective-dated, one-sided intervals
  -- No effective_to and no fund_id: the rate for a period is the latest row at
  -- or before it, and ownership comes through tier_id. Two-sided intervals
  -- would need gaps and overlaps policed; this shape cannot express either.
  -- A tier whose rate is undecided (madya, PRD §6) simply has no row.
  id             INTEGER PRIMARY KEY,
  tier_id        INTEGER NOT NULL REFERENCES dues_tier(id),
  amount         INTEGER NOT NULL CHECK (amount >= 0),
  effective_from TEXT    NOT NULL CHECK (effective_from GLOB '[0-9][0-9][0-9][0-9]-[0-1][0-9]'
                                         AND date(effective_from||'-01') IS NOT NULL),
  created_at     INTEGER NOT NULL,
  UNIQUE (tier_id, effective_from)
) STRICT;

CREATE TABLE member (
  id          INTEGER PRIMARY KEY,
  fund_id     INTEGER NOT NULL,
  name        TEXT    NOT NULL CHECK (length(trim(name)) > 0),
  tier_id     INTEGER,                    -- NULL = no dues obligation
  joined_on   TEXT             CHECK (joined_on IS NULL OR (date(joined_on) IS NOT NULL AND joined_on = date(joined_on))),
  inactive_on TEXT             CHECK (inactive_on IS NULL OR (date(inactive_on) IS NOT NULL AND inactive_on = date(inactive_on))),
  created_at  INTEGER NOT NULL,
  UNIQUE (fund_id, id),
  FOREIGN KEY (fund_id) REFERENCES fund(id),
  -- Composite, so a member cannot borrow another fund's tier. NULL tier_id
  -- satisfies it either way: SQLite's MATCH SIMPLE skips a partly-NULL key.
  FOREIGN KEY (fund_id, tier_id) REFERENCES dues_tier(fund_id, id)
) STRICT;

-- +goose Down

DROP TABLE member;
DROP TABLE dues_rate;
DROP TABLE dues_tier;
DROP INDEX purpose_single_main;
DROP TABLE purpose;
DROP TABLE account;
DROP TABLE fund;
