// Every user-facing string lives here: Bahasa Indonesia, sentence case, warm
// (ADR-014). Centralized from the first screen so a second language stays
// additive — no copy inline in components.

// The one sentence the public demo (ADR-045) says about its shared login, in two
// places: under the save button of the two Akun bendahara dialogs, and as the
// words for the demo_login_locked code if a request ever gets past the button.
const demoLoginLocked = 'Di demo, email dan kata sandi tidak bisa diubah supaya pengunjung lain tetap bisa masuk.'

export const copy = {
  app: {
    name: 'uruni',
    tagline: 'Kas bersama yang selalu cocok.',
  },
  // The app shell every authenticated screen renders inside: the header's
  // logout control and the sticky footer nav. The header's heading is the
  // fund's name, which comes from the server, not from here.
  shell: {
    // The sticky footer's five destinations (ADR-032). Single words, because a
    // tab label that wraps on a small phone is too long - and these are
    // landmarks, not sentences. "Iuran" is a Riwayat tab
    // (copy.history.tabs.dues), not a footer slot.
    nav: {
      // Names the <nav> for a screen reader, which otherwise announces an
      // unlabelled navigation landmark.
      label: 'Navigasi utama',
      home: 'Beranda',
      history: 'Riwayat',
      record: 'Catat',
      members: 'Anggota',
      settings: 'Pengaturan',
    },
    logout: 'Keluar',
    loggingOut: 'Sedang keluar…',
  },
  // Riwayat's tab strip (ADR-032's "second level"): each tab is a route under
  // /history, never component state (History.tsx). Iuran's strings stay in
  // copy.dues and Talangan's in copy.reimbursements - this namespace holds
  // only the tab labels. Cek kas's heading reuses copy.reconcile.heading.
  history: {
    tabs: {
      transactions: 'Transaksi',
      dues: 'Iuran',
      reimbursements: 'Talangan',
    },
    // The Transaksi tab's search and paging (ADR-032). The placeholder names
    // what the server searches, in the record form's words, so she knows a
    // member's name or an amount will find a row. While a page is loading the
    // button reuses common.loading.
    transactions: {
      searchLabel: 'Cari transaksi',
      searchPlaceholder: 'Catatan, pos, anggota, atau jumlah',
      loadMore: 'Muat lebih banyak',
      noResults: (q: string) => `Tidak ada transaksi yang cocok dengan “${q}”.`,
      // The purpose filter - also the link from an envelope's detail screen,
      // the one route ADR-032 gives a closed envelope's record. It names
      // itself with the record form's word ("Pos", CONTEXT.md: one word per
      // concept). The empty line is separate from noResults above: an empty
      // envelope is not a failed search.
      purposeFilterLabel: (name: string) => `Pos: ${name}`,
      purposeFilterEmpty: 'Belum ada transaksi untuk pos ini.',
      // Riwayat's filters: the public report's four, in its words
      // (report_copy.go) - Bulan, Pos, Anggota and the record form's Jenis
      // (record.directionLabel/In/Out, reused). Picked in a dialog behind one
      // button; each active one shows as a chip she can clear on its own.
      filterButton: 'Filter',
      filterButtonActive: (count: number) => `Filter, ${count} aktif`,
      filterHeading: 'Filter transaksi',
      filterMonth: 'Bulan',
      filterPurpose: 'Pos',
      filterMember: 'Anggota',
      filterAll: 'Semua',
      filterApply: 'Tampilkan',
      filterReset: 'Hapus semua filter',
      monthFilterLabel: (month: string) => `Bulan: ${month}`,
      memberFilterLabel: (name: string) => `Anggota: ${name}`,
      filterClear: (label: string) => `Hapus filter ${label}`,
      filterEmpty: 'Tidak ada transaksi yang cocok dengan filter ini.',
      // Undoing an ordinary transaction (ADR-042): one door, the entry detail's
      // button, which hands off to the inline form. A reversal is a new entry, never
      // an edit, so the copy says "batalkan", never "hapus". reverse is both the
      // button and the form's title; reverseDate* state the date the ledger will give it.
      reverse: 'Batalkan transaksi',
      reverseExplainer:
        'Transaksi ini tidak dihapus. Pembatalannya dicatat sebagai entri baru, dan saldo kembali seperti sebelum transaksi ini dicatat.',
      // date is already formatted (formatIsoDate) by the caller.
      reverseDateOriginal: (date: string) => `Dicatat pada ${date}.`,
      reverseDateToday: 'Dicatat hari ini, karena kas sudah dicek sejak transaksi ini.',
      reverseNoteLabel: 'Alasan (opsional)',
      reverseConfirm: 'Ya, batalkan',
      reverseCancel: 'Jangan jadi',
      reverseSubmitting: 'Membatalkan…',
      reverseSuccess: 'Transaksi dibatalkan.',
      // Scoped ahead of copy.common.errors' transaction_already_reversed
      // (reversalErrorText): that one is worded for the Iuran screens, and this
      // list names its rows "transaksi" whatever the kind.
      errors: {
        transaction_already_reversed: 'Transaksi ini sudah dibatalkan.',
      },
    },
    // The Talangan tab's search and paging (ADR-032). A narrower placeholder
    // than Transaksi's: this search covers only member name and note, not pos
    // or jumlah, so the placeholder does not claim otherwise.
    reimbursements: {
      searchLabel: 'Cari talangan',
      searchPlaceholder: 'Nama anggota atau catatan',
      loadMore: 'Muat lebih banyak',
      noResults: (q: string) => `Tidak ada talangan yang cocok dengan “${q}”.`,
    },
    // Cek kas's tab: a paged, read-only list of past snapshots - no search
    // (ADR-032: "a handful of dated snapshots a year"). The tab label reuses
    // copy.reconciliation.heading, so "Cek kas" has one source.
    reconciliations: {
      emptyState: 'Belum pernah cek kas. Riwayatnya muncul di sini setelah cek kas pertama.',
      loadMore: 'Muat lebih banyak',
      // The snapshot's note, shown in the detail sheet - no existing bare
      // "Catatan" label to reuse (every other is "Catatan (opsional)", a form
      // field's label).
      noteLabel: 'Catatan',
    },
    // The Iuran tab: the dues payment history (ADR-032), with the status
    // matrix on its own screen, opened by copy.dues.entryLink at the top of
    // the tab. Searches member name only. The reversal row's label and the
    // reversed badge stay in copy.dues.history (reversalRow/reversedBadge),
    // reused here: the same two words mean the same thing in the
    // period-scoped panel and in this fund-wide list.
    dues: {
      heading: 'Riwayat pembayaran',
      searchLabel: 'Cari pembayaran',
      searchPlaceholder: 'Nama anggota',
      loadMore: 'Muat lebih banyak',
      noResults: (q: string) => `Tidak ada pembayaran yang cocok dengan “${q}”.`,
      empty: 'Belum ada pembayaran iuran.',
      // date is already formatted (formatIsoDate) by the caller.
      reversesLabel: (date: string) => `Membatalkan pembayaran ${date}`,
    },
  },
  // Receipt photos (ADR-011, PRD section 7.4): an optional photo of the nota,
  // attached at record time or after the fact, on a transaction row or a
  // reimbursement claim. Shared by ReceiptPicker.tsx (the field/attach control)
  // and ReceiptDialog.tsx (the viewer). Upload error codes live in
  // common.errors below with every other wire code.
  receipts: {
    // The record-time field's label (RecordTransaction.tsx, the reimbursement
    // claim form) - also ReceiptPicker's "choose a file" button caption, since
    // picking a photo IS this field. The after-the-fact affordance on a row
    // with no photo yet (TransactionList.tsx, the reimbursement row) - and
    // inside the viewer the "attach another" action, since adding a first photo
    // and a second are the same action.
    addFromRow: 'Tambah foto nota',
    // The dialog's blank state, per parent kind - a claim is a talangan, not a
    // transaksi.
    emptyTransaction: 'Transaksi ini belum punya foto nota. Tambahkan dengan tombol di bawah.',
    emptyReimbursement: 'Talangan ini belum punya foto nota. Tambahkan dengan tombol di bawah.',
    // The row indicator once receipt_ids is non-empty - opens the viewer.
    viewReceipt: 'Lihat nota',
    // The viewer dialog's heading (unapproved - see report).
    dialogHeading: 'Nota',
    change: 'Ganti foto',
    delete: 'Hapus foto',
    deleteConfirm: 'Hapus foto nota ini?',
    uploading: 'Mengunggah…',
    deleting: 'Menghapus…',
    cancel: 'Batal',
    // aria-label for ReceiptPicker's clear-selection control, distinct from
    // `cancel` (unapproved - see report): "Batal" reads as abandoning the
    // whole form, not clearing one field.
    clearSelected: 'Hapus foto yang dipilih',
    // aria-label for the row icon, built from its name (unapproved - see
    // report): a bare glyph needs an accessible name distinct from the sighted
    // viewReceipt/addFromRow caption beside it, since the icon is the whole
    // control on a dense row.
    rowControlAria: (hasReceipt: boolean) => (hasReceipt ? 'Lihat nota' : 'Tambah foto nota'),
    // The per-photo "more" menu's aria-label (ReceiptDialog.tsx) - the button
    // sits on the photo, so it needs an accessible name distinct from Ganti
    // foto/Hapus foto, the items inside it.
    photoMenuAria: 'Pilihan foto',
    // The photo itself, tapped to open the full-screen viewer
    // (ReceiptViewer.tsx) - distinct from photoMenuAria, since both sit on the
    // same image.
    zoomAria: 'Perbesar foto',
    // Shown once a transaction or a reimbursement claim posted but its photo
    // did not - the record is never rolled back (see RecordTransaction.tsx and
    // Reimbursements.tsx).
    transactionPhotoFailed: 'Transaksi tersimpan, tapi fotonya belum terunggah. Foto bisa ditambahkan dari Riwayat.',
    reimbursementPhotoFailed: 'Talangan tersimpan, tapi fotonya belum terunggah. Foto bisa ditambahkan dari Riwayat.',
  },
  common: {
    loading: 'Memuat…',
    offlineBanner: 'Belum tersambung — Uruni butuh koneksi.',
    retry: 'Coba lagi',
    // Shown only when a new build is already downloaded and waiting. Never an
    // alarm - nothing is broken, and nothing reloads until she taps.
    updateAvailable: 'Versi baru sudah siap.',
    updateReload: 'Muat ulang',
    // The dialog primitive's close button - every sheet in the app uses this
    // label, never a screen-specific synonym.
    close: 'Tutup',
    // Wire error codes -> Indonesian copy. The server's message field stays
    // English by design (ADR-014: the API is a code surface) and never reaches
    // the treasurer; this map is what she sees instead.
    //
    // Seeded with the codes the shared states and the auth routes can hit
    // (not_found, method_not_allowed, a network failure, invalid_argument,
    // already_registered, invalid_credentials, too_many_requests); each later
    // slice adds its route's codes. invalid_credentials and too_many_requests
    // also get more specific auth.login copy below - the strings here are the
    // ErrorState fallback for the same codes reached elsewhere.
    errors: {
      not_found: 'Data yang dicari tidak ditemukan.',
      method_not_allowed: 'Aksi ini tidak didukung.',
      network_error: 'Belum tersambung — Uruni butuh koneksi.',
      // A write whose response never arrived. The server may already have saved
      // it - so say it is uncertain, and to check before saving again. One line for
      // every write (ADR-043): it names no place and promises no more than the system
      // keeps (a repeat-safe key is forgotten after 15 minutes or a reload).
      write_outcome_unknown:
        'Koneksinya terputus sebelum Uruni sempat menjawab, jadi belum pasti tersimpan. Cek dulu apakah sudah tersimpan sebelum menyimpan lagi.',
      invalid_argument: 'Ada isian yang belum sesuai. Coba periksa lagi.',
      already_registered: 'Akun bendahara untuk Uruni ini sudah pernah dibuat.',
      invalid_credentials: 'Email atau kata sandi salah.',
      // A new password on the list of the most common ones (register, and the
      // password change in Pengaturan). No blame: the list is public and long.
      password_too_common: 'Kata sandi ini terlalu umum dan mudah ditebak. Pilih yang lain, ya.',
      // The current password typed to change the login's email or password
      // (Pengaturan, Akun bendahara). Unlike invalid_credentials above, she is
      // signed in, so it can say plainly which field to check.
      wrong_current_password: 'Kata sandi saat ini salah. Coba periksa lagi.',
      too_many_requests: 'Terlalu banyak percobaan. Tunggu dulu, lalu coba lagi nanti, ya.',
      // The public demo refuses a change of its shared login (ADR-045); the save
      // button is disabled there, so this is the backstop.
      demo_login_locked: demoLoginLocked,
      // Every slot for checking a password is busy (503). Nothing is wrong with
      // what she typed and she is not locked out - the server is occupied for a
      // moment - so it says to try again right away, unlike too_many_requests
      // above, which says to wait minutes.
      server_busy: 'Uruni sedang sibuk. Coba lagi sebentar lagi, ya.',
      // The setup wizard: a second POST /api/setup, and a malformed request
      // body.
      fund_already_exists: 'Kas ini sudah pernah disiapkan.',
      invalid_json: 'Ada yang tidak beres saat mengirim data. Coba lagi.',
      // A malformed or reused idempotency key (ADR-043): the wrapper never sends
      // either, so these are unreachable from the app. The 409 is a definite answer.
      invalid_idempotency_key: 'Ada yang tidak beres saat mengirim data. Coba lagi.',
      idempotency_key_reused: 'Ada yang tidak beres saat mengirim data. Coba lagi.',
      // A cross-site write the origin guard refused, and a body over the 1 MiB
      // cap: the app sends neither, so they share the same wording.
      cross_origin_request: 'Ada yang tidak beres saat mengirim data. Coba lagi.',
      request_too_large: 'Ada yang tidak beres saat mengirim data. Coba lagi.',
      // The session ended while she was in the app: it expired, or she was
      // signed out from another device or by a restore.
      unauthenticated: 'Sudah keluar otomatis. Masuk lagi untuk melanjutkan.',
      // The database's CHECK refused what the app's own checks let through: the
      // same sentence as invalid_argument, since to her it is the same thing.
      check_violation: 'Ada isian yang belum sesuai. Coba periksa lagi.',
      // A 500. The same sentence ErrorState shows for a code it does not know.
      internal_error: 'Ada yang tidak beres. Coba lagi sebentar lagi.',
      // Deleting a location (or a member or tier) that something already
      // points at. The locations section renders more specific wording
      // pointing at deactivate; this is the shared fallback for the same code
      // reached elsewhere.
      referenced_by_other_records: 'Data ini sudah dipakai di catatan lain, jadi tidak bisa dihapus.',
      // ADR-040: a rate a correction has since replaced or deleted (a stale screen).
      // The list reloads when this arrives, so the sentence says to look again.
      dues_rate_superseded: 'Nominal ini sudah diperbaiki atau dihapus. Daftarnya sudah dimuat ulang, silakan periksa lagi.',
      // Renaming the fund's kas utama through this route - the one purpose it
      // refuses. A titipan and an amplop are renameable.
      purpose_not_renameable: 'Nama kas utama tidak bisa diganti.',
      // Two tiers with the same name (UNIQUE (fund_id, name)), or two rates for
      // the same tier and month (UNIQUE (tier_id, effective_from)). errors.go
      // maps every UNIQUE breach to this one code.
      unique_violation: 'Sudah ada yang sama. Coba nama atau bulan yang lain.',
      // ADR-031: RecordTransaction.tsx's picker excludes a closed envelope's
      // purpose (?selectable=true), so this is the fallback for the rare case
      // it was posted anyway - a stale form left open across a close.
      // Incidentals.tsx's screen-scoped errors map has no entry for this code:
      // the guard fires on the record form, not there.
      incidental_closed: 'Amplop ini sudah ditutup — buka lagi dulu untuk mencatat transaksi baru.',
      // ADR-033: POST /api/transactions/{id}/purpose-correction's named
      // refusals. The chip, dialog and row labels normally keep these
      // unreachable; the strings are here so a code that does arrive is never
      // orphaned.
      purpose_correction_opening: 'Pos saldo awal tidak bisa diperbaiki.',
      purpose_correction_dues: 'Pos pembayaran iuran tidak bisa diperbaiki di sini.',
      purpose_correction_reimbursement: 'Pos talangan tidak bisa diperbaiki di sini — perbaiki lewat catatan talangannya.',
      purpose_correction_transfer: 'Pos hasil pindah tidak bisa diperbaiki lagi.',
      // The refusal now covers every reversal, not only a dues one (ADR-042).
      purpose_correction_reversal: 'Pos pembatalan tidak bisa diperbaiki.',
      // Reverse-then-correct, reachable from a stale page (ADR-042).
      purpose_correction_already_reversed: 'Transaksi ini sudah dibatalkan, posnya tidak bisa diperbaiki.',
      purpose_correction_target_closed: 'Amplop ini sudah ditutup — buka lagi dulu sebelum memindahkan transaksi ke sini.',
      purpose_correction_source_closed: 'Amplop ini sudah ditutup — buka lagi dulu sebelum memindahkan transaksi dari sini.',
      purpose_correction_noop: 'Baris ini sudah memakai pos itu.',
      // Every amount has a ceiling of Rp999.999.999.999 (12 digits). The
      // amount input stops at 12 digits, so this is the fallback for a request
      // that got past it (a stale client, a direct API call).
      amount_too_large: 'Jumlahnya terlalu besar. Batasnya Rp999.999.999.999 — cek lagi, mungkin ada nol yang kelebihan.',
      // A date outside the plausible window (before 2000, or past the field's
      // ceiling). The pickers keep inside it; this covers a stale form or a mistyped year.
      date_out_of_range: 'Tanggalnya di luar batas yang wajar — cek lagi, mungkin salah ketik tahunnya.',
      // ADR-036: POST /api/purpose-moves' named refusals. The form already
      // withholds Titipan, a closed envelope and an amount above the balance,
      // so these are the fallback for a stale form - a close or a posting that
      // landed after it loaded.
      purpose_move_same_purpose: 'Pilih dua pos yang berbeda.',
      purpose_move_pass_through: 'Uang titipan tidak bisa dipindahkan antar pos - titipan hanya bisa diteruskan.',
      purpose_move_closed: 'Amplop ini sudah ditutup — buka lagi dulu sebelum memindahkan uang ke sini atau dari sini.',
      purpose_move_insufficient: 'Saldo pos asal tidak cukup untuk jumlah ini.',
      purpose_move_account_inactive: 'Lokasi ini sudah tidak dipakai. Pilih lokasi yang lain.',
      // Every posting and every Cek kas count refuses a retired location, and a
      // count must name every active one. The forms offer only active
      // locations, so these are the fallback for a stale form.
      account_inactive: 'Lokasi ini sudah tidak dipakai. Pilih lokasi yang lain.',
      // A location whose recorded balance is not zero cannot be retired.
      // Location-specific codes, so - unlike referenced_by_other_records - the
      // locations dialog shows these sentences as they are, through
      // ErrorState. Above zero: move it out. Below zero: nothing to move; a
      // count finds the gap.
      account_holds_money: 'Lokasi ini masih menyimpan uang. Pindahkan dulu saldonya ke lokasi lain lewat Pindah lokasi, baru nonaktifkan.',
      account_balance_negative: 'Saldo tercatat lokasi ini minus. Cek kas dulu untuk menemukan selisihnya, baru nonaktifkan.',
      reconciliation_location_missing:
        'Ada lokasi aktif yang belum dihitung. Daftarnya sudah diperbarui — hitung lokasi itu, lalu simpan lagi.',
      // A Cek kas "Sesuaikan saldo" (adjusted) must equal the selisih exactly.
      // The form blocks a mismatch before save, so she meets this only when
      // the gap moved under a stale form (a transaction landed after she
      // opened it) - hence "muat ulang", not "pick another option".
      reconciliation_plug_mismatch: 'Selisihnya berubah sejak lokasi ini dihitung. Muat ulang, cek lagi angkanya, lalu simpan.',
      // ADR-034: a sumbangan bernama can't be re-tagged as an ordinary row can
      // - moving it would leave participation counting the member against an
      // envelope the money has left. Added ahead of its UI, like the
      // purpose_correction_* codes above.
      purpose_correction_named_contribution:
        'Pos sumbangan yang sudah bernama tidak bisa diperbaiki di sini — batalkan sumbangannya, lalu catat lagi.',
      // The named 409 for a second reversal of the same row (ADR-029, ADR-042): the
      // shared fallback, worded for the Iuran screens; Riwayat's mapper checks
      // copy.history.transactions.errors first.
      transaction_already_reversed: 'Pembayaran iuran ini sudah dibatalkan.',
      // The reversal route's other refusals (ADR-042). The entry detail offers the
      // button only where the ledger would accept, so these cover a stale page.
      reversal_opening: 'Saldo awal tidak bisa dibatalkan.',
      reversal_reimbursement: 'Pelunasan talangan tidak bisa dibatalkan.',
      reversal_transfer: 'Pindahan tidak bisa dibatalkan satu sisi saja.',
      reversal_of_reversal: 'Pembatalan tidak bisa dibatalkan lagi.',
      reversal_adjustment: 'Penyesuaian tidak bisa dibatalkan.',
      reversal_reconciliation_fix: 'Transaksi ini dipakai untuk menutup selisih cek kas, jadi tidak bisa dibatalkan.',
      reversal_before_original: 'Transaksi ini bertanggal setelah hari ini — pembatalannya baru bisa dicatat mulai tanggal itu.',
      // The receipt upload routes (internal/http/receipts.go's
      // processAndStoreReceipt) - a phone shooting HEIC by default gets its own
      // wording, as in the Go comment beside that code.
      heic_unsupported: 'Foto HEIC belum bisa dipakai. Bagikan atau ekspor fotonya sebagai JPEG, lalu coba lagi.',
      payload_too_large: 'Ukuran foto lebih dari 10 MB. Coba foto yang lebih kecil.',
      image_too_large: 'Resolusi foto terlalu besar. Coba foto yang lebih kecil.',
      unsupported_media_type: 'File ini bukan foto. Gunakan JPEG, PNG, atau WebP.',
      // Restoring from an uploaded backup (ADR-012). Each its own code
      // (internal/http/restore.go) rather than a shared "invalid file"
      // message, because what the treasurer should do next differs by case -
      // update the server, pick a different file, or nothing at all (a
      // rolled-back restore changed nothing).
      restore_format_too_new: 'File ini dibuat oleh versi Uruni yang lebih baru. Perbarui dulu aplikasinya, lalu coba lagi.',
      restore_format_too_old: 'File ini dibuat oleh versi Uruni yang terlalu lama dan sudah tidak bisa dibaca.',
      restore_invalid_file: 'File ini bukan cadangan Uruni yang sah.',
      restore_bad_image: 'Salah satu foto nota di dalam cadangan tidak bisa dibaca.',
      restore_file_too_large: 'File ini lebih besar daripada cadangan Uruni pada umumnya.',
      // The inspect step: the file's totals do not match its entries, so it is
      // refused before she is asked for her password. Not
      // restore_totals_mismatch below, which is about a restore that already
      // ran and rolled back.
      restore_file_inconsistent:
        'Jumlah di file ini tidak cocok dengan catatan di dalamnya, jadi file ini tidak bisa dipakai untuk memulihkan. Coba file cadangan yang lain.',
      // A rolled-back restore (ADR-012's numbers check) - nothing in the kas
      // changed, and the wording says so rather than leaving her to guess.
      restore_totals_mismatch: 'Ada yang tidak cocok saat memeriksa hasil pemulihan, jadi tidak ada yang diubah.',
      restore_upload_expired: 'Unggahan ini sudah kedaluwarsa. Unggah lagi file cadangannya.',
    },
    // Shown for a code not in the map above.
    unknownError: 'Ada yang tidak beres. Coba lagi sebentar lagi.',
  },
  auth: {
    // The peek button on every password field (PasswordInput). One fixed
    // label; aria-pressed says whether it is on.
    showPassword: 'Tampilkan kata sandi',
    // The frame around both signed-out screens, Login and Register
    // (components/AuthChrome.tsx).
    chrome: {
      // Under the logo, not in the card: what Uruni is and what it promises,
      // word for word from Positioning.md and the README - the tagline, then
      // the treasurer's peace of mind.
      tagline: 'Kelola dana kebersamaan.',
      promise: 'Tenang, karena catatan selalu cocok dengan uang yang ada.',
      // The footer under the card: where the source and the licence live, for
      // the operator and for AGPL-3.0 section 13 - a network user is offered
      // the source of the version they are using.
      sourceCode: 'Kode sumber',
      license: 'Lisensi AGPL-3.0',
      // The licence notices of everything Uruni ships with (#521), served by
      // the app itself at /third-party-notices.
      thirdParty: 'Lisensi pihak ketiga',
      // Links the maintainer's site. "Pemelihara" is the word Indonesian
      // open-source translations use for a project's maintainer.
      maintainer: 'Pemelihara',
    },
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
      emailLabel: 'Email',
      passwordLabel: 'Kata sandi',
      submit: 'Masuk',
      submitting: 'Sedang masuk…',
      invalidCredentials: 'Email atau kata sandi salah. Coba periksa lagi.',
      tooManyRequests: 'Terlalu banyak percobaan masuk. Tunggu dulu, lalu coba lagi nanti, ya.',
      // The notice the public demo (ADR-045) shows above the form: what this is,
      // and the credentials spelled out for a visitor who clears the pre-filled
      // fields. Title is bold, the two values are monospace.
      demo: {
        title: 'Ini demo Uruni.',
        body: 'Semua datanya rekaan, dan siapa pun yang masuk bisa mengubahnya, jadi jangan simpan data asli di sini. Demo kembali bersih sendiri, paling lama setiap jam.',
        emailLabel: 'Email',
        passwordLabel: 'Kata sandi',
      },
    },
  },
  // The first-run setup wizard (PRD section 7.1): four steps, only the first
  // two (fund name, at least one location) mandatory - balances and roster are
  // openly optional per step, not gated behind a forced review.
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
      body: 'Kosongkan kalau lokasi ini belum ada uangnya.',
      amountLabel: (accountName: string) => `Saldo awal — ${accountName}`,
    },
    roster: {
      heading: 'Tambah anggota dan iuran (opsional)',
      body: 'Bisa dilewati dan diisi nanti kapan saja lewat menu anggota.',
      tierNameLabel: 'Nama kategori iuran',
      rateAmountLabel: 'Besar iuran per bulan',
      memberNameLabel: 'Nama anggota',
      addMember: 'Tambah anggota',
      removeMember: 'Hapus anggota',
      skip: 'Lewati',
      finish: 'Selesai',
    },
  },
  // The everyday record-transaction form (PRD section 7.2): amount, in/out,
  // location, purpose, date, optional note. Reached from the footer nav's
  // "Catat" tab. successIn/successOut are shown on home after a successful
  // post, worded per Design-System.md's voice examples for each direction.
  // The h1 follows what is being recorded (RecordTransaction.tsx): headingOut,
  // headingIn, headingDonation (Masuk onto an envelope), the talangan record
  // heading for a ticked claim, the two move names, and the dues payment
  // heading for Iuran.
  record: {
    headingOut: 'Catat pengeluaran',
    headingIn: 'Catat pemasukan',
    headingDonation: 'Catat sumbangan',
    directionLabel: 'Jenis',
    directionIn: 'Uang masuk',
    directionOut: 'Uang keluar',
    // Money that is neither entering nor leaving the kas, only changing place
    // - cash deposited at the bank, or drawn back out. Not "Uang pindah",
    // which reads broken beside the two above, and not "Transfer", which
    // CONTEXT.md reserves as the English identifier for the row pair rather
    // than a label. The same two words name these rows in Riwayat
    // (rowLabels.transferLocation).
    directionTransfer: 'Pindah lokasi',
    // The fifth kind (ADR-036): money that stays where it is but changes what
    // it is for - the counterpart of Pindah lokasi, one word apart. The same
    // two words name these rows in Riwayat (rowLabels.transferPurposeMove).
    directionPurpose: 'Pindah pos',
    // The fourth kind: a dues payment. The same word as Riwayat's Iuran tab,
    // where the payment then appears.
    directionDues: 'Iuran',
    // The visible captions on the kind selector's five-column track. One word
    // each because five options across a phone leave about 75px apiece, and
    // the icon above each caption carries the verb - the arrows say out, in
    // and moved - so "Uang" and "Pindah" are not repeated. The full names above
    // stay the aria-labels, and what every other surface (Reconcile, the entry
    // detail, Riwayat) shows.
    captionOut: 'Keluar',
    captionIn: 'Masuk',
    captionLocation: 'Lokasi',
    captionPurpose: 'Pos',
    amountLabel: 'Jumlah',
    locationLabel: 'Lokasi',
    // A transfer splits the one location field in two. "Dari" and "ke" are the
    // plainest words, and they keep the field she knows meaning what it meant:
    // where the money is, or where it leaves from.
    fromLocationLabel: 'Dari lokasi',
    toLocationLabel: 'Ke lokasi',
    // The same location on both sides moves nothing. The ledger refuses it
    // too, but she should read why here rather than meet a rejected submit.
    sameLocationHint: 'Pilih dua lokasi yang berbeda - uang pindah dari satu tempat ke tempat lain.',
    // Shown under each location while moving money, so she sees what is about
    // to move against what is there - and so the swap button has a visible
    // effect. amount is already formatted by the caller.
    locationBalance: (amount: string) => `Saldo sekarang ${amount}`,
    // Under each pos picker in Pindah pos: names what the figure is the balance
    // of, so it never reads as the fund total or a location's.
    purposeBalance: (amount: string) => `Saldo pos sekarang ${amount}`,
    // Only ever about the source: receiving money never pushes a balance down.
    // Warns without blocking, as the titipan hint does - every other posting
    // path allows an out larger than the balance, and a real deposit from a
    // wallet the app thinks is empty has to be recordable.
    locationGoesNegative: (amount: string) => `Setelah pindah, saldo lokasi asal jadi minus ${amount}.`,
    // One tap for the pair: depositing cash and drawing it back out are the
    // same two locations in opposite order. Visible beside the icon rather than
    // an aria-label on a bare glyph - an icon alone between two pickers read as
    // decoration, and the caption gives the control its accessible name.
    swapLocations: 'Tukar lokasi',
    swapPurposes: 'Tukar pos',
    purposeLabel: 'Pos',
    // Pindah pos splits the one pos field in two, as a location transfer
    // splits Lokasi: where the money is for now, and where it should be for.
    // Bare "Dari" and "Ke" because the selected kind says what is being moved.
    fromPurposeLabel: 'Dari pos',
    toPurposeLabel: 'Ke pos',
    // Same pos on both sides moves nothing, and the ledger refuses it too -
    // she reads why here instead of meeting a rejected submit.
    noOpenEnvelopeHint: 'Belum ada amplop terbuka.',
    samePurposeHint: 'Pilih dua pos yang berbeda - uang pindah dari satu pos ke pos lain.',
    // The source cannot give more than it holds (ADR-036). Unlike a location
    // transfer this blocks, because the ledger refuses it: amount is the
    // balance already formatted by the caller.
    purposeInsufficientHint: (amount: string) => `Saldo pos asal hanya ${amount}, tidak cukup untuk jumlah ini.`,
    dateLabel: 'Tanggal',
    noteLabel: 'Catatan (opsional)',
    submit: 'Simpan',
    submitting: 'Menyimpan…',
    // Installed to a home screen there is no browser back button, so leaving
    // the form without recording needs its own way out.
    cancel: 'Batal',
    // Keluar's tick box: a member paid out of pocket. Ticked, the entry becomes
    // a Talangan claim instead of money leaving the kas.
    frontedLabel: 'Pengeluaran ini ditalangi',
    // Says what did NOT happen: a claim is off-ledger until it is paid, so the
    // saldo she watches has not moved yet.
    successFronted: 'Talangan dicatat. Saldo belum berkurang sampai talangan dibayar.',
    successIn: 'Pemasukan berhasil dicatat.',
    successOut: 'Pengeluaran berhasil dicatat.',
    // Says what did NOT happen as much as what did: the total is the number she
    // watches, and a confirmation that left it unmentioned would invite her to
    // check it.
    successTransfer: 'Uang sudah dipindahkan. Total kas tidak berubah.',
    // A purpose move changes no total either, and the sentence says so for the
    // reason successTransfer does.
    successPurposeMove: 'Uang sudah dipindahkan antar pos. Total kas tidak berubah.',
    // Shown when an "uang keluar" tagged to a titipan would take that titipan
    // below zero (PRD section 7.6). Names the likelier reading and the tag
    // that fits it: a titipan holds money that came in for someone else, so
    // spending more than came in usually means the kas paid this itself. It
    // never blocks - a titipan may legitimately sit negative - so the sentence
    // suggests rather than corrects, and stops at one line.
    passThroughNegativeHint: (name: string) =>
      `Pengeluaran ini lebih besar dari uang ${name} yang pernah masuk. Kalau ini dibayar pakai uang kas sendiri, pilih Kas Utama.`,
    // ADR-034: who a contribution is from, shown only when the money is coming
    // in and the chosen pos is an open amplop - a guest or an anonymous giver
    // leaves it as contributorNone, the field's default and never a validation
    // error.
    contributorLabel: 'Dari siapa? (opsional)',
    contributorNone: 'Tidak disebutkan',
  },
  // The home screen (PRD section 7.7): balance hero, per-location balances,
  // reconciliation status and recent activity - the landing page reached at
  // "/" once a fund exists (App.tsx's AuthedGate).
  home: {
    balanceHeading: 'Saldo kas',
    locationsHeading: 'Saldo per lokasi',
    // The purpose breakdown (PRD section 7.7, ADR-032): each open incidental
    // and each Titipan purpose gets its own row, so an envelope's balance -
    // including a negative one covered from Kas Utama per ADR-031 - is where
    // the treasurer looks. Kas Utama never appears here; it is the hero above.
    // A fund with neither an open incidental nor a Titipan shows no section,
    // so this heading has no empty-state string.
    purposeBreakdownHeading: 'Saldo per pos',
    // The total owed to members who fronted an expense, after the purpose
    // breakdown and only when above zero. Same words as the Talangan tab it
    // opens, so the row and the list read as one thing.
    owedToMembers: 'Talangan belum dibayar',
    recentActivityHeading: 'Aktivitas terbaru',
    // Arus kas, Beranda's first section below the divider (ADR-038): the
    // running month's two figures, the report's In and Out, so the two screens
    // never disagree. The heading names the month, so the card carries only
    // the two figures.
    cashflowHeading: 'Arus kas bulan ini',
    monthIn: 'Masuk',
    monthOut: 'Keluar',
    recentActivityEmpty: 'Belum ada transaksi tercatat.',
    // The entry point into Riwayat's Transaksi tab - the recent-five peek stays
    // a peek, so it needs a way to the full list.
    recentActivityViewAll: 'Lihat semua',
    // A purpose the balances response didn't name - it should not happen (both
    // come from the same fund), so this is a placeholder that keeps a row
    // readable rather than a state with meaning of its own.
    purposeUnknown: 'Tanpa pos',
    // date is already formatted (Intl.DateTimeFormat) by the caller. hasNewEntries
    // is the server's yes/no (has_new_entries): something was recorded after the
    // count, so the status above is about the count, not about now.
    lastChecked: (date: string, hasNewEntries: boolean) =>
      hasNewEntries ? `Terakhir dicek ${date} · ada catatan baru sejak itu` : `Terakhir dicek ${date}`,
  },
  // The label line TransactionList.tsx renders on a row the app itself created
  // - never a row she recorded herself (Decisions.md). Built at display time
  // from facts already on the row (GET /api/transactions' fields), so a
  // rename reaches every row instantly instead of freezing old rows with old
  // wording, and nothing generated is mistaken for what she typed. Each entry
  // pairs the sr-only kind word every screen reader needs (the icon is
  // decorative, aria-hidden) with the visible-text formatter - keyed by the
  // row shape, not the raw schema `kind` column, since one schema kind covers
  // more than one label (`transfer` is transferLocation or transferPurpose;
  // `adjustment` is duesReversal, contributionReversal or adjustment). The
  // " · " separator and " -> " arrow are written out here, never as a literal
  // non-ASCII character in a .tsx file (rule 10 - this file is the one
  // designated exception).
  rowLabels: {
    dues: {
      kind: 'Iuran',
      text: (period: string, memberName: string) => `${period} · ${memberName}`,
    },
    duesReversal: {
      text: (period: string, memberName: string) => `Pembatalan · ${period} · ${memberName}`,
    },
    // A named contribution to an envelope (ADR-034): the member's name, as a
    // settlement shows it; its reversal carries no period, unlike dues.
    contribution: {
      kind: 'Sumbangan',
      text: (memberName: string) => memberName,
      // A contribution into an envelope that names nobody: the visible text
      // says so rather than leaving the row with only a pos and a date.
      anonymous: 'Tanpa nama',
    },
    contributionReversal: {
      text: (memberName: string) => `Pembatalan · ${memberName}`,
    },
    // A reversal of a row naming no member (ADR-042) states the original's date.
    unnamedReversal: {
      text: (date: string) => `Pembatalan · transaksi ${date}`,
    },
    opening: {
      text: (accountName: string) => `Saldo awal · ${accountName}`,
    },
    // The settlement's visible text is just the member's name - the claim's
    // typed note (if any) already renders on the row's note line, so the label
    // does not repeat it.
    settlement: {
      kind: 'Talangan',
      text: (memberName: string) => memberName,
    },
    transferLocation: {
      kind: 'Pindah lokasi',
      text: (fromName: string, toName: string) => `${fromName} → ${toName}`,
    },
    transferPurpose: {
      kind: 'Tutup amplop',
      text: (fromName: string, toName: string) => `${fromName} → ${toName}`,
    },
    // A purpose correction's two legs (ADR-033). The same from -> to text as a
    // roll, and the same wire shape - both are kind='transfer' with
    // transfer_kind='reclass_purpose' - so only the icon and this word tell
    // them apart. Without it every correction would read "Tutup amplop": an
    // envelope closing that never happened.
    transferPurposeCorrection: {
      kind: 'Perbaikan pos',
      text: (fromName: string, toName: string) => `${fromName} → ${toName}`,
    },
    // The treasurer moving money between purposes on purpose (ADR-036,
    // transfer.reason 'allocation'). The visible text opens with the kind word,
    // so unlike the two above it needs no sr-only kind.
    transferPurposeMove: {
      text: (fromName: string, toName: string) => `Pindah pos · ${fromName} → ${toName}`,
    },
    // Every adjustment that is not a reversal: a cek kas fix or one she posted
    // herself - the report's word for both (ADR-038).
    adjustment: {
      text: (accountName: string) => `Penyesuaian · ${accountName}`,
    },
  },
  // ReconciliationBanner's copy: "cocok" when GET
  // /api/reconciliations/open-lines comes back empty, "selisih" otherwise.
  // Modeled on Design-System.md:106-107's voice examples. Extended in place
  // for the reconcile screen (PRD section 7.8) - one namespace, not a second
  // copy.reconcile block: the screen is the same reconciliation concept the
  // banner names, doing something about it. matched/discrepancy are reused
  // verbatim on the confirmation screen, not restated.
  reconciliation: {
    // The first-run state, before any count: neutral on purpose. Uruni has
    // nothing to compare its ledger against yet, so it says so and invites the
    // count instead of claiming a match.
    neverChecked: 'Belum pernah dicek. Hitung uangnya kapan saja.',
    // A claim about the count, not about now: Beranda's date line says when, and
    // whether anything was recorded since (home.lastChecked).
    matched: 'Kas dan catatan cocok saat dicek.',
    // amount is already formatted (formatIDR) by the caller - never a raw
    // number crosses into copy.
    discrepancy: (amount: string) => `Ada selisih ${amount}. Mau dicek bareng?`,
    // The per-location verdict while counting; the whole-fund strings above
    // stay for Beranda's banner and the result headline. amount is formatted.
    lineMatched: 'Cocok.',
    lineDiscrepancy: (amount: string) => `Selisih ${amount}.`,
    heading: 'Cek kas',
    intro: 'Hitung uang yang benar-benar ada di setiap lokasi, lalu bandingkan dengan catatan.',
    recordedLabel: 'Menurut catatan',
    countedLabel: 'Dihitung',
    // accountName is the location's name (e.g. "Tunai") - the per-field
    // labelling AmountInput's callers do.
    actualLabel: (accountName: string) => `Jumlah sebenarnya di ${accountName}`,
    resolutionLabel: 'Bagaimana selisih ini diselesaikan?',
    // Keyed by the schema's resolution string (reconciliation.go's four
    // values) so both the choice buttons and the confirmation list's per-line
    // label read from one map. "matched" is never a button - a zero-gap line
    // needs no choice - but still gets a label for the confirmation list.
    resolutionOptions: {
      matched: 'Cocok',
      entry_added: 'Ada transaksi yang belum tercatat',
      adjusted: 'Sesuaikan saldo (koreksi)',
      left_open: 'Simpan dulu, selesaikan nanti',
    },
    // The fix fields (PRD section 7.8's "add the missing transaction or post a
    // noted adjustment"). Direction/amount reuse copy.record's direction labels
    // (Uang masuk/Uang keluar) - a generic in/out toggle.
    fixPurposeLabel: 'Pos',
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
    staleNotice: 'Ada transaksi baru sejak kamu mulai menghitung tadi. Angkanya sudah diperbarui. Yuk, cek selisihnya sekali lagi.',
    backToHome: 'Kembali ke beranda',
    // Per-line difference (#227): the shared line list (ReconciliationLines,
    // used by Confirmation and Cek kas's detail sheet) shows this whenever a
    // line's own difference_amount is nonzero - amount is already formatted
    // (formatIDR) by the caller, same convention as discrepancy above.
    differenceLabel: 'Selisih',
    // Cek kas's list-row badge (#227): a short chip label, not the banner's
    // full sentence (discrepancy above reads as a question, wrong register
    // for a list row). "Cocok" alone reuses resolutionOptions.matched
    // instead of repeating the word here.
    selisihBadge: (amount: string) => `Selisih ${amount}`,
    // #498: what a line still has open after its fix. Live under the fix
    // fields as she types (amount already formatted, by magnitude), and per
    // line in the saved snapshot when an entry closed only part of the gap.
    remainingGap: (amount: string) => `Masih tersisa selisih ${amount}`,
    // The same slot when the fix squares the gap exactly.
    remainingNone: 'Selisihnya tertutup.',
    // Under remainingGap when the resolution is "Sesuaikan saldo": unlike
    // "Ada transaksi yang belum tercatat", a plug must close the whole gap,
    // so save stays off until it does.
    adjustedMustMatch:
      'Penyesuaian harus pas dengan selisihnya. Kalau baru sebagian yang ketemu, pilih "Ada transaksi yang belum tercatat".',
  },
  // The dues status roster (PRD section 7.3): "view members and, for the
  // current period, who has paid / partially paid / paid in advance." A
  // period-scoped read screen only - the "belum bayar" filter is a plain list,
  // never a reminder or nudge (PRD section 7.3's explicit rule), so this
  // namespace carries no send/notify/chase copy at all.
  dues: {
    entryLink: 'Lihat status iuran',
    periodLabel: 'Periode',
    // Isolates unpaid + partial rows. Partial is included on purpose: she paid
    // something but still owes the rest, so "belum bayar" (not yet paid *in
    // full*) is true of that row - only "paid" and "paid_in_advance" are
    // settled for the period.
    unpaidFilterLabel: 'Tampilkan yang belum bayar saja',
    owedLabel: 'Iuran',
    paidLabel: 'Dibayar',
    empty: 'Belum ada anggota dengan iuran untuk periode ini.',
    emptyFiltered: 'Semua anggota sudah bayar untuk periode ini.',
    // Keyed by the schema's four status values (internal/ledger's
    // MemberDuesStatus) so the badge and any other lookup share one map, as
    // reconciliation.resolutionOptions does.
    statuses: {
      unpaid: 'Belum bayar',
      partial: 'Bayar sebagian',
      paid: 'Lunas',
      paid_in_advance: 'Lunas — sudah bayar di muka',
    },
    // Paid ahead with a known end: its own line under the amounts, naming the
    // last paid month, while the badge reads a plain "Lunas". The
    // paid_in_advance label above stays for the rare member who is ahead with
    // a gap right after this month.
    paidThrough: (period: string) => `Sudah dibayar sampai ${period}`,
    // The twelve-month grid, a second view of this screen (ADR-044). Status
    // words are `statuses` above; this holds what is new. Short month heads are
    // the one place a month is abbreviated.
    grid: {
      heading: 'Status iuran 12 bulan',
      range: (from: string, to: string) => `${from} \u2013 ${to}`,
      viewOneMonth: 'Satu bulan',
      viewTwelveMonths: '12 bulan',
      none: 'Tidak ada iuran',
      empty: 'Belum ada anggota dengan iuran dalam 12 bulan terakhir.',
      monthHeads: ['Jan', 'Feb', 'Mar', 'Apr', 'Mei', 'Jun', 'Jul', 'Agu', 'Sep', 'Okt', 'Nov', 'Des'],
      // Visually hidden label on each cell: "Oktober 2025: Lunas".
      cellLabel: (period: string, status: string) => `${period}: ${status}`,
    },
    // The matrix is its own screen, opened from Riwayat's Iuran tab, so back
    // returns to that tab rather than home. Just "Kembali": the screen's
    // heading already names where it leads, and a longer label did not fit
    // half of a 375px action row.
    back: 'Kembali',
    // Recording a dues payment (PRD section 7.3). Two ways in: Catat's fourth
    // Jenis, Iuran - a dues payment moves money, and Catat is where money
    // moving is recorded (ADR-032) - and this button on the status matrix,
    // since paying often starts from "who hasn't paid".
    recordLink: 'Catat pembayaran',
    payment: {
      heading: 'Catat pembayaran iuran',
      memberLabel: 'Anggota',
      memberPlaceholder: 'Pilih anggota',
      // The periods this member still owes for, oldest first, each with the
      // rate in effect for that month already filled in.
      periodsHeading: 'Periode yang dibayar',
      noMemberYet: 'Pilih anggota dulu untuk melihat iuran yang belum dibayar.',
      // A normal, good answer - never phrased as an error or an empty failure:
      // this member is square.
      noOutstanding: 'Tidak ada iuran tertunggak untuk anggota ini.',
      // Paying ahead: each tap adds the next month to the list, named outright
      // so she knows which month she is adding.
      addMonth: (period: string) => `Tambah ${period}`,
      // Shown on a period paid in part, so the pre-filled amount is the sisa
      // (the rest), not the whole month's rate.
      remainingLabel: 'Sisa',
      // One amount field per period, so each label names its month, as
      // reconciliation.actualLabel does per location.
      amountLabel: (period: string) => `Jumlah — ${period}`,
      totalLabel: 'Total dibayar',
      locationLabel: 'Lokasi',
      dateLabel: 'Tanggal',
      submit: 'Simpan',
      submitting: 'Menyimpan…',
      // As record.cancel: installed standalone, there is no browser back
      // button.
      cancel: 'Batal',
      success: 'Pembayaran iuran tercatat.',
    },
    // Undoing a dues payment recorded in error (PRD section 7.3). A payment is
    // never edited away: the reversal is a new entry, so this copy says
    // "batalkan" (undo it with another entry), never "hapus".
    history: {
      // One stable label on a disclosure, not a show/hide swap: open/closed is
      // carried by aria-expanded and the chevron, and a control whose name
      // changes under the reader's cursor is harder to follow.
      title: 'Pembayaran',
      empty: 'Belum ada pembayaran untuk periode ini.',
      // The reversal row, listed alongside the payment it undoes.
      reversalRow: 'Pembatalan',
      reversedBadge: 'Sudah dibatalkan',
      reverse: 'Batalkan',
      noteLabel: 'Alasan (opsional)',
      // A yes to the question the open card is asking - short enough to share
      // one action row with Batal at 375px.
      confirm: 'Ya, batalkan',
      submitting: 'Membatalkan…',
      cancel: 'Jangan jadi',
      success: 'Pembayaran dibatalkan.',
    },
  },
  // The roster and the dues tiers, on their own screen rather than as more
  // sections of Pengaturan: a fund's whole membership is a list, not a
  // setting, and would have buried the locations and titipan below a scroll of
  // names. A member's tier is set on the member, which is why the tiers live
  // here and not with the dues status view.
  members: {
    heading: 'Anggota',
    // Card list, dialog editing, keyset-paged, server-side search (ADR-032
    // "Lists: paging and search"). The shape settings.locations uses: the
    // whole card opens the edit dialog, which carries rename, the
    // deactivate/reinstate pair and delete, confirmed inline in the same
    // footer rather than a second dialog.
    roster: {
      heading: 'Daftar anggota',
      body: 'Semua yang ikut iuran kas ini. Anggota yang sudah tidak ikut lagi cukup dinonaktifkan — catatan lamanya tetap utuh.',
      empty: 'Belum ada anggota.',
      // Search finds nothing - a real answer, not a failure (the wording idiom
      // of history.transactions.noResults).
      noResults: (q: string) => `Tidak ada anggota yang cocok dengan "${q}".`,
      searchLabel: 'Cari anggota',
      searchPlaceholder: 'Nama anggota',
      loadMore: 'Muat lebih banyak',
      nameLabel: 'Nama anggota',
      tierLabel: 'Kategori',
      // The "no tier" option: a member with no tier owes no dues, a real state
      // (PRD section 6), not a blank to be filled in later.
      tierNone: 'Tanpa kategori',
      // A tier that exists but has no rate effective yet (PRD section 6's
      // undecided-rate) - never an invented amount.
      noRateYet: 'Nominal belum ditentukan',
      // The row's second line: tier name and what it costs this month, joined
      // by a middle dot - copy/id.ts is the one file the ASCII rule exempts
      // (CLAUDE.md rule 10), so the dot is written directly.
      tierRateLine: (tierLabel: string, rateLabel: string) => `${tierLabel} · ${rateLabel}`,
      // The Tunggakan badge (ADR-032's "The roster row, and a word that does
      // not exist yet"): absent when arrears_months is 0, otherwise this - a
      // count, never the current period's vocabulary (dues.statuses above).
      // "Tunggakan" is PRD-ID's word, not a coinage.
      arrearsBadge: (months: number) => `Tunggakan ${months} bulan`,
      joinedOnLabel: 'Mulai ikut',
      // ADR-040: from which month a changed tier holds. The same two words
      // as a rate's start month (settings.tiers.effectiveFromLabel).
      tierFromLabel: 'Mulai bulan',
      add: 'Tambah anggota',
      adding: 'Menambahkan…',
      // The whole card is the edit affordance (as settings.locations), so
      // "Ubah" lives on the dialog it opens rather than on a button beside the
      // card.
      editTitle: 'Ubah anggota',
      // Read before the card's visible text (a visually hidden prefix), so the
      // button's name is "Ubah <label> <value>" and holds every visible word.
      editVerb: 'Ubah',
      save: 'Simpan',
      saving: 'Menyimpan…',
      cancel: 'Batal',
      deactivate: 'Nonaktifkan',
      deactivating: 'Menonaktifkan…',
      // The consequence, named in terracotta and never alarm-red, right above
      // the dialog footer that asks for the confirm - a retired member and a
      // deleted one are different things (see settings.locations
      // .deactivateConfirm).
      deactivateConfirm:
        'Anggota ini tidak akan muncul lagi saat mencatat iuran baru. Riwayatnya tetap utuh, dan bisa diaktifkan lagi kapan saja.',
      deactivateConfirmAction: 'Ya, nonaktifkan',
      reinstate: 'Aktifkan lagi',
      reinstating: 'Mengaktifkan…',
      inactiveBadge: 'Tidak aktif',
      delete: 'Hapus',
      deleting: 'Menghapus…',
      deleteConfirm: 'Anggota ini akan dihapus selamanya. Hanya bisa untuk anggota yang belum pernah punya catatan apa pun.',
      deleteConfirmAction: 'Ya, hapus',
      // The 409 from a member who already has posted history: a refusal that
      // points at the right action, as the locations section's does.
      deleteRefused: 'Anggota ini sudah punya catatan — nonaktifkan saja, jangan dihapus.',
    },
  },
  // One bundled settings screen, not four top-level destinations: naming a
  // location, retiring one, and adding a pass-through purpose are all rare
  // next to the everyday loop.
  settings: {
    heading: 'Pengaturan',
    // The running build, under the fund name in the header (AppVersion.tsx). A
    // product name and a version string, so nothing here needs translating.
    versionLine: (version: string) => `Uruni ${version}`,
    // Renaming the kas (PRD section 7.1's promise: "bisa diganti nanti kalau
    // perlu"). The name is a label - it heads every screen and the public
    // report - so this changes nothing already recorded.
    fund: {
      heading: 'Kas ini',
      body: 'Nama kas muncul di seluruh aplikasi dan di laporan publik; zona waktunya menentukan kapan hari dan bulan berganti. Menggantinya tidak mengubah catatan apa pun.',
      nameLabel: 'Nama kas',
      save: 'Simpan',
      saving: 'Menyimpan…',
      cancel: 'Batal',
      // The whole card is the edit affordance, so the word for the action
      // lives on the dialog it opens. There is one kas, so the card never
      // needs to say which one.
      editTitle: 'Ubah nama kas',
      // Read before the card's visible text (a visually hidden prefix), so the
      // button's name is "Ubah <label> <value>" and holds every visible word.
      editVerb: 'Ubah',
      // The fund's second setting (ADR-041): a card opening a dialog at
      // ?edit=fund:time_zone, showing the zone's label from copy.timeZone.options.
      zone: {
        editTitle: 'Ubah zona waktu',
        body: 'Dipakai untuk tanggal hari ini, pergantian bulan, dan laporan publik. Menggantinya tidak mengubah catatan yang sudah ada.',
        editVerb: 'Ubah',
      },
    },
    // The treasurer's own login (#386; "user" as CONTEXT.md names the login, not
    // an account, which is a place money sits): who is signed in, and the two edits it
    // offers. Each edit asks for the current password and signs the other
    // devices out, which the notes say before she commits to it. The mismatch
    // is caught in the browser; a too-short password reuses auth.register's
    // sentence, and a wrong current password is common.errors'.
    user: {
      heading: 'Akun bendahara',
      body: (email: string) => `Masuk sebagai ${email}`,
      changeEmail: 'Ubah email',
      changePassword: 'Ubah kata sandi',
      email: {
        title: 'Ubah email',
        newLabel: 'Email baru',
        currentLabel: 'Kata sandi saat ini',
        note: 'Setelah ini, masuk dengan email baru. Perangkat lain yang sedang masuk akan keluar otomatis.',
        demoLocked: demoLoginLocked,
        save: 'Simpan email',
        saving: 'Menyimpan…',
        success: 'Email sudah diganti.',
      },
      password: {
        title: 'Ubah kata sandi',
        currentLabel: 'Kata sandi saat ini',
        newLabel: 'Kata sandi baru',
        repeatLabel: 'Ulangi kata sandi baru',
        note: 'Perangkat lain yang sedang masuk akan keluar otomatis.',
        demoLocked: demoLoginLocked,
        save: 'Simpan kata sandi',
        saving: 'Menyimpan…',
        success: 'Kata sandi sudah diganti.',
        mismatch: 'Kedua kata sandi baru belum sama.',
      },
    },
    // The public report's link card (ADR-035): heading, Salin, Bagikan and the
    // confirm sentence, plus the smallest wording the card needs.
    report: {
      heading: 'Laporan publik',
      body: 'Siapa pun yang memegang tautan ini bisa membaca laporan kas tanpa perlu masuk.',
      linkLabel: 'Tautan laporan publik',
      copy: 'Salin',
      copied: 'Tersalin',
      share: 'Bagikan',
      // The title Bagikan hands the phone's share sheet.
      shareTitle: 'Laporan kas',
      open: 'Buka laporan',
      // The monthly PDF: the current month, the words of the link in the
      // report's footer.
      downloadPdf: 'Unduh PDF',
      renew: 'Buat tautan baru',
      renewing: 'Membuat…',
      // The consequence, in terracotta (ADR-032).
      renewConfirm: 'Tautan lama berhenti bekerja. Siapa pun yang memegangnya harus diberi tautan baru.',
      renewConfirmAction: 'Ya, buat tautan baru',
      cancel: 'Batal',
    },
    locations: {
      heading: 'Lokasi penyimpanan',
      body: 'Tempat uang kas disimpan — tunai atau rekening. Nama dan jenisnya bisa diubah kapan saja; lokasi yang sudah tidak dipakai bisa dinonaktifkan, catatannya tetap utuh.',
      empty: 'Belum ada lokasi.',
      kindLabel: 'Jenis',
      kindCash: 'Tunai',
      kindBank: 'Bank',
      nameLabel: 'Nama lokasi',
      // An opening balance can only be set the moment a location is born (add
      // dialog only, never the edit dialog) - the hint says so rather than
      // leaving her to wonder where it went once the location exists.
      openingBalanceLabel: 'Saldo awal (opsional)',
      openingBalanceHint: 'Uang yang sudah ada di lokasi ini sekarang. Hanya bisa diisi saat menambah lokasi.',
      add: 'Tambah lokasi',
      adding: 'Menambahkan…',
      // The whole card is the edit affordance - no separate "Ubah" button, so
      // its label lives on the dialog it opens.
      editTitle: 'Ubah lokasi',
      // Read before the card's visible text (a visually hidden prefix), so the
      // button's name is "Ubah <label> <value>" and holds every visible word.
      editVerb: 'Ubah',
      save: 'Simpan',
      saving: 'Menyimpan…',
      cancel: 'Batal',
      deactivate: 'Nonaktifkan',
      deactivating: 'Menonaktifkan…',
      // The inline confirm swapped into the edit dialog's footer (ADR-032) -
      // never window.confirm(), and the consequence is named in terracotta,
      // not alarm-red: a retired location and a deleted one are different
      // things.
      deactivateConfirm:
        'Lokasi ini tidak akan muncul lagi saat mencatat atau cek kas. Riwayatnya tetap utuh, dan bisa diaktifkan lagi kapan saja.',
      deactivateConfirmAction: 'Ya, nonaktifkan',
      reinstate: 'Aktifkan lagi',
      reinstating: 'Mengaktifkan…',
      inactiveBadge: 'Tidak aktif',
      delete: 'Hapus',
      deleting: 'Menghapus…',
      deleteConfirm: 'Lokasi ini akan dihapus selamanya. Hanya bisa untuk lokasi yang belum pernah dipakai mencatat apa pun.',
      deleteConfirmAction: 'Ya, hapus',
      // The 409 the server answers once anything references the location. Not
      // phrased as a failure: deactivating is what she wants, and this points
      // her at it.
      deleteRefused: 'Lokasi ini sudah punya riwayat — nonaktifkan saja, jangan dihapus.',
    },
    passThrough: {
      heading: 'Titipan',
      body: 'Uang yang dikumpulkan untuk diteruskan ke pihak lain. Bukan milik kas ini, hanya lewat. Namanya bisa diperbaiki kapan saja.',
      empty: 'Belum ada titipan.',
      nameLabel: 'Nama titipan',
      add: 'Tambah titipan',
      adding: 'Menambahkan…',
      // The locations section's pair of words, for the same reason: a name is a
      // label, not catatan yang sudah tercatat. The whole card is the edit
      // affordance, so "Ubah" is the dialog's title and the card's accessible
      // name, not a button beside it.
      editTitle: 'Ubah titipan',
      // Read before the card's visible text (a visually hidden prefix), so the
      // button's name is "Ubah <label> <value>" and holds every visible word.
      editVerb: 'Ubah',
      save: 'Simpan',
      saving: 'Menyimpan…',
      cancel: 'Batal',
    },
    tiers: {
      heading: 'Kategori iuran',
      body: 'Besar iuran per bulan menurut kategori. Nominal baru ditambahkan mulai bulan tertentu, jadi bulan-bulan sebelumnya tetap memakai nominal lama. Memperbaiki nominal mengubah semua bulan yang dipakainya.',
      empty: 'Belum ada kategori.',
      nameLabel: 'Nama kategori',
      add: 'Tambah kategori',
      adding: 'Menambahkan…',
      // The card opens the tier's screen, as an Amplop card opens its
      // envelope: a tier holds a name AND a price history, and a dialog has
      // neither the height for the second nor one unambiguous way out of
      // correcting a row inside it. The card still answers what it costs this
      // month without going in.
      cardVerb: 'Atur',
      // The screen's way back. Pengaturan is where it was opened from, and the
      // only place it is reached from - as the envelope detail's back control.
      backToSettings: 'Kembali ke pengaturan',
      // The heading over the name field on that screen. The screen's h1 is the
      // tier's name, so this names the field's purpose rather than
      // repeating it.
      nameHeading: 'Nama',
      save: 'Simpan',
      saving: 'Menyimpan…',
      cancel: 'Batal',
      // On the tier's screen the name and a rate correction are both
      // saveable at once, so a bare "Simpan" twice would leave two identical
      // buttons - ambiguous read aloud and to a test query. Each names what it
      // saves.
      saveName: 'Simpan nama',
      saveRate: 'Simpan nominal',
      // Deleting a tier. Only for one nobody was ever put in - a setup typo,
      // a tier named twice - so the confirm says what goes with it, and the
      // refusal points at the reason rather than restating the wire message
      // (ADR-014). The locations section's pair of words.
      delete: 'Hapus kategori',
      deleting: 'Menghapus…',
      deleteConfirm:
        'Kategori ini akan dihapus selamanya, beserta riwayat nominalnya. Hanya bisa untuk kategori yang belum pernah dipakai anggota mana pun.',
      deleteConfirmAction: 'Ya, hapus',
      deleteRefused: 'Kategori ini pernah dipakai anggota, jadi tidak bisa dihapus. Kalau perlu, ganti saja namanya.',
      // A tier with no rate yet is an ordinary state, not a problem.
      noRates: 'Nominal belum ditentukan.',
      ratesHeading: 'Nominal',
      rateAmountLabel: 'Besar iuran per bulan',
      effectiveFromLabel: 'Mulai bulan',
      addRate: 'Tambah nominal',
      addingRate: 'Menambahkan…',
      // The heading over the new-rate fields, so the history above and the
      // thing being added below are told apart without a bordered box.
      addRateHeading: 'Nominal baru',
      editRate: 'Perbaiki nominal',
      // The step before a correction: it reprices every month the rate covers (ADR-040),
      // so it names that span first. span is rateSpanRange, rateSpanOpen or one bare month.
      rateCorrectConfirm: (oldAmount: string, newAmount: string, span: string) =>
        `Nominal ${oldAmount} diganti ${newAmount} untuk ${span}. Pembayaran yang sudah tercatat tidak berubah, tetapi status iuran bulan-bulan itu bisa ikut berubah.`,
      rateCorrectConfirmAction: 'Ya, perbaiki',
      rateCorrecting: 'Memperbaiki…',
      // The months a corrected rate covers, from its own month to the month
      // before the next rate starts. Open-ended when no later rate stands.
      rateSpanRange: (from: string, to: string) => `${from} sampai ${to}`,
      rateSpanOpen: (from: string) => `${from} seterusnya`,
      deleteRate: 'Hapus nominal',
      deletingRate: 'Menghapus…',
      // From effective_from onward, since a rate has no end date - the next
      // one starting is what ends it.
      effectiveFrom: (period: string) => `Mulai ${period}`,
    },
    // Where a new envelope is opened (ADR-032 "an incidental is a purpose, so
    // it is opened in Pengaturan"): incidental and pass-through are two kinds
    // of one purpose (CONTEXT.md), so this section sits beside Titipan rather
    // than duplicating the retired /incidentals list screen. The word is
    // "amplop", here and everywhere (CONTEXT.md, one word per concept) -
    // "Kegiatan insidental" would be the Indonesian rendering of the English
    // identifier `incidental`, not a word the treasurer uses. Identifiers,
    // routes and the schema stay `incidental` (ADR-014).
    incidentals: {
      heading: 'Amplop',
      body: 'Amplop terpisah untuk acara sekali jalan — kumpulkan, pakai, lalu tutup. Amplop yang sudah ditutup tetap tercatat, dan bisa dibuka lagi.',
      empty: 'Belum ada amplop dibuka.',
      // Closed envelopes pile up over the years, so Pengaturan lists only the
      // open ones and gathers the rest behind this one row - also the heading
      // of the screen it opens, so the two read the same.
      closedRow: 'Amplop yang sudah ditutup',
      closedEmpty: 'Belum ada amplop yang ditutup.',
      // Each closed envelope's line on that screen: when it was closed, the one
      // fact that orders the list. date is already formatted.
      closedOn: (date: string) => `Ditutup ${date}`,
      // The dialog heading's word (copy.incidentals.open.heading) - one word
      // for "start a new envelope", not two.
      add: 'Buka amplop',
      cardVerb: 'Lihat',
    },
    // The whole fund as one file (ADR-012), plus - ADR-013 - the list of
    // copies the server already keeps once a day. Two things: download now
    // (heading/body/download/downloading) and browse what exists (autoHeading
    // onward) beneath it.
    backup: {
      heading: 'Cadangan',
      // What the file holds, and why that means keeping it somewhere safe.
      // "info login" rather than spelling out "kata sandi" - the file carries
      // the password hash, not the password, and this line is a caution, not an
      // inventory of the zip's contents.
      body: 'Satu file berisi seluruh catatan kas — termasuk info login. Simpan di tempat yang aman.',
      download: 'Unduh cadangan',
      downloading: 'Menyiapkan…',
      // The auto-list's small heading, distinct enough from "Cadangan" above it
      // that download-now and browse-what-exists read as separate action rows.
      autoHeading: 'Cadangan otomatis',
      autoBody: 'Uruni menyimpan cadangan baru setiap hari, kalau ada perubahan.',
      // Every dump's kind (internal/http/backup.go's backupListItem) - "harian"
      // for the daily scheduled copy, "sebelum pemulihan" for the safety net
      // taken right before a restore.
      kindDaily: 'Harian',
      kindPreRestore: 'Sebelum pemulihan',
      // An older-format dump (ADR-012's compatibility rule): listed and
      // downloadable, never offered for restore. This label explains why one
      // row has no restore button.
      olderFormat: 'Dibuat versi lama',
      empty: 'Belum ada cadangan otomatis.',
      // The row's download control - plain ink, not a button per row (review
      // standard against an outlined button on every row) - so it needs an
      // aria-label naming which dump it downloads, as any icon-only control
      // does for its dense list.
      downloadRowAria: (kind: string, date: string) => `Unduh cadangan ${kind} ${date}`,
      // The row's "pulihkan" control, plain ink beside the download one,
      // present only on a current-format row; aria-named like downloadRowAria.
      restoreRowAria: (kind: string, date: string) => `Pulihkan cadangan ${kind} ${date}`,
      // The button that starts the upload+restore flow. A plain outline
      // button, the visual weight of "Unduh cadangan" above it - restoring is
      // not the everyday action this card exists for, nor a destructive-red
      // one, since the safety net and the totals check (ADR-012) make it safe
      // to offer plainly.
      restoreLabel: 'Pulihkan dari cadangan',
      inspecting: 'Memeriksa file…',
    },
  },
  // The confirm dialog restoring from an uploaded backup opens into (ADR-012),
  // once the upload has been checked and the server has answered with a
  // preview. Every fact shown is read off that preview - the dialog never
  // recomputes or guesses at what the file holds.
  //
  // Per-fund lines (ADR-012's amendment): a fund present on both sides either
  // keeps everything (fundKeptSafe) or names what would be lost past the file's
  // cutoff date for it (fundKeptWithCutoff) - or, when the file carries no
  // transaction for it yet, without a date (fundKeptNoCutoffDate); a live-only
  // fund is removed outright; a file-only fund is added.
  restoreConfirm: {
    heading: 'Pulihkan dari cadangan',
    dateLabel: (date: string) => `Cadangan dari ${date}`,
    totalLabel: (total: string) => `Total seluruh dana: ${total}`,
    fundKeptSafe: (name: string) => `${name}: semua transaksi tetap ada`,
    fundKeptWithCutoff: (name: string, count: number, date: string) => `${name}: ${count} transaksi setelah ${date} akan hilang`,
    fundKeptNoCutoffDate: (name: string, count: number) => `${name}: ${count} transaksi akan hilang`,
    fundRemoved: (name: string) => `${name}: akan dihapus`,
    fundAdded: (name: string) => `${name}: akan ditambahkan`,
    // Said once, plainly, before the password field - the one thing every
    // treasurer confirming this needs to know, not tucked into a tooltip.
    // "keluar otomatis" over "logout": CONTEXT.md has no term for it, and it is
    // what happens, in plain words.
    warning: 'Memulihkan akan mengganti seluruh catatan kas dengan isi file ini. Semua orang yang sedang masuk akan keluar otomatis.',
    passwordLabel: 'Kata sandi akun di server ini',
    confirm: 'Pulihkan',
    confirming: 'Memulihkan…',
    cancel: 'Batal',
  },
  // Correcting a posted row's pos (ADR-033): the marker on a corrected row, and
  // the one-field dialog that does the correcting. The word is "pos"
  // throughout (CONTEXT.md: one word per concept), never "reklasifikasi",
  // which is journal vocabulary the treasurer does not use.
  // The shared date pickers (components/DateField + MonthField). The chosen
  // date renders through lib/dates, so these are only the empty states and the
  // month picker's year arrows.
  dateField: {
    empty: 'Pilih tanggal',
    emptyMonth: 'Pilih bulan',
    previousYear: 'Tahun sebelumnya',
    nextYear: 'Tahun berikutnya',
    // Under the date of every form that posts a dated entry when the date is
    // before the opening balance (PRD section 7.1: history starts at adoption,
    // and the opening balance carries everything before it). Never blocks - she
    // may mean it - so it asks her to look and names the likelier reading.
    // tanggal arrives formatted (lib/dates formatIsoDate). Two forms: the
    // location's own opening, or the fund's earliest when the form has no
    // location or the chosen one has no opening entry.
    beforeOpening: (lokasi: string, tanggal: string) =>
      `Tanggal ini sebelum saldo awal ${lokasi} (${tanggal}). Kalau uang ini sudah termasuk di saldo awal, tidak perlu dicatat lagi.`,
    beforeOpeningFund: (tanggal: string) =>
      `Tanggal ini sebelum saldo awal kas (${tanggal}). Kalau uang ini sudah termasuk di saldo awal, tidak perlu dicatat lagi.`,
  },
  // The fund's time zone (ADR-041): the one place the app says WIB, WITA or WIT.
  // The server stores IANA names (lib/timeZones.ts). The abbreviation also
  // rides a displayed time of day ("pukul 13.42 WIB"), not Intl's zone names.
  timeZone: {
    label: 'Zona waktu',
    setupHint: 'Menentukan kapan hari dan bulan berganti untuk kas ini. Bisa diganti nanti di Pengaturan.',
    options: {
      'Asia/Jakarta': {
        abbreviation: 'WIB',
        label: 'WIB - Waktu Indonesia Barat',
        example: 'mis. Jakarta, Medan, Palembang',
      },
      'Asia/Makassar': {
        abbreviation: 'WITA',
        label: 'WITA - Waktu Indonesia Tengah',
        example: 'mis. Makassar, Denpasar, Balikpapan',
      },
      'Asia/Jayapura': {
        abbreviation: 'WIT',
        label: 'WIT - Waktu Indonesia Timur',
        example: 'mis. Jayapura, Ambon',
      },
    },
  },
  // One ledger entry read in full - opened by tapping a card in any
  // transaction list. Field labels reuse the record form's nouns (CONTEXT.md:
  // one word per concept); "Dicatat pada" is the one new one, for when the
  // entry was recorded, as opposed to its Tanggal.
  transactionDetail: {
    heading: 'Rincian transaksi',
    // Names the card's full-width tap target for a screen reader, with the
    // row's facts, since a list of twenty-five identical "lihat rincian"
    // controls tells her nothing (as purposeCorrection.controlAria).
    openAria: (purposeName: string, amount: string, date: string) => `Lihat rincian: ${purposeName}, ${amount}, ${date}`,
    directionIn: 'Uang masuk',
    directionOut: 'Uang keluar',
    purposeLabel: 'Pos',
    noteLabel: 'Catatan',
    locationLabel: 'Lokasi',
    dateLabel: 'Tanggal',
    recordedAtLabel: 'Dicatat pada',
    receiptsLabel: 'Foto nota',
  },
  purposeCorrection: {
    // The control the pos becomes on an eligible row. Names the row it would
    // correct, because a screen reader otherwise meets it as one of
    // twenty-five identical-sounding controls.
    controlAria: (purposeName: string) => `Perbaiki pos: ${purposeName}`,
    // Read out beside the muted icon on a row a correction already points at.
    // Never rendered visibly - the glyph is the visible half (the icon/srWord
    // split).
    corrected: 'Sudah diperbaiki',
    heading: 'Perbaiki pos',
    // Shown above the picker when the row has been corrected once: the tag the
    // money is under NOW, which is not the tag the row still shows. Without it
    // the dialog and the row underneath appear to disagree (ADR-033).
    currentLabel: (purposeName: string) => `Pos saat ini: ${purposeName}`,
    pickerLabel: 'Pos yang benar',
    // Says what the correction does and does not do: "memindahkan pos" is the
    // one action in this app that posts rows without moving money, and a
    // treasurer anxious about the numbers deserves to be told before she taps.
    explainer: 'Saldo kas tidak berubah - hanya posnya yang dipindahkan. Transaksi aslinya tetap tercatat apa adanya.',
    save: 'Perbaiki',
    saving: 'Memperbaiki…',
    cancel: 'Batal',
    success: 'Pos sudah diperbaiki.',
  },
  // Riwayat's Talangan tab (PRD section 7.4; under Riwayat per ADR-032;
  // "Talangan" is the user-facing word, while identifiers and the
  // reimbursement table stay English): record that a member fronted money,
  // settle when repaid, waive when the member forgives the debt ("putihkan"),
  // or correct/remove a claim entered wrongly - only until settled, after
  // which the payout is a posted ledger row. heading also names the tab's
  // tablist (aria-label) - the tab has no on-screen `<h1>`, Riwayat's does.
  //
  // "Putihkan" over the PRD's conversational example ("sudah, saya yang
  // tanggung"): the example was prose flavour, not interface copy. The word
  // sits in the debt/forgiveness register without the legal-verdict
  // connotation of "ampuni".
  reimbursements: {
    heading: 'Talangan',
    body: 'Uang yang ditanggung anggota untuk kas, sudah tercatat.',
    // Ticked by default: the tab opens on claims still owed. The same plain
    // checkbox as dues.unpaidFilterLabel - one list narrowed, not two views.
    outstandingFilterLabel: 'Tampilkan yang belum dibayar saja',
    emptyOutstanding: 'Tidak ada talangan yang belum dibayar.',
    emptyAll: 'Belum ada talangan tercatat.',
    record: {
      heading: 'Catat talangan',
      memberLabel: 'Anggota',
      memberPlaceholder: 'Pilih anggota',
      purposeLabel: 'Pos',
      amountLabel: 'Jumlah',
      dateLabel: 'Tanggal pengeluaran',
      noteLabel: 'Catatan (opsional)',
    },
    settle: {
      heading: 'Bayar talangan',
      accountLabel: 'Bayar dari',
      dateLabel: 'Tanggal pembayaran',
      submit: 'Bayar',
      submitting: 'Membayar…',
      cancel: 'Batal',
      success: 'Talangan sudah dibayar.',
    },
    actions: {
      settle: 'Bayar',
      // The row's "more" menu holding Perbaiki, Putihkan and Hapus - named like
      // the receipt viewer's menu (receipts.photoMenuAria).
      menuAria: 'Pilihan talangan',
      waive: 'Putihkan',
      unwaive: 'Batalkan pemutihan',
      correct: 'Perbaiki',
      delete: 'Hapus',
      deleting: 'Menghapus…',
    },
    status: {
      outstanding: 'Belum dibayar',
      waived: 'Diputihkan',
      settled: 'Dibayar',
    },
    waive: {
      success: 'Talangan sudah diputihkan.',
    },
    unwaive: {
      success: 'Pemutihan dibatalkan — talangan kembali ditagih.',
    },
    delete: {
      success: 'Talangan dihapus.',
    },
    correct: {
      heading: 'Perbaiki talangan',
      submit: 'Simpan',
      submitting: 'Menyimpan…',
      cancel: 'Batal',
      success: 'Talangan berhasil diperbaiki.',
    },
    errors: {
      reimbursement_already_settled: 'Talangan ini sudah dibayar.',
      reimbursement_waived: 'Talangan ini sudah diputihkan.',
      // DELETE /api/reimbursements/{id} refuses a claim that still has a
      // receipt attached - the shared common.errors wording ("dipakai di
      // catatan lain") is true but not actionable here; this names the fix.
      referenced_by_other_records: 'Hapus foto notanya dulu sebelum menghapus talangan ini.',
    },
  },
  // The envelope's detail screen (its list moved to copy.settings.incidentals;
  // the detail view titles itself with the occasion instead).
  incidentals: {
    open: {
      heading: 'Buka amplop',
      occasionLabel: 'Acara',
      occasionPlaceholder: 'Mis. Halal bihalal RT',
      targetLabel: 'Target dana (opsional)',
      dateLabel: 'Tanggal dibuka',
      // ADR-034: one figure every expected member is asked to give - not a dues
      // rate, no tiers, no effective dates, the same nullable shape targetLabel
      // carries. Shared by the open dialog and the edit dialog below
      // (copy.incidentals.rename reuses these keys): one wording for the same
      // two fields, opening or correcting.
      minimumLabel: 'Sumbangan minimal per anggota (opsional)',
      // Who the envelope is for (ADR-034) - the sick, the bereaved family, the
      // birthday pair - never expected to contribute themselves. Zero or more,
      // always members.
      recipientsLabel: 'Untuk siapa amplop ini? (opsional)',
      recipientsEmpty: 'Belum ada anggota aktif.',
      submit: 'Buka',
      submitting: 'Membuka…',
      cancel: 'Batal',
    },
    detail: {
      targetLabel: 'Target',
      collectedLabel: 'Terkumpul',
      disbursedLabel: 'Terpakai',
      // What the envelope holds now - PRD-ID's "sisa" for an envelope's
      // leftover (section 7.5). Rp0 once closed: the leftover rolled out.
      remainingLabel: 'Sisa',
      // The way back follows where the detail was opened from: Beranda's
      // per-pos list or a card in Pengaturan. Pengaturan is also the label
      // for a deep link or a reload, which carry no origin.
      backToSettings: 'Kembali ke pengaturan',
      backToHome: 'Kembali ke beranda',
    },
    close: {
      heading: 'Tutup amplop',
      // Direction-neutral (ADR-031): closing moves money either way - leftover
      // out to Kas Utama, or a shortfall covered from it - so a label naming
      // one direction ("Sisa dana masuk ke") would be true half the time.
      accountLabel: 'Akun untuk pemindahan dana',
      dateLabel: 'Tanggal ditutup',
      noteLabel: 'Catatan (opsional)',
      notePlaceholder: 'Mis. sisa dana halal bihalal',
      submit: 'Tutup',
      submitting: 'Menutup…',
      cancel: 'Batal',
      success: 'Amplop sudah ditutup.',
      // Shown after a close, honestly - the amount is rendered even at zero
      // (ADR-031's "shown honestly, including zero"), and this sentence says
      // which way it went: rolled_amount is signed - positive rolled out,
      // negative covered from Kas Utama, zero landed square.
      rolledLabel: (rolledAmount: number) =>
        rolledAmount > 0
          ? 'Sisa dana digulung ke Kas Utama'
          : rolledAmount < 0
            ? 'Kekurangan ditutup dari Kas Utama'
            : 'Amplop pas, tidak ada sisa dana',
    },
    actions: {
      // Contributions and disbursements are one entry point into the real
      // record form (RecordTransaction.tsx) - direction is decided there, by
      // its in/out toggle, not by two buttons here. One word each, like the
      // footer's "Catat": on the envelope's screen the object is implied, and
      // three buttons share 375px. The way into Riwayat is the activity
      // section's "Lihat semua".
      record: 'Catat',
      close: 'Tutup',
      // The way back from a closed envelope (ADR-031) - a late entry needs
      // somewhere to post, through the ordinary record form. Two words, like
      // its neighbours in the row.
      reopen: 'Buka lagi',
      // Correcting a mistyped occasion and the envelope's minimum and
      // recipients (ADR-034): one dialog for everything on the envelope that is
      // editable like the occasion, never a posted fact. Offered for an open AND
      // a closed envelope, since a typo is usually found after the occasion is
      // over, and the same holds for a minimum set too high or a recipient added
      // late. Just the verb: it shares a row with Tutup and Catat, and "Ubah
      // amplop" left it 6px of padding a side. The dialog keeps the full name.
      rename: 'Ubah',
    },
    reopen: {
      success: 'Amplop dibuka kembali.',
    },
    // The edit dialog (ADR-034): occasion, minimum and recipients together -
    // nameLabel reuses open.occasionLabel's word ("Acara"), and
    // minimumLabel/recipientsLabel/recipientsEmpty reuse the open dialog's keys
    // verbatim, since both dialogs edit the same fields at different points in
    // the envelope's life.
    rename: {
      heading: 'Ubah amplop',
      nameLabel: 'Acara',
      save: 'Simpan',
      saving: 'Menyimpan…',
      cancel: 'Batal',
      success: 'Amplop sudah diperbarui.',
    },
    // The participation table (ADR-034): who has contributed and how much,
    // derived from the ledger against the envelope's expectation - never
    // stored. "Kurang dari minimal" is deliberately neutral (muted, never
    // terracotta, Design-System.md) - being under a minimum is not a
    // discrepancy the way a reconciliation gap is.
    participation: {
      heading: 'Partisipasi',
      // Recipients are never expected to contribute themselves - shown once,
      // near the top of the table they are excluded from. names is already
      // joined by the caller (", " between them).
      recipientsLine: (names: string) => `Untuk: ${names}`,
      states: {
        given: 'Sudah menyumbang',
        notGiven: 'Belum menyumbang',
        underMinimum: 'Kurang dari minimal',
      },
      // Expected members with nobody contributed yet still need the heading to
      // explain the empty rows beneath it - this is not that empty state (there
      // are no expected members at all, e.g. every active member is a
      // recipient).
      noneExpected: 'Tidak ada anggota yang diharapkan menyumbang untuk amplop ini.',
      // A contribution from someone the envelope did not expect (a later
      // joiner, a recipient who gave anyway) - listed with their amount, never
      // as "belum".
      otherHeading: 'Sumbangan lain',
      // The row action (ADR-034: record a contribution with the member already
      // filled in) - navigates to Catat with the envelope's purpose and this
      // member pre-chosen.
      record: 'Catat sumbangan',
      recordAria: (memberName: string) => `Catat sumbangan dari ${memberName}`,
    },
    status: {
      open: 'Berjalan',
      closed: 'Ditutup',
    },
    errors: {
      incidental_already_closed: 'Amplop ini sudah ditutup.',
      incidental_not_closed: 'Amplop ini belum ditutup.',
    },
  },
} as const
