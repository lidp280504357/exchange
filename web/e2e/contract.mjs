// Contract checks (requirements §9.1): validates API responses against the
// OpenAPI documents in api/openapi. Every spec file is registered with Ajv
// (JSON Schema 2020-12, the dialect of OpenAPI 3.1) under its file name,
// so relative $refs between the files resolve.
import { readdirSync, readFileSync } from "node:fs";
import Ajv2020 from "ajv/dist/2020.js";
import addFormats from "ajv-formats";
import { parse } from "yaml";

const DIR = new URL("../../api/openapi/", import.meta.url);
// The admin console's contract lives apart (it stays out of the public reference).
const ADMIN = new URL("../../api/admin/admin.yaml", import.meta.url);
const METHODS = ["get", "post", "put", "patch", "delete"];

// pointer builds a URI-fragment JSON pointer from path segments.
const pointer = (segments) =>
  "/" + segments.map((s) => encodeURIComponent(String(s).replace(/~/g, "~0").replace(/\//g, "~1"))).join("/");

function resolve(docs, file, ref) {
  const [target, frag = ""] = ref.split("#");
  const f = target ? new URL(target, new URL(file, DIR)).pathname.split("/").pop() : file;
  let node = docs[f];
  for (const seg of frag.split("/").slice(1)) node = node?.[decodeURIComponent(seg).replace(/~1/g, "/").replace(/~0/g, "~")];
  return { file: f, node, segments: frag.split("/").slice(1).map((s) => decodeURIComponent(s).replace(/~1/g, "/").replace(/~0/g, "~")) };
}

export function loadContracts() {
  const ajv = new Ajv2020({ strict: false, allErrors: true, validateSchema: false });
  addFormats(ajv);
  ajv.addFormat("int64", true);
  const docs = {};
  const files = readdirSync(DIR)
    .filter((n) => n.endsWith(".yaml"))
    .map((f) => [f, new URL(f, DIR)]);
  for (const [f, url] of [...files, ["admin.yaml", ADMIN]]) {
    const doc = parse(readFileSync(url, "utf8"));
    doc.$id = f;
    docs[f] = doc;
    ajv.addSchema(doc, f);
  }
  // admin.yaml names some public schemas as ../openapi/<file> (the apps to
  // download's public answer, H0), which resolves to openapi/<file> from
  // its id: the public files are there too, their own refs among them.
  for (const [f] of files) {
    const alias = structuredClone(docs[f]);
    alias.$id = `openapi/${f}`;
    ajv.addSchema(alias, alias.$id);
  }
  const routes = [];
  for (const [file, doc] of Object.entries(docs)) {
    for (const [path, item] of Object.entries(doc.paths ?? {})) {
      const re = new RegExp("^" + path.replace(/\{[^}]+\}/g, "[^/]+") + "$");
      for (const m of METHODS) if (item[m]) routes.push({ file, path, method: m, re, op: item[m] });
    }
  }
  const validators = new Map();

  // check returns what is wrong with a response, or "" when it matches.
  function check(method, pathname, status, body) {
    const route = routes.find((r) => r.method === method.toLowerCase() && r.re.test(pathname));
    if (!route) return `${method} ${pathname} is not in the contracts`;
    const where = `${method} ${route.path} → ${status}`;
    let file = route.file;
    let segments = ["paths", route.path, route.method, "responses"];
    let key = String(status) in route.op.responses ? String(status) : "default";
    let response = route.op.responses[key];
    if (!response) return `${where}: status not documented`;
    segments = [...segments, key];
    if (response.$ref) {
      const r = resolve(docs, file, response.$ref);
      ({ file, segments } = r);
      response = r.node;
    }
    const schema = response?.content?.["application/json"]?.schema;
    if (!schema) return ""; // no JSON body documented (204)
    const id = `${file}#${pointer([...segments, "content", "application/json", "schema"])}`;
    let validate = validators.get(id);
    if (!validate) {
      validate = ajv.compile({ $ref: id });
      validators.set(id, validate);
    }
    return validate(body) ? "" : `${where}: ${ajv.errorsText(validate.errors, { dataVar: "body" })}`;
  }

  // checkSchema returns what is wrong with a value of a schema a spec file
  // names (a WebSocket channel's message), or "" when it matches.
  function checkSchema(file, name, value) {
    const id = `${file}#${pointer(["components", "schemas", name])}`;
    let validate = validators.get(id);
    if (!validate) {
      validate = ajv.compile({ $ref: id });
      validators.set(id, validate);
    }
    return validate(value) ? "" : `${file} ${name}: ${ajv.errorsText(validate.errors, { dataVar: "data" })}`;
  }
  return { check, checkSchema, routes: routes.length };
}
