import { defineConfig } from "vite";

// The Go binary serves the built app itself, so `build` only has to produce
// dist/ for //go:embed. `dev` exists for frontend iteration with HMR: it
// proxies the API and WebSocket to a Go server started with --dev, which
// relaxes the Host allowlist and skips the auth token for exactly this case.
const BACKEND = "http://127.0.0.1:4111";

export default defineConfig({
  server: {
    proxy: {
      "/api": { target: BACKEND, changeOrigin: false },
      "/ws": { target: BACKEND, ws: true, changeOrigin: false },
    },
  },
  build: {
    // Fail loudly rather than silently shipping a half-built app to go:embed.
    emptyOutDir: true,
  },
});
