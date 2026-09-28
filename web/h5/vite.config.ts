import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// In development the API runs on the test server (CLAUDE.md): /v1 and the
// WebSocket are proxied there, so cookies and origins look like production.
const api = process.env.API_ORIGIN ?? "https://astras.vip";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  // The Turnstile site key is public; it comes from the repository .env in
  // development and from the build environment on the server.
  envDir: "../..",
  envPrefix: ["VITE_", "TURNSTILE_SITE_KEY"],
  server: {
    port: 5173,
    proxy: {
      "/v1/ws": { target: api.replace(/^http/, "ws"), ws: true, changeOrigin: true },
      "/v1": { target: api, changeOrigin: true },
    },
  },
  build: { sourcemap: true, chunkSizeWarningLimit: 800 },
});
