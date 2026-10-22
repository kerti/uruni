-- The PRD section 6 data model, and the only migration Uruni gets before v1.0.0:
-- every schema change edits this file in place, so it always reads as the
-- complete current schema (ADR-025). After pulling a change, run `make db-reset`:
-- goose tracks migrations by number and cannot see that this one moved.
--
-- user and session (ADR-030) are not PRD section 6 entities: they are the
-- treasurer's login, the only rows not scoped to a fund (no fund_id, no
-- membership table).
--
-- Conventions follow docs/ADR/024-schema-conventions.md.
--
-- Every money column carries the ceiling 999999999999 (money.MaxAmount): the
-- backstop for anything that writes around the ledger, a restore included. A
-- signed column is bounded on both sides; reconciliation_line's two derived
-- figures are the exceptions. A CHECK cannot name a Go constant, so
-- internal/db/amount_ceiling_test.go pins the literal and fails on a money
-- column that lacks it.
--
-- Every business date and period carries the floor 2000-01-01 (2000-01 for a
-- month): the only half of the plausible window a restore enforces. The ceilings
-- move with the clock and the fund's zone, so they are the ledger's, on write.
-- The literals mirror internal/ledger/date_window.go; internal/db/date_floor_test.go
-- pins them.

-- +goose Up

CREATE TABLE "user" (                     -- the treasurer's login; the only table not scoped to a fund (ADR-030)
  id            INTEGER PRIMARY KEY,
  -- NOCASE: the UNIQUE index and every `email = ?` compare case-insensitively.
  -- It folds ASCII letters only (a non-ASCII capital is a different character),
  -- and stored bytes are never rewritten. Go still trims and lower-cases on the way in.
  email         TEXT    NOT NULL COLLATE NOCASE UNIQUE CHECK (length(trim(email)) > 0),
  password_hash TEXT    NOT NULL CHECK (length(trim(password_hash)) > 0),
  created_at    INTEGER NOT NULL
) STRICT;

CREATE TABLE session (                    -- a logged-in session; token is the cookie value
  -- No fund_id (ADR-030): a session proves the treasurer, not a fund. Rows are
  -- swept lazily on session writes (#113), never by a background ticker (ADR-013).
  token      TEXT    PRIMARY KEY CHECK (length(token) > 0),
  data       BLOB    NOT NULL,
  expires_at INTEGER NOT NULL
) STRICT;

CREATE INDEX session_by_expiry ON session(expires_at);

-- A write the phone may repeat after a lost response (ADR-043). Request-scoped
-- like session: outside the backup, cleared by a restore, no fund_id, foreign key
-- or immutability trigger. result is what the ledger method returned, as JSON,
-- never the HTTP reply. Expired rows are purged by the next keyed write, never
-- by a ticker (ADR-013).
CREATE TABLE idempotency_key (
  key         TEXT    PRIMARY KEY CHECK (length(key) BETWEEN 16 AND 64 AND key NOT GLOB '*[^A-Za-z0-9_-]*'),
  fingerprint TEXT    NOT NULL CHECK (length(fingerprint) = 64),
  result      BLOB    NOT NULL,
  created_at  INTEGER NOT NULL,            -- UTC unix seconds (ADR-024)
  expires_at  INTEGER NOT NULL CHECK (expires_at > created_at)
) STRICT;

CREATE INDEX idempotency_key_by_expiry ON idempotency_key(expires_at);

CREATE TABLE fund (
  id          INTEGER PRIMARY KEY,
  name        TEXT    NOT NULL CHECK (length(trim(name)) > 0),
  currency    TEXT    NOT NULL DEFAULT 'IDR' CHECK (currency = 'IDR'),
  -- ADR-041: the one place an instant becomes a day or a month. IANA names (Go and
  -- Intl take them as is); WIB, WITA and WIT exist only as copy. Pinned to
  -- internal/tz and web/src/lib/timeZones.ts by TestTimeZoneListsAgree.
  time_zone   TEXT    NOT NULL DEFAULT 'Asia/Jakarta'
              CHECK (time_zone IN ('Asia/Jakarta', 'Asia/Makassar', 'Asia/Jayapura')),
  -- Unguessability of the public report link lives here, not in an id scheme
  -- (ADR-024): 22+ base62 chars is roughly a UUID's worth of entropy.
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
  -- never-used duplicate (that's DELETE). Same shape as member.inactive_on.
  inactive_on TEXT CHECK (inactive_on IS NULL OR (date(inactive_on) IS NOT NULL AND inactive_on = date(inactive_on) AND inactive_on >= '2000-01-01')),
  -- Children reference (fund_id, id) instead of id alone, so SQLite itself
  -- rejects a row whose parent belongs to another fund (ADR-024 composite FKs).
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
  -- A tier with an undecided rate (madya, PRD section 6) simply has no row.
  --
  -- ADR-040: a mistake is corrected by a new row, never an overwrite. The highest
  -- correction of a month stands (dues_rate_standing). A deleting correction
  -- (deleted = 1) copies the amount it withdraws; only a correction can delete.
  -- The UPDATE trigger at the foot of this file holds that.
  id             INTEGER PRIMARY KEY,
  tier_id        INTEGER NOT NULL REFERENCES dues_tier(id),
  amount         INTEGER NOT NULL CHECK (amount >= 0 AND amount <= 999999999999),
  effective_from TEXT    NOT NULL CHECK (effective_from GLOB '[0-9][0-9][0-9][0-9]-[0-1][0-9]'
                                         AND date(effective_from||'-01') IS NOT NULL
                                         AND effective_from >= '2000-01'),
  correction     INTEGER NOT NULL DEFAULT 0 CHECK (correction >= 0),
  deleted        INTEGER NOT NULL DEFAULT 0 CHECK (deleted IN (0, 1)),
  created_at     INTEGER NOT NULL,
  CHECK (deleted = 0 OR correction > 0),
  UNIQUE (tier_id, effective_from, correction)
) STRICT;

-- The rate in force per month: the highest correction, and none when that row is
-- a deletion. Dropping deleted rows BEFORE taking the highest would be wrong (Jan
-- 50.000, Jun 70.000 then deleted, read at June: 70.000 instead of 50.000). Every
-- dues read goes through this view (internal/db's guard test fails a query that
-- does not). Named exceptions: the backup export, the by-id lookup PATCH and
-- DELETE start from, the next-correction read.
CREATE VIEW dues_rate_standing AS
SELECT dr.id, dr.tier_id, dr.amount, dr.effective_from, dr.correction, dr.created_at
FROM dues_rate dr
WHERE dr.deleted = 0
  AND dr.correction = (
    SELECT MAX(c.correction) FROM dues_rate c
    WHERE c.tier_id = dr.tier_id AND c.effective_from = dr.effective_from
  );

CREATE TABLE member (
  -- No tier column (ADR-040): a member's tier is effective-dated, so it lives in
  -- member_tier below and the current tier is derived from those rows.
  id          INTEGER PRIMARY KEY,
  fund_id     INTEGER NOT NULL,
  name        TEXT    NOT NULL CHECK (length(trim(name)) > 0),
  -- NOT NULL (#471): a fund's history starts at adoption (PRD 7.1), so there
  -- is no "always was a member". The API defaults a missing date to today in
  -- the fund's time zone; backdating is the deliberate live-arrears exception.
  joined_on   TEXT    NOT NULL CHECK (date(joined_on) IS NOT NULL AND joined_on = date(joined_on) AND joined_on >= '2000-01-01'),
  inactive_on TEXT             CHECK (inactive_on IS NULL OR (date(inactive_on) IS NOT NULL AND inactive_on = date(inactive_on) AND inactive_on >= '2000-01-01')),
  created_at  INTEGER NOT NULL,
  UNIQUE (fund_id, id),
  FOREIGN KEY (fund_id) REFERENCES fund(id)
) STRICT;

CREATE TABLE member_tier (                -- a member's tier, month by month (ADR-040)
  -- One-sided month rows exactly like dues_rate (no effective_to), corrected by a
  -- new row, never an overwrite; the highest correction of a month stands. The
  -- UPDATE trigger at the foot of this file holds that.
  id             INTEGER PRIMARY KEY,
  fund_id        INTEGER NOT NULL,
  member_id      INTEGER NOT NULL,
  tier_id        INTEGER,                 -- NULL = no dues obligation from this month
  effective_from TEXT    NOT NULL CHECK (effective_from GLOB '[0-9][0-9][0-9][0-9]-[0-1][0-9]'
                                         AND date(effective_from||'-01') IS NOT NULL
                                         AND effective_from >= '2000-01'),
  correction     INTEGER NOT NULL DEFAULT 0 CHECK (correction >= 0),
  created_at     INTEGER NOT NULL,
  UNIQUE (fund_id, id),
  UNIQUE (member_id, effective_from, correction),
  -- Deleting a member (setup duplicates only, ADR-024) takes their rows; safe,
  -- because a member any posted row names is refused by that row's FK first.
  FOREIGN KEY (fund_id, member_id) REFERENCES member(fund_id, id) ON DELETE CASCADE,
  -- No cascade: a tier any row names, even a superseded one, cannot be deleted
  -- (ADR-040). A NULL tier_id passes the composite (MATCH SIMPLE).
  FOREIGN KEY (fund_id, tier_id)   REFERENCES dues_tier(fund_id, id)
) STRICT;

-- The standing row of each month: the highest correction. Every dues read goes
-- through this view (internal/db's guard test fails a query that does not).
-- Named exceptions: the backup export, the next-correction read.
CREATE VIEW member_tier_standing AS
SELECT mt.id, mt.fund_id, mt.member_id, mt.tier_id, mt.effective_from, mt.correction, mt.created_at
FROM member_tier mt
WHERE mt.correction = (
  SELECT MAX(c.correction) FROM member_tier c
  WHERE c.member_id = mt.member_id AND c.effective_from = mt.effective_from
);

CREATE TABLE transfer (                   -- pair-holder: cash<->bank, or purpose reclass
  -- Value-neutral movements are two transactions of equal amount and opposite
  -- direction, bound by one of these rows (ADR-024). A single row would change
  -- the fund's total; nothing moved, so nothing may.
  id         INTEGER PRIMARY KEY,
  fund_id    INTEGER NOT NULL REFERENCES fund(id),
  kind       TEXT    NOT NULL CHECK (kind IN ('between_accounts','reclass_purpose')),
  -- Null is a roll (CloseIncidentalAndRoll); set names the row this pair
  -- corrects (ADR-033). It sits on the pair, not on either leg, because the
  -- correction is the pair. Forward FK to "transaction" below, resolved at
  -- insert time, not at CREATE TABLE.
  corrects_transaction_id INTEGER,
  -- Why a purpose pair that corrects nothing moved (ADR-036): 'roll' is an
  -- envelope closing into or out of Kas Utama (ADR-031), 'allocation' is the
  -- treasurer moving money between purposes on purpose ("Pindah peruntukan").
  -- NULL means the row predates the column; every such uncorrected pair is a
  -- roll, so readers treat NULL as roll. A separate column, not a third kind:
  -- kind answers only what shape of move this is (ADR-033).
  reason TEXT,
  created_at INTEGER NOT NULL,
  UNIQUE (fund_id, id),                   -- see account.id above: enables composite FKs from children
  FOREIGN KEY (fund_id, corrects_transaction_id) REFERENCES "transaction"(fund_id, id),
  -- Asymmetric on purpose (ADR-033): nothing but a purpose move may claim to
  -- correct anything, but a purpose move need not. The mirror (a correction
  -- must set the link) would need a third transfer.kind answering "why it
  -- moved" beside the two answering "what moved"; that split was refused.
  -- Unenforced, the worst a wrongly-linked roll can do is mislabel one row in
  -- Riwayat - unlike the dues-reversal CHECK below, which guards a query
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
  amount      INTEGER NOT NULL CHECK (amount > 0 AND amount <= 999999999999),
  incurred_on TEXT    NOT NULL CHECK (date(incurred_on) IS NOT NULL AND incurred_on = date(incurred_on) AND incurred_on >= '2000-01-01'),
  waived_on   TEXT             CHECK (waived_on IS NULL OR (date(waived_on) IS NOT NULL AND waived_on = date(waived_on) AND waived_on >= '2000-01-01')),
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
  -- SUM(ledger_entry.signed_amount) (the view below) and a negative amount is
  -- impossible rather than merely unexpected.
  amount      INTEGER NOT NULL CHECK (amount > 0 AND amount <= 999999999999),
  occurred_on TEXT    NOT NULL CHECK (date(occurred_on) IS NOT NULL AND occurred_on = date(occurred_on) AND occurred_on >= '2000-01-01'),
  kind        TEXT    NOT NULL CHECK (kind IN
                ('opening','normal','dues','reimbursement','adjustment','transfer')),
  member_id        INTEGER,               -- dues, or a named contribution (ADR-034)
  dues_period      TEXT,                  -- 'YYYY-MM'; several months paid at once = several rows
  reimbursement_id INTEGER,               -- the settling payout
  transfer_id      INTEGER,
  reverses_transaction_id INTEGER,        -- the dues or normal row this adjustment reverses (ADR-029, ADR-042)
  note        TEXT,
  created_at  INTEGER NOT NULL,
  UNIQUE (fund_id, id),
  -- Six composite FKs: each stops a transaction borrowing another fund's row,
  -- which a single-column REFERENCES would allow. The reverses_transaction_id
  -- one stops naming another fund's row as the row being reversed (ADR-029).
  FOREIGN KEY (fund_id, account_id)       REFERENCES account(fund_id, id),
  FOREIGN KEY (fund_id, purpose_id)       REFERENCES purpose(fund_id, id),
  FOREIGN KEY (fund_id, member_id)        REFERENCES member(fund_id, id),
  FOREIGN KEY (fund_id, reimbursement_id) REFERENCES reimbursement(fund_id, id),
  FOREIGN KEY (fund_id, transfer_id)      REFERENCES transfer(fund_id, id),
  FOREIGN KEY (fund_id, reverses_transaction_id) REFERENCES "transaction"(fund_id, id),
  CHECK (dues_period IS NULL OR (dues_period GLOB '[0-9][0-9][0-9][0-9]-[0-1][0-9]'
                                 AND date(dues_period||'-01') IS NOT NULL
                                 AND dues_period >= '2000-01')),
  CHECK (kind <> 'dues'          OR (member_id IS NOT NULL AND dues_period IS NOT NULL AND direction = 'in')),
  -- Two shapes of a non-dues row may carry a member: a dues reversal (the one
  -- kind='adjustment' shape, ADR-029) and a named contribution (the one
  -- kind='normal' shape, ADR-034). A CHECK cannot see purpose.kind, so which
  -- purpose a contribution may be tagged to is the BEFORE INSERT trigger's job
  -- below. Neither shape carries dues_period here: a dues reversal's period is
  -- verified against the payment it reverses by that same trigger, since a
  -- CHECK cannot read another row. Every other kind needs neither field.
  CHECK (kind =  'dues'          OR (member_id IS NULL AND dues_period IS NULL)
                                 OR (kind = 'normal' AND direction = 'in'
                                     AND reverses_transaction_id IS NULL AND dues_period IS NULL)
                                 OR (kind = 'adjustment' AND reverses_transaction_id IS NOT NULL
                                     AND member_id IS NOT NULL AND direction = 'out')),
  -- ...and the column guards itself the other way: nothing that is not a
  -- reversal may claim to reverse anything. Without this, a kind='normal' row
  -- with no member and no period satisfies the CHECK above yet sets
  -- reverses_transaction_id - which would hide the original row from every
  -- read of ledger_entry.reversed (that only asks whether *something* points
  -- at the row) and consume the reversed-once slot, so the genuine reversal could
  -- never be posted. The schema makes that row unrepresentable (ADR-029).
  -- Only the kind is pinned here (ADR-042): a reversal goes the OTHER way from the
  -- row it reverses, which a CHECK cannot see, so the trigger below checks
  -- direction, amount and location. dues_period is deliberately not required
  -- (ADR-034): reversing a named contribution carries none, reversing a dues
  -- payment must - which the trigger checks, as this CHECK cannot.
  CHECK (reverses_transaction_id IS NULL OR kind = 'adjustment'),
  CHECK (kind <> 'reimbursement' OR (reimbursement_id IS NOT NULL AND direction = 'out')),
  CHECK (kind <> 'transfer'      OR transfer_id IS NOT NULL)
  -- kind='adjustment' otherwise requires nothing extra: an ordinary correction
  -- may be raised any time, not only during a reconciliation (ADR-024).
) STRICT;

-- A CHECK reads only its own row, so the two cross-table halves of ADR-034 -
-- which purpose a named member may be tagged to, and what a reversal may
-- target and must copy - are a BEFORE INSERT trigger. The ledger checks first
-- so the treasurer gets copy, not a constraint error; this makes both shapes
-- unrepresentable for anything that writes around the ledger.
-- +goose StatementBegin
CREATE TRIGGER transaction_named_row_shape BEFORE INSERT ON "transaction" BEGIN
  -- A kind='normal' row naming a member is a contribution (ADR-034) and may
  -- only be tagged to an incidental purpose.
  SELECT RAISE(ABORT, 'a named transaction must be tagged to an incidental purpose')
  WHERE NEW.kind = 'normal' AND NEW.member_id IS NOT NULL
    AND NOT EXISTS (SELECT 1 FROM purpose WHERE id = NEW.purpose_id AND kind = 'incidental');

  -- A reversal (ADR-029, ADR-034, ADR-042) must reverse a dues or normal row, go
  -- the other way, and copy its amount, location, member and period exactly, never
  -- re-typed by the caller. "IS" is NULL-safe equality: "=" would refuse two
  -- unnamed rows. The purpose is the one copied field not checked: the effective
  -- purpose is a fold over correction pairs, which a trigger could only restate.
  -- Nor is the date: a restore keeps this trigger on, and a reversal already in a
  -- live file may predate its original.
  SELECT RAISE(ABORT, 'a reversal must reverse a dues payment or a normal transaction: the other way, same amount and location, same member and period')
  WHERE NEW.reverses_transaction_id IS NOT NULL
    AND NOT EXISTS (
      SELECT 1 FROM "transaction" o
      WHERE o.id = NEW.reverses_transaction_id
        AND o.kind IN ('dues', 'normal')
        AND o.direction <> NEW.direction
        AND o.amount = NEW.amount AND o.account_id = NEW.account_id
        AND o.member_id IS NEW.member_id AND o.dues_period IS NEW.dues_period
    );
END;
-- +goose StatementEnd

-- "Settle once" as a constraint, not prose. Partial, so other kinds are free.
CREATE UNIQUE INDEX reimbursement_settled_once ON "transaction"(reimbursement_id) WHERE kind = 'reimbursement';

-- A row is reversible at most once (ADR-029, ADR-042); partial, same shape as
-- reimbursement_settled_once. The name predates ADR-042 and stays.
CREATE UNIQUE INDEX dues_payment_reversed_once ON "transaction"(reverses_transaction_id) WHERE reverses_transaction_id IS NOT NULL;

-- One opening entry per account: nothing else stops a second opening row, and
-- a second one silently doubles the money the fund starts with.
CREATE UNIQUE INDEX opening_balance_once_per_account ON "transaction"(account_id) WHERE kind = 'opening';

CREATE TABLE receipt (                    -- attachment, not a ledger fact; addable after the fact
  -- A table of its own because ledger rows are immutable: a photo taken after
  -- the entry was posted still has somewhere to go (ADR-011).
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
  -- actual is what she counted: bounded on both sides. recorded is the ledger's
  -- balance of the location, a sum of bounded amounts that nothing refuses past
  -- the ceiling; a bound here would leave such a location impossible to count.
  -- difference is fixed by the equality CHECK below.
  recorded_amount   INTEGER NOT NULL,  -- frozen at snapshot time
  actual_amount     INTEGER NOT NULL CHECK (actual_amount     BETWEEN -999999999999 AND 999999999999),  -- what the treasurer counted
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
  -- A table, not purpose columns: only kind='incidental' rows have an occasion,
  -- a target or a closing date. The money itself is ordinary transactions
  -- carrying purpose_id; closing an envelope moves nothing, which is why
  -- closed_on lives here and not in the ledger. Mutable by design - nothing
  -- here is a posted fact.
  purpose_id    INTEGER PRIMARY KEY REFERENCES purpose(id),
  occasion      TEXT    NOT NULL CHECK (length(trim(occasion)) > 0),
  target_amount INTEGER CHECK (target_amount IS NULL OR (target_amount > 0 AND target_amount <= 999999999999)),
  opened_on     TEXT    NOT NULL CHECK (date(opened_on) IS NOT NULL AND opened_on = date(opened_on) AND opened_on >= '2000-01-01'),
  closed_on     TEXT             CHECK (closed_on IS NULL OR (date(closed_on) IS NOT NULL AND closed_on = date(closed_on) AND closed_on >= '2000-01-01')),
  -- One figure every expected member is asked to give (ADR-034). Not a dues
  -- rate: no tiers, no effective dates, editable like occasion.
  minimum_per_member INTEGER CHECK (minimum_per_member IS NULL OR (minimum_per_member > 0 AND minimum_per_member <= 999999999999)),
  created_at    INTEGER NOT NULL
) STRICT;

-- The members an envelope is *for* (ADR-034): the sick, the bereaved family,
-- the birthday pair - never expected to contribute themselves. Zero or more,
-- editable like occasion - a choice, not a record, hence ON DELETE CASCADE.
--
-- incidental has no fund_id (it is 1:1 with a purpose row), so fund ownership
-- is read through the composite (fund_id, purpose_id) FK to purpose (ADR-024),
-- and the plain purpose_id FK to incidental proves the purpose is an envelope,
-- not main or pass_through.
CREATE TABLE incidental_recipient (
  fund_id    INTEGER NOT NULL,
  purpose_id INTEGER NOT NULL,
  member_id  INTEGER NOT NULL,
  PRIMARY KEY (fund_id, purpose_id, member_id),
  FOREIGN KEY (purpose_id)          REFERENCES incidental(purpose_id),
  FOREIGN KEY (fund_id, purpose_id) REFERENCES purpose(fund_id, id),
  -- Deleting a member (setup duplicates only, ADR-024) takes their recipient
  -- rows with them rather than refusing, as a posted transaction would.
  FOREIGN KEY (fund_id, member_id)  REFERENCES member(fund_id, id) ON DELETE CASCADE
) STRICT;

-- (fund_id, occurred_on, id): the leftmost pair still serves every
-- fund_id+occurred_on range query, and with id the index fully orders the
-- (occurred_on DESC, id DESC) keyset scan of GET /api/transactions (#225).
CREATE INDEX transaction_by_date    ON "transaction"(fund_id, occurred_on, id);
CREATE INDEX transaction_by_account ON "transaction"(account_id, occurred_on);
CREATE INDEX transaction_by_purpose ON "transaction"(purpose_id);
CREATE INDEX transaction_by_dues    ON "transaction"(member_id, dues_period) WHERE kind = 'dues';

-- Same shape: orders the (incurred_on DESC, id DESC) keyset scan (#226).
CREATE INDEX reimbursement_by_date  ON reimbursement(fund_id, incurred_on, id);

-- Same shape: orders the (performed_at DESC, id DESC) keyset scan (#227).
CREATE INDEX reconciliation_by_date ON reconciliation(fund_id, performed_at, id);

-- The ledger as every read sees it (#627, #628): "transaction" plus the derived
-- facts each query used to re-write. internal/db/ledger_entry_test.go fails a
-- query under internal/store/queries that derives any of them itself.
--
--   signed_amount        the only place a minus appears (amount is always > 0).
--                        CAST so sqlc types a sum of it as int64.
--   reversed_by_transaction_id, reversed_on, reversed
--                        the reversal pointing at this row (ADR-029; at most one,
--                        dues_payment_reversed_once), its date, and whether one
--                        exists. A dated read (the public report's month-end)
--                        compares reversed_on with its bound: a reversal dated
--                        after the bound has not happened yet.
--   effective_purpose_id the tag this row's money is under NOW (ADR-033): its own
--                        purpose_id, moved by purpose corrections. A row's
--                        corrections form a path starting at purpose_id; the tag
--                        it ends on is the one the money entered once more than it
--                        left, so no ordering of corrections is needed (ids are
--                        not an order) and a row corrected back ends where it
--                        began. PostPurposeCorrection puts the target on the leg
--                        moving the same way as the row, so
--                        leg.direction = t.direction reads back what it wrote.
--
--                        NOT the tag a balance sums. Balances sum the stored
--                        purpose_id: the correction's legs already carry the
--                        move, so reading effective_purpose_id beside them would
--                        count it twice. It is for the correction dialog, for
--                        EffectivePurposeForTransaction and for the "corrected"
--                        marker; Riwayat lists the stored tag (ADR-033).
--
-- Unused columns cost nothing: SQLite drops them when it flattens the view, so a
-- balance never runs the reversal lookup or the correction walk; the list runs
-- the walk and skips only the reversal lookups.
CREATE VIEW ledger_entry AS
SELECT t.id, t.fund_id, t.account_id, t.purpose_id, t.direction, t.amount, t.occurred_on, t.kind,
       t.member_id, t.dues_period, t.reimbursement_id, t.transfer_id, t.reverses_transaction_id,
       t.note, t.created_at,
       CAST(CASE WHEN t.direction = 'in' THEN t.amount ELSE -t.amount END AS INTEGER) AS signed_amount,
       (SELECT r.id FROM "transaction" r WHERE r.reverses_transaction_id = t.id) AS reversed_by_transaction_id,
       (SELECT r.occurred_on FROM "transaction" r WHERE r.reverses_transaction_id = t.id) AS reversed_on,
       CAST(EXISTS (SELECT 1 FROM "transaction" r WHERE r.reverses_transaction_id = t.id) AS INTEGER) AS reversed,
       CAST(COALESCE((SELECT leg.purpose_id
                      FROM transfer c
                      JOIN "transaction" leg ON leg.transfer_id = c.id
                      WHERE c.corrects_transaction_id = t.id
                      GROUP BY leg.purpose_id
                      HAVING SUM(CASE WHEN leg.direction = t.direction THEN 1 ELSE -1 END)
                             + (leg.purpose_id = t.purpose_id) = 1
                      LIMIT 1), t.purpose_id) AS INTEGER) AS effective_purpose_id
FROM "transaction" t;

-- Immutability is a trigger, not a convention (ADR-024, CLAUDE.md rule 3).
-- INSERT stays open, which lets ADR-012's import restore a database. A snapshot
-- is a claim about a moment, so it is as immutable as the ledger: revisiting a
-- left_open difference means a second snapshot. incidental is deliberately
-- absent (mutable by design).

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

-- member_tier is immutable against UPDATE only (ADR-040). No BEFORE DELETE
-- trigger, deliberately: a member's deletion (the cascade), a never-named tier's
-- and restore's clear-all delete these rows, and the owners' foreign keys already
-- refuse the deletions that would lose a record. A test fails when any other
-- query deletes from the table.
-- +goose StatementBegin
CREATE TRIGGER member_tier_immutable_update BEFORE UPDATE ON member_tier BEGIN
  SELECT RAISE(ABORT, 'member_tier rows are immutable - a correction is a new row');
END;
-- +goose StatementEnd

-- dues_rate: UPDATE only, for the same reasons (ADR-040); DeleteDuesRatesByTier
-- and restore's clear-all delete rows, and a test fails when any other statement does.
-- +goose StatementBegin
CREATE TRIGGER dues_rate_immutable_update BEFORE UPDATE ON dues_rate BEGIN
  SELECT RAISE(ABORT, 'dues_rate rows are immutable - a correction is a new row');
END;
-- +goose StatementEnd

-- +goose Down

DROP VIEW ledger_entry;
DROP TRIGGER dues_rate_immutable_update;
DROP TRIGGER member_tier_immutable_update;
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
DROP VIEW member_tier_standing;
DROP TABLE member_tier;
DROP TABLE member;
DROP VIEW dues_rate_standing;
DROP TABLE dues_rate;
DROP TABLE dues_tier;
DROP INDEX purpose_single_main;
DROP TABLE purpose;
DROP TABLE account;
DROP TABLE fund;
DROP INDEX idempotency_key_by_expiry;
DROP TABLE idempotency_key;
DROP INDEX session_by_expiry;
DROP TABLE session;
DROP TABLE "user";
