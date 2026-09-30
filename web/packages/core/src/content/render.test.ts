import { createElement, isValidElement, type ReactElement, type ReactNode } from "react";
import { describe, expect, it } from "vitest";
import { parseMarkdown } from "./markdown";
import { Markdown, renderMarkdown, type LinkProps } from "./render";

type El = ReactElement<Record<string, unknown> & { children?: ReactNode }>;

/** flat walks a React tree: element types and text, in document order. */
function flat(node: ReactNode, out: string[] = []): string[] {
  if (node === null || node === undefined || typeof node === "boolean") return out;
  if (typeof node === "string" || typeof node === "number") {
    out.push(`"${node}"`);
    return out;
  }
  if (Array.isArray(node)) {
    for (const n of node) flat(n, out);
    return out;
  }
  if (isValidElement(node)) {
    const el = node as El;
    if (typeof el.type === "function") return flat((el.type as (p: unknown) => ReactNode)(el.props), out);
    const name = typeof el.type === "string" ? el.type : "#frag";
    if (name !== "#frag") out.push(`<${name}>`);
    flat(el.props.children, out);
    if (name !== "#frag") out.push(`</${name}>`);
  }
  return out;
}

/** find returns the first element of a type in a tree. */
function find(node: ReactNode, type: string): El | undefined {
  if (Array.isArray(node)) {
    for (const n of node) {
      const f = find(n, type);
      if (f) return f;
    }
    return undefined;
  }
  if (!isValidElement(node)) return undefined;
  const el = node as El;
  if (el.type === type) return el;
  return find(el.props.children, type);
}

const render = (src: string, link?: (p: LinkProps) => ReactNode) => renderMarkdown(parseMarkdown(src).blocks, { link });

describe("renderMarkdown", () => {
  it("renders headings with ids, paragraphs and inline marks", () => {
    const out = render("## 手续费\n\n**买方**以 *base* 付 `fee`，~~旧规则~~。");
    expect(flat(out).join("")).toBe(
      '<h2>"手续费"</h2><p><strong>"买方"</strong>"以 "<em>"base"</em>" 付 "<code>"fee"</code>"，"<del>"旧规则"</del>"。"</p>',
    );
    expect(find(out, "h2")?.props.id).toBe("手续费");
  });

  it("keeps raw HTML as text", () => {
    const out = render('<script>alert(1)</script>\n\n<img src=x onerror="alert(1)">');
    const texts = flat(out);
    expect(texts).toContain('"<script>alert(1)</script>"');
    expect(texts).not.toContain("<script>");
    expect(texts).not.toContain("<img>");
  });

  it("renders safe links and drops unsafe ones", () => {
    const out = render("[站内](/help/fees) [外部](https://ethereum.org) [坏](javascript:alert(1)) [邮件](mailto:a@b.c)");
    const links = (out[0] as El).props.children as ReactNode[];
    const anchors = links.map((n) => find(n, "a")).filter((a): a is El => Boolean(a));
    expect(anchors.map((a) => [a.props.href, a.props.target, a.props.rel])).toEqual([
      ["/help/fees", undefined, undefined],
      ["https://ethereum.org", "_blank", "noopener noreferrer"],
      ["mailto:a@b.c", undefined, "noopener noreferrer"],
    ]);
    // The refused link keeps its text, merged with the spaces around it.
    expect(flat(out).join("")).toContain('" 坏 "');
  });

  it("lets the page render links (router links for site paths)", () => {
    const seen: LinkProps[] = [];
    render("[a](/help/deposit) [b](https://x.y)", (p) => {
      seen.push(p);
      return createElement("span", null, p.children);
    });
    expect(seen.map((p) => [p.href, p.external])).toEqual([
      ["/help/deposit", false],
      ["https://x.y", true],
    ]);
  });

  it("renders tight lists without paragraphs, loose ones with", () => {
    expect(flat(render("- a\n- b")).join("")).toBe('<ul><li>"a"</li><li>"b"</li></ul>');
    expect(flat(render("1. a\n\n2. b")).join("")).toBe('<ol><li><p>"a"</p></li><li><p>"b"</p></li></ol>');
    const ol = find(render("3. c\n4. d"), "ol");
    expect(ol?.props.start).toBe(3);
  });

  it("renders tables, code, quotes and rules", () => {
    const out = render("| a | b |\n|:-|-:|\n| 1 | 2 |\n\n```ts\nconst x = 1 < 2;\n```\n\n> quote\n\n---");
    expect(flat(out).join("")).toBe(
      '<div><table><thead><tr><th>"a"</th><th>"b"</th></tr></thead><tbody><tr><td>"1"</td><td>"2"</td></tr></tbody></table></div>' +
        '<pre><code>"const x = 1 < 2;"</code></pre><blockquote><p>"quote"</p></blockquote><hr></hr>',
    );
    expect(find(out, "th")?.props.style).toEqual({ textAlign: "left" });
    expect(find(out, "code")?.props.className).toBe("language-ts");
  });

  it("refuses odd code languages as class names", () => {
    const out = render('```x" onclick="y\ncode\n```');
    expect(find(out, "code")?.props.className).toBeUndefined();
  });

  it("works as a component", () => {
    expect(flat(createElement(Markdown, { source: "hi" })).join("")).toBe('<p>"hi"</p>');
  });
});
