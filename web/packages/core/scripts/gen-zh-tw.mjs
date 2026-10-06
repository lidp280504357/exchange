// Generates the Traditional Chinese (zh-TW) resources from the Simplified
// ones (design 2026-10-06 繁体中文 §2.2): OpenCC's Simplified → Taiwan
// conversion with Taiwan's phrases (s2twp), the term table
// src/i18n/zh-TW.overrides.ts laid over it. Each source keeps its
// Traditional twin beside it:
//   packages/core/src/i18n/zh-CN.ts            → zh-TW.ts (zhTW)
//   packages/ui/src/i18n.ts, apps/{pc,m}/src/i18n.ts and the areas'
//   apps/{pc,m}/src/i18n/<area>.ts (their "zh-CN" object) → <name>.zh-TW.ts
//   packages/core/content/<section>/<slug>.zh-CN.md → <slug>.zh-TW.md
//   packages/core/assets/coins/*.json (names, introductions) → src/coins.zh-TW.ts
// Run: pnpm i18n (task web:i18n) writes them; --check compares instead
// (task web:check, like the API types); --review prints what the Taiwan
// wording and the term table changed, for a person to go through;
// --convert <text>... prints the texts converted (to try a term).
import { existsSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { Locale, Trie } from "opencc-js";
import ts from "typescript";
import { zhTWOverrides } from "../src/i18n/zh-TW.overrides.ts";

const here = dirname(fileURLToPath(import.meta.url));
const web = resolve(here, "../../..");
const args = process.argv.slice(2);
const mode = ["--check", "--review", "--convert"].find((m) => args.includes(m))?.slice(2) ?? "write";

// OpenCC's stages: Simplified → Traditional (phrases, characters), Taiwan's
// phrases, Taiwan's variants. The term table's phrases go in front of the
// first (a group's first dictionary wins a tie) and stay as they are in
// the other two.
const keep = zhTWOverrides.phrases.map(([, tw]) => [tw, tw]);
const stages = [
  [zhTWOverrides.phrases, ...Locale.from.cn[0]],
  [keep, ...Locale.to.twp[0]],
  [keep, ...Locale.to.twp[1]],
].map((group) => {
  const t = new Trie();
  t.loadDictGroup(group);
  return t;
});

/** toTraditional converts Simplified text to the sites' Traditional wording. */
function toTraditional(text) {
  let out = stages.reduce((s, t) => t.convert(s), text);
  for (const [from, to] of zhTWOverrides.characters) out = out.replaceAll(from, to);
  return out;
}

// Simplified characters with more than one Traditional form: how each was
// converted in place goes into the review.
const AMBIGUOUS = "后发干面只里系准台周范冲签汇历复制征才云折志钟注表别布采几借据克了累么千确舍胜术团向叶佣涌游余与愿赞占症斗托价杆回并当尽板杠挂筑凶秘松卷划获丰党恶困宁苹纤咸旋药欲脏扎致谷适须郁御朴仆曲胡庄蒙沈";

const HEADER = (src) =>
  `// Generated from ${src} by\n` +
  "// packages/core/scripts/gen-zh-tw.mjs (OpenCC s2twp and the term table\n" +
  "// packages/core/src/i18n/zh-TW.overrides.ts); do not edit: change the\n" +
  "// source or the term table and run pnpm i18n (task web:i18n).\n";

/** The "zh-CN" object of a message file (or the zhCN constant), its strings converted. */
function messages(file) {
  const text = readFileSync(file, "utf8");
  const sf = ts.createSourceFile(file, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
  let found;
  const find = (node) => {
    if (found) return;
    if (ts.isPropertyAssignment(node) && nameOf(node.name) === "zh-CN" && ts.isObjectLiteralExpression(node.initializer)) found = node.initializer;
    else if (ts.isVariableDeclaration(node) && node.name.getText(sf) === "zhCN" && node.initializer && ts.isObjectLiteralExpression(node.initializer)) found = node.initializer;
    else ts.forEachChild(node, find);
  };
  find(sf);
  if (!found) throw new Error(`${rel(file)}: no "zh-CN" object`);
  const start = found.getStart(sf);
  const edits = [];
  const strings = [];
  const walk = (node) => {
    if (ts.isTemplateExpression(node)) throw new Error(`${rel(file)}: a template with substitutions in the messages`);
    if ((ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) && !(ts.isPropertyAssignment(node.parent) && node.parent.name === node)) {
      // The text between the quotes as written: escapes are ASCII, which
      // OpenCC leaves alone.
      const from = node.getStart(sf) + 1;
      const to = node.getEnd() - 1;
      const raw = text.slice(from, to);
      strings.push(raw);
      const tw = toTraditional(raw);
      if (tw !== raw) edits.push([from - start, to - start, tw]);
    }
    ts.forEachChild(node, walk);
  };
  walk(found);
  let body = text.slice(start, found.getEnd());
  for (const [from, to, tw] of edits.reverse()) body = body.slice(0, from) + tw + body.slice(to);
  // Out of its file the object starts at the left margin.
  const lineStart = text.lastIndexOf("\n", start) + 1;
  const indent = /^ */.exec(text.slice(lineStart, start))[0].length;
  const margin = new RegExp(`^ {0,${indent}}`);
  body = body
    .split("\n")
    .map((line, i) => (i === 0 ? line : line.replace(margin, "")))
    .join("\n");
  return { body, strings };
}

function nameOf(name) {
  return ts.isIdentifier(name) || ts.isStringLiteral(name) ? name.text : undefined;
}

function rel(file) {
  return relative(web, file);
}

function list(dir, test) {
  return existsSync(dir)
    ? readdirSync(dir)
        .filter(test)
        .sort()
        .map((f) => join(dir, f))
    : [];
}

if (mode === "convert") {
  for (const text of args.filter((a) => a !== "--convert")) console.log(toTraditional(text));
  process.exit(0);
}

// What to write: [file, content] and the source strings (for --review).
const outputs = [];
const sources = [];

function messageFile(src, out, exportAs) {
  const { body, strings } = messages(src);
  outputs.push([out, `${HEADER(rel(src))}\n${exportAs} ${body};\n`]);
  sources.push(...strings);
}

messageFile(join(web, "packages/core/src/i18n/zh-CN.ts"), join(web, "packages/core/src/i18n/zh-TW.ts"), "export const zhTW =");
messageFile(join(web, "packages/ui/src/i18n.ts"), join(web, "packages/ui/src/i18n.zh-TW.ts"), "export default");
for (const app of ["pc", "m"]) {
  const src = join(web, "apps", app, "src");
  messageFile(join(src, "i18n.ts"), join(src, "i18n.zh-TW.ts"), "export default");
  for (const area of list(join(src, "i18n"), (f) => /^[a-z-]+\.ts$/.test(f))) {
    messageFile(area, area.replace(/\.ts$/, ".zh-TW.ts"), "export default");
  }
}

const content = join(web, "packages/core/content");
for (const section of list(content, (f) => !f.includes("."))) {
  for (const src of list(section, (f) => f.endsWith(".zh-CN.md"))) {
    const text = readFileSync(src, "utf8");
    outputs.push([src.replace(/\.zh-CN\.md$/, ".zh-TW.md"), toTraditional(text)]);
    sources.push(...text.split("\n").filter((l) => /\p{Script=Han}/u.test(l)));
  }
}

const coins = {};
for (const file of list(join(web, "packages/core/assets/coins"), (f) => f.endsWith(".json"))) {
  const p = JSON.parse(readFileSync(file, "utf8"));
  coins[p.symbol] = { name: toTraditional(p.name["zh-CN"]), intro: toTraditional(p.intro["zh-CN"]) };
  sources.push(p.name["zh-CN"], p.intro["zh-CN"]);
}
outputs.push([
  join(web, "packages/core/src/coins.zh-TW.ts"),
  `${HEADER("packages/core/assets/coins/*.json")}\nexport const coinsZhTW: Record<string, { name: string; intro: string }> = ${JSON.stringify(coins, null, 2)};\n`,
]);

if (mode === "review") review(sources);
else {
  const stale = [];
  for (const [file, text] of outputs) {
    const now = existsSync(file) ? readFileSync(file, "utf8") : null;
    if (now === text) continue;
    if (mode === "check") stale.push(rel(file));
    else {
      writeFileSync(file, text);
      console.log(rel(file));
    }
  }
  // A Traditional file whose source is gone.
  const made = new Set(outputs.map(([f]) => f));
  const twins = [
    ...list(join(web, "packages/core/src/i18n"), (f) => f.endsWith(".zh-TW.ts")),
    ...list(join(web, "packages/ui/src"), (f) => f.endsWith(".zh-TW.ts")),
    ...["pc", "m"].flatMap((app) => [...list(join(web, "apps", app, "src"), (f) => f.endsWith(".zh-TW.ts")), ...list(join(web, "apps", app, "src/i18n"), (f) => f.endsWith(".zh-TW.ts"))]),
    ...list(content, (f) => !f.includes(".")).flatMap((s) => list(s, (f) => f.endsWith(".zh-TW.md"))),
  ];
  const orphans = twins.filter((f) => !made.has(f)).map(rel);
  if (stale.length || orphans.length) {
    for (const f of stale) console.error(`stale: ${f}`);
    for (const f of orphans) console.error(`no source: ${f}`);
    console.error(mode === "check" ? "The Traditional Chinese files differ from their sources: run pnpm i18n (task web:i18n) and commit them." : "Remove the files without a source.");
    process.exit(1);
  }
}

/**
 * review prints, with counts and an example each, the words Taiwan's
 * phrases changed (登錄 → 登入), the term table's (數據), and the
 * characters with more than one Traditional form as converted in place,
 * so that a person can go through them (design §2.2: 执行会话把转换后的
 * 全部文案过一遍).
 */
function review(texts) {
  const [cn, twPhrases] = stages;
  const terms = new Map(zhTWOverrides.phrases.map(([s, t]) => [s, t]));
  const changed = new Map();
  const add = (kind, from, to, example) => {
    const key = `${kind}\t${from} → ${to}`;
    const hit = changed.get(key) ?? { n: 0, example };
    hit.n++;
    changed.set(key, hit);
  };
  for (const text of new Set(texts)) {
    for (const [from, to] of segments(cn, text)) {
      if (terms.has(from)) add("term", from, terms.get(from), text);
      else if ([...from].some((c) => AMBIGUOUS.includes(c))) add("char", from, to, text);
    }
    const traditional = cn.convert(text);
    for (const [from, to] of segments(twPhrases, traditional)) if (from !== to) add("taiwan", from, to, text);
  }
  const rows = [...changed].sort((a, b) => a[0].localeCompare(b[0]) || b[1].n - a[1].n);
  for (const [key, { n, example }] of rows) console.log(`${key}\t${n}\t${example.slice(0, 80)}`);
}

/** segments lists what a stage matched in text: [source, replacement]. */
function segments(trie, text) {
  const out = [];
  for (let i = 0; i < text.length; ) {
    const m = trie.matchPrefix(text, i);
    if (m) {
      out.push([text.slice(i, m.end), m.value]);
      i = m.end;
    } else i += text.codePointAt(i) > 0xffff ? 2 : 1;
  }
  return out;
}
