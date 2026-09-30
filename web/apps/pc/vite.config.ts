import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";
import { routePreload } from "../../scripts/route-preload.mjs";

// The PC site (astras.vip). In development /v1 and the WebSocket go to the
// test server, so cookies and origins look like production (the gateway's
// origin lists include localhost:5173).
const api = process.env.API_ORIGIN ?? "https://astras.vip";

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    // The landing pages' chunks start with index.html (design §4.4).
    routePreload([
      ["^/$", ["src/pages/markets/Home.tsx", "src/i18n/markets.ts", "src/i18n/content.ts"]],
      ["^/markets/?$", ["src/pages/markets/Markets.tsx", "src/i18n/markets.ts", "src/i18n/content.ts"]],
      ["^/coin/", ["src/pages/markets/Coin.tsx", "src/i18n/markets.ts", "src/i18n/content.ts"]],
      ["^/trade/", ["src/pages/trade/SpotTerminal.tsx", "src/i18n/trade.ts"]],
      ["^/futures/", ["src/pages/trade/FuturesTerminal.tsx", "src/i18n/trade.ts"]],
      ["^/assets/?$", ["src/pages/assets/Overview.tsx", "src/i18n/assets.ts", "src/i18n/auth.ts"]],
    ]),
  ],
  // The Turnstile site key is public; it comes from the repository .env in
  // development and from the build environment on the server.
  envDir: "../../..",
  envPrefix: ["VITE_", "TURNSTILE_SITE_KEY"],
  server: {
    port: 5173,
    strictPort: true,
    proxy: {
      "/v1/ws": { target: api.replace(/^http/, "ws"), ws: true, changeOrigin: true },
      "/v1": { target: api, changeOrigin: true },
    },
  },
  // Pages load lazily (one chunk each, charts and tables with the pages
  // that use them); sourcemaps stay out of the public build.
  // Built files go to /static/: /assets/* are the assets pages' routes (/assets/deposit, …),
  // which nginx must answer with index.html.
  build: { sourcemap: false, assetsDir: "static" },
});
