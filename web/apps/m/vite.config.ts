import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// The mobile site (m.astras.vip). In development /v1 and the WebSocket go to
// the test server (the gateway's origin lists include localhost:5174).
const api = process.env.API_ORIGIN ?? "https://astras.vip";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  envDir: "../../..",
  envPrefix: ["VITE_", "TURNSTILE_SITE_KEY"],
  server: {
    port: 5174,
    strictPort: true,
    proxy: {
      "/v1/ws": { target: api.replace(/^http/, "ws"), ws: true, changeOrigin: true },
      "/v1": { target: api, changeOrigin: true },
    },
  },
  // Built files go to /static/: /assets/* are the assets pages' routes (/assets/deposit, …),
  // which nginx must answer with index.html.
  build: { sourcemap: false, assetsDir: "static" },
});
