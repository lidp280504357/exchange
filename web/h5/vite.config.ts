import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// In development the API runs on the test server (CLAUDE.md): /v1 and the
// WebSocket are proxied there, so cookies and origins look like production.
const api = process.env.API_ORIGIN ?? "https://astras.vip";

export default defineConfig({
  // The phase 1-3 H5 is served at /h5/ until the new sites replace it
  // (ADR-0012); H5_BASE=/h5/ in the deploy build.
  base: process.env.H5_BASE ?? "/",
  plugins: [react(), tailwindcss()],
  // The Turnstile site key is public; it comes from the repository .env in
  // development and from the build environment on the server.
  envDir: "../..",
  envPrefix: ["VITE_", "TURNSTILE_SITE_KEY"],
  server: {
    // 5173 is the new PC site's; the legacy H5 runs next to it.
    port: 5175,
    proxy: {
      "/v1/ws": { target: api.replace(/^http/, "ws"), ws: true, changeOrigin: true },
      "/v1": { target: api, changeOrigin: true },
    },
  },
  build: { sourcemap: false, chunkSizeWarningLimit: 800 },
});
