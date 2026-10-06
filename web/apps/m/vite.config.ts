import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";
import { routePreload } from "../../scripts/route-preload.mjs";

// The mobile site (m.astras.vip). In development /v1 and the WebSocket go to
// the test server (the gateway's origin lists include localhost:5174).
const api = process.env.API_ORIGIN ?? "https://astras.vip";

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    // The landing pages' chunks start with index.html (design §4.4).
    routePreload([
      ["^/$", ["src/pages/home/Home.tsx", "src/i18n/markets.ts", "src/i18n/content.ts"]],
      ["^/markets/?$", ["src/pages/home/Markets.tsx", "src/i18n/markets.ts", "src/i18n/futures.ts"]],
      ["^/coin/", ["src/pages/home/Coin.tsx", "src/i18n/markets.ts"]],
      ["^/trade/", ["src/pages/trade/SpotTerminal.tsx", "src/i18n/trade.ts"]],
      // Before the terminal's: /futures/data is the overview page (design 2026-10-06 §3.3).
      ["^/futures/data/?$", ["src/pages/futures/FuturesData.tsx", "src/i18n/futures.ts"]],
      ["^/futures/", ["src/pages/trade/FuturesTerminal.tsx", "src/i18n/trade.ts", "src/i18n/futures.ts"]],
      ["^/assets/?$", ["src/pages/assets/Overview.tsx", "src/i18n/assets.ts"]],
    ]),
  ],
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
