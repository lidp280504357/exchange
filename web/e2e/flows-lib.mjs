// Shared plumbing of the checklist flows (docs/runbook/ui-checklist.md,
// docs/阶段4验收报告.md §8): pc-flows.mjs, m-flows.mjs and admin-flows.mjs
// automate what the user would otherwise tick by hand. Run by
// scripts/e2e/webflows.sh after the smokes.
//
// Each flow is a list of steps named after the checklist item they cover
// ("2", "P4", "A1" ...). A step runs in its own browser tab or tabs; a
// failed step leaves a screenshot of each tab and a log (the error, the
// address, the console, page errors and failed requests) under the run's
// output directory, prints the path and the run goes on with the next
// step; the run fails if any step did. Steps wait on events and states,
// never on time; a check that cannot be made stable stays on the manual
// list instead. Requests are intercepted only to stage a fault (offline,
// an expired token, a degraded mark price), never to change what the
// server holds.
import { AsyncLocalStorage } from "node:async_hooks";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import { loadContracts } from "./contract.mjs";
import { start } from "./lib.mjs";

/** OUT is the run's output directory: failure screenshots and logs. */
export const OUT = process.env.FLOWS_OUT ?? join(homedir(), ".cache", "exchange-e2e", "flows", new Date().toISOString().replace(/[:.]/g, "-"));
const ONLY = (process.env.FLOWS_ONLY ?? "").split(",").map((s) => s.trim()).filter(Boolean);

export const PHONE_IOS = {
  viewport: { width: 390, height: 844, deviceScaleFactor: 3, isMobile: true, hasTouch: true },
  userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 18_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.5 Mobile/15E148 Safari/604.1",
};
export const PHONE_ANDROID = {
  viewport: { width: 412, height: 915, deviceScaleFactor: 2.625, isMobile: true, hasTouch: true },
  userAgent: "Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Mobile Safari/537.36",
};
/** desktop is a desktop window of width × height. */
export const desktop = (width, height = 900) => ({ viewport: { width, height } });
/** phone is the iPhone at another width (the checklist's 360 / 390 / 430). */
export const phone = (width, height = 844) => ({ ...PHONE_IOS, viewport: { ...PHONE_IOS.viewport, width, height } });

function bypassToken() {
  if (process.env.CAPTCHA_BYPASS_TOKEN) return process.env.CAPTCHA_BYPASS_TOKEN;
  const env = new URL("../../.env", import.meta.url);
  if (!existsSync(env)) throw new Error("CAPTCHA_BYPASS_TOKEN is not set and there is no .env");
  return readFileSync(env, "utf8").match(/^CAPTCHA_BYPASS_TOKEN="?([^"\n]*)"?$/m)?.[1];
}

const slug = (s) => s.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "").slice(0, 40);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/**
 * flows starts Chrome for a site and returns the runner: open(...) gives a
 * tab (its own browser context, so its own cookies and session), step(...)
 * runs a checklist step, done() prints the summary and exits.
 */
export async function flows({ site, app, api, apiPrefix = "/v1/" }) {
  mkdirSync(OUT, { recursive: true });
  console.log(`flows ${site}: failures go to ${OUT}`);
  // lib.mjs finds Chrome (or skips the run without it) and launches it the
  // way the smokes do; its own page stays unused, each step opens tabs.
  const t = await start({ app, api, name: site, device: desktop(1280), apiPrefix });
  const browser = t.page.browser();
  await t.page.close();
  const bypass = bypassToken();
  const contracts = loadContracts();
  const violations = new Set();
  const tabs = new Set();
  const results = [];
  const began = Date.now();
  let inStep = false;
  // Each step runs in an async context of its own: a step that ran over
  // its budget may still be working when the next one starts, and whatever
  // it then asks of a tab, or of open(), fails instead of acting on the
  // next step's pages (review BJ ②).
  const steps = new AsyncLocalStorage();
  let current = 0;
  let stepCount = 0;
  const guard = () => {
    const mine = steps.getStore();
    if (mine !== undefined && mine !== current) throw new Error("a tab used after its step's budget ran out");
  };
  /**
   * guarded is page with each of its methods behind guard(), and so are
   * its keyboard, mouse and touchscreen (a sign-in ends with a key press).
   */
  const devices = new Set(["keyboard", "mouse", "touchscreen"]);
  const guarded = (page) =>
    new Proxy(page, {
      get(target, prop) {
        const v = Reflect.get(target, prop, target);
        if (devices.has(prop) && v && typeof v === "object") return guarded(v);
        if (typeof v !== "function") return v;
        return (...args) => {
          guard();
          return v.apply(target, args);
        };
      },
    });
  // What runs before the run exits, whatever ended it (the account's orders cancelled ...).
  const exits = [];

  /**
   * open makes a tab: device ({viewport, userAgent}), settings merged into
   * the site's stored settings before any page loads (locale zh-CN by
   * default), media features to emulate. The tab records its console,
   * page errors and failed requests, and checks every API response against
   * the contracts (except the ones a step staged).
   */
  async function open(opts) {
    guard();
    // persistent: the browser's own profile instead of an off-the-record one
    // (Chrome installs no web app from an off-the-record window).
    const { name, persistent = false } = opts;
    const context = persistent ? browser.defaultBrowserContext() : await browser.createBrowserContext();
    const tab = { name, context, console: [], errors: [], failed: [], staged: new Set() };
    await attach(tab, await context.newPage(), opts);
    // Closed inside a step, a tab stays until the step's evidence is taken.
    tab.close = async () => {
      if (inStep) {
        tab.closing = true;
        return;
      }
      tabs.delete(tab);
      await (persistent ? tab.page.close() : context.close()).catch(() => {});
    };
    // renew gives the tab a fresh page in the same context, back where the
    // old one was (not about:blank, where the site's own navigation and
    // storage are refused; review BJ ①) and signed in through the context's
    // cookies, then closes the old page, which a step that ran over its
    // budget may still be working on.
    tab.renew = async () => {
      const old = tab.page;
      const url = old.url();
      await attach(tab, await context.newPage(), opts);
      if (/^https?:/.test(url)) {
        await tab.page.goto(url, { waitUntil: "domcontentloaded" }).catch(() => {});
        await tab.settled();
      }
      await old.close().catch(() => {});
    };
    tabs.add(tab);
    return tab;
  }

  /** attach sets page up for the tab (device, time zone, media features, settings) and makes it the tab's page. */
  async function attach(tab, page, { device, settings = {}, media = [], timezone = "Asia/Singapore" }) {
    await page.setViewport(device.viewport);
    if (device.userAgent) await page.setUserAgent(device.userAgent);
    await page.emulateTimezone(timezone);
    if (media.length) await page.emulateMediaFeatures(media);
    await page.evaluateOnNewDocument(
      (token, extra) => {
        window.__E2E_CAPTCHA_TOKEN__ = token;
        try {
          // Seeded once per tab: a reload keeps what the step changed since.
          if (sessionStorage.getItem("flows.seeded")) return;
          const stored = JSON.parse(localStorage.getItem("exchange.settings") ?? "null");
          const state = { locale: "zh-CN", ...(stored?.state ?? {}), ...extra };
          localStorage.setItem("exchange.settings", JSON.stringify({ state, version: 1 }));
          sessionStorage.setItem("flows.seeded", "1");
        } catch {
          // about:blank has no storage
        }
      },
      bypass,
      settings,
    );
    page.on("console", (m) => {
      tab.console.push(`${new Date().toISOString()} ${m.type()} ${m.text()}`);
      if (tab.console.length > 300) tab.console.shift();
      if (m.type() === "error" && !m.text().startsWith("Failed to load resource") && !m.text().includes("cloudflareinsights.com")) {
        tab.errors.push("console: " + m.text());
      }
    });
    page.on("pageerror", (e) => tab.errors.push("pageerror: " + e.message));
    // The requests in flight, for settled(): an event stream or a WebSocket
    // never ends, so they are left out; a redirect replaces its hop; a new
    // document (a reload, a page loaded) ends the old one's, which Chrome
    // does not always report as ended (a list's request cut by a reload
    // kept every later settled() waiting its full 20 s).
    const inflight = new Set();
    // What the old document starts between the navigation's request and the
    // new one's commit (a poll) ends with it too: at the main frame's commit
    // (Page.frameNavigated: a new document only) the requests made by
    // another document's loader go (review BP). The loaders come from this
    // tab's own CDP session (no payloads kept), keyed by the CDP request id,
    // which puppeteer's HTTPRequest.id is (internal, checked below).
    const loaders = new Map();
    const cdp = await page.createCDPSession();
    await Promise.all([cdp.send("Page.enable"), cdp.send("Network.enable", { maxTotalBufferSize: 1024, maxResourceBufferSize: 1024 })]);
    cdp.on("Network.requestWillBeSent", ({ requestId, loaderId }) => loaders.set(requestId, loaderId));
    cdp.on("Page.frameNavigated", ({ frame }) => {
      if (frame.parentId) return;
      for (const r of inflight) {
        if (!r.isNavigationRequest() && loaders.has(r.id) && loaders.get(r.id) !== frame.loaderId) {
          inflight.delete(r);
          loaders.delete(r.id);
        }
      }
    });
    page.on("request", (r) => {
      if (typeof r.id !== "string" && !tab.errors.some((e) => e.startsWith("flows-lib:"))) {
        tab.errors.push("flows-lib: puppeteer's HTTPRequest.id is no longer the CDP request id that settled() keys the loaders on");
      }
      for (const hop of r.redirectChain()) inflight.delete(hop);
      if (r.isNavigationRequest() && r.frame() === page.mainFrame()) inflight.clear();
      if (r.resourceType() !== "eventsource" && r.resourceType() !== "websocket") inflight.add(r);
    });
    page.on("requestfinished", (r) => {
      inflight.delete(r);
      loaders.delete(r.id);
    });
    page.on("requestfailed", (r) => {
      inflight.delete(r);
      loaders.delete(r.id);
      tab.failed.push(`${r.method()} ${r.url()} ${r.failure()?.errorText ?? ""}`);
      if (tab.failed.length > 100) tab.failed.shift();
    });
    page.on("response", async (r) => {
      const req = r.request();
      const type = req.resourceType();
      if ((type !== "fetch" && type !== "xhr") || tab.staged.has(req)) return;
      const path = new URL(r.url()).pathname;
      if (!path.startsWith(apiPrefix) || path.startsWith("/v1/dev/") || path === "/v1/ws") return;
      let body;
      try {
        const text = r.status() === 204 ? "" : await r.text();
        body = text ? JSON.parse(text) : undefined;
      } catch {
        return; // navigated away
      }
      const problem = contracts.check(req.method(), path, r.status(), body);
      if (problem) violations.add(problem);
    });
    tab.page = guarded(page);
    for (const [k, v] of Object.entries(helpers(page, app, inflight))) {
      tab[k] =
        typeof v === "function"
          ? (...args) => {
              guard();
              return v(...args);
            }
          : v;
    }
  }

  /**
   * step runs one checklist step. item is the checklist's number, name what
   * it shows; fn gets nothing and throws on a failed check. Options:
   * allowErrors (regexes of console errors the step expects, e.g. offline),
   * fatal (stop the run: the later steps need this one), timeout (ms).
   */
  async function step(item, name, fn, { allowErrors = [], fatal = false, timeout = 240_000 } = {}) {
    // FLOWS_ONLY=3,P4 runs those items alone (and the setup, "—"), to go
    // over a failure again.
    if (ONLY.length && item !== "—" && !ONLY.includes(item)) return true;
    const t0 = Date.now();
    const marks = new Map([...tabs].map((tb) => [tb, tb.errors.length]));
    let error = null;
    let timer;
    let late = false;
    inStep = true;
    current = ++stepCount;
    const run = steps.run(current, () => Promise.resolve().then(fn));
    run.catch(() => {}); // what it does after its budget is not the run's
    try {
      await Promise.race([
        run,
        new Promise((_, reject) => {
          timer = setTimeout(() => {
            late = true;
            reject(new Error(`step over its ${timeout / 1000} s budget`));
          }, timeout);
        }),
      ]);
      const unexpected = [...tabs].flatMap((tb) => tb.errors.slice(marks.get(tb) ?? 0).filter((e) => !allowErrors.some((re) => re.test(e))));
      if (unexpected.length) error = new Error("script errors:\n  " + unexpected.join("\n  "));
    } catch (e) {
      error = e;
    } finally {
      clearTimeout(timer);
      inStep = false;
      current = 0;
    }
    const ms = Date.now() - t0;
    const closeClosing = async () => {
      for (const tb of [...tabs]) if (tb.closing) await tb.close();
    };
    if (!error) {
      results.push({ item, name, ok: true, ms });
      console.log(`ok   ${item.padEnd(4)} ${name} (${(ms / 1000).toFixed(1)} s)`);
      await closeClosing();
      return true;
    }
    const base = join(OUT, `${site}-${slug(item)}-${slug(name)}`);
    const lines = [`${site} ${item} ${name}`, `error: ${error.stack ?? error.message}`, ""];
    for (const tb of tabs) {
      await Promise.race([tb.page.screenshot({ path: `${base}-${slug(tb.name)}.png` }), sleep(10000)]).catch(() => {});
      lines.push(
        `== tab ${tb.name}: ${tb.page.url()}`,
        "-- page errors", ...tb.errors,
        "-- failed requests", ...tb.failed.slice(-30),
        "-- console", ...tb.console.slice(-150), "",
      );
    }
    writeFileSync(`${base}.log`, lines.join("\n"));
    results.push({ item, name, ok: false, ms, error: error.message.split("\n")[0] });
    console.log(`FAIL ${item.padEnd(4)} ${name}: ${error.message}\n     evidence: ${base}.log and ${base}-*.png`);
    await closeClosing();
    if (late) {
      // The step may still be working: its own tabs close, the tabs later
      // steps share start again on fresh pages.
      for (const tb of [...tabs]) await (marks.has(tb) ? tb.renew() : tb.close()).catch(() => {});
    }
    if (fatal) await done();
    return false;
  }

  /** skip records a step that cannot run here, with the reason. */
  function skip(item, name, why) {
    results.push({ item, name, ok: null, ms: 0, error: why });
    console.log(`skip ${item.padEnd(4)} ${name}: ${why}`);
  }

  /** signedIn opens a tab (open's options) signed in as who through the form, at next (/assets by default). */
  async function signedIn(who, opts, next) {
    const tab = await open(opts);
    await tab.signIn(who, next);
    return tab;
  }

  /** atExit adds what must run before the run exits, however it ends (done() runs them in order). */
  function atExit(fn) {
    exits.push(fn);
  }

  /** done runs the cleanups, closes Chrome, prints the summary and exits: 1 when a step failed or a response broke the contracts. */
  async function done() {
    for (const fn of exits.splice(0)) {
      try {
        await fn();
      } catch (e) {
        console.log(`     cleanup: ${e.message}`);
      }
    }
    for (const tb of [...tabs]) await tb.close();
    await browser.close().catch(() => {});
    const failed = results.filter((r) => r.ok === false);
    const skipped = results.filter((r) => r.ok === null);
    const secs = ((Date.now() - began) / 1000).toFixed(0);
    console.log(`\n== ${site} flows: ${results.length - failed.length - skipped.length} ok, ${failed.length} failed, ${skipped.length} skipped in ${secs} s`);
    for (const r of failed) console.log(`   FAIL ${r.item} ${r.name}: ${r.error}`);
    if (violations.size) console.log("FAIL API responses outside the contracts:\n" + [...violations].join("\n"));
    else console.log("ok   every API response matched the OpenAPI contracts");
    writeFileSync(join(OUT, `${site}-summary.json`), JSON.stringify({ site, seconds: Number(secs), results, violations: [...violations] }, null, 2));
    process.exit(failed.length || violations.size ? 1 : 0);
  }

  return { open, signedIn, step, skip, done, atExit, bypass, api, app, results };
}

/** helpers are the lib.mjs helpers for any page: finding things the way a user does, by their visible text. */
export function helpers(page, app, inflight = null) {
  const h = {
    // A live page (prices streaming, lists polling) may never go idle:
    // loaded, then quiet for half a second at most 20 s.
    go: async (path) => {
      const res = await page.goto(app + path, { waitUntil: "domcontentloaded" });
      await h.settled();
      return res;
    },
    waitText: (text, timeout = 20000) => page.waitForFunction((s) => document.body.innerText.includes(s), { timeout }, text),
    waitPath: (path, timeout = 20000) => page.waitForFunction((p) => location.pathname === p, { timeout }, path),
    /** clickButton clicks the visible, enabled button or link whose text is label (scope narrows the search). */
    async clickButton(label, scope = "") {
      const handle = await page.waitForFunction(
        (want, sc) => {
          for (const el of document.querySelectorAll(`${sc} button, ${sc} a`)) {
            const text = el.innerText.replace(/\s+/g, " ").trim();
            const off = el.disabled === true || el.getAttribute("aria-disabled") === "true";
            const r = el.getBoundingClientRect();
            if (text === want && !off && r.width > 0 && r.height > 0 && getComputedStyle(el).visibility !== "hidden") return el;
          }
          return null;
        },
        { timeout: 10000 },
        label,
        scope,
      );
      await handle.evaluate((el) => el.scrollIntoView({ block: "center" }));
      await handle.asElement().click();
    },
    async typeInto(selector, value) {
      await page.waitForSelector(selector, { visible: true });
      await page.$eval(selector, (el) => el.scrollIntoView({ block: "center" }));
      await page.click(selector, { clickCount: 3 });
      await page.keyboard.press("Backspace");
      await page.type(selector, value);
    },
    /** frames waits for count animation frames: layout and paint have caught up with the last change. */
    frames: (count = 2) =>
      page.evaluate((n) => new Promise((done) => {
        const tick = (left) => (left ? requestAnimationFrame(() => tick(left - 1)) : done());
        tick(n);
      }), count),
    /**
     * clickTab presses a tab by its text (Radix tabs switch on a real press,
     * not a scripted click), the nth of that text, brought to the middle of
     * the screen first: near the bottom a phone's fixed bars (the
     * terminal's buy and sell) would take the press.
     */
    async clickTab(text, nth = 0) {
      const handle = await page.waitForFunction(
        (txt, n) => [...document.querySelectorAll("[role=tab]")].filter((el) => el.textContent.trim().startsWith(txt))[n] ?? null,
        { timeout: 10000 },
        text,
        nth,
      );
      await handle.evaluate((el) => el.scrollIntoView({ block: "center" }));
      await h.frames(2);
      await handle.asElement().click();
      await page.waitForFunction((txt) => [...document.querySelectorAll("[role=tab][data-state=active]")].some((el) => el.textContent.trim().startsWith(txt)), { timeout: 5000 }, text);
    },
    /**
     * fieldError is the message tied to the input at selector
     * (aria-describedby) while it is marked invalid, and whether it sits
     * under the input; null without one.
     */
    fieldError: (selector) =>
      page.evaluate((sel) => {
        const input = document.querySelector(sel);
        if (!input || input.getAttribute("aria-invalid") !== "true") return null;
        const msg = (input.getAttribute("aria-describedby") ?? "").split(/\s+/).map((id) => document.getElementById(id)).find((el) => el?.innerText.trim());
        if (!msg) return null;
        const a = input.getBoundingClientRect();
        const m = msg.getBoundingClientRect();
        return { text: msg.innerText.trim(), below: m.top >= a.top && m.top - a.bottom < 80 && Math.abs(m.left - a.left) < 300 };
      }, selector),
    /**
     * signIn signs in as who ({email, password}) through the user sites'
     * form and waits for next (the console's flows sign in their own way).
     */
    async signIn(who, next = "/assets") {
      await h.go(`/login?next=${encodeURIComponent(next)}`);
      await h.typeInto('input[autocomplete="username"]', who.email);
      await h.typeInto('input[autocomplete="current-password"]', who.password);
      await page.keyboard.press("Enter");
      await h.waitPath(next.split("?")[0], 30000);
    },
    /**
     * nav changes page by the site's own router (history and popstate),
     * then waits for it to settle; a page elsewhere (about:blank, another
     * origin) loads the address instead.
     */
    nav: async (path) => {
      const at = await page.evaluate(() => location.origin).catch(() => "");
      if (at !== new URL(app).origin) return h.go(path);
      await page.evaluate((p) => {
        history.pushState({}, "", p);
        dispatchEvent(new PopStateEvent("popstate"));
      }, path);
      await page.waitForFunction((p) => location.pathname + location.search === p, { timeout: 10000 }, path);
      await h.settled();
    },
    /**
     * settled waits for the page's requests to be done for 500 ms (event
     * streams and WebSockets aside), timeout at most; FLOWS_DEBUG=1 prints
     * what was still in flight then.
     */
    settled: async (timeout = 20000) => {
      if (!inflight) return page.waitForNetworkIdle({ idleTime: 500, timeout }).catch(() => {});
      const end = Date.now() + timeout;
      let quiet = 0;
      while (Date.now() < end) {
        if (page.isClosed()) return;
        if (inflight.size) quiet = 0;
        else if (!quiet) quiet = Date.now();
        else if (Date.now() - quiet >= 500) return;
        await sleep(50);
      }
      if (process.env.FLOWS_DEBUG) console.log(`     settled: in flight after ${timeout} ms: ${[...inflight].map((r) => `${r.resourceType()} ${r.url()}`).join(", ")}`);
    },
  };
  return h;
}

// --- Calls to the API from Node (the flows' own accounts) ------------------

/** signInApi signs the flows' account in over the API: an access token. */
export async function signInApi(base, who) {
  const res = await api(base, "POST", "/v1/auth/login/password", { identifier: who.email, password: who.password, device_id: `flows-${Date.now()}` });
  if (res.status !== 200) throw new Error(`login/password: ${res.status} ${JSON.stringify(res.body)}`);
  return res.body.access_token;
}

/** spotAvailable is what the account has available of asset on its spot account ("0" without any). */
export async function spotAvailable(base, token, asset) {
  const res = await api(base, "GET", "/v1/account/balances?account_type=SPOT", undefined, { Authorization: `Bearer ${token}` });
  if (res.status !== 200) throw new Error(`balances: ${res.status} ${JSON.stringify(res.body)}`);
  return res.body.balances.find((b) => b.asset === asset)?.available ?? "0";
}

/** cancelOrders cancels every active spot order of the account: a step that placed one leaves none behind. */
export async function cancelOrders(base, who) {
  const token = await signInApi(base, who);
  const res = await api(base, "DELETE", "/v1/orders", undefined, { Authorization: `Bearer ${token}` });
  if (res.status !== 202) throw new Error(`cancel orders: ${res.status} ${JSON.stringify(res.body)}`);
  return res.body.requested;
}

/**
 * listDecimals maps each asset to the decimals its amounts show with in the
 * sites' lists: the asset's own, at most 8 (shownDecimals, cut).
 */
export async function listDecimals(base) {
  const res = await api(base, "GET", "/v1/market/assets");
  if (res.status !== 200) throw new Error(`/v1/market/assets: ${res.status}`);
  return new Map(res.body.assets.map((a) => [a.asset_code, Math.min(a.decimals, 8)]));
}

/** decimalIssues lists the amounts of rows ({asset, values}) not at their asset's places (want: listDecimals). */
export function decimalIssues(where, rows, want) {
  const out = [];
  for (const { asset, values } of rows) {
    const places = want.get(asset);
    if (places === undefined) {
      out.push(`${where}: "${asset}" is no asset of /v1/market/assets`);
      continue;
    }
    for (const v of values) {
      const got = v.includes(".") ? v.split(".")[1].length : 0;
      if (got !== places) out.push(`${where}: ${asset} "${v}" with ${got} decimals, the asset's lists show ${places}`);
    }
  }
  return out;
}

/** api calls the user API: {status, body}. */
export async function api(base, method, path, body, headers = {}) {
  const res = await fetch(base + path, {
    method,
    headers: { "Content-Type": "application/json", "X-Client-Type": "APP", ...headers },
    body: body === undefined ? undefined : JSON.stringify(body),
  });
  const text = await res.text();
  let json;
  try {
    json = text ? JSON.parse(text) : undefined;
  } catch {
    json = text;
  }
  return { status: res.status, body: json };
}

async function inbox(base, target) {
  const r = await api(base, "GET", `/v1/dev/messages?target=${encodeURIComponent(target)}&limit=50`);
  return r.body?.messages ?? [];
}

/** code waits for a new message to target (more than before) and returns its 6-digit code. */
export async function code(base, target, before) {
  for (let i = 0; i < 60; i++) {
    const messages = await inbox(base, target);
    if (messages.length > before) {
      // Six digits on their own: a longer number (a platform name with a
      // run's number in it) is not a code.
      const c = `${messages[0].subject ?? ""} ${messages[0].body ?? ""}`.match(/(?<!\d)\d{6}(?!\d)/)?.[0];
      if (c) return c;
    }
    await sleep(500); // polling the dev inbox, not waiting for the page
  }
  throw new Error("no code arrived for " + target);
}

export const inboxCount = async (base, target) => (await inbox(base, target)).length;

/**
 * register signs up an account over the API, as scripts/e2e/lib/common.sh
 * does (the human check passes with the bypass token), and returns its
 * credentials; the browser signs in with them through the form.
 */
export async function register(base, bypass, email, password) {
  const device = `flows-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  const terms = await api(base, "GET", "/v1/auth/terms");
  if (terms.status !== 200) throw new Error(`terms: ${terms.status}`);
  const before = await inboxCount(base, email);
  const req = await api(base, "POST", "/v1/auth/otp/request", { scene: "REGISTER", channel: "EMAIL", identifier: email, captcha_token: bypass, device_id: device });
  if (req.status !== 200) throw new Error(`otp/request: ${req.status} ${JSON.stringify(req.body)}`);
  const c = await code(base, email, before);
  const ver = await api(base, "POST", "/v1/auth/otp/verify", { challenge_id: req.body.challenge_id, code: c, device_id: device });
  if (ver.status !== 200) throw new Error(`otp/verify: ${ver.status} ${JSON.stringify(ver.body)}`);
  const done = await api(base, "POST", "/v1/auth/register/complete", {
    otp_ticket: ver.body.otp_ticket, password, country: "SG",
    terms_version: terms.body.terms_version, risk_disclosure_version: terms.body.risk_disclosure_version, device_id: device,
  });
  if (done.status !== 201) throw new Error(`register/complete: ${done.status} ${JSON.stringify(done.body)}`);
  return { email, password, accessToken: done.body.access_token };
}

// --- Checks evaluated in the page ------------------------------------------

/** siteCookieDomain is the domain the sites share their cookies on (".astras.vip" for https://m.astras.vip). */
export const siteCookieDomain = (app) => `.${new URL(app).hostname.split(".").slice(-2).join(".")}`;

/** fmtTime formats an ISO time as the sites do (YYYY-MM-DD HH:mm:ss) in zone. */
export function fmtTime(iso, zone) {
  const p = Object.fromEntries(
    new Intl.DateTimeFormat("en-CA", { timeZone: zone, year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" })
      .formatToParts(new Date(iso))
      .map((x) => [x.type, x.value]),
  );
  return `${p.year}-${p.month}-${p.day} ${p.hour}:${p.minute}:${p.second}`;
}

/** colorsOf returns the computed colours of the first few elements of each class, leaving out those fading between two colours. */
export const colorsOf = (tab, classes) =>
  tab.page.evaluate((cls) => {
    const out = {};
    for (const c of cls) {
      out[c] = [...document.querySelectorAll(`.${c}`)]
        .filter((el) => el.getBoundingClientRect().width > 0 && !el.getAnimations().some((a) => a.playState === "running"))
        .slice(0, 5)
        .map((el) => getComputedStyle(el)[c.startsWith("bg-") ? "backgroundColor" : "color"]);
    }
    return out;
  }, classes);

/**
 * scrollThrough scrolls the page to its end a screen at a time, letting
 * each screen's lazy content load (network quiet); at most screens of
 * them (a list that loads more as it scrolls has no end).
 */
export async function scrollThrough(tab, screens = 40) {
  for (let i = 0; i < screens; i++) {
    const more = await tab.page.evaluate(() => {
      const before = window.scrollY;
      window.scrollBy(0, window.innerHeight);
      return window.scrollY > before;
    });
    await tab.frames();
    if (!more) break;
    await tab.settled(5000);
  }
}

/** overflowX lists what sticks out of the page sideways (empty when nothing scrolls horizontally). */
export function overflowX(page) {
  return page.evaluate(() => {
    const doc = document.documentElement;
    const width = doc.clientWidth;
    if (doc.scrollWidth <= width + 1 && document.body.scrollWidth <= width + 1) return [];
    const out = [];
    for (const el of document.body.querySelectorAll("*")) {
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.right <= width + 1) continue;
      // Inside a box that scrolls or clips sideways it is that box's
      // business. An absolutely placed element (a visually hidden sr-only
      // text) is clipped only from its containing block outwards: a box
      // that is not positioned does not clip it.
      let p = el.parentElement;
      if (getComputedStyle(el).position === "absolute") {
        while (p && p !== document.body) {
          const s = getComputedStyle(p);
          if (s.position !== "static" || s.transform !== "none" || s.filter !== "none" || s.contain !== "none") break;
          p = p.parentElement;
        }
      }
      let clipped = false;
      while (p && p !== document.body) {
        const s = getComputedStyle(p);
        if (s.overflowX !== "visible" && p.getBoundingClientRect().right <= width + 1) {
          clipped = true;
          break;
        }
        p = p.parentElement;
      }
      if (!clipped) out.push(`${describe(el)} right ${Math.round(r.right)} > ${width}`);
      if (out.length >= 8) break;
    }
    return out.length ? out : [`the page scrolls ${doc.scrollWidth - width}px sideways`];

    function describe(el) {
      const id = el.id ? `#${el.id}` : "";
      const test = el.getAttribute("data-testid") ? `[data-testid=${el.getAttribute("data-testid")}]` : "";
      const cls = typeof el.className === "string" ? "." + el.className.split(/\s+/).filter(Boolean).slice(0, 3).join(".") : "";
      return `${el.tagName.toLowerCase()}${id}${test}${cls} "${(el.innerText ?? "").trim().slice(0, 30)}"`;
    }
  });
}

/**
 * contrastIssues measures the contrast of every visible text against the
 * colours behind it (WCAG 2.x: 4.5:1, 3:1 for large text), grouped by
 * colour pair. Text over an image or a gradient, disabled controls and
 * hidden text are left out (the image and gradient cases stay on the
 * manual list).
 */
export async function contrastIssues(page) {
  // A part that is syncing is dimmed on purpose (the book at half opacity
  // under its 同步中 pill): read the page once it is live and its fades
  // have ended.
  await page.waitForFunction(() => !document.body.innerText.includes("同步中"), { timeout: 20000 }).catch(() => {});
  await page.evaluate(() =>
    Promise.all(
      document
        .getAnimations()
        .filter((a) => a.playState === "running" && Number.isFinite(a.effect?.getComputedTiming().endTime))
        .map((a) => a.finished.catch(() => {})),
    ),
  );
  return page.evaluate(() => {
    // Computed colours come as rgb(), color(srgb), or oklab()/oklch() for
    // Tailwind's opacity modifiers (color-mix in oklab).
    const num = (v, scale = 1) => (v.endsWith("%") ? (parseFloat(v) / 100) * scale : parseFloat(v));
    const fromOklab = (L, a, b, alpha) => {
      const l = (L + 0.3963377774 * a + 0.2158037573 * b) ** 3;
      const m = (L - 0.1055613458 * a - 0.0638541728 * b) ** 3;
      const s = (L - 0.0894841775 * a - 1.291485548 * b) ** 3;
      const lin = [4.0767416621 * l - 3.3077115913 * m + 0.2309699292 * s, -1.2684380046 * l + 2.6097574011 * m - 0.3413193965 * s, -0.0041960863 * l - 0.7034186147 * m + 1.707614701 * s];
      const gamma = (c) => 255 * Math.min(1, Math.max(0, c <= 0.0031308 ? 12.92 * c : 1.055 * c ** (1 / 2.4) - 0.055));
      return [...lin.map(gamma), alpha];
    };
    const parse = (s) => {
      let m = s.match(/rgba?\(([\d.]+),\s*([\d.]+),\s*([\d.]+)(?:,\s*([\d.]+))?\)/);
      if (m) return [+m[1], +m[2], +m[3], m[4] === undefined ? 1 : +m[4]];
      m = s.match(/color\(srgb ([\d.e-]+) ([\d.e-]+) ([\d.e-]+)(?: \/ ([\d.]+))?\)/);
      if (m) return [m[1] * 255, m[2] * 255, m[3] * 255, m[4] === undefined ? 1 : +m[4]];
      m = s.match(/oklab\(([\d.%e-]+) ([\d.%e-]+) ([\d.%e-]+)(?: \/ ([\d.%]+))?\)/);
      if (m) return fromOklab(num(m[1]), num(m[2], 0.4), num(m[3], 0.4), m[4] === undefined ? 1 : num(m[4]));
      m = s.match(/oklch\(([\d.%e-]+) ([\d.%e-]+) ([\d.e-]+)(?:deg)?(?: \/ ([\d.%]+))?\)/);
      if (m) {
        const C = num(m[2], 0.4);
        const h = (parseFloat(m[3]) * Math.PI) / 180;
        return fromOklab(num(m[1]), C * Math.cos(h), C * Math.sin(h), m[4] === undefined ? 1 : num(m[4]));
      }
      return null;
    };
    const over = (top, bottom) => {
      const a = top[3];
      return [top[0] * a + bottom[0] * (1 - a), top[1] * a + bottom[1] * (1 - a), top[2] * a + bottom[2] * (1 - a), 1];
    };
    const lum = ([r, g, b]) => {
      const f = (c) => ((c /= 255) <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
      return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
    };
    // cover is what an absolutely placed child without text paints over
    // its parent (a segmented control's sliding thumb, a book row's depth
    // bar): a colour, "image" for a gradient or an image, or null.
    const covers = new Map();
    const cover = (c) => {
      if (!covers.has(c)) {
        const cs = getComputedStyle(c);
        let v = null;
        if (cs.position === "absolute" && !c.textContent.trim()) {
          if (cs.backgroundImage !== "none") v = "image";
          else {
            const cc = parse(cs.backgroundColor);
            if (cc && cc[3] * Number(cs.opacity) > 0) v = [cc[0], cc[1], cc[2], cc[3] * Number(cs.opacity)];
          }
        }
        covers.set(c, v);
      }
      return covers.get(c);
    };
    /**
     * behind is the opaque colour behind el, or null over an image or a
     * gradient: the backgrounds of el and its ancestors, and at each level
     * the covers under the middle of el.
     */
    const behind = (el) => {
      const r = el.getBoundingClientRect();
      const x = r.left + r.width / 2;
      const y = r.top + r.height / 2;
      const layers = [];
      let opaque = false;
      for (let p = el, from = null; p && !opaque; from = p, p = p.parentElement) {
        for (const c of p.children) {
          if (c === from) continue;
          const v = cover(c);
          if (!v) continue;
          const cr = c.getBoundingClientRect();
          if (x < cr.left || x > cr.right || y < cr.top || y > cr.bottom) continue;
          if (v === "image") return null;
          layers.push(v);
          if (v[3] >= 1) {
            opaque = true;
            break;
          }
        }
        if (opaque) break;
        const s = getComputedStyle(p);
        if (s.backgroundImage !== "none") return null;
        const c = parse(s.backgroundColor);
        if (c && c[3] > 0) {
          layers.push(c);
          if (c[3] >= 1) opaque = true;
        }
      }
      let bg = [255, 255, 255, 1];
      if (!layers.length || layers[layers.length - 1][3] < 1) bg = parse(getComputedStyle(document.documentElement).backgroundColor) ?? bg;
      for (let i = layers.length - 1; i >= 0; i--) bg = over(layers[i], bg);
      return bg;
    };
    const groups = new Map();
    for (const el of document.body.querySelectorAll("*")) {
      if (![...el.childNodes].some((n) => n.nodeType === 3 && n.textContent.trim())) continue;
      if (el.closest("[aria-hidden=true], svg, canvas, :disabled, [aria-disabled=true]")) continue;
      const s = getComputedStyle(el);
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0 || s.visibility === "hidden") continue;
      let alpha = 1;
      for (let p = el; p; p = p.parentElement) alpha *= Number(getComputedStyle(p).opacity);
      if (alpha === 0) continue;
      const bg = behind(el);
      const fg = parse(s.color);
      // Transparent text (skeletons, text drawn by a gradient) is not read.
      if (!bg || !fg || fg[3] === 0) continue;
      const text = over([fg[0], fg[1], fg[2], fg[3] * alpha], bg);
      const [hi, lo] = [lum(text), lum(bg)].sort((a, b) => b - a);
      const ratio = (hi + 0.05) / (lo + 0.05);
      const size = parseFloat(s.fontSize);
      const large = size >= 24 || (size >= 18.66 && Number(s.fontWeight) >= 700);
      const need = large ? 3 : 4.5;
      if (ratio >= need) continue;
      const key = `${s.color} on ${bg.slice(0, 3).map(Math.round).join(",")}`;
      const g = groups.get(key) ?? { pair: key, ratio: Math.round(ratio * 100) / 100, need, count: 0, samples: [] };
      g.count++;
      if (g.samples.length < 3) g.samples.push(el.innerText.trim().slice(0, 24));
      groups.set(key, g);
    }
    return [...groups.values()].sort((a, b) => b.count - a.count);
  });
}

/**
 * truncatedWithoutHint lists text cut short with an ellipsis that offers
 * no way to read it whole: no title on it or near it, no tooltip trigger,
 * no copy button beside it.
 */
export function truncatedWithoutHint(page) {
  return page.evaluate(() => {
    const out = [];
    for (const el of document.body.querySelectorAll("*")) {
      const s = getComputedStyle(el);
      const clamped = s.webkitLineClamp !== "none" && s.webkitLineClamp !== "";
      if (s.textOverflow !== "ellipsis" && !clamped) continue;
      if (el.scrollWidth <= el.clientWidth + 1 && el.scrollHeight <= el.clientHeight + 1) continue;
      // Text cut short, not a chart or an icon wider than its cell.
      if (el.getBoundingClientRect().width === 0 || !el.innerText.trim()) continue;
      let hint = false;
      for (let p = el, i = 0; p && i < 3; p = p.parentElement, i++) {
        if (p.title || p.getAttribute("aria-label") || p.getAttribute("data-state") || p.getAttribute("aria-describedby")) hint = true;
        const box = p.parentElement;
        if (box && [...box.querySelectorAll("button")].some((b) => /copy|复制/i.test(`${b.getAttribute("aria-label") ?? ""} ${b.title} ${b.innerText}`))) hint = true;
        if (hint) break;
      }
      if (!hint) out.push(`${el.tagName.toLowerCase()} "${el.innerText.trim().slice(0, 40)}"`);
      if (out.length >= 10) break;
    }
    return out;
  });
}

/**
 * longAnimations lists the animations the page runs (or waits to run)
 * longer than 10 ms, CSS or script (motion runs its fades through the Web
 * Animations API): with reduced motion there should be none.
 */
export function longAnimations(tab) {
  return tab.page.evaluate(() =>
    document
      .getAnimations()
      .filter((a) => a.playState !== "finished")
      .map((a) => {
        const t = a.effect?.getTiming?.() ?? {};
        const total = (Number(t.duration) || 0) + (Number(t.delay) || 0);
        const el = a.effect?.target;
        const name = a.animationName || a.transitionProperty || (a.effect?.getKeyframes?.() ?? []).flatMap((k) => Object.keys(k)).filter((k) => !["offset", "easing", "composite", "computedOffset"].includes(k))[0] || "animation";
        const cls = typeof el?.className === "string" ? "." + el.className.split(/\s+/).filter(Boolean).slice(0, 2).join(".") : "";
        const text = (el?.innerText ?? "").trim().replace(/\s+/g, " ").slice(0, 20);
        return { total, what: `${name} on ${el?.tagName?.toLowerCase() ?? "?"}${cls}${text ? ` "${text}"` : ""}` };
      })
      .filter((x) => x.total > 10 || !Number.isFinite(x.total))
      .map((x) => `${x.what} (${Number.isFinite(x.total) ? Math.round(x.total) + " ms" : "endless"})`),
  );
}

/**
 * textAligned says, for each element matching selector, where its text
 * sits in its box ("right", "left", "center", or "full" when it fills it):
 * where the text is drawn, not the CSS that put it there (a flex cell
 * aligns with justify-content, not text-align).
 */
export function textAligned(page, selector) {
  return page.$$eval(selector, (els) =>
    els
      .filter((el) => el.innerText.trim() && el.getBoundingClientRect().width > 0)
      .map((el) => {
        const range = document.createRange();
        range.selectNodeContents(el);
        const t = range.getBoundingClientRect();
        const r = el.getBoundingClientRect();
        const s = getComputedStyle(el);
        const dl = t.left - (r.left + parseFloat(s.paddingLeft) + parseFloat(s.borderLeftWidth));
        const dr = r.right - parseFloat(s.paddingRight) - parseFloat(s.borderRightWidth) - t.right;
        const align = dl < 2 && dr < 2 ? "full" : dr < 2 ? "right" : dl < 2 ? "left" : Math.abs(dl - dr) < 2 ? "center" : "other";
        return { text: el.innerText.trim(), align, nums: s.fontVariantNumeric };
      }),
  );
}

/**
 * wsWatch counts the page's WebSocket connections and the frames they
 * receive (Chrome DevTools Protocol), for "no new connection" and "pushes
 * resumed" checks.
 */
export async function wsWatch(page) {
  const cdp = await page.createCDPSession();
  await cdp.send("Network.enable");
  const w = { created: 0, frames: 0, closed: 0 };
  cdp.on("Network.webSocketCreated", () => w.created++);
  cdp.on("Network.webSocketFrameReceived", () => w.frames++);
  cdp.on("Network.webSocketClosed", () => w.closed++);
  /** until waits for test(w) to hold, checking on each event, up to timeout ms. */
  w.until = (test, what, timeout = 20000) =>
    new Promise((resolve, reject) => {
      if (test(w)) return resolve();
      const check = () => {
        if (test(w)) {
          cleanup();
          resolve();
        }
      };
      const timer = setTimeout(() => {
        cleanup();
        reject(new Error(`${what}: ${JSON.stringify({ created: w.created, frames: w.frames, closed: w.closed })}`));
      }, timeout);
      const events = ["Network.webSocketCreated", "Network.webSocketFrameReceived", "Network.webSocketClosed"];
      const cleanup = () => {
        clearTimeout(timer);
        for (const e of events) cdp.off(e, check);
      };
      for (const e of events) cdp.on(e, check);
    });
  return w;
}

/**
 * stage answers the next request matching test with response once (a
 * fault: an expired token, a degraded contract) and lets everything else
 * through; the answered request is left out of the contract check.
 */
export async function stage(tab, test, response) {
  await tab.page.setRequestInterception(true);
  let used = false;
  const handler = (req) => {
    if (req.isInterceptResolutionHandled()) return;
    if (!used && test(req)) {
      used = true;
      tab.staged.add(req);
      return req.respond(response);
    }
    return req.continue();
  };
  tab.page.on("request", handler);
  return {
    get used() {
      return used;
    },
    async stop() {
      tab.page.off("request", handler);
      await tab.page.setRequestInterception(false);
    },
  };
}
