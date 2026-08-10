import { writeFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vite'
import type { Plugin } from 'vite'
import react from '@vitejs/plugin-react'

/**
 * Puts back the .gitkeep that `emptyOutDir` deletes.
 *
 * The dist directory is generated and therefore ignored, all but that one file:
 * `//go:embed all:dist` needs *something* to point at, or a fresh clone will not
 * compile. Deleting it breaks `go vet` — several steps away from the frontend
 * build that did it, usually on CI rather than on the machine that caused it.
 *
 * This used to live in `make web`, which only helps the people who go through
 * make. It belongs next to the code that removes the file, so that every way of
 * running the build is covered.
 */
function keepEmbedAnchor(): Plugin {
  return {
    name: 'hypercraft:keep-embed-anchor',
    closeBundle() {
      writeFileSync(fileURLToPath(new URL('../internal/webui/dist/.gitkeep', import.meta.url)), '')
    },
  }
}

// The build output lands in the Go package that embeds it, so `go build`
// produces a binary with the UI inside.
export default defineConfig({
  plugins: [react(), keepEmbedAnchor()],
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 900,
  },
  server: {
    port: 5173,
    proxy: {
      // `npm run dev` talks to a panel started with `go run ./cmd/hypercraft`.
      '/api': { target: 'http://127.0.0.1:19190', changeOrigin: true, ws: true },
    },
  },
})
