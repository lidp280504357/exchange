// Front matter of the announcement and help Markdown (design §6.2): a
// block of "key: value" lines between two "---" lines at the top. Only
// the simple YAML the content needs: strings (bare or quoted), numbers,
// true/false and dates, which stay strings ("2026-09-30").

export type FrontValue = string | number | boolean;
export type FrontMatter = Record<string, FrontValue>;

const KEY = /^([A-Za-z_][\w-]*)\s*:\s*(.*)$/;

function unquote(v: string): string {
  if (v.length >= 2 && v.startsWith('"') && v.endsWith('"')) {
    return v.slice(1, -1).replace(/\\(["\\nt])/g, (_, c: string) => (c === "n" ? "\n" : c === "t" ? "\t" : c));
  }
  if (v.length >= 2 && v.startsWith("'") && v.endsWith("'")) return v.slice(1, -1).replace(/''/g, "'");
  return v;
}

/** frontValue turns one YAML scalar into a value. */
export function frontValue(raw: string): FrontValue {
  const v = raw.trim();
  if (v.startsWith('"') || v.startsWith("'")) return unquote(v);
  // A comment after a bare value.
  const bare = v.replace(/\s+#.*$/, "");
  if (bare === "true") return true;
  if (bare === "false") return false;
  if (/^-?\d+(\.\d+)?$/.test(bare) && !/^-?0\d/.test(bare)) return Number(bare);
  return bare;
}

/**
 * parseFrontMatter splits a Markdown file into its front matter and body.
 * A file without the opening "---" line has empty front matter; an
 * unclosed block is treated as body.
 */
export function parseFrontMatter(src: string): { data: FrontMatter; body: string } {
  const text = src.replace(/^\uFEFF/, "").replace(/\r\n?/g, "\n");
  if (!text.startsWith("---\n")) return { data: {}, body: text };
  const end = text.indexOf("\n---", 3);
  if (end === -1) return { data: {}, body: text };
  const after = text.slice(end + 4);
  if (after !== "" && !after.startsWith("\n")) return { data: {}, body: text };
  const data: FrontMatter = {};
  for (const line of text.slice(4, end).split("\n")) {
    if (line.trim() === "" || line.trimStart().startsWith("#")) continue;
    const m = KEY.exec(line);
    if (m) data[m[1]!] = frontValue(m[2]!);
  }
  return { data, body: after.replace(/^\n/, "") };
}

/** frontString reads a string field (numbers become text); fallback when missing. */
export function frontString(data: FrontMatter, key: string, fallback = ""): string {
  const v = data[key];
  return typeof v === "string" ? v : typeof v === "number" ? String(v) : fallback;
}

/** frontNumber reads a number field; fallback when missing or not a number. */
export function frontNumber(data: FrontMatter, key: string, fallback = 0): number {
  const v = data[key];
  return typeof v === "number" && Number.isFinite(v) ? v : fallback;
}

/** frontBool reads a true/false field. */
export function frontBool(data: FrontMatter, key: string, fallback = false): boolean {
  const v = data[key];
  return typeof v === "boolean" ? v : fallback;
}
