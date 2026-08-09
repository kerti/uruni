# ADR-008 — PWA: installable shell, no offline data

**Status:** Accepted · [ADR index](./README.md)

**Decision.** Web app manifest + a **minimal service worker** (via `vite-plugin-pwa`) that caches only the app shell and shows a clear "butuh koneksi" state offline. **No IndexedDB / offline data** (PRD 7.2).

**Consequences.** The connection-required rule erases offline-sync — the hardest part of PWAs — by construction.
