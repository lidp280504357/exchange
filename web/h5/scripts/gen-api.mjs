// Generates TypeScript types from the OpenAPI contracts in api/openapi
// (one file per service) into src/api/gen. Run: pnpm api:types
import { mkdirSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import openapiTS, { astToString } from "openapi-typescript";

const here = dirname(fileURLToPath(import.meta.url));
const specs = resolve(here, "../../../api/openapi");
const out = resolve(here, "../src/api/gen");
mkdirSync(out, { recursive: true });

for (const file of readdirSync(specs).filter((f) => f.endsWith(".yaml") && f !== "common.yaml")) {
  const ast = await openapiTS(pathToFileURL(join(specs, file)));
  const name = file.replace(/\.yaml$/, "");
  const header = `// Generated from api/openapi/${file} by scripts/gen-api.mjs; do not edit.\n\n`;
  writeFileSync(join(out, `${name}.ts`), header + astToString(ast));
  console.log(`src/api/gen/${name}.ts`);
}
