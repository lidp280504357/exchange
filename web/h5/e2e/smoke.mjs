// Browser smoke test of the H5, run by scripts/e2e/h5.sh (task e2e):
//
//   APP=https://astras.vip node web/h5/e2e/smoke.mjs
//
// Registers a user over the API (with the environment's
// CAPTCHA_BYPASS_TOKEN, from the environment or the repository .env), then
// drives the H5 in headless Chrome: at phone size sign-in, balances, a
// transfer, a live WebSocket update, markets, notifications, sessions,
// session restore on reload and sign-out; at desktop size the same
// account's assets, markets and transfer form; and sign-up through the
// form. Every API response, from the page or from the test itself, is
// checked against the OpenAPI contracts (contract.mjs). APP is the H5 (the
// deployed site or a dev server on http://localhost:5173); API defaults to
// APP, except for a dev server, whose API is the test environment. Chrome
// comes from CHROME or the usual install paths. Screenshots go to SHOTS
// when set.
import { existsSync, mkdirSync, readFileSync } from "node:fs";
import puppeteer from "puppeteer-core";
import { loadContracts } from "./contract.mjs";

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

const bypass = bypassToken();
const contracts = loadContracts();
const violations = new Set();
const errors = [];
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const ok = (what) => console.log("ok   " + what);

function checkContract(method, url, status, body) {
  const path = new URL(url).pathname;
  if (!path.startsWith("/v1/") || path.startsWith("/v1/dev/") || path === "/v1/ws") return;
  const problem = contracts.check(method, path, status, body);
  if (problem) violations.add(problem);
}

async function api(method, path, body, headers = {}) {
  const r = await fetch(API + path, { method, headers: { "Content-Type": "application/json", ...headers }, body: body && JSON.stringify(body) });
  const text = await r.text();
  let json;
  try {
    json = text ? JSON.parse(text) : undefined;
  } catch {
    json = text;
  }
  checkContract(method, API + path, r.status, json);
  return { status: r.status, json };
}

async function readCode(target) {
  for (let i = 0; i < 20; i++) {
    await sleep(500);
    const m = await api("GET", `/v1/dev/messages?target=${encodeURIComponent(target)}`);
    const code = m.json?.messages?.[0]?.subject?.match(/\d{6}/)?.[0];
    if (code) return code;
  }
  throw new Error("no code arrived for " + target);
}

// 1. A fresh user, created over the API.
const run = Date.now();
const email = `e2e-h5-${run}@example.com`;
const password = `e2e h5 ${run}`;
const device = `e2e-h5-${run}`;
const ch = await api("POST", "/v1/auth/otp/request", { scene: "REGISTER", channel: "EMAIL", identifier: email, captcha_token: bypass, device_id: device });
if (ch.status !== 200) throw new Error("otp request: " + JSON.stringify(ch));
const code = await readCode(email);
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

// 2. The browser.
if (SHOTS) mkdirSync(SHOTS, { recursive: true });
const browser = await puppeteer.launch({ executablePath: CHROME, headless: true, args: ["--no-first-run"] });

// preparePage opens a page in its own context (cookies, storage), in
// English, reporting script errors and checking API responses.
async function preparePage({ mobile, captcha = "" }) {
  const context = await browser.createBrowserContext();
  const page = await context.newPage();
  await page.setViewport(mobile ? { width: 390, height: 844, deviceScaleFactor: 2, isMobile: true, hasTouch: true } : { width: 1280, height: 800 });
  await page.evaluateOnNewDocument((token) => {
    localStorage.setItem("exchange.lang", "en");
    if (token) window.__E2E_CAPTCHA_TOKEN__ = token;
  }, captcha);
  page.on("pageerror", (e) => errors.push("pageerror: " + e.message));
  // Chrome logs every 4xx fetch as an error; expected API errors (a wrong
  // password) are asserted below, so only script errors count here.
  page.on("console", (m) => {
    if (m.type() === "error" && !m.text().startsWith("Failed to load resource")) errors.push("console: " + m.text());
  });
  page.requests = [];
  page.on("request", (r) => {
    if (r.url().includes("/v1/")) page.requests.push(new URL(r.url()).pathname);
  });
  page.on("response", async (r) => {
    const type = r.request().resourceType();
    if ((type !== "fetch" && type !== "xhr") || !r.url().includes("/v1/")) return;
    let body;
    try {
      const text = r.status() === 204 ? "" : await r.text();
      body = text ? JSON.parse(text) : undefined;
    } catch {
      return; // body unavailable (navigated away)
    }
    checkContract(r.request().method(), r.url(), r.status(), body);
  });
  return page;
}

const shot = (page, name) => (SHOTS ? page.screenshot({ path: `${SHOTS}/${name}.png`, fullPage: true }) : undefined);
const waitText = (page, s, timeout = 20000) => page.waitForFunction((t) => document.body.innerText.includes(t), { timeout }, s);
// clickText clicks the visible element with that label, waiting up to 10 s
// for it to render (and to become enabled).
async function clickText(page, selector, label) {
  for (let i = 0; i < 40; i++) {
    for (const h of await page.$$(selector)) {
      const [text, disabled] = await h.evaluate((el) => [el.innerText.trim(), el.disabled === true]);
      if (text === label && !disabled && (await h.isVisible())) {
        await h.evaluate((el) => el.scrollIntoView({ block: "center" })); // clear of the fixed navigation
        return h.click();
      }
    }
    await sleep(250);
  }
  throw new Error(`no visible, enabled ${selector} "${label}"`);
}
async function typeInto(page, selector, value) {
  await page.waitForSelector(selector, { visible: true });
  // Centered, so the fixed bottom navigation of the phone layout cannot
  // take the click.
  await page.$eval(selector, (el) => el.scrollIntoView({ block: "center" }));
  await page.click(selector, { clickCount: 3 });
  await page.keyboard.press("Backspace");
  await page.type(selector, value);
}

try {
  // Phone.
  const page = await preparePage({ mobile: true });
  await page.goto(APP + "/", { waitUntil: "networkidle0" });
  await page.waitForSelector('input[autocomplete="username"]');
  if (new URL(page.url()).pathname !== "/login") throw new Error("anonymous visit should land on /login: " + page.url());
  if (page.requests.includes("/v1/auth/token/refresh")) throw new Error("an anonymous visit should not try to refresh");
  ok("anonymous visit redirects to the login page without a refresh call");
  await shot(page, "1-login");

  await typeInto(page, 'input[autocomplete="username"]', email);
  await typeInto(page, 'input[autocomplete="current-password"]', "wrong password!!");
  await clickText(page, "form button", "Sign in");
  await waitText(page, "Wrong account or password");
  ok("a wrong password shows the localized message of its error code");
  await typeInto(page, 'input[autocomplete="current-password"]', password);
  await clickText(page, "form button", "Sign in");
  await page.waitForFunction(() => location.pathname === "/", { timeout: 15000 });
  await waitText(page, "10,000", 30000);
  await waitText(page, "Adjustment (simulated funds)", 15000);
  ok("signed in; assets and the fund flow show the 10,000 USDT welcome funds");
  await shot(page, "2-assets");

  await clickText(page, "nav a", "Transfer");
  await typeInto(page, 'input[inputmode="decimal"]', "0.0000001");
  await waitText(page, "At most 6 decimals");
  ok("the form rejects amounts beyond the asset precision");
  await typeInto(page, 'input[inputmode="decimal"]', "123.45");
  await clickText(page, "form button", "Confirm");
  await waitText(page, "Transferred");
  await waitText(page, "Completed");
  ok("a transfer of 123.45 USDT completes through the form and shows in the history");
  await shot(page, "3-transfer");

  await clickText(page, "nav a", "Assets");
  await clickText(page, "button", "Futures");
  await waitText(page, "123.45");
  // A change made elsewhere (the APP session) must arrive over the WebSocket.
  const t2 = await api(
    "POST",
    "/v1/account/transfers",
    { asset: "USDT", amount: "1", from_account_type: "SPOT", to_account_type: "FUTURES" },
    { Authorization: `Bearer ${appToken}`, "Idempotency-Key": `h5-ws-${run}` },
  );
  if (t2.status !== 201) throw new Error("api transfer: " + JSON.stringify(t2));
  await waitText(page, "124.45", 15000);
  ok("a balance change made elsewhere appears live (WebSocket push)");
  await shot(page, "4-futures-live");

  // Deposits: the page assigns this user's Sepolia address.
  await clickText(page, "a button", "Deposit");
  await page.waitForFunction(() => /^0x[0-9a-fA-F]{40}$/.test(document.querySelector('[data-testid="deposit-address"]')?.innerText ?? ""), { timeout: 15000 });
  await waitText(page, "After 12 block confirmations");
  await waitText(page, "Deposit history");
  ok("the deposit page shows the user's ETH-SEPOLIA address and the deposit rules");
  await shot(page, "4b-deposit");

  await clickText(page, "nav a", "Assets");
  await clickText(page, "a button", "Withdraw");
  await waitText(page, "Address book");
  await waitText(page, "Withdrawal history");
  await waitText(page, "Minimum 0.001, fee 0.0002 ETH");
  ok("the withdraw page shows the rules, the address book and the history");
  await shot(page, "4c-withdraw");

  await clickText(page, "nav a", "Markets");
  await waitText(page, "BTC/USDT");
  ok("markets list the seeded pairs");
  await shot(page, "5-markets");

  // Trading on ETH-BTC (no market maker): a limit buy below the market
  // rests, shows in the book and the open orders, and goes away when
  // canceled. 0.03017 stays clear of the prices of scripts/e2e/matching.sh
  // and marketdata.sh.
  await clickText(page, "td a", "ETH/BTC");
  await waitText(page, "Order book");
  await page.waitForSelector('[data-testid="chart"] canvas', { timeout: 15000 });
  await typeInto(page, 'input[name="price"]', "0.03017");
  await typeInto(page, 'input[name="quantity"]', "0.1");
  await waitText(page, "Total: 0.003017 BTC");
  await clickText(page, "form button", "Buy ETH");
  await waitText(page, "Order placed");
  await page.waitForFunction(() => document.querySelector('[data-testid="bids"]')?.innerText.includes("0.03017"), { timeout: 15000 });
  await page.waitForFunction(() => document.querySelector('[data-testid="orders-open"]')?.innerText.includes("Open"), { timeout: 15000 });
  ok("a limit buy placed through the form rests in the book and the open orders");
  await shot(page, "5b-trade");
  await clickText(page, '[data-testid="orders-open"] button', "Cancel");
  await page.waitForFunction(() => !document.querySelector('[data-testid="bids"]')?.innerText.includes("0.03017"), { timeout: 15000 });
  await page.waitForFunction(() => document.querySelector('[data-testid="orders-open"]')?.querySelectorAll("tbody tr").length === 0, { timeout: 15000 });
  ok("canceling it clears the book and the open orders (engine and WebSocket)");

  await clickText(page, "nav a", "Notices");
  await waitText(page, "Welcome to Exchange");
  ok("notifications show the welcome notice");
  await shot(page, "6-notifications");

  await clickText(page, "nav a", "Security");
  await waitText(page, "This device");
  ok("security lists the sessions with the current device marked");
  await shot(page, "7-security");

  await page.reload({ waitUntil: "networkidle0" });
  await waitText(page, "This device", 15000);
  ok("a reload restores the session from the refresh cookie");

  await clickText(page, "header a", "Settings");
  await waitText(page, "Anti-phishing code");
  ok("settings are reachable on a phone");
  await shot(page, "8-settings");

  await clickText(page, "header button", "Sign out");
  await page.waitForFunction(() => location.pathname === "/login", { timeout: 10000 });
  await page.reload({ waitUntil: "networkidle0" });
  await page.waitForSelector('input[autocomplete="username"]');
  ok("sign out ends the session; a reload stays signed out");

  // Desktop, same account.
  const desk = await preparePage({ mobile: false });
  await desk.goto(APP + "/login", { waitUntil: "networkidle0" });
  await typeInto(desk, 'input[autocomplete="username"]', email);
  await typeInto(desk, 'input[autocomplete="current-password"]', password);
  await clickText(desk, "form button", "Sign in");
  await waitText(desk, "9,875.55", 15000);
  await clickText(desk, "header a", "Markets");
  await waitText(desk, "ETH/USDT");
  await clickText(desk, "header a", "Transfer");
  await waitText(desk, "Max");
  await desk.waitForFunction(() => document.body.innerText.split("Completed").length - 1 >= 2, { timeout: 15000 });
  ok("at desktop size: balances after both transfers, markets, the transfer form and its history");
  await shot(desk, "9-desktop");

  // Sign-up through the form.
  const signup = await preparePage({ mobile: true, captcha: bypass });
  const newEmail = `e2e-h5-ui-${run}@example.com`;
  await signup.goto(APP + "/register", { waitUntil: "networkidle0" });
  await typeInto(signup, 'input[type="email"]', newEmail);
  await clickText(signup, "button", "Send code");
  await typeInto(signup, 'input[autocomplete="one-time-code"]', await readCode(newEmail));
  await clickText(signup, "button", "Confirm");
  await typeInto(signup, 'input[autocomplete="new-password"]', `e2e h5 ui ${run}`);
  // The terms checkbox appears once the current versions have loaded.
  await signup.waitForSelector('input[type="checkbox"]', { visible: true });
  await signup.click('input[type="checkbox"]');
  await clickText(signup, "form button", "Sign up");
  await signup.waitForFunction(() => location.pathname === "/", { timeout: 15000 });
  await waitText(signup, "10,000", 30000);
  ok("sign-up through the form (code, password, terms) lands on the assets with the welcome funds");
  await shot(signup, "10-signup");

  // The API reference is built with the site (not by the dev server). Redoc
  // only runs when its pinned hash matches, so rendering proves both.
  if (!APP.startsWith("http://localhost")) {
    const docs = await preparePage({ mobile: false });
    await docs.goto(APP + "/docs/", { waitUntil: "networkidle0" });
    await waitText(docs, "Idempotency", 30000);
    await waitText(docs, "Move funds between the SPOT and FUTURES accounts");
    await waitText(docs, "Send a one-time code");
    ok("the API reference at /docs/ renders every service's operations");
    await shot(docs, "11-docs");
  }
} finally {
  await browser.close();
}

let failed = false;
if (violations.size) {
  console.log("FAIL responses that do not match the OpenAPI contracts:\n  " + [...violations].join("\n  "));
  failed = true;
} else {
  ok("every API response matched the OpenAPI contracts");
}
if (errors.length) {
  console.log("FAIL browser errors:\n  " + errors.join("\n  "));
  failed = true;
}
if (failed) process.exit(1);
console.log("all browser checks passed");
