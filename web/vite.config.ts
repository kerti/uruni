import { writeFileSync } from 'node:fs'
import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { VitePWA } from 'vite-plugin-pwa'
import { configDefaults, defineConfig, type Plugin } from 'vitest/config'

// In production the Go binary is the single origin (ADR-001). In dev the two
// halves run apart for hot-reload, so vite proxies the server's routes — `/api`
// (JSON) and `/report` (SSR public report) — to Go on :8080 (ADR-020).
const goServer = `http://localhost:${process.env.PORT ?? 8080}`

// A production build empties web/dist, which would delete the committed
// .gitkeep that `//go:embed all:web/dist` needs on a fresh clone — and the
// deletion would ride along in the next commit. Put the placeholder back.
function keepDistPlaceholder(): Plugin {
  return {
    name: 'uruni:keep-dist-placeholder',
    apply: 'build',
    closeBundle() {
      writeFileSync(path.resolve(import.meta.dirname, 'dist/.gitkeep'), '')
    },
  }
}

// ADR-008's installable half (M6.7). The connection-required half — the
// "butuh koneksi" banner — is M6.2's and needs no service worker.
//
// Two rules shape everything here: the service worker caches the built shell
// and nothing else (no runtimeCaching, so no API response ever lands in a
// cache where a stale screen could read as current data), and it never
// updates the app out from under the treasurer — see registerType below.
//
// The icons in public/icons/ are generated once from the brand asset, not at
// build time (CI has no rasterizer):
//
//   rsvg-convert -w 192 -h 192 docs/brand/uruni-appicon.svg -o web/public/icons/icon-192.png
//   rsvg-convert -w 512 -h 512 docs/brand/uruni-appicon.svg -o web/public/icons/icon-512.png
//   rsvg-convert -w 180 -h 180 docs/brand/uruni-appicon.svg -o web/public/icons/apple-touch-icon-180.png
//   rsvg-convert -w 512 -h 512 docs/brand/uruni-appicon-maskable.svg -o web/public/icons/icon-maskable-512.png
//
// uruni-appicon-maskable.svg is the same mark on a full-bleed Forest field,
// scaled to sit inside Android's 80% safe zone rather than being cropped by
// the mask. It lives in docs/brand/ with the other sources, not in public/,
// so it is never shipped or precached.
function pwa(): Plugin[] {
  return VitePWA({
    // 'prompt', deliberately, not 'autoUpdate'. Uruni is form-heavy - amount,
    // date and note fields open across the record, reconcile, dues and
    // reimbursement flows - and an auto-reload on a new deploy would discard a
    // half-typed transaction at exactly the wrong moment. UpdateBanner.tsx
    // offers the reload instead and lets the treasurer pick the moment.
    registerType: 'prompt',
    // UpdateBanner does the registering (virtual:pwa-register/react), so the
    // plugin must not also inject its own registration script.
    injectRegister: null,
    includeAssets: ['favicon.svg', 'icons/*.png'],
    manifest: {
      name: 'Uruni',
      short_name: 'Uruni',
      description: 'Kas bersama yang selalu cocok.',
      lang: 'id',
      start_url: '/',
      scope: '/',
      display: 'standalone',
      orientation: 'portrait',
      background_color: '#F6F4EF',
      theme_color: '#1F5D50',
      icons: [
        { src: '/icons/icon-192.png', sizes: '192x192', type: 'image/png' },
        { src: '/icons/icon-512.png', sizes: '512x512', type: 'image/png' },
        { src: '/icons/icon-maskable-512.png', sizes: '512x512', type: 'image/png', purpose: 'maskable' },
      ],
    },
    workbox: {
      // Precache the built shell only. No runtimeCaching entry exists on
      // purpose: /api/* must always reach the server, and offline must read
      // as offline (ADR-008, CLAUDE.md rule 4).
      globPatterns: ['**/*.{js,css,html,svg,png,woff2}'],
      navigateFallback: '/index.html',
      // The Go binary owns these, not the SPA: /report is server-rendered
      // (M7), /api is JSON, /healthz is the smoke check. Without this denylist
      // the service worker would answer them with index.html once installed.
      navigateFallbackDenylist: [/^\/api\//, /^\/report/, /^\/healthz$/],
    },
  }) as Plugin[]
}

export default defineConfig({
  plugins: [react(), tailwindcss(), ...pwa(), keepDistPlaceholder()],
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api': { target: goServer, changeOrigin: true },
      '/report': { target: goServer, changeOrigin: true },
      '/healthz': { target: goServer, changeOrigin: true },
    },
  },
  test: {
    environment: 'jsdom',
    globals: true,
    // virtual:pwa-register/react only exists once vite-plugin-pwa has built
    // the service worker, and jsdom has no service worker to register. Point
    // it at a stub so the unit suite can render App (and UpdateBanner) at all.
    alias: {
      'virtual:pwa-register/react': path.resolve(import.meta.dirname, './src/test/pwa-register-stub.ts'),
    },
    setupFiles: ['./src/test/setup.ts'],
    // web/e2e holds Playwright specs (ADR-015's browser leg, M6.3) — a
    // different test runner with its own config (playwright.config.ts), not
    // one of Vitest's `*.spec.ts` unit tests. Vitest's default include glob
    // would otherwise pick them up and fail importing '@playwright/test'.
    exclude: [...configDefaults.exclude, 'e2e/**'],
    coverage: {
      reporter: ['text', 'lcov'],
      reportsDirectory: './coverage',
    },
  },
})
