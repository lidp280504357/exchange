// Shared plumbing of the browser smoke tests (pc-smoke.mjs, m-smoke.mjs,
// admin-smoke.mjs):
// Chrome, the human-check bypass, the dev inbox, the contract check of
// every API response and of the pushes of the channels in PUSH_SCHEMAS,
// and helpers that find elements the way a user does, by their visible
// text.
import { execFileSync } from "node:child_process";
import { appendFileSync, existsSync, mkdirSync, readFileSync } from "node:fs";
import puppeteer from "puppeteer-core";
import { loadContracts } from "./contract.mjs";

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// The WebSocket channels whose messages the contracts describe (file and
// schema of the message's data); the smokes keep and check their pushes.
const PUSH_SCHEMAS = { margin: ["margin.yaml", "MarginPush"] };
export const ok = (what) => console.log("ok   " + what);

const CHROME =
  process.env.CHROME ??
  ["/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "/usr/bin/google-chrome", "/usr/bin/chromium", "/usr/bin/chromium-browser"].find((p) =>
    existsSync(p),
  );

function bypassToken() {
  if (process.env.CAPTCHA_BYPASS_TOKEN) return process.env.CAPTCHA_BYPASS_TOKEN;
  const env = new URL("../../.env", import.meta.url);
  if (!existsSync(env)) throw new Error("CAPTCHA_BYPASS_TOKEN is not set and there is no .env");
  return readFileSync(env, "utf8").match(/^CAPTCHA_BYPASS_TOKEN="?([^"\n]*)"?$/m)?.[1];
}

/**
 * start opens headless Chrome on app (the site under test; api answers the
 * dev inbox) with one page in Chinese. device is { viewport, userAgent }
 * (a phone sets isMobile and hasTouch in its viewport); name prefixes the
 * screenshots written to SHOTS. Without Chrome the run is skipped.
 */
export async function start({ app, api, name, device, apiPrefix = "/v1/" }) {
  if (!CHROME) {
    console.log("SKIP browser checks: no Chrome found (set CHROME)");
    process.exit(0);
  }
  const shots = process.env.SHOTS ?? "";
  if (shots) mkdirSync(shots, { recursive: true });
  const bypass = bypassToken();
  const contracts = loadContracts();
  const violations = new Set();
  const errors = [];

  const browser = await puppeteer.launch({ executablePath: CHROME, headless: true, args: ["--no-first-run", "--lang=zh-CN"] });
  const context = await browser.createBrowserContext();
  const page = await context.newPage();
  await page.setViewport(device.viewport);
  if (device.userAgent) await page.setUserAgent(device.userAgent);
  await page.emulateTimezone("Asia/Singapore");
  await page.evaluateOnNewDocument((token) => {
    // Chinese, the site's first language; the human check passes with the bypass token.
    if (!localStorage.getItem("exchange.settings")) {
      localStorage.setItem("exchange.settings", JSON.stringify({ state: { locale: "zh-CN" }, version: 1 }));
    }
    window.__E2E_CAPTCHA_TOKEN__ = token;
  }, bypass);
  page.on("pageerror", (e) => errors.push("pageerror: " + e.message));
  // The pushes of the channels in PUSH_SCHEMAS, from the page's WebSocket
  // frames, each checked against its schema; and the channels the server
  // confirmed the page's subscription to (on its latest connection: a
  // margin account is pushed as it changes, not replayed to a page that
  // subscribed after).
  const pushes = [];
  const subscribed = new Set();
  const cdp = await page.createCDPSession();
  await cdp.send("Network.enable");
  cdp.on("Network.webSocketCreated", () => subscribed.clear());
  cdp.on("Network.webSocketFrameReceived", ({ response }) => {
    let m;
    try {
      m = JSON.parse(response.payloadData);
    } catch {
      return;
    }
    if (m?.op === "subscribe" && m.ok === true && Array.isArray(m.args)) for (const a of m.args) subscribed.add(a);
    const schema = PUSH_SCHEMAS[m?.channel];
    if (!schema || m.data === undefined) return;
    pushes.push(m);
    const problem = contracts.checkSchema(...schema, m.data);
    if (problem) violations.add(`push on ${m.channel}: ${problem}`);
  });
  // Chrome logs every 4xx fetch as an error; expected API errors are
  // asserted by the scripts, so only script errors count here.
  page.on("console", (m) => {
    // Cloudflare injects its analytics beacon into every page; the admin
    // console's CSP (default-src 'self') blocks it, which is the point.
    if (m.type() === "error" && !m.text().startsWith("Failed to load resource") && !m.text().includes("cloudflareinsights.com")) {
      errors.push("console: " + m.text());
    }
  });
  // The signed-in user, from the token responses (sign-up, sign-in, refresh).
  let userId = null;
  page.on("response", async (r) => {
    const type = r.request().resourceType();
    if ((type !== "fetch" && type !== "xhr") || !r.url().includes(apiPrefix)) return;
    let body;
    try {
      const text = r.status() === 204 ? "" : await r.text();
      body = text ? JSON.parse(text) : undefined;
    } catch {
      return; // body unavailable (navigated away)
    }
    if (typeof body?.user_id === "string" && typeof body?.access_token === "string") {
      userId = body.user_id;
      // A sign-up, not a sign-in: the run's exit hook clears the account out (F38).
      if (r.status() === 201 && new URL(r.url()).pathname === "/v1/auth/register/complete") noteRegistered(body.user_id);
    }
    const path = new URL(r.url()).pathname;
    if (!path.startsWith(apiPrefix) || path.startsWith("/v1/dev/") || path === "/v1/ws") return;
    const problem = contracts.check(r.request().method(), path, r.status(), body);
    if (problem) violations.add(problem);
  });

  const inbox = async (target) => {
    const r = await fetch(`${api}/v1/dev/messages?target=${encodeURIComponent(target)}&limit=50`);
    return (await r.json()).messages ?? [];
  };

  const t = {
    page,
    /**
     * firstScreenFailures opens path afresh and returns the requests that
     * failed while its first screen came up (ready: a selector to wait for,
     * else the network going quiet), each as "status method url" for an
     * answer of 400 or more or "failed reason url": the browser logs every
     * one in red in its console, as the user saw for the home page's hero
     * (B117). A request the page aborted itself is not a failure.
     */
    async firstScreenFailures(path, ready) {
      const failed = [];
      const onResponse = (r) => {
        if (r.status() >= 400) failed.push(`${r.status()} ${r.request().method()} ${r.url()}`);
      };
      const onFailed = (q) => {
        const why = q.failure()?.errorText ?? "";
        if (!why.includes("ERR_ABORTED")) failed.push(`failed ${why} ${q.url()}`);
      };
      page.on("response", onResponse);
      page.on("requestfailed", onFailed);
      try {
        await page.goto(app + path, { waitUntil: "networkidle2" });
        if (ready) await page.waitForSelector(ready, { visible: true, timeout: 20000 });
        await sleep(1500);
      } finally {
        page.off("response", onResponse);
        page.off("requestfailed", onFailed);
      }
      return failed;
    },
    /**
     * openMargin opens margin trading for the signed-in user alone through
     * web.sh's helper (scripts/e2e/lib/margin-user.sh, put back when web.sh
     * ends; a switch on for everyone it leaves as it is) and reports whether
     * margin trading is open to the user: run on their own, the smokes leave
     * the switch as it is. The services see a change within 5 seconds.
     */
    async openMargin() {
      const helper = process.env.MARGIN_USER_HELPER;
      if (!helper || !userId) return false;
      execFileSync("bash", [helper, "on", userId], { stdio: "inherit" });
      await sleep(6000);
      return true;
    },
    shot: (label) => (shots ? page.screenshot({ path: `${shots}/${name}-${label}.png`, fullPage: false }) : undefined),
    /** waitSubscribed waits up to timeout ms for the server to confirm the page's subscription to channel. */
    async waitSubscribed(channel, timeout = 20000) {
      for (const end = Date.now() + timeout; Date.now() < end; await sleep(100)) if (subscribed.has(channel)) return;
      throw new Error(`the page was not subscribed to ${channel} within ${timeout / 1000} s`);
    },
    /** waitPush waits up to timeout ms for a push (of a channel in PUSH_SCHEMAS) that pred accepts, and returns it. */
    async waitPush(pred, timeout = 15000, what = "the push") {
      for (const end = Date.now() + timeout; Date.now() < end; await sleep(250)) {
        const p = pushes.find(pred);
        if (p) return p;
      }
      throw new Error(`no ${what} within ${timeout / 1000} s (${pushes.length} pushes kept)`);
    },
    go: (path) => page.goto(app + path, { waitUntil: "networkidle2" }),
    waitText: (text, timeout = 20000) => page.waitForFunction((s) => document.body.innerText.includes(s), { timeout }, text),
    waitPath: (path, timeout = 20000) => page.waitForFunction((p) => location.pathname === p, { timeout }, path),
    inboxCount: async (target) => (await inbox(target)).length,

    /** readCode waits for a new message to target and returns its 6-digit code. */
    async readCode(target, before) {
      for (let i = 0; i < 40; i++) {
        await sleep(500);
        const messages = await inbox(target);
        if (messages.length > before) {
          const code = `${messages[0].subject ?? ""} ${messages[0].body ?? ""}`.match(/\d{6}/)?.[0];
          if (code) return code;
        }
      }
      throw new Error("no code arrived for " + target);
    },

    /**
     * clickButton clicks the visible, enabled button (or link) whose text
     * is label, waiting up to 10 s for it; scope narrows the search (a
     * dialog or a sheet).
     */
    async clickButton(label, scope = "") {
      for (let i = 0; i < 40; i++) {
        for (const h of await page.$$(`${scope} button, ${scope} a`)) {
          const [text, disabled] = await h.evaluate((el) => [el.innerText.replace(/\s+/g, " ").trim(), el.disabled === true || el.getAttribute("aria-disabled") === "true"]);
          if (text === label && !disabled && (await h.isVisible())) {
            await h.evaluate((el) => el.scrollIntoView({ block: "center" }));
            return h.click();
          }
        }
        await sleep(250);
      }
      throw new Error(`no visible, enabled button "${label}"`);
    },

    /**
     * clickLive clicks the element of a list that refreshes itself (the
     * admin's live tables): when a refresh replaces it between finding and
     * clicking, it finds it again, a few times.
     */
    async clickLive(selector) {
      for (let i = 0; ; i++) {
        try {
          return await page.click(selector);
        } catch (e) {
          if (i >= 5 || !/detached|not clickable/i.test(String(e))) throw e;
          await sleep(300);
        }
      }
    },

    /** clickContaining clicks the first visible, enabled button whose text has every part. */
    async clickContaining(parts, scope = "") {
      for (let i = 0; i < 40; i++) {
        for (const h of await page.$$(`${scope} button`)) {
          const [text, disabled] = await h.evaluate((el) => [el.innerText, el.disabled === true]);
          if (!disabled && parts.every((p) => text.includes(p)) && (await h.isVisible())) {
            await h.evaluate((el) => el.scrollIntoView({ block: "center" }));
            return h.click();
          }
        }
        await sleep(250);
      }
      throw new Error(`no visible, enabled button with ${parts.join(" + ")}`);
    },

    async typeInto(selector, value) {
      await page.waitForSelector(selector, { visible: true });
      await page.$eval(selector, (el) => el.scrollIntoView({ block: "center" }));
      await page.click(selector, { clickCount: 3 });
      await page.keyboard.press("Backspace");
      await page.type(selector, value);
    },

    /** fail reports the failure with the page's address and script errors, then exits 1. */
    async fail(e) {
      await t.shot("failure");
      console.error("FAIL", e.message);
      console.error("url:", page.url());
      if (errors.length) console.error("script errors:\n" + errors.join("\n"));
      await browser.close();
      process.exit(1);
    },

    /** finish closes Chrome and fails the run on script errors or contract violations. */
    async finish() {
      await browser.close();
      if (errors.length) {
        console.error("FAIL script errors:\n" + errors.join("\n"));
        process.exit(1);
      }
      if (violations.size) {
        console.error("FAIL API responses or pushes outside the contracts:\n" + [...violations].join("\n"));
        process.exit(1);
      }
      ok("every API response and checked push matched the OpenAPI contracts");
    },
  };
  return t;
}

/**
 * choosePicture puts a picture made in the page (width × height, a
 * gradient under a white disc) into the file input at selector, as if the
 * user had picked it: what the profile pages shrink and upload (design
 * 2026-10-07, avatars and usernames).
 */
export async function choosePicture(page, selector, { width = 900, height = 600, type = "image/png" } = {}) {
  await page.evaluate(
    async (sel, w, h, kind) => {
      const c = document.createElement("canvas");
      c.width = w;
      c.height = h;
      const g = c.getContext("2d");
      const grad = g.createLinearGradient(0, 0, w, h);
      grad.addColorStop(0, "#f0b90b");
      grad.addColorStop(1, "#2563eb");
      g.fillStyle = grad;
      g.fillRect(0, 0, w, h);
      g.fillStyle = "#ffffff";
      g.beginPath();
      g.arc(w / 2, h / 2, Math.min(w, h) / 4, 0, Math.PI * 2);
      g.fill();
      const blob = await new Promise((resolve) => c.toBlob(resolve, kind, 0.9));
      const input = document.querySelector(sel);
      const files = new DataTransfer();
      files.items.add(new File([blob], kind === "image/jpeg" ? "picture.jpg" : "picture.png", { type: kind }));
      input.files = files.files;
      input.dispatchEvent(new Event("change", { bubbles: true }));
    },
    selector,
    width,
    height,
    type,
  );
}

/**
 * APPS_OFFERED is an answer of GET /v1/platform/apps with both kinds the
 * download pages show (design 2026-10-07, App download page): an Android
 * app uploaded in the console and an App Store link for iOS.
 */
export const APPS_OFFERED = {
  android: {
    mode: "FILE", url: "https://astras.vip/downloads/android/0192a000-0000-7000-8000-000000000001.apk", install_url: null, ios_install: null,
    package: "vip.astras.app", version: "1.2.0", build: "42", min_os: "24", size: 50541363, sha256: "9f".repeat(32), mobileconfig_url: null,
    notes: { "zh-CN": "新增价格提醒", en: "Price alerts" }, updated_at: "2026-10-07T03:00:00Z",
  },
  ios: {
    mode: "LINK", url: "https://apps.apple.com/app/id1234567890", install_url: null, ios_install: "APP_STORE", package: null, version: null,
    build: null, min_os: null, size: null, sha256: null, mobileconfig_url: null, notes: { "zh-CN": "", en: "" }, updated_at: "2026-10-07T03:00:00Z",
  },
};

/**
 * APPS_HIDDEN is an answer of GET /v1/platform/apps with the console's
 * download-entry switch off and no app offered (design 2026-10-07, App
 * download page, H5/H6): the sites show no entry, the page still opens.
 */
export const APPS_HIDDEN = { android: null, ios: null, entry: { visible: false } };

/**
 * withApps runs fn while the page's GET /v1/platform/apps answers apps
 * (the download pages' states without changing the server's settings),
 * then lets the network be again.
 */
export function withApps(page, apps, fn) {
  return withAnswer(page, "/v1/platform/apps", apps, fn);
}

/**
 * pickLanguage chooses a language in the settings page's language dropdown
 * (F30): opens it by its name in the page's current language (label), then
 * picks the option by the language's own name - in the dropdown's list, or
 * the searchable list it becomes past six languages (F33).
 */
export async function pickLanguage(page, label, name) {
  const trigger = `main button[aria-label="${label}"]`;
  await page.waitForSelector(trigger, { visible: true, timeout: 20000 });
  await page.click(trigger);
  await page.waitForSelector('[role="option"]', { visible: true, timeout: 10000 });
  for (const option of await page.$$('[role="option"]')) {
    const own = await option.evaluate((o) => (o.querySelector("[lang]")?.textContent ?? o.innerText.split("\n")[0]).trim());
    if (own === name) return option.click();
  }
  throw new Error(`the language dropdown has no ${name}`);
}

/**
 * firstVisitLocale opens the site in a fresh browser profile (no saved
 * settings) whose languages are tags, on device ({viewport, userAgent}),
 * and returns {locale}: the page's language on its first screen (F30,
 * F33: negotiated from the browser's languages, else the platform's
 * fallback language). A first screen in one language that turns into
 * another once the platform's profile is in comes back as "en then
 * zh-CN" (the flash F33 removed): the settled language is read 500 ms
 * after the profile's answer, which must come within 30 s of the first
 * screen (else the visit throws). A first screen that waited for the
 * profile and went on without it (core awaitFallbackLocale's marks) had no
 * fallback language to draw in, so it says nothing either way, and how it
 * went on decides: its 1.5 s ran out, and the answer then came within 10 s
 * (counted from the first screen, each window on its own timer, cleared
 * once it ends): a 2xx one is a slow server ("timeout"), so it tries
 * again, three times in all, then returns {locale: null, gaveUp: [how
 * each ended]} for the caller to note (F34, F37); an error is a failed
 * read ("failed"), and so is the page's own read failing; no answer at
 * all is "unanswered" (the page did not ask, or the server did not
 * answer). A failed or unanswered read is a fault, returned at once with
 * gaveUp ending in it for the caller to fail (F35, F36). A page that did
 * not wait at all comes back as "en then …" when the fallback language is
 * not English (with English as the fallback its first screen is right).
 */
export async function firstVisitLocale(page, app, path, tags, device = {}) {
  const gaveUp = [];
  for (let attempt = 0; attempt < 3; attempt++) {
    const { first, settled, ended } = await openFirstVisit(page, app, path, tags, device);
    if (ended === "failed" || ended === "unanswered") return { locale: null, gaveUp: [...gaveUp, ended] };
    if (ended === "timeout") {
      gaveUp.push(ended);
      continue;
    }
    return { locale: first === settled ? first : `${first} then ${settled}` };
  }
  return { locale: null, gaveUp };
}

/** within is what promise gives within ms, else null (its timer cleared either way). */
async function within(promise, ms) {
  let timer;
  try {
    return await Promise.race([promise.catch(() => null), new Promise((r) => (timer = setTimeout(() => r(null), ms)))]);
  } finally {
    clearTimeout(timer);
  }
}

/**
 * openFirstVisit is one of firstVisitLocale's visits: its first screen's
 * language, the settled one, and how its wait ended (null: no wait;
 * "unanswered": it timed out and no profile answer came within 10 s;
 * "failed" too when the late answer was an error).
 */
async function openFirstVisit(page, app, path, tags, device) {
  const ctx = await page.browser().createBrowserContext();
  try {
    const visit = await ctx.newPage();
    if (device.viewport) await visit.setViewport(device.viewport);
    if (device.userAgent) await visit.setUserAgent(device.userAgent);
    await visit.evaluateOnNewDocument((t) => {
      Object.defineProperty(navigator, "languages", { get: () => t });
      Object.defineProperty(navigator, "language", { get: () => t[0] });
      // The language when React first draws into #root (replacing index.html's
      // static placeholder): watched from the end of parsing, which comes
      // before any module script runs, so the first change there is React's.
      document.addEventListener("readystatechange", () => {
        if (document.readyState !== "interactive") return;
        new MutationObserver((_, observer) => {
          window.__firstScreenLang = document.documentElement.lang;
          observer.disconnect();
        }).observe(document.getElementById("root"), { childList: true });
      });
    }, tags);
    // The profile's answer, however long the page takes to load: each wait below has its own window (F37).
    const profile = visit.waitForResponse((r) => new URL(r.url()).pathname === "/v1/platform/profile", { timeout: 0 });
    profile.catch(() => {}); // awaited below; a failure before that is the one to report
    await visit.goto(app + path, { waitUntil: "domcontentloaded", timeout: 60000 });
    await visit.waitForFunction(() => window.__firstScreenLang !== undefined, { timeout: 30000, polling: 100 });
    // How the first screen's wait ended: marked before the first render.
    const ended = await visit.evaluate(
      () =>
        performance
          .getEntriesByType("mark")
          .map((m) => m.name.match(/^fallback-locale:(read|failed|timeout)$/)?.[1])
          .find(Boolean) ?? null,
    );
    const first = await visit.evaluate(() => window.__firstScreenLang);
    if (ended === "failed") return { first, settled: null, ended }; // nothing to settle
    if (ended === "timeout") {
      // Late (a slow server), late with an error (a failed read), or never (the page did not ask, or no answer).
      const answer = await within(profile, 10000);
      return { first, settled: null, ended: !answer ? "unanswered" : answer.ok() ? "timeout" : "failed" };
    }
    if (!(await within(profile, 30000))) throw new Error("no answer for the platform's profile within 30 s");
    await new Promise((r) => setTimeout(r, 500)); // the effects that follow the profile
    return { first, settled: await visit.evaluate(() => document.documentElement.lang), ended };
  } finally {
    await ctx.close();
  }
}

/**
 * indicatorsAway lists the sliding indicators under scope (a Segmented's
 * thumb, a Tabs' underline or pill: motion layoutId spans) that are not on
 * their item: a thumb or pill covers it, an underline lies along its foot.
 * Read right after something is put in above them, it is empty only when
 * they move with their items instead of sliding after them (F40).
 */
export function indicatorsAway(page, scope) {
  return page.evaluate((scope) => {
    const away = [];
    for (const span of document.querySelectorAll(`${scope} span[aria-hidden].-z-10`)) {
      const item = span.parentElement;
      const r = span.getBoundingClientRect();
      const p = item.getBoundingClientRect();
      if (!p.width) continue;
      const dx = Math.round(r.left - p.left);
      const dy = Math.round(r.height <= 3 ? r.bottom - p.bottom : r.top - p.top);
      if (Math.abs(dx) > 1 || Math.abs(dy) > 1) away.push(`${item.innerText.trim().split("\n")[0]}: ${dx} px across, ${dy} px down`);
    }
    return away;
  }, scope);
}

/**
 * noteRegistered adds an account this run signed up to $E2E_REGISTERED,
 * the file the shell scripts' exit hook clears out (scripts/e2e/lib/
 * common.sh: their accounts are named for the run, E2E_RUN, and marked
 * TEST by that; cleared out by these IDs). Run by hand, without the file,
 * it does nothing (F38).
 */
export function noteRegistered(userId) {
  const file = process.env.E2E_REGISTERED;
  if (file && userId) appendFileSync(file, `${userId}\n`);
}

/** note prints what a run could not check here, without failing it. */
export const note = (what) => console.log("note " + what);

/**
 * decodeQr reads the QR codes an element shows, left to right, from one
 * screenshot of it, with Chrome's BarcodeDetector (the download QR codes
 * carry a logo, F25: what matters is that they still read as their links).
 * null when this Chrome cannot read QR codes (no BarcodeDetector, or one
 * without qr_code: it is on macOS, not on every platform's Chrome): the
 * caller notes it and goes on (F29). A missing element fails either way.
 */
export async function decodeQr(page, element) {
  if (!element) throw new Error("decodeQr: no such element on the page");
  const can = await page.evaluate(async () => "BarcodeDetector" in window && (await BarcodeDetector.getSupportedFormats()).includes("qr_code"));
  if (!can) return null; // before the screenshot, which would be for nothing (F31)
  const png = await element.screenshot({ encoding: "base64" });
  return page.evaluate(async (b64) => {
    const bytes = Uint8Array.from(atob(b64), (c) => c.charCodeAt(0));
    const bitmap = await createImageBitmap(new Blob([bytes], { type: "image/png" }));
    const codes = await new BarcodeDetector({ formats: ["qr_code"] }).detect(bitmap);
    return codes.sort((a, b) => a.boundingBox.x - b.boundingBox.x).map((c) => c.rawValue);
  }, png);
}

/**
 * PRODUCTS_PAUSED is an answer of GET /v1/platform/products with spot and
 * the USDT-margined contracts closed and the coin-margined ones open
 * (design 2026-10-07, product line switches §1 #2): the sites hide the
 * closed lines and their terminals say so, and a user's futures USDT (the
 * smokes' transfer of step 5) is left to wind down.
 */
export const PRODUCTS_PAUSED = {
  spot: { enabled: false, closed_at: "2026-10-07T08:00:00Z" },
  usdt_m: { enabled: false, closed_at: "2026-10-07T08:00:00Z" },
  coin_m: { enabled: true },
};

/**
 * withProducts runs fn while the page's GET /v1/platform/products answers
 * products (the product lines' switches without changing the server's).
 * The sites read them as they start: go to a page inside fn.
 */
export function withProducts(page, products, fn) {
  return withAnswer(page, "/v1/platform/products", products, fn);
}

/**
 * ACCOUNT_LIQUIDATING is an answer of GET /v1/derivatives/account: a USDT
 * futures account whose cross positions are being liquidated (C68, F24),
 * nothing transferable meanwhile.
 */
export const ACCOUNT_LIQUIDATING = {
  asset: "USDT", wallet_balance: "100", available: "100", frozen: "0", order_margin: "0", position_margin: "0", unrealized_pnl: "0",
  cross_unrealized_pnl: "0", margin_balance: "100", transferable: "0", liquidating: true,
};

/**
 * withFuturesAccount runs fn while the page's GET /v1/derivatives/account
 * answers account (whatever the asset asked for): a state of the futures
 * account the smokes cannot bring about, such as a liquidation.
 */
export function withFuturesAccount(page, account, fn) {
  return withAnswer(page, "/v1/derivatives/account", account, fn);
}

// withAnswer runs fn while the page's requests of path answer body, then
// lets the network be again.
async function withAnswer(page, path, body, fn) {
  const answer = (req) => {
    if (req.isInterceptResolutionHandled()) return;
    if (new URL(req.url()).pathname === path) {
      void req.respond({ status: 200, contentType: "application/json", body: JSON.stringify(body) });
    } else {
      void req.continue();
    }
  };
  await page.setRequestInterception(true);
  page.on("request", answer);
  try {
    await fn();
  } finally {
    page.off("request", answer);
    await page.setRequestInterception(false);
  }
}

/**
 * menuOnTop scrolls the PC site's page halfway down, requires the table's
 * header to be stuck right under the top bar (else there is nothing to
 * cover the menu and the check would prove nothing: a short page, review
 * BP), hovers the top bar's menu named label and waits until the menu's
 * first item is the element at that item's centre: the top bar's layer is
 * above the page's sticky ones (review B61). It names what covers the item
 * when it is not, and leaves the page at its top. Use it on a long table
 * (the markets).
 */
export async function menuOnTop(page, label) {
  await page.waitForSelector("main table tbody tr", { timeout: 20000 });
  const gap = await page.evaluate(async () => {
    window.scrollTo(0, document.documentElement.scrollHeight / 2);
    await new Promise((r) => setTimeout(r, 300));
    const cell = document.querySelector("main table thead th");
    const bar = document.querySelector("header");
    return cell && bar ? Math.round(cell.getBoundingClientRect().top - bar.getBoundingClientRect().bottom) : null;
  });
  if (gap === null || Math.abs(gap) > 2) {
    await page.evaluate(() => window.scrollTo(0, 0));
    throw new Error(`the table's header is not stuck under the top bar (${gap}px off), so nothing would cover the ${label} menu`);
  }
  const trigger = (
    await page.evaluateHandle((l) => [...document.querySelectorAll("header nav .group > a")].find((a) => a.textContent.trim() === l) ?? null, label)
  ).asElement();
  if (!trigger) throw new Error(`no ${label} menu in the top bar`);
  await trigger.hover();
  const covered = await page
    .waitForFunction(
      (l) => {
        const a = [...document.querySelectorAll("header nav .group > a")].find((x) => x.textContent.trim() === l);
        const panel = a?.nextElementSibling;
        const first = panel?.querySelector("a");
        if (!first || getComputedStyle(panel).opacity !== "1") return false;
        const r = first.getBoundingClientRect();
        const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
        return hit !== null && first.contains(hit);
      },
      { timeout: 5000 },
      label,
    )
    .then(() => "")
    .catch(() =>
      page.evaluate((l) => {
        const a = [...document.querySelectorAll("header nav .group > a")].find((x) => x.textContent.trim() === l);
        const first = a?.nextElementSibling?.querySelector("a");
        if (!first) return "nothing: the menu has no item";
        const r = first.getBoundingClientRect();
        const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
        return hit ? `${hit.tagName.toLowerCase()} "${hit.textContent.trim().slice(0, 20)}"` : "nothing at that point";
      }, label),
    );
  await page.mouse.move(0, 700);
  await page.evaluate(() => window.scrollTo(0, 0));
  if (covered) throw new Error(`the ${label} menu's first item is under ${covered}`);
}

/**
 * legendClear waits (20 s at most) for the candle chart within scope to
 * have drawn its candles, then for its highest candle to start below its
 * legend (5 s more at most, for the chart to redraw with the room it
 * leaves), and tells, in page pixels, where the legend ends and where the
 * highest candle starts, with the share of the pane left for the legend
 * (the plot's data-legend-room) and whether the candle is clear. The
 * legend sits over the plot and the candles' scale leaves room for it
 * (B116); the highest candle is the topmost row of the chart's pane with
 * a pixel in the rise or fall colour, as drawn on its canvas (the
 * crosshair is on another).
 */
export async function legendClear(page, scope = "") {
  const measure = () =>
    page.evaluate((scope) => {
      const plot = document.querySelector(`${scope} [data-testid="candle-plot"]`);
      const legend = document.querySelector(`${scope} [data-testid="candle-legend"]`);
      if (!plot || !legend || legend.offsetHeight === 0) return null;
      const canvases = [...plot.querySelectorAll("canvas")];
      if (canvases.length === 0) return null;
      // The pane's canvas is the largest; its crosshair layer, as large, comes after it.
      const pane = canvases.reduce((a, c) => (c.width * c.height > a.width * a.height ? c : a));
      const probe = document.createElement("canvas").getContext("2d");
      const rgb = (css) => {
        probe.clearRect(0, 0, 1, 1);
        probe.fillStyle = css;
        probe.fillRect(0, 0, 1, 1);
        return [...probe.getImageData(0, 0, 1, 1).data.slice(0, 3)];
      };
      const root = getComputedStyle(document.documentElement);
      const colours = ["--up", "--down"].map((v) => rgb(root.getPropertyValue(v).trim()));
      const box = pane.getBoundingClientRect();
      const data = pane.getContext("2d").getImageData(0, 0, pane.width, pane.height).data;
      for (let y = 0; y < pane.height; y++) {
        for (let x = 0; x < pane.width; x++) {
          const i = (y * pane.width + x) * 4;
          if (colours.some(([r, g, b]) => Math.abs(data[i] - r) + Math.abs(data[i + 1] - g) + Math.abs(data[i + 2] - b) < 24)) {
            const legendBottom = Math.round(legend.getBoundingClientRect().bottom);
            const candleTop = Math.round(box.top + (y * box.height) / pane.height);
            return { legendBottom, candleTop, clear: candleTop >= legendBottom, room: plot.dataset.legendRoom ?? "", legendHeight: legend.offsetHeight };
          }
        }
      }
      return null;
    }, scope);
  let last = null;
  for (const end = Date.now() + 20000; !last && Date.now() < end; await sleep(250)) last = await measure();
  if (!last) throw new Error("the candle chart drew no candles within 20 s");
  for (const end = Date.now() + 5000; !last.clear && Date.now() < end; await sleep(250)) last = (await measure()) ?? last;
  return last;
}
