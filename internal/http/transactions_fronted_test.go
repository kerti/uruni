package http

import (
	"context"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/kerti/uruni/internal/store"
)

// ADR-051 in Riwayat > Transaksi (#845): the list is a flow, so newFrontedPage's
// payout (fronted 27 Okt, paid from Tunai 12 Nov) is listed on 27 Okt, between
// the 3 Nov and 15 Sep ins - except under the Lokasi filter, a cash view, where
// it sits on 12 Nov. The month filter, the order and the keyset all follow.

// placedRow is one listed row as the list reads it: the day it is placed on,
// its own (cash) date, and the claim's date on a payout.
type placedRow struct {
	placed, occurred, claim string
}

func (p placedRow) String() string { return fmt.Sprintf("%s(%s/%s)", p.placed, p.occurred, p.claim) }

func placedRows(rows []transactionResponse) []placedRow {
	out := make([]placedRow, 0, len(rows))
	for _, r := range rows {
		claim := ""
		if r.ClaimIncurredOn != nil {
			claim = *r.ClaimIncurredOn
		}
		out = append(out, placedRow{r.PlacedOn, r.OccurredOn, claim})
	}
	return out
}

var (
	flowOrder = []placedRow{
		{"2026-11-03", "2026-11-03", ""},
		{"2026-10-27", "2026-11-12", "2026-10-27"},
		{"2026-09-15", "2026-09-15", ""},
	}
	cashOrder = []placedRow{
		{"2026-11-12", "2026-11-12", "2026-10-27"},
		{"2026-11-03", "2026-11-03", ""},
		{"2026-09-15", "2026-09-15", ""},
	}
)

func TestTransactionsListPlacesThePayoutOnItsFrontingDate(t *testing.T) {
	t.Parallel()
	p := newFrontedPage(t)
	r := authedRouterAt(t, p.db, func() time.Time { return time.Date(2026, 12, 5, 3, 0, 0, 0, time.UTC) })
	w := duesGroupWorld{r: r}
	cash := fmt.Sprint("account_id=", p.cashID)

	for _, tc := range []struct {
		name, query string
		want        []placedRow
	}{
		{"the whole fund", "", flowOrder},
		{"a pos is still a flow", fmt.Sprint("purpose_id=", p.mainID), flowOrder},
		{"October holds the payout", "month=2026-10", flowOrder[1:2]},
		{"November does not", "month=2026-11", flowOrder[:1]},
		{"Lokasi: on its own date", cash, cashOrder},
		{"Lokasi: November holds it", cash + "&month=2026-11", cashOrder[:2]},
		{"Lokasi: October does not", cash + "&month=2026-10", nil},
	} {
		got := placedRows(w.list(t, tc.query))
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: GET /api/transactions?%s = %v, want %v", tc.name, tc.query, got, tc.want)
		}
	}
}

// The keyset cuts on the placed date: with the cursor on the payout's own
// date (12 Nov), the next page would repeat the 3 Nov row. Every page size
// walks the set once, in order, for the flow and for the cash view.
func TestTransactionsPageKeysetFollowsThePlacedDate(t *testing.T) {
	t.Parallel()
	p := newFrontedPage(t)
	q := store.New(p.db)

	for _, view := range []struct {
		name    string
		account any
		cash    int64
		want    []placedRow
	}{
		{"flow", nil, 0, flowOrder},
		{"cash view", p.cashID, 1, cashOrder},
	} {
		for size := int64(1); size <= 3; size++ {
			var got []placedRow
			var cursorOn any
			var cursorID *int64
			for pages := 0; ; pages++ {
				if pages > 10 {
					t.Fatalf("%s, size %d: paging did not terminate", view.name, size)
				}
				rows, err := q.ListTransactionsPage(context.Background(), store.ListTransactionsPageParams{
					FundID: p.fund.ID, PageLimit: size + 1, CashView: view.cash, AccountID: view.account,
					CursorPlacedOn: cursorOn, CursorID: cursorID,
				})
				if err != nil {
					t.Fatalf("ListTransactionsPage() = %v", err)
				}
				more := int64(len(rows)) > size
				if more {
					rows = rows[:size]
				}
				for _, r := range rows {
					claim := ""
					if r.ClaimIncurredOn != nil {
						claim = *r.ClaimIncurredOn
					}
					got = append(got, placedRow{r.PlacedOn, r.OccurredOn, claim})
				}
				if !more {
					break
				}
				last := rows[len(rows)-1]
				cursorOn, cursorID = last.PlacedOn, &last.ID
			}
			if !slices.Equal(got, view.want) {
				t.Errorf("%s, page size %d: walked %v, want %v", view.name, size, got, view.want)
			}
		}
	}
}
