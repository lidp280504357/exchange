import { createElement, Fragment, type ReactNode } from "react";
import { isExternal, parseMarkdown, type Align, type Block, type Inline, type MarkdownDoc } from "./markdown";

// React elements from the Markdown tree (./markdown). Every string goes
// in as a text child, which React escapes, so nothing in the source can
// become markup; there is no dangerouslySetInnerHTML anywhere. The
// elements carry no classes: the page styles them from a wrapper (the
// apps' Tailwind does not scan this package).

export type LinkProps = {
  href: string;
  title?: string;
  /** Leaves the site (http, https, mailto). */
  external: boolean;
  children: ReactNode;
};

export type RenderOptions = {
  /** Renders a link, e.g. a router link for site paths; defaults to <a>. */
  link?: (props: LinkProps) => ReactNode;
};

function defaultLink({ href, title, external, children }: LinkProps): ReactNode {
  const newTab = external && !/^mailto:/i.test(href);
  return createElement("a", { href, title, target: newTab ? "_blank" : undefined, rel: external ? "noopener noreferrer" : undefined }, children);
}

function inlines(nodes: readonly Inline[], o: RenderOptions, key: string): ReactNode[] {
  return nodes.map((n, i) => {
    const k = `${key}.${i}`;
    switch (n.type) {
      case "text":
        return n.value;
      case "strong":
        return createElement("strong", { key: k }, ...inlines(n.children, o, k));
      case "em":
        return createElement("em", { key: k }, ...inlines(n.children, o, k));
      case "del":
        return createElement("del", { key: k }, ...inlines(n.children, o, k));
      case "code":
        return createElement("code", { key: k }, n.value);
      case "break":
        return createElement("br", { key: k });
      case "link": {
        const props: LinkProps = { href: n.href, title: n.title, external: isExternal(n.href), children: inlines(n.children, o, k) };
        return createElement(Fragment, { key: k }, (o.link ?? defaultLink)(props));
      }
    }
  });
}

const LANG = /^[\w+#.-]{1,20}$/;

function alignStyle(a: Align | undefined) {
  return a ? { textAlign: a } : undefined;
}

function block(b: Block, o: RenderOptions, loose: boolean, key: string): ReactNode {
  switch (b.type) {
    case "heading":
      return createElement(`h${b.depth}`, { key, id: b.id }, ...inlines(b.children, o, key));
    case "paragraph":
      return loose ? createElement("p", { key }, ...inlines(b.children, o, key)) : createElement(Fragment, { key }, ...inlines(b.children, o, key));
    case "code":
      return createElement(
        "pre",
        { key, "data-lang": LANG.test(b.lang) ? b.lang : undefined },
        createElement("code", { className: LANG.test(b.lang) ? `language-${b.lang}` : undefined }, b.value),
      );
    case "blockquote":
      return createElement("blockquote", { key }, ...blocks(b.children, o, key));
    case "hr":
      return createElement("hr", { key });
    case "list":
      return createElement(
        b.ordered ? "ol" : "ul",
        { key, start: b.ordered && b.start !== 1 ? b.start : undefined },
        ...b.items.map((item, i) => {
          const k = `${key}.${i}`;
          // A tight list keeps its items' text out of <p> (the first paragraph only, like CommonMark).
          const children = item.map((child, j) => block(child, o, b.loose || child.type !== "paragraph" || j > 0, `${k}.${j}`));
          return createElement("li", { key: k }, ...children);
        }),
      );
    case "table":
      return createElement(
        "div",
        { key, "data-md": "table" },
        createElement(
          "table",
          null,
          createElement("thead", null, createElement("tr", null, ...b.header.map((cell, i) => createElement("th", { key: i, style: alignStyle(b.align[i]) }, ...inlines(cell, o, `${key}.h${i}`))))),
          createElement(
            "tbody",
            null,
            ...b.rows.map((row, r) =>
              createElement("tr", { key: r }, ...row.map((cell, i) => createElement("td", { key: i, style: alignStyle(b.align[i]) }, ...inlines(cell, o, `${key}.${r}.${i}`)))),
            ),
          ),
        ),
      );
  }
}

function blocks(list: readonly Block[], o: RenderOptions, key: string): ReactNode[] {
  return list.map((b, i) => block(b, o, true, `${key}.${i}`));
}

/** renderMarkdown turns parsed blocks into React elements. */
export function renderMarkdown(list: readonly Block[], options: RenderOptions = {}): ReactNode[] {
  return blocks(list, options, "md");
}

export type MarkdownProps = RenderOptions & {
  /** A parsed document, or the source to parse. */
  doc?: MarkdownDoc;
  source?: string;
};

/** Markdown renders a document (the page wraps it in its typography styles). */
export function Markdown({ doc, source, link }: MarkdownProps): ReactNode {
  const d = doc ?? parseMarkdown(source ?? "");
  return createElement(Fragment, null, ...renderMarkdown(d.blocks, { link }));
}
