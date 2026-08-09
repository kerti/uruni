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
} as const
