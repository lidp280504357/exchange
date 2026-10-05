// Shared plumbing of the browser smoke tests (pc-smoke.mjs, m-smoke.mjs,
// admin-smoke.mjs):
// Chrome, the human-check bypass, the dev inbox, the contract check of
// every API response and of the pushes of the channels in PUSH_SCHEMAS,
// and helpers that find elements the way a user does, by their visible
// text.
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync } from "node:fs";
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
  // frames, each checked against its schema.
  const pushes = [];
  const cdp = await page.createCDPSession();
  await cdp.send("Network.enable");
  cdp.on("Network.webSocketFrameReceived", ({ response }) => {
    let m;
    try {
      m = JSON.parse(response.payloadData);
    } catch {
      return;
    }
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
    if (typeof body?.user_id === "string" && typeof body?.access_token === "string") userId = body.user_id;
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
     * openMargin opens margin trading for the signed-in user alone through
     * web.sh's helper (scripts/e2e/lib/margin-user.sh, put back when web.sh
     * ends) and reports whether it did: run on their own, the smokes leave
     * the switch as it is. The services see it within 5 seconds.
     */
    async openMargin() {
      const helper = process.env.MARGIN_USER_HELPER;
      if (!helper || !userId) return false;
      execFileSync("bash", [helper, "on", userId], { stdio: "inherit" });
      await sleep(6000);
      return true;
    },
    shot: (label) => (shots ? page.screenshot({ path: `${shots}/${name}-${label}.png`, fullPage: false }) : undefined),
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
