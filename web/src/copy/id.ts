// Every user-facing string lives here: Bahasa Indonesia, sentence case, warm
// (ADR-014). Centralized from the first screen so a second language stays
// additive — no copy inline in components.
export const copy = {
  app: {
    name: 'uruni',
    tagline: 'Kas bersama yang selalu cocok.',
  },
  // The app shell every authenticated screen renders inside (M6.6). Only the
  // logout control needs words today; the header's heading is the fund's own
  // name, which comes from the server, not from here.
  shell: {
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
  // The everyday "catat transaksi" form (M6.8, PRD §7.2): amount, in/out,
  // location, purpose, date, optional note. Reachable from the add-FAB on
  // home. successIn/successOut are shown on home after a successful post -
  // worded per Design-System.md's own voice examples for each direction.
  record: {
    addAction: 'Catat transaksi',
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
  // Modeled on Design-System.md:106-107's voice examples.
  reconciliation: {
    // The first-run state, before any count has ever been taken: neutral on
    // purpose. Uruni has nothing to compare its ledger against yet, so it
    // says so and invites the count instead of claiming a match.
    neverChecked: 'Belum pernah dicek — hitung uangnya kapan saja.',
    matched: 'Kas dan catatan sudah cocok.',
    // amount is already formatted (formatIDR) by the caller - never a raw
    // number crosses into copy.
    discrepancy: (amount: string) => `Ada selisih ${amount} — mau dicek bareng?`,
  },
} as const
