package money_test

import (
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/kerti/uruni/internal/money"
)

func TestAdd(t *testing.T) {
	tests := []struct {
		name     string
		a, b     money.Amount
		want     money.Amount
		overflow bool
	}{
		{name: "zero values are Rp 0, not unset", a: 0, b: 0, want: 0},
		{name: "ordinary dues payment", a: 50_000, b: 70_000, want: 120_000},
		{name: "an expense subtracted as a negative", a: 120_000, b: -2_000, want: 118_000},
		{name: "a balance can legitimately go negative", a: 1_000, b: -3_000, want: -2_000},
		{name: "up to the ceiling exactly", a: math.MaxInt64 - 1, b: 1, want: math.MaxInt64},
		{name: "down to the floor exactly", a: math.MinInt64 + 1, b: -1, want: math.MinInt64},
		{name: "one past the ceiling", a: math.MaxInt64, b: 1, overflow: true},
		{name: "one past the floor", a: math.MinInt64, b: -1, overflow: true},
		{name: "both extremes cancel", a: math.MaxInt64, b: math.MinInt64, want: -1},
		{name: "doubling the ceiling", a: math.MaxInt64, b: math.MaxInt64, overflow: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.a.Add(tt.b)
			assertResult(t, got, err, tt.want, tt.overflow)
		})
	}
}

func TestSub(t *testing.T) {
	tests := []struct {
		name     string
		a, b     money.Amount
		want     money.Amount
		overflow bool
	}{
		{name: "zero values are Rp 0, not unset", a: 0, b: 0, want: 0},
		{name: "a reconciliation difference that matches", a: 500_000, b: 500_000, want: 0},
		{name: "counted more than recorded", a: 520_000, b: 500_000, want: 20_000},
		{name: "counted less than recorded", a: 480_000, b: 500_000, want: -20_000},
		{name: "subtracting a negative adds", a: 1_000, b: -2_000, want: 3_000},
		{name: "down to the floor exactly", a: math.MinInt64 + 1, b: 1, want: math.MinInt64},
		{name: "one past the floor", a: math.MinInt64, b: 1, overflow: true},
		{name: "one past the ceiling", a: math.MaxInt64, b: -1, overflow: true},

		// The case an Add(-b) implementation gets wrong: negating math.MinInt64
		// wraps to itself, so a - MinInt64 would come back as a + MinInt64.
		// ADR-028 calls this out by name. No live call site can reach it today,
		// but actual_amount is a figure the treasurer types, so the one
		// unbounded input in the trust core meets the one asymmetric operation.
		{name: "MinInt64 as the subtrahend, representable", a: -1, b: math.MinInt64, want: math.MaxInt64},
		{name: "MinInt64 as the subtrahend, overflowing", a: 0, b: math.MinInt64, overflow: true},
		{name: "MinInt64 minus itself", a: math.MinInt64, b: math.MinInt64, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.a.Sub(tt.b)
			assertResult(t, got, err, tt.want, tt.overflow)
		})
	}
}

// A regression test for the implementation ADR-028 warns against: Sub written
// as Add(-b). Negating math.MinInt64 wraps back to itself, and the two ways
// that goes wrong point in opposite directions, so one example cannot catch
// both. Verified against big.Int truth over the edge cases plus two million
// random pairs: Add(-b) disagrees on eleven, all with math.MinInt64 as the
// subtrahend; the shipped Sub disagrees on none.
func TestSubIsNotAddOfTheNegation(t *testing.T) {
	t.Run("silently returns a wrong value where the true difference overflows", func(t *testing.T) {
		// Add(-b) computes 0 + MinInt64 = MinInt64, and its sign check sees a
		// sum that moved the expected way, so it returns MinInt64 with no
		// error — a confidently wrong number, which is the failure mode this
		// package exists to refuse.
		if got, err := money.Amount(0).Sub(math.MinInt64); !errors.Is(err, money.ErrOverflow) {
			t.Errorf("Sub(0, math.MinInt64) = (%d, %v), want ErrOverflow", got, err)
		}
	})

	t.Run("falsely reports overflow where the true difference fits", func(t *testing.T) {
		// The mirror case: -1 - MinInt64 is exactly math.MaxInt64, but Add(-b)
		// rejects it.
		got, err := money.Amount(-1).Sub(math.MinInt64)
		if err != nil {
			t.Fatalf("Sub(-1, math.MinInt64) returned %v, want %d", err, math.MaxInt64)
		}
		if got != math.MaxInt64 {
			t.Errorf("Sub(-1, math.MinInt64) = %d, want %d", got, math.MaxInt64)
		}
	})
}

func TestMul(t *testing.T) {
	tests := []struct {
		name     string
		a        money.Amount
		n        int64
		want     money.Amount
		overflow bool
	}{
		{name: "three months of dues at the pelaksana rate", a: 50_000, n: 3, want: 150_000},
		{name: "no periods owed", a: 50_000, n: 0, want: 0},
		{name: "no rate set", a: 0, n: 12, want: 0},
		{name: "zero times zero", a: 0, n: 0, want: 0},
		{name: "identity", a: 70_000, n: 1, want: 70_000},
		{name: "negating by count", a: 70_000, n: -1, want: -70_000},
		{name: "the ceiling exactly", a: math.MaxInt64, n: 1, want: math.MaxInt64},
		{name: "twice the ceiling", a: math.MaxInt64, n: 2, overflow: true},
		{name: "the floor exactly", a: math.MinInt64, n: 1, want: math.MinInt64},

		// math.MinInt64 / -1 is defined in Go as math.MinInt64, so the division
		// check agrees with itself here and needs the explicit guard.
		{name: "the floor negated, which has no positive counterpart", a: math.MinInt64, n: -1, overflow: true},
		{name: "a large amount by a large count", a: math.MaxInt64 / 2, n: 3, overflow: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.a.Mul(tt.n)
			assertResult(t, got, err, tt.want, tt.overflow)
		})
	}
}

func TestDBRoundTrip(t *testing.T) {
	for _, v := range []int64{0, 1, -1, 50_000, math.MaxInt64, math.MinInt64} {
		if got := money.FromDB(v).Int64(); got != v {
			t.Errorf("FromDB(%d).Int64() = %d, want %d", v, got, v)
		}
	}
}

// The API contract M4 is built against: a bare JSON number, which also rejects
// a fractional literal for free (ADR-026).
func TestJSON(t *testing.T) {
	t.Run("marshals as a bare number", func(t *testing.T) {
		got, err := json.Marshal(struct {
			Amount money.Amount `json:"amount"`
		}{Amount: 50_000})
		if err != nil {
			t.Fatalf("Marshal returned %v", err)
		}
		if want := `{"amount":50000}`; string(got) != want {
			t.Errorf("Marshal = %s, want %s", got, want)
		}
	})

	t.Run("unmarshals a whole number", func(t *testing.T) {
		var target struct {
			Amount money.Amount `json:"amount"`
		}
		if err := json.Unmarshal([]byte(`{"amount":50000}`), &target); err != nil {
			t.Fatalf("Unmarshal returned %v", err)
		}
		if target.Amount != 50_000 {
			t.Errorf("Amount = %d, want 50000", target.Amount)
		}
	})

	t.Run("rejects a fractional literal", func(t *testing.T) {
		var target struct {
			Amount money.Amount `json:"amount"`
		}
		if err := json.Unmarshal([]byte(`{"amount":50000.50}`), &target); err == nil {
			t.Fatal("Unmarshal accepted a fractional amount, want an error")
		}
	})
}

func assertResult(t *testing.T, got money.Amount, err error, want money.Amount, overflow bool) {
	t.Helper()

	if overflow {
		if !errors.Is(err, money.ErrOverflow) {
			t.Fatalf("got (%d, %v), want an error matching ErrOverflow", got, err)
		}
		if got != 0 {
			t.Errorf("got %d alongside the overflow error, want the zero Amount", got)
		}
		return
	}

	if err != nil {
		t.Fatalf("got error %v, want %d", err, want)
	}
	if got != want {
		t.Errorf("got %d, want %d", got, want)
	}
}
