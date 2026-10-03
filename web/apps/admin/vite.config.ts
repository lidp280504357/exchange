import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";
import { routePreload } from "../../scripts/route-preload.mjs";

// The admin console (admin.astras.vip). In development /admin/v1 goes to the
// test server, so the session cookie (Path=/admin/) behaves as in production.
const api = process.env.API_ORIGIN ?? "https://admin.astras.vip";

// A page behind the sign-in: the console's shell and the page itself.
const page = (module: string) => ["src/layout/SignedIn.tsx", `src/pages/${module}.tsx`];

export default defineConfig({
  plugins: [
    react(),
    tailwindcss(),
    // index.html starts the shell's and the page's chunks with the entry,
    // instead of three waves (entry, shell, page) one round trip apart
    // (C6: the overview scored 83 for performance on Lighthouse). A page
    // other pages import parts of (funds/Adjustments, Instruments) has no
    // chunk of its own to start.
    routePreload([
      ["^/login", ["src/pages/Login.tsx"]],
      ["^/setup", ["src/pages/Setup.tsx"]],
      ["^/$", page("Overview")],
      ["^/users/[^/]+", page("users/UserPage")],
      ["^/users", page("users/Users")],
      ["^/identity-requests", page("users/IdentityRequests")],
      ["^/deposits", page("wallet/Deposits")],
      ["^/withdrawals", page("wallet/Withdrawals")],
      ["^/custody", page("wallet/Custody")],
      ["^/approvals", page("funds/Approvals")],
      ["^/ledger", page("Ledger")],
      ["^/orders", page("orders/Orders")],
      ["^/positions", page("trading/Positions")],
      ["^/liquidations", page("trading/Liquidations")],
      ["^/derivatives", page("Derivatives")],
      ["^/house", page("House")],
      ["^/sim/control", page("sim/Control")],
      ["^/sim/events", page("sim/Events")],
      ["^/sim/bots", page("sim/Bots")],
      ["^/sim/token", page("sim/Token")],
      ["^/sim", page("sim/Overview")],
      ["^/risk", page("Flags")],
      ["^/announcements", page("content/Announcements")],
      ["^/help-articles", page("content/HelpArticles")],
      ["^/broadcasts", page("content/Broadcasts")],
      ["^/admins", page("system/Admins")],
      ["^/audit", page("Audit")],
      ["^/reports", page("Reports")],
      ["^/health", page("system/Health")],
      ["^/settings", page("system/Settings")],
      ["^/account", page("system/Account")],
    ]),
  ],
  server: {
    port: 5180,
    strictPort: true,
    proxy: { "/admin/v1": { target: api, changeOrigin: true } },
  },
  build: { sourcemap: false },
});
