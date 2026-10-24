package txguard

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

func openPool(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestMarkedReportsTheMark(t *testing.T) {
	ctx := context.Background()
	if Marked(ctx) {
		t.Error("Marked(background) = true, want false")
	}
	if !Marked(Mark(ctx)) {
		t.Error("Marked(Mark(background)) = false, want true")
	}
}

func TestUnmarkedContextPassesThroughEveryMethod(t *testing.T) {
	ctx := context.Background()
	p := Wrap(openPool(t))

	if _, err := p.ExecContext(ctx, "CREATE TABLE t (n INTEGER)"); err != nil {
		t.Fatalf("ExecContext: %v", err)
	}
	stmt, err := p.PrepareContext(ctx, "INSERT INTO t (n) VALUES (?)")
	if err != nil {
		t.Fatalf("PrepareContext: %v", err)
	}
	if _, err := stmt.ExecContext(ctx, 7); err != nil {
		t.Fatalf("prepared exec: %v", err)
	}
	_ = stmt.Close()

	rows, err := p.QueryContext(ctx, "SELECT n FROM t")
	if err != nil {
		t.Fatalf("QueryContext: %v", err)
	}
	_ = rows.Close()

	var n int
	if err := p.QueryRowContext(ctx, "SELECT n FROM t").Scan(&n); err != nil || n != 7 {
		t.Fatalf("QueryRowContext = %d, %v, want 7, nil", n, err)
	}
}

// The pool's one connection is held by an open transaction, as it is in the
// code the guard protects: an unguarded call would block until the deadline.
func TestMarkedContextIsRefusedOnEveryMethodWithoutBlocking(t *testing.T) {
	db := openPool(t)
	p := Wrap(db)

	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()

	ctx, cancel := context.WithTimeout(Mark(context.Background()), 2*time.Second)
	defer cancel()

	check := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, ErrNested) {
			t.Errorf("%s on a marked context = %v, want ErrNested", name, err)
		}
	}

	_, err = p.ExecContext(ctx, "SELECT 1")
	check("ExecContext", err)
	_, err = p.PrepareContext(ctx, "SELECT 1")
	check("PrepareContext", err)
	rows, err := p.QueryContext(ctx, "SELECT 1")
	if rows != nil {
		_ = rows.Close()
	}
	check("QueryContext", err)
	var n int
	check("QueryRowContext", p.QueryRowContext(ctx, "SELECT 1").Scan(&n))

	if ctx.Err() != nil {
		t.Errorf("refusals took until the deadline (%v): they blocked on the held connection", ctx.Err())
	}
}
