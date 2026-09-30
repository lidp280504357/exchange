// Generates TypeScript types from the contracts: every public service in
// api/openapi (one file each) and the admin console's api/admin/admin.yaml,
// into src/api/gen. Run: pnpm api:types (task web:types).
import { mkdirSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import openapiTS, { astToString } from "openapi-typescript";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "../../../..");
const out = resolve(here, "../src/api/gen");
mkdirSync(out, { recursive: true });

const specs = readdirSync(join(root, "api/openapi"))
  .filter((f) => f.endsWith(".yaml") && f !== "common.yaml")
  .map((f) => ["api/openapi/" + f, f.replace(/\.yaml$/, "")]);
specs.push(["api/admin/admin.yaml", "admin"]);

for (const [path, name] of specs) {
  const ast = await openapiTS(pathToFileURL(join(root, path)));
  writeFileSync(join(out, `${name}.ts`), `// Generated from ${path} by scripts/gen-api.mjs; do not edit.\n\n${astToString(ast)}`);
  console.log(`src/api/gen/${name}.ts`);
}
