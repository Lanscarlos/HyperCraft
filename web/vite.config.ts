import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The build output lands in the Go package that embeds it, so `go build`
// produces a binary with the UI inside. `make web` restores the .gitkeep that
// emptyOutDir removes.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: '../internal/webui/dist',
    emptyOutDir: true,
    chunkSizeWarningLimit: 900,
  },
  server: {
    port: 5173,
    proxy: {
      // `npm run dev` talks to a panel started with `go run ./cmd/hypercraft`.
      '/api': { target: 'http://127.0.0.1:8080', changeOrigin: true, ws: true },
    },
  },
})
