// routePreload is a Vite build plugin (design §4.4): index.html starts
// downloading the page chunk of the address it was opened at, with the
// chunks that page imports, while the entry is still loading, instead of
// waiting for the entry to run and ask for it. That is one round trip
// less before the first real content on a slow network.
//
// routes are [pattern, modules]: a RegExp source matched against
// location.pathname, and the page's modules (paths relative to the app,
// such as "src/pages/home/Home.tsx" and its area's strings). The first
// matching pattern wins.
export function routePreload(routes) {
  return {
    name: "route-preload",
    apply: "build",
    transformIndexHtml: {
      order: "post",
      handler(html, ctx) {
        const bundle = ctx.bundle;
        if (!bundle) return html;
        const chunks = Object.values(bundle).filter((c) => c.type === "chunk");
        const byFile = new Map(chunks.map((c) => [c.fileName, c]));
        // A chunk's file and, recursively, the chunks it imports statically.
        const files = (chunk, out) => {
          if (out.has(chunk.fileName)) return;
          out.add(chunk.fileName);
          for (const f of chunk.imports) {
            const dep = byFile.get(f);
            if (dep) files(dep, out);
          }
        };
        const entry = new Set();
        for (const c of chunks) if (c.isEntry) files(c, entry);
        const map = [];
        for (const [pattern, modules] of routes) {
          const out = new Set();
          for (const m of modules) {
            const chunk = chunks.find((c) => c.facadeModuleId?.endsWith("/" + m));
            if (!chunk) throw new Error(`route-preload: no chunk for ${m}`);
            files(chunk, out);
          }
          // The entry's own chunks are preloaded by index.html already.
          map.push([pattern, [...out].filter((f) => !entry.has(f)).map((f) => "/" + f)]);
        }
        const script =
          `<script>(function(){var m=${JSON.stringify(map)},p=location.pathname;` +
          `for(var i=0;i<m.length;i++){if(new RegExp(m[i][0]).test(p)){m[i][1].forEach(function(h){` +
          `var l=document.createElement("link");l.rel="modulepreload";l.href=h;document.head.appendChild(l)});return}}})()</script>`;
        return html.replace("</head>", `  ${script}\n  </head>`);
      },
    },
  };
}
