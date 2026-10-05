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
        // A40 (review BJ ⑧): the bundler's own split made a page's first
        // visit fetch 8-31 small files. Now the libraries the entry imports
        // are one file ("vendor", unchanged across deploys, preloaded beside
        // the entry), the entry's own code (the sign-in page included)
        // another, what two pages or more share a third ("kit", fetched with
        // the signed-in shell), and a page is one file of its own. The two
        // entry groups do not pull their dependencies in: the shell's motion
        // and tables stay out of the sign-in page. What the margin pages
        // share is a file of their own (A55): they sit behind margin.enabled,
        // and in "kit" their sample data and strings went to every page.
        codeSplitting: {
          groups: [
            {
              name: "vendor",
              tags: ["$initial"],
              test: (id) => id.includes("node_modules") || id.startsWith("\0"),
              priority: 3,
              includeDependenciesRecursively: false,
            },
            { name: "index", tags: ["$initial"], priority: 2, includeDependenciesRecursively: false },
            { name: "margin", test: (id) => id.includes("/src/pages/margin/"), minShareCount: 2, priority: 1, includeDependenciesRecursively: false },
            { name: "kit", minShareCount: 2, priority: 1 },
          ],
        },
      },
    },
  },
});
