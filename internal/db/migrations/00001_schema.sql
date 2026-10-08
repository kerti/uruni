-- The PRD section 6 data model. This is the only migration Uruni has, and the only one
-- it gets before v1.0.0 - every schema change edits this file in place, so it
-- always reads as the complete current schema (ADR-025). After pulling a change
-- to it, run `make db-reset`: goose tracks migrations by number and cannot see
-- that this one moved. fund, the location money physically sits in
-- (account), the tag every transaction carries (purpose), and who owes dues
-- (dues_tier, dues_rate, member), the ledger itself - transfer, reimbursement,
-- transaction, receipt - and the counts taken against it (reconciliation,
-- reconciliation_line) plus the incidental envelope. That is every PRD section 6
-- entity; the file is complete.
--
-- user and session (M5 Auth, ADR-030) are not PRD section 6 entities: they are the
-- treasurer's login, the only rows in this schema not scoped to a fund
-- (ADR-030 decision 2) - no fund_id, no membership table.
--
-- Conventions below follow docs/ADR/024-schema-conventions.md; see it for the
-- reasoning, not restated here.

-- +goose Up

CREATE TABLE "user" (                     -- the treasurer's login; the only table not scoped to a fund (ADR-030)
  id            INTEGER PRIMARY KEY,
  email         TEXT    NOT NULL UNIQUE CHECK (length(trim(email)) > 0),
  password_hash TEXT    NOT NULL CHECK (length(trim(password_hash)) > 0),
  created_at    INTEGER NOT NULL
) STRICT;

CREATE TABLE session (                    -- a logged-in session; token is the cookie value
  -- No fund_id (ADR-030): a session proves the treasurer, not a fund. Rows are
  -- swept lazily as a side effect of session writes (#113), never a background
  -- ticker (ADR-013's scope).
  token      TEXT    PRIMARY KEY CHECK (length(token) > 0),
  data       BLOB    NOT NULL,
  expires_at INTEGER NOT NULL
) STRICT;

CREATE INDEX session_by_expiry ON session(expires_at);

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
  -- A used-then-retired location (a bank switch, a tin no longer kept), not a
  -- never-used duplicate (that's DELETE). Mirrors member.inactive_on exactly,
  -- below - same shape, same reasoning (M6.1, #134).
  inactive_on TEXT CHECK (inactive_on IS NULL OR (date(inactive_on) IS NOT NULL AND inactive_on = date(inactive_on))),
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
-- kind='main' rows - incidental and pass_through purposes are unrestricted.
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
  -- A tier whose rate is undecided (madya, PRD section 6) simply has no row.
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
  -- NOT NULL (#471): a fund's history starts at adoption (PRD 7.1), so there
  -- is no "always was a member" - the API defaults a missing date to today
  -- in Asia/Jakarta, and backdating is the deliberate live-arrears exception.
  joined_on   TEXT    NOT NULL CHECK (date(joined_on) IS NOT NULL AND joined_on = date(joined_on)),
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
  -- Null is a roll (CloseIncidentalAndRoll); set names the row this pair
  -- corrects (ADR-033) - a treasurer fixing a mis-tagged row's peruntukan
  -- rather than hand-building the pair herself. It sits on the pair, not on
  -- either leg, because the correction is the pair. Forward FK to
  -- "transaction" below, resolved at insert time, not at CREATE TABLE - the
  -- same deferred-check shape "transaction" itself relies on for its own
  -- FOREIGN KEY (fund_id, transfer_id) back onto this table, the other way.
  corrects_transaction_id INTEGER,
  -- Why a purpose pair that corrects nothing moved (ADR-036): 'roll' is an
  -- envelope closing into or out of Kas Utama (ADR-031), 'allocation' is the
  -- treasurer moving money between purposes on purpose ("Pindah peruntukan").
  -- NULL means the row predates the column; every such uncorrected pair is a
  -- roll, because nothing else could post one, so readers treat NULL as roll.
  -- A separate column, not a third kind: kind keeps answering only what shape
  -- of move this is (ADR-033).
  reason TEXT,
  created_at INTEGER NOT NULL,
  UNIQUE (fund_id, id),                   -- see account.id above: enables composite FKs from children
  FOREIGN KEY (fund_id, corrects_transaction_id) REFERENCES "transaction"(fund_id, id),
  -- Asymmetric on purpose (ADR-033): nothing but a purpose move may claim to
  -- correct anything, but a purpose move need not. The mirror - a correction
  -- must set the link - is not enforced, which would need a third
  -- transfer.kind answering "why it moved" beside the two answering "what
  -- moved" (between_accounts, reclass_purpose); that split was refused.
  -- Unenforced, the worst a wrongly-linked roll can do is mislabel one row
  -- in Riwayat - unlike the dues-reversal CHECK below, which guards a query
  -- (DuesPaidByPeriod) and had to be absolute.
  CHECK (corrects_transaction_id IS NULL OR kind = 'reclass_purpose'),
  -- A reason belongs to a purpose pair that corrects nothing: a correction is
  -- labelled by its link, and a between_accounts pair has no reason to give.
  CHECK (reason IS NULL OR (kind = 'reclass_purpose' AND corrects_transaction_id IS NULL AND reason IN ('roll','allocation')))
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
  member_id        INTEGER,               -- dues, or a named contribution (ADR-034)
  dues_period      TEXT,                  -- 'YYYY-MM'; several months paid at once = several rows
  reimbursement_id INTEGER,               -- the settling payout
  transfer_id      INTEGER,
  reverses_transaction_id INTEGER,        -- the dues payment this adjustment reverses (ADR-029)
  note        TEXT,
  created_at  INTEGER NOT NULL,
  UNIQUE (fund_id, id),
  -- Six composite FKs: each one is what stops a transaction borrowing another
  -- fund's row, which a single-column REFERENCES would happily allow. The
  -- reverses_transaction_id one is what stops a treasurer of one fund naming
  -- another fund's row as the payment they are reversing (ADR-029).
  FOREIGN KEY (fund_id, account_id)       REFERENCES account(fund_id, id),
  FOREIGN KEY (fund_id, purpose_id)       REFERENCES purpose(fund_id, id),
  FOREIGN KEY (fund_id, member_id)        REFERENCES member(fund_id, id),
  FOREIGN KEY (fund_id, reimbursement_id) REFERENCES reimbursement(fund_id, id),
  FOREIGN KEY (fund_id, transfer_id)      REFERENCES transfer(fund_id, id),
  FOREIGN KEY (fund_id, reverses_transaction_id) REFERENCES "transaction"(fund_id, id),
  CHECK (dues_period IS NULL OR (dues_period GLOB '[0-9][0-9][0-9][0-9]-[0-1][0-9]'
                                 AND date(dues_period||'-01') IS NOT NULL)),
  CHECK (kind <> 'dues'          OR (member_id IS NOT NULL AND dues_period IS NOT NULL AND direction = 'in')),
  -- A dues reversal (ADR-029) and a named contribution (ADR-034, widening
  -- ADR-029's reversal) are the two shapes of a non-dues row allowed to
  -- carry a member: a dues reversal is the one shape of kind='adjustment'
  -- allowed to carry one, and a contribution is the one shape of an
  -- ordinary kind='normal' row allowed to - CHECK alone cannot see
  -- purpose.kind, so which purpose a contribution's member may be tagged
  -- to is the BEFORE INSERT trigger's job below, not this CHECK's. Neither
  -- shape carries dues_period: a dues reversal's own period is verified
  -- against the payment it reverses by that same trigger, since a CHECK
  -- cannot read another row either; every other kind still needs neither
  -- field.
  CHECK (kind =  'dues'          OR (member_id IS NULL AND dues_period IS NULL)
                                 OR (kind = 'normal' AND direction = 'in'
                                     AND reverses_transaction_id IS NULL AND dues_period IS NULL)
                                 OR (kind = 'adjustment' AND reverses_transaction_id IS NOT NULL
                                     AND member_id IS NOT NULL AND direction = 'out')),
  -- ...and the column guards itself in the other direction: nothing that is
  -- not a reversal may claim to reverse anything. Without this, a
  -- kind='normal' row carrying no member and no period satisfies the CHECK
  -- above and still sets reverses_transaction_id - which would both hide the
  -- original dues payment from DuesPaidByPeriod (the NOT EXISTS only asks
  -- whether *something* points at the row) and consume the reversed-once
  -- slot, so the genuine reversal could never be posted. The ledger never
  -- writes that row; the schema is what makes it unrepresentable (ADR-029).
  -- dues_period is deliberately not required here any more (ADR-034): a
  -- reversal of a named contribution carries none, a reversal of a dues
  -- payment still must - and that difference is exactly what the trigger
  -- below checks against the row being reversed, which this own-row CHECK
  -- cannot read.
  CHECK (reverses_transaction_id IS NULL
         OR (kind = 'adjustment' AND direction = 'out'
             AND member_id IS NOT NULL)),
  CHECK (kind <> 'reimbursement' OR (reimbursement_id IS NOT NULL AND direction = 'out')),
  CHECK (kind <> 'transfer'      OR transfer_id IS NOT NULL)
  -- kind='adjustment' otherwise requires nothing extra: an ordinary
  -- correction may be raised on any Tuesday, not only during a
  -- reconciliation (ADR-024).
) STRICT;

-- A CHECK reads only its own row, so the two cross-table halves of ADR-034's
-- named-contribution design - which purpose a member may be tagged to, and
-- what a reversal may target and must copy - are a BEFORE INSERT trigger
-- instead. The ledger checks first so the treasurer gets copy, not a
-- constraint error (ADR-034); this is what makes both shapes unrepresentable
-- for anything that writes around the ledger.
-- +goose StatementBegin
CREATE TRIGGER transaction_named_row_shape BEFORE INSERT ON "transaction" BEGIN
  -- A kind='normal' row naming a member is a contribution (ADR-034) and may
  -- only be tagged to an incidental purpose - a dues payment already owns
  -- member_id on a 'main' purpose, and nothing else has a reason to.
  SELECT RAISE(ABORT, 'a named transaction must be tagged to an incidental purpose')
  WHERE NEW.kind = 'normal' AND NEW.member_id IS NOT NULL
    AND NOT EXISTS (SELECT 1 FROM purpose WHERE id = NEW.purpose_id AND kind = 'incidental');

  -- A reversal (ADR-029, widened by ADR-034) must target a dues payment or
  -- a named contribution, and must carry that row's own member_id and
  -- dues_period exactly - never re-typed by the caller. This is what keeps
  -- a dues reversal carrying its period and a contribution reversal
  -- carrying none: the CHECK above cannot read the original row, only this
  -- trigger can. "o.dues_period IS NEW.dues_period" is NULL-safe equality -
  -- a contribution's own dues_period is always NULL, so the reversal's must
  -- be too.
  SELECT RAISE(ABORT, 'a reversal must target a dues payment or a named contribution, carrying its member and period')
  WHERE NEW.reverses_transaction_id IS NOT NULL
    AND NOT EXISTS (
      SELECT 1 FROM "transaction" o
      WHERE o.id = NEW.reverses_transaction_id
        AND o.member_id = NEW.member_id AND o.dues_period IS NEW.dues_period
        AND (o.kind = 'dues' OR (o.kind = 'normal' AND o.member_id IS NOT NULL))
    );
END;
-- +goose StatementEnd

-- "Settle once" was otherwise only prose. Partial, so the NULLs on every other
-- kind are unconstrained.
CREATE UNIQUE INDEX reimbursement_settled_once ON "transaction"(reimbursement_id) WHERE kind = 'reimbursement';

-- A dues payment is reversible at most once, the same partial-unique shape as
-- reimbursement_settled_once above (ADR-029). Partial, so every row that
-- carries no reversal - which is everything except a dues reversal itself -
-- stays unconstrained.
CREATE UNIQUE INDEX dues_payment_reversed_once ON "transaction"(reverses_transaction_id) WHERE reverses_transaction_id IS NOT NULL;

-- One opening entry per account, enforced the same way and for a sharper
-- reason: nothing else in the schema stops a second opening row, and a second
-- one silently doubles the money the fund starts with. Partial, so every
-- non-opening kind - and every account that has no opening row at all - stays
-- unconstrained.
CREATE UNIQUE INDEX opening_balance_once_per_account ON "transaction"(account_id) WHERE kind = 'opening';

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
  -- One figure every expected member is asked to give (ADR-034) - same shape
  -- and nullability as target_amount, not a dues rate: no tiers, no
  -- effective dates, editable like occasion, never a posted fact.
  minimum_per_member INTEGER CHECK (minimum_per_member IS NULL OR minimum_per_member > 0),
  created_at    INTEGER NOT NULL
) STRICT;

-- The members an envelope is *for* (ADR-034): the sick, the bereaved family,
-- the birthday pair - never expected to contribute themselves. Zero or more,
-- always members, editable like occasion - a choice, not a record, which is
-- what ON DELETE CASCADE from member says below.
--
-- fund_id is not a column incidental itself carries (it is 1:1 with a
-- purpose row and has none), so this table's own composite FKs read fund
-- ownership through purpose - (fund_id, purpose_id) REFERENCES
-- purpose(fund_id, id), ADR-024's usual fund-scoping shape - and the plain
-- purpose_id REFERENCES incidental(purpose_id) is what additionally proves
-- the purpose named is actually an envelope, not main or pass_through.
CREATE TABLE incidental_recipient (
  fund_id    INTEGER NOT NULL,
  purpose_id INTEGER NOT NULL,
  member_id  INTEGER NOT NULL,
  PRIMARY KEY (fund_id, purpose_id, member_id),
  FOREIGN KEY (purpose_id)          REFERENCES incidental(purpose_id),
  FOREIGN KEY (fund_id, purpose_id) REFERENCES purpose(fund_id, id),
  -- A recipient is a choice, not a record: deleting a member (setup
  -- duplicates only, ADR-024) takes their recipient rows with them rather
  -- than refusing the delete the way a posted transaction would.
  FOREIGN KEY (fund_id, member_id)  REFERENCES member(fund_id, id) ON DELETE CASCADE
) STRICT;

-- fund_id, occurred_on, id, in that order: the leftmost pair alone still
-- covers every fund_id+occurred_on-range query this index served before
-- (#225 added the third column, nothing removed it), and with id the index
-- fully orders GET /api/transactions's own (occurred_on DESC, id DESC)
-- keyset scan instead of leaving SQLite to sort after the fact.
CREATE INDEX transaction_by_date    ON "transaction"(fund_id, occurred_on, id);
CREATE INDEX transaction_by_account ON "transaction"(account_id, occurred_on);
CREATE INDEX transaction_by_purpose ON "transaction"(purpose_id);
CREATE INDEX transaction_by_dues    ON "transaction"(member_id, dues_period) WHERE kind = 'dues';

-- Same shape as transaction_by_date above (#226): fully orders GET
-- /api/reimbursements's own (incurred_on DESC, id DESC) keyset scan.
CREATE INDEX reimbursement_by_date  ON reimbursement(fund_id, incurred_on, id);

-- Same shape again (#227): fully orders GET /api/reconciliations's own
-- (performed_at DESC, id DESC) keyset scan.
CREATE INDEX reconciliation_by_date ON reconciliation(fund_id, performed_at, id);

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
DROP INDEX reconciliation_by_date;
DROP INDEX reimbursement_by_date;
DROP INDEX transaction_by_dues;
DROP INDEX transaction_by_purpose;
DROP INDEX transaction_by_account;
DROP INDEX transaction_by_date;
DROP TABLE incidental_recipient;
DROP TABLE incidental;
DROP TABLE reconciliation_line;
DROP TABLE reconciliation;
DROP TABLE receipt;
DROP INDEX opening_balance_once_per_account;
DROP INDEX dues_payment_reversed_once;
DROP INDEX reimbursement_settled_once;
DROP TRIGGER transaction_named_row_shape;
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
DROP INDEX session_by_expiry;
DROP TABLE session;
DROP TABLE "user";
