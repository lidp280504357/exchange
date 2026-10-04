// A small Markdown parser for the announcements and the help centre
// (design §6.2): headings, paragraphs, bold, italic, strikethrough, inline
// code, fenced code, lists (nested), links, tables and blockquotes, into a
// tree that render.ts turns into React elements. There is no HTML in the
// output: raw HTML in the source stays text, and links keep only safe
// destinations (http, https, mailto, site paths and anchors).

export type Align = "left" | "center" | "right" | null;

export type Inline =
  | { type: "text"; value: string }
  | { type: "strong"; children: Inline[] }
  | { type: "em"; children: Inline[] }
  | { type: "del"; children: Inline[] }
  | { type: "code"; value: string }
  | { type: "link"; href: string; title?: string; children: Inline[] }
  | { type: "break" };

export type Block =
  | { type: "heading"; depth: 1 | 2 | 3 | 4 | 5 | 6; id: string; children: Inline[] }
  | { type: "paragraph"; children: Inline[] }
  | { type: "code"; lang: string; value: string }
  | { type: "blockquote"; children: Block[] }
  | { type: "list"; ordered: boolean; start: number; loose: boolean; items: Block[][] }
  | { type: "table"; align: Align[]; header: Inline[][]; rows: Inline[][][] }
  | { type: "hr" };

export type TocItem = { id: string; text: string; depth: number };

export type MarkdownDoc = { blocks: Block[]; toc: TocItem[] };

// ---------------------------------------------------------------- links

const SAFE_SCHEMES = new Set(["http", "https", "mailto"]);

/**
 * safeHref returns a link destination that is safe to render, or null:
 * http(s), mailto, site paths ("/help/deposit"), anchors ("#fees") and
 * relative paths. Other schemes (javascript:, data:, vbscript: ...),
 * anything with whitespace or control characters, and any backslash are
 * refused: browsers read a backslash as a slash, so "\\evil.com" is
 * "//evil.com", another site (C5.5 ⑫).
 */
export function safeHref(raw: string): string | null {
  const href = raw.trim();
  if (href === "" || /[\u0000- \u007f-\u009f\\]/.test(href)) return null;
  const scheme = /^([a-zA-Z][a-zA-Z0-9+.-]*):/.exec(href);
  if (scheme) return SAFE_SCHEMES.has(scheme[1]!.toLowerCase()) ? href : null;
  // A colon before any slash, "?" or "#" would be read as a scheme.
  const firstColon = href.indexOf(":");
  if (firstColon !== -1) {
    const firstSep = href.search(/[/?#]/);
    if (firstSep === -1 || firstColon < firstSep) return null;
  }
  return href;
}

/** isExternal reports whether a (safe) link leaves the site. */
export function isExternal(href: string): boolean {
  return /^(https?:)?\/\//i.test(href) || /^mailto:/i.test(href);
}

// --------------------------------------------------------------- inlines

const ASCII_PUNCT = /[!-/:-@[-`{-~]/;

function isSpace(c: string | undefined): boolean {
  return c === undefined || /\s/.test(c);
}

function isAlnum(c: string | undefined): boolean {
  return c !== undefined && /[\p{L}\p{N}]/u.test(c);
}

/** runLength counts how many times src[i] repeats from i. */
function runLength(src: string, i: number): number {
  let n = 0;
  while (src[i + n] === src[i]) n++;
  return n;
}

/** codeSpanEnd returns the index after a code span opening at i, or -1. */
function codeSpanEnd(src: string, i: number): number {
  const n = runLength(src, i);
  let j = i + n;
  while (j < src.length) {
    const k = src.indexOf("`", j);
    if (k === -1) return -1;
    const m = runLength(src, k);
    if (m === n) return k + m;
    j = k + m;
  }
  return -1;
}

/**
 * findCloser looks for the closing delimiter `delim` after `from`,
 * skipping escapes and code spans; with `skipDouble` (single * or _), a
 * doubled delimiter is jumped over with its own closer.
 */
function findCloser(src: string, from: number, delim: string, skipDouble: boolean): number {
  const c = delim[0]!;
  let i = from;
  while (i < src.length) {
    const ch = src[i];
    if (ch === "\\") {
      i += 2;
      continue;
    }
    if (ch === "`") {
      const end = codeSpanEnd(src, i);
      i = end === -1 ? i + runLength(src, i) : end;
      continue;
    }
    if (ch === c) {
      const n = runLength(src, i);
      if (skipDouble && n === 2) {
        const close = findCloser(src, i + 2, c + c, false);
        i = close === -1 ? i + 2 : close + 2;
        continue;
      }
      const before = src[i - 1];
      const after = src[i + delim.length];
      const rightFlanking = !isSpace(before);
      const wordOk = c !== "_" || !isAlnum(after);
      if (n >= delim.length && rightFlanking && wordOk) {
        // Closing a single delimiter takes the last of the run ("*a**" is em "a*").
        return n > delim.length && delim.length === 1 ? i + n - 1 : i;
      }
      i += n;
      continue;
    }
    i++;
  }
  return -1;
}

/** linkEnd parses "[text](dest "title")" at i: the parts and the index after, or null. */
function parseLinkAt(src: string, i: number): { text: string; dest: string; title?: string; end: number } | null {
  let depth = 0;
  let j = i;
  for (; j < src.length; j++) {
    const ch = src[j];
    if (ch === "\\") {
      j++;
      continue;
    }
    if (ch === "`") {
      const end = codeSpanEnd(src, j);
      if (end !== -1) j = end - 1;
      continue;
    }
    if (ch === "[") depth++;
    else if (ch === "]" && --depth === 0) break;
  }
  if (j >= src.length || src[j + 1] !== "(") return null;
  const m = /^\(\s*(<[^<>\n]*>|[^\s()<>]+(?:\([^\s()<>]*\)[^\s()<>]*)*)?(?:\s+(?:"([^"\n]*)"|'([^'\n]*)'))?\s*\)/.exec(src.slice(j + 1));
  if (!m) return null;
  let dest = m[1] ?? "";
  if (dest.startsWith("<")) dest = dest.slice(1, -1);
  const title = m[2] ?? m[3];
  return { text: src.slice(i + 1, j), dest: dest.replace(/\\([!-/:-@[-`{-~])/g, "$1"), title, end: j + 1 + m[0].length };
}

function unlink(nodes: Inline[]): Inline[] {
  return nodes.flatMap((n) => (n.type === "link" ? unlink(n.children) : [n]));
}

function mergeText(nodes: Inline[]): Inline[] {
  const out: Inline[] = [];
  for (const n of nodes) {
    const last = out[out.length - 1];
    if (n.type === "text" && last?.type === "text") last.value += n.value;
    else if (n.type !== "text" || n.value !== "") out.push(n.type === "text" ? { ...n } : n);
  }
  return out;
}

/** parseInline parses one paragraph's text; "\n" is a hard line break. */
export function parseInline(src: string): Inline[] {
  const nodes: Inline[] = [];
  let buf = "";
  const flush = () => {
    if (buf) nodes.push({ type: "text", value: buf });
    buf = "";
  };
  let i = 0;
  while (i < src.length) {
    const ch = src[i]!;
    if (ch === "\\" && i + 1 < src.length && ASCII_PUNCT.test(src[i + 1]!)) {
      buf += src[i + 1];
      i += 2;
      continue;
    }
    if (ch === "\n") {
      flush();
      nodes.push({ type: "break" });
      i++;
      continue;
    }
    if (ch === "`") {
      const n = runLength(src, i);
      const end = codeSpanEnd(src, i);
      if (end === -1) {
        buf += src.slice(i, i + n);
        i += n;
        continue;
      }
      let code = src.slice(i + n, end - n).replace(/\n/g, " ");
      if (code.length >= 2 && code.startsWith(" ") && code.endsWith(" ") && code.trim() !== "") code = code.slice(1, -1);
      flush();
      nodes.push({ type: "code", value: code });
      i = end;
      continue;
    }
    if (ch === "<") {
      const m = /^<((?:https?:\/\/|mailto:)[^\s<>]+)>/i.exec(src.slice(i));
      const href = m ? safeHref(m[1]!) : null;
      if (m && href) {
        flush();
        nodes.push({ type: "link", href, children: [{ type: "text", value: m[1]! }] });
        i += m[0].length;
        continue;
      }
    }
    if (ch === "[") {
      const link = parseLinkAt(src, i);
      if (link) {
        flush();
        const children = mergeText(unlink(parseInline(link.text)));
        const href = safeHref(link.dest);
        if (href) nodes.push({ type: "link", href, ...(link.title ? { title: link.title } : {}), children });
        else nodes.push(...children);
        i = link.end;
        continue;
      }
    }
    if (ch === "~" && runLength(src, i) === 2 && !isSpace(src[i + 2])) {
      const close = src.indexOf("~~", i + 2);
      if (close !== -1 && !isSpace(src[close - 1])) {
        flush();
        nodes.push({ type: "del", children: parseInline(src.slice(i + 2, close)) });
        i = close + 2;
        continue;
      }
    }
    if (ch === "*" || ch === "_") {
      const n = runLength(src, i);
      const leftOk = ch !== "_" || !isAlnum(src[i - 1]);
      if (leftOk && n >= 2 && !isSpace(src[i + 2])) {
        const close = findCloser(src, i + 2, ch + ch, false);
        if (close !== -1 && close > i + 2) {
          flush();
          nodes.push({ type: "strong", children: parseInline(src.slice(i + 2, close)) });
          i = close + 2;
          continue;
        }
      }
      if (leftOk && !isSpace(src[i + 1]) && (n === 1 || n >= 3)) {
        const close = findCloser(src, i + 1, ch, true);
        if (close !== -1 && close > i + 1) {
          flush();
          nodes.push({ type: "em", children: parseInline(src.slice(i + 1, close)) });
          i = close + 1;
          continue;
        }
      }
      buf += src.slice(i, i + n);
      i += n;
      continue;
    }
    buf += ch;
    i++;
  }
  flush();
  return mergeText(nodes);
}

/** plainText is the text of inline nodes (table of contents, excerpts). */
export function plainText(nodes: readonly Inline[]): string {
  return nodes
    .map((n) => {
      switch (n.type) {
        case "text":
        case "code":
          return n.value;
        case "break":
          return " ";
        default:
          return plainText(n.children);
      }
    })
    .join("");
}

// ------------------------------------------------------------------ ids

/** slugify makes an anchor id from a heading: letters and digits of any script, dashes between words. */
export function slugify(text: string): string {
  const s = text
    .toLowerCase()
    .replace(/[^\p{L}\p{N}\s-]/gu, "")
    .trim()
    .replace(/[\s-]+/g, "-")
    .replace(/^-+|-+$/g, "");
  return s || "section";
}

function slugger(): (text: string) => string {
  const seen = new Map<string, number>();
  return (text) => {
    const base = slugify(text);
    const n = seen.get(base) ?? 0;
    seen.set(base, n + 1);
    return n === 0 ? base : `${base}-${n}`;
  };
}

// ---------------------------------------------------------------- blocks

const FENCE = /^( {0,3})(`{3,}|~{3,})[ \t]*([^\s`]*)[^`]*$/;
const HEADING = /^ {0,3}(#{1,6})(?:[ \t]+(.*?))?(?:[ \t]+#+)?[ \t]*$/;
const HR = /^ {0,3}([-*_])(?:[ \t]*\1){2,}[ \t]*$/;
const QUOTE = /^ {0,3}> ?(.*)$/;
const ITEM = /^( *)([-*+]|\d{1,9}[.)])( {1,4}|[ \t]*$)(.*)$/;
const DELIMITER_CELL = /^\s*:?-+:?\s*$/;

type ItemStart = { indent: number; ordered: boolean; start: number; content: number; text: string };

function blank(line: string | undefined): boolean {
  return line === undefined || line.trim() === "";
}

function indentOf(line: string): number {
  return line.length - line.replace(/^ +/, "").length;
}

function itemStart(line: string): ItemStart | null {
  const m = ITEM.exec(line.replace(/\t/g, "    "));
  if (!m) return null;
  const marker = m[2]!;
  const ordered = /\d/.test(marker);
  const spaces = m[4] === "" ? 1 : m[3]!.length;
  return { indent: m[1]!.length, ordered, start: ordered ? Number.parseInt(marker, 10) : 1, content: m[1]!.length + marker.length + spaces, text: m[4]! };
}

/** splitRow splits a table row into trimmed cells ("\|" is a literal bar). */
export function splitRow(line: string): string[] {
  let s = line.trim();
  if (s.startsWith("|")) s = s.slice(1);
  if (s.endsWith("|") && !s.endsWith("\\|")) s = s.slice(0, -1);
  const cells: string[] = [];
  let cur = "";
  for (let i = 0; i < s.length; i++) {
    const ch = s[i];
    if (ch === "\\" && s[i + 1] === "|") {
      cur += "|";
      i++;
    } else if (ch === "|") {
      cells.push(cur.trim());
      cur = "";
    } else {
      cur += ch;
    }
  }
  cells.push(cur.trim());
  return cells;
}

function tableAt(lines: string[], i: number): { align: Align[]; header: string[] } | null {
  const head = lines[i];
  const delim = lines[i + 1];
  if (head === undefined || delim === undefined || !head.includes("|") || !delim.includes("-")) return null;
  const cells = splitRow(delim);
  if (!cells.every((c) => DELIMITER_CELL.test(c))) return null;
  const header = splitRow(head);
  if (header.length !== cells.length) return null;
  const align = cells.map((c): Align => {
    const l = c.startsWith(":");
    const r = c.endsWith(":");
    return l && r ? "center" : r ? "right" : l ? "left" : null;
  });
  return { align, header };
}

/** startsBlock reports whether a line opens a block that ends a paragraph. */
function startsBlock(lines: string[], i: number): boolean {
  const line = lines[i]!;
  return FENCE.test(line) || HEADING.test(line) || HR.test(line) || QUOTE.test(line) || itemStart(line) !== null || tableAt(lines, i) !== null;
}

// CJK ideographs, symbols and full-width forms: lines of Chinese text join without a space.
const CJK = /[\u2e80-\u9fff\uf900-\ufaff\uff00-\uffef]/;

/** joinLines joins a paragraph's lines: a hard break stays "\n", CJK text joins without a space. */
function joinLines(lines: string[]): string {
  let out = "";
  lines.forEach((raw, k) => {
    const last = k === lines.length - 1;
    const hard = !last && (/ {2,}$/.test(raw) || /\\$/.test(raw));
    const line = raw.trim().replace(/\\$/, "");
    out += line;
    if (last) return;
    if (hard) out += "\n";
    else {
      // Look past emphasis marks: "**加粗**" then "继续" still joins without a space.
      const tail = line.replace(/[*_~`]+$/, "").slice(-1);
      const head = lines[k + 1]!.trim().replace(/^[*_~`]+/, "").charAt(0);
      out += CJK.test(tail) && CJK.test(head) ? "" : " ";
    }
  });
  return out;
}

type Ctx = { slug: (text: string) => string; toc: TocItem[] };

function parseBlocks(lines: string[], ctx: Ctx): Block[] {
  const blocks: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i]!;
    if (blank(line)) {
      i++;
      continue;
    }

    const fence = FENCE.exec(line);
    if (fence) {
      const indent = fence[1]!.length;
      const mark = fence[2]!;
      const body: string[] = [];
      i++;
      const closing = new RegExp(`^ {0,3}${mark[0] === "`" ? "`" : "~"}{${mark.length},}[ \\t]*$`);
      while (i < lines.length && !closing.test(lines[i]!)) {
        body.push(lines[i]!.replace(new RegExp(`^ {0,${indent}}`), ""));
        i++;
      }
      i++; // the closing fence (or the end)
      blocks.push({ type: "code", lang: fence[3] ?? "", value: body.join("\n") });
      continue;
    }

    const heading = HEADING.exec(line);
    if (heading) {
      const children = parseInline((heading[2] ?? "").trim());
      const depth = heading[1]!.length as 1 | 2 | 3 | 4 | 5 | 6;
      const text = plainText(children);
      const id = ctx.slug(text);
      if (depth === 2 || depth === 3) ctx.toc.push({ id, text, depth });
      blocks.push({ type: "heading", depth, id, children });
      i++;
      continue;
    }

    if (HR.test(line)) {
      blocks.push({ type: "hr" });
      i++;
      continue;
    }

    if (QUOTE.test(line)) {
      const inner: string[] = [];
      while (i < lines.length && !blank(lines[i])) {
        const q = QUOTE.exec(lines[i]!);
        // A line without ">" continues the quote's paragraph (lazy continuation).
        inner.push(q ? q[1]! : lines[i]!);
        i++;
      }
      blocks.push({ type: "blockquote", children: parseBlocks(inner, ctx) });
      continue;
    }

    const table = tableAt(lines, i);
    if (table) {
      i += 2;
      const rows: Inline[][][] = [];
      while (i < lines.length && !blank(lines[i]) && lines[i]!.includes("|")) {
        const cells = splitRow(lines[i]!);
        rows.push(table.header.map((_, k) => parseInline(cells[k] ?? "")));
        i++;
      }
      blocks.push({ type: "table", align: table.align, header: table.header.map((h) => parseInline(h)), rows });
      continue;
    }

    const first = itemStart(line);
    if (first) {
      const items: Block[][] = [];
      let loose = false;
      while (i < lines.length) {
        const item = itemStart(lines[i]!);
        if (!item || item.ordered !== first.ordered || item.indent >= first.content) break;
        const content: string[] = [item.text];
        i++;
        let sawBlank = false;
        while (i < lines.length) {
          const l = lines[i]!;
          if (blank(l)) {
            let j = i + 1;
            while (j < lines.length && blank(lines[j])) j++;
            if (j < lines.length && indentOf(lines[j]!) >= item.content) {
              content.push("");
              sawBlank = true;
              i++;
              continue;
            }
            break;
          }
          if (indentOf(l) >= item.content) {
            content.push(l.slice(item.content));
            i++;
            continue;
          }
          // An unindented line continues the item's paragraph unless it opens a block.
          if (!sawBlank && !startsBlock(lines, i)) {
            content.push(l.trim());
            i++;
            continue;
          }
          break;
        }
        if (sawBlank) loose = true;
        items.push(parseBlocks(content, ctx));
        // Blank lines between items make the list loose.
        let j = i;
        while (j < lines.length && blank(lines[j])) j++;
        const nextItem = j < lines.length ? itemStart(lines[j]!) : null;
        if (j > i && nextItem && nextItem.ordered === first.ordered && nextItem.indent < first.content) {
          loose = true;
          i = j;
        }
      }
      blocks.push({ type: "list", ordered: first.ordered, start: first.start, loose, items });
      continue;
    }

    const para: string[] = [line];
    i++;
    while (i < lines.length && !blank(lines[i]) && !startsBlock(lines, i)) {
      para.push(lines[i]!);
      i++;
    }
    blocks.push({ type: "paragraph", children: parseInline(joinLines(para)) });
  }
  return blocks;
}

// ---------------------------------------------------------------- modes

/** ContentMode is the content the sites show: "test" while the exchange is in test mode, "formal" when live. */
export type ContentMode = "test" | "formal";

const MODE_OPEN = /^ {0,3}:::(test|formal)[ \t]*$/;
const MODE_CLOSE = /^ {0,3}:::[ \t]*$/;

/**
 * renderByMode keeps the Markdown of one mode (design 2026-10-04 §4.4): a
 * block from a line ":::test" or ":::formal" to a line ":::" (or the end)
 * stays in its own mode only, without its marker lines. Blocks do not
 * nest; other ":::" lines and anything inside fenced code stay as they
 * are. The bundled drafts, the console's articles and its preview all go
 * through here.
 */
export function renderByMode(src: string, mode: ContentMode): string {
  const out: string[] = [];
  let block: ContentMode | null = null;
  let fence: RegExp | null = null;
  for (const line of src.replace(/\r\n?/g, "\n").split("\n")) {
    if (fence) {
      if (fence.test(line)) fence = null;
      if (block === null || block === mode) out.push(line);
      continue;
    }
    const open = MODE_OPEN.exec(line);
    if (block === null && open) {
      block = open[1] as ContentMode;
      continue;
    }
    if (block !== null && MODE_CLOSE.test(line)) {
      block = null;
      continue;
    }
    const f = FENCE.exec(line);
    if (f) fence = new RegExp(`^ {0,3}${f[2]![0] === "`" ? "`" : "~"}{${f[2]!.length},}[ \\t]*$`);
    if (block === null || block === mode) out.push(line);
  }
  return out.join("\n");
}

/**
 * parseMarkdown parses a document (front matter already removed) into
 * blocks and the table of contents of its level 2 and 3 headings, whose
 * ids the blocks carry.
 */
export function parseMarkdown(src: string): MarkdownDoc {
  const ctx: Ctx = { slug: slugger(), toc: [] };
  const lines = src.replace(/\r\n?/g, "\n").split("\n");
  return { blocks: parseBlocks(lines, ctx), toc: ctx.toc };
}

/** excerpt is the text of the first paragraph, cut at `max` characters. */
export function excerpt(blocks: readonly Block[], max = 140): string {
  for (const b of blocks) {
    if (b.type === "paragraph") {
      const text = plainText(b.children).replace(/\s+/g, " ").trim();
      return text.length > max ? `${text.slice(0, max - 1).trimEnd()}…` : text;
    }
  }
  return "";
}
