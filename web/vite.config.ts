import { existsSync, readdirSync, readFileSync, writeFileSync } from 'node:fs'
import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import licenseModule, { type Dependency, type Options as LicenseOptions } from 'rollup-plugin-license'
import { VitePWA } from 'vite-plugin-pwa'
import { configDefaults, defineConfig, type Plugin } from 'vitest/config'

import { copy } from './src/copy/id.ts'

// In production the Go binary is the single origin (ADR-001). In dev the two
// halves run apart for hot-reload, so vite proxies the server's routes - `/api`
// (JSON) and `/report` (SSR public report) - to the Go server (ADR-020). Both
// ports come from the environment, as the Makefile passes them: PORT is the Go
// server's (the same variable it listens on, default 8080), WEB_PORT is this
// dev server's (default 5173).
const goServer = `http://localhost:${process.env.PORT ?? 8080}`
const webPort = Number(process.env.WEB_PORT ?? 5173)

// Vite rejects Host headers it doesn't recognise (DNS-rebinding protection), so
// reaching the dev server through a tunnel or a LAN hostname needs that host
// allowed. It is never a *new* value: it is the host of URUNI_BASE_URL, which
// is already the origin this instance answers on (ADR-019) and reaches vite
// from .env via the Makefile's `export`. Loopback is allowed by vite itself,
// so the default `make setup` writes adds nothing.
const allowedHosts = (() => {
  const base = process.env.URUNI_BASE_URL?.trim()
  if (!base) return []
  try {
    return [new URL(base).hostname]
  } catch {
    return []
  }
})()

// A production build empties web/dist, which would delete the committed
// .gitkeep that `//go:embed all:web/dist` needs on a fresh clone - and the
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

// ADR-008's installable half (M6.7). The connection-required half - the
// "butuh koneksi" banner - is M6.2's and needs no service worker.
//
// Two rules shape everything here: the service worker caches the built shell
// and nothing else (no runtimeCaching, so no API response ever lands in a
// cache where a stale screen could read as current data), and it never
// updates the app out from under the treasurer - see registerType below.
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
      description: copy.app.tagline,
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
      // (M7), /api is JSON, /healthz is the smoke check, /third-party-notices
      // is the embedded licence text. Without this denylist
      // the service worker would answer them with index.html once installed.
      navigateFallbackDenylist: [/^\/api\//, /^\/report/, /^\/healthz$/, /^\/third-party-notices$/],
    },
  }) as Plugin[]
}

// The licence notices the bundle owes its upstreams (#521), as JSON for
// `cmd/uruni-notices` to fold into THIRD-PARTY-NOTICES. Only when
// URUNI_WEB_NOTICES_JSON names an output file: an ordinary build writes nothing.
//
// Two halves, because the bundle is built twice. The app bundle is read by
// rollup-plugin-license from the modules that actually ended up in it - not the
// lockfile, which would list dev tooling and miss nothing it ships. The service
// worker is built afterwards by vite-plugin-pwa's own workbox-build, which that
// plugin cannot see; its runtime (dist/workbox-*.js) carries a
// `workbox:<module>:<version>` banner per module, from which we read the
// packages and their LICENSE files in node_modules. Names only, never versions,
// so a routine dependency bump leaves THIRD-PARTY-NOTICES unchanged.
// The package is CommonJS with a types-only `export default`; under nodenext the
// import is the module object, so reach for `.default` when it is there.
type LicenseFactory = (options: LicenseOptions) => Plugin
const licenseExport = licenseModule as unknown as LicenseFactory | { default: LicenseFactory }
const license: LicenseFactory = typeof licenseExport === 'function' ? licenseExport : licenseExport.default

const noticesOut = process.env.URUNI_WEB_NOTICES_JSON
const bundled = new Map<string, string>()

function addPackage(name: string, text: string | null | undefined) {
  if (name && text && !bundled.has(name)) bundled.set(name, text.trim())
}

function notices(): Plugin[] {
  if (!noticesOut) return []
  const dist = path.resolve(import.meta.dirname, 'dist')
  return [
    license({
      thirdParty(dependencies: Dependency[]) {
        for (const dep of dependencies) addPackage(dep.name ?? '', dep.licenseText)
      },
    }),
    {
      name: 'uruni:notices-json',
      apply: 'build',
      enforce: 'post',
      closeBundle: {
        order: 'post',
        handler() {
          for (const file of existsSync(dist) ? readdirSync(dist) : []) {
            if (!/^workbox-[0-9a-f]+\.js$/.test(file)) continue
            const source = readFileSync(path.join(dist, file), 'utf8')
            for (const m of source.matchAll(/workbox:([a-z-]+):\d/g)) {
              const pkg = `workbox-${m[1]}`
              const licenseFile = path.resolve(import.meta.dirname, 'node_modules', pkg, 'LICENSE')
              addPackage(pkg, existsSync(licenseFile) ? readFileSync(licenseFile, 'utf8') : null)
            }
          }
          const sorted = [...bundled].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))
          writeFileSync(
            noticesOut!,
            JSON.stringify(
              sorted.map(([name, text]) => ({ name, license: text })),
              null,
              2,
            ) + '\n',
          )
        },
      },
    },
  ]
}

export default defineConfig({
  plugins: [react(), tailwindcss(), ...pwa(), keepDistPlaceholder(), ...notices()],
  resolve: {
    alias: {
      '@': path.resolve(import.meta.dirname, './src'),
    },
  },
  build: {
    rolldownOptions: {
      output: {
        codeSplitting: {
          // React, React DOM and the router in a chunk of their own: about
          // half the JavaScript, and the half that changes only when a
          // dependency is bumped. A release then re-downloads (and the
          // service worker re-precaches) only the app chunk, and neither
          // chunk crosses Vite's 500 kB warning. The total is unchanged -
          // both load at startup.
          groups: [{ name: 'react', test: /[\\/]node_modules[\\/](react|react-dom|react-router|scheduler)[\\/]/ }],
        },
      },
    },
  },
  server: {
    port: webPort,
    allowedHosts,
    proxy: {
      '/api': { target: goServer, changeOrigin: true },
      '/report': { target: goServer, changeOrigin: true },
      '/healthz': { target: goServer, changeOrigin: true },
      '/third-party-notices': { target: goServer, changeOrigin: true },
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
    // web/e2e holds Playwright specs (ADR-015's browser leg, M6.3) - a
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
