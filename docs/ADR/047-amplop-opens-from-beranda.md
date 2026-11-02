# ADR-047 - Amplop opens from Beranda

**Status:** `draft` · M11 ([#752](https://github.com/kerti/uruni/issues/752)) · grilled with the maintainer 2026-11-02 · supersedes in part [ADR-032](./032-two-level-navigation.md) (the [#263](https://github.com/kerti/uruni/issues/263) amendment: "opening a new envelope is Pengaturan's") · [ADR index](./README.md)

**Context.** ADR-032's #263 amendment put *Buka amplop* and the envelope list in Pengaturan, for two reasons. First, Beranda's purpose breakdown rendered nothing when no envelope was open and no Titipan existed, so a button inside it would be invisible exactly when a treasurer wants her first envelope. Second, incidental and pass-through are two kinds of one `purpose`, and Titipan was already created in Pengaturan, so one rule - *a purpose is created in Pengaturan* - covered both.

The first reason no longer holds. Since [#387](https://github.com/kerti/uruni/issues/387) Kas Utama heads Saldo per pos, so the section renders for every fund that has finished setup. The alpha.6 UX sweep (item S5) found the cost of the second: opening, watching and closing an envelope is day-to-day work, and its two doors that are not on the envelope itself - opening one, and finding a closed one - sat midway down a screen she otherwise visits rarely.

**Decision.**

**Configuration lives in Pengaturan; operations live where the money is.** The line is drawn by lifecycle, not by `purpose` kind. An amplop is opened for an occasion, collected into, paid out of and closed, usually within weeks: an operation. A Titipan has no close; once added it stays until retired: configuration. `purpose` is a schema fact the treasurer never sees, so "they are the same object" was an argument from the data model, not from her work. Accepted cost: a pass-through for a one-off relief drive is still started in Pengaturan while an amplop for the same occasion starts on Beranda; she adds a Titipan rarely enough for one trip there.

**Saldo per pos hosts both doors**, below its rows, with no section of its own:

- **Buka amplop** - an outline button, always shown. It opens the existing dialog, now addressed on Beranda (`/?edit=incidental:new`). Outline, because Beranda's one filled action stays the count.
- **Amplop yang sudah ditutup (N)** - a row, shown only when N > 0, opening `/incidentals/closed`.

A separate Amplop section was rejected because it would either pull the open rows out of Saldo per pos or list them twice, and the section's rows are what add up to the balance. A door in Catat was rejected because opening an envelope posts nothing, so it is not recording. Restoring a `/incidentals` list screen behind one Beranda row was rejected as the screen ADR-032 retired, one tap further away.

**What an amplop is is said in the Buka amplop dialog**, as its description under the title - the line Pengaturan's section used to carry. She reads it the moment it matters, and Beranda carries no explanatory text.

**Pengaturan's Amplop section is removed**, with no pointer to its new place. Beranda is the envelope's one door, so every way back - from the detail and from the closed list - leads to Beranda, and the detail's `from=home` parameter is dropped. A bare `/incidentals` redirects to Beranda. Titipan stays in Pengaturan.

**An open amplop's row shows who has given** - "N dari M sudah menyumbang" ([#746](https://github.com/kerti/uruni/issues/746)), muted, under its name, only when the envelope expects anyone - moved from Pengaturan's card so the line does not disappear with it. It informs and never chases (PRD section 4): no reminder or action rides on it. The name truncates; the amount stays on one line.

**Consequences.**

- Saldo per pos is no longer purely figures: it carries one button and sometimes one row, which pushes Arus kas and the recent five down by roughly one row's height on a phone.
- A new fund sees Buka amplop on Beranda from its first visit.
- Beranda's load already fetched the envelope list to know which are open; it now fetches all of them (one request, as before) to count the closed ones. The recipients picker's roster is fetched when the dialog opens, not with every Beranda load.
- Pengaturan's sections are all configuration again: the fund, Lokasi, Titipan, Kategori iuran, Laporan publik, the account and Cadangan.
