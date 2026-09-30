import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// The console is served at /admin/ next to the H5 (nginx); in development
// /admin/v1 is proxied to the test server, so the session cookie (Path
// /admin/) behaves as in production.
const api = process.env.API_ORIGIN ?? "https://astras.vip";

export default defineConfig({
  base: "/admin/",
  plugins: [react(), tailwindcss()],
  server: {
    // 5180 is the new admin console's; the legacy one runs next to it.
    port: 5181,
    proxy: { "/admin/v1": { target: api, changeOrigin: true } },
  },
  build: { sourcemap: false },
});
