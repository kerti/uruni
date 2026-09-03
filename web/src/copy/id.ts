// Every user-facing string lives here: Bahasa Indonesia, sentence case, warm
// (ADR-014). Centralized from the first screen so a second language stays
// additive — no copy inline in components.
export const copy = {
  app: {
    name: 'uruni',
    tagline: 'Kas bersama yang selalu cocok.',
  },
  // The app shell every authenticated screen renders inside (M6.6): the
  // header's logout control and, as of M6.15, the sticky footer nav. The
  // header's heading is the fund's own name, which comes from the server,
  // not from here.
  shell: {
    // The sticky footer's four destinations (M6.15). Single words, because
    // a tab label that wraps on a small phone is a tab label that is too
    // long - and because these are landmarks, not sentences.
    nav: {
      // Names the <nav> itself for a screen reader, which otherwise
      // announces an unlabelled navigation landmark.
      label: 'Navigasi utama',
      home: 'Beranda',
      record: 'Catat',
      dues: 'Iuran',
      members: 'Anggota',
      settings: 'Pengaturan',
    },
    logout: 'Keluar',
    loggingOut: 'Sedang keluar…',
  },
  common: {
    loading: 'Memuat…',
    offlineBanner: 'Belum tersambung — Uruni butuh koneksi.',
    retry: 'Coba lagi',
    // Shown only when a new build is already downloaded and waiting (M6.7).
    // Never phrased as an alarm - nothing is broken, and nothing reloads
    // until she taps.
    updateAvailable: 'Versi baru sudah siap.',
    updateReload: 'Muat ulang',
    // Wire error codes -> Indonesian copy. The server's message field stays
    // English by design (ADR-014: the API is a code surface) and never
    // reaches the treasurer; this map is what she sees instead.
    //
    // Seeded with the codes the shared states and M6.4's auth routes can hit
    // (not_found, method_not_allowed, a network failure, plus register/login's
    // invalid_argument, already_registered, invalid_credentials and
    // too_many_requests). Each later slice adds its own route's codes as it
    // lands. invalid_credentials and too_many_requests also get their own,
    // more specific auth.login copy below - the shared strings here are the
    // ErrorState fallback for the same codes reached from elsewhere.
    errors: {
      not_found: 'Data yang dicari tidak ditemukan.',
      method_not_allowed: 'Aksi ini tidak didukung.',
      network_error: 'Belum tersambung — Uruni butuh koneksi.',
      invalid_argument: 'Ada isian yang belum sesuai. Coba periksa lagi.',
      already_registered: 'Akun bendahara untuk Uruni ini sudah pernah dibuat.',
      invalid_credentials: 'Email atau kata sandi salah.',
      too_many_requests: 'Terlalu banyak percobaan. Coba lagi beberapa menit lagi, ya.',
      // M6.5's setup wizard: a second POST /api/setup, a second opening
      // balance for the same location, and a malformed request body.
      fund_already_exists: 'Kas ini sudah pernah disiapkan.',
      opening_balance_exists: 'Saldo awal untuk lokasi ini sudah pernah dicatat.',
      invalid_json: 'Ada yang tidak beres saat mengirim data. Coba lagi.',
      // M6.15's settings screen: deleting a location (or later a member or
      // tier) that something already points at. The locations section
      // renders its own, more specific wording pointing at deactivate; this
      // is the shared fallback for the same code reached elsewhere.
      referenced_by_other_records: 'Data ini sudah dipakai di catatan lain, jadi tidak bisa dihapus.',
      // Renaming a purpose that is not a titipan - the fund's own kas utama,
      // or an incidental, whose name is the occasion itself.
      purpose_not_renameable: 'Hanya nama titipan yang bisa diganti.',
      // M6.17: two tiers with the same name (UNIQUE (fund_id, name)), or two
      // rates for the same tier and month (UNIQUE (tier_id, effective_from)).
      // errors.go maps every UNIQUE breach to this one code.
      unique_violation: 'Sudah ada yang sama. Coba nama atau bulan yang lain.',
    },
    // Shown for a code not in the map above.
    unknownError: 'Ada yang tidak beres. Coba lagi sebentar lagi.',
  },
  auth: {
    register: {
      heading: 'Buat akun bendahara',
      body: 'Ini akun pertama untuk Uruni — sekali dibuat, akun ini yang menjaga kas bersama.',
      emailLabel: 'Email',
      passwordLabel: 'Kata sandi',
      submit: 'Buat akun',
      submitting: 'Membuat akun…',
      passwordTooShort: 'Kata sandi minimal 8 karakter, ya.',
    },
    login: {
      heading: 'Masuk ke Uruni',
      body: 'Masukkan email dan kata sandi bendahara untuk melanjutkan.',
      emailLabel: 'Email',
      passwordLabel: 'Kata sandi',
      submit: 'Masuk',
      submitting: 'Sedang masuk…',
      invalidCredentials: 'Email atau kata sandi salah. Coba periksa lagi.',
      tooManyRequests: 'Terlalu banyak percobaan masuk. Coba lagi beberapa menit lagi, ya.',
    },
  },
  // The first-run setup wizard (M6.5, PRD §7.1): four steps, only the first
  // two (fund name, at least one location) mandatory - balances and roster
  // are both openly optional per-step, not gated behind a forced review.
  setup: {
    stepLabel: (step: number) => `Langkah ${step} dari 4`,
    back: 'Kembali',
    next: 'Lanjut',
    submitting: 'Menyimpan…',
    fund: {
      heading: 'Beri nama kas ini',
      body: 'Nama ini akan muncul di laporan publik dan di seluruh aplikasi — bisa diganti nanti kalau perlu.',
      nameLabel: 'Nama kas',
    },
    locations: {
      heading: 'Pilih tempat kas disimpan',
      body: 'Setiap tempat penyimpanan uang — tunai atau rekening bank — dicatat sebagai satu lokasi. Boleh diganti namanya, boleh ditambah.',
      kindLabel: 'Jenis',
      kindCash: 'Tunai',
      kindBank: 'Bank',
      nameLabel: 'Nama lokasi',
      addRow: 'Tambah lokasi',
      removeRow: 'Hapus lokasi',
      minOneLocation: 'Minimal satu lokasi diperlukan.',
    },
    balances: {
      heading: 'Isi saldo awal (opsional)',
      body: 'Kosongkan kalau lokasi ini belum punya saldo untuk dicatat sekarang — bisa ditambah nanti lewat menu lokasi.',
      amountLabel: (accountName: string) => `Saldo awal — ${accountName}`,
      // The note stored on the opening-balance transaction itself, not a form
      // label — it outlives the wizard and shows up in home's recent activity
      // and in the public report, where the location column may not be there
      // to explain it. Same words as amountLabel today, kept separate because
      // one is a screen string and the other is ledger data.
      note: (accountName: string) => `Saldo awal — ${accountName}`,
    },
    roster: {
      heading: 'Tambah anggota dan iuran (opsional)',
      body: 'Bisa dilewati dan diisi nanti kapan saja lewat menu anggota.',
      tierNameLabel: 'Nama golongan iuran',
      rateAmountLabel: 'Besar iuran per bulan',
      memberNameLabel: 'Nama anggota',
      addMember: 'Tambah anggota',
      removeMember: 'Hapus anggota',
      skip: 'Lewati',
      finish: 'Selesai',
    },
  },
  // The everyday record-transaction form (M6.8, PRD §7.2): amount, in/out,
  // location, purpose, date, optional note. Reached from the footer nav's
  // "Catat" tab, which replaced the add-FAB in M6.15.
  // successIn/successOut are shown on home after a successful post -
  // worded per Design-System.md's own voice examples for each direction.
  record: {
    heading: 'Catat transaksi',
    directionLabel: 'Jenis',
    directionIn: 'Uang masuk',
    directionOut: 'Uang keluar',
    amountLabel: 'Jumlah',
    locationLabel: 'Lokasi',
    purposeLabel: 'Peruntukan',
    dateLabel: 'Tanggal',
    noteLabel: 'Catatan (opsional)',
    submit: 'Simpan',
    submitting: 'Menyimpan…',
    // Installed to a home screen there is no browser back button, so leaving
    // the form without recording needs its own way out.
    cancel: 'Batal',
    successIn: 'Pemasukan berhasil dicatat.',
    successOut: 'Pengeluaran berhasil dicatat.',
  },
  // The home screen (M6.9, PRD §7.7): balance hero, per-location balances,
  // reconciliation status and recent activity - the everyday-loop landing
  // page reached at "/" once a fund exists (App.tsx's AuthedGate).
  home: {
    balanceHeading: 'Saldo kas',
    locationsHeading: 'Saldo per lokasi',
    recentActivityHeading: 'Aktivitas terbaru',
    recentActivityEmpty: 'Belum ada transaksi tercatat.',
    // A purpose the balances response didn't name - it should not happen
    // (both come from the same fund), so this is a placeholder that keeps a
    // row readable rather than a state with meaning of its own.
    purposeUnknown: 'Tanpa tujuan',
    // date is already formatted (Intl.DateTimeFormat) by the caller.
    lastChecked: (date: string) => `Terakhir dicek ${date}`,
  },
  // ReconciliationBanner's own copy (M6.9): "cocok" when GET
  // /api/reconciliations/open-lines comes back empty, "selisih" otherwise.
  // Modeled on Design-System.md:106-107's voice examples. Extended in place
  // for M6.10's reconcile screen (PRD §7.8) - one namespace throughout, per
  // the orchestrator's own ruling, not a second copy.reconcile block: this
  // screen is the same reconciliation concept the banner already names,
  // just doing something about it. matched/discrepancy above are reused
  // verbatim on the confirmation screen, not restated here.
  reconciliation: {
    // The first-run state, before any count has ever been taken: neutral on
    // purpose. Uruni has nothing to compare its ledger against yet, so it
    // says so and invites the count instead of claiming a match.
    neverChecked: 'Belum pernah dicek — hitung uangnya kapan saja.',
    matched: 'Kas dan catatan sudah cocok.',
    // amount is already formatted (formatIDR) by the caller - never a raw
    // number crosses into copy.
    discrepancy: (amount: string) => `Ada selisih ${amount} — mau dicek bareng?`,
    heading: 'Cek kas',
    intro: 'Hitung uang yang benar-benar ada di setiap lokasi, lalu bandingkan dengan catatan.',
    recordedLabel: 'Menurut catatan',
    // accountName is the location's own name (e.g. "Tunai") - same
    // per-field labelling AmountInput's callers already do.
    actualLabel: (accountName: string) => `Jumlah sebenarnya — ${accountName}`,
    resolutionLabel: 'Bagaimana selisih ini diselesaikan?',
    // Keyed by the schema's own resolution string (reconciliation.go's own
    // four values) so both the choice buttons and the confirmation list's
    // per-line label can read from one map. "matched" is never a button -
    // a zero-gap line needs no choice - but still gets a label for the
    // confirmation list.
    resolutionOptions: {
      matched: 'Cocok',
      entry_added: 'Ada transaksi yang belum tercatat',
      adjusted: 'Sesuaikan saldo (koreksi)',
      left_open: 'Simpan dulu, selesaikan nanti',
    },
    // The fix fields (PRD §7.8's "add the missing transaction or post a
    // noted adjustment"). Direction/amount reuse copy.record's own
    // direction labels (Uang masuk/Uang keluar) - a generic in/out toggle,
    // not specific to the record screen.
    fixPurposeLabel: 'Peruntukan',
    fixAmountLabel: 'Jumlah',
    fixDateLabel: 'Tanggal',
    fixNoteLabel: 'Catatan (opsional)',
    submit: 'Simpan hasil cek',
    submitting: 'Menyimpan…',
    // Same reasoning as record.cancel: installed standalone, there is no
    // browser back button.
    cancel: 'Batal',
    // Shown when POST /api/reconciliations rejects a "matched" line because
    // its real difference is no longer zero - a transaction landed between
    // the count and the submit (reconciliation.go:185-188). Never phrased
    // as an error or a loss: the numbers were refreshed, nothing was
    // discarded, she just needs to look again.
    staleNotice: 'Ada transaksi baru sejak kamu mulai menghitung tadi. Angkanya sudah diperbarui — yuk, cek selisihnya sekali lagi.',
    backToHome: 'Kembali ke beranda',
  },
  // The dues status roster (M6.12, PRD §7.3): "view members and, for the
  // current period, who has paid / partially paid / paid in advance." A
  // period-scoped read screen only - the "belum bayar" filter is a plain
  // list, never a reminder or nudge (PRD §7.3's own explicit rule), so this
  // namespace deliberately carries no send/notify/chase copy at all.
  dues: {
    entryLink: 'Lihat status iuran',
    heading: 'Status iuran',
    periodLabel: 'Periode',
    // Isolates unpaid + partial rows. Partial is included on purpose: she
    // paid something but still owes the rest, so "belum bayar" (not yet
    // paid *in full*) is still true of that row - only "paid" and
    // "paid_in_advance" are actually settled for the period.
    unpaidFilterLabel: 'Tampilkan yang belum bayar saja',
    owedLabel: 'Iuran',
    paidLabel: 'Dibayar',
    empty: 'Belum ada anggota dengan iuran untuk periode ini.',
    emptyFiltered: 'Semua anggota sudah bayar untuk periode ini.',
    // Keyed by the schema's own four status values (internal/ledger's
    // MemberDuesStatus) so the badge and any other lookup share one map -
    // same pattern as reconciliation.resolutionOptions above.
    statuses: {
      unpaid: 'Belum bayar',
      partial: 'Bayar sebagian',
      paid: 'Lunas',
      paid_in_advance: 'Lunas — sudah bayar di muka',
    },
    back: 'Kembali ke beranda',
    // Recording a dues payment (M6.13, PRD §7.3). Reached from the status
    // roster above, not from a second link on home - navigation as a whole
    // is settled once alpha.4's screens exist (#177).
    recordLink: 'Catat pembayaran',
    payment: {
      heading: 'Catat pembayaran iuran',
      memberLabel: 'Anggota',
      memberPlaceholder: 'Pilih anggota',
      // The periods this member still owes for, oldest first, each with the
      // rate that was in effect for that month already filled in.
      periodsHeading: 'Periode yang dibayar',
      noMemberYet: 'Pilih anggota dulu untuk melihat iuran yang belum dibayar.',
      // A normal, good answer - never phrased as an error or an empty
      // failure: this member is square.
      noOutstanding: 'Tidak ada iuran tertunggak untuk anggota ini.',
      // Shown on a period that has been paid in part, so the pre-filled
      // amount is the sisa (the rest), not the whole month's rate.
      remainingLabel: 'Sisa',
      // One amount field per period, so each label names its own month -
      // same shape as reconciliation.actualLabel per location.
      amountLabel: (period: string) => `Jumlah — ${period}`,
      totalLabel: 'Total dibayar',
      locationLabel: 'Lokasi',
      dateLabel: 'Tanggal',
      // The note every posted row carries, so a dues payment reads as one
      // in recent activity and in the report instead of as a bare amount -
      // same shape and same reasoning as setup.openingBalance.note (#178).
      // The month is not repeated here: each row already carries its own
      // dues_period, and one request's note is shared by every period it
      // pays.
      note: (memberName: string) => `Iuran — ${memberName}`,
      submit: 'Simpan pembayaran',
      submitting: 'Menyimpan…',
      // Same reasoning as record.cancel: installed standalone, there is no
      // browser back button.
      cancel: 'Batal',
      success: 'Pembayaran iuran tercatat.',
    },
    // Undoing a dues payment recorded in error (M6.14, PRD §7.3). A
    // payment is never edited away: the reversal is its own new entry, so
    // this copy says "batalkan" (undo it with another entry), never
    // "hapus".
    history: {
      // One stable label on a disclosure, not a show/hide copy swap: the
      // open/closed state is carried by aria-expanded and the chevron, and
      // a control whose name changes under the reader's cursor is the
      // harder thing to follow.
      title: 'Pembayaran',
      empty: 'Belum ada pembayaran untuk periode ini.',
      // The reversal row itself, listed alongside the payment it undoes.
      reversalRow: 'Pembatalan',
      reversedBadge: 'Sudah dibatalkan',
      reverse: 'Batalkan',
      dateLabel: 'Tanggal pembatalan',
      noteLabel: 'Alasan (opsional)',
      // Used when she leaves the reason blank - a row still has to say what
      // it is, same rule as payment.note above.
      note: (memberName: string) => `Pembatalan iuran — ${memberName}`,
      confirm: 'Batalkan pembayaran ini',
      submitting: 'Membatalkan…',
      cancel: 'Jangan jadi',
      success: 'Pembayaran dibatalkan.',
    },
  },
  // The roster and the dues tiers (M6.16/M6.17), on their own screen rather
  // than as two more sections of Pengaturan: a fund's whole membership is a
  // list, not a setting, and it would have buried the locations and titipan
  // below a scroll of names. A member's tier is set on the member, which is
  // why the tiers live here and not with the dues status view.
  members: {
    heading: 'Anggota',
    roster: {
      heading: 'Daftar anggota',
      body: 'Semua yang ikut iuran kas ini. Anggota yang sudah tidak ikut lagi cukup dinonaktifkan — catatan lamanya tetap utuh.',
      empty: 'Belum ada anggota.',
      nameLabel: 'Nama anggota',
      tierLabel: 'Golongan',
      // The "no tier" option: a member with no tier owes no dues, which is a
      // real state (PRD §6), not a blank to be filled in later.
      tierNone: 'Tanpa golongan',
      joinedOnLabel: 'Mulai ikut',
      add: 'Tambah anggota',
      adding: 'Menambahkan…',
      edit: 'Ubah',
      save: 'Simpan',
      saving: 'Menyimpan…',
      cancel: 'Batal',
      deactivate: 'Nonaktifkan',
      deactivating: 'Menonaktifkan…',
      reinstate: 'Aktifkan lagi',
      reinstating: 'Mengaktifkan…',
      inactiveBadge: 'Tidak aktif',
      delete: 'Hapus',
      deleting: 'Menghapus…',
      // The 409 from a member who already has posted history. Same shape as
      // the locations section's: a refusal that points at the right action.
      deleteRefused: 'Anggota ini sudah punya catatan — nonaktifkan saja, jangan dihapus.',
    },
    tiers: {
      heading: 'Golongan & tarif',
      body: 'Besar iuran per bulan menurut golongan. Tarif baru ditambahkan mulai bulan tertentu, bukan menimpa yang lama — supaya bulan-bulan lampau tetap terbaca dengan tarif yang berlaku waktu itu.',
      empty: 'Belum ada golongan.',
      nameLabel: 'Nama golongan',
      add: 'Tambah golongan',
      adding: 'Menambahkan…',
      edit: 'Ubah',
      save: 'Simpan',
      saving: 'Menyimpan…',
      cancel: 'Batal',
      // A tier with no rate yet is an ordinary state, not a problem.
      noRates: 'Tarif belum ditentukan.',
      ratesHeading: 'Tarif',
      rateAmountLabel: 'Besar iuran per bulan',
      effectiveFromLabel: 'Mulai bulan',
      addRate: 'Tambah tarif',
      addingRate: 'Menambahkan…',
      editRate: 'Perbaiki nominal',
      deleteRate: 'Hapus tarif',
      deletingRate: 'Menghapus…',
      // From effective_from onward, since a rate has no end date - the next
      // one starting is what ends it.
      effectiveFrom: (period: string) => `Mulai ${period}`,
    },
  },
  // One bundled settings screen (M6.15), not four top-level destinations:
  // naming a location, retiring one, and adding a pass-through purpose are
  // all rare next to the everyday loop. M6.16 and M6.17 add their sections
  // to this same screen.
  settings: {
    heading: 'Pengaturan',
    // Renaming the kas (PRD §7.1's own promise: "bisa diganti nanti kalau
    // perlu"). The name is a label - it heads every screen and the public
    // report - so this changes nothing already recorded.
    fund: {
      heading: 'Nama kas',
      body: 'Nama ini muncul di seluruh aplikasi dan di laporan publik. Menggantinya tidak mengubah catatan apa pun.',
      nameLabel: 'Nama kas',
      save: 'Simpan',
      saving: 'Menyimpan…',
      saved: 'Nama kas diperbarui.',
    },
    locations: {
      heading: 'Lokasi penyimpanan',
      body: 'Tempat uang kas disimpan — tunai atau rekening. Nama dan jenisnya bisa diubah kapan saja; lokasi yang sudah tidak dipakai bisa dinonaktifkan, catatannya tetap utuh.',
      empty: 'Belum ada lokasi.',
      kindLabel: 'Jenis',
      kindCash: 'Tunai',
      kindBank: 'Bank',
      nameLabel: 'Nama lokasi',
      add: 'Tambah lokasi',
      adding: 'Menambahkan…',
      // Name and jenis are both editable: she named these herself in the
      // setup wizard, and neither label is a posted fact.
      edit: 'Ubah',
      save: 'Simpan',
      saving: 'Menyimpan…',
      cancel: 'Batal',
      deactivate: 'Nonaktifkan',
      deactivating: 'Menonaktifkan…',
      reinstate: 'Aktifkan lagi',
      reinstating: 'Mengaktifkan…',
      inactiveBadge: 'Tidak aktif',
      delete: 'Hapus',
      deleting: 'Menghapus…',
      // The 409 the server answers the moment anything references the
      // location. Not phrased as a failure: deactivating is what she
      // actually wants, and this points her at it.
      deleteRefused: 'Lokasi ini sudah punya riwayat — nonaktifkan saja, jangan dihapus.',
    },
    passThrough: {
      heading: 'Titipan',
      body: 'Uang yang dikumpulkan untuk diteruskan ke pihak lain, misalnya kas bidang. Bukan milik kas ini, hanya lewat. Namanya bisa diperbaiki kapan saja.',
      empty: 'Belum ada titipan.',
      nameLabel: 'Nama titipan',
      add: 'Tambah titipan',
      adding: 'Menambahkan…',
      // Same pair of words the locations section uses, and for the same
      // reason: a name is a label, not catatan yang sudah tercatat.
      edit: 'Ubah',
      save: 'Simpan',
      saving: 'Menyimpan…',
      cancel: 'Batal',
    },
  },
} as const
