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
import { menuOnTop, ok, start } from "./lib.mjs";

const APP = (process.env.APP ?? "https://astras.vip").replace(/\/$/, "");
const API = process.env.API ?? (APP.startsWith("http://localhost") ? "https://astras.vip" : APP);

const run = Date.now();
const email = `e2e-pc-${run}@example.com`;
const password = `e2e pc site ${run}`;

const t = await start({ app: APP, api: API, name: "pc", device: { viewport: { width: 1440, height: 900 } } });
const { page, shot, go, waitText, waitPath, clickButton, typeInto } = t;

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
  // Margin trading for this user alone (web.sh puts the switch back).
  const marginOn = await t.openMargin();
  if (marginOn) ok("margin trading is open to this user (on for everyone, or opened for it alone)");
  await shot("1-assets");
  // A top-bar menu closes once the pointer leaves it, also after one of its
  // items was clicked (B109: the focus the click left on the item kept it
  // open). The assets menu: hovered open, 充值 followed, the pointer away.
  const assetsMenu = () =>
    page.evaluate(() => {
      const group = [...document.querySelectorAll("header nav .group")].find((g) => g.firstElementChild?.textContent.trim() === "资产");
      return group ? getComputedStyle(group.lastElementChild).visibility : "missing";
    });
  await page.hover('header nav .group > a[href="/assets"]');
  await page.waitForFunction(
    () => getComputedStyle([...document.querySelectorAll("header nav .group")].find((g) => g.firstElementChild?.textContent.trim() === "资产").lastElementChild).visibility === "visible",
    { timeout: 5000 },
  );
  await clickButton("充值", "header nav");
  await waitPath("/assets/deposit");
  await page.mouse.move(10, 700);
  await page.waitForFunction(
    () => getComputedStyle([...document.querySelectorAll("header nav .group")].find((g) => g.firstElementChild?.textContent.trim() === "资产").lastElementChild).visibility === "hidden",
    { timeout: 3000 },
  ).catch(async () => {
    throw new Error(`the assets menu stays ${await assetsMenu()} after the pointer left it`);
  });
  ok("a top-bar menu opens on hover and closes once the pointer leaves it, also after one of its items was followed");
  await go("/assets");
  // The balance table's header sits right on top of its rows: a header stuck
  // 56 px under the top bar once sat 56 px down inside its own scroll
  // container instead, over the first row.
  await page.waitForFunction(() => document.querySelector('table[aria-label="我的资产"] tbody')?.innerText.includes("USDT"), { timeout: 20000 });
  const gap = await page.$eval('table[aria-label="我的资产"]', (table) => {
    // A header cell: the sticky offset moves the cells, not the thead box.
    const head = table.tHead.rows[0].cells[0].getBoundingClientRect();
    const first = table.tBodies[0].rows[0].getBoundingClientRect();
    return { headTop: head.top - table.getBoundingClientRect().top, overlap: head.bottom - first.top };
  });
  if (gap.headTop > 1 || gap.overlap > 1) throw new Error(`the assets table header is ${gap.headTop}px down and covers ${gap.overlap}px of the first row`);
  ok("the assets table header sits on top of its rows");
  const switchLines = await page.evaluate(() => {
    const label = [...document.querySelectorAll("label")].find((l) => l.textContent.trim().startsWith("隐藏小额"));
    return label ? Math.round(label.getBoundingClientRect().height / parseFloat(getComputedStyle(label).lineHeight)) : 0;
  });
  if (switchLines !== 1) throw new Error(`the hide-small switch label takes ${switchLines} lines`);
  ok("the hide-small switch label stays on one line");

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
  // The page scrolls the table (it has no scroll box of its own, which left
  // a blank under it), and its header sticks right under the top bar.
  const scrolled = await page.evaluate(async () => {
    const table = document.querySelector("main table");
    const box = table.parentElement;
    window.scrollTo(0, document.documentElement.scrollHeight / 2);
    await new Promise((r) => setTimeout(r, 500));
    const gap = table.tHead.rows[0].cells[0].getBoundingClientRect().top - document.querySelector("header").getBoundingClientRect().bottom;
    window.scrollTo(0, 0);
    return { innerScroll: box.scrollHeight > box.clientHeight + 1, gap: Math.round(gap) };
  });
  if (scrolled.innerScroll || Math.abs(scrolled.gap) > 1) throw new Error(`the market table scrolls in a box (${scrolled.innerScroll}) or its header is ${scrolled.gap}px off the top bar`);
  ok("the market table scrolls with the page, its header stuck under the top bar");
  // The top bar's menus open over the table's stuck header (the assets
  // page of a new account is too short for its header to stick).
  await menuOnTop(page, "合约");
  await menuOnTop(page, "资产");
  ok("the 合约 and 资产 menus open over the market table's stuck header");
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

  // 4. Spot terminal. First the book fills its panel (B71, B74) on both
  // terminals, signed in, at 1280 × 760, 1280 × 800, 1440 × 900 and
  // 1920 × 1080, measured on the page: each side has the rows of at least
  // 20 px that fit in the room the two sides get ((room ÷ 2 ÷ 20) rows, 5
  // to 20), drawn taller (to 24 px) to fill it, so the last bid row ends at
  // most 4 px above the panel's bottom (below it only when even 5 rows do
  // not fit and are cut off; further above only past 20 rows of 24 px). All relative to the panel, so the test
  // mode's banner above the page changes nothing. Then with step 10 kept
  // from before: BTC-USDT's public book (200 levels a side) cannot fill
  // it, so the book is cut finer, the trigger shows "≈ 1" and says why,
  // and 10 is off in the menu with its note (review CD). Then a limit buy
  // 5% under the last price rests, then cancels.
  const bookFills = async (width, height) => {
    await page.setViewport({ width, height });
    const fills = () => {
      const sides = [...document.querySelectorAll('[role="group"][aria-label="买盘"], [role="group"][aria-label="卖盘"]')];
      let panel = sides[0];
      while (panel && !(String(panel.className).includes("bg-bg-1") && panel.querySelector("[role=tablist]"))) panel = panel.parentElement;
      const bids = sides.find((s) => s.getAttribute("aria-label") === "买盘");
      const rowsOf = (s) => [...s.querySelectorAll("[data-book-row]")];
      if (!panel || sides.length !== 2 || !bids || rowsOf(bids).length === 0) return false;
      const room = sides.reduce((a, s) => a + s.getBoundingClientRect().height, 0);
      const want = Math.max(5, Math.min(20, Math.floor(room / 2 / 20)));
      const rows = sides.map((s) => rowsOf(s).length);
      const gap = panel.getBoundingClientRect().bottom - rowsOf(bids).at(-1).getBoundingClientRect().bottom;
      const trigger = document.querySelector('button[aria-label="价格精度"]');
      window.__book = {
        panel: Math.round(panel.getBoundingClientRect().height),
        room: Math.round(room),
        want,
        rows,
        rowHeight: Math.round(rowsOf(bids)[0].getBoundingClientRect().height * 100) / 100,
        gap: Math.round(gap * 10) / 10,
        step: trigger?.innerText.trim() ?? "",
        why: trigger?.parentElement?.title ?? "",
      };
      // 20 rows of 24 px are as far as the book goes: a taller room keeps
      // the rest blank, as designed.
      const atMost = want === 20 && window.__book.rowHeight >= 23.99;
      return rows.every((n) => n === want) && (gap <= 4 || atMost) && (gap >= -0.5 || room < 2 * 5 * 20);
    };
    try {
      await page.waitForFunction(fills, { timeout: 20000 });
    } catch {
      throw new Error(`the book does not fill its panel at ${width} × ${height}: ${JSON.stringify(await page.evaluate(() => window.__book))}`);
    }
    return page.evaluate(() => window.__book);
  };
  const keepStep = (step) =>
    page.evaluate((s) => {
      const prefs = JSON.parse(localStorage.getItem("exchange.terminal") ?? '{"state":{},"version":1}');
      prefs.state.bookStep = { ...prefs.state.bookStep, "BTC-USDT": s };
      localStorage.setItem("exchange.terminal", JSON.stringify(prefs));
    }, step);
  for (const terminal of ["/trade/BTC-USDT", "/futures/BTC-USDT-PERP"]) {
    await go(terminal);
    for (const [w, h] of [[1280, 760], [1280, 800], [1440, 900], [1920, 1080]]) {
      const b = await bookFills(w, h);
      ok(`${terminal} at ${w} × ${h}: ${b.rows.join(" and ")} rows of ${b.rowHeight} px in ${b.room} px, ${b.gap} px under the bids`);
    }
  }
  await keepStep("10");
  await go("/trade/BTC-USDT");
  const coarse = await bookFills(1920, 1080);
  if (coarse.step !== "10") {
    if (!coarse.step.startsWith("≈") || !/按 10 填满|所选 10 刚够/.test(coarse.why)) throw new Error(`with 10 kept the book shows ${coarse.step} without saying why: ${JSON.stringify(coarse)}`);
    await page.click('button[aria-label="价格精度"]');
    await page.waitForSelector("[role=option]", { visible: true, timeout: 5000 });
    const ten = await page.evaluate(() => {
      const o = [...document.querySelectorAll("[role=option]")].find((x) => x.innerText.trim().startsWith("10"));
      return o ? { off: o.hasAttribute("data-disabled"), text: o.innerText.replace(/\s+/g, " ").trim() } : null;
    });
    await page.keyboard.press("Escape");
    if (!ten?.off || !/深度不足|余量不足/.test(ten.text)) throw new Error(`with 10 kept, 10 is not off in the menu with its note: ${JSON.stringify(ten)}`);
  }
  ok(`with step 10 kept, the book still fills (${coarse.rows.join(" and ")} rows, trigger "${coarse.step}"${coarse.why ? `: ${coarse.why}` : ""}; 10 off in the menu)`);
  await keepStep("");
  await page.setViewport({ width: 1440, height: 900 });
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

  // 5b. The margin accounts (margin design 2026-10-06 §7): the cross account
  // with its gauge. While margin trading is open to the user, 10 USDT moves
  // into the cross account from the dialog and back out; while it is not,
  // the page says so.
  await go("/assets/margin");
  await page.waitForSelector('[data-testid="margin-account-MARGIN_CROSS"]', { visible: true, timeout: 20000 });
  await page.waitForSelector('[data-testid="margin-level"]', { visible: true });
  const closed = await page.evaluate(() => document.body.innerText.includes("杠杆交易 · "));
  if (closed && marginOn) throw new Error("margin trading was opened for this user, but the margin page says it is not open");
  if (closed) {
    ok("the margin page shows the cross account and says margin trading is not open to this user");
  } else {
    const marginTransfer = async (direction, amount, done) => {
      await page.click('button[data-testid="margin-transfer"]');
      await page.waitForSelector('form[data-testid="margin-transfer-form"]', { visible: true });
      if (direction === "OUT") await clickButton("划出到现货", "[role=dialog]");
      await typeInto('form[data-testid="margin-transfer-form"] input[inputmode="decimal"]', amount);
      await clickButton("确认划转", "[role=dialog]");
      await waitText(done);
      await page.waitForSelector("[role=dialog]", { hidden: true, timeout: 10000 });
    };
    // The push of the account comes once, to the pages subscribed by then.
    await t.waitSubscribed("margin");
    await marginTransfer("IN", "10", "已划入 10 USDT");
    await page.waitForFunction(() => document.querySelector('[data-testid="margin-account-MARGIN_CROSS"] tbody')?.innerText.includes("USDT"), { timeout: 20000 });
    // margin-service publishes the account it changed (margin.accounts), the
    // gateway pushes it whole on "margin" (ACCOUNT), checked against MarginPush.
    await t.waitPush(
      (p) => p.data.type === "ACCOUNT" && p.data.account?.account === "MARGIN_CROSS" && p.data.account.balances.some((b) => b.asset === "USDT" && Number(b.free) >= 10),
      20000,
      "ACCOUNT push of the cross account holding the 10 USDT",
    );
    // The assets overview counts the margin accounts (B102): their tile shows
    // the 10 USDT (the amount may run into its unit: "10.00USDT").
    await go("/assets");
    await page.waitForFunction((sel) => /(^|[^\d.,])10\.00(?!\d)/.test(document.querySelector(sel)?.innerText ?? ""), { timeout: 20000 }, '[data-testid="assets-margin"]');
    await go("/assets/margin");
    await page.waitForSelector('[data-testid="margin-account-MARGIN_CROSS"]', { visible: true, timeout: 20000 });
    await marginTransfer("OUT", "10", "已划出 10 USDT");
    ok("10 USDT moves into the cross margin account from its dialog, shows in its coins (pushed on the margin channel) and in the assets overview's total, and moves back");
    // The spot terminal trades from the cross account once chosen above
    // the order form, with its margin level; back to spot afterwards.
    await go("/trade/BTC-USDT");
    await page.waitForSelector('[data-testid="margin-bar"]', { visible: true, timeout: 20000 });
    await clickButton("全仓", '[data-testid="margin-bar"]');
    await page.waitForSelector('[data-testid="margin-bar"] [data-testid="margin-level"]', { visible: true, timeout: 10000 });
    await clickButton("现货", '[data-testid="margin-bar"]');
    await page.waitForSelector('[data-testid="margin-bar"] [data-testid="margin-level"]', { hidden: true, timeout: 10000 });
    ok("the spot terminal switches its order form to the cross margin account and back");
  }
  await shot("5b-margin");

  // 6. The deposit address: ETH on Sepolia, derived by the platform's own
  // wallet. A custodian's address would come from UDUN, which may be the
  // real gateway: custody.sh covers that path on the stand-in (ADR-0017).
  const depositAddress = async (query, pattern, what) => {
    await go(`/assets/deposit?${query}`);
    await page.waitForSelector('[data-testid="deposit-address"]', { visible: true, timeout: 20000 });
    await page.waitForFunction((re) => new RegExp(re).test(document.querySelector('[data-testid="deposit-address"]')?.innerText.trim() ?? ""), { timeout: 20000 }, pattern);
    const address = await page.$eval('[data-testid="deposit-address"]', (el) => el.innerText.trim());
    ok(`the deposit page gives ${what} (${address.slice(0, 8)}…)`);
  };
  await depositAddress("asset=ETH&network=ETH-SEPOLIA", "^0x[0-9a-fA-F]{40}$", "an ETH address on Sepolia");

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
