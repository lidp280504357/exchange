// Browser smoke test of the mobile site (design 2026-09-30 §7, §12.2), run
// by scripts/e2e/web.sh (task e2e):
//
//   APP=https://m.astras.vip node web/e2e/m-smoke.mjs
//
// Drives the deployed site as a phone would (390 x 844, touch, an iPhone
// user agent, so nginx does not send it to the PC site), in Chinese: sign
// up through the form, the welcome funds on the assets tab, sign out from
// the "me" tab and back in, the market list and its search, a limit order
// placed from the spot terminal's order sheet and cancelled from its open
// orders, a transfer to futures and its ledger entry, a deposit address,
// the futures terminal, notifications, devices, help, the language switch
// and sign-out. Script errors fail the run; every API response is checked
// against the OpenAPI contracts (lib.mjs). Screenshots go to SHOTS when set.
import { ok, sleep, start } from "./lib.mjs";

const APP = (process.env.APP ?? "https://m.astras.vip").replace(/\/$/, "");
const API = process.env.API ?? (APP.startsWith("http://localhost") ? "https://m.astras.vip" : APP);

const run = Date.now();
const email = `e2e-m-${run}@example.com`;
const password = `e2e mobile site ${run}`;

const phone = {
  viewport: { width: 390, height: 844, deviceScaleFactor: 3, isMobile: true, hasTouch: true },
  userAgent:
    "Mozilla/5.0 (iPhone; CPU iPhone OS 18_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.5 Mobile/15E148 Safari/604.1",
};
const t = await start({ app: APP, api: API, name: "m", device: phone });
const { page, shot, go, waitText, waitPath, clickButton, typeInto } = t;

// openOrders waits for the open orders tab to show n orders.
const openOrders = (n) =>
  page.waitForFunction((want) => [...document.querySelectorAll("[role=tab]")].some((el) => el.innerText.replace(/\s+/g, "") === want), { timeout: 20000 }, `当前委托(${n})`);

// sheetOpen waits for a bottom sheet to finish sliding in: a button
// clicked while it moves is not clickable yet.
async function sheetOpen() {
  await page.waitForSelector("[role=dialog]", { visible: true });
  await sleep(600);
}

// signOut leaves from the "me" tab: its row, then the confirmation sheet.
async function signOut() {
  await go("/me");
  await clickButton("退出登录");
  await sheetOpen();
  await clickButton("退出登录", "[role=dialog]");
  await waitText("欢迎来到 Astras", 10000);
}

try {
  // 1. Sign-up through the form: account, password, terms, then the code.
  await go("/register");
  await typeInto('input[autocomplete="email"]', email);
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
  await page.waitForSelector('[data-testid="assets-total"]', { visible: true, timeout: 30000 });
  await waitText("10,000", 30000);
  ok(`signed up ${email} through the form; the assets tab shows the welcome funds`);
  await shot("1-assets");

  // 2. Sign out from the "me" tab, sign back in with the password.
  await signOut();
  await go("/login?next=%2Fmarkets");
  await typeInto('input[autocomplete="username"]', email);
  await typeInto('input[autocomplete="current-password"]', "a wrong password!");
  await page.keyboard.press("Enter");
  await waitText("账户或密码不正确");
  await typeInto('input[autocomplete="current-password"]', password);
  await page.keyboard.press("Enter");
  await waitPath("/markets");
  ok("sign-out asks first; a wrong password shows its message in place; the right one returns to ?next");

  // 3. Markets: every market listed, the search narrows them. From 50 rows
  // the list is windowed: a role=list of role=listitem rows instead of a ul.
  const ROWS = ':is(ul, [role=list])[aria-label="行情"] > :is(li, [role=listitem])';
  const rows = () => page.$$eval(ROWS, (els) => els.map((el) => el.innerText));
  await page.waitForFunction((sel) => document.querySelectorAll(sel).length >= 3, { timeout: 20000 }, ROWS);
  await typeInto('input[placeholder="搜索币种名称或代码"]', "ETH");
  // The search also matches names in both languages ("Ethena", "Ethereum
  // Classic"): a few rows, ETH's among them.
  await page.waitForFunction(
    (sel) => {
      const list = [...document.querySelectorAll(sel)].map((el) => el.innerText);
      return list.length > 0 && list.length <= 8 && list.some((r) => r.includes("ETH"));
    },
    { timeout: 10000 },
    ROWS,
  );
  ok(`the market list shows the markets and the search narrows them (${(await rows()).length} rows for ETH)`);
  await shot("2-markets");

  // 4. Spot terminal: the buy sheet places a limit buy 5% under the last
  // price (after its confirmation), which rests and then cancels.
  await go("/trade/BTC-USDT");
  await clickButton("买入 BTC");
  await sheetOpen();
  await page.waitForFunction(() => Number(document.querySelector('[role=dialog] input[aria-label="价格"]')?.value) > 0, { timeout: 20000 });
  const last = await page.$eval('[role=dialog] input[aria-label="价格"]', (el) => Number(el.value));
  const price = (Math.floor(last * 0.95 * 100) / 100).toFixed(2);
  await typeInto('[role=dialog] input[aria-label="价格"]', price);
  await typeInto('[role=dialog] input[aria-label="数量"]', "0.0002");
  await clickButton("买入 BTC", "[role=dialog]");
  await clickButton("确认", "[role=dialog]");
  // The sheet's overlay takes taps until it has slid away.
  await page.waitForSelector("[role=dialog]", { hidden: true });
  await openOrders(1);
  ok(`a limit buy at ${price} from the order sheet rests in the open orders`);
  await shot("3-order");
  await clickButton("撤单");
  await openOrders(0);
  ok("cancelling it empties the open orders (engine and push)");

  // 5. A transfer to futures, then its ledger entry.
  await go("/assets/transfer");
  await typeInto('form[data-testid="transfer-form"] input[inputmode="decimal"]', "12.34");
  await clickButton("确认划转");
  await waitText("已划转");
  await go("/assets/history");
  await waitText("账户划转");
  ok("a transfer of 12.34 USDT to futures completes and shows in the history");

  // 6. Deposit addresses: ETH on Sepolia (the platform's own wallet) and
  // USDT on TRC20 (the custodian's, ADR-0011).
  const depositAddress = async (query, pattern, what) => {
    await go(`/assets/deposit?${query}`);
    await page.waitForSelector('[data-testid="deposit-address"]', { visible: true, timeout: 20000 });
    await page.waitForFunction((re) => new RegExp(re).test(document.querySelector('[data-testid="deposit-address"]')?.innerText.trim() ?? ""), { timeout: 20000 }, pattern);
    const address = await page.$eval('[data-testid="deposit-address"]', (el) => el.innerText.trim());
    ok(`the deposit page gives ${what} (${address.slice(0, 8)}…)`);
  };
  await depositAddress("asset=ETH&network=ETH-SEPOLIA", "^0x[0-9a-fA-F]{40}$", "an ETH address on Sepolia");
  await depositAddress("asset=USDT&network=TRON", "^T[1-9A-HJ-NP-Za-km-z]{33}$", "a TRC20 address from the custodian");

  // 7. The futures terminal: mark price and funding.
  await go("/futures/BTC-USDT-PERP");
  await waitText("标记价格");
  await waitText("资金费率");
  ok("the futures terminal shows the mark price and the funding countdown");

  // 8. Notifications, devices, the help centre.
  await go("/notifications");
  // The welcome notice of the sign-up, under the "all" pill.
  await waitText("欢迎加入");
  await waitText("全部");
  await go("/account/sessions");
  await waitText("当前设备");
  await go("/help");
  await waitText("注册与登录");
  ok("notifications, devices (current one marked) and the help centre render");

  // 9. Settings: English switches the site's language at once (the page
  // and its header).
  await go("/account/settings");
  await clickButton("English");
  await waitText("Time zone", 10000);
  await clickButton("简体中文");
  await waitText("时区", 10000);
  ok("the language setting switches the site to English and back");

  // 10. Sign out.
  await signOut();
  ok("sign out ends the session");
} catch (e) {
  await t.fail(e);
}

await t.finish();
