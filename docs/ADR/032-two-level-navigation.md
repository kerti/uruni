# ADR-032 — Two-level navigation: five noun slots, one privileged verb

**Status:** Accepted · `draft` · [ADR index](./README.md)

**Context.** [#212](https://github.com/kerti/uruni/issues/212) was filed because home had become the place new features land: reimbursements ([#151](https://github.com/kerti/uruni/issues/151)) and incidentals ([#152](https://github.com/kerti/uruni/issues/152)) each arrived as another button under the balance hero, and receipts ([#153](https://github.com/kerti/uruni/issues/153)/[#154](https://github.com/kerti/uruni/issues/154)) plus M7's report were queued to arrive the same way. The issue names the symptom. Grilled 2026-09-10, the cause turned out to be larger than the symptom, in two ways.

**First, the footer bar is incoherent.** M6.15 gave the app five destinations — Beranda, Catat, Iuran, Anggota, Pengaturan — and four of them are nouns while one is a verb. That mix is why a new feature has no obvious slot: a receipt is a noun, exporting is a verb, and neither has a home. The issue's own proposal doubled down, mirroring four domains as history tabs under Beranda *and* as input tabs under Catat — eight destinations for four concepts, and no rule the treasurer could learn for which held what.

**Second, and worse: four features required by the PRD have no entry point anywhere in the SPA**, and two more (M7, M8) are queued behind the same gap.

| PRD | Missing surface | Server side |
|---|---|---|
| §6 Account/Location | transfer between locations | `POST /api/transfers` exists ([#213](https://github.com/kerti/uruni/issues/213)) |
| §7.1 | opening balance for a location added after setup | `POST /api/accounts/{id}/opening-balance` exists |
| §7.7 | breakdown by purpose tag | `GET /api/balances` already returns `purposes[]`; `Home.tsx` uses it only as a name lookup |
| §7.8 | reconciliation snapshot history | `GET /api/reconciliations`, `/{id}` exist; no client function at all |
| §7.3 | dues payment history | **nothing** — only `/api/dues-status` |
| §7.9, §7.10 | report link, export | M7 and M8 |

So this is not a tidying pass on one screen. It is the information architecture the rest of `0.x` hangs off, and [ADR-031](./031-posting-to-a-closed-incidental.md) already deferred to it in writing: a purpose that goes negative *"depends on the negative balance being visible, and today it is not... Where they belong is the information-architecture question in #212."*

## Decision

**Five footer slots hold nouns, with exactly one privileged verb. Tabs exist in exactly one place. Every non-posting edit is a dialog.**

### The bar

`Beranda · Riwayat · Catat · Anggota · Pengaturan`

**Catat is the only verb, and it earns that** from PRD §7.2, which requires *"a prominent add action reachable in one tap."* Every other create or edit is reached from the noun it belongs to. One rule, and it is short enough to be learned by accident: **Catat records money moving; everything else is edited where it lives.**

**Iuran loses its slot** and becomes a tab under Riwayat, its status matrix at the top of that tab. This is the cost of coherence and it was taken deliberately: dues status is one reading of one month, and it was occupying a fifth of the app's navigation.

**Catat renders as a raised centre action** — a Forest pill jutting above the bar, larger icon, label kept. This does not revive the add-FAB that M6.15 removed: that FAB was removed because *"a screen may not have two entry points"*, and a loud tab is still one door. Forest rather than the Sage accent, because Design-System.md makes Forest the action colour and white on Sage does not clear AA; it shares a hue with the balance hero, which `Home.tsx` already accepts and separates by shape and elevation.

**One consequence must be paid in the same PR.** The footer's active state is currently Forest-versus-muted, so a permanently-Forest Catat would read as "you are here" on every screen. Catat needs a distinct active treatment, or the other four need an active marker that is not colour alone.

**Slot order is convention, not reach.** A right-handed thumb reaches the right of the bar most easily, which argues for putting Anggota and Pengaturan — opened monthly — on the left and Riwayat on the right. Rejected: Beranda not being first is more confusing than a marginal reach win is worth, and `Shell.tsx` already argues the header's fund-name link is not a substitute for a reachable Beranda tab. The raised centre Catat takes the single best spot for the action she performs most, which is where the reach budget is actually spent.

### The second level

**Tabs when every panel is an unbounded list. Sections when the panels are short and read together.** Applied, that puts tabs in exactly one place:

- **Riwayat** — tabs: `Transaksi · Iuran · Penggantian · Cek kas`. Four unbounded lists, one visible at a time, each paged and searchable on its own.
- **Beranda** — no second level. It is the calm screen; it gets nothing to choose.
- **Anggota** — no second level. The roster only.
- **Pengaturan** — sections, as today.

**Tabs are routes, never component state.** `/riwayat/iuran` through an `<Outlet/>`. `Shell.tsx` already states the reason for the footer and it holds one level down: *"a deep link and a back button must both land on the right tab."* This is also why shadcn's `tabs` component is not adopted — it would invite `useState` and quietly break both.

**Cek kas is read-only, and must stay so.** It lists past snapshots and opens them. It carries no "start a reconciliation" control: M6.10's ruling is that the banner on Beranda *is* that flow's affordance, and a button here would be the second door that ruling refused. Listing snapshots is a different verb against a different object, which is why the tab itself is not a violation.

### Where the homeless features land

**Incidentals are not history — they are a live balance, so they go on Beranda.** PRD §7.7's unbuilt *"optional breakdown by purpose tag"* becomes the entry point: each open incidental and each pass-through purpose (**Titipan**, the label `copy` already uses) renders as a row with its balance, and tapping it opens that envelope. This builds §7.7, gives incidentals a home matching what they are, satisfies ADR-031's deferred requirement in the same stroke, and removes a button from Beranda rather than adding one. A **closed** envelope drops off Beranda and is reachable through Riwayat → Transaksi filtered to its purpose — the right asymmetry: open envelopes are current business, closed ones are the record.

Beranda's order becomes: hero, per-location rows, reconciliation banner, **purpose breakdown**, recent five.

**Beranda keeps the recent-five peek**, with a link into Riwayat → Transaksi. Not duplication: it is the evidence for the post-record confirmation. `App.tsx`'s `handleRecorded` navigates to `/` and Home refetches precisely so *"a new entry is visible in recent activity without a manual refresh"*, and moving the list wholesale would end every recording on a screen that cannot show it worked.

**Transfer between locations becomes a third direction in the record form** — `Uang masuk` / `Uang keluar` / **`Pindah lokasi`**. Choosing it swaps the single location field for from/to and hides purpose, which a transfer does not have. The label is new copy: "Uang pindah" reads broken beside the two that exist, and "Transfer" is the English identifier [`CONTEXT.md`](../../CONTEXT.md) reserves for the row pair, not a label. `Pindah lokasi` names what is actually different — the money is neither entering nor leaving, it is changing place. Rejected: a separate route off Catat, which turns Catat into a menu and forfeits §7.2's one tap; and a dialog from Beranda's location rows, which posts to the ledger and so breaks this ADR's own dialog rule.

**Tier management leaves Anggota for Pengaturan.** This supersedes the placement in M6.16/M6.17, whose stated reason was that *"a member's tier is set on the member, so the two are read and edited in one sitting."* That premise stops holding here, because the roster row now **displays** the tier's effect (below) — the two are read together on the roster itself and written together only rarely. Anggota gets its full height for one long, searchable, paged list; Pengaturan gains a genuinely settings-shaped thing; and the app keeps exactly one screen where tabs mean anything.

### The roster row, and a word that does not exist yet

The member row carries **name, tier name, current rate, and an arrears badge**. The first three are period-independent and come free from calls Anggota already makes.

The badge is new, and its definition is the load-bearing part. It reports arrears **strictly before the current period**:

| Arrears | Row reads |
|---|---|
| none owed before this period — including paid-ahead, and a member who joined this period | **no badge**, a quiet row |
| any period before this one not fully paid | **`Tunggakan N bulan`**, N counted in whole periods |

A part-paid earlier period counts as one month of tunggakan, the same as an unpaid one. The badge is a triage signal and the member's own screen carries the detail; a second state to distinguish them would add a word to say something already said one tap away.

**Two rejected alternatives, both of which were proposed and both of which fail.**

*Current-period dues status on the row* — rejected. `getDuesStatus` is period-scoped, so this drags a period selector into Anggota, at which point Anggota contains the Iuran matrix and the treasurer has no rule for which screen answers "sudah bayar belum?" — the duplication this whole ADR exists to remove.

*A tri-state including the current period* ("paid everything up to and including now") — rejected for a sharper reason. On the first of every month the entire roster would flip to unpaid at once, and for the first weeks of every month the badge could not distinguish a member who simply has not paid yet — normal, benign, expected — from one six periods behind, which is the only thing the badge is worth having for. The signal drowns in noise every month, worst exactly when she is chasing collections. It also has no room for PRD §7.3's *"paid in advance."*

**The word is `tunggakan`, and it is not a new one.** PRD-ID §7.1 already says *"**tunggakan** yang masih nyata"* and `copy.dues` already says *"iuran tertunggak"*; this ADR adopts that word rather than coining one, per [`CONTEXT.md`](../../CONTEXT.md)'s standing rule that a concept gets one word and no synonyms.

**What it must not reuse is the period vocabulary**, and an earlier draft of this ADR did. `copy.dues.status` ships `Belum bayar` / `Bayar sebagian` / `Lunas` / `Lunas — sudah bayar di muka` for **the current period**, and PRD §7.3 defines exactly those three states. Spending `Lunas` again on a cumulative meaning would put two disagreeing readings of the same word on screen at once — the Anggota badge and the Iuran matrix — and a treasurer who catches them disagreeing trusts neither. Hence a badge that is either absent or a count: it borrows no word the period states have claimed.

**One source of truth for what is owed.** The list field computes through the same ledger function `GET /api/members/{id}/outstanding-dues` uses, never a second SQL expression. Two implementations of a member's debt will eventually disagree, and this is trust-core territory (`CLAUDE.md` rule 2). Reuse also inherits PRD §7.1 for free: history starts at adoption, no pre-adoption period is generated as owed, backdating a join date is the only way one appears, and a new member therefore carries no badge on day one.

### Editing surfaces

**A dialog edits one row and posts no ledger entry. Anything that moves money is a route.**

| Dialog | Route |
|---|---|
| rename fund · add/rename/retire location · add/edit pass-through purpose · add/edit member · add/edit tier · add/edit rate · open an incidental | record transaction · transfer · dues payment · reimbursement claim · reconcile count |

Money forms stay screens because they carry five or more fields, a photo picker ([#154](https://github.com/kerti/uruni/issues/154)), smart defaults and a success handoff — and because a posting dismissible by a stray backdrop tap is the wrong shape for the one thing this app must not get wrong. The effect on the admin screens is the point: with inline forms gone, Anggota and Pengaturan become card lists, which is what keeps them short enough that sections beat tabs.

**An open dialog is a search parameter** — `/pengaturan?ubah=lokasi:3`. Android's back gesture then closes the dialog instead of leaving the screen, the state is deep-linkable, and no component holds modal state. Same argument as tabs-are-routes, one level further down.

**Bottom sheet, not centred modal**: `max-h-[85dvh]` (`dvh`, not `vh`, for iOS toolbars), internal scroll, `env(safe-area-inset-bottom)` padding. A centred modal with a phone keyboard open puts the field under the thumb row.

**One dependency, not two.** `@radix-ui/react-dialog` arrives with shadcn `dialog` and is worth it for focus trap, focus restore and background inerting, all easy to get subtly wrong by hand. `vaul` is **not** added; the Radix dialog is styled as a sheet. Rule 7, fewest moving parts.

**No nested dialogs, ever.** Deleting a member from inside an edit dialog confirms inline within that dialog; a second layer over the first is unescapable on a phone. Never `window.confirm()`. Destructive copy names the consequence, in terracotta and never alarm-red — a retired location and a deleted one are different things (PRD §6). Every card's edit affordance clears 44px; the `sm` button variant does not.

**One named exception, stated rather than hidden.** The add-location dialog carries an **optional opening balance**, which is a ledger posting inside a dialog. PRD §7.1 gives every location an opening balance but the setup wizard only reaches the ones existing at setup, so a bank account added in March currently has no way to state what is in it. An opening balance is a property of a location's birth, not a transaction she would ever go looking for; it happens once per location and never again, and the field is hidden entirely when editing an existing location. The alternative — a `Saldo awal` route visited once and never found again — is worse.

### Lists: paging and search

**Load more, keyset cursor, 25 a page, newest first.**

- **Load-more over infinite scroll.** With a fixed footer, infinite scroll puts the bar over the thing being reached for, loses scroll position on back-navigation, and has no bottom — the treasurer scanning for one entry never learns whether she has seen everything. A button is a touch target under our control.
- **Keyset over `LIMIT`/`OFFSET`.** Dates are backdatable (§7.2 defaults to today, editable), so a row can land in the middle of a newest-first list between two fetches; offset then silently skips or duplicates. `(occurred_on DESC, id DESC)` cannot. Equal difficulty in SQLite, and rule 7 asks for the best SQLite SQL rather than the portable subset.
- **Newest-first is a change.** `GET /api/transactions` answers oldest-first today and `Home.tsx` compensates with `.slice(-5).reverse()`; server-side ordering retires that.

**Search is a server parameter, `LIKE` with `NOCASE`, no FTS5.**

**Client-side filtering of a paged list is prohibited outright.** Filtering the 25 rows that happen to be loaded looks like it works and quietly lies: she searches "Budi", sees two rows, and concludes there are two. If a list is paged, its search goes to the server.

FTS5 is rejected for scale, not ignorance: a virtual table, sync triggers and a second copy of the text, to beat `LIKE` across a few thousand rows. An RT of 50 members generates roughly 600 dues rows and a few hundred transactions a year.

| List | Search over |
|---|---|
| Anggota | member name |
| Transaksi | note, purpose name, member name, and exact amount when the input is all digits |
| Iuran | member name |
| Penggantian | member name, note |
| Cek kas | none — a handful of dated snapshots a year |

`?q=` lives in the URL for the same reason tabs do. Input is debounced, and a keystroke without signal shows the offline state honestly rather than reading as "no results" — §7.2, connection required.

**Filters are held back to M7.** PRD §7.9 specifies month, purpose, member, income/expense and dues status **for the public report**. Riwayat gets search and a period filter, nothing more. Porting the full filter set into the private app builds M7's screen twice, which is exactly the creep `CLAUDE.md`'s prime directive names.

## Consequences

**Every PRD feature now has exactly one address**, including the six that had none. Transfer and opening balance reach the ledger through Catat and the location dialog; the purpose breakdown and reconciliation history become Beranda and Riwayat; dues payment history gets the endpoint it never had; M7's report link and M8's export are Pengaturan sections, decided before either is built rather than after.

**Three server-side additions**, none of them in the ledger's arithmetic: keyset paging plus search on four list endpoints, a `GET /api/dues-payments` that does not exist, and an arrears field on the member list computed through the existing outstanding-dues path. `GET /api/transactions` changing to newest-first is a breaking change to an endpoint only this SPA consumes.

**One documented placement is superseded** — M6.16/M6.17 putting tiers with the roster — and its reasoning is answered above rather than dropped. M6.10's single-door ruling and M6.15's footer both survive intact.

**The bar is now full.** Five slots, and the rule that fills them is a rule about nouns, so the next feature does not get a slot: it gets an address inside one. That is the whole point, and it will feel like a constraint the first time something does not obviously fit. It should.

**Tabs exist in one place, which is what makes them mean something.** The risk is the opposite of the one this ADR started from: a future screen wanting "just two tabs" because tabs now exist in the codebase. The rule — unbounded lists only — is the defence, and Anggota is the worked example of refusing them.

**This is more than one slice**, and must not be built as one PR.
