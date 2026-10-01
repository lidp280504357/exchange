// Shared plumbing of the browser smoke tests (pc-smoke.mjs, m-smoke.mjs,
// admin-smoke.mjs):
// Chrome, the human-check bypass, the dev inbox, the contract check of
// every API response, and helpers that find elements the way a user does,
// by their visible text.
import { existsSync, mkdirSync, readFileSync } from "node:fs";
import puppeteer from "puppeteer-core";
import { loadContracts } from "./contract.mjs";

export const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
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
  // Chrome logs every 4xx fetch as an error; expected API errors are
  // asserted by the scripts, so only script errors count here.
  page.on("console", (m) => {
    // Cloudflare injects its analytics beacon into every page; the admin
    // console's CSP (default-src 'self') blocks it, which is the point.
    if (m.type() === "error" && !m.text().startsWith("Failed to load resource") && !m.text().includes("cloudflareinsights.com")) {
      errors.push("console: " + m.text());
    }
  });
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
    shot: (label) => (shots ? page.screenshot({ path: `${shots}/${name}-${label}.png`, fullPage: false }) : undefined),
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
        console.error("FAIL API responses outside the contracts:\n" + [...violations].join("\n"));
        process.exit(1);
      }
      ok("every API response matched the OpenAPI contracts");
    },
  };
  return t;
}
