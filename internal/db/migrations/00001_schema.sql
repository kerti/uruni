-- The PRD §6 data model, built up one M2 slice at a time in a single migration
-- file — nothing is deployed yet, so the schema stays editable until the epic
-- reaches main (#21). So far: fund, the location money physically sits in
-- (account), the tag every transaction carries (purpose), and who owes dues
-- (dues_tier, dues_rate, member), the ledger itself — transfer, reimbursement,
-- transaction, receipt — and the counts taken against it (reconciliation,
-- reconciliation_line) plus the incidental envelope. That is every PRD §6
-- entity; the file is complete.
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

CREATE TABLE transfer (                   -- pair-holder: cash<->bank, or purpose reclass
  -- Value-neutral movements are two transactions of equal amount and opposite
  -- direction, bound by one of these rows (ADR-024). A single row would change
  -- the fund's total; nothing moved, so nothing may.
  id         INTEGER PRIMARY KEY,
  fund_id    INTEGER NOT NULL REFERENCES fund(id),
  kind       TEXT    NOT NULL CHECK (kind IN ('between_accounts','reclass_purpose')),
  created_at INTEGER NOT NULL,
  UNIQUE (fund_id, id)                    -- see account.id above: enables composite FKs from children
) STRICT;

CREATE TABLE reimbursement (              -- off-ledger until settled
  -- A member fronting their own money does not move the kas, so there is no
  -- ledger row until the payout. Settling posts one real 'out'; waived_on
  -- closes a claim that will never be repaid (ADR-024).
  id          INTEGER PRIMARY KEY,
  fund_id     INTEGER NOT NULL,
  member_id   INTEGER NOT NULL,
  purpose_id  INTEGER NOT NULL,
  amount      INTEGER NOT NULL CHECK (amount > 0),
  incurred_on TEXT    NOT NULL CHECK (date(incurred_on) IS NOT NULL AND incurred_on = date(incurred_on)),
  waived_on   TEXT             CHECK (waived_on IS NULL OR (date(waived_on) IS NOT NULL AND waived_on = date(waived_on))),
  note        TEXT,
  created_at  INTEGER NOT NULL,
  UNIQUE (fund_id, id),
  FOREIGN KEY (fund_id) REFERENCES fund(id),
  FOREIGN KEY (fund_id, member_id)  REFERENCES member(fund_id, id),
  FOREIGN KEY (fund_id, purpose_id) REFERENCES purpose(fund_id, id)
) STRICT;

CREATE TABLE "transaction" (              -- the ledger. insert-only.
  id          INTEGER PRIMARY KEY,
  fund_id     INTEGER NOT NULL,
  account_id  INTEGER NOT NULL,
  purpose_id  INTEGER NOT NULL,
  direction   TEXT    NOT NULL CHECK (direction IN ('in','out')),
  -- The sign lives in direction, never in the amount, so summing the ledger is
  -- one CASE and a negative amount is impossible rather than merely unexpected.
  amount      INTEGER NOT NULL CHECK (amount > 0),
  occurred_on TEXT    NOT NULL CHECK (date(occurred_on) IS NOT NULL AND occurred_on = date(occurred_on)),
  kind        TEXT    NOT NULL CHECK (kind IN
                ('opening','normal','dues','reimbursement','adjustment','transfer')),
  member_id        INTEGER,               -- dues
  dues_period      TEXT,                  -- 'YYYY-MM'; several months paid at once = several rows
  reimbursement_id INTEGER,               -- the settling payout
  transfer_id      INTEGER,
  note        TEXT,
  created_at  INTEGER NOT NULL,
  UNIQUE (fund_id, id),
  -- Five composite FKs: each one is what stops a transaction borrowing another
  -- fund's row, which a single-column REFERENCES would happily allow.
  FOREIGN KEY (fund_id, account_id)       REFERENCES account(fund_id, id),
  FOREIGN KEY (fund_id, purpose_id)       REFERENCES purpose(fund_id, id),
  FOREIGN KEY (fund_id, member_id)        REFERENCES member(fund_id, id),
  FOREIGN KEY (fund_id, reimbursement_id) REFERENCES reimbursement(fund_id, id),
  FOREIGN KEY (fund_id, transfer_id)      REFERENCES transfer(fund_id, id),
  CHECK (dues_period IS NULL OR (dues_period GLOB '[0-9][0-9][0-9][0-9]-[0-1][0-9]'
                                 AND date(dues_period||'-01') IS NOT NULL)),
  CHECK (kind <> 'dues'          OR (member_id IS NOT NULL AND dues_period IS NOT NULL AND direction = 'in')),
  CHECK (kind =  'dues'          OR (member_id IS NULL AND dues_period IS NULL)),
  CHECK (kind <> 'reimbursement' OR (reimbursement_id IS NOT NULL AND direction = 'out')),
  CHECK (kind <> 'transfer'      OR transfer_id IS NOT NULL)
  -- kind='adjustment' deliberately requires nothing extra: a correction may be
  -- raised on any Tuesday, not only during a reconciliation (ADR-024).
) STRICT;

-- "Settle once" was otherwise only prose. Partial, so the NULLs on every other
-- kind are unconstrained.
CREATE UNIQUE INDEX reimbursement_settled_once ON "transaction"(reimbursement_id) WHERE kind = 'reimbursement';

CREATE TABLE receipt (                    -- attachment, not a ledger fact; addable after the fact
  -- Its own table precisely because ledger rows are immutable: a photo taken
  -- after the entry was posted still has somewhere to go (ADR-011).
  id               INTEGER PRIMARY KEY,
  fund_id          INTEGER NOT NULL,
  transaction_id   INTEGER,
  reimbursement_id INTEGER,
  path             TEXT    NOT NULL CHECK (length(trim(path)) > 0),
  uploaded_at      INTEGER NOT NULL,
  CHECK ((transaction_id IS NULL) <> (reimbursement_id IS NULL)),   -- exactly one parent
  FOREIGN KEY (fund_id, transaction_id)   REFERENCES "transaction"(fund_id, id),
  FOREIGN KEY (fund_id, reimbursement_id) REFERENCES reimbursement(fund_id, id)
) STRICT;

CREATE TABLE reconciliation (             -- a count of the real money, frozen
  -- The one place a total is stored rather than derived (CLAUDE.md rule 2), and
  -- only because it is a historical claim: "on this day the recorded figure was
  -- this". through_transaction_id is the ledger cutoff that makes the claim
  -- reproducible - a backdated entry posted afterwards gets a higher id, so it
  -- lands in today's balance without silently rewriting last month's snapshot.
  id           INTEGER PRIMARY KEY,
  fund_id      INTEGER NOT NULL,
  performed_at INTEGER NOT NULL,
  through_transaction_id INTEGER,
  note         TEXT,
  created_at   INTEGER NOT NULL,
  UNIQUE (fund_id, id),                   -- see account.id above: enables composite FKs from children
  FOREIGN KEY (fund_id) REFERENCES fund(id),
  FOREIGN KEY (fund_id, through_transaction_id) REFERENCES "transaction"(fund_id, id)
) STRICT;

CREATE TABLE reconciliation_line (        -- one counted location within a snapshot
  id                INTEGER PRIMARY KEY,
  fund_id           INTEGER NOT NULL,
  reconciliation_id INTEGER NOT NULL,
  account_id        INTEGER NOT NULL,
  recorded_amount   INTEGER NOT NULL,     -- frozen at snapshot time
  actual_amount     INTEGER NOT NULL,     -- what the treasurer counted
  difference_amount INTEGER NOT NULL,
  resolution        TEXT    NOT NULL CHECK (resolution IN ('matched','entry_added','adjusted','left_open')),
  adjustment_transaction_id INTEGER,      -- the entry that squared this line
  UNIQUE (reconciliation_id, account_id), -- a location is counted once per snapshot
  FOREIGN KEY (fund_id, reconciliation_id)         REFERENCES reconciliation(fund_id, id),
  FOREIGN KEY (fund_id, account_id)                REFERENCES account(fund_id, id),
  FOREIGN KEY (fund_id, adjustment_transaction_id) REFERENCES "transaction"(fund_id, id),
  -- Stored because the report reads it, and checked because a stored derived
  -- figure that disagrees with its inputs is worse than no figure at all.
  CHECK (difference_amount = actual_amount - recorded_amount),
  -- "This line was adjusted" is the claim capable of being a lie (ADR-024), so
  -- it must name the entry. The other three resolutions name nothing.
  CHECK (resolution <> 'adjusted' OR adjustment_transaction_id IS NOT NULL)
) STRICT;

CREATE TABLE incidental (                 -- the envelope's lifecycle, 1:1 with its purpose row
  -- Not a column on purpose: only kind='incidental' rows have an occasion, a
  -- target or a closing date, and a table keeps those out of every other tag.
  -- The money itself is ordinary transactions carrying purpose_id; closing an
  -- envelope moves nothing, which is why closed_on lives here and not in the
  -- ledger. Mutable by design - opening a collection is a decision that gets
  -- revised, and nothing here is a posted fact.
  purpose_id    INTEGER PRIMARY KEY REFERENCES purpose(id),
  occasion      TEXT    NOT NULL CHECK (length(trim(occasion)) > 0),
  target_amount INTEGER CHECK (target_amount IS NULL OR target_amount > 0),
  opened_on     TEXT    NOT NULL CHECK (date(opened_on) IS NOT NULL AND opened_on = date(opened_on)),
  closed_on     TEXT             CHECK (closed_on IS NULL OR (date(closed_on) IS NOT NULL AND closed_on = date(closed_on))),
  created_at    INTEGER NOT NULL
) STRICT;

CREATE INDEX transaction_by_date    ON "transaction"(fund_id, occurred_on);
CREATE INDEX transaction_by_account ON "transaction"(account_id, occurred_on);
CREATE INDEX transaction_by_purpose ON "transaction"(purpose_id);
CREATE INDEX transaction_by_dues    ON "transaction"(member_id, dues_period) WHERE kind = 'dues';

-- Immutability is a trigger, not a convention (ADR-024, CLAUDE.md rule 3).
-- INSERT stays open, which is what lets ADR-012's import restore a database.
-- A snapshot is a claim about a moment, so it is as immutable as the ledger:
-- revisiting a left_open difference means a second snapshot, not an edit to the
-- first. incidental is deliberately absent - see its comment above.

-- +goose StatementBegin
CREATE TRIGGER transaction_immutable_update BEFORE UPDATE ON "transaction" BEGIN
  SELECT RAISE(ABORT, 'transaction rows are immutable - post an adjusting entry');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER transaction_immutable_delete BEFORE DELETE ON "transaction" BEGIN
  SELECT RAISE(ABORT, 'transaction rows are immutable - post an adjusting entry');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER transfer_immutable_update BEFORE UPDATE ON transfer BEGIN
  SELECT RAISE(ABORT, 'transfer rows are immutable - post an adjusting entry');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER transfer_immutable_delete BEFORE DELETE ON transfer BEGIN
  SELECT RAISE(ABORT, 'transfer rows are immutable - post an adjusting entry');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER reconciliation_immutable_update BEFORE UPDATE ON reconciliation BEGIN
  SELECT RAISE(ABORT, 'reconciliation rows are immutable - take a new snapshot');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER reconciliation_immutable_delete BEFORE DELETE ON reconciliation BEGIN
  SELECT RAISE(ABORT, 'reconciliation rows are immutable - take a new snapshot');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER reconciliation_line_immutable_update BEFORE UPDATE ON reconciliation_line BEGIN
  SELECT RAISE(ABORT, 'reconciliation_line rows are immutable - take a new snapshot');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER reconciliation_line_immutable_delete BEFORE DELETE ON reconciliation_line BEGIN
  SELECT RAISE(ABORT, 'reconciliation_line rows are immutable - take a new snapshot');
END;
-- +goose StatementEnd

-- +goose Down

DROP TRIGGER reconciliation_line_immutable_delete;
DROP TRIGGER reconciliation_line_immutable_update;
DROP TRIGGER reconciliation_immutable_delete;
DROP TRIGGER reconciliation_immutable_update;
DROP TRIGGER transfer_immutable_delete;
DROP TRIGGER transfer_immutable_update;
DROP TRIGGER transaction_immutable_delete;
DROP TRIGGER transaction_immutable_update;
DROP INDEX transaction_by_dues;
DROP INDEX transaction_by_purpose;
DROP INDEX transaction_by_account;
DROP INDEX transaction_by_date;
DROP TABLE incidental;
DROP TABLE reconciliation_line;
DROP TABLE reconciliation;
DROP TABLE receipt;
DROP INDEX reimbursement_settled_once;
DROP TABLE "transaction";
DROP TABLE reimbursement;
DROP TABLE transfer;
DROP TABLE member;
DROP TABLE dues_rate;
DROP TABLE dues_tier;
DROP INDEX purpose_single_main;
DROP TABLE purpose;
DROP TABLE account;
DROP TABLE fund;
