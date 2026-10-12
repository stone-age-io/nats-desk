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
  plugins: [
    {
      // dist is committed, so a build must come out the same on every OS. On
      // Windows git checks index.html out with CRLF, and when Vite strips the
      // dev-time <link> and <script> tags it takes their LF but leaves their
      // CR. Those stray CRs are enough for git to classify the result as
      // binary and commit it byte for byte, so it would change with whichever
      // machine built it last. Dropping every CR - not turning a stray one
      // into a line break - is what makes the output match an LF checkout's
      // exactly. The bundles are LF already.
      name: "lf-index-html",
      transformIndexHtml: {
        order: "post",
        handler: (html) => html.replaceAll("\r", ""),
      },
    },
  ],
});
