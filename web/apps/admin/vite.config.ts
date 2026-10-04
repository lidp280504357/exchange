import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// The admin console (admin.astras.vip). In development /admin/v1 goes to the
// test server, so the session cookie (Path=/admin/) behaves as in production.
// Its pages' chunks are preloaded by the entry (src/preload.ts), not by an
// inline script in index.html as on the user sites: the console's CSP
// forbids inline scripts.
const api = process.env.API_ORIGIN ?? "https://admin.astras.vip";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 5180,
    strictPort: true,
    proxy: { "/admin/v1": { target: api, changeOrigin: true } },
  },
  build: {
    sourcemap: false,
    rolldownOptions: {
      output: {
        // A40: the bundler's own split made a page's first visit fetch 8-31
        // small files (each shared component and icon in a chunk of its own,
        // per set of pages using it). Now what the entry imports (the
        // sign-in page included) is one chunk, what two pages or more share
        // is one more ("kit", fetched with the signed-in shell), and a page
        // is one file of its own.
        codeSplitting: {
          groups: [
            { name: "index", tags: ["$initial"], priority: 2 },
            { name: "kit", minShareCount: 2, priority: 1 },
          ],
        },
      },
    },
  },
});
