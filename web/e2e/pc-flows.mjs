// The PC site's part of the user's checklist (docs/runbook/ui-checklist.md:
// the general items 1-12 and P1-P9; docs/阶段4验收报告.md §8), run by
// scripts/e2e/webflows.sh after the smokes:
//
//   APP=https://astras.vip node web/e2e/pc-flows.mjs
//
// One account is made over the API; each step signs in its own tabs
// through the form. The widths are the checklist's 1024, 1280 and 1920,
// side by side. Pages change by the site's own router (no reload, so no
// token refresh per page; the gateway allows 60 sign-in calls a minute).
// Faults are staged by answering one request: offline (Chrome's network
// emulation), an expired access token, a degraded mark price. Steps that
// fail leave screenshots and a log (flows-lib.mjs).
import {
  api, budgetBuy, cancelOrders, colorsOf, contrastIssues, decimalIssues, desktop, flows, fmtTime, listDecimals, longAnimations, overflowX, PHONE_IOS,
  register, scrollThrough, signInApi, siteCookieDomain, spotAvailable, stage, truncatedWithoutHint, wsWatch,
} from "./flows-lib.mjs";
import { menuOnTop } from "./lib.mjs";

const APP = (process.env.APP ?? "https://astras.vip").replace(/\/$/, "");
const API = process.env.API ?? (APP.startsWith("http://localhost") ? "https://astras.vip" : APP);
const M_APP = APP.startsWith("http://localhost") ? APP.replace(/:\d+$/, ":5174") : APP.replace("://", "://m.");

const f = await flows({ site: "pc", app: APP, api: API });
const run = Date.now();
const user = { email: `e2e-pcflows-${run}@example.com`, password: `e2e pc flows ${run}` };
const WIDTHS = [1024, 1280, 1920];
const PUBLIC = ["/", "/markets", "/coin/BTC", "/trade/BTC-USDT", "/futures/BTC-USDT-PERP", "/announcements", "/help", "/legal/terms", "/account/settings"];
const PRIVATE = ["/assets", "/assets/deposit", "/assets/withdraw", "/assets/transfer", "/assets/history", "/account/security", "/account/sessions", "/notifications"];
const AUTH = ["/login", "/register", "/reset"];
const RED = "rgb(246, 70, 93)";
const GREEN = "rgb(14, 203, 129)";

// --- helpers ---------------------------------------------------------------

// The tab helpers are flows-lib's (tab.nav, tab.signIn, tab.clickTab, tab.fieldError).
const nav = (tab, path) => tab.nav(path);
const signIn = (tab, next) => tab.signIn(user, next);
const clickTab = (tab, text, nth) => tab.clickTab(text, nth);
const fieldError = (tab, selector) => tab.fieldError(selector);

/** signedInTab opens a tab at width signed in as the flows' account. */
const signedInTab = (name, width = 1280, extra = {}) => f.signedIn(user, { name, device: desktop(width), ...extra });

const notFound = (tab) => tab.page.evaluate(() => /页面不存在|没有交易对/.test(document.body.innerText));

/** focusShown reports whether the focused element (or the field box around it) shows a focus indicator. */
const focusShown = (tab) =>
  tab.page.evaluate(() => {
    const el = document.activeElement;
    if (!el || el === document.body) return false;
    const ring = (e) => {
      const s = getComputedStyle(e);
      return (s.outlineStyle !== "none" && parseFloat(s.outlineWidth) > 0) || (s.boxShadow && s.boxShadow !== "none");
    };
    for (let e = el, i = 0; e && i < 4; e = e.parentElement, i++) if (ring(e)) return true;
    return false;
  });

/** blur leaves any field, so the terminal's keys reach it. */
const blur = (tab) => tab.page.evaluate(() => document.activeElement instanceof HTMLElement && document.activeElement.blur());

const lastPrice = (tab) => tab.page.waitForFunction(() => Number(document.querySelector('input[aria-label="价格"]')?.value.replace(/,/g, "")) > 0, { timeout: 20000 });

/** openOrders waits for the open orders tab to count n. */
const openOrders = (tab, n) =>
  tab.page.waitForFunction((want) => [...document.querySelectorAll("[role=tab]")].some((el) => el.textContent.replace(/\s+/g, "") === want), { timeout: 20000 }, `当前委托(${n})`);

/** limitBuy places a limit buy 5% under the last price, through the confirmation when asked. */
async function limitBuy(tab, { confirm }) {
  await lastPrice(tab);
  const last = await tab.page.$eval('input[aria-label="价格"]', (el) => Number(el.value.replace(/,/g, "")));
  await tab.typeInto('input[aria-label="价格"]', (Math.floor(last * 0.95 * 100) / 100).toFixed(2));
  await tab.typeInto('input[aria-label="数量"]', "0.0002");
  await tab.page.keyboard.press("Enter");
  if (confirm) {
    await tab.page.waitForSelector("[role=dialog]", { visible: true });
    await tab.clickButton("买入", "[role=dialog]");
  }
}

// --- the account -----------------------------------------------------------

await f.step(
  "—",
  "an account over the API",
  async () => {
    const { accessToken } = await register(API, f.bypass, user.email, user.password);
    user.token = accessToken;
  },
  { fatal: true },
);
// Whatever ends the run, the account leaves no order on the book (P2 and P6 place some).
f.atExit(() => cancelOrders(API, user));

// P2 and P6 place a limit buy of 0.0002 BTC: they need the welcome funds.
// What the platform gives is in its profile (nothing at launch: they are
// skipped, saying so); the ledger credits them a moment after the sign-up.
user.usdt = 0;
await f.step("—", "what the platform gives a new account (its profile)", async () => {
  const profile = await api(API, "GET", "/v1/platform/profile");
  if (profile.status !== 200) throw new Error(`/v1/platform/profile: ${profile.status}`);
  user.gift = Number(profile.body.welcome_credits.find((c) => c.asset === "USDT")?.amount ?? 0);
});
if (user.gift > 0) {
  await f.step("—", `the welcome funds arrive (${user.gift} USDT)`, async () => {
    const until = Date.now() + 40_000;
    while ((user.usdt = Number(await spotAvailable(API, user.token, "USDT"))) <= 0) {
      if (Date.now() > until) throw new Error("no USDT 40 s after the sign-up");
      await new Promise((r) => setTimeout(r, 1000));
    }
  });
}
// The reason P2 and P6 cannot run, or "" (50 USDT is more than the order needs at any recent price).
const NO_FUNDS = () => {
  if (user.gift === undefined) return "the platform profile could not be read";
  if (user.gift <= 0) return "the platform gives no welcome funds";
  if (user.usdt <= 0) return `the welcome funds (${user.gift} USDT) did not arrive`;
  if (user.usdt < 50) return `the welcome funds, ${user.usdt} USDT, are short of the 50 USDT a 0.0002 BTC limit buy is given`;
  return "";
};

/** cleared cancels the account's orders after an order step, its failure not hiding the step's own. */
async function cleared() {
  try {
    await cancelOrders(API, user);
  } catch (e) {
    console.log(`     cancelling the account's orders: ${e.message}`);
  }
}

// --- P1: sign-in by keyboard -----------------------------------------------

const A = await f.open({ name: "main", device: desktop(1280) });

await f.step("P1", "keyboard sign-in: the account field has focus, Tab to the password, a visible focus, Enter signs in", async () => {
  await A.go("/login?next=%2Fassets");
  await A.page.waitForFunction(() => document.activeElement?.getAttribute("autocomplete") === "username", { timeout: 10000 });
  await A.page.keyboard.type(user.email);
  if (!(await focusShown(A))) throw new Error("the account field shows no focus");
  // Tab to the password: past the account field's clear button, if any, each stop visibly focused.
  for (let i = 0; i < 3; i++) {
    await A.page.keyboard.press("Tab");
    if ((await A.page.evaluate(() => document.activeElement?.getAttribute("autocomplete"))) === "current-password") break;
    if (!(await focusShown(A))) throw new Error(`no visible focus on ${await A.page.evaluate(() => document.activeElement?.outerHTML.slice(0, 80))}`);
  }
  if ((await A.page.evaluate(() => document.activeElement?.getAttribute("autocomplete"))) !== "current-password") throw new Error("Tab does not reach the password");
  if (!(await focusShown(A))) throw new Error("the password field shows no focus");
  await A.page.keyboard.type(user.password);
  // Every stop after it shows where the focus is.
  for (let i = 0; i < 3; i++) {
    await A.page.keyboard.press("Tab");
    const what = await A.page.evaluate(() => `${document.activeElement?.tagName} ${document.activeElement?.textContent?.trim().slice(0, 20)}`);
    if (!(await focusShown(A))) throw new Error(`no visible focus on ${what}`);
  }
  await A.page.focus('input[autocomplete="current-password"]');
  await A.page.keyboard.press("Enter");
  await A.waitPath("/assets", 30000);
});

await f.step("P1", "the code field of sign-up has focus as soon as the code is sent", async () => {
  const R = await f.open({ name: "register", device: desktop(1280) });
  try {
    await R.go("/register");
    await R.typeInto('input[autocomplete="email"]', `e2e-pcflows-code-${run}@example.com`);
    await R.typeInto('input[autocomplete="new-password"]', `e2e pc flows code ${run}`);
    await R.page.waitForSelector('button[role="checkbox"]', { visible: true });
    await R.page.click('button[role="checkbox"]');
    await R.clickButton("继续");
    await R.waitText("验证你的邮箱");
    await R.clickButton("发送验证码");
    await R.page.waitForFunction(() => document.activeElement?.getAttribute("autocomplete") === "one-time-code", { timeout: 15000 });
  } finally {
    await R.close();
  }
});

// The main tab is signed in for the steps after, whatever P1 found.
await f.step(
  "—",
  "the main tab is signed in",
  async () => {
    if (new URL(A.page.url()).pathname !== "/assets") await signIn(A);
  },
  { fatal: true },
);

// --- 1: navigation -----------------------------------------------------------

await f.step("1", "every link of the top bar (menus included) and the footer opens a page", async () => {
  await nav(A, "/");
  const links = await A.page.$$eval("header a[href], footer a[href]", (as) => [...new Set(as.map((a) => a.getAttribute("href")))]);
  const internal = links.filter((h) => h.startsWith("/") && !h.startsWith("//") && h !== "/docs/");
  if (internal.length < 15) throw new Error(`only ${internal.length} links: ${internal.join(" ")}`);
  const broken = [];
  for (const href of internal) {
    await nav(A, href);
    if (await notFound(A)) broken.push(href);
  }
  if (links.includes("/docs/")) {
    const r = await fetch(APP + "/docs/");
    if (r.status !== 200) broken.push(`/docs/ (${r.status})`);
  }
  if (broken.length) throw new Error(`dead links: ${broken.join(", ")}`);
  // The menus open on hover.
  await nav(A, "/");
  const menus = await A.page.$$("header nav .group");
  if (menus.length < 2) throw new Error(`${menus.length} menus in the top bar`);
  for (const m of menus) {
    await m.hover();
    await A.page.waitForFunction(
      (el) => [...el.children].some((c) => c.tagName === "DIV" && getComputedStyle(c).visibility === "visible" && getComputedStyle(c).opacity === "1"),
      { timeout: 5000 },
      m,
    );
  }
  await A.page.mouse.move(0, 600);
});

await f.step("1", "a top-bar menu opened over the markets table's stuck header shows its first item on top (合约, 资产)", async () => {
  // The markets: a new account's assets page is too short for its header to stick.
  await nav(A, "/markets");
  await menuOnTop(A.page, "合约");
  await menuOnTop(A.page, "资产");
});

await f.step("1", "a page that needs the account sends a visitor to sign in, and back after", async () => {
  const V = await f.open({ name: "visitor", device: desktop(1280) });
  try {
    await V.go("/assets/history");
    await V.page.waitForFunction(() => location.pathname === "/login" && new URLSearchParams(location.search).get("next") === "/assets/history", { timeout: 15000 });
    await V.typeInto('input[autocomplete="username"]', user.email);
    await V.typeInto('input[autocomplete="current-password"]', user.password);
    await V.page.keyboard.press("Enter");
    await V.waitPath("/assets/history", 30000);
  } finally {
    await V.close();
  }
});

// --- 2: no sideways scrolling at 1024 / 1280 / 1920 --------------------------

await f.step("2", `no page scrolls sideways at ${WIDTHS.join(" / ")}, each scrolled to its end`, async () => {
  const problems = [];
  await Promise.all(
    WIDTHS.map(async (w) => {
      const tab = await f.open({ name: `w${w}`, device: desktop(w) });
      try {
        await tab.go("/login");
        for (const p of AUTH) {
          await nav(tab, p);
          await scrollThrough(tab);
          for (const o of await overflowX(tab.page)) problems.push(`${w} ${p}: ${o}`);
        }
        await signIn(tab);
        for (const p of [...PUBLIC, ...PRIVATE]) {
          await nav(tab, p);
          await scrollThrough(tab);
          for (const o of await overflowX(tab.page)) problems.push(`${w} ${p}: ${o}`);
        }
      } finally {
        await tab.close();
      }
    }),
  );
  if (problems.length) throw new Error(`sideways scrolling:\n  ${problems.join("\n  ")}`);
});

// --- 3: contrast ---------------------------------------------------------------

await f.step("3", "text contrast at least 4.5:1 (3:1 for large text) on the main pages", async () => {
  const found = new Map();
  for (const p of ["/", "/markets", "/trade/BTC-USDT", "/futures/BTC-USDT-PERP", "/assets", "/account/settings"]) {
    await nav(A, p);
    for (const g of await contrastIssues(A.page)) {
      const k = g.pair;
      const prev = found.get(k) ?? { ...g, count: 0, pages: [] };
      prev.count += g.count;
      prev.pages.push(p);
      found.set(k, prev);
    }
  }
  if (found.size) {
    const lines = [...found.values()].sort((a, b) => b.count - a.count).map((g) => `${g.pair}: ${g.ratio}:1 < ${g.need}:1, ${g.count} texts on ${g.pages.join(" ")}, e.g. ${g.samples.map((s) => `"${s}"`).join(", ")}`);
    throw new Error(`low contrast:\n  ${lines.join("\n  ")}`);
  }
});

// --- 4: rise and fall colours ----------------------------------------------------

await f.step("4", "红涨绿跌 swaps the colours on the markets, the terminals (ticker, book, trades) and the assets", async () => {
  const check = async (up, down, where) => {
    const c = await colorsOf(A, ["text-up", "text-down"]);
    const bad = [...c["text-up"].filter((x) => x !== up).map((x) => `text-up ${x}`), ...c["text-down"].filter((x) => x !== down).map((x) => `text-down ${x}`)];
    if (bad.length) throw new Error(`${where}: ${bad.join(", ")}`);
    return c["text-up"].length + c["text-down"].length;
  };
  await nav(A, "/account/settings");
  await A.page.click('button[role="radio"][value="red-up"]');
  await A.page.waitForFunction(() => document.documentElement.dataset.updown === "red-up", { timeout: 5000 });
  let seen = 0;
  for (const p of ["/markets", "/trade/BTC-USDT", "/futures/BTC-USDT-PERP", "/assets/history"]) {
    await nav(A, p);
    if (p.startsWith("/trade") || p.startsWith("/futures")) {
      await A.page.waitForSelector('[aria-label="买盘"] [data-book-row]', { timeout: 20000 });
    }
    seen += await check(RED, GREEN, p);
  }
  if (seen < 10) throw new Error(`only ${seen} coloured figures found`);
  await nav(A, "/account/settings");
  await A.page.click('button[role="radio"][value="green-up"]');
  await A.page.waitForFunction(() => document.documentElement.dataset.updown === "green-up", { timeout: 5000 });
  await nav(A, "/trade/BTC-USDT");
  await A.page.waitForSelector('[aria-label="买盘"] [data-book-row]', { timeout: 20000 });
  await check(GREEN, RED, "/trade/BTC-USDT back to 绿涨红跌");
});

// --- 5: reduced motion -------------------------------------------------------------


/**
 * searchResults opens ⌘K, asks for BTC and returns the animations running
 * the moment its results are drawn (their entrance lasts 200 ms and more).
 */
async function searchResults(tab) {
  await tab.page.keyboard.down("Control");
  await tab.page.keyboard.press("k");
  await tab.page.keyboard.up("Control");
  await tab.page.waitForSelector('[role=dialog] input[placeholder="搜索币种"]', { visible: true });
  await tab.page.type('[role=dialog] input[placeholder="搜索币种"]', "BTC");
  await tab.page.waitForFunction(() => document.querySelectorAll("[role=dialog] [role=option]").length > 1, { timeout: 10000 });
  const running = await longAnimations(tab);
  await tab.page.keyboard.press("Escape");
  return running;
}

await f.step("5", "with reduced motion, page changes, list entrances, dialogs and price flashes do not animate", async () => {
  // Control: without the preference the same list does animate, so the check sees it.
  await nav(A, "/markets");
  if (!(await searchResults(A)).length) throw new Error("no animation seen even without the preference: the check sees nothing");

  const R = await f.open({ name: "reduced", device: desktop(1280), media: [{ name: "prefers-reduced-motion", value: "reduce" }] });
  const found = [];
  await R.go("/");
  // Each page sampled the moment its content is drawn.
  for (const [p, ready] of [
    ["/markets", () => document.querySelectorAll("main table tbody tr").length > 5],
    ["/account/settings", () => !!document.querySelector('[role=radiogroup][aria-label="涨跌颜色"]')],
    ["/announcements", () => document.querySelectorAll('main a[href^="/announcements/"]').length > 0],
  ]) {
    await R.page.evaluate((path) => {
      history.pushState({}, "", path);
      dispatchEvent(new PopStateEvent("popstate"));
    }, p);
    await R.page.waitForFunction(ready, { timeout: 15000 });
    for (const a of await longAnimations(R)) found.push(`${p}: ${a}`);
  }
  for (const a of await searchResults(R)) found.push(`search results: ${a}`);
  await R.close();
  if (found.length) throw new Error(`animations under reduced motion:\n  ${[...new Set(found)].slice(0, 20).join("\n  ")}`);
});

// --- 6: offline and back ----------------------------------------------------------

await f.step(
  "6",
  "offline shows 网络不可用 at once; back online the banner goes and prices stream again, without a reload",
  async () => {
    const O = await f.open({ name: "offline", device: desktop(1280) });
    try {
      const ws = await wsWatch(O.page);
      await O.go("/markets");
      await ws.until((w) => w.frames > 3, "prices stream before going offline");
      await O.page.evaluate(() => {
        window.__flowsMarker = "kept";
      });
      await O.page.setOfflineMode(true);
      await O.page.waitForFunction(() => [...document.querySelectorAll("[role=status]")].some((el) => el.innerText.includes("网络不可用")), { timeout: 10000 });
      await O.page.setOfflineMode(false);
      await O.page.waitForFunction(() => ![...document.querySelectorAll("[role=status]")].some((el) => /网络不可用|正在重新连接/.test(el.innerText)), { timeout: 45000 });
      const after = ws.frames;
      await ws.until((w) => w.frames > after + 3, "prices stream again after going back online", 45000);
      if ((await O.page.evaluate(() => window.__flowsMarker)) !== "kept") throw new Error("the page reloaded");
    } finally {
      await O.close();
    }
  },
  { allowErrors: [/ERR_INTERNET_DISCONNECTED|WebSocket|net::ERR|Failed to fetch|NetworkError/i] },
);

// --- 7: an expired access token in the middle of a form ------------------------------

await f.step("7", "an access token that expires mid-form is renewed and the request retried; the form keeps what was typed", async () => {
  await nav(A, "/assets/withdraw?asset=ETH&network=ETH-SEPOLIA");
  await A.page.waitForSelector('input[placeholder^="最小提现"]', { visible: true, timeout: 20000 });
  await A.typeInto('input[placeholder^="最小提现"]', "0.01");
  await A.clickButton("新地址");
  const address = "0x" + "52908400098527886E0F7030069857D2E4169EE7".toLowerCase();
  const staged = await stage(A, (req) => req.method() === "POST" && req.url().includes("/v1/wallet/withdraw-addresses/validate"), {
    status: 401,
    contentType: "application/json",
    body: JSON.stringify({ code: "AUTH_TOKEN_EXPIRED", message: "access token expired", trace_id: "flows" }),
  });
  try {
    const refreshed = A.page.waitForResponse((r) => r.url().includes("/v1/auth/token/refresh") && r.status() === 200, { timeout: 20000 });
    const retried = A.page.waitForResponse((r) => r.url().includes("/v1/wallet/withdraw-addresses/validate") && r.status() === 200 && !A.staged.has(r.request()), { timeout: 20000 });
    await A.typeInto('input[placeholder^="粘贴或输入"]', address);
    await refreshed;
    await retried;
    if (!staged.used) throw new Error("the request was never answered with the expired token");
  } finally {
    await staged.stop();
  }
  if (new URL(A.page.url()).pathname !== "/assets/withdraw") throw new Error(`left the form for ${A.page.url()}`);
  const kept = await A.page.evaluate(() => [document.querySelector('input[placeholder^="最小提现"]')?.value, document.querySelector('input[placeholder^="粘贴或输入"]')?.value]);
  if (kept[0] !== "0.01" || kept[1] !== address) throw new Error(`the form lost what was typed: ${JSON.stringify(kept)}`);
});

// --- 8: errors next to their fields -------------------------------------------------

await f.step("8", "form errors sit under their fields: sign-in (the server's code), sign-up, an order, a withdrawal address", async () => {
  const V = await f.open({ name: "forms", device: desktop(1280) });
  try {
    // A server error code mapped to its field.
    await V.go("/login");
    await V.typeInto('input[autocomplete="username"]', user.email);
    await V.typeInto('input[autocomplete="current-password"]', "not the password 1");
    await V.page.keyboard.press("Enter");
    await V.page.waitForFunction(() => document.querySelector('input[autocomplete="current-password"]')?.getAttribute("aria-invalid") === "true", { timeout: 15000 });
    const pw = await fieldError(V, 'input[autocomplete="current-password"]');
    if (!pw?.text.includes("账户或密码不正确") || !pw.below) throw new Error(`sign-in: ${JSON.stringify(pw)}`);
    // Client checks of sign-up.
    await V.go("/register");
    await V.typeInto('input[autocomplete="email"]', "not-an-email");
    await V.typeInto('input[autocomplete="new-password"]', "123");
    await V.page.keyboard.press("Tab");
    await V.page.click('button[role="checkbox"]');
    await V.clickButton("继续");
    await V.page.waitForFunction(() => document.querySelector('input[autocomplete="email"]')?.getAttribute("aria-invalid") === "true", { timeout: 5000 });
    const em = await fieldError(V, 'input[autocomplete="email"]');
    if (!em?.below) throw new Error(`sign-up email: ${JSON.stringify(em)}`);
  } finally {
    await V.close();
  }
  // An order beyond the balance: the amount says so under it (the form
  // keeps quantities to the pair's lot, so a too-small one cannot be typed).
  await nav(A, "/trade/BTC-USDT");
  await lastPrice(A);
  await A.typeInto('input[aria-label="数量"]', "1000");
  await A.page.waitForFunction(() => document.querySelector('input[aria-label="金额"]')?.getAttribute("aria-invalid") === "true", { timeout: 5000 });
  const total = await fieldError(A, 'input[aria-label="金额"]');
  if (!total?.text.includes("超出可用余额") || !total.below) throw new Error(`order amount: ${JSON.stringify(total)}`);
  await A.typeInto('input[aria-label="数量"]', "");
  // A malformed withdrawal address.
  await nav(A, "/assets/withdraw?asset=ETH&network=ETH-SEPOLIA");
  await A.clickButton("新地址");
  await A.typeInto('input[placeholder^="粘贴或输入"]', "0x123");
  await A.page.waitForFunction(() => document.querySelector('input[placeholder^="粘贴或输入"]')?.getAttribute("aria-invalid") === "true", { timeout: 10000 });
  const addr = await fieldError(A, 'input[placeholder^="粘贴或输入"]');
  if (!addr?.text || !addr.below) throw new Error(`withdrawal address: ${JSON.stringify(addr)}`);
});

// --- 9: empty states of a new account ------------------------------------------------

/** emptyStates describes each empty state in scope: illustration, title, description, action. */
const emptyStates = (tab, scope = "main") =>
  tab.page.evaluate((sc) =>
    [...document.querySelectorAll(`${sc} svg[viewBox="0 0 88 88"]`)].map((svg) => {
      const box = svg.parentElement;
      const texts = [...box.children].filter((c) => c !== svg && c.tagName === "DIV").map((c) => c.innerText.trim()).filter(Boolean);
      const action = box.querySelector("a[href], button");
      return { title: texts[0] ?? "", description: texts[1] ?? "", action: action ? action.innerText.trim() || action.getAttribute("aria-label") : "" };
    }),
  scope);

await f.step("9", "a new account's empty lists (orders, fills, positions) each show an illustration, a line and a next step", async () => {
  const missing = [];
  const look = async (where, tabText) => {
    if (tabText) await clickTab(A, tabText);
    await A.page.waitForFunction(() => document.querySelector('main svg[viewBox="0 0 88 88"]'), { timeout: 15000 });
    for (const e of await emptyStates(A)) {
      const lack = [!e.title && "a title", !e.description && "a line saying what goes here", !e.action && "a next step"].filter(Boolean);
      if (lack.length) missing.push(`${where} "${e.title}": no ${lack.join(", no ")}`);
    }
  };
  await nav(A, "/trade/BTC-USDT");
  await look("spot open orders", "当前委托");
  await look("spot order history", "历史委托");
  await look("spot fills", "成交明细");
  await nav(A, "/futures/BTC-USDT-PERP");
  await look("futures positions", "仓位");
  if (missing.length) throw new Error(`empty states without their parts:\n  ${missing.join("\n  ")}`);
});

// --- 10: truncated text ----------------------------------------------------------------

await f.step("10", "text cut short (addresses, names, IDs) can be read whole: a title, a tooltip or a copy button", async () => {
  const N = await signedInTab("narrow", 1024);
  try {
    const found = [];
    for (const p of ["/markets", "/assets", "/assets/history", "/account/security", "/account/sessions", "/trade/BTC-USDT", "/assets/deposit?asset=ETH&network=ETH-SEPOLIA"]) {
      await nav(N, p);
      for (const x of await truncatedWithoutHint(N.page)) found.push(`${p}: ${x}`);
    }
    if (found.length) throw new Error(`cut short with no way to read it:\n  ${found.join("\n  ")}`);
  } finally {
    await N.close();
  }
});

// --- 11: time zone ----------------------------------------------------------------------

await f.step("11", "changing the time zone in settings changes the times in tables (YYYY-MM-DD HH:mm:ss)", async () => {
  const firstTime = async () => {
    await A.page.waitForFunction(() => document.querySelector("main table tbody time[datetime]"), { timeout: 20000 });
    return A.page.$eval("main table tbody time[datetime]", (el) => ({ iso: el.getAttribute("datetime"), text: el.innerText.trim() }));
  };
  await nav(A, "/assets/history");
  const before = await firstTime();
  if (before.text !== fmtTime(before.iso, "Asia/Singapore")) throw new Error(`in the browser's zone ${before.iso} shows "${before.text}", not "${fmtTime(before.iso, "Asia/Singapore")}"`);
  await nav(A, "/account/settings");
  await A.page.click('button[aria-label="时区"]');
  await A.page.waitForSelector('input[aria-label="搜索时区或城市"]', { visible: true });
  await A.page.type('input[aria-label="搜索时区或城市"]', "America/New_York");
  await A.page.waitForFunction(() => [...document.querySelectorAll("[role=option]")].some((o) => o.innerText.includes("America/New York")), { timeout: 5000 });
  await A.page.evaluate(() => [...document.querySelectorAll("[role=option]")].find((o) => o.innerText.includes("America/New York"))?.click());
  await A.page.waitForFunction(() => JSON.parse(localStorage.getItem("exchange.settings") ?? "{}").state?.timeZone === "America/New_York", { timeout: 5000 });
  await nav(A, "/assets/history");
  const after = await firstTime();
  if (after.text !== fmtTime(after.iso, "America/New_York")) throw new Error(`in New York ${after.iso} shows "${after.text}", not "${fmtTime(after.iso, "America/New_York")}"`);
  if (!/^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/.test(after.text)) throw new Error(`format: "${after.text}"`);
  await nav(A, "/account/sessions");
  await A.page.waitForFunction(() => document.querySelector("main time[datetime]"), { timeout: 20000 });
  const sessions = await A.page.$$eval("main time[datetime]", (els) => els.map((el) => ({ iso: el.getAttribute("datetime"), text: el.innerText.trim(), title: el.title })));
  const wrong = sessions.filter((s) => /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/.test(s.text) && s.text !== fmtTime(s.iso, "America/New_York"));
  if (wrong.length) throw new Error(`device times not in the zone: ${JSON.stringify(wrong.slice(0, 3))}`);
  await nav(A, "/account/settings");
  await A.page.evaluate(() => {
    const s = JSON.parse(localStorage.getItem("exchange.settings"));
    s.state.timeZone = "";
    localStorage.setItem("exchange.settings", JSON.stringify(s));
  });
});

// --- 12: numbers ---------------------------------------------------------------------------

await f.step("12", "figures right-aligned in tabular digits, with thousands separators, the pair's and the assets' decimals", async () => {
  const pairs = await api(API, "GET", "/v1/market/pairs");
  if (pairs.status !== 200) throw new Error(`/v1/market/pairs: ${pairs.status}`);
  const btc = pairs.body.pairs.find((p) => p.symbol === "BTC-USDT");
  if (!btc) throw new Error("no BTC-USDT in /v1/market/pairs");
  const problems = [];
  const columns = async (where, headers) => {
    const cells = await A.page.evaluate((hs) => {
      const out = [];
      for (const table of document.querySelectorAll("main table")) {
        const ths = [...table.tHead?.rows[0]?.cells ?? []];
        for (const h of hs) {
          const i = ths.findIndex((th) => th.innerText.trim().startsWith(h));
          if (i < 0) continue;
          for (const row of [...table.tBodies[0].rows].slice(0, 5)) {
            const td = row.cells[i];
            if (!td || td.colSpan > 1) continue;
            const s = getComputedStyle(td);
            out.push({ h, text: td.innerText.trim(), align: s.textAlign, nums: s.fontVariantNumeric });
          }
        }
      }
      return out;
    }, headers);
    if (!cells.length) problems.push(`${where}: none of the columns ${headers.join(", ")}`);
    for (const c of cells) {
      if (c.align !== "right") problems.push(`${where} ${c.h} "${c.text}" aligned ${c.align}`);
      if (!c.nums.includes("tabular-nums")) problems.push(`${where} ${c.h} "${c.text}" without tabular digits`);
    }
    return cells;
  };
  await nav(A, "/markets");
  await A.page.waitForFunction(() => document.querySelectorAll("main table tbody tr").length > 5, { timeout: 20000 });
  const market = await columns("markets", ["最新价", "24h 涨跌", "成交额"]);
  const btcRow = await A.page.evaluate(() => [...document.querySelectorAll("main table tbody tr")].find((r) => /BTC\s*\/?\s*USDT/.test(r.innerText))?.innerText ?? "");
  if (!/\d{1,3}(,\d{3})+(\.\d+)?/.test(btcRow)) problems.push(`markets: BTC's price without thousands separators: "${btcRow.replace(/\s+/g, " ")}"`);
  await nav(A, "/assets");
  await columns("assets", ["可用", "冻结", "估值"]);
  // Each asset's available and frozen amounts at its decimals (at most 8, cut).
  const held = await A.page.evaluate(() => {
    const table = [...document.querySelectorAll("main table")].find((t) => [...(t.tHead?.rows[0]?.cells ?? [])].some((th) => th.innerText.trim().startsWith("可用")));
    if (!table) return [];
    const ths = [...table.tHead.rows[0].cells].map((th) => th.innerText.trim());
    const cols = ["可用", "冻结"].map((h) => ths.findIndex((t) => t.startsWith(h)));
    return [...table.tBodies[0].rows]
      .filter((r) => r.cells.length > Math.max(...cols))
      .slice(0, 20)
      .map((r) => ({ asset: r.cells[0].querySelector(".font-medium")?.innerText.trim() ?? r.cells[0].innerText.trim(), values: cols.map((i) => r.cells[i].innerText.trim()) }));
  });
  if (held.length) problems.push(...decimalIssues("assets", held, await listDecimals(API)));
  else if (user.usdt > 0) problems.push("assets: no rows read");
  // The trade tape: prices at the pair's decimals, amounts right-aligned.
  await nav(A, "/trade/BTC-USDT");
  await clickTab(A, "最新成交");
  const tape = await A.page.waitForFunction(
    () => {
      const rows = [...document.querySelectorAll("[role=tabpanel] button.grid")].filter((r) => r.children.length === 3 && /\d/.test(r.innerText)).slice(0, 5);
      return rows.length ? rows.map((r) => [...r.children].map((c) => ({ text: c.innerText.trim(), align: getComputedStyle(c).textAlign }))) : null;
    },
    { timeout: 20000 },
  );
  for (const row of await tape.jsonValue()) {
    const price = row[0]?.text ?? "";
    const decimals = price.includes(".") ? price.split(".")[1].length : 0;
    if (decimals !== btc.price_decimals) problems.push(`trades: price "${price}" with ${decimals} decimals, the pair has ${btc.price_decimals}`);
    if (row[1] && row[1].align !== "right") problems.push(`trades: amount "${row[1].text}" aligned ${row[1].align}`);
  }
  if (problems.length) throw new Error(problems.join("\n  "));
  if (!market.length) throw new Error("no market figures checked");
});

// --- P2: ordering by keyboard -----------------------------------------------------------------

const P2 = "keyboard order: / opens the pair search, arrows and Enter pick, B and S switch side, Enter submits, Esc closes";
if (NO_FUNDS()) f.skip("P2", P2, NO_FUNDS());
else await f.step("P2", P2, async () => {
  try {
    await keyboardOrder();
  } finally {
    await cleared();
  }
});

async function keyboardOrder() {
  await nav(A, "/trade/BTC-USDT");
  await lastPrice(A);
  await blur(A);
  await A.page.keyboard.press("/");
  await A.page.waitForFunction(() => document.activeElement?.getAttribute("aria-label") === "搜索币种", { timeout: 5000 });
  await A.page.keyboard.type("ETH");
  await A.page.waitForFunction(() => document.querySelectorAll('[role=listbox][aria-label="切换交易对"] [role=option]').length > 1, { timeout: 5000 });
  await A.page.keyboard.press("ArrowDown");
  const picked = await A.page.evaluate(() => document.querySelector('[role=listbox][aria-label="切换交易对"] [role=option].bg-bg-3')?.innerText.replace(/\s+/g, " ").trim());
  await A.page.keyboard.press("Enter");
  await A.page.waitForFunction(() => location.pathname !== "/trade/BTC-USDT", { timeout: 5000 });
  const path = new URL(A.page.url()).pathname;
  if (!path.startsWith("/trade/") || !picked) throw new Error(`picked "${picked}" went to ${path}`);
  await nav(A, "/trade/BTC-USDT");
  await lastPrice(A);
  await blur(A);
  await A.page.keyboard.press("s");
  await A.page.waitForFunction(() => [...document.querySelectorAll("button[type=submit]")].some((b) => b.innerText.includes("卖出")), { timeout: 5000 });
  await A.page.keyboard.press("b");
  await A.page.waitForFunction(() => [...document.querySelectorAll("button[type=submit]")].some((b) => b.innerText.includes("买入")), { timeout: 5000 });
  const last = await A.page.$eval('input[aria-label="价格"]', (el) => Number(el.value.replace(/,/g, "")));
  await A.typeInto('input[aria-label="价格"]', (Math.floor(last * 0.95 * 100) / 100).toFixed(2));
  await A.typeInto('input[aria-label="数量"]', "0.0002");
  await A.page.keyboard.press("Enter");
  await A.page.waitForSelector("[role=dialog]", { visible: true });
  await A.page.keyboard.press("Escape");
  await A.page.waitForFunction(() => !document.querySelector("[role=dialog]"), { timeout: 5000 });
  await A.page.focus('input[aria-label="数量"]');
  await A.page.keyboard.press("Enter");
  await A.page.waitForSelector("[role=dialog]", { visible: true });
  await A.clickButton("买入", "[role=dialog]");
  await openOrders(A, 1);
  await A.clickButton("撤单");
  await openOrders(A, 0);
}

// --- P3: ⌘K ------------------------------------------------------------------------------------

await f.step("P3", "⌘K / Ctrl+K opens the site search, which reaches the futures and the spot terminal", async () => {
  await nav(A, "/markets");
  for (const [mod, want] of [["Meta", "/futures/BTC-USDT-PERP"], ["Control", "/trade/BTC-USDT"]]) {
    await A.page.keyboard.down(mod);
    await A.page.keyboard.press("k");
    await A.page.keyboard.up(mod);
    await A.page.waitForSelector('[role=dialog] input[placeholder="搜索币种"]', { visible: true, timeout: 5000 });
    await A.page.type('[role=dialog] input[placeholder="搜索币种"]', "BTC");
    await A.page.waitForFunction(() => document.querySelectorAll("[role=dialog] [role=option]").length > 1, { timeout: 5000 });
    const futures = want.startsWith("/futures");
    await A.page.evaluate((fut) => {
      const options = [...document.querySelectorAll("[role=dialog] [role=option]")];
      const o = options.find((x) => x.innerText.includes("BTC") && x.innerText.includes("永续") === fut && (fut || x.innerText.includes("/USDT")));
      o?.click();
    }, futures);
    await A.waitPath(want, 10000);
  }
});

// --- P4: the order book fills the form; switching pairs keeps the connection -------------------------

await f.step("P4", "a click on the book fills the price, Shift+click the cumulative amount; switching pairs opens no new connection", async () => {
  const B = await signedInTab("book", 1280);
  try {
    const ws = await wsWatch(B.page);
    await nav(B, "/trade/BTC-USDT");
    await lastPrice(B);
    await B.page.waitForFunction(() => document.querySelectorAll('[aria-label="卖盘"] [data-book-row]').length > 3, { timeout: 20000 });
    const num = (s) => Number(String(s).replace(/,/g, ""));
    // A row's cells with text: price, quantity, cumulative (its depth bar has none).
    const cells = (row) => row.evaluate((r) => [...r.children].map((c) => c.innerText.trim()).filter(Boolean));
    const rows = await B.page.$$('[aria-label="卖盘"] [data-book-row]');
    const best = rows[rows.length - 1];
    const bestPrice = num((await cells(best))[0]);
    if (!(bestPrice > 0)) throw new Error(`the best ask reads ${JSON.stringify(await cells(best))}`);
    await best.click();
    await B.page.waitForFunction((p) => Number(document.querySelector('input[aria-label="价格"]').value.replace(/,/g, "")) <= p, { timeout: 5000 }, bestPrice);
    const price = num(await B.page.$eval('input[aria-label="价格"]', (el) => el.value));
    if (Math.abs(price - bestPrice) > bestPrice * 0.001) throw new Error(`price ${price} after a click on ${bestPrice}`);
    const deeper = rows[rows.length - 3];
    const cumulative = num((await cells(deeper))[2]);
    await B.page.keyboard.down("Shift");
    await deeper.click();
    await B.page.keyboard.up("Shift");
    await B.page.waitForFunction((c) => {
      const q = Number(document.querySelector('input[aria-label="数量"]').value.replace(/,/g, ""));
      return q > 0 && q <= c;
    }, { timeout: 5000 }, cumulative);
    const qty = num(await B.page.$eval('input[aria-label="数量"]', (el) => el.value));
    if (qty < cumulative * 0.99 - 0.0001) throw new Error(`quantity ${qty} after a Shift+click on a cumulative ${cumulative}`);
    const connections = ws.created;
    await B.page.click('button[aria-label="切换交易对"]');
    await B.page.waitForSelector('input[aria-label="搜索币种"]', { visible: true });
    await B.page.type('input[aria-label="搜索币种"]', "ETH");
    // The list ranks names too (Ethena comes first): pick ETH/USDT itself.
    const eth = await B.page.waitForFunction(
      () => [...document.querySelectorAll('[role=listbox][aria-label="切换交易对"] [role=option]')].find((o) => o.innerText.trim().startsWith("ETH/USDT")) ?? null,
      { timeout: 10000 },
    );
    await eth.asElement().click();
    await B.page.waitForFunction(() => location.pathname.startsWith("/trade/ETH"), { timeout: 10000 });
    const frames = ws.frames;
    await ws.until((w) => w.frames > frames + 3, "prices of the new pair arrive");
    if (ws.created !== connections) throw new Error(`switching pairs opened ${ws.created - connections} new connections`);
  } finally {
    await B.close();
  }
});

// --- P5: the terminal's layout survives a reload ------------------------------------------------------

await f.step("P5", "the panel height, depth chart, interval, indicators and book precision survive a reload", async () => {
  const P = await signedInTab("prefs", 1280);
  try {
    await nav(P, "/trade/BTC-USDT");
    await P.page.waitForSelector('[role=separator][aria-label="调整委托面板高度"]', { visible: true, timeout: 20000 });
    const before = await P.page.$eval('[role=separator][aria-label="调整委托面板高度"]', (el) => Number(el.getAttribute("aria-valuenow")));
    await P.page.focus('[role=separator][aria-label="调整委托面板高度"]');
    await P.page.keyboard.press("ArrowUp");
    await P.page.keyboard.press("ArrowUp");
    await P.page.waitForFunction((b) => Number(document.querySelector('[role=separator][aria-label="调整委托面板高度"]').getAttribute("aria-valuenow")) === b + 40, { timeout: 5000 }, before);
    await P.page.evaluate(() => [...document.querySelectorAll("button[aria-expanded]")].find((b) => b.innerText.includes("深度图"))?.click());
    await P.page.waitForSelector('svg[role=img][aria-label="深度图"]', { timeout: 10000 });
    await P.page.waitForSelector('[role=group][aria-label="周期"] button', { visible: true, timeout: 20000 });
    await P.page.evaluate(() => [...document.querySelectorAll('[role=group][aria-label="周期"] button')].find((b) => b.innerText.trim() === "1小时")?.click());
    const ema = await P.page.$eval('[role=group][aria-label="指标"]', (g) => [...g.querySelectorAll("button")].find((b) => b.innerText.trim() === "EMA")?.getAttribute("aria-pressed"));
    await P.page.evaluate(() => [...document.querySelectorAll('[role=group][aria-label="指标"] button')].find((b) => b.innerText.trim() === "EMA")?.click());
    await P.page.click('button[role=combobox][aria-label="价格精度"]');
    await P.page.waitForSelector("[role=option]", { visible: true });
    // A Radix select takes a real press.
    const other = await P.page.waitForFunction(() => [...document.querySelectorAll("[role=option]")].find((x) => x.getAttribute("aria-selected") !== "true") ?? null);
    const step = await other.evaluate((o) => o.innerText.trim());
    await other.asElement().click();
    const state = () =>
      P.page.evaluate(() => ({
        height: Number(document.querySelector('[role=separator][aria-label="调整委托面板高度"]')?.getAttribute("aria-valuenow")),
        depth: !!document.querySelector('svg[role=img][aria-label="深度图"]'),
        interval: [...document.querySelectorAll('[role=group][aria-label="周期"] button[aria-pressed=true]')].map((b) => b.innerText.trim()).join(),
        ema: [...document.querySelectorAll('[role=group][aria-label="指标"] button')].find((b) => b.innerText.trim() === "EMA")?.getAttribute("aria-pressed"),
        step: document.querySelector('button[role=combobox][aria-label="价格精度"]')?.innerText.trim(),
      }));
    await P.page.waitForFunction((s) => document.querySelector('button[role=combobox][aria-label="价格精度"]')?.innerText.trim() === s, { timeout: 5000 }, step);
    const want = await state();
    if (want.height !== before + 40 || !want.depth || want.interval !== "1小时" || want.ema === ema || want.step !== step) throw new Error(`not set: ${JSON.stringify(want)}`);
    await P.page.reload({ waitUntil: "domcontentloaded" });
    await P.page.waitForSelector('[role=group][aria-label="周期"] button[aria-pressed=true]', { timeout: 20000 });
    await P.page.waitForSelector('svg[role=img][aria-label="深度图"]', { timeout: 20000 });
    await P.page.waitForFunction((s) => document.querySelector('button[role=combobox][aria-label="价格精度"]')?.innerText.trim() === s, { timeout: 20000 }, step).catch(() => {});
    const got = await state();
    if (JSON.stringify(got) !== JSON.stringify(want)) throw new Error(`after a reload ${JSON.stringify(got)}, before ${JSON.stringify(want)}`);
  } finally {
    await P.close();
  }
});

// --- P6: no confirmation when turned off; insufficient balance offers a deposit ----------------------------

const P6 = "the order confirmation can be turned off in settings; more than the balance shows the error and a 充值 entry";
if (NO_FUNDS()) f.skip("P6", P6, NO_FUNDS());
else await f.step("P6", P6, async () => {
  try {
    await orderWithoutConfirmation();
  } finally {
    await cleared();
  }
});

async function orderWithoutConfirmation() {
  await nav(A, "/account/settings");
  const sw = 'button[role=switch][aria-checked]';
  await A.page.waitForSelector(sw, { visible: true });
  await A.page.evaluate(() => {
    const label = [...document.querySelectorAll("label")].find((l) => l.innerText.includes("下单二次确认"));
    const button = label?.htmlFor ? document.getElementById(label.htmlFor) : label?.closest("div")?.querySelector("button[role=switch]");
    if (button?.getAttribute("aria-checked") === "true") button.click();
  });
  await A.page.waitForFunction(() => JSON.parse(localStorage.getItem("exchange.settings")).state.confirmOrders === false, { timeout: 5000 });
  await nav(A, "/trade/BTC-USDT");
  await limitBuy(A, { confirm: false });
  await openOrders(A, 1);
  if (await A.page.$("[role=dialog]")) throw new Error("a confirmation opened while turned off");
  await A.clickButton("撤单");
  await openOrders(A, 0);
  // More than the balance: the error in place, and the deposit entry beside the balance.
  await A.typeInto('input[aria-label="数量"]', "1000");
  await A.page.waitForFunction(() => document.body.innerText.includes("超出可用余额"), { timeout: 5000 });
  await A.typeInto('input[aria-label="数量"]', "");
  await A.clickButton("充值", "form");
  await A.waitPath("/assets/deposit", 10000);
  await nav(A, "/account/settings");
  await A.page.evaluate(() => {
    const s = JSON.parse(localStorage.getItem("exchange.settings"));
    s.state.confirmOrders = true;
    localStorage.setItem("exchange.settings", JSON.stringify(s));
  });
}

// --- P7: futures: degraded mark price, funding countdown, high leverage ------------------------------------

await f.step("P7", "futures: a degraded mark price shows the reduce-only banner; the funding countdown ticks; leverage above 20 warns", async () => {
  const mark = await api(API, "GET", "/v1/market/BTC-USDT-PERP/mark-price");
  if (mark.status !== 200) throw new Error(`mark price: ${mark.status}`);
  const D = await signedInTab("futures", 1280);
  try {
    const staged = await stage(D, (req) => req.method() === "GET" && new URL(req.url()).pathname === "/v1/market/BTC-USDT-PERP/mark-price", {
      status: 200,
      contentType: "application/json",
      body: JSON.stringify({ ...mark.body, degraded: true }),
    });
    try {
      await nav(D, "/futures/BTC-USDT-PERP");
      await D.page.waitForFunction(() => [...document.querySelectorAll("[role=status]")].some((el) => el.innerText.includes("当前只能减仓")), { timeout: 20000 });
      if (!staged.used) throw new Error("the mark price was never asked for");
    } finally {
      await staged.stop();
    }
    const countdown = () =>
      D.page.evaluate(() => {
        const box = [...document.querySelectorAll("div")].find((d) => d.firstElementChild?.innerText?.trim() === "资金费率 / 倒计时");
        return box?.querySelector("span.text-fg-1")?.innerText.trim() ?? null;
      });
    const first = await countdown();
    if (!/^\d{2}:\d{2}:\d{2}$/.test(first ?? "")) throw new Error(`countdown "${first}"`);
    await D.page.waitForFunction(
      (t) => [...document.querySelectorAll("div")].find((d) => d.firstElementChild?.innerText?.trim() === "资金费率 / 倒计时")?.querySelector("span.text-fg-1")?.innerText.trim() !== t,
      { timeout: 3000 },
      first,
    );
    await D.page.click('button[aria-label="杠杆"]');
    await D.page.waitForSelector('input[aria-label="杠杆倍数"]', { visible: true });
    const warning = () => D.page.evaluate(() => [...document.querySelectorAll("[role=dialog] *")].some((el) => el.getAttribute("role") === "alert" && el.innerText.includes("高杠杆风险") && getComputedStyle(el).opacity === "1"));
    await D.typeInto('input[aria-label="杠杆倍数"]', "25");
    await D.page.waitForFunction(() => [...document.querySelectorAll("[role=dialog] [role=alert]")].some((el) => el.innerText.includes("高杠杆风险")), { timeout: 5000 });
    await D.page.waitForFunction(() => [...document.querySelectorAll("[role=dialog] [role=alert]")].some((el) => getComputedStyle(el).opacity === "1"), { timeout: 5000 });
    await D.typeInto('input[aria-label="杠杆倍数"]', "20");
    await D.page.waitForFunction(() => ![...document.querySelectorAll("[role=dialog] [role=alert]")].some((el) => el.innerText.includes("高杠杆风险")), { timeout: 5000 });
    if (await warning()) throw new Error("the warning stays at 20x");
    await D.clickButton("取消", "[role=dialog]");
  } finally {
    await D.close();
  }
});

// --- P8: the site preference ----------------------------------------------------------------------------------

const P8 = "切换到手机版 keeps this computer on m.astras.vip; a phone that chose the PC site is not sent back";
if (APP.startsWith("http://localhost")) f.skip("P8", P8, "needs the deployed sites' nginx");
else await f.step("P8", P8, async () => {
  const S = await f.open({ name: "switch", device: desktop(1280) });
  try {
    await S.go("/");
    await S.clickButton("切换到手机版", "footer");
    await S.page.waitForFunction((m) => location.origin === m, { timeout: 15000 }, M_APP);
    await S.page.waitForSelector("#root *", { timeout: 15000 });
    const pref = (await S.context.cookies()).find((c) => c.name === "site_pref");
    if (pref?.value !== "m") throw new Error(`site_pref ${JSON.stringify(pref)}`);
    // A desktop asking for a mobile page again stays there.
    await S.page.goto(M_APP + "/markets", { waitUntil: "domcontentloaded" });
    if (new URL(S.page.url()).origin !== M_APP) throw new Error(`sent to ${S.page.url()}`);
  } finally {
    await S.close();
  }
  const P = await f.open({ name: "phone", device: PHONE_IOS });
  try {
    await P.page.goto(M_APP + "/markets", { waitUntil: "domcontentloaded" });
    await P.context.setCookie({ name: "site_pref", value: "pc", domain: siteCookieDomain(APP), path: "/" });
    await P.page.goto(APP + "/markets", { waitUntil: "domcontentloaded" });
    if (new URL(P.page.url()).origin !== APP) throw new Error(`a phone with site_pref=pc was sent to ${P.page.url()}`);
  } finally {
    await P.close();
  }
});

// --- P9: margin trading (margin design 2026-10-06 §7) -------------------------------------------

const P9 =
  "margin: the market list tags isolated pairs; the terminal trades from the cross account with its gauge, what it may borrow and the transfer, borrow and repay dialogs; a borrow there shows as a debt, an order is tagged 全仓; the assets' margin page shows both kinds of account and repays everything";

/** marginAs is the margin API as the flows' account, signed in once (each sign-in counts against the IP and the device). */
async function marginAs() {
  const token = await signInApi(API, user);
  const auth = { Authorization: `Bearer ${token}` };
  return {
    token,
    auth,
    /** cross is the cross account and its USDT. */
    async cross() {
      const r = await api(API, "GET", "/v1/margin/accounts", undefined, auth);
      if (r.status !== 200) throw new Error(`/v1/margin/accounts: ${r.status}`);
      return { account: r.body.cross, usdt: r.body.cross.balances.find((b) => b.asset === "USDT") };
    },
    /** post writes, each with its own idempotency key. */
    async post(path, body) {
      const r = await api(API, "POST", path, body, { ...auth, "Idempotency-Key": `flows-${Date.now()}-${Math.random()}` });
      if (r.status !== 200) throw new Error(`${path} ${JSON.stringify(body)}: ${r.status} ${JSON.stringify(r.body)}`);
      return r.body;
    },
  };
}

// "" when margin trading is open to the flows' account (MARGIN_TRADE: its
// status, then margin.enabled) and it has the 25 USDT the step moves and
// trades with, else why P9 cannot run.
async function marginClosed() {
  const m = await marginAs();
  if (Number(await spotAvailable(API, m.token, "USDT")) < 25) return "the account has under 25 USDT (no welcome funds?)";
  const r = await api(API, "GET", "/v1/user/eligibility?feature=MARGIN_TRADE", undefined, m.auth);
  if (r.status !== 200) return `the eligibility could not be read (${r.status})`;
  return r.body.allowed ? "" : `margin trading is not open to the account (${r.body.reason_code})`;
}

/** marginCleared cancels the account's orders, repays the cross account and moves its USDT back to spot; its failure does not hide the step's own. */
async function marginCleared() {
  try {
    const m = await marginAs();
    await cancelOrders(API, user, m.token);
    const { usdt } = await m.cross();
    if (!usdt) return;
    if (Number(usdt.borrowed) > 0 || Number(usdt.interest) > 0) await m.post("/v1/margin/repay", { account: "MARGIN_CROSS", asset: "USDT", amount: "ALL" });
    const free = (await m.cross()).usdt?.free ?? "0";
    if (Number(free) > 0) await m.post("/v1/margin/transfer", { direction: "OUT", account: "MARGIN_CROSS", asset: "USDT", amount: free });
  } catch (e) {
    console.log(`     clearing the cross margin account: ${e.message}`);
  }
}

const noMargin = await marginClosed();
// The step clears the account when it ends; past its time budget the run
// goes on without it, so the exit clears it again.
if (!noMargin) f.atExit(marginCleared);
if (noMargin) f.skip("P9", P9, noMargin);
else await f.step("P9", P9, async () => {
  let D;
  try {
    const pairs = await api(API, "GET", "/v1/margin/pairs");
    if (pairs.status !== 200) throw new Error(`/v1/margin/pairs: ${pairs.status}`);
    const btc = pairs.body.items.find((p) => p.symbol === "BTC-USDT");
    if (!btc?.isolated) throw new Error("BTC-USDT takes no isolated margin account");
    // 20 USDT into the cross account over the API: the smokes move funds through the dialogs.
    const m = await marginAs();
    await m.post("/v1/margin/transfer", { direction: "IN", account: "MARGIN_CROSS", asset: "USDT", amount: "20" });
    D = await signedInTab("margin", 1280);
    // The market list: the pair's isolated leverage beside its name.
    await nav(D, "/markets");
    await D.page.waitForFunction(
      (tag) =>
        [...document.querySelectorAll('[title*="逐仓"]')].some((el) => el.innerText.trim() === tag && el.parentElement?.innerText.replace(/\s+/g, "").startsWith("BTC/USDT")),
      { timeout: 20000 },
      `${btc.leverage}x`,
    );
    // The terminal on the cross account: gauge, what it may borrow (under the
    // form since B120), the three dialogs (in the bar).
    await nav(D, "/trade/BTC-USDT");
    const bar = '[data-testid="margin-bar"]';
    const info = '#order-form [data-testid="margin-info"]';
    await D.page.waitForSelector(bar, { visible: true, timeout: 20000 });
    await D.clickButton("全仓", bar);
    await D.page.waitForSelector(`${info} [data-testid="margin-level"]`, { visible: true, timeout: 10000 });
    await D.page.waitForFunction((i) => /\d/.test(document.querySelector(`${i} [data-testid="margin-borrowable"]`)?.innerText ?? ""), { timeout: 15000 }, info);
    for (const [link, form] of [["划转", "transfer"], ["还币", "repay"]]) {
      await D.clickButton(link, bar);
      await D.page.waitForSelector(`[role=dialog] form[data-testid="margin-${form}-form"]`, { visible: true, timeout: 10000 });
      await D.page.keyboard.press("Escape");
      await D.page.waitForSelector("[role=dialog]", { hidden: true, timeout: 10000 });
    }
    // Borrow 1 USDT from the bar's dialog: the account owes it, the gauge leaves "no debt".
    await D.clickButton("借币", bar);
    const borrow = '[role=dialog] form[data-testid="margin-borrow-form"]';
    await D.page.waitForSelector(borrow, { visible: true, timeout: 10000 });
    await D.page.waitForFunction((f) => document.querySelector(f)?.innerText.includes("USDT"), { timeout: 10000 }, borrow);
    await D.typeInto(`${borrow} input[aria-label="数量"]`, "1");
    await D.clickButton("确认借币", "[role=dialog]");
    await D.waitText("已借入 1 USDT");
    await D.page.waitForSelector("[role=dialog]", { hidden: true, timeout: 10000 });
    const owed = await m.cross();
    if (Number(owed.usdt?.borrowed) !== 1 || owed.account.margin_level === null) throw new Error(`after the borrow: ${JSON.stringify(owed.account)}`);
    // A limit buy from the cross account of about 15 USDT (of the 21 it
    // holds, whatever the price) rests tagged 全仓, then is cancelled.
    await lastPrice(D);
    const last = await D.page.$eval('input[aria-label="价格"]', (el) => Number(el.value.replace(/,/g, "")));
    const order = await budgetBuy(API, "BTC-USDT", last, 15);
    await D.typeInto('input[aria-label="价格"]', order.price);
    await D.typeInto('input[aria-label="数量"]', order.quantity);
    await D.page.keyboard.press("Enter");
    await D.page.waitForSelector("[role=dialog]", { visible: true });
    await D.clickButton("买入", "[role=dialog]");
    await openOrders(D, 1);
    await D.page.waitForFunction(() => [...document.querySelectorAll('[data-testid="margin-tag"]')].some((el) => el.innerText.includes("全仓")), { timeout: 15000 });
    await D.clickButton("撤单");
    await openOrders(D, 0);
    await D.clickButton("现货", bar);
    await D.page.waitForSelector(info, { hidden: true, timeout: 10000 });
    // The assets' margin page: the cross account with its debt, the isolated
    // section, and the debt repaid in full from the row's 还币.
    await nav(D, "/assets/margin");
    const cross = '[data-testid="margin-account-MARGIN_CROSS"]';
    await D.page.waitForFunction((c) => document.querySelector(`${c} tbody`)?.innerText.includes("USDT"), { timeout: 20000 }, cross);
    await D.waitText("逐仓杠杆");
    await D.clickButton("还币", `${cross} tbody`);
    const repay = '[role=dialog] form[data-testid="margin-repay-form"]';
    await D.page.waitForSelector(repay, { visible: true, timeout: 10000 });
    await D.clickButton("最大", "[role=dialog]");
    await D.waitText("将全部还清");
    await D.clickButton("确认还币", "[role=dialog]");
    await D.waitText("已还");
    await D.page.waitForSelector("[role=dialog]", { hidden: true, timeout: 10000 });
    const until = Date.now() + 15_000;
    for (;;) {
      const { usdt } = await m.cross();
      if (usdt && Number(usdt.borrowed) === 0 && Number(usdt.interest) === 0) break;
      if (Date.now() > until) throw new Error(`still owed after 还币 in full: ${JSON.stringify(usdt)}`);
      await new Promise((r) => setTimeout(r, 1000));
    }
  } finally {
    await D?.close();
    await marginCleared();
  }
});

// --- G4: a coin-margined contract (design 2026-10-06 §2.6) ---------------------------------------

// BTC-USD-PERP trades whole contracts of 100 USD, its margin and result in
// BTC from BTC's own futures account. The account buys a little BTC with its
// welcome funds, moves it to futures from the transfer page, opens one
// contract at the market and closes it from its position card.
const G4 =
  "a coin-margined contract: BTC moves to its own futures account; BTC-USD-PERP takes whole contracts and shows their BTC and USD worth; a 1-contract long shows in contracts with its value, and closes from its card";
const coinContract = await api(API, "GET", "/v1/market/contracts/BTC-USD-PERP");
if (NO_FUNDS()) f.skip("G4", G4, NO_FUNDS());
else if (coinContract.status !== 200 || coinContract.body.status !== "TRADING") {
  f.skip("G4", G4, `BTC-USD-PERP is not trading (${coinContract.status} ${coinContract.body?.status ?? ""})`);
} else await f.step("G4", G4, async () => {
  const token = await signInApi(API, user);
  const auth = { Authorization: `Bearer ${token}` };
  const buy = await api(API, "POST", "/v1/orders", { symbol: "BTC-USDT", side: "BUY", type: "MARKET", quote_amount: "40" }, { ...auth, "Idempotency-Key": `pcflows-g4-${run}` });
  if (buy.status !== 202) throw new Error(`a market buy of 40 USDT of BTC: ${buy.status} ${JSON.stringify(buy.body)}`);
  const until = Date.now() + 20_000;
  while (Number(await spotAvailable(API, token, "BTC")) < 0.0003) {
    if (Date.now() > until) throw new Error("under 0.0003 BTC on the spot account 20 s after the buy");
    await new Promise((r) => setTimeout(r, 1000));
  }
  const T = await signedInTab("coin-m", 1440);
  try {
    await nav(T, "/assets/transfer?asset=BTC");
    await T.typeInto('form[data-testid="transfer-form"] input[inputmode="decimal"]', "0.0003");
    await T.clickButton("确认划转");
    await T.waitText("已划转");
    await nav(T, "/futures/BTC-USD-PERP");
    const form = `#order-form`;
    await T.page.waitForSelector(`${form} input[aria-label="数量"]`, { visible: true, timeout: 20000 });
    // Whole contracts, BTC's futures account (the 0.0003 just moved).
    await T.page.waitForFunction(
      (sel) => {
        const text = document.querySelector(sel)?.innerText ?? "";
        const available = /可用\s*([\d.,]+) BTC/.exec(text);
        return /钱包余额\s*[\d.,]+ BTC/.test(text) && available && Number(available[1].replace(/,/g, "")) >= 0.0002;
      },
      { timeout: 20000 },
      form,
    );
    const unit = await T.page.$eval(`${form} input[aria-label="数量"]`, (el) => el.parentElement?.innerText ?? "");
    if (!unit.includes("张")) throw new Error(`the quantity field's unit: "${unit}"`);
    await T.clickTab("市价");
    await T.typeInto(`${form} input[aria-label="数量"]`, "1");
    await T.page.waitForFunction(() => /≈ 0\.\d+ BTC · 100 USD/.test(document.querySelector('[data-testid="contracts-value"]')?.innerText ?? ""), { timeout: 10000 });
    await T.clickButton("开多", form);
    await T.page.waitForSelector("[role=dialog]", { visible: true, timeout: 10000 });
    const confirm = await T.page.$eval("[role=dialog]", (d) => d.innerText);
    if (!/1 张/.test(confirm) || !/100 USD/.test(confirm)) throw new Error(`the confirmation: ${confirm.replace(/\s+/g, " ")}`);
    await T.clickButton("确认", "[role=dialog]");
    await T.page.waitForSelector('[data-testid="position-value"]', { visible: true, timeout: 30000 });
    const card = await T.page.$eval('[data-testid="position-value"]', (el) => el.closest("article")?.innerText ?? "");
    if (!/价值 0\.\d+ BTC · 100 USD/.test(card) || !/持仓数量 \(张\)/.test(card) || !/未实现盈亏 \(BTC\)/.test(card)) {
      throw new Error(`the position card: ${card.replace(/\s+/g, " ")}`);
    }
    await T.page.evaluate(() =>
      [...(document.querySelector('[data-testid="position-value"]')?.closest("article")?.querySelectorAll("button") ?? [])].find((b) => b.innerText.trim() === "平仓")?.click(),
    );
    await T.page.waitForSelector("[role=dialog]", { visible: true, timeout: 10000 });
    await T.clickButton("平多", "[role=dialog]");
    await T.page.waitForFunction(() => !document.querySelector('[data-testid="position-value"]'), { timeout: 30000 });
  } finally {
    await T.close();
  }
});

// --- the account leaves nothing on the book -----------------------------------------------------

// The order steps cancel their own: a step whose cancelling failed shows
// here, before the exit's cleanup cancels what is left.
await f.step("—", "no order of the flows' account is left open", async () => {
  const token = await signInApi(API, user);
  const until = Date.now() + 15_000;
  for (;;) {
    const open = await api(API, "GET", "/v1/orders?status=ACTIVE", undefined, { Authorization: `Bearer ${token}` });
    if (open.status !== 200) throw new Error(`active orders: ${open.status}`);
    if (!open.body.items.length) return;
    if (Date.now() > until) {
      const left = open.body.items.map((o) => `${o.symbol} ${o.side} ${o.price ?? ""} (${o.order_id})`).join(", ");
      throw new Error(`${open.body.items.length} orders left open by the steps: ${left}`);
    }
    await new Promise((r) => setTimeout(r, 1000));
  }
});

await f.done();
