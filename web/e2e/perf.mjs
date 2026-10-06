// Measures the budgets of design §12.1 that Lighthouse does not, on the
// deployed sites with live market data (BTC-USDT streams Binance's book
// through HOUSE, about ten depth messages a second):
//
//   node perf.mjs            terminal pages of both sites, the phone's market list, the 1000-row table, margin trading
//   node perf.mjs table      the 1000-row table alone
//   node perf.mjs margin     margin trading alone (both sites)
//   node perf.mjs memory     the PC terminal's heap over 30 minutes (MINUTES)
//
// For each terminal: main-thread long tasks (> 50 ms) per minute over a
// minute of streaming; the book's redraw, from the moment its throttle
// tells it to (it redraws at most every 250 ms, BOOK_EVERY in the apps,
// since the user asked for a calm book on 2026-10-01) to the frame that
// shows it; every depth message to the frame of the first redraw after it
// (the throttle's 250 ms plus the redraw's budget); every trade message to
// the frame that shows it on the trade tape (not throttled); switching
// pairs (the new pair's snapshot to its book on screen); leaving and
// coming back (no new WebSocket, no new snapshot, content within 200
// ms); p50, p95 and the longest of each. The phone runs with
// the CPU slowed four times, as a mid-range phone. The 1000-row table is
// the design system's virtual DataTable, scrolled for three seconds.
// Margin trading (margin design 2026-10-06 §7, B108) is measured as a new
// account with 20 USDT in its cross account (signed up over the API, the
// human check passed with CAPTCHA_BYPASS_TOKEN, from .env when not
// exported): see margin().
// Prints one line per measurement; exits non-zero when a budget is missed
// (BUDGET=warn only reports).
import { existsSync, readFileSync } from "node:fs";
import puppeteer from "puppeteer-core";
import { api, register } from "./flows-lib.mjs";

const PC = process.env.PC_BASE ?? "https://astras.vip";
const M = process.env.M_BASE ?? "https://m.astras.vip";
const CHROME =
  process.env.CHROME ??
  ["/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "/usr/bin/google-chrome", "/usr/bin/chromium"].find((p) => existsSync(p));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const misses = [];
// The books redraw at most this often (BOOK_EVERY in the PC book panel and
// the mobile terminals).
const BOOK_EVERY = 250;

function report(what, value, budget, ok) {
  console.log(`${ok ? "ok  " : "MISS"} ${what}: ${value} (budget ${budget})`);
  if (!ok) misses.push(what);
}

const pct = (list, p) => {
  if (list.length === 0) return NaN;
  const s = [...list].sort((a, b) => a - b);
  return s[Math.min(s.length - 1, Math.floor((p / 100) * s.length))];
};
const ms = (v) => `${Math.round(v)} ms`;
// stats prints a list's p50, p95 and longest, or says it has none.
const stats = (list) => (list.length ? `${ms(pct(list, 50))} / ${ms(pct(list, 95))} / ${ms(Math.max(...list))}` : "no samples");

// instrument records, in the page, every WebSocket and message (channel,
// type, time), the long tasks, and the order book's DOM changes.
function instrument() {
  window.__perf = { sockets: 0, messages: [], longTasks: [], mutations: [], notices: [], tape: [] };
  // core's useOrderBook notes here when its throttle tells a book to redraw;
  // each notice is kept with the next frame, the one its redraw shows in
  // (React flushes the store's update in a microtask before it).
  window.__perfBookNotify = [];
  window.__perfBookNotify.push = function (at) {
    requestAnimationFrame((frame) => window.__perf.notices.push({ at, frame }));
    return Array.prototype.push.call(this, at);
  };
  const Native = window.WebSocket;
  window.WebSocket = class extends Native {
    constructor(...args) {
      super(...args);
      window.__perf.sockets++;
      this.addEventListener("message", (e) => {
        const at = performance.now();
        try {
          const m = JSON.parse(e.data);
          if (m.channel) window.__perf.messages.push({ at, channel: m.channel, type: m.type ?? m.data?.type ?? "" });
        } catch {
          // not JSON
        }
      });
    }
  };
  new PerformanceObserver((list) => {
    for (const e of list.getEntries()) window.__perf.longTasks.push({ at: e.startTime, duration: e.duration });
  }).observe({ type: "longtask", buffered: true });
  new PerformanceObserver((list) => {
    const last = list.getEntries().at(-1);
    if (last) window.__perf.lcp = last.startTime;
  }).observe({ type: "largest-contentful-paint", buffered: true });
  // The book's two sides only, once shown (the phone mounts its panels
  // hidden): the row between them shows the last trade and the mark price,
  // which are not the book's and redraw on their own.
  const watch = () => {
    const sides = [...document.querySelectorAll('[role="group"][aria-label="买盘"], [role="group"][aria-label="卖盘"]')].filter((s) => s.checkVisibility());
    if (sides.length === 0) return setTimeout(watch, 200);
    const observer = new MutationObserver(() => {
      const at = performance.now();
      requestAnimationFrame((frame) => window.__perf.mutations.push({ at, frame }));
    });
    for (const side of sides) observer.observe(side, { subtree: true, childList: true, characterData: true });
  };
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", watch);
  else watch();
}

async function open(browser, { viewport, userAgent, cpu }) {
  const context = await browser.createBrowserContext();
  const page = await context.newPage();
  await page.setViewport(viewport);
  if (userAgent) await page.setUserAgent(userAgent);
  if (cpu) await page.emulateCPUThrottling(cpu);
  await page.evaluateOnNewDocument(() => {
    if (!localStorage.getItem("exchange.settings")) {
      localStorage.setItem("exchange.settings", JSON.stringify({ state: { locale: "zh-CN" }, version: 1 }));
    }
  });
  await page.evaluateOnNewDocument(instrument);
  return page;
}

// navigate moves the single-page app to path without a reload.
const navigate = (page, path) =>
  page.evaluate((p) => {
    history.pushState({}, "", p);
    dispatchEvent(new PopStateEvent("popstate"));
  }, path);

// The terminals' tabs: the order book's, the trade tape's (both sites).
const bookTabLabel = "盘口";

async function terminal(browser, { site, base, device, budgets, bookTab, tapeTab }) {
  const page = await open(browser, device);
  // tab presses a tab by its label, as a pointer does (the PC site's tabs
  // switch on the pointer going down; an element's click() does not). A
  // tab not found is a miss, and the measurements behind it are skipped,
  // not the other sites'.
  const tab = async (label) => {
    for (const h of await page.$$("button, [role=tab]")) {
      if ((await h.evaluate((b) => b.textContent.trim())) === label && (await h.isVisible())) {
        await h.click();
        return true;
      }
    }
    report(`${site} the ${label} tab`, "not found", "found", false);
    return false;
  };
  const done = () => page.browserContext().close();
  await page.goto(`${base}/trade/BTC-USDT`, { waitUntil: "networkidle2", timeout: 60000 });
  if (bookTab && !(await tab(bookTabLabel))) return done();
  await page.waitForSelector("[data-book-row]", { visible: true, timeout: 30000 });
  await sleep(5000); // past the load

  // A minute of streaming.
  const from = await page.evaluate(() => performance.now());
  await sleep(60000);
  const window60 = await page.evaluate(
    (t0, every) => {
      const p = window.__perf;
      return {
        longTasks: p.longTasks.filter((l) => l.at >= t0).map((l) => l.duration),
        depth: p.messages.filter((m) => m.at >= t0 && m.channel === "depth:BTC-USDT").map((m) => m.at),
        // From a throttle window before the minute, so that a redraw early
        // in it pairs with its notice just before it.
        mutations: p.mutations.filter((m) => m.at >= t0 - every),
        notices: p.notices.filter((n) => n.at >= t0 - every),
      };
    },
    from,
    BOOK_EVERY,
  );
  const perMinute = window60.longTasks.length;
  report(
    `${site} long tasks in a minute of streaming (${window60.depth.length} depth messages)`,
    `${perMinute}${perMinute ? `, longest ${ms(Math.max(...window60.longTasks))}` : ""}`,
    `≤ ${budgets.longTasks}`,
    perMinute <= budgets.longTasks,
  );
  // Each notice of the throttle to the frame that shows the redraw it set
  // off: the book's first change after it not taken by an earlier notice,
  // within the throttle's window (notices are at least that far apart). A
  // notice with no change in that window changed no visible level, or
  // redrew later than the window, and its change then has no notice; a
  // change with no notice of its own fails the run: the throttle was
  // bypassed (as before b178292, when a render for another reason brought
  // newer levels in), or a redraw took the whole window. Only the notices
  // and changes in the minute count.
  const redraw = [];
  const taken = new Set();
  for (const n of window60.notices) {
    const i = window60.mutations.findIndex((x, k) => !taken.has(k) && x.at >= n.at && x.at - n.at < BOOK_EVERY);
    if (i < 0) continue;
    taken.add(i);
    if (n.at >= from) redraw.push(window60.mutations[i].frame - n.at);
  }
  const notices = window60.notices.filter((n) => n.at >= from).length;
  const changes = window60.mutations.filter((x) => x.at >= from).length;
  const bypassed = window60.mutations.filter((x, k) => x.at >= from && !taken.has(k)).length;
  report(
    `${site} book redraw: the throttle's notice to the frame that shows it (p50 / p95 / max over ${redraw.length} notices with a change; ${notices - redraw.length} without a change within ${BOOK_EVERY} ms; ${changes} changes of the book, ${bypassed} without a notice)`,
    stats(redraw),
    `p95 ≤ ${budgets.toPixel} ms, every change on a notice`,
    redraw.length > 0 && pct(redraw, 95) <= budgets.toPixel && bypassed === 0,
  );
  // Each depth message to the frame of the first notice after it (the
  // book then shows a state at least that new); messages after the last
  // notice are left out. The throttle's window plus the redraw's budget.
  const perMessage = [];
  for (const at of window60.depth) {
    const n = window60.notices.find((x) => x.at >= at);
    if (n) perMessage.push(n.frame - at);
  }
  report(
    `${site} depth message to the frame that shows it (p50 / p95 / max over ${perMessage.length} of ${window60.depth.length} messages)`,
    stats(perMessage),
    `p95 ≤ ${BOOK_EVERY} + ${budgets.toPixel} ms`,
    perMessage.length > 0 && pct(perMessage, 95) <= BOOK_EVERY + budgets.toPixel,
  );

  // Trades (not throttled): each trade message to the first change of the
  // trade tape after it, over 30 seconds on the tape's own tab; then back
  // to the book.
  if (await tab(tapeTab)) {
    await page.waitForSelector('section[aria-label="最新成交"]', { visible: true, timeout: 10000 });
    const fromTape = await page.evaluate(() => {
      const tapeShown = [...document.querySelectorAll('section[aria-label="最新成交"]')].find((el) => el.checkVisibility());
      new MutationObserver(() => {
        const at = performance.now();
        requestAnimationFrame((frame) => window.__perf.tape.push({ at, frame }));
      }).observe(tapeShown, { subtree: true, childList: true, characterData: true });
      return performance.now();
    });
    await sleep(30000);
    const tape = await page.evaluate(
      (t0) => ({
        trades: window.__perf.messages.filter((m) => m.at >= t0 && m.channel === "trades:BTC-USDT").map((m) => m.at),
        changes: window.__perf.tape.filter((c) => c.at >= t0),
      }),
      fromTape,
    );
    const perTrade = [];
    for (const at of tape.trades) {
      const c = tape.changes.find((x) => x.at >= at);
      if (c) perTrade.push(c.frame - at);
    }
    report(
      `${site} trade message to the frame that shows it (p50 / p95 / max over ${perTrade.length} of ${tape.trades.length} trades)`,
      stats(perTrade),
      `p95 ≤ ${budgets.toPixel} ms`,
      perTrade.length > 0 && pct(perTrade, 95) <= budgets.toPixel,
    );
    if (!(await tab(bookTabLabel))) return done();
    await page.waitForSelector("[data-book-row]", { visible: true, timeout: 10000 });
  }

  // Switching to ETH-USDT (the address changed as a link does, by
  // pushState): its snapshot to its book on screen.
  const t0 = await page.evaluate(() => performance.now());
  await navigate(page, "/trade/ETH-USDT");
  await page.waitForFunction(
    () => {
      const row = document.querySelector('[role="group"][aria-label="买盘"] [data-book-row]');
      const price = Number(row?.textContent?.match(/[\d,]+\.\d+/)?.[0]?.replace(/,/g, ""));
      return price > 0 && price < 20000; // ETH, not BTC
    },
    { timeout: 30000, polling: "raf" },
  );
  const shown = await page.evaluate(() => performance.now());
  const snapshot = await page.evaluate((t) => window.__perf.messages.find((m) => m.at >= t && m.channel === "depth:ETH-USDT")?.at, t0);
  report(
    `${site} switch to ETH-USDT: snapshot to book (from the switch ${ms(shown - t0)})`,
    snapshot ? ms(shown - snapshot) : "no snapshot seen",
    `≤ ${budgets.switch} ms`,
    snapshot !== undefined && shown - snapshot <= budgets.switch,
  );

  // Away to the markets and back within the WebSocket's 15-second grace:
  // the book is on screen again (the client kept its subscription).
  const sockets = await page.evaluate(() => window.__perf.sockets);
  await navigate(page, "/markets");
  await page.waitForFunction(() => !document.querySelector("[data-book-row]"), { timeout: 10000 });
  await sleep(3000);
  const t1 = await page.evaluate(() => performance.now());
  const content = await page.evaluate(
    (path) =>
      new Promise((resolve) => {
        const t = performance.now();
        history.pushState({}, "", path);
        dispatchEvent(new PopStateEvent("popstate"));
        const check = () => {
          if (document.querySelector("[data-book-row]")) resolve(performance.now() - t);
          else if (performance.now() - t > 10000) resolve(Infinity);
          else requestAnimationFrame(check);
        };
        requestAnimationFrame(check);
      }),
    "/trade/ETH-USDT",
  );
  await sleep(1500);
  const back = await page.evaluate(
    (t, n) => ({
      sockets: window.__perf.sockets - n,
      snapshots: window.__perf.messages.filter((m) => m.at >= t && m.channel === "depth:ETH-USDT" && m.type === "snapshot").length,
    }),
    t1,
    sockets,
  );
  report(
    `${site} back to the terminal: book on screen, new WebSockets, new snapshots`,
    `${ms(content)}, ${back.sockets}, ${back.snapshots}`,
    "≤ 200 ms, 0, 0",
    content <= 200 && back.sockets === 0 && back.snapshots === 0,
  );
  await done();
}

async function table(browser) {
  const page = await open(browser, { viewport: { width: 1280, height: 900 } });
  await page.goto(`${PC}/storybook/iframe.html?id=data-datatable--virtual-1000&viewMode=story`, { waitUntil: "networkidle2", timeout: 60000 });
  // Storybook shows a placeholder table while it prepares the story.
  await page.waitForSelector('table[aria-label="Orders"] tbody tr', { timeout: 60000 });
  await sleep(1000);
  const r = await page.evaluate(async () => {
    let el = document.querySelector('table[aria-label="Orders"]');
    while (el && !(el.scrollHeight > el.clientHeight + 100 && getComputedStyle(el).overflowY !== "visible")) el = el.parentElement;
    if (!el) return null;
    const frames = [];
    const end = performance.now() + 3000;
    await new Promise((done) => {
      const step = (t) => {
        frames.push(t);
        el.scrollTop += 60;
        if (el.scrollTop + el.clientHeight >= el.scrollHeight) el.scrollTop = 0;
        if (t < end) requestAnimationFrame(step);
        else done();
      };
      requestAnimationFrame(step);
    });
    const gaps = frames.slice(1).map((t, i) => t - frames[i]);
    return { fps: (frames.length - 1) / ((frames.at(-1) - frames[0]) / 1000), slow: gaps.filter((g) => g > 25).length, rows: el.querySelectorAll("tbody tr").length };
  });
  if (!r) return report("1000-row table", "no scrolling element", "60 fps", false);
  report(`1000-row table scrolled 3 s (${r.rows} rows in the DOM, ${r.slow} frames over 25 ms)`, `${r.fps.toFixed(1)} fps`, "≥ 55 fps", r.fps >= 55);
  await page.browserContext().close();
}

// markets scrolls the phone's market list (50 rows and more, windowed)
// for three seconds with the CPU slowed four times.
async function markets(browser) {
  const page = await open(browser, {
    viewport: { width: 390, height: 844, isMobile: true, hasTouch: true, deviceScaleFactor: 3 },
    userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1",
    cpu: 4,
  });
  await page.goto(`${M}/markets`, { waitUntil: "networkidle2", timeout: 60000 });
  await page.waitForSelector(':is(ul, [role=list])[aria-label="行情"] > :is(li, [role=listitem])', { timeout: 30000 });
  await sleep(3000);
  const r = await page.evaluate(async () => {
    const frames = [];
    const end = performance.now() + 3000;
    let down = true;
    await new Promise((done) => {
      const step = (t) => {
        frames.push(t);
        window.scrollBy(0, down ? 40 : -40);
        if (window.scrollY + innerHeight >= document.documentElement.scrollHeight - 2) down = false;
        if (window.scrollY <= 0) down = true;
        if (t < end) requestAnimationFrame(step);
        else done();
      };
      requestAnimationFrame(step);
    });
    const gaps = frames.slice(1).map((t, i) => t - frames[i]);
    return { fps: (frames.length - 1) / ((frames.at(-1) - frames[0]) / 1000), slow: gaps.filter((g) => g > 25).length };
  });
  report(`phone market list scrolled 3 s at a quarter of the CPU (${r.slow} frames over 25 ms)`, `${r.fps.toFixed(1)} fps`, "≥ 55 fps", r.fps >= 55);
  await page.browserContext().close();
}

// --- margin trading (B108) ----------------------------------------------

function bypassToken() {
  if (process.env.CAPTCHA_BYPASS_TOKEN) return process.env.CAPTCHA_BYPASS_TOKEN;
  const env = new URL("../../.env", import.meta.url);
  return existsSync(env) ? readFileSync(env, "utf8").match(/^CAPTCHA_BYPASS_TOKEN="?([^"\n]*)"?$/m)?.[1] : undefined;
}

// clickTo clicks, in the page, the visible button labelled label within
// scope, and measures from the click to the first frame on which selector
// is visible (-1 without such a button, Infinity after 15 seconds).
const clickTo = (page, label, scope, selector) =>
  page.evaluate(
    (label, scope, selector) =>
      new Promise((resolve) => {
        const button = [...document.querySelectorAll(`${scope} button`)].find((b) => b.textContent.trim() === label && b.checkVisibility());
        if (!button) return resolve(-1);
        const t = performance.now();
        button.click();
        const check = () => {
          if (document.querySelector(selector)?.checkVisibility()) resolve(performance.now() - t);
          else if (performance.now() - t > 15000) resolve(Infinity);
          else requestAnimationFrame(check);
        };
        requestAnimationFrame(check);
      }),
    label,
    scope,
    selector,
  );

// routeTo moves the single-page app to path, as a link does, and measures
// to the first frame on which selector is visible (Infinity after 15 s).
const routeTo = (page, path, selector) =>
  page.evaluate(
    (path, selector) =>
      new Promise((resolve) => {
        const t = performance.now();
        history.pushState({}, "", path);
        dispatchEvent(new PopStateEvent("popstate"));
        const check = () => {
          if (document.querySelector(selector)?.checkVisibility()) resolve(performance.now() - t);
          else if (performance.now() - t > 15000) resolve(Infinity);
          else requestAnimationFrame(check);
        };
        requestAnimationFrame(check);
      }),
    path,
    selector,
  );

/**
 * margin measures margin trading on a site, as a new account with 20 USDT
 * in its cross account, signed in through the form: the margin accounts
 * page loaded afresh (its LCP, and the JavaScript it fetched, compressed,
 * against the first-screen budget); reached again from the assets
 * overview (the route budget: content within 200 ms); on the terminal,
 * the order form switched to the cross account (its gauge on screen, the
 * same budget), the borrow dialog opened the first time (a lazy chunk; no
 * budget, reported), and a minute of streaming in margin mode (the
 * terminal's long-task budget). Skipped when margin trading is not open
 * to new accounts.
 */
async function margin(browser, { site, base, device, budgets, sheet }) {
  const bypass = bypassToken();
  if (!bypass) return console.log(`skip ${site} margin: no CAPTCHA_BYPASS_TOKEN`);
  const password = `e2e perf margin ${Date.now()}`;
  const user = await register(base, bypass, `e2e-perf-${site.toLowerCase()}-${Date.now()}@example.com`, password);
  const auth = { Authorization: `Bearer ${user.accessToken}` };
  const eligible = await api(base, "GET", "/v1/user/eligibility?feature=MARGIN_TRADE", undefined, auth);
  if (eligible.body?.allowed !== true) return console.log(`skip ${site} margin: not open to new accounts (${eligible.body?.reason_code ?? eligible.status})`);
  // The welcome funds land a moment after the sign-up. Idempotency keys
  // take letters, digits, dashes and underscores.
  const key = `perf-${site}-${Date.now()}`;
  for (let i = 0; ; i++) {
    const r = await api(base, "POST", "/v1/margin/transfer", { direction: "IN", account: "MARGIN_CROSS", asset: "USDT", amount: "20" }, {
      ...auth,
      "Idempotency-Key": `${key}-in-${i}`,
    });
    if (r.status === 200) break;
    if (i >= 20) return report(`${site} margin: 20 USDT into the cross account`, `${r.status} ${r.body?.code}`, "200", false);
    await sleep(2000);
  }

  // Whatever happens below, the page closes and the 20 USDT go back to spot
  // (nothing is borrowed).
  const page = await open(browser, device);
  try {
    await page.evaluateOnNewDocument((token) => {
      window.__E2E_CAPTCHA_TOKEN__ = token;
    }, bypass);
    await marginPages(page, { site, base, user, password, budgets, sheet });
  } finally {
    await page.browserContext().close().catch(() => {});
    await api(base, "POST", "/v1/margin/transfer", { direction: "OUT", account: "MARGIN_CROSS", asset: "USDT", amount: "20" }, {
      ...auth,
      "Idempotency-Key": `${key}-out`,
    });
  }
}

/** marginPages takes the measurements of margin() on a page of a fresh context. */
async function marginPages(page, { site, base, user, password, budgets, sheet }) {
  await page.goto(`${base}/login?next=%2Fassets`, { waitUntil: "networkidle2", timeout: 60000 });
  await page.type('input[autocomplete="username"]', user.email);
  await page.type('input[autocomplete="current-password"]', password);
  await page.keyboard.press("Enter");
  await page.waitForFunction(() => location.pathname === "/assets", { timeout: 30000 });

  // A page loaded afresh: its LCP, and the JavaScript fetched (compressed,
  // as served) by the frame that first showed selector, its first screen.
  // One poller, registered once, notes when each of these first shows.
  await page.evaluateOnNewDocument((selectors) => {
    window.__perfShown = {};
    const check = () => {
      for (const sel of selectors) if (!(sel in window.__perfShown) && document.querySelector(sel)) window.__perfShown[sel] = performance.now();
      if (selectors.some((sel) => !(sel in window.__perfShown))) requestAnimationFrame(check);
    };
    requestAnimationFrame(check);
  }, ['[data-testid="assets-total"]', '[data-testid="margin-account-MARGIN_CROSS"]']);
  const fresh = async (path, selector) => {
    await page.goto(`${base}${path}`, { waitUntil: "networkidle2", timeout: 60000 });
    await page.waitForSelector(selector, { visible: true, timeout: 30000 });
    await sleep(1500);
    return page.evaluate((sel) => {
      const shown = window.__perfShown?.[sel] ?? Infinity;
      const js = performance.getEntriesByType("resource").filter((r) => /\.js($|\?)/.test(new URL(r.name).pathname) && r.responseEnd <= shown);
      return {
        lcp: window.__perf.lcp ?? null,
        kb: js.reduce((s, r) => s + (r.encodedBodySize || 0), 0) / 1024,
        files: js.length,
        largest: js
          .sort((a, b) => b.encodedBodySize - a.encodedBodySize)
          .slice(0, 12)
          .map((r) => `${new URL(r.name).pathname.split("/").pop()} ${(r.encodedBodySize / 1024).toFixed(1)}`),
      };
    }, selector);
  };
  // The assets overview first, for comparison: the same shell and session.
  const overview = await fresh("/assets", '[data-testid="assets-total"]');
  const account = '[data-testid="margin-account-MARGIN_CROSS"]';
  const load = await fresh("/assets/margin", account);
  if (process.env.PERF_DEBUG) {
    console.log(`     ${site} overview's largest first-screen files (KB): ${overview.largest.join(", ")}`);
    console.log(`     ${site} margin page's largest first-screen files (KB): ${load.largest.join(", ")}`);
  }
  report(`${site} margin page loaded afresh: LCP`, load.lcp === null ? "none" : ms(load.lcp), `≤ ${budgets.lcp} ms`, load.lcp !== null && load.lcp <= budgets.lcp);
  report(
    `${site} margin page loaded afresh: JavaScript by its first screen (${load.files} files, compressed as served; the assets overview's ${overview.kb.toFixed(0)} KB in ${overview.files})`,
    `${load.kb.toFixed(0)} KB`,
    `≤ ${budgets.js} KB`,
    load.kb <= budgets.js,
  );

  // Back from the overview by a link: the cached accounts at once.
  await routeTo(page, "/assets", '[data-testid="assets-total"]');
  await sleep(1000);
  const route = await routeTo(page, "/assets/margin", account);
  report(`${site} assets overview to the margin page: accounts on screen`, ms(route), "≤ 200 ms", route <= 200);

  // The terminal: the order form on the cross account.
  await routeTo(page, "/trade/BTC-USDT", "[data-book-row]");
  await sleep(3000);
  let bar = '[data-testid="margin-bar"]';
  if (sheet) {
    const opened = await clickTo(page, "买入 BTC", "", '[role=dialog] [data-testid="margin-bar"]');
    if (opened < 0 || opened === Infinity) {
      report(`${site} the order sheet`, opened < 0 ? "no 买入 BTC button" : "no margin bar", "found", false);
      return;
    }
    bar = `[role=dialog] ${bar}`;
    await sleep(800); // the sheet slides in
  }
  const level = `${bar} [data-testid="margin-level"]`;
  const cross = await clickTo(page, "全仓", bar, level);
  report(`${site} order form switched to the cross account: its gauge on screen`, cross < 0 ? "no 全仓 button" : ms(cross), "≤ 200 ms", cross >= 0 && cross <= 200);
  // The dialog's chunk loads while the terminal is idle on a margin
  // account: opened as a user would, a moment after choosing the account.
  await sleep(2500);
  const borrow = await clickTo(page, "借币", bar, 'form[data-testid="margin-borrow-form"]');
  report(`${site} borrow dialog first opened, 2.5 s after the cross account was chosen: its form on screen`, borrow < 0 ? "no 借币 button" : ms(borrow), "— (reported)", borrow >= 0 && borrow !== Infinity);
  await page.keyboard.press("Escape");
  await page.waitForFunction(() => !document.querySelector('form[data-testid="margin-borrow-form"]'), { timeout: 10000 });
  await sleep(1000);

  // A minute of streaming in margin mode.
  const from = await page.evaluate(() => performance.now());
  await sleep(60000);
  const tasks = await page.evaluate((t0) => window.__perf.longTasks.filter((l) => l.at >= t0).map((l) => l.duration), from);
  report(
    `${site} long tasks in a minute of streaming on the cross account${sheet ? " (its order sheet open)" : ""}`,
    `${tasks.length}${tasks.length ? `, longest ${ms(Math.max(...tasks))}` : ""}`,
    `≤ ${budgets.longTasks}`,
    tasks.length <= budgets.longTasks,
  );
}

const PC_DEVICE = { viewport: { width: 1440, height: 900 } };
const PHONE_DEVICE = {
  viewport: { width: 390, height: 844, isMobile: true, hasTouch: true, deviceScaleFactor: 3 },
  userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1",
  cpu: 4,
};

async function margins(browser) {
  await margin(browser, { site: "PC", base: PC, device: PC_DEVICE, budgets: { lcp: 2000, js: 250, longTasks: 0 } });
  await margin(browser, { site: "phone", base: M, device: PHONE_DEVICE, budgets: { lcp: 2500, js: 200, longTasks: 2 }, sheet: true });
}

async function memory(browser) {
  const minutes = Number(process.env.MINUTES ?? 30);
  const page = await open(browser, { viewport: { width: 1440, height: 900 } });
  const cdp = await page.createCDPSession();
  await cdp.send("Performance.enable");
  const heap = async () => {
    await cdp.send("HeapProfiler.collectGarbage");
    const { metrics } = await cdp.send("Performance.getMetrics");
    return metrics.find((m) => m.name === "JSHeapUsedSize").value / 1048576;
  };
  await page.goto(`${PC}/trade/BTC-USDT`, { waitUntil: "networkidle2", timeout: 60000 });
  await page.waitForSelector("[data-book-row]", { timeout: 30000 });
  await sleep(60000);
  const start = await heap();
  for (let i = 1; i <= minutes; i++) {
    await sleep(60000);
    if (i % 5 === 0) console.log(`     ${i} min: ${(await heap()).toFixed(1)} MB`);
  }
  const end = await heap();
  report(`PC terminal heap over ${minutes} minutes (${start.toFixed(1)} → ${end.toFixed(1)} MB)`, `${(end - start).toFixed(1)} MB`, "≤ 30 MB", end - start <= 30);
}

if (!CHROME) {
  console.log("SKIP: no Chrome found (set CHROME)");
  process.exit(0);
}
const browser = await puppeteer.launch({
  executablePath: CHROME,
  headless: true,
  protocolTimeout: 300000,
  args: ["--no-first-run", "--lang=zh-CN"],
});
try {
  if (process.argv[2] === "memory") {
    await memory(browser);
  } else if (process.argv[2] === "table") {
    await table(browser);
  } else if (process.argv[2] === "margin") {
    await margins(browser);
  } else {
    await terminal(browser, {
      site: "PC",
      base: PC,
      device: { viewport: { width: 1440, height: 900 } },
      budgets: { longTasks: 0, toPixel: 50, switch: 300 },
      tapeTab: "最新成交",
    });
    await terminal(browser, {
      site: "phone",
      base: M,
      device: {
        viewport: { width: 390, height: 844, isMobile: true, hasTouch: true, deviceScaleFactor: 3 },
        userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1",
        cpu: 4,
      },
      budgets: { longTasks: 2, toPixel: 100, switch: 400 },
      bookTab: true,
      tapeTab: "成交",
    });
    await markets(browser);
    await table(browser);
    await margins(browser);
  }
} finally {
  await browser.close();
}
if (misses.length && process.env.BUDGET !== "warn") {
  console.log(`missed: ${misses.join("; ")}`);
  process.exit(1);
}
