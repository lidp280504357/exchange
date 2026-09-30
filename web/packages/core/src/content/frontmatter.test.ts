import { describe, expect, it } from "vitest";
import { frontBool, frontNumber, frontString, frontValue, parseFrontMatter } from "./frontmatter";

describe("parseFrontMatter", () => {
  it("reads strings, numbers, booleans and dates, and returns the body", () => {
    const src = [
      "---",
      "title: USDT 永续合约上线",
      'summary: "BTC、ETH 永续：最高 50 倍"',
      "date: 2026-09-29",
      "pinned: true",
      "order: 3",
      "category: trading # a comment",
      "# a comment line",
      "",
      "---",
      "",
      "## 正文",
    ].join("\n");
    const { data, body } = parseFrontMatter(src);
    expect(data).toEqual({
      title: "USDT 永续合约上线",
      summary: "BTC、ETH 永续：最高 50 倍",
      date: "2026-09-29",
      pinned: true,
      order: 3,
      category: "trading",
    });
    expect(body).toBe("\n## 正文");
  });

  it("handles CRLF, a BOM, colons in values and quotes", () => {
    const { data, body } = parseFrontMatter("\uFEFF---\r\ntitle: 公告：新版网站\r\nq: 'it''s'\r\nesc: \"a \\\"b\\\"\"\r\n---\r\nbody");
    expect(data).toEqual({ title: "公告：新版网站", q: "it's", esc: 'a "b"' });
    expect(body).toBe("body");
  });

  it("leaves files without front matter alone", () => {
    expect(parseFrontMatter("# Title\n\ntext")).toEqual({ data: {}, body: "# Title\n\ntext" });
    expect(parseFrontMatter("---\ntitle: x\nno closing line")).toEqual({ data: {}, body: "---\ntitle: x\nno closing line" });
    expect(parseFrontMatter("---\n---\nbody")).toEqual({ data: {}, body: "body" });
    // A thematic break later in the body is not front matter.
    expect(parseFrontMatter("text\n---\nmore").data).toEqual({});
  });

  it("types scalars", () => {
    expect(frontValue("42")).toBe(42);
    expect(frontValue("-1.5")).toBe(-1.5);
    expect(frontValue("007")).toBe("007");
    expect(frontValue("false")).toBe(false);
    expect(frontValue("'true'")).toBe("true");
    expect(frontValue("2026-09-30")).toBe("2026-09-30");
  });

  it("reads fields with fallbacks", () => {
    const data = { title: "t", order: 2, pinned: true, n: 7 };
    expect(frontString(data, "title")).toBe("t");
    expect(frontString(data, "n")).toBe("7");
    expect(frontString(data, "missing", "x")).toBe("x");
    expect(frontNumber(data, "order")).toBe(2);
    expect(frontNumber(data, "title", 9)).toBe(9);
    expect(frontBool(data, "pinned")).toBe(true);
    expect(frontBool(data, "order")).toBe(false);
  });
});
