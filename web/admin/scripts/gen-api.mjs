// Generates TypeScript types from the admin contract api/admin/admin.yaml
// into src/api/gen. Run: pnpm api:types
import { mkdirSync, writeFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import openapiTS, { astToString } from "openapi-typescript";

const here = dirname(fileURLToPath(import.meta.url));
const spec = resolve(here, "../../../api/admin/admin.yaml");
const out = resolve(here, "../src/api/gen");
mkdirSync(out, { recursive: true });
const ast = await openapiTS(pathToFileURL(spec));
writeFileSync(resolve(out, "admin.ts"), `// Generated from api/admin/admin.yaml by scripts/gen-api.mjs; do not edit.\n\n${astToString(ast)}`);
console.log("src/api/gen/admin.ts");
