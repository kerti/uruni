package demo

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/kerti/uruni/internal/ledger"
	"github.com/kerti/uruni/internal/money"
	"github.com/kerti/uruni/internal/store"
)

// The story of the fixture, told as events: each one a thing the treasurer
// does on a day of a month offset, run through the real ledger with its clock
// standing on that day. They are written here in the order they read, then
// sorted by (month, day) - stably, so two things on one day keep the order
// they are written in - and replayed oldest first.
//
// Nothing here edits or deletes a posted row. A mistake is a reversal, a
// purpose correction or a corrected rate, as it is for a real treasurer.

// event is one thing that happens.
type event struct {
	month, day int
	what       string
	run        func(*step) error
}

// step is the world an event runs in: the ledger clocked at the event's day, the
// date it is on, and the rows earlier events made.
type step struct {
	ctx  context.Context
	l    *ledger.Ledger
	w    *world
	date string
}

// world is what the replay learns as it goes: the ids the later events name.
type world struct {
	cal        calendar
	uploadsDir string

	fundID  int64
	mainID  int64
	cashID  int64
	bankID  int64
	tiers   map[string]int64 // tier key -> id
	members map[string]int64 // member name -> id
	titipan int64            // the pass-through purpose for the uniforms
}

// story collects events.
type story struct {
	w      *world
	events []event
}

func (s *story) at(month, day int, what string, run func(*step) error) {
	s.events = append(s.events, event{month: month, day: day, what: what, run: run})
}

// replayHistory writes the fourteen months of entries: it builds the story,
// orders it, and runs each event through ledgerAt at its own day.
func replayHistory(ctx context.Context, ledgerAt func(time.Time) *ledger.Ledger, cal calendar, setup ledger.SetUpFundResult, uploadsDir string) error {
	w := &world{
		cal: cal, uploadsDir: uploadsDir,
		fundID: setup.Fund.ID, mainID: setup.MainPurposeID, cashID: setup.Accounts[0].ID, bankID: setup.Accounts[1].ID,
		tiers: map[string]int64{}, members: map[string]int64{},
	}
	s := &story{w: w}
	s.setup()
	s.dues()
	s.everydaySpending()
	s.passThrough()
	s.reimbursements()
	s.farewellForErna()
	s.visitToArga()
	s.babyOfSari()
	s.mistakes()
	s.cekKas()

	sort.SliceStable(s.events, func(i, j int) bool {
		a, b := s.events[i], s.events[j]
		if a.month != b.month {
			return a.month < b.month
		}
		return a.day < b.day
	})
	for _, ev := range s.events {
		st := &step{ctx: ctx, l: ledgerAt(cal.instant(ev.month, ev.day)), w: w, date: cal.date(ev.month, ev.day)}
		if err := ev.run(st); err != nil {
			return fmt.Errorf("%s (month %d, day %d): %w", ev.what, ev.month, ev.day, err)
		}
	}
	return nil
}

// --- small helpers every event leans on ---

func (st *step) post(direction ledger.Direction, accountID, purposeID, amount int64, note string) (store.Transaction, error) {
	return st.l.PostTransaction(st.ctx, ledger.PostTransactionParams{
		FundID: st.w.fundID, AccountID: accountID, PurposeID: purposeID, Direction: direction,
		Amount: money.Amount(amount), OccurredOn: st.date, Note: &note,
	})
}

// contribute is a member (or, with name empty, an anonymous giver) putting money
// into an envelope.
func (st *step) contribute(purposeID int64, name string, amount int64, note string) (store.Transaction, error) {
	p := ledger.PostTransactionParams{
		FundID: st.w.fundID, AccountID: st.w.cashID, PurposeID: purposeID, Direction: ledger.DirectionIn,
		Amount: money.Amount(amount), OccurredOn: st.date,
	}
	if note != "" {
		p.Note = &note
	}
	if name != "" {
		id := st.w.members[name]
		p.MemberID = &id
	}
	return st.l.PostTransaction(st.ctx, p)
}

func (st *step) payDues(name string, accountID int64, periods []ledger.PeriodAmount, note string) ([]store.Transaction, error) {
	p := ledger.PostDuesPaymentsParams{
		FundID: st.w.fundID, AccountID: accountID, MemberID: st.w.members[name], OccurredOn: st.date, Periods: periods,
	}
	if note != "" {
		p.Note = &note
	}
	return st.l.PostDuesPayments(st.ctx, p)
}

func period(cal calendar, month int, amount int64) ledger.PeriodAmount {
	return ledger.PeriodAmount{DuesPeriod: cal.period(month), Amount: money.Amount(amount)}
}

// --- M-13: the fund is set up ---

const (
	openingCash = 1_250_000
	openingBank = 3_000_000
	// leadRateTypo is the Team Leader rate typed wrongly when the rates rise, and
	// corrected the next day (Perbaiki nominal).
	leadRateTypo = 55_000
)

// setup is the first day: tiers and their rates, the eleven members, the dues
// some of them paid before the fund existed, and the cek kas that settles those
// with an adjusted plug. Later rows of the same shape (a member who joins, a
// tier that changes, the rates that rise) are told where they happen.
func (s *story) setup() {
	w := s.w
	for _, spec := range tierSpecs {
		s.at(firstMonth, 1, "creating the "+spec.name+" tier", func(st *step) error {
			tier, err := st.l.CreateDuesTier(st.ctx, ledger.CreateDuesTierParams{FundID: w.fundID, Name: spec.name})
			if err != nil {
				return err
			}
			w.tiers[spec.key] = tier.ID
			_, err = st.l.CreateDuesRate(st.ctx, ledger.CreateDuesRateParams{
				FundID: w.fundID, TierID: tier.ID, Amount: money.Amount(spec.before), EffectiveFrom: w.cal.period(firstMonth),
			})
			return err
		})
	}

	for _, p := range roster {
		s.at(p.joined.month, p.joined.day, "adding "+p.name, func(st *step) error {
			tier := p.tiers[0].tier
			if p.enteredAs != "" {
				tier = p.enteredAs
			}
			tierID := w.tiers[tier]
			m, err := st.l.CreateMember(st.ctx, ledger.CreateMemberParams{
				FundID: w.fundID, Name: p.name, TierID: &tierID, JoinedOn: st.date,
			})
			if err != nil {
				return err
			}
			w.members[p.name] = m.ID
			return nil
		})
		if p.enteredAs != "" {
			// Entered in the wrong tier, set right the same day. The corrected row
			// takes the next correction number of the join month; the first stays in
			// the data.
			s.at(p.joined.month, p.joined.day, "correcting the tier of "+p.name, func(st *step) error {
				tierID := w.tiers[p.tiers[0].tier]
				_, err := st.l.UpdateMember(st.ctx, ledger.UpdateMemberParams{
					FundID: w.fundID, MemberID: w.members[p.name], SetTier: true, TierID: &tierID, TierFrom: w.cal.period(p.joined.month),
				})
				return err
			})
		}
		for _, change := range p.tiers[1:] {
			s.at(change.month, 1, "changing the tier of "+p.name, func(st *step) error {
				var tierID *int64
				if change.tier != "" {
					id := w.tiers[change.tier]
					tierID = &id
				}
				_, err := st.l.UpdateMember(st.ctx, ledger.UpdateMemberParams{
					FundID: w.fundID, MemberID: w.members[p.name], SetTier: true, TierID: tierID, TierFrom: w.cal.period(change.month),
				})
				return err
			})
		}
		if p.left != nil {
			s.at(p.left.month, p.left.day, p.name+" leaves", func(st *step) error {
				_, err := st.l.UpdateMember(st.ctx, ledger.UpdateMemberParams{
					FundID: w.fundID, MemberID: w.members[p.name], SetInactiveOn: true, InactiveOn: &st.date,
				})
				return err
			})
		}
	}

	// The dues paid before Uruni: recorded on the setup day as the product tells a
	// treasurer to, then squared away by the adjusted cek kas below.
	var prepaid int64
	for _, pre := range prepaidBeforeUruni {
		for _, p := range roster {
			if p.name != pre.name {
				continue
			}
			for _, m := range pre.months {
				prepaid += p.owes(m)
			}
		}
	}
	s.at(firstMonth, 1, "recording the dues paid before Uruni", func(st *step) error {
		for _, pre := range prepaidBeforeUruni {
			var periods []ledger.PeriodAmount
			for _, p := range roster {
				if p.name != pre.name {
					continue
				}
				for _, m := range pre.months {
					periods = append(periods, period(w.cal, m, p.owes(m)))
				}
			}
			if _, err := st.payDues(pre.name, w.cashID, periods, "Dibayar sebelum Uruni, sudah termasuk saldo awal"); err != nil {
				return err
			}
		}
		return nil
	})
	s.at(firstMonth, 1, "the first cek kas, settled with an adjusted plug", func(st *step) error {
		cash, err := st.l.AccountBalance(st.ctx, w.fundID, w.cashID)
		if err != nil {
			return err
		}
		bank, err := st.l.AccountBalance(st.ctx, w.fundID, w.bankID)
		if err != nil {
			return err
		}
		plug := money.Amount(prepaid)
		note := "Iuran yang dibayar sebelum Uruni sudah ada di saldo awal"
		cashActual, err := cash.Sub(plug)
		if err != nil {
			return err
		}
		fixNote := "Iuran dibayar sebelum Uruni, sudah termasuk saldo awal"
		_, err = st.l.TakeReconciliation(st.ctx, ledger.TakeReconciliationParams{
			FundID: w.fundID, Note: &note,
			Counts: []ledger.AccountCount{
				{AccountID: w.cashID, ActualAmount: cashActual, Resolution: ledger.ResolutionAdjusted, Fix: &ledger.Fix{
					PurposeID: w.mainID, Direction: ledger.DirectionOut, Amount: plug, OccurredOn: st.date, Note: &fixNote,
				}},
				{AccountID: w.bankID, ActualAmount: bank, Resolution: ledger.ResolutionMatched},
			},
		})
		return err
	})

	// M-6: every rate rises. The Team Leader one is typed wrongly first and
	// corrected the next day, before anyone has paid at it.
	for _, spec := range tierSpecs {
		amount := spec.from
		if spec.key == tierLead {
			amount = leadRateTypo
		}
		var rateID int64
		s.at(rateRiseMonth, 1, "raising the "+spec.name+" rate", func(st *step) error {
			r, err := st.l.CreateDuesRate(st.ctx, ledger.CreateDuesRateParams{
				FundID: w.fundID, TierID: w.tiers[spec.key], Amount: money.Amount(amount), EffectiveFrom: w.cal.period(rateRiseMonth),
			})
			rateID = r.ID
			return err
		})
		if spec.key == tierLead {
			s.at(rateRiseMonth, 2, "correcting the Team Leader rate", func(st *step) error {
				_, err := st.l.CorrectDuesRate(st.ctx, w.fundID, rateID, money.Amount(spec.from))
				return err
			})
		}
	}
}

// --- dues, every month ---

// dues turns the roster and its exceptions (duesCases) into payments: everyone
// pays what they owe on their usual day, except where the story says otherwise.
func (s *story) dues() {
	w := s.w
	for _, p := range roster {
		for m := max(p.joined.month, firstMonth); m <= thisMonth; m++ {
			if p.owes(m) == 0 {
				continue
			}
			var periods []ledger.PeriodAmount
			note := ""
			c, special := duesCases[duesKey{p.name, m}]
			switch {
			case !special:
				periods = append(periods, period(w.cal, m, p.owes(m)))
			case c.kind == duesPartial:
				periods = append(periods, period(w.cal, m, c.amount))
				note = "Bayar sebagian dulu"
			case c.kind == duesAhead:
				for k := m; k <= m+c.months; k++ {
					periods = append(periods, period(w.cal, k, rate(p.tierAt(k), k)))
				}
				note = "Bayar di muka sampai bulan depan"
			}
			// The month before was left to ride along with this one.
			if prev := duesCases[duesKey{p.name, m - 1}]; prev.kind == duesWithNext && p.owes(m-1) > 0 {
				periods = append([]ledger.PeriodAmount{period(w.cal, m-1, p.owes(m-1))}, periods...)
				note = "Bayar dua bulan sekaligus"
			}
			if len(periods) == 0 {
				continue
			}
			day := p.payDay
			if m == p.joined.month {
				day = max(day, p.joined.day)
			}
			s.at(m, day, fmt.Sprintf("dues of %s", p.name), func(st *step) error {
				account := w.cashID
				if p.viaBank {
					account = w.bankID
				}
				_, err := st.payDues(p.name, account, periods, note)
				return err
			})
		}
	}

	// Mas Pandu tops his part payment of M-2 up a month later.
	s.at(-1, 20, "Mas Pandu completes his dues", func(st *step) error {
		_, err := st.payDues("Mas Pandu", w.bankID, []ledger.PeriodAmount{period(w.cal, -2, 15_000)}, "Pelunasan bulan lalu")
		return err
	})
}

// --- ordinary spending and the small movements of every month ---

type expenseKind struct {
	note       string
	base, step int64
	bank       bool
}

var expenseKinds = []expenseKind{
	{"Galon air minum", 36_000, 3_000, false},
	{"Kopi, teh dan gula untuk pantry", 68_000, 5_000, false},
	{"Kertas HVS dan map", 54_000, 4_000, false},
	{"Tinta printer", 95_000, 5_000, false},
	{"Tisu dan sabun cuci tangan", 42_000, 3_000, false},
	{"Snack rapat bulanan", 75_000, 5_000, false},
	{"Pulsa dan internet kantor", 100_000, 0, true},
	{"Fotokopi dan jilid laporan", 28_000, 2_000, false},
	{"Baterai dan lakban", 24_000, 1_000, false},
	{"Iuran kebersihan", 30_000, 0, false},
	{"Air mineral gelas untuk tamu", 33_000, 2_000, false},
	{"Konsumsi lembur akhir bulan", 85_000, 5_000, false},
}

var expenseDays = []int{4, 10, 17, 22, 26}

// everydaySpending is three to five ordinary expenses a month, cash put into the
// bank, the bank's small interest and, now and then, a little income.
func (s *story) everydaySpending() {
	w := s.w
	for i, m := 0, firstMonth; m <= thisMonth; i, m = i+1, m+1 {
		for j := 0; j < 3+i%3; j++ {
			kind := expenseKinds[(i*7+j*5)%len(expenseKinds)]
			amount := kind.base + kind.step*int64((i+j)%3)
			s.at(m, expenseDays[j], kind.note, func(st *step) error {
				account := w.cashID
				if kind.bank {
					account = w.bankID
				}
				_, err := st.post(ledger.DirectionOut, account, w.mainID, amount, kind.note)
				return err
			})
		}
		if m > firstMonth {
			// Cash goes to the bank in one month and comes back in the next, so
			// neither location drifts away.
			from, to, amount, note := w.cashID, w.bankID, int64(150_000+25_000*(i%3)), "Setor tunai ke rekening"
			if i%2 == 1 {
				from, to, amount, note = w.bankID, w.cashID, int64(200_000+25_000*(i%3)), "Ambil tunai dari rekening"
			}
			s.at(m, 14, note, func(st *step) error {
				_, err := st.l.PostTransferBetweenAccounts(st.ctx, ledger.PostTransferBetweenAccountsParams{
					FundID: w.fundID, PurposeID: w.mainID, FromAccountID: from, ToAccountID: to,
					Amount: money.Amount(amount), OccurredOn: st.date, Note: &note,
				})
				return err
			})
			s.at(m, 29, "bank fee", func(st *step) error {
				_, err := st.post(ledger.DirectionOut, w.bankID, w.mainID, 2_500, "Biaya administrasi rekening")
				return err
			})
			interest := int64(3_000 + 500*(i%5))
			s.at(m, 28, "bank interest", func(st *step) error {
				_, err := st.post(ledger.DirectionIn, w.bankID, w.mainID, interest, "Bunga rekening")
				return err
			})
		}
		if i%3 == 1 {
			s.at(m, 20, "selling old boxes", func(st *step) error {
				_, err := st.post(ledger.DirectionIn, w.cashID, w.mainID, 20_000+5_000*int64(i%4), "Jual kardus bekas")
				return err
			})
		}
	}
}

// --- a pass-through, collected and forwarded (M-9 and M-3) ---

func (s *story) passThrough() {
	w := s.w
	s.at(-9, 2, "opening the uniform pass-through", func(st *step) error {
		p, err := st.l.CreatePassThroughPurpose(st.ctx, ledger.CreatePassThroughPurposeParams{FundID: w.fundID, Name: "Titipan Seragam"})
		w.titipan = p.ID
		return err
	})
	round := func(month int, days []int, amounts []int64, forwardDay int, note string) {
		var total int64
		for i, day := range days {
			total += amounts[i]
			s.at(month, day, "collecting for the uniforms", func(st *step) error {
				_, err := st.post(ledger.DirectionIn, w.cashID, w.titipan, amounts[i], note)
				return err
			})
		}
		s.at(month, forwardDay, "paying the uniform maker", func(st *step) error {
			_, err := st.post(ledger.DirectionOut, w.cashID, w.titipan, total, "Dibayar ke konveksi seragam")
			return err
		})
	}
	round(-9, []int{6, 13, 20}, []int64{150_000, 100_000, 150_000}, 22, "Uang seragam dari anggota")
	round(-3, []int{7, 15}, []int64{125_000, 125_000}, 18, "Uang seragam dari anggota")
}

// --- reimbursements: one settled, one still owed; their receipts ---

func (s *story) reimbursements() {
	w := s.w
	var settled, owed int64
	s.at(-3, 6, "Mas Pandu fronts a water dispenser", func(st *step) error {
		note := "Dispenser air minum untuk pantry"
		r, err := st.l.CreateReimbursement(st.ctx, ledger.CreateReimbursementParams{
			FundID: w.fundID, MemberID: w.members["Mas Pandu"], PurposeID: w.mainID,
			Amount: 285_000, IncurredOn: st.date, Note: &note,
		})
		settled = r.ID
		return err
	})
	s.at(-3, 6, "the dispenser receipt", func(st *step) error {
		return attachReceipt(st.ctx, st.l, w.fundID, w.uploadsDir, nil, &settled, receiptSlip{
			shop:  "Toko Sumber Rejeki",
			lines: []slipLine{{"Dispenser galon", 275_000}, {"Selang air", 10_000}},
			total: 285_000,
		})
	})
	s.at(-3, 12, "settling the water dispenser", func(st *step) error {
		_, err := st.l.SettleReimbursement(st.ctx, ledger.SettleReimbursementParams{
			FundID: w.fundID, ReimbursementID: settled, AccountID: w.cashID, OccurredOn: st.date,
		})
		return err
	})

	s.at(thisMonth, 6, "Mbak Anisa fronts a cake", func(st *step) error {
		note := "Kue ulang tahun kantor"
		r, err := st.l.CreateReimbursement(st.ctx, ledger.CreateReimbursementParams{
			FundID: w.fundID, MemberID: w.members["Mbak Anisa"], PurposeID: w.mainID,
			Amount: 120_000, IncurredOn: st.date, Note: &note,
		})
		owed = r.ID
		return err
	})
	s.at(thisMonth, 6, "the cake receipt", func(st *step) error {
		return attachReceipt(st.ctx, st.l, w.fundID, w.uploadsDir, nil, &owed, receiptSlip{
			shop:  "Toko Roti Manis",
			lines: []slipLine{{"Kue tart sedang", 105_000}, {"Lilin dan pisau", 15_000}},
			total: 120_000,
		})
	})
}

// --- M-8: Perpisahan Bu Erna ---

type gift struct {
	name   string
	amount int64
	day    int
}

func (s *story) farewellForErna() {
	w := s.w
	var envelope int64
	s.at(-8, 3, "opening the farewell envelope", func(st *step) error {
		minimum := money.Amount(20_000)
		e, err := st.l.OpenIncidental(st.ctx, ledger.OpenIncidentalParams{
			FundID: w.fundID, Occasion: "Perpisahan Bu Erna", OpenedOn: st.date, MinimumPerMember: &minimum,
			RecipientMemberIDs: []int64{w.members["Bu Erna"]},
		})
		envelope = e.PurposeID
		return err
	})
	for _, g := range []gift{
		{"Pak Arga", 20_000, 5}, {"Mbak Dewi", 25_000, 5}, {"Mbak Indri", 20_000, 6}, {"Mbak Sari", 20_000, 6},
		{"Mas Pandu", 20_000, 7}, {"Mbak Dini", 20_000, 7}, {"Mas Yoga", 20_000, 8}, {"Pak Lingga", 20_000, 9},
		{"Mbak Anisa", 25_000, 9}, {"", 50_000, 10},
	} {
		s.at(-8, g.day, "a farewell contribution", func(st *step) error {
			_, err := st.contribute(envelope, g.name, g.amount, "")
			return err
		})
	}
	s.at(-8, 16, "buying the farewell gift", func(st *step) error {
		_, err := st.post(ledger.DirectionOut, w.cashID, envelope, 210_000, "Hadiah perpisahan dan konsumsi")
		return err
	})
	s.at(-8, 20, "closing the farewell envelope", func(st *step) error {
		note := "Sisa amplop masuk Kas Utama"
		_, err := st.l.CloseIncidentalAndRoll(st.ctx, ledger.CloseIncidentalAndRollParams{
			FundID: w.fundID, PurposeID: envelope, AccountID: w.cashID, ClosedOn: st.date, Note: &note,
		})
		return err
	})
}

// --- M-2: Jenguk Pak Arga, closed and then reopened ---

func (s *story) visitToArga() {
	w := s.w
	var envelope int64
	var bingkisan store.Transaction
	s.at(-2, 3, "opening the visit envelope", func(st *step) error {
		minimum := money.Amount(15_000)
		e, err := st.l.OpenIncidental(st.ctx, ledger.OpenIncidentalParams{
			FundID: w.fundID, Occasion: "Jenguk Pak Arga", OpenedOn: st.date, MinimumPerMember: &minimum,
			RecipientMemberIDs: []int64{w.members["Pak Arga"]},
		})
		envelope = e.PurposeID
		return err
	})
	for _, g := range []gift{
		{"Mbak Dewi", 20_000, 4}, {"Mbak Indri", 20_000, 4}, {"Mbak Sari", 15_000, 5}, {"Mas Pandu", 15_000, 5},
		{"Mbak Dini", 15_000, 6}, {"Pak Lingga", 20_000, 6}, {"Mbak Anisa", 15_000, 7}, {"Mas Fajar", 15_000, 7},
	} {
		s.at(-2, g.day, "a visit contribution", func(st *step) error {
			_, err := st.contribute(envelope, g.name, g.amount, "")
			return err
		})
	}
	// Kas Utama adds to the envelope (Pindah pos).
	s.at(-2, 5, "moving money from Kas Utama to the visit envelope", func(st *step) error {
		note := "Kas ikut menyumbang"
		_, err := st.l.PostPurposeMove(st.ctx, ledger.PostPurposeMoveParams{
			FundID: w.fundID, FromPurposeID: w.mainID, ToPurposeID: envelope, AccountID: w.cashID,
			Amount: 100_000, OccurredOn: st.date, Note: &note,
		})
		return err
	})
	// The basket is first recorded as an ordinary expense of Kas Utama, then its
	// pos is corrected to the envelope it was bought for.
	s.at(-2, 6, "buying the basket", func(st *step) error {
		var err error
		bingkisan, err = st.post(ledger.DirectionOut, w.cashID, w.mainID, 150_000, "Buah dan bingkisan untuk Pak Arga")
		return err
	})
	s.at(-2, 7, "correcting the basket's pos", func(st *step) error {
		_, err := st.l.PostPurposeCorrection(st.ctx, ledger.PostPurposeCorrectionParams{
			FundID: w.fundID, TransactionID: bingkisan.ID, PurposeID: envelope,
		})
		return err
	})
	s.at(-2, 9, "closing the visit envelope", func(st *step) error {
		note := "Sisa amplop masuk Kas Utama"
		_, err := st.l.CloseIncidentalAndRoll(st.ctx, ledger.CloseIncidentalAndRollParams{
			FundID: w.fundID, PurposeID: envelope, AccountID: w.cashID, ClosedOn: st.date, Note: &note,
		})
		return err
	})
	// Late contributions come in, so the envelope is opened again.
	s.at(-2, 18, "reopening the visit envelope", func(st *step) error {
		_, err := st.l.ReopenIncidental(st.ctx, w.fundID, envelope)
		return err
	})
	for _, g := range []gift{{"Mas Rizki", 15_000, 19}, {"Mbak Wulan", 15_000, 20}, {"", 10_000, 20}} {
		s.at(-2, g.day, "a late visit contribution", func(st *step) error {
			_, err := st.contribute(envelope, g.name, g.amount, "Menyusul")
			return err
		})
	}
}

// --- M0: Kelahiran anak Mbak Sari, open ---

func (s *story) babyOfSari() {
	w := s.w
	var envelope int64
	s.at(thisMonth, 4, "opening the baby envelope", func(st *step) error {
		minimum, target := money.Amount(25_000), money.Amount(600_000)
		e, err := st.l.OpenIncidental(st.ctx, ledger.OpenIncidentalParams{
			FundID: w.fundID, Occasion: "Kelahiran anak Mbak Sari", OpenedOn: st.date,
			TargetAmount: &target, MinimumPerMember: &minimum,
			RecipientMemberIDs: []int64{w.members["Mbak Sari"]},
		})
		envelope = e.PurposeID
		return err
	})
	// Mas Rizki and Mbak Wulan have given nothing (Belum menyumbang), Mbak Indri
	// less than the minimum (Kurang dari minimal), and one gift has no name.
	for _, g := range []gift{
		{"Pak Arga", 25_000, 5}, {"Mbak Dewi", 50_000, 5}, {"Mas Pandu", 25_000, 6}, {"Mbak Dini", 30_000, 7},
		{"Pak Lingga", 25_000, 8}, {"Mbak Anisa", 25_000, 9}, {"Mas Fajar", 25_000, 10}, {"Mbak Indri", 10_000, 11},
		{"", 20_000, 12},
	} {
		s.at(thisMonth, g.day, "a baby contribution", func(st *step) error {
			_, err := st.contribute(envelope, g.name, g.amount, "")
			return err
		})
	}
}

// --- mistakes, and how they are put right without erasing anything ---

func (s *story) mistakes() {
	w := s.w

	// M-6: an expense is entered twice, and the second is reversed the next day.
	var twice store.Transaction
	s.at(-6, 18, "buying archive folders", func(st *step) error {
		_, err := st.post(ledger.DirectionOut, w.cashID, w.mainID, 46_000, "Map arsip")
		return err
	})
	s.at(-6, 18, "entering the same folders again", func(st *step) error {
		var err error
		twice, err = st.post(ledger.DirectionOut, w.cashID, w.mainID, 46_000, "Map arsip")
		return err
	})
	s.at(-6, 19, "reversing the second entry", func(st *step) error {
		note := "Tercatat dua kali"
		_, err := st.l.ReverseTransaction(st.ctx, ledger.ReverseTransactionParams{FundID: w.fundID, TransactionID: twice.ID, Note: &note})
		return err
	})

	// M-5: a dues payment is entered twice, and the second is reversed the next day.
	var dues []store.Transaction
	s.at(-5, 6, "entering Mbak Dewi's dues again", func(st *step) error {
		var err error
		dues, err = st.payDues("Mbak Dewi", w.cashID, []ledger.PeriodAmount{period(w.cal, -5, rate(tierLead, -5))}, "")
		return err
	})
	s.at(-5, 7, "reversing the repeated dues payment", func(st *step) error {
		note := "Tercatat dua kali"
		_, err := st.l.ReverseTransaction(st.ctx, ledger.ReverseTransactionParams{FundID: w.fundID, TransactionID: dues[0].ID, Note: &note})
		return err
	})

	// M-4: the printer is repaired, and the receipt is kept.
	s.at(-4, 14, "repairing the printer", func(st *step) error {
		tx, err := st.post(ledger.DirectionOut, w.cashID, w.mainID, 175_000, "Servis printer")
		if err != nil {
			return err
		}
		return attachReceipt(st.ctx, st.l, w.fundID, w.uploadsDir, &tx.ID, nil, receiptSlip{
			shop:  "Servis Komputer Maju",
			lines: []slipLine{{"Servis printer", 150_000}, {"Ganti roller kertas", 25_000}},
			total: 175_000,
		})
	})
}

// --- cek kas, at the end of every month after the first ---

// cekKas counts both locations at the end of each month. The money the
// treasurer finds equals what the ledger records (the first cek kas, at setup,
// is the one that needed a plug), so each is cocok. The end of M0 is today: the
// latest cek kas, and it matches.
func (s *story) cekKas() {
	w := s.w
	for m := firstMonth + 1; m <= thisMonth; m++ {
		s.at(m, monthEnd, "cek kas", func(st *step) error {
			counts := make([]ledger.AccountCount, 0, 2)
			for _, id := range []int64{w.cashID, w.bankID} {
				balance, err := st.l.AccountBalance(st.ctx, w.fundID, id)
				if err != nil {
					return err
				}
				counts = append(counts, ledger.AccountCount{AccountID: id, ActualAmount: balance, Resolution: ledger.ResolutionMatched})
			}
			_, err := st.l.TakeReconciliation(st.ctx, ledger.TakeReconciliationParams{FundID: w.fundID, Counts: counts})
			return err
		})
	}
}
