// Every user-facing string lives here: Bahasa Indonesia, sentence case, warm
// (ADR-014). Centralized from the first screen so a second language stays
// additive — no copy inline in components.
export const copy = {
  app: {
    name: 'uruni',
    tagline: 'Kas bersama yang selalu cocok.',
  },
  smoke: {
    heading: 'Uruni sudah jalan',
    body: 'Halaman ini cuma penanda bahwa aplikasi dan server sudah tersambung. Tampilan sebenarnya menyusul.',
    check: 'Periksa koneksi server',
    checking: 'Sedang memeriksa…',
    online: 'Server tersambung.',
    offline: 'Belum tersambung — Uruni butuh koneksi.',
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
} as const
