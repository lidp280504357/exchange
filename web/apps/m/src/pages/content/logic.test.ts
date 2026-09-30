import { describe, expect, it } from "vitest";
import { groupByCategory, helpCategories, matchArticle, relatedArticles } from "./logic";

const articles = [
  { slug: "register-login", category: "account", title: "注册与登录", summary: "邮箱注册，设置密码" },
  { slug: "account-security", category: "account", title: "账户安全", summary: "身份验证器与防钓鱼码" },
  { slug: "deposit", category: "funds", title: "如何充值", summary: "选择币种和网络" },
  { slug: "withdraw", category: "funds", title: "How to withdraw", summary: "Address book and limits" },
  { slug: "faq", category: "faq", title: "常见问题", summary: "资金为模拟" },
  { slug: "misc", category: "zzz", title: "Other", summary: "" },
];

describe("matchArticle", () => {
  it("matches every word in the title or the summary, ignoring case", () => {
    expect(matchArticle(articles[2]!, "充值")).toBe(true);
    expect(matchArticle(articles[2]!, "网络")).toBe(true);
    expect(matchArticle(articles[3]!, "WITHDRAW limits")).toBe(true);
    expect(matchArticle(articles[3]!, "withdraw deposit")).toBe(false);
    expect(matchArticle(articles[3]!, "   ")).toBe(true);
  });
});

describe("helpCategories", () => {
  it("keeps the help centre's order and puts unknown categories last", () => {
    expect(helpCategories(articles)).toEqual(["account", "funds", "faq", "zzz"]);
    expect(helpCategories([])).toEqual([]);
  });
});

describe("groupByCategory", () => {
  it("groups in list order", () => {
    const groups = groupByCategory(articles);
    expect(groups.map((g) => g.category)).toEqual(["account", "funds", "faq", "zzz"]);
    expect(groups[0]!.items.map((a) => a.slug)).toEqual(["register-login", "account-security"]);
  });
});

describe("relatedArticles", () => {
  it("lists the other articles of the same category", () => {
    expect(relatedArticles(articles, "deposit").map((a) => a.slug)).toEqual(["withdraw"]);
    expect(relatedArticles(articles, "faq")).toEqual([]);
    expect(relatedArticles(articles, "missing")).toEqual([]);
    expect(relatedArticles(articles, "register-login", 0)).toEqual([]);
  });
});
