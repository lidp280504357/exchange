import { describe, expect, it } from "vitest";
import { excerpt, parseInline, parseMarkdown, plainText, safeHref, slugify, splitRow, type Block } from "./markdown";

const text = (value: string) => ({ type: "text", value });

describe("parseInline", () => {
  it("parses bold, italic, strikethrough and code", () => {
    expect(parseInline("a **b** *c* _d_ ~~e~~ `f`")).toEqual([
      text("a "),
      { type: "strong", children: [text("b")] },
      text(" "),
      { type: "em", children: [text("c")] },
      text(" "),
      { type: "em", children: [text("d")] },
      text(" "),
      { type: "del", children: [text("e")] },
      text(" "),
      { type: "code", value: "f" },
    ]);
  });

  it("nests emphasis both ways", () => {
    expect(parseInline("**a *b* c**")).toEqual([{ type: "strong", children: [text("a "), { type: "em", children: [text("b")] }, text(" c")] }]);
    expect(parseInline("*a **b** c*")).toEqual([{ type: "em", children: [text("a "), { type: "strong", children: [text("b")] }, text(" c")] }]);
  });

  it("leaves lone and spaced delimiters and snake_case alone", () => {
    expect(parseInline("2 * 3 * 4")).toEqual([text("2 * 3 * 4")]);
    expect(parseInline("max_leverage and funding_rate")).toEqual([text("max_leverage and funding_rate")]);
    expect(parseInline("**not closed")).toEqual([text("**not closed")]);
    expect(parseInline("a ` b")).toEqual([text("a ` b")]);
  });

  it("keeps code literal, escapes and all", () => {
    expect(parseInline("`**x** <b>`")).toEqual([{ type: "code", value: "**x** <b>" }]);
    expect(parseInline("``a ` b``")).toEqual([{ type: "code", value: "a ` b" }]);
    expect(parseInline("\\*not em\\* and \\`tick")).toEqual([text("*not em* and `tick")]);
  });

  it("parses links with titles and autolinks", () => {
    expect(parseInline('see [the **fees**](/help/fees "费率") now')).toEqual([
      text("see "),
      { type: "link", href: "/help/fees", title: "费率", children: [text("the "), { type: "strong", children: [text("fees")] }] },
      text(" now"),
    ]);
    expect(parseInline("<https://astras.vip/docs/>")).toEqual([{ type: "link", href: "https://astras.vip/docs/", children: [text("https://astras.vip/docs/")] }]);
    expect(parseInline("[a](https://en.wikipedia.org/wiki/Perpetual_(disambiguation))")[0]).toMatchObject({
      href: "https://en.wikipedia.org/wiki/Perpetual_(disambiguation)",
    });
  });

  it("drops unsafe link targets but keeps their text", () => {
    expect(parseInline("[click](javascript:alert(1))")).toEqual([text("click")]);
    expect(parseInline("[x](JaVaScRiPt:alert(1))")).toEqual([text("x")]);
    expect(parseInline("[x](data:text/html;base64,AAAA)")).toEqual([text("x")]);
    expect(parseInline("[x](<java script:alert(1)>)")).toEqual([text("x")]);
    // Raw HTML is plain text.
    expect(parseInline('<img src=x onerror="alert(1)"> <script>')).toEqual([text('<img src=x onerror="alert(1)"> <script>')]);
    // No links inside links.
    expect(parseInline("[a [b](/b)](/a)")).toEqual([{ type: "link", href: "/a", children: [text("a b")] }]);
  });

  it("turns newlines into hard breaks", () => {
    expect(parseInline("a\nb")).toEqual([text("a"), { type: "break" }, text("b")]);
  });
});

describe("safeHref", () => {
  it("allows http(s), mailto, site paths, anchors and relative paths", () => {
    for (const ok of ["https://astras.vip", "http://x.y/a?b=c#d", "mailto:a@b.c", "/help/deposit", "#fees", "withdraw", "./a:b", "/a?x=1:2"]) {
      expect(safeHref(ok)).toBe(ok);
    }
  });

  it("refuses other schemes, whitespace and control characters", () => {
    for (const bad of ["javascript:alert(1)", " vbscript:x", "data:text/html,x", "java\tscript:x", "java\nscript:x", "file:///etc/passwd", "a b", "x:y", ""]) {
      expect(safeHref(bad)).toBeNull();
    }
  });
});

describe("parseMarkdown", () => {
  it("parses headings with ids and a table of contents", () => {
    const doc = parseMarkdown("# 标题\n\n## 如何充值\n\n### Step 1: pick a coin!\n\n## 如何充值\n");
    expect(doc.blocks.map((b) => (b.type === "heading" ? [b.depth, b.id] : b.type))).toEqual([
      [1, "标题"],
      [2, "如何充值"],
      [3, "step-1-pick-a-coin"],
      [2, "如何充值-1"],
    ]);
    expect(doc.toc).toEqual([
      { id: "如何充值", text: "如何充值", depth: 2 },
      { id: "step-1-pick-a-coin", text: "Step 1: pick a coin!", depth: 3 },
      { id: "如何充值-1", text: "如何充值", depth: 2 },
    ]);
  });

  it("joins paragraph lines, Chinese without a space, and keeps hard breaks", () => {
    const doc = parseMarkdown("第一行\n第二行\nand English\ntext  \nafter a break");
    expect(doc.blocks).toEqual([
      { type: "paragraph", children: [text("第一行第二行 and English text"), { type: "break" }, text("after a break")] },
    ]);
  });

  it("joins Chinese lines across emphasis marks without a space", () => {
    expect(parseMarkdown("**问题？**\n答案").blocks).toEqual([
      { type: "paragraph", children: [{ type: "strong", children: [text("问题？")] }, text("答案")] },
    ]);
  });

  it("parses fenced code verbatim", () => {
    const doc = parseMarkdown("```bash\ncurl https://astras.vip/v1/time\n# not a heading\n\n**not bold**\n```\nafter");
    expect(doc.blocks).toEqual([
      { type: "code", lang: "bash", value: "curl https://astras.vip/v1/time\n# not a heading\n\n**not bold**" },
      { type: "paragraph", children: [text("after")] },
    ]);
    expect(parseMarkdown("~~~\nunclosed").blocks).toEqual([{ type: "code", lang: "", value: "unclosed" }]);
  });

  it("parses tight and loose lists, ordered starts and nesting", () => {
    const doc = parseMarkdown("- a\n- b\n  - b1\n  - b2\n- c\ncontinued\n\n3. three\n4. four");
    const [ul, ol] = doc.blocks as Extract<Block, { type: "list" }>[];
    expect(ul).toMatchObject({ type: "list", ordered: false, loose: false });
    expect(ul!.items).toHaveLength(3);
    expect(ul!.items[1]).toEqual([
      { type: "paragraph", children: [text("b")] },
      { type: "list", ordered: false, start: 1, loose: false, items: [[{ type: "paragraph", children: [text("b1")] }], [{ type: "paragraph", children: [text("b2")] }]] },
    ]);
    expect(ul!.items[2]).toEqual([{ type: "paragraph", children: [text("c continued")] }]);
    expect(ol).toMatchObject({ type: "list", ordered: true, start: 3, loose: false });
    expect(ol!.items).toHaveLength(2);

    const loose = parseMarkdown("1. one\n\n2. two\n\n   more of two\n\nafter").blocks;
    expect(loose[0]).toMatchObject({ type: "list", ordered: true, loose: true });
    expect((loose[0] as Extract<Block, { type: "list" }>).items[1]).toEqual([
      { type: "paragraph", children: [text("two")] },
      { type: "paragraph", children: [text("more of two")] },
    ]);
    expect(loose[1]).toEqual({ type: "paragraph", children: [text("after")] });
  });

  it("parses tables with alignment, escaped bars and short rows", () => {
    const doc = parseMarkdown("| 项目 | 费率 | 说明 |\n|:---|---:|:-:|\n| 现货 | 0.10% | a \\| b |\n| 合约 |\n\nnext");
    expect(doc.blocks[0]).toEqual({
      type: "table",
      align: ["left", "right", "center"],
      header: [[text("项目")], [text("费率")], [text("说明")]],
      rows: [
        [[text("现货")], [text("0.10%")], [text("a | b")]],
        [[text("合约")], [], []],
      ],
    });
    expect(doc.blocks[1]).toEqual({ type: "paragraph", children: [text("next")] });
    expect(splitRow("| a | b |")).toEqual(["a", "b"]);
  });

  it("parses blockquotes with inner blocks, and rules", () => {
    const doc = parseMarkdown("> **注意**：只向这个地址发送 ETH。\n> - 一\n> - 二\n\n---\n\n***");
    expect(doc.blocks[0]).toEqual({
      type: "blockquote",
      children: [
        { type: "paragraph", children: [{ type: "strong", children: [text("注意")] }, text("：只向这个地址发送 ETH。")] },
        { type: "list", ordered: false, start: 1, loose: false, items: [[{ type: "paragraph", children: [text("一")] }], [{ type: "paragraph", children: [text("二")] }]] },
      ],
    });
    expect(doc.blocks.slice(1)).toEqual([{ type: "hr" }, { type: "hr" }]);
  });

  it("does not take look-alikes for blocks", () => {
    const doc = parseMarkdown("#not a heading\n\n2026 年 9 月\n\n-not a list\n\n**bold line**");
    expect(doc.blocks.map((b) => b.type)).toEqual(["paragraph", "paragraph", "paragraph", "paragraph"]);
  });

  it("gives plain text and excerpts", () => {
    const doc = parseMarkdown("## 标题\n\n本站是**学习用**的测试环境，资金均为[模拟](/help/faq)。\n\n第二段");
    expect(plainText((doc.blocks[1] as Extract<Block, { type: "paragraph" }>).children)).toBe("本站是学习用的测试环境，资金均为模拟。");
    expect(excerpt(doc.blocks)).toBe("本站是学习用的测试环境，资金均为模拟。");
    expect(excerpt(doc.blocks, 6)).toBe("本站是学习…");
    expect(excerpt([])).toBe("");
  });

  it("slugifies headings of any script", () => {
    expect(slugify("Margin & Leverage (杠杆)")).toBe("margin-leverage-杠杆");
    expect(slugify("!!!")).toBe("section");
  });
});
