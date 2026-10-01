// Measures the budgets of design §12.1 that Lighthouse does not, on the
// deployed sites with live market data (BTC-USDT streams Binance's book
// through HOUSE, about ten depth messages a second):
//
//   node perf.mjs            terminal pages of both sites, the phone's market list, the 1000-row table
//   node perf.mjs table      the 1000-row table alone
//   node perf.mjs memory     the PC terminal's heap over 30 minutes (MINUTES)
//
// For each terminal: main-thread long tasks (> 50 ms) per minute over a
// minute of streaming; WebSocket depth message to the order book's DOM
// update (the frame that shows it); switching pairs (the new pair's
// snapshot to its book on screen); leaving and coming back (no new
// WebSocket, no new snapshot, content within 200 ms). The phone runs with
// the CPU slowed four times, as a mid-range phone. The 1000-row table is
// the design system's virtual DataTable, scrolled for three seconds.
// Prints one line per measurement; exits non-zero when a budget is missed
// (BUDGET=warn only reports).
import { existsSync } from "node:fs";
import puppeteer from "puppeteer-core";

const PC = process.env.PC_BASE ?? "https://astras.vip";
const M = process.env.M_BASE ?? "https://m.astras.vip";
const CHROME =
  process.env.CHROME ??
  ["/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "/usr/bin/google-chrome", "/usr/bin/chromium"].find((p) => existsSync(p));
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const misses = [];

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

// instrument records, in the page, every WebSocket and message (channel,
// type, time), the long tasks, and the order book's DOM changes.
function instrument() {
  window.__perf = { sockets: 0, messages: [], longTasks: [], mutations: [] };
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
  const watch = () => {
    const book = document.querySelector('[role="group"][aria-label="买盘"]')?.parentElement;
    if (!book) return setTimeout(watch, 200);
    new MutationObserver(() => {
      const at = performance.now();
      requestAnimationFrame((frame) => window.__perf.mutations.push({ at, frame }));
    }).observe(book, { subtree: true, childList: true, characterData: true });
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

async function terminal(browser, { site, base, device, budgets, bookTab }) {
  const page = await open(browser, device);
  await page.goto(`${base}/trade/BTC-USDT`, { waitUntil: "networkidle2", timeout: 60000 });
  if (bookTab) {
    await page.evaluate(() => [...document.querySelectorAll("button, [role=tab]")].find((b) => b.textContent.trim() === "盘口")?.click());
  }
  await page.waitForSelector("[data-book-row]", { timeout: 30000 });
  await sleep(5000); // past the load

  // A minute of streaming.
  const from = await page.evaluate(() => performance.now());
  await sleep(60000);
  const window60 = await page.evaluate((t0) => {
    const p = window.__perf;
    return {
      longTasks: p.longTasks.filter((l) => l.at >= t0).map((l) => l.duration),
      depth: p.messages.filter((m) => m.at >= t0 && m.channel === "depth:BTC-USDT").map((m) => m.at),
      mutations: p.mutations.filter((m) => m.at >= t0),
    };
  }, from);
  const perMinute = window60.longTasks.length;
  report(
    `${site} long tasks in a minute of streaming (${window60.depth.length} depth messages)`,
    `${perMinute}${perMinute ? `, longest ${ms(Math.max(...window60.longTasks))}` : ""}`,
    `≤ ${budgets.longTasks}`,
    perMinute <= budgets.longTasks,
  );
  // Each update of the book against the newest depth message it shows
  // (the messages since the previous update; most change no visible level).
  const lat = [];
  let prev = -Infinity;
  for (const m of window60.mutations) {
    const shown = window60.depth.filter((at) => at > prev && at <= m.at);
    prev = m.at;
    if (shown.length) lat.push(m.frame - Math.max(...shown));
  }
  report(
    `${site} depth message to the frame that shows it (p50 / p95 over ${lat.length} book updates)`,
    `${ms(pct(lat, 50))} / ${ms(pct(lat, 95))}`,
    `p95 ≤ ${budgets.toPixel} ms`,
    pct(lat, 95) <= budgets.toPixel,
  );

  // Switching to ETH-USDT: its snapshot to its book on screen.
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
    `${site} switch to ETH-USDT: snapshot to book (from the click ${ms(shown - t0)})`,
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
  await page.browserContext().close();
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
  } else {
    await terminal(browser, {
      site: "PC",
      base: PC,
      device: { viewport: { width: 1440, height: 900 } },
      budgets: { longTasks: 0, toPixel: 50, switch: 300 },
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
    });
    await markets(browser);
    await table(browser);
  }
} finally {
  await browser.close();
}
if (misses.length && process.env.BUDGET !== "warn") {
  console.log(`missed: ${misses.join("; ")}`);
  process.exit(1);
}
