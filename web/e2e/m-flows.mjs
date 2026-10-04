// The mobile site's part of the user's checklist (docs/runbook/ui-checklist.md:
// the general items 1-12, M1-M5, and §8-1's walk of docs/阶段4验收报告.md),
// run by scripts/e2e/webflows.sh after the smokes:
//
//   APP=https://m.astras.vip node web/e2e/m-flows.mjs
//
// Phones are emulated (touch, an iPhone or Android user agent, so nginx
// keeps them here) at the checklist's widths 360, 390 and 430. Gestures
// are real touch events (Chrome DevTools Protocol), so the sheets' drag,
// pull-to-refresh and the terminal's swipe run their own handlers. One
// account is made over the API for most steps; the walk signs up through
// the form on each phone. Faults are staged by answering one request
// (flows-lib.mjs); real devices, iOS autofill and how a gesture feels stay
// on the manual list.
import {
  code, colorsOf, contrastIssues, flows, fmtTime, inboxCount, longAnimations, overflowX, PHONE_ANDROID, PHONE_IOS, phone, register, scrollThrough, siteCookieDomain,
  stage, textAligned, truncatedWithoutHint, wsWatch,
} from "./flows-lib.mjs";

const APP = (process.env.APP ?? "https://m.astras.vip").replace(/\/$/, "");
const API = process.env.API ?? (APP.startsWith("http://localhost") ? "https://m.astras.vip" : APP);
const PC_APP = APP.startsWith("http://localhost") ? APP.replace(/:\d+$/, ":5173") : APP.replace("://m.", "://");

const f = await flows({ site: "m", app: APP, api: API });
const run = Date.now();
const user = { email: `e2e-mflows-${run}@example.com`, password: `e2e mobile flows ${run}` };
const WIDTHS = [360, 390, 430];
const PUBLIC = ["/", "/markets", "/trade/BTC-USDT", "/futures/BTC-USDT-PERP", "/me", "/coin/BTC", "/announcements", "/help", "/legal/terms", "/account/settings"];
const PRIVATE = ["/assets", "/assets/deposit", "/assets/withdraw", "/assets/transfer", "/assets/history", "/account/security", "/account/sessions", "/notifications"];
const AUTH = ["/login", "/register", "/reset"];
const RED = "rgb(246, 70, 93)";
const GREEN = "rgb(14, 203, 129)";

// --- helpers ---------------------------------------------------------------

async function nav(tab, path) {
  await tab.page.evaluate((p) => {
    history.pushState({}, "", p);
    dispatchEvent(new PopStateEvent("popstate"));
  }, path);
  await tab.page.waitForFunction((p) => location.pathname + location.search === p, { timeout: 10000 }, path);
  await tab.settled();
}

async function signIn(tab, next = "/assets") {
  await tab.go(`/login?next=${encodeURIComponent(next)}`);
  await tab.typeInto('input[autocomplete="username"]', user.email);
  await tab.typeInto('input[autocomplete="current-password"]', user.password);
  await tab.page.keyboard.press("Enter");
  await tab.waitPath(next.split("?")[0], 30000);
}

async function signedInTab(name, device = phone(390), extra = {}) {
  const tab = await f.open({ name, device, ...extra });
  await signIn(tab);
  return tab;
}

/** sheet waits for a bottom sheet to be open and still (its slide finished). */
async function sheet(tab) {
  await tab.page.waitForSelector("[role=dialog][data-state=open]", { visible: true, timeout: 10000 });
  await tab.page.waitForFunction(
    () => {
      const d = document.querySelector("[role=dialog][data-state=open]");
      return d && d.getAnimations({ subtree: true }).every((a) => a.playState !== "running") && getComputedStyle(d).transform === "none";
    },
    { timeout: 10000 },
  );
}

/** drag moves a finger from (x, y) by (dx, dy) in steps, a frame apart, and lifts it. */
async function drag(tab, x, y, dx, dy, steps = 12) {
  await tab.page.touchscreen.touchStart(x, y);
  for (let i = 1; i <= steps; i++) {
    await tab.page.touchscreen.touchMove(x + (dx * i) / steps, y + (dy * i) / steps);
    await tab.frames(1);
  }
  await tab.page.touchscreen.touchEnd();
}

const center = (tab, selector) =>
  tab.page.$eval(selector, (el) => {
    const r = el.getBoundingClientRect();
    return { x: r.left + r.width / 2, y: r.top + r.height / 2, w: r.width, h: r.height };
  });

const fieldError = (tab, selector) =>
  tab.page.evaluate((sel) => {
    const input = document.querySelector(sel);
    if (!input || input.getAttribute("aria-invalid") !== "true") return null;
    const msg = (input.getAttribute("aria-describedby") ?? "").split(/\s+/).map((id) => document.getElementById(id)).find((el) => el?.innerText.trim());
    if (!msg) return null;
    const a = input.getBoundingClientRect();
    const m = msg.getBoundingClientRect();
    return { text: msg.innerText.trim(), below: m.top >= a.top && m.top - a.bottom < 80 };
  }, selector);

/**
 * clickTab presses a tab by its text (Radix tabs switch on a real press,
 * not a scripted click), brought to the middle of the screen first: near
 * the bottom the terminal's fixed buy and sell bar would take the press.
 */
async function clickTab(tab, text, nth = 0) {
  const handle = await tab.page.waitForFunction(
    (txt, n) => [...document.querySelectorAll("[role=tab]")].filter((el) => el.textContent.trim().startsWith(txt))[n] ?? null,
    { timeout: 10000 },
    text,
    nth,
  );
  await handle.evaluate((el) => el.scrollIntoView({ block: "center" }));
  await tab.frames(2);
  await handle.asElement().click();
  await tab.page.waitForFunction((txt) => [...document.querySelectorAll("[role=tab][data-state=active]")].some((el) => el.textContent.trim().startsWith(txt)), { timeout: 5000 }, text);
}

// --- the account -----------------------------------------------------------

await f.step("—", "an account over the API (the welcome funds)", () => register(API, f.bypass, user.email, user.password), { fatal: true });
const A = await f.open({ name: "main", device: phone(390) });
await f.step("—", "the main tab signs in", () => signIn(A), { fatal: true });

// --- 1: navigation ------------------------------------------------------------

await f.step("1", "every link of the tab bar, the home page and the me page opens its page", async () => {
  const broken = [];
  const seen = new Set();
  for (const from of ["/", "/me", "/assets"]) {
    await nav(A, from);
    const links = await A.page.$$eval("nav a[href], main a[href]", (as) => as.map((a) => a.getAttribute("href")));
    for (const href of links) {
      if (!href.startsWith("/") || href.startsWith("//") || seen.has(href)) continue;
      seen.add(href);
      await nav(A, href);
      const want = new URL(APP + href);
      const at = new URL(A.page.url());
      const dead = at.pathname !== want.pathname || (await A.page.evaluate(() => /没有交易对|未找到该币种|文章不存在/.test(document.body.innerText)));
      if (dead) broken.push(`${href} (from ${from}) -> ${at.pathname}`);
    }
  }
  if (seen.size < 15) throw new Error(`only ${seen.size} links: ${[...seen].join(" ")}`);
  if (broken.length) throw new Error(`dead links: ${broken.join(", ")}`);
});

await f.step("1", "a page that needs the account sends a visitor to sign in, and back after", async () => {
  const V = await f.open({ name: "visitor", device: phone(390) });
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

// --- 2: no sideways scrolling ------------------------------------------------------

await f.step("2", `no page scrolls sideways at ${WIDTHS.join(" / ")}, each scrolled to its end`, async () => {
  const problems = [];
  await Promise.all(
    WIDTHS.map(async (w) => {
      const tab = await f.open({ name: `w${w}`, device: phone(w) });
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

// --- 3: contrast -------------------------------------------------------------------

await f.step("3", "text contrast at least 4.5:1 (3:1 for large text) on the main pages", async () => {
  const found = new Map();
  for (const p of ["/", "/markets", "/trade/BTC-USDT", "/assets", "/me"]) {
    await nav(A, p);
    for (const g of await contrastIssues(A.page)) {
      const prev = found.get(g.pair) ?? { ...g, count: 0, pages: [] };
      prev.count += g.count;
      prev.pages.push(p);
      found.set(g.pair, prev);
    }
  }
  if (found.size) {
    const lines = [...found.values()].sort((a, b) => b.count - a.count).map((g) => `${g.pair}: ${g.ratio}:1 < ${g.need}:1, ${g.count} texts on ${g.pages.join(" ")}, e.g. ${g.samples.map((s) => `"${s}"`).join(", ")}`);
    throw new Error(`low contrast:\n  ${lines.join("\n  ")}`);
  }
});

// --- 4: rise and fall colours ----------------------------------------------------------

await f.step("4", "红涨绿跌 swaps the colours on the markets, the terminal (book, trades) and the history", async () => {
  const check = async (up, down, where) => {
    const c = await colorsOf(A, ["text-up", "text-down", "bg-up", "bg-down"]);
    const bad = [
      ...c["text-up"].filter((x) => x !== up).map((x) => `text-up ${x}`),
      ...c["text-down"].filter((x) => x !== down).map((x) => `text-down ${x}`),
      ...c["bg-up"].filter((x) => !x.startsWith(up.replace(")", ""))).map((x) => `bg-up ${x}`),
      ...c["bg-down"].filter((x) => !x.startsWith(down.replace(")", ""))).map((x) => `bg-down ${x}`),
    ];
    if (bad.length) throw new Error(`${where}: ${bad.join(", ")}`);
    return Object.values(c).flat().length;
  };
  await nav(A, "/account/settings");
  await A.page.click('button[role="radio"][value="red-up"]');
  await A.page.waitForFunction(() => document.documentElement.dataset.updown === "red-up", { timeout: 5000 });
  let seen = 0;
  await nav(A, "/markets");
  seen += await check(RED, GREEN, "/markets");
  await nav(A, "/trade/BTC-USDT");
  await clickTab(A, "盘口");
  await A.page.waitForSelector('[aria-label="买盘"] [data-book-row]', { timeout: 20000 });
  seen += await check(RED, GREEN, "/trade/BTC-USDT book");
  await clickTab(A, "成交");
  seen += await check(RED, GREEN, "/trade/BTC-USDT trades");
  if (seen < 10) throw new Error(`only ${seen} coloured figures found`);
  await nav(A, "/account/settings");
  await A.page.click('button[role="radio"][value="green-up"]');
  await A.page.waitForFunction(() => document.documentElement.dataset.updown === "green-up", { timeout: 5000 });
  await nav(A, "/markets");
  await check(GREEN, RED, "/markets back to 绿涨红跌");
});

// --- 5: reduced motion ---------------------------------------------------------------------


await f.step("5", "with reduced motion, page changes, list entrances and sheets do not animate", async () => {
  await nav(A, "/trade/BTC-USDT");
  await A.clickButton("买入 BTC");
  await A.page.waitForSelector("[role=dialog]", { visible: true });
  const control = await longAnimations(A);
  await A.page.keyboard.press("Escape");
  await A.page.waitForSelector("[role=dialog]", { hidden: true });
  if (!control.length) throw new Error("no animation seen even without the preference: the check sees nothing");
  const R = await f.open({ name: "reduced", device: phone(390), media: [{ name: "prefers-reduced-motion", value: "reduce" }] });
  const found = [];
  await R.go("/");
  // Each page sampled the moment its content is drawn.
  for (const [p, ready] of [
    ["/markets", () => document.querySelectorAll(':is(ul, [role=list])[aria-label="行情"] > :is(li, [role=listitem])').length > 5],
    ["/me", () => !!document.querySelector('[data-testid="me-quick"]')],
    ["/trade/BTC-USDT", () => [...document.querySelectorAll("button")].some((b) => b.innerText.trim() === "买入 BTC")],
  ]) {
    await R.page.evaluate((path) => {
      history.pushState({}, "", path);
      dispatchEvent(new PopStateEvent("popstate"));
    }, p);
    await R.page.waitForFunction(ready, { timeout: 15000 });
    for (const a of await longAnimations(R)) found.push(`${p}: ${a}`);
  }
  await R.settled();
  await R.clickButton("买入 BTC");
  await R.page.waitForSelector("[role=dialog]", { visible: true });
  for (const a of await longAnimations(R)) found.push(`order sheet: ${a}`);
  await R.close();
  if (found.length) throw new Error(`animations under reduced motion:\n  ${[...new Set(found)].slice(0, 20).join("\n  ")}`);
});

// --- 6: offline and back -------------------------------------------------------------------

await f.step(
  "6",
  "offline shows 网络不可用 at once; back online the strip goes and prices stream again, without a reload",
  async () => {
    const O = await f.open({ name: "offline", device: phone(390) });
    try {
      const ws = await wsWatch(O.page);
      await O.go("/markets");
      await ws.until((w) => w.frames > 3, "prices stream before going offline");
      await O.page.evaluate(() => {
        window.__flowsMarker = "kept";
      });
      await O.page.setOfflineMode(true);
      await O.page.waitForFunction(() => document.querySelector("header > [role=status]")?.innerText.includes("网络不可用"), { timeout: 10000 });
      await O.page.setOfflineMode(false);
      await O.page.waitForFunction(() => !document.querySelector("header > [role=status]"), { timeout: 45000 });
      const after = ws.frames;
      await ws.until((w) => w.frames > after + 3, "prices stream again after going back online", 45000);
      if ((await O.page.evaluate(() => window.__flowsMarker)) !== "kept") throw new Error("the page reloaded");
    } finally {
      await O.close();
    }
  },
  { allowErrors: [/ERR_INTERNET_DISCONNECTED|WebSocket|net::ERR|Failed to fetch|NetworkError/i] },
);

// --- 7: an expired access token in the middle of a form -------------------------------------

/** newAddress opens the withdrawal address book of ETH on Sepolia in its new-address form. */
async function newAddress(tab) {
  await nav(tab, "/assets/withdraw?asset=ETH&network=ETH-SEPOLIA");
  await tab.page.waitForSelector('button[aria-haspopup="dialog"]', { visible: true, timeout: 20000 });
  await tab.page.evaluate(() => [...document.querySelectorAll('main button[aria-haspopup="dialog"]')].find((b) => /地址/.test(b.innerText))?.click());
  await sheet(tab);
  // An empty book opens on the new address's form; one with addresses lists them first.
  const form = '[role=dialog] input[placeholder^="粘贴或输入"]';
  await tab.page.waitForFunction(
    (sel) => document.querySelector(sel) || [...document.querySelectorAll("[role=dialog] button")].some((b) => b.innerText.trim() === "添加新地址"),
    { timeout: 10000 },
    form,
  );
  if (!(await tab.page.$(form))) await tab.clickButton("添加新地址", "[role=dialog]");
  await tab.page.waitForSelector(form, { visible: true, timeout: 10000 });
}

await f.step("7", "an access token that expires mid-form is renewed and the request retried; the form keeps what was typed", async () => {
  await newAddress(A);
  const address = "0x52908400098527886e0f7030069857d2e4169ee7";
  const staged = await stage(A, (req) => req.method() === "POST" && req.url().includes("/v1/wallet/withdraw-addresses/validate"), {
    status: 401,
    contentType: "application/json",
    body: JSON.stringify({ code: "AUTH_TOKEN_EXPIRED", message: "access token expired", trace_id: "flows" }),
  });
  try {
    const refreshed = A.page.waitForResponse((r) => r.url().includes("/v1/auth/token/refresh") && r.status() === 200, { timeout: 20000 });
    const retried = A.page.waitForResponse((r) => r.url().includes("/v1/wallet/withdraw-addresses/validate") && r.status() === 200 && !A.staged.has(r.request()), { timeout: 20000 });
    await A.typeInto('[role=dialog] input[placeholder^="粘贴或输入"]', address);
    await refreshed;
    await retried;
    if (!staged.used) throw new Error("the request was never answered with the expired token");
  } finally {
    await staged.stop();
  }
  if (new URL(A.page.url()).pathname !== "/assets/withdraw") throw new Error(`left the form for ${A.page.url()}`);
  const kept = await A.page.$eval('[role=dialog] input[placeholder^="粘贴或输入"]', (el) => el.value);
  if (kept !== address) throw new Error(`the form lost what was typed: "${kept}"`);
  await A.page.keyboard.press("Escape");
});

// --- 8: errors next to their fields -----------------------------------------------------------

await f.step("8", "form errors sit under their fields: sign-in (the server's code), an order, a withdrawal address", async () => {
  const V = await f.open({ name: "forms", device: phone(390) });
  try {
    await V.go("/login");
    await V.typeInto('input[autocomplete="username"]', user.email);
    await V.typeInto('input[autocomplete="current-password"]', "not the password 1");
    await V.page.keyboard.press("Enter");
    await V.page.waitForFunction(() => document.querySelector('input[autocomplete="current-password"]')?.getAttribute("aria-invalid") === "true", { timeout: 15000 });
    const pw = await fieldError(V, 'input[autocomplete="current-password"]');
    if (!pw?.text.includes("账户或密码不正确") || !pw.below) throw new Error(`sign-in: ${JSON.stringify(pw)}`);
  } finally {
    await V.close();
  }
  await nav(A, "/trade/BTC-USDT");
  await A.clickButton("买入 BTC");
  await sheet(A);
  await A.page.waitForFunction(() => Number(document.querySelector('[role=dialog] input[aria-label="价格"]')?.value.replace(/,/g, "")) > 0, { timeout: 20000 });
  // Beyond the balance (the form keeps quantities to the pair's lot).
  await A.typeInto('[role=dialog] input[aria-label="数量"]', "1000");
  await A.page.waitForFunction(() => document.querySelector('[role=dialog] input[aria-label="金额"]')?.getAttribute("aria-invalid") === "true", { timeout: 5000 });
  const total = await fieldError(A, '[role=dialog] input[aria-label="金额"]');
  if (!total?.text.includes("超出可用余额") || !total.below) throw new Error(`order amount: ${JSON.stringify(total)}`);
  await A.page.keyboard.press("Escape");
  await A.page.waitForSelector("[role=dialog]", { hidden: true });
  await newAddress(A);
  await A.typeInto('[role=dialog] input[placeholder^="粘贴或输入"]', "0x123");
  await A.page.waitForFunction(() => document.querySelector('[role=dialog] input[placeholder^="粘贴或输入"]')?.getAttribute("aria-invalid") === "true", { timeout: 10000 });
  const addr = await fieldError(A, '[role=dialog] input[placeholder^="粘贴或输入"]');
  if (!addr?.text || !addr.below) throw new Error(`withdrawal address: ${JSON.stringify(addr)}`);
  await A.page.keyboard.press("Escape");
});

// --- 9: empty states ---------------------------------------------------------------------------

await f.step("9", "a new account's empty lists (orders, fills, positions) each show an illustration, a line and a next step", async () => {
  const missing = [];
  const look = async (where) => {
    await A.page.waitForFunction(() => document.querySelector("[role=tabpanel]:not([hidden]) svg[aria-hidden]"), { timeout: 15000 });
    const states = await A.page.evaluate(() =>
      [...document.querySelectorAll("[role=tabpanel]:not([hidden]) svg[aria-hidden]")]
        .map((svg) => svg.parentElement)
        .filter((box) => box.classList.contains("text-center"))
        .map((box) => {
          const texts = [...box.children].filter((c) => c.tagName === "DIV").map((c) => c.innerText.trim()).filter(Boolean);
          return { title: texts[0] ?? "", description: texts[1] ?? "", action: box.querySelector("a[href], button") ? "yes" : "" };
        }),
    );
    for (const e of states) {
      const lack = [!e.title && "a title", !e.description && "a line saying what goes here", !e.action && "a next step"].filter(Boolean);
      if (lack.length) missing.push(`${where} "${e.title}": no ${lack.join(", no ")}`);
    }
  };
  await nav(A, "/trade/BTC-USDT?orders=open");
  await look("spot open orders");
  await clickTab(A, "历史委托");
  await look("spot order history");
  await clickTab(A, "成交明细");
  await look("spot fills");
  await nav(A, "/futures/BTC-USDT-PERP");
  await clickTab(A, "仓位");
  await look("futures positions");
  if (missing.length) throw new Error(`empty states without their parts:\n  ${missing.join("\n  ")}`);
});

// --- 10: truncated text -------------------------------------------------------------------------

await f.step("10", "text cut short can be read whole: a title, a tooltip or a copy button", async () => {
  const N = await signedInTab("narrow", phone(360));
  try {
    const found = [];
    for (const p of ["/markets", "/assets", "/me", "/assets/deposit?asset=ETH&network=ETH-SEPOLIA", "/account/sessions", "/assets/history"]) {
      await nav(N, p);
      for (const x of await truncatedWithoutHint(N.page)) found.push(`${p}: ${x}`);
    }
    if (found.length) throw new Error(`cut short with no way to read it:\n  ${found.join("\n  ")}`);
  } finally {
    await N.close();
  }
});

// --- 11: time zone ------------------------------------------------------------------------------

await f.step("11", "changing the time zone in settings changes the times shown (YYYY-MM-DD HH:mm:ss)", async () => {
  const times = async () => {
    await A.page.waitForFunction(() => document.querySelector("main time[datetime]"), { timeout: 20000 });
    return A.page.$$eval("main time[datetime]", (els) => els.map((el) => ({ iso: el.getAttribute("datetime"), text: el.innerText.trim() })));
  };
  await nav(A, "/account/sessions");
  const before = (await times()).filter((x) => /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/.test(x.text));
  if (!before.length) throw new Error("no full times on the devices page");
  for (const x of before) if (x.text !== fmtTime(x.iso, "Asia/Singapore")) throw new Error(`in the browser's zone ${x.iso} shows "${x.text}"`);
  await nav(A, "/account/settings");
  await A.page.click('button[aria-haspopup="dialog"][aria-label^="时区"]');
  await sheet(A);
  await A.page.type('[role=dialog] input[aria-label="搜索时区或城市"]', "America/New_York");
  await A.page.waitForFunction(() => [...document.querySelectorAll("[role=dialog] [role=option]")].some((o) => o.innerText.includes("America/New York")), { timeout: 5000 });
  const option = await A.page.waitForFunction(() => [...document.querySelectorAll("[role=dialog] [role=option]")].find((o) => o.innerText.includes("America/New York")));
  await option.asElement().click();
  await A.page.waitForFunction(() => JSON.parse(localStorage.getItem("exchange.settings") ?? "{}").state?.timeZone === "America/New_York", { timeout: 5000 });
  await nav(A, "/account/sessions");
  const after = (await times()).filter((x) => /^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}$/.test(x.text));
  for (const x of after) if (x.text !== fmtTime(x.iso, "America/New_York")) throw new Error(`in New York ${x.iso} shows "${x.text}"`);
  await A.page.evaluate(() => {
    const s = JSON.parse(localStorage.getItem("exchange.settings"));
    s.state.timeZone = "";
    localStorage.setItem("exchange.settings", JSON.stringify(s));
  });
});

// --- 12: numbers ----------------------------------------------------------------------------------

await f.step("12", "figures in tabular digits, amounts right-aligned in the trades at the pair's decimals, prices with thousands separators", async () => {
  const problems = [];
  await nav(A, "/markets");
  await A.page.waitForFunction(() => document.querySelectorAll(':is(ul, [role=list])[aria-label="行情"] > :is(li, [role=listitem])').length > 5, { timeout: 20000 });
  const btc = await A.page.evaluate(() => [...document.querySelectorAll(':is(ul, [role=list])[aria-label="行情"] > :is(li, [role=listitem])')].find((r) => /\bBTC\b/.test(r.innerText))?.innerText ?? "");
  if (!/\d{1,3}(,\d{3})+(\.\d+)?/.test(btc)) problems.push(`markets: BTC without thousands separators: "${btc.replace(/\s+/g, " ")}"`);
  const nums = await A.page.evaluate(() => getComputedStyle(document.documentElement).fontVariantNumeric);
  if (!nums.includes("tabular-nums")) problems.push(`the page's digits are ${nums}`);
  const pairs = await (await fetch(`${API}/v1/market/pairs`)).json();
  const pair = (pairs.pairs ?? pairs.items ?? pairs).find?.((p) => p.symbol === "BTC-USDT");
  await nav(A, "/trade/BTC-USDT");
  await clickTab(A, "成交");
  await A.page.waitForFunction(() => document.querySelectorAll("[role=tabpanel]:not([hidden]) button.grid").length > 2, { timeout: 20000 });
  const rows = "[role=tabpanel]:not([hidden]) button.grid";
  const prices = (await textAligned(A.page, `${rows} > span:nth-child(1)`)).slice(0, 5);
  const amounts = (await textAligned(A.page, `${rows} > span:nth-child(2)`)).slice(0, 5);
  for (const p of prices) {
    const decimals = p.text.includes(".") ? p.text.split(".")[1].length : 0;
    if (pair && decimals !== pair.price_decimals) problems.push(`trades: price "${p.text}" with ${decimals} decimals, the pair has ${pair.price_decimals}`);
  }
  for (const a of amounts) if (a.align !== "right" && a.align !== "full") problems.push(`trades: amount "${a.text}" aligned ${a.align}`);
  if (!amounts.length) problems.push("trades: no rows read");
  if (problems.length) throw new Error(problems.join("\n  "));
});

// --- M1: tap targets and the safe area ----------------------------------------------------------------

await f.step("M1", "every tap target is at least 44 px (its own box or its hit area); the tab bar keeps clear of the home indicator", async () => {
  const coarse = await A.page.evaluate(() => matchMedia("(pointer: coarse)").matches);
  if (!coarse) throw new Error("the emulated phone does not report a coarse pointer");
  const small = [];
  for (const p of ["/", "/markets", "/trade/BTC-USDT", "/assets", "/me"]) {
    await nav(A, p);
    const found = await A.page.evaluate(() => {
      const out = [];
      const targets = [...document.querySelectorAll("a[href], button, input:not([type=hidden]), [role=tab], [role=option], [role=switch], [role=radio]")];
      const on = (el, x, y) => {
        const at = document.elementFromPoint(x, y);
        return !!at && (at === el || el.contains(at));
      };
      for (const el of targets) {
        let r = el.getBoundingClientRect();
        if (r.width === 0 || r.height === 0 || r.bottom < 0 || r.top > innerHeight || el.closest("[aria-hidden=true], [inert]")) continue;
        if (r.width >= 44 && r.height >= 44) continue;
        // A target under a fixed bar (the order buttons, the tab bar) is
        // judged where it can be pressed: scrolled to the middle of the
        // screen. Still covered there, it is not a target on this screen.
        const y0 = scrollY;
        if (!on(el, r.left + r.width / 2, r.top + r.height / 2)) {
          el.scrollIntoView({ block: "center", inline: "nearest", behavior: "instant" });
          r = el.getBoundingClientRect();
        }
        const cx = r.left + r.width / 2;
        const cy = r.top + r.height / 2;
        if (on(el, cx, cy)) {
          // The hit area: a press 22 px from the centre (or at the edge of the screen) still lands on it.
          const hits = (x, y) => x < 0 || y < 0 || x >= innerWidth || y >= innerHeight || on(el, x, y);
          const wide = r.width >= 44 || (hits(cx - 21, cy) && hits(cx + 21, cy));
          const tall = r.height >= 44 || (hits(cx, cy - 21) && hits(cx, cy + 21));
          if (!wide || !tall) out.push(`${el.tagName.toLowerCase()}${el.getAttribute("aria-label") ? `[${el.getAttribute("aria-label")}]` : ""} "${el.innerText?.trim().slice(0, 16) ?? ""}" ${Math.round(r.width)}×${Math.round(r.height)}`);
        }
        if (scrollY !== y0) scrollTo({ top: y0, behavior: "instant" });
      }
      return out;
    });
    for (const x of found) small.push(`${p}: ${x}`);
  }
  const safe = await A.page.evaluate(() => {
    const bar = document.querySelector('nav[aria-label="首页"]');
    if (!bar) return "no tab bar";
    // Tailwind's utilities sit in @layer blocks (and media queries): look inside.
    const pads = (rules) => {
      for (const rule of rules) {
        if (rule.selectorText && rule.style?.cssText.includes("safe-area-inset-bottom") && bar.matches(rule.selectorText)) return true;
        if (rule.cssRules && pads(rule.cssRules)) return true;
      }
      return false;
    };
    for (const sheet of document.styleSheets) {
      let rules;
      try {
        rules = sheet.cssRules;
      } catch {
        continue;
      }
      if (pads(rules)) return "";
    }
    return "the tab bar has no padding for the safe area";
  });
  const problems = [...new Set(small)].slice(0, 30);
  if (safe) problems.push(safe);
  const root = await A.page.evaluate(() => getComputedStyle(document.documentElement).fontSize);
  if (problems.length) throw new Error(`tap targets under 44 px (root font size ${root}; Tailwind's size-11 is 2.75rem):\n  ${problems.join("\n  ")}`);
});

// --- M2: sheets close on a drag down --------------------------------------------------------------------

await f.step("M2", "the order sheet and the pair switcher close when dragged down by their handle", async () => {
  await nav(A, "/trade/BTC-USDT");
  for (const open of [() => A.clickButton("买入 BTC"), () => A.page.evaluate(() => [...document.querySelectorAll("header button")].find((b) => /BTC\s*\/\s*USDT/.test(b.innerText))?.click())]) {
    await open();
    await sheet(A);
    const h = await center(A, "[role=dialog][data-state=open] .cursor-grab");
    await drag(A, h.x, h.y, 0, 260);
    await A.page.waitForSelector("[role=dialog]", { hidden: true, timeout: 5000 });
  }
});

// --- M3: pull to refresh; swiping the terminal's tabs ----------------------------------------------------

await f.step("M3", "pulling the market list down refreshes it; swiping the terminal's panels switches its tabs", async () => {
  await nav(A, "/markets");
  await A.page.evaluate(() => window.scrollTo(0, 0));
  await A.frames(2);
  const refetched = A.page.waitForRequest((r) => r.url().includes("/v1/market/tickers"), { timeout: 15000 });
  const list = await center(A, ':is(ul, [role=list])[aria-label="行情"]');
  await A.page.touchscreen.touchStart(list.x, 200);
  for (let i = 1; i <= 20; i++) {
    await A.page.touchscreen.touchMove(list.x, 200 + i * 10);
    await A.frames(1);
  }
  await A.page.waitForFunction(() => [...document.querySelectorAll("[aria-live=polite]")].some((el) => el.innerText.includes("松开刷新") && Number(getComputedStyle(el).opacity) > 0), { timeout: 5000 });
  await A.page.touchscreen.touchEnd();
  await refetched;
  // The terminal: chart, book and trades side by side under one tab list.
  await nav(A, "/trade/BTC-USDT");
  await clickTab(A, "盘口");
  const panel = await center(A, "[role=tabpanel]:not([hidden])");
  await drag(A, panel.x + 120, panel.y, -240, 0, 8);
  await A.page.waitForFunction(() => document.querySelector("[role=tab][data-state=active]")?.textContent.trim().startsWith("成交"), { timeout: 5000 });
  await drag(A, panel.x - 120, panel.y, 240, 0, 8);
  await A.page.waitForFunction(() => document.querySelector("[role=tab][data-state=active]")?.textContent.trim().startsWith("盘口"), { timeout: 5000 });
});

// --- M4: installable, works offline ------------------------------------------------------------------------

await f.step("M4", "the site installs (manifest, service worker, no installability error) and shows its offline page offline", async () => {
  const P = await f.open({ name: "pwa", device: phone(390), persistent: true });
  try {
    // The worker's own requests carry the browser's desktop user agent, not
    // the emulated phone's: a phone that chose this site (site_pref=m), so
    // nginx does not send its /offline.html to the PC site.
    if (!APP.startsWith("http://localhost")) await P.page.setCookie({ name: "site_pref", value: "m", domain: siteCookieDomain(APP), path: "/" });
    await P.go("/");
    const manifest = await P.page.$eval('link[rel="manifest"]', (l) => l.href);
    const m = await (await fetch(manifest)).json();
    const sizes = (m.icons ?? []).map((i) => i.sizes);
    if (!sizes.includes("192x192") || !sizes.includes("512x512") || m.display !== "standalone") throw new Error(`manifest ${JSON.stringify(m)}`);
    // The site registers its worker once loaded; it is active when installed.
    const active = await P.page
      .waitForFunction(async () => (await navigator.serviceWorker.getRegistration())?.active?.scriptURL ?? null, { timeout: 20000, polling: 500 })
      .catch(() => null);
    if (!active) {
      const why = await P.page.evaluate(async () => {
        try {
          await navigator.serviceWorker.register("/sw.js");
          return "it registers when asked again";
        } catch (e) {
          return String(e);
        }
      });
      throw new Error(`no active service worker 20 s after the page loaded (${why})`);
    }
    const scope = await P.page.evaluate(async () => (await navigator.serviceWorker.getRegistration())?.scope);
    if (new URL(scope).pathname !== "/") throw new Error(`service worker scope ${scope}`);
    const cdp = await P.page.createCDPSession();
    const { installabilityErrors } = await cdp.send("Page.getInstallabilityErrors");
    if (installabilityErrors.length) throw new Error(`not installable: ${JSON.stringify(installabilityErrors)}`);
    // Offline: the page and its service worker.
    await P.page.reload({ waitUntil: "domcontentloaded" });
    await P.settled();
    await P.page.waitForFunction(() => !!navigator.serviceWorker.controller, { timeout: 10000 });
    const worker = await P.page.browser().waitForTarget((t) => t.type() === "service_worker" && t.url().startsWith(APP), { timeout: 10000 });
    const sw = await worker.createCDPSession();
    await sw.send("Network.enable");
    await sw.send("Network.emulateNetworkConditions", { offline: true, latency: 0, downloadThroughput: -1, uploadThroughput: -1 });
    await P.page.setOfflineMode(true);
    await P.page.goto(APP + "/help?offline=" + Date.now(), { waitUntil: "domcontentloaded" }).catch(() => {});
    await P.page.waitForFunction(() => document.querySelector("h1")?.innerText.includes("网络不可用"), { timeout: 10000 });
    await P.page.setOfflineMode(false);
    await sw.send("Network.emulateNetworkConditions", { offline: false, latency: 0, downloadThroughput: -1, uploadThroughput: -1 });
  } finally {
    await P.close();
  }
}, { allowErrors: [/ERR_INTERNET_DISCONNECTED|net::ERR|Failed to fetch|NetworkError/i] });

// --- §8-1 and M5: the walk on an iPhone and an Android phone ---------------------------------------------------

for (const [name, device] of [["iPhone", PHONE_IOS], ["Android", PHONE_ANDROID]]) {
  await f.step(
    "§8-1",
    `${name}: sign up (the code field: one-time-code, autofocus), markets, the order sheet, assets, a deposit address with its QR code, the withdrawal step-up, settings`,
    async () => {
      const W = await f.open({ name: name.toLowerCase(), device });
      try {
        const email = `e2e-mwalk-${name.toLowerCase()}-${run}@example.com`;
        await W.go("/register");
        await W.typeInto('input[autocomplete="email"]', email);
        await W.typeInto('input[autocomplete="new-password"]', `e2e mobile walk ${run}`);
        await W.page.waitForSelector('button[role="checkbox"]', { visible: true });
        await W.page.click('button[role="checkbox"]');
        await W.clickButton("继续");
        await W.waitText("验证你的邮箱");
        const before = await inboxCount(API, email);
        await W.clickButton("发送验证码");
        await W.page.waitForFunction(() => document.activeElement?.getAttribute("autocomplete") === "one-time-code", { timeout: 15000 });
        const otp = await W.page.$eval('input[autocomplete="one-time-code"]', (el) => ({ mode: el.inputMode, max: el.maxLength }));
        if (otp.mode !== "numeric" || otp.max !== 6) throw new Error(`the code field ${JSON.stringify(otp)}`);
        await W.page.keyboard.type(await code(API, email, before));
        await W.clickButton("创建账户");
        await W.waitPath("/assets", 30000);
        await W.page.waitForSelector('[data-testid="assets-total"]', { visible: true, timeout: 30000 });
        await nav(W, "/markets");
        await W.typeInto('input[placeholder="搜索币种名称或代码"]', "BTC");
        await W.page.waitForFunction(() => [...document.querySelectorAll(':is(ul, [role=list])[aria-label="行情"] > :is(li, [role=listitem])')].some((r) => r.innerText.includes("BTC")), { timeout: 10000 });
        await nav(W, "/trade/BTC-USDT");
        await W.clickButton("买入 BTC");
        await sheet(W);
        await W.page.waitForSelector('[role=dialog] input[aria-label="价格"]', { visible: true });
        await W.page.waitForSelector('[role=dialog] input[aria-label="数量"]', { visible: true });
        await W.page.keyboard.press("Escape");
        await W.page.waitForSelector("[role=dialog]", { hidden: true });
        await nav(W, "/assets/deposit?asset=ETH&network=ETH-SEPOLIA");
        await W.page.waitForFunction(() => /^0x[0-9a-fA-F]{40}$/.test(document.querySelector('[data-testid="deposit-address"]')?.innerText.trim() ?? ""), { timeout: 20000 });
        await W.page.waitForSelector('div[role="img"][aria-label$="充值地址二维码"] svg', { visible: true, timeout: 10000 });
        // The withdrawal's step-up: a new address to save asks for it first; closed, nothing is saved.
        await newAddress(W);
        await W.typeInto('[role=dialog] input[placeholder^="粘贴或输入"]', "0x52908400098527886e0f7030069857d2e4169ee7");
        await W.page.waitForFunction(() => [...document.querySelectorAll("[role=dialog] button")].some((b) => b.innerText.includes("保存到地址簿") && !b.disabled), { timeout: 20000 });
        await W.clickButton("保存到地址簿", "[role=dialog]");
        await W.page.waitForFunction(() => [...document.querySelectorAll("[role=dialog] h2")].some((h) => h.innerText.includes("安全验证")), { timeout: 15000 });
        // It asks for a code (sent on request; the sign-up's code field above
        // showed its attributes, and the address just had a code, so a second
        // one now would only meet the resend cooldown). Closed, nothing is saved.
        await W.page.waitForFunction(() => [...document.querySelectorAll("[role=dialog] button")].some((b) => b.innerText.trim() === "发送验证码"), { timeout: 10000 });
        await W.page.keyboard.press("Escape");
        await nav(W, "/account/settings");
        await W.clickButton("English");
        await W.waitText("Time zone", 10000);
        await W.clickButton("简体中文");
        await W.waitText("时区", 10000);
      } finally {
        await W.close();
      }
    },
  );
}

// --- P8 from this side: the PC site on a phone that chose it ------------------------------------------------------

const P8 = "切换到电脑版 on a phone keeps it on the PC site";
if (APP.startsWith("http://localhost")) f.skip("P8", P8, "needs the deployed sites' nginx");
else await f.step("P8", P8, async () => {
  const S = await f.open({ name: "switch", device: phone(390) });
  try {
    await S.go("/me");
    await S.clickButton("切换到电脑版");
    await S.page.waitForFunction((pc) => location.origin === pc, { timeout: 15000 }, PC_APP);
    await S.page.goto(PC_APP + "/markets", { waitUntil: "domcontentloaded" });
    if (new URL(S.page.url()).origin !== PC_APP) throw new Error(`sent back to ${S.page.url()}`);
    const pref = (await S.context.cookies(PC_APP)).find((c) => c.name === "site_pref");
    if (pref?.value !== "pc") throw new Error(`site_pref ${JSON.stringify(pref)}`);
  } finally {
    await S.close();
  }
});

await f.done();
