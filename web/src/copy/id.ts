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
  common: {
    loading: 'Memuat…',
    offlineBanner: 'Belum tersambung — Uruni butuh koneksi.',
    retry: 'Coba lagi',
    // Wire error codes -> Indonesian copy. The server's message field stays
    // English by design (ADR-014: the API is a code surface) and never
    // reaches the treasurer; this map is what she sees instead.
    //
    // Seeded only with codes the shared states can hit this slice
    // (not_found, method_not_allowed, and a network failure). Each later
    // slice adds its own route's codes here as it lands.
    errors: {
      not_found: 'Data yang dicari tidak ditemukan.',
      method_not_allowed: 'Aksi ini tidak didukung.',
      network_error: 'Belum tersambung — Uruni butuh koneksi.',
    },
    // Shown for a code not in the map above.
    unknownError: 'Ada yang tidak beres. Coba lagi sebentar lagi.',
  },
} as const
