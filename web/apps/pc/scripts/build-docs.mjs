// Builds the API reference served at /docs/: merges the per-service
// contracts in api/openapi into one OpenAPI document (dist/docs/openapi.json)
// and writes a Redoc page next to it. Runs after vite build (pnpm build), so
// a contract that cannot be merged fails the build.
import { mkdirSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { parse } from "yaml";

const here = dirname(fileURLToPath(import.meta.url));
const specs = resolve(here, "../../../../api/openapi");
const out = resolve(here, "../dist/docs");

// Redoc renders the page; pinned, and checked by the browser against its hash.
const REDOC = {
  src: "https://cdn.jsdelivr.net/npm/redoc@2.5.4/bundles/redoc.standalone.js",
  integrity: "sha384-w447zOpYfw/1Tv/5AK9NfHTlQIqE3RVR6KY62jCyy9zNDgO64cMwGGP1Fj0zJVf5",
};

// Services in reading order; files not listed follow alphabetically.
const ORDER = ["gateway", "auth", "user", "account", "market", "notification"];
const METHODS = ["get", "put", "post", "delete", "options", "head", "patch", "trace"];

const INTRO = `The public REST API of the exchange, merged from the per-service contracts in
\`api/openapi\` (requirements §7). Base URL \`https://astras.vip\`; every path starts with \`/v1\`.

| Topic | Convention |
|---|---|
| Amounts | Decimal **strings** such as \`"0.00125000"\`, never JSON numbers |
| Time | RFC 3339 in UTC with milliseconds, such as \`2026-09-28T12:00:00.123Z\` |
| IDs | UUIDv7 strings |
| Sign-in | \`Authorization: Bearer <access_token>\`; web clients refresh through an HttpOnly cookie, apps send the refresh token in the body with \`X-Client-Type: APP\` |
| Sensitive actions | \`X-Step-Up-Token\` from \`POST /v1/auth/step-up\` |
| Idempotency | Writes accept an \`Idempotency-Key\` (UUID), kept 24 hours per user and path: the same key and body replays the first response, a different body is \`409 COMMON_IDEMPOTENCY_CONFLICT\` |
| Rate limits | \`X-RateLimit-Limit\`, \`X-RateLimit-Remaining\`; over the limit \`429\` with \`Retry-After\` |
| Tracing | Every response has \`X-Trace-Id\`; error bodies repeat it as \`trace_id\` |
| Errors | \`{ "code", "message", "trace_id", "details" }\`; \`code\` is stable (appendix C), the HTTP status only gives the class |

Live updates: \`GET /v1/ws\` (WebSocket, requirements §7.3).`;

const files = readdirSync(specs)
  .filter((f) => f.endsWith(".yaml"))
  .map((f) => f.replace(/\.yaml$/, ""))
  .sort((a, b) => rank(a) - rank(b) || a.localeCompare(b));

function rank(name) {
  const i = ORDER.indexOf(name);
  return i < 0 ? ORDER.length : i;
}

// localRefs rewrites references into other contract files to the merged
// document, where their components now live.
function localRefs(node, file) {
  if (Array.isArray(node)) return node.map((v) => localRefs(v, file));
  if (node === null || typeof node !== "object") return node;
  const outNode = {};
  for (const [k, v] of Object.entries(node)) {
    if (k === "$ref" && typeof v === "string") {
      const m = /^(?:\.\/)?(?:[a-z-]+\.yaml)?(#\/.+)$/.exec(v);
      if (!m) throw new Error(`${file}.yaml: unsupported $ref ${v}`);
      outNode[k] = m[1];
    } else {
      outNode[k] = localRefs(v, file);
    }
  }
  return outNode;
}

const title = (s) => s.charAt(0).toUpperCase() + s.slice(1);
const merged = {
  openapi: "3.1.0",
  info: { title: "Exchange API", version: "", description: INTRO },
  servers: [{ url: "https://astras.vip" }],
  tags: [],
  "x-tagGroups": [],
  paths: {},
  components: {},
};
const owner = {}; // "schemas/Error" -> file that defined it first

for (const file of files) {
  const doc = localRefs(parse(readFileSync(join(specs, `${file}.yaml`), "utf8")), file);
  merged.info.version ||= doc.info?.version ?? "";
  for (const [type, entries] of Object.entries(doc.components ?? {})) {
    merged.components[type] ??= {};
    for (const [name, value] of Object.entries(entries)) {
      const key = `${type}/${name}`;
      const prev = merged.components[type][name];
      if (prev !== undefined && JSON.stringify(prev) !== JSON.stringify(value)) {
        throw new Error(`components/${key} differs between ${owner[key]}.yaml and ${file}.yaml`);
      }
      merged.components[type][name] = value;
      owner[key] ??= file;
    }
  }

  // Tags are per service ("account" means different things in auth and
  // account), shown under their own name and grouped by service.
  const described = Object.fromEntries((doc.tags ?? []).map((t) => [t.name, t]));
  const used = [];
  for (const [path, item] of Object.entries(doc.paths ?? {})) {
    if (merged.paths[path]) throw new Error(`${path} is defined twice (${file}.yaml)`);
    for (const method of METHODS) {
      const op = item[method];
      if (!op) continue;
      // A file-wide security requirement applies to operations without one.
      if (op.security === undefined && doc.security !== undefined) op.security = doc.security;
      op.tags = (op.tags ?? [file]).map((t) => {
        if (!used.includes(t)) used.push(t);
        return `${file}.${t}`;
      });
    }
    merged.paths[path] = item;
  }
  for (const t of used) {
    merged.tags.push({ ...described[t], name: `${file}.${t}`, "x-displayName": title(t) });
  }
  if (used.length > 0) {
    merged["x-tagGroups"].push({ name: title(file), tags: used.map((t) => `${file}.${t}`) });
  }
}

// Every reference must now resolve inside the merged document.
(function check(node, where) {
  if (Array.isArray(node)) return node.forEach((v) => check(v, where));
  if (node === null || typeof node !== "object") return;
  for (const [k, v] of Object.entries(node)) {
    if (k === "$ref") {
      const target = v
        .slice(2)
        .split("/")
        .map((p) => p.replaceAll("~1", "/").replaceAll("~0", "~"))
        .reduce((n, p) => n?.[p], merged);
      if (target === undefined) throw new Error(`unresolved $ref ${v} in ${where}`);
    } else {
      check(v, where);
    }
  }
})(merged.paths, "paths");

const page = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="utf-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1" />
    <title>Exchange API reference</title>
    <link rel="icon" href="/icon-192.png" />
    <style>body { margin: 0; }</style>
  </head>
  <body>
    <redoc spec-url="/docs/openapi.json" required-props-first expand-responses="200,201"></redoc>
    <script src="${REDOC.src}" integrity="${REDOC.integrity}" crossorigin="anonymous"></script>
  </body>
</html>
`;

mkdirSync(out, { recursive: true });
writeFileSync(join(out, "openapi.json"), `${JSON.stringify(merged, null, 2)}\n`);
writeFileSync(join(out, "index.html"), page);
const operations = Object.values(merged.paths).reduce((n, item) => n + METHODS.filter((m) => item[m]).length, 0);
console.log(`dist/docs: ${Object.keys(merged.paths).length} paths, ${operations} operations from ${files.length} contracts`);
