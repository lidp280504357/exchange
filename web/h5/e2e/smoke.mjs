// Browser smoke test of the H5, run by scripts/e2e/h5.sh (task e2e):
//
//   APP=https://astras.vip node web/h5/e2e/smoke.mjs
//
// It registers a user over the API (with the environment's
// CAPTCHA_BYPASS_TOKEN, from the environment or the repository .env), then
// drives the H5 in headless Chrome at phone size: sign-in, balances, a
// transfer, a live WebSocket update, markets, notifications, sessions,
// session restore on reload and sign-out. APP is the H5 (the deployed site
// or a dev server on http://localhost:5173); API defaults to APP, except
// for a dev server, whose API is the test environment. Chrome comes from
// CHROME or the usual install paths. Screenshots go to SHOTS when set.
import { existsSync, mkdirSync, readFileSync } from "node:fs";
import puppeteer from "puppeteer-core";

const APP = process.env.APP ?? "https://astras.vip";
const API = process.env.API ?? (APP.startsWith("http://localhost") ? "https://astras.vip" : APP);
const SHOTS = process.env.SHOTS ?? "";
const CHROME =
  process.env.CHROME ??
  [
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/usr/bin/google-chrome",
    "/usr/bin/chromium",
    "/usr/bin/chromium-browser",
  ].find((p) => existsSync(p));
if (!CHROME) {
  console.log("SKIP browser checks: no Chrome found (set CHROME)");
  process.exit(0);
}

function bypassToken() {
  if (process.env.CAPTCHA_BYPASS_TOKEN) return process.env.CAPTCHA_BYPASS_TOKEN;
  const env = new URL("../../../.env", import.meta.url);
  if (!existsSync(env)) throw new Error("CAPTCHA_BYPASS_TOKEN is not set and there is no .env");
  return readFileSync(env, "utf8").match(/^CAPTCHA_BYPASS_TOKEN="?([^"\n]*)"?$/m)?.[1];
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const ok = (what) => console.log("ok   " + what);
async function api(method, path, body, headers = {}) {
  const r = await fetch(API + path, { method, headers: { "Content-Type": "application/json", ...headers }, body: body && JSON.stringify(body) });
  const text = await r.text();
  let json;
  try {
    json = JSON.parse(text);
  } catch {
    json = text;
  }
  return { status: r.status, json };
}

// 1. A fresh user, created over the API.
const run = Date.now();
const email = `e2e-h5-${run}@example.com`;
const password = `e2e h5 ${run}`;
const device = `e2e-h5-${run}`;
const ch = await api("POST", "/v1/auth/otp/request", { scene: "REGISTER", channel: "EMAIL", identifier: email, captcha_token: bypassToken(), device_id: device });
if (ch.status !== 200) throw new Error("otp request: " + JSON.stringify(ch));
let code;
for (let i = 0; i < 20 && !code; i++) {
  await sleep(500);
  const m = await api("GET", `/v1/dev/messages?target=${encodeURIComponent(email)}`);
  code = m.json?.messages?.[0]?.subject?.match(/\d{6}/)?.[0];
}
const v = await api("POST", "/v1/auth/otp/verify", { challenge_id: ch.json.challenge_id, code, device_id: device });
const terms = (await api("GET", "/v1/auth/terms")).json;
const reg = await api(
  "POST",
  "/v1/auth/register/complete",
  // Notices render in the profile's language, so match the English UI.
  { otp_ticket: v.json.otp_ticket, password, country: "SG", language: "en", terms_version: terms.terms_version, risk_disclosure_version: terms.risk_disclosure_version, device_id: device },
  { "X-Client-Type": "APP" },
);
if (reg.status !== 201) throw new Error("register: " + JSON.stringify(reg));
const appToken = reg.json.access_token;
ok(`registered ${email}`);

// 2. The browser, sized like a phone.
if (SHOTS) mkdirSync(SHOTS, { recursive: true });
const browser = await puppeteer.launch({ executablePath: CHROME, headless: true, args: ["--no-first-run"] });
const errors = [];
try {
  const page = await browser.newPage();
  await page.setViewport({ width: 390, height: 844, deviceScaleFactor: 2, isMobile: true, hasTouch: true });
  // The labels below are English; the app otherwise follows the browser.
  await page.evaluateOnNewDocument(() => localStorage.setItem("exchange.lang", "en"));
  page.on("pageerror", (e) => errors.push("pageerror: " + e.message));
  // Chrome logs every 4xx fetch as an error; expected API errors (a wrong
  // password) are asserted below, so only script errors count here.
  page.on("console", (m) => {
    if (m.type() === "error" && !m.text().startsWith("Failed to load resource")) errors.push("console: " + m.text());
  });
  const requests = [];
  page.on("request", (r) => {
    if (r.url().includes("/v1/")) requests.push(new URL(r.url()).pathname);
  });
  const shot = (name) => (SHOTS ? page.screenshot({ path: `${SHOTS}/${name}.png`, fullPage: true }) : undefined);
  const waitText = (s, timeout = 20000) => page.waitForFunction((t) => document.body.innerText.includes(t), { timeout }, s);
  // clickText clicks the visible element with that label, waiting up to
  // 10 s for it to render.
  const clickText = async (selector, label) => {
    for (let i = 0; i < 40; i++) {
      for (const h of await page.$$(selector)) {
        if ((await h.evaluate((el) => el.innerText.trim())) === label && (await h.isVisible())) return h.click();
      }
      await sleep(250);
    }
    throw new Error(`no visible ${selector} "${label}"`);
  };
  const typeInto = async (selector, value) => {
    await page.click(selector, { clickCount: 3 });
    await page.keyboard.press("Backspace");
    await page.type(selector, value);
  };

  await page.goto(APP + "/", { waitUntil: "networkidle0" });
  await page.waitForSelector('input[autocomplete="username"]');
  if (new URL(page.url()).pathname !== "/login") throw new Error("anonymous visit should land on /login: " + page.url());
  if (requests.includes("/v1/auth/token/refresh")) throw new Error("an anonymous visit should not try to refresh");
  ok("anonymous visit redirects to the login page without a refresh call");
  await shot("1-login");

  await typeInto('input[autocomplete="username"]', email);
  await typeInto('input[autocomplete="current-password"]', "wrong password!!");
  await clickText("form button", "Sign in");
  await waitText("Wrong account or password");
  ok("a wrong password shows the localized message of its error code");
  await typeInto('input[autocomplete="current-password"]', password);
  await clickText("form button", "Sign in");
  await page.waitForFunction(() => location.pathname === "/", { timeout: 15000 });
  await waitText("10,000", 30000);
  await waitText("Adjustment (simulated funds)", 15000);
  ok("signed in; assets and the fund flow show the 10,000 USDT welcome funds");
  await shot("2-assets");

  await clickText("nav a", "Transfer");
  await page.waitForSelector('input[inputmode="decimal"]');
  await typeInto('input[inputmode="decimal"]', "0.0000001");
  await waitText("At most 6 decimals");
  ok("the form rejects amounts beyond the asset precision");
  await typeInto('input[inputmode="decimal"]', "123.45");
  await clickText("form button", "Confirm");
  await waitText("Transferred");
  await waitText("Completed");
  ok("a transfer of 123.45 USDT completes through the form and shows in the history");
  await shot("3-transfer");

  await clickText("nav a", "Assets");
  await clickText("button", "Futures");
  await waitText("123.45");
  // A change made elsewhere (the APP session) must arrive over the WebSocket.
  const t2 = await api(
    "POST",
    "/v1/account/transfers",
    { asset: "USDT", amount: "1", from_account_type: "SPOT", to_account_type: "FUTURES" },
    { Authorization: `Bearer ${appToken}`, "Idempotency-Key": `h5-ws-${run}` },
  );
  if (t2.status !== 201) throw new Error("api transfer: " + JSON.stringify(t2));
  await waitText("124.45", 15000);
  ok("a balance change made elsewhere appears live (WebSocket push)");
  await shot("4-futures-live");

  await clickText("nav a", "Markets");
  await waitText("BTC/USDT");
  ok("markets list the seeded pairs");
  await shot("5-markets");

  await clickText("nav a", "Notices");
  await waitText("Welcome to Exchange");
  ok("notifications show the welcome notice");
  await shot("6-notifications");

  await clickText("nav a", "Security");
  await waitText("This device");
  ok("security lists the sessions with the current device marked");
  await shot("7-security");

  await page.reload({ waitUntil: "networkidle0" });
  await waitText("This device", 15000);
  ok("a reload restores the session from the refresh cookie");

  await clickText("header a", "Settings");
  await waitText("Anti-phishing code");
  ok("settings are reachable on a phone");
  await shot("8-settings");

  await clickText("header button", "Sign out");
  await page.waitForFunction(() => location.pathname === "/login", { timeout: 10000 });
  await page.reload({ waitUntil: "networkidle0" });
  await page.waitForSelector('input[autocomplete="username"]');
  ok("sign out ends the session; a reload stays signed out");
} finally {
  await browser.close();
}
if (errors.length) {
  console.log("FAIL browser errors:\n  " + errors.join("\n  "));
  process.exit(1);
}
console.log("all browser checks passed");
