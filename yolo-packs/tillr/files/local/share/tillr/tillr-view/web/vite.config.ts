import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// Build output lands in `dist/`, which `tillr-view serve` embeds under the `observatory`
// build tag. During development the Go server is the API and Vite is the UI, so `/api` is
// proxied to it: the front end never has a second source of data (design §5.3).
//
// `ws: true` is not optional: the live channel is an HTTP upgrade on `/api/v1/ws`, and a
// proxy without it answers the upgrade as an ordinary request, so the socket never
// connects and the screen freezes with nothing in the browser to say why.
//
// The target is the Go server's address, which `just observatory-dev <port>` moves by
// exporting `TILLR_VIEW_API`: a proxy pinned to 4318 while the server listens elsewhere is
// the same silent freeze, one port over.
//
// It is read off `globalThis` rather than `process`: this package ships no `@types/node`
// (and adding one would put Node's globals into the browser code's type space), so a bare
// `process` reference fails `npm run build`'s type check even though Vite runs it in Node.
const env = (globalThis as { process?: { env?: Record<string, string | undefined> } }).process?.env ?? {};
const apiTarget = env.TILLR_VIEW_API ?? "http://127.0.0.1:4318";

export default defineConfig({
  plugins: [react()],
  build: { outDir: "dist", emptyOutDir: true },
  server: {
    port: 5173,
    proxy: {
      "/api": { target: apiTarget, changeOrigin: false, ws: true },
    },
  },
});
