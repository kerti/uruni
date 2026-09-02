import { writeFileSync } from 'node:fs'
import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
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

export default defineConfig({
  plugins: [react(), tailwindcss(), keepDistPlaceholder()],
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
