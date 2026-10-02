# ADR-036 — Moving money between purposes, and saying why it moved

**Status:** Accepted · `draft` · [ADR index](./README.md)

**Context.** A fund often gives from Kas Utama to an envelope: a bereavement, a neighbour in hospital, a celebration the RT pays part of. Uruni could not record that. The engine has had the operation since M3 - a `reclass_purpose` pair moves what money is *for* without moving any money - but only two callers reach it: closing an envelope (the leftover rolls into Kas Utama, [ADR-031](./031-posting-to-a-closed-incidental.md)) and correcting a posted row's pos ([ADR-033](./033-correcting-a-posted-purpose.md)), which moves a whole row and nothing smaller. `POST /api/transfers` moves money between locations and keeps its purpose. So the only way to give Rp 200.000 to an envelope was a fake expense out of Kas Utama and a fake income into the envelope - two rows that inflate both totals and put an expense on the public report that never left the fund. Grilled 2026-10-02.

## Decision

**A fifth kind in Catat: "Pindah pos".** It sits beside "Pindah lokasi", its exact counterpart: same money, different purpose rather than different place. No new screen and no new concept - pos is already the treasurer's word (it was *peruntukan* until #385). The form asks for the source purpose, the destination purpose, the amount, the date, an optional note and a location. The location is asked rather than chosen silently, on the close form's precedent ("Akun untuk pemindahan dana"): both legs post on that one account, so its balance does not move, but the row names it and the choice should be hers.

**Which moves are allowed.**
- Between any two **open** purposes: Kas Utama to an envelope, an envelope back to Kas Utama, one envelope to another.
- **Never Titipan (pass-through)**, in either direction. That money belongs to the parent body and leaves only by being forwarded; moving it into Kas Utama or an envelope would be spending money that is not the fund's.
- **A closed envelope is neither source nor target** - reopen first, the same rule ADR-031 applies to postings.
- **The source may not go below zero.** A purpose cannot give more than it holds, read from its balance (the ledger sum, rule 2) at the moment of posting.

**Why a pair moved is now recorded: `transfer.reason`.** A nullable column on `transfer`, `'roll'` or `'allocation'`, set only on a `reclass_purpose` pair that corrects nothing. Until now a reclass pair without a correction link had one possible origin, so Riwayat could label it "Tutup amplop" by elimination (ADR-033 made the same observation about corrections). An allocation breaks that: without the column, a move from an envelope back to Kas Utama is indistinguishable from a roll. ADR-033 refused a third `transfer.kind` because `kind` answers *what shape* of move a pair is; this is a separate column precisely so `kind` keeps answering only that. `NULL` means the row predates this ADR - and every such uncorrected reclass pair is a roll, because nothing else could post one - so labels read `NULL` as a roll and stay exact for history.

Labels, built at display time (#257): a correction stays "Perbaikan pos"; `'allocation'` reads "Pindah pos · <from> -> <to>"; a roll (`'roll'` or `NULL`) stays "Tutup amplop". The public report already folds any reclass pair into one move row (ADR-035) and carries the reason alongside.

**No `format_version` bump.** The backup gains `reason` on each transfer. The importer rejects unknown fields but reads a missing one as empty, so a backup made before this change restores cleanly, its rolls reading `NULL` - which is what they are. A backup made after it does not restore on an older server; that direction was never promised before v1.0.0 ([ADR-012](./012-backup-and-export.md), [#330](https://github.com/kerti/uruni/issues/330)). The version dance waits for the upgrade chain.

## Consequences

- `00001_schema.sql` changes (ADR-025), so every live instance needs the schema brought forward before it runs this code. The route is the backup itself: download a backup, start the new image on an empty database, register, run setup, restore - the importer carries the data across and the new column reads `NULL`. No hand-copied rows.
- `POST /api/purpose-moves` is the route. It is the third caller of `postTransferPairTx` and adds no second write path (ADR-027).
- The source-balance check reads the balance at posting time, not as of the move's date: a backdated move can still leave a purpose briefly negative on an earlier day, the same tolerance every other backdated posting has.
- Reconciliation is untouched: both legs sit on one account and net to zero there.
