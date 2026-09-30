// Lint of the new apps and shared packages (design §12.2): no raw colours
// outside the design tokens (packages/ui/src/styles/tokens.css) and no
// direct toLocale* calls (times and numbers go through @exchange/core's
// formatters, in the user's language and time zone).
import { readdirSync, readFileSync, statSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath } from "node:url";

const web = join(dirname(fileURLToPath(import.meta.url)), "..");
const roots = ["packages", "apps"].map((d) => join(web, d));
const skipDirs = new Set(["node_modules", "dist", "storybook-static", "gen"]);
const allowColours = new Set(["packages/ui/src/styles/tokens.css"]);

const colour = /(?<![\w&])#(?:[0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{4}|[0-9a-fA-F]{3})\b|\brgba?\(|\bhsla?\(/;
const toLocale = /\.toLocale(?:String|DateString|TimeString)\(/;

function* files(dir) {
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) {
      if (!skipDirs.has(name) && !name.startsWith(".")) yield* files(path);
    } else if (/\.(tsx?|css)$/.test(name)) {
      yield path;
    }
  }
}

const problems = [];
for (const root of roots) {
  for (const path of files(root)) {
    const rel = relative(web, path);
    const lines = readFileSync(path, "utf8").split("\n");
    lines.forEach((line, i) => {
      const code = line.replace(/\/\/.*$/, "");
      if (!allowColours.has(rel) && colour.test(code)) problems.push(`${rel}:${i + 1}: a raw colour; use a token (bg-bg-1, text-up ...)`);
      if (toLocale.test(code)) problems.push(`${rel}:${i + 1}: toLocale*; use formatTime/formatDecimal from @exchange/core`);
    });
  }
}

if (problems.length > 0) {
  console.error(problems.join("\n"));
  process.exit(1);
}
console.log("tokens: no raw colours or toLocale* calls in apps and packages");
