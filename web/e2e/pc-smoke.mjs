// Browser smoke test of the PC site (design 2026-09-30 §6, §12.2), run by
// scripts/e2e/web.sh (task e2e):
//
//   APP=https://astras.vip node web/e2e/pc-smoke.mjs
//
// Drives the deployed site in headless Chrome, in Chinese, the way a new
// user would: sign up through the form (the human check passes with the
// environment's CAPTCHA_BYPASS_TOKEN, the code comes from the dev inbox),
// the welcome funds on the assets page, sign out and back in, the market
// list and its search, a limit order placed from the spot terminal and
// cancelled from its open orders, a transfer to futures and its ledger
// entry, a deposit address, the futures terminal, notifications, devices,
// the language switch and sign-out. Script errors fail the run; every API
// response is checked against the OpenAPI contracts. Chrome comes from
// CHROME or the usual install paths; screenshots go to SHOTS when set.
import { ok, start } from "./lib.mjs";

const APP = (process.env.APP ?? "https://astras.vip").replace(/\/$/, "");
const API = process.env.API ?? (APP.startsWith("http://localhost") ? "https://astras.vip" : APP);

const run = Date.now();
const email = `e2e-pc-${run}@example.com`;
const password = `e2e pc site ${run}`;

const t = await start({ app: APP, api: API, name: "pc", device: { viewport: { width: 1440, height: 900 } } });
const { page, shot, go, waitText, waitPath, clickButton, clickContaining, typeInto } = t;

try {
  // 1. Sign-up through the form: account, password, terms, then the code.
  await go("/register");
  await typeInto('input[type="email"]', email);
  await typeInto('input[autocomplete="new-password"]', password);
  // The terms box shows once the current versions have loaded.
  await page.waitForSelector('button[role="checkbox"]', { visible: true });
  await page.click('button[role="checkbox"]');
  await clickButton("继续");
  await waitText("验证你的邮箱");
  const before = await t.inboxCount(email);
  await clickButton("发送验证码");
  const code = await t.readCode(email, before);
  await typeInto('input[autocomplete="one-time-code"]', code);
  await clickButton("创建账户");
  await waitPath("/assets", 30000);
  await waitText("10,000", 30000);
  ok(`signed up ${email} through the form; the assets page shows the welcome funds`);
  await shot("1-assets");

  // 2. Sign out from the account menu, sign back in with the password.
  await go("/");
  await page.waitForSelector('header a[href="/account/security"]', { visible: true });
  await page.hover('header a[href="/account/security"]');
  await clickButton("退出登录");
  await waitText("注册", 10000);
  await go("/login?next=%2Fmarkets");
  await typeInto('input[autocomplete="username"]', email);
  await typeInto('input[autocomplete="current-password"]', "a wrong password!");
  await page.keyboard.press("Enter");
  await waitText("账户或密码不正确");
  await typeInto('input[autocomplete="current-password"]', password);
  await page.keyboard.press("Enter");
  await waitPath("/markets");
  ok("a wrong password shows its message in place; the right one signs in and returns to ?next");

  // 3. Markets: every market listed, the search narrows them.
  await page.waitForFunction(() => document.body.innerText.includes("BTC") && document.body.innerText.includes("ETH"), { timeout: 20000 });
  await typeInto('input[placeholder="搜索币种名称或代码"]', "ETH");
  await page.waitForFunction(
    () => {
      const rows = [...document.querySelectorAll("tbody tr, [role=row]")].map((r) => r.innerText);
      return rows.length > 0 && rows.some((r) => r.includes("ETH")) && !rows.some((r) => r.includes("BTC\n/USDT"));
    },
    { timeout: 10000 },
  );
  ok("the market list shows the markets and the search narrows them");
  await shot("2-markets");

  // 4. Spot terminal: a limit buy 5% under the last price rests, then cancels.
  await go("/trade/BTC-USDT");
  await page.waitForFunction(() => Number(document.querySelector('input[aria-label="价格"]')?.value) > 0, { timeout: 20000 });
  const last = await page.$eval('input[aria-label="价格"]', (el) => Number(el.value));
  const price = (Math.floor(last * 0.95 * 100) / 100).toFixed(2);
  await typeInto('input[aria-label="价格"]', price);
  await typeInto('input[aria-label="数量"]', "0.0002");
  await clickButton("买入 BTC");
  await page.waitForSelector("[role=dialog]", { visible: true });
  await clickButton("买入", "[role=dialog]");
  await page.waitForFunction(() => [...document.querySelectorAll("[role=tab]")].some((t) => t.innerText.replace(/\s+/g, "") === "当前委托(1)"), {
    timeout: 20000,
  });
  ok(`a limit buy at ${price} rests in the open orders`);
  await shot("3-order");
  await clickButton("撤单");
  await page.waitForFunction(() => [...document.querySelectorAll("[role=tab]")].some((t) => t.innerText.replace(/\s+/g, "") === "当前委托(0)"), {
    timeout: 20000,
  });
  ok("cancelling it empties the open orders (engine and push)");

  // 5. A transfer to futures, then its ledger entry.
  await go("/assets/transfer");
  await typeInto('form[data-testid="transfer-form"] input[inputmode="decimal"]', "12.34");
  await clickButton("确认划转");
  await waitText("已划转");
  await go("/assets/history");
  await waitText("账户划转");
  ok("a transfer of 12.34 USDT to futures completes and shows in the ledger");

  // 6. The deposit address of ETH (Sepolia).
  await go("/assets/deposit");
  await clickContaining(["ETH", "以太坊"]);
  await page.waitForSelector('[data-testid="deposit-address"]', { visible: true, timeout: 20000 });
  const address = await page.$eval('[data-testid="deposit-address"]', (el) => el.innerText.trim());
  if (!/^0x[0-9a-fA-F]{40}$/.test(address)) throw new Error("deposit address: " + address);
  ok(`the deposit page gives an ETH address (${address.slice(0, 8)}…)`);

  // 7. The futures terminal: mark price and funding.
  await go("/futures/BTC-USDT-PERP");
  await waitText("标记价格");
  await waitText("资金费率");
  ok("the futures terminal shows the mark price and the funding countdown");

  // 8. Notifications, devices, the help centre.
  await go("/notifications");
  await page.waitForFunction(() => /全部\s*\(\d+\)/.test(document.body.innerText), { timeout: 20000 });
  await go("/account/sessions");
  await waitText("当前设备");
  await go("/help");
  await waitText("注册与登录");
  ok("notifications, devices (current one marked) and the help centre render");

  // 9. Settings: English switches the site's language at once.
  await go("/account/settings");
  await page.waitForSelector('button[role="radio"][value="en"]', { visible: true });
  await page.click('button[role="radio"][value="en"]');
  await page.waitForFunction(() => document.querySelector("header")?.innerText.includes("Markets"), { timeout: 10000 });
  await page.click('button[role="radio"][value="zh-CN"]');
  ok("the language setting switches the site to English and back");

  // 10. Sign out.
  await go("/");
  await page.waitForSelector('header a[href="/account/security"]', { visible: true });
  await page.hover('header a[href="/account/security"]');
  await clickButton("退出登录");
  await waitText("注册", 10000);
  ok("sign out ends the session");
} catch (e) {
  await t.fail(e);
}

await t.finish();
