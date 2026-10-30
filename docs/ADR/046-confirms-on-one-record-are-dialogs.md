# ADR-046 - A confirm on one record is a dialog, even when it posts

**Status:** Accepted · implemented in M11 ([#744](https://github.com/kerti/uruni/issues/744)) - drafted and approved by the maintainer 2026-10-30 · change only by adding a superseding ADR · supersedes in part [ADR-032](./032-two-level-navigation.md) (the dialog rule) and [ADR-042](./042-reversing-any-ordinary-transaction.md) (the entry point), each clause named at the foot · [ADR index](./README.md)

**Context.** The alpha.6 UX sweep (2026-10-29, item P2) counted six ways to confirm or edit one record. Batalkan transaksi closed the entry detail and opened an inline panel under the row. Batalkan iuran was another inline panel. Tutup amplop was an inline panel on the same screen whose Ubah amplop is a dialog. Hapus talangan was a terse inline "Hapus?". Bayar talangan was a full screen for one picker and a date. Each shape was right by the rule that produced it: ADR-032 keeps every posting out of dialogs ("anything that moves money is a route", narrowed by #276 to *value-neutral, single field*), and ADR-042 rejected the reversal form inside the detail dialog by name for that reason. The result, on a phone, is a treasurer who cannot predict what a tap will open.

The rule's own reason is one sentence: *"a posting dismissible by a stray backdrop tap is the wrong shape for the one thing this app must not get wrong."* That names a failure of the dismissal, not of the container. The money forms it protects (Catat, Catat iuran) carry an amount she types, five or more fields, a photo picker and a success handoff; the confirms above carry none of that.

**Decision.**

**A confirm or a short edit acting on one existing record is a dialog, whether or not it posts, when the record fixes the amount.** She types no money figure in it: the amount is the record's (a claim's, a transaction's, an envelope's remainder). Today that is:

- **Batalkan** - a transaction from the entry detail (Riwayat, Beranda, the envelope detail) and a dues payment from a member's payments. The detail still posts nothing: its button closes it and opens the reversal dialog, so no dialog sits over another.
- **Tutup amplop** - Lokasi, date and note; the roll-over amount is the envelope's.
- **Bayar talangan** - Bayar dari and date, above the claim it pays (who, which pos, when, note, amount).
- **Hapus talangan** - a plain destructive confirm. It never posted; it moves only so one list has one shape.

**A dialog that posts ignores a tap outside it.** It closes only by its X, Escape, or its own buttons, each a deliberate act; a stray tap on the backdrop does nothing. Escape stays, since it is how a keyboard leaves any dialog and is never a stray. A dialog that posts nothing keeps today's outside-tap dismissal.

**Still routes:** every form where she types an amount or builds a record from nothing - Catat (all of its variants), Catat iuran, Ubah talangan - and every multi-section editor (Kategori). ADR-032's remaining tests stand: no unbounded list in a dialog, no dialog that switches modes, no nested dialogs, an open dialog is a search parameter (`?edit=reverse:<id>`, `?edit=settle:<id>`, `?edit=close:<id>`, `?edit=delete-claim:<id>`), so Android's back gesture closes it.

**Addresses.** `/reimbursements/settle?id=<id>` stays as a redirect to the Talangan tab with the dialog open, so a bookmark or an old link still lands on the claim.

**Consequences.**

- The same tap opens the same shape everywhere: a dialog for one record, a screen for a form.
- A reversal is no longer scrolled into view under a row the detail had covered; the dialog is centred where the keyboard leaves room (ADR-032's `visualViewport` rule).
- Copy (approved with this ADR): the Talangan menu's *Perbaiki* becomes *Ubah* (*Ubah talangan*), so *Perbaiki* means only *Perbaiki pos*. *Hapus* stays for a claim: deleting it removes it, where *Batalkan* leaves a visible reversing entry.
- The e2e journeys that drove the inline panels and the settle screen are rewritten against the dialogs.

**Rejected.**

- **Keep postings as routes and move only Hapus talangan.** Leaves three of the four shapes P2 counted, for a fear the backdrop rule answers directly.
- **A bottom sheet** (the sweep's alternative). ADR-032 already tried and dropped it after an iPhone review; the centred dialog stays.
- **Bring the money-entry forms into dialogs too.** They fail the test here on its own terms: she types the amount.

**Superseded, by clause.**

- **In ADR-032:** "A dialog edits one row and posts no ledger entry. Anything that moves money is a route", and #276's narrowing to "value-neutral and carries a single field", give way to the rule above for confirms on one record. The add-location opening balance stays its named exception. Everything else in the dialog section stands.
- **In ADR-042:** the entry point's hand-off "to the inline form" under the row becomes a hand-off to the reversal dialog, and the rejected alternative "The form inside the detail dialog" stays rejected (the form is a dialog of its own, opened after the detail closes). Its copy, date line and eligibility (`reversible`, `counted`) are unchanged.
