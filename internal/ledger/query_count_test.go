package ledger

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"

	"github.com/kerti/uruni/internal/db"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// A statement counter at the driver, under database/sql, so every SELECT a
// ledger call makes is counted whichever Querier or transaction issued it: the
// constant-query guarantees (#532) are about what reaches SQLite, not about a
// call count some wrapper chose to keep.

// countingConnector hands database/sql connections that bump n once per
// statement run through ExecContext or QueryContext. BEGIN and COMMIT are not
// statements of either kind, so n counts the reads and writes alone.
type countingConnector struct {
	dsn string
	n   *atomic.Int64
}

func (c countingConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := (&sqlite.Driver{}).Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &countingConn{Conn: conn, n: c.n}, nil
}

func (c countingConnector) Driver() driver.Driver { return &sqlite.Driver{} }

type countingConn struct {
	driver.Conn
	n *atomic.Int64
}

func (c *countingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, query)
}

func (c *countingConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func (c *countingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.n.Add(1)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *countingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.n.Add(1)
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

// newCountingLedger is newTestLedger over a connection that counts. The returned
// counter runs from zero at the time of the call; tests read it as a difference
// around the call under test.
func newCountingLedger(t *testing.T) (*Ledger, *atomic.Int64) {
	t.Helper()
	var n atomic.Int64
	sqlDB := sql.OpenDB(countingConnector{
		dsn: "file::memory:?_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)",
		n:   &n,
	})
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("Close() = %v, want no error", err)
		}
	})
	if _, err := db.Up(context.Background(), sqlDB, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("db.Up() = %v, want no error", err)
	}
	return New(sqlDB), &n
}

// queriesRun is how many statements fn sends to SQLite.
func queriesRun(n *atomic.Int64, fn func()) int64 {
	before := n.Load()
	fn()
	return n.Load() - before
}

// scaleNow is the clock every scale fixture is built and read at.
var scaleNow = time.Date(2026, time.October, 15, 12, 0, 0, 0, time.UTC)

const scalePeriod = "2026-10"

// scaleFund is a fund of `members` members who each joined `months` months
// before scalePeriod, over three accounts and three purposes. Members cycle
// through four ways of having paid, so a roster holds all of: square, in
// arrears, short, and paid in advance (the case that walks paidThrough). Two
// tiers alternate, the second with a mid-history rate rise.
type scaleFund struct {
	f       fixture
	members []int64
}

func newScaleFund(t *testing.T, l *Ledger, members, months int) scaleFund {
	t.Helper()
	ctx := context.Background()
	f := newFixture(t, l)
	q := store.New(l.db)
	a := createDuesTier(t, q, f.fundID, "Tier A")
	createDuesRate(t, q, a, 10_000, "2000-01")
	b := createDuesTier(t, q, f.fundID, "Tier B")
	createDuesRate(t, q, b, 20_000, "2000-01")
	createDuesRate(t, q, b, 30_000, "2025-06")

	start := time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC).AddDate(0, -months, 0)
	joinedOn := start.Format(time.DateOnly)
	sf := scaleFund{f: f}
	for i := 0; i < members; i++ {
		tier, pays := &a, money.Amount(10_000)
		if (i/2)%2 == 1 {
			tier, pays = &b, 30_000
		}
		id := createDuesMember(t, q, f.fundID, duesMemberParams{name: fmt.Sprintf("Warga %03d", i), tierID: tier, joinedOn: &joinedOn})
		sf.members = append(sf.members, id)

		var periods []PeriodAmount
		for m := 0; m <= months+2; m++ {
			period := start.AddDate(0, m, 0).Format(duesPeriodLayout)
			var amount money.Amount
			switch i % 4 {
			case 0: // square through last month
				if period < scalePeriod {
					amount = pays
				}
			case 1: // never paid
			case 2: // short in the first month only
				if m == 0 {
					amount = 5_000
				}
			case 3: // paid through two months ahead
				amount = pays
			}
			if amount > 0 {
				periods = append(periods, PeriodAmount{DuesPeriod: period, Amount: amount})
			}
		}
		if len(periods) == 0 {
			continue
		}
		if _, err := l.at(scaleNow).PostDuesPayments(ctx, PostDuesPaymentsParams{
			FundID: f.fundID, AccountID: f.cashID, MemberID: id, OccurredOn: "2026-10-01", Periods: periods,
		}); err != nil {
			t.Fatalf("PostDuesPayments(member %d) = %v, want no error", id, err)
		}
	}
	return sf
}
