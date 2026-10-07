// Browser smoke test of the PC site (design 2026-09-30 §6, §12.2), run by
// scripts/e2e/web.sh (task e2e):
//
//   APP=https://astras.vip node web/e2e/pc-smoke.mjs
//
// Drives the deployed site in headless Chrome, in Chinese, the way a new
// user would: a visitor's home and terms pages without a failed request,
// sign up through the form (the human check passes with the
// environment's CAPTCHA_BYPASS_TOKEN, the code comes from the dev inbox),
// the welcome funds on the assets page, sign out and back in, the market
// list and its search, the first screens of the home, markets, coin,
// assets and terms pages without a failed request, a limit order placed
// from the spot terminal and
// cancelled from its open orders, a transfer to futures and its ledger
// entry, a deposit address, the futures terminal, the candle charts'
// legends clear of the highest candle (the futures and spot terminals
// on hourly candles, the coin page at 1024 wide), the futures data (the
// terminal's 数据 tab, the futures category's columns, /futures/data),
// notifications, devices,
// the profile (the drawn username, a rename, an avatar uploaded and
// removed), the App download page and entries, closed product lines
// (hidden, their terminals not open, the wind-down page), the language
// switch and sign-out. Script errors fail the run; every API
// response is checked against the OpenAPI contracts. Chrome comes from
// CHROME or the usual install paths; screenshots go to SHOTS when set.
import { APPS_HIDDEN, APPS_OFFERED, PRODUCTS_PAUSED, choosePicture, legendClear, menuOnTop, ok, sleep, start, withApps, withProducts } from "./lib.mjs";

const APP = (process.env.APP ?? "https://astras.vip").replace(/\/$/, "");
const API = process.env.API ?? (APP.startsWith("http://localhost") ? "https://astras.vip" : APP);

const run = Date.now();
const email = `e2e-pc-${run}@example.com`;
const password = `e2e pc site ${run}`;

const t = await start({ app: APP, api: API, name: "pc", device: { viewport: { width: 1440, height: 900 } } });
const { page, shot, go, waitText, waitPath, clickButton, typeInto } = t;

try {
  // 0. A visitor's home and terms pages ask for nothing that fails either
  // (B117, B118: the user saw the hero's 404 signed out).
  const visitorFailing = [];
  for (const path of ["/", "/legal/terms"]) {
    for (const f of await t.firstScreenFailures(path, null)) visitorFailing.push(`${path}: ${f}`);
  }
  if (visitorFailing.length) throw new Error(`requests failed while a visitor's first screens came up: ${visitorFailing.join("; ")}`);
  ok("a visitor's home and terms pages come up without a failed request");
  // A visitor's top bar asks for the margin assets (the margin entry's
  // pair) only once its 交易 menu opens, not on every page (B143).
  const marginAsked = [];
  const onMarginAsked = (r) => {
    if (new URL(r.url()).pathname === "/v1/margin/assets") marginAsked.push(r.url());
  };
  page.on("request", onMarginAsked);
  try {
    await go("/legal/terms");
    await sleep(1500);
    if (marginAsked.length) throw new Error(`a visitor's page asked for the margin assets before the 交易 menu opened: ${marginAsked.join("; ")}`);
    const trade = await page.evaluateHandle(() => [...document.querySelectorAll("header nav a")].find((a) => a.innerText.trim() === "交易"));
    await trade.hover();
    for (let i = 0; i < 20 && !marginAsked.length; i++) await sleep(250);
    if (!marginAsked.length) throw new Error("the 交易 menu opened without asking for the margin assets");
  } finally {
    page.off("request", onMarginAsked);
  }
  ok("a visitor's top bar asks for the margin assets only once its 交易 menu opens");

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
  // Its entries carry a line under their titles (B135): the deposit one by its link.
  await page.click('header nav .group a[href="/assets/deposit"]');
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
  await page.waitForSelector('header a[href="/account/profile"]', { visible: true });
  await page.hover('header a[href="/account/profile"]');
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
  // a blank under it), and its header sticks right under the top bar. From
  // 200 rows (Markets.tsx VIRTUAL_FROM: 全部 has the 91 pairs and the 109
  // contracts since they opened) the table scrolls virtually in a box of its
  // own, its header stuck to the box's top; the page-scrolled table is then
  // the 现货 category's.
  const tableScroll = () =>
    page.evaluate(async () => {
      const table = document.querySelector("main table");
      const box = table.parentElement;
      const boxed = box.scrollHeight > box.clientHeight + 1 && getComputedStyle(box).overflowY === "auto";
      if (boxed) box.scrollTop = box.scrollHeight / 2;
      else window.scrollTo(0, document.documentElement.scrollHeight / 2);
      await new Promise((r) => setTimeout(r, 500));
      const top = boxed ? box.getBoundingClientRect().top : document.querySelector("header").getBoundingClientRect().bottom;
      const gap = table.tHead.rows[0].cells[0].getBoundingClientRect().top - top;
      const rows = table.tBodies[0].rows.length;
      box.scrollTop = 0;
      window.scrollTo(0, 0);
      return { boxed, rows, gap: Math.round(gap) };
    });
  // The contracts come with their own request after the pairs: the table
  // switches to its box when they arrive, so the check waits for them (the
  // rail's 合约 count; measured before, a page-scrolled header read -298px
  // off on 78a1acf).
  await page.waitForFunction(
    () => Number([...document.querySelectorAll("main nav button")].find((b) => b.innerText.trim().startsWith("合约"))?.innerText.match(/(\d+)\s*$/)?.[1] ?? 0) > 0,
    { timeout: 20000 },
  );
  let scrolled = await tableScroll();
  if (scrolled.boxed) {
    if (Math.abs(scrolled.gap) > 1) throw new Error(`the market table's header is ${scrolled.gap}px off its box's top`);
    // The box fills the row beside the category rail: no blank under it (F19).
    const fill = await page.evaluate(() => ({
      rail: Math.round(document.querySelector('main nav[aria-label="分类"]').getBoundingClientRect().bottom),
      box: Math.round(document.querySelector("main table").closest("section").getBoundingClientRect().bottom),
    }));
    if (fill.box < fill.rail - 1) throw new Error(`the market table's box ends ${fill.rail - fill.box}px above the category rail's end`);
    ok(`the long market table scrolls in its own box (${scrolled.rows} rows in the DOM), its header stuck to the box's top, the box as long as the rail`);
    await go("/markets?cat=spot");
    // Its rows, not the 14 skeleton rows of a table still loading (B139).
    await page.waitForSelector("main table:not([aria-busy]) tbody tr", { timeout: 20000 });
    scrolled = await tableScroll();
  }
  if (scrolled.boxed || Math.abs(scrolled.gap) > 1) throw new Error(`the market table scrolls in a box (${scrolled.boxed}) or its header is ${scrolled.gap}px off the top bar`);
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

  // 3b. The first screens of the home, markets, coin, assets and terms
  // pages ask for nothing that fails (B117: the hero's 404 was red in the
  // browser's console; an article the console has not published is read
  // from its draft without asking for it).
  const failing = [];
  for (const [path, ready] of [
    ["/", null],
    ["/markets", "main table tbody tr"],
    ["/coin/BTC", '[data-testid="candle-plot"] canvas'],
    ["/assets", '[data-testid="assets-total"]'],
    ["/legal/terms", null],
  ]) {
    for (const f of await t.firstScreenFailures(path, ready)) failing.push(`${path}: ${f}`);
  }
  if (failing.length) throw new Error(`requests failed while first screens came up: ${failing.join("; ")}`);
  ok("the home, markets, coin, assets and terms pages come up without a failed request");

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
  await page.waitForSelector('[data-testid="margin-account-MARGIN_CROSS"] [data-testid="margin-level"]', { visible: true });
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
    // the order form, its borrowable amount and margin level under the
    // form; back to spot afterwards. The bar and the form keep the panel's
    // padding (p-3, as the futures panel's) on both sides in each account
    // (B120: the spot form was flush with the panel's edges).
    await go("/trade/BTC-USDT");
    await page.waitForSelector('[data-testid="margin-bar"]', { visible: true, timeout: 20000 });
    const insets = () =>
      page.evaluate(() => {
        const panel = document.getElementById("order-form");
        const box = panel.getBoundingClientRect();
        const left = box.left + panel.clientLeft;
        const right = left + panel.clientWidth;
        const of = (el) => {
          const r = el.getBoundingClientRect();
          return [r.left - left, right - r.right].map((v) => Math.round(v * 10) / 10);
        };
        return {
          padding: parseFloat(getComputedStyle(panel).paddingLeft),
          bar: of(document.querySelector('[data-testid="margin-bar"] > :first-child')),
          side: of(document.querySelector("#order-form form > :first-child")),
          submit: of(document.querySelector('#order-form button[type="submit"]')),
        };
      });
    const spotInsets = await insets();
    await clickButton("全仓", '[data-testid="margin-bar"]');
    await page.waitForSelector('#order-form [data-testid="margin-info"] [data-testid="margin-level"]', { visible: true, timeout: 10000 });
    await page.waitForSelector('#order-form [data-testid="margin-info"] [data-testid="margin-borrowable"]', { visible: true, timeout: 10000 });
    const crossInsets = await insets();
    await shot("5b-margin-cross");
    await clickButton("现货", '[data-testid="margin-bar"]');
    await page.waitForSelector('[data-testid="margin-info"]', { hidden: true, timeout: 10000 });
    for (const [state, { padding, ...parts }] of [["spot", spotInsets], ["cross", crossInsets]]) {
      for (const [part, [l, r]] of Object.entries(parts)) {
        if (padding < 8 || Math.abs(l - padding) > 0.6 || Math.abs(r - padding) > 0.6) {
          throw new Error(`the ${part} of the ${state} order form is ${l} px from the left and ${r} px from the right, not the panel's ${padding} px: ${JSON.stringify({ spotInsets, crossInsets })}`);
        }
      }
    }
    ok(`the spot terminal switches its order form to the cross margin account and back, ${spotInsets.padding} px from both edges in each, the margin level and borrowable amount under the form`);
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
  // The candle charts start below their legends (B116: the legend sits over
  // the plot, and the candles' scale leaves room for it).
  const clearOf = async (where) => {
    const l = await legendClear(page);
    if (!l.clear) {
      throw new Error(`${where}: the highest candle starts at ${l.candleTop}px, under the legend that ends at ${l.legendBottom}px (${l.legendHeight}px high, room ${l.room})`);
    }
    return `${where}: legend ${l.legendHeight}px, the highest candle ${l.candleTop - l.legendBottom}px below it`;
  };
  // The terminals on hourly candles, as in the user's report (a candle of
  // the hour opens on the hour: its legend shows HH:00).
  const hourly = async (where) => {
    await clickButton("1小时");
    await page.waitForFunction(() => [...document.querySelectorAll('button[aria-pressed="true"]')].some((b) => b.innerText.trim() === "1小时"), { timeout: 10000 });
    await page.waitForFunction(() => /\d{2}:00(?!:)/.test(document.querySelector('[data-testid="candle-legend"]')?.innerText ?? ""), { timeout: 20000 });
    await sleep(1500); // the hourly candles drawn
    return clearOf(`${where}, hourly`);
  };
  const futuresChart = await hourly("the futures terminal at 1440 × 900");
  await go("/trade/BTC-USDT");
  const spotChart = await hourly("the spot terminal at 1440 × 900");
  // The coin page at 1024 wide, where its chart is narrowest.
  await page.setViewport({ width: 1024, height: 768 });
  await go("/coin/BTC");
  const coinChart = await clearOf("the BTC coin page at 1024 × 768");
  await page.setViewport({ width: 1440, height: 900 });
  ok(`the candle charts start below their legends (${futuresChart}; ${spotChart}; ${coinChart})`);

  // 7b. Futures data (design 2026-10-06 §3.3, batch F4): the terminal's
  // 数据 tab draws the contract's seven statistics and lists its
  // liquidations, a period reads the charts again, the platform coin's
  // contract has none (and nothing is asked for it), the market list's
  // futures category shows the open interest and funding, and
  // /futures/data lists the contracts with the board of the one picked.
  // Preconditions: market-data-service reads the reference market's
  // statistics (market.futures_data on; BTC-USDT-PERP follows BTCUSDT and
  // has points of every statistic and period), the platform coin's
  // ASTRA-USDT-PERP follows none (no reference_symbol); a day without
  // liquidations passes (the card says so).
  const futuresReads = [];
  const onFuturesRead = (r) => {
    const u = new URL(r.url());
    if (/^\/v1\/market\/[^/]+\/(futures-data|liquidations)$/.test(u.pathname)) futuresReads.push(`${r.status()} ${u.pathname}${u.search}`);
  };
  page.on("response", onFuturesRead);
  try {
    const statistics = ["持仓量", "大户账户数多空比", "大户持仓量多空比", "多空账户数比", "主动买卖量", "基差", "资金费率历史"];
    const drawn = (scope) =>
      page.waitForFunction(
        (sel, names) => {
          const cards = [...document.querySelectorAll(`${sel} section[aria-label]`)];
          return names.every((n) => cards.find((c) => c.getAttribute("aria-label") === n)?.querySelector("svg path"));
        },
        { timeout: 30000 },
        scope,
        statistics,
      );
    await go("/futures/BTC-USDT-PERP");
    await clickButton("数据");
    await drawn('[data-testid="futures-data"]');
    await page.waitForFunction(
      () => {
        const card = [...document.querySelectorAll('[data-testid="futures-data"] section[aria-label]')].find((c) => c.getAttribute("aria-label") === "爆仓");
        return card && (card.querySelectorAll('[role="rowgroup"] [role="row"]').length > 0 || card.innerText.includes("最近一天没有爆仓"));
      },
      { timeout: 20000 },
    );
    const hourly = futuresReads.length;
    await clickButton("1小时", '[data-testid="futures-data"]');
    for (let i = 0; i < 40 && !futuresReads.slice(hourly).some((r) => r.includes("period=1h")); i++) await sleep(250);
    if (!futuresReads.slice(hourly).some((r) => r.startsWith("200 ") && r.includes("period=1h"))) throw new Error(`the 1-hour period read nothing: ${futuresReads.slice(hourly).join("; ")}`);
    await drawn('[data-testid="futures-data"]');
    await shot("7b-futures-data");
    const astra = futuresReads.length;
    await go("/futures/ASTRA-USDT-PERP");
    await clickButton("数据");
    await waitText("暂无数据");
    await sleep(1500);
    const asked = futuresReads.slice(astra).filter((r) => r.includes("ASTRA"));
    if (asked.length) throw new Error(`the platform coin's contract was asked for futures data: ${asked.join("; ")}`);
    ok("the futures terminal's 数据 tab draws the seven statistics and the liquidations, reads a period again, and asks nothing for ASTRA");

    await go("/markets?cat=futures");
    await page.waitForFunction(
      () => {
        const table = document.querySelector('table[aria-label="行情"]');
        const head = table?.tHead?.innerText ?? "";
        const btc = [...(table?.tBodies[0]?.rows ?? [])].find((tr) => tr.innerText.includes("BTCUSDT"))?.innerText ?? "";
        return head.includes("持仓量") && head.includes("资金费率") && /[+-]?\d+\.\d{4}%/.test(btc);
      },
      { timeout: 20000 },
    );
    await go("/futures/data");
    await page.waitForFunction(() => [...document.querySelectorAll("table tbody tr")].some((tr) => tr.innerText.includes("BTCUSDT") && /\d+\.\d{4}%/.test(tr.innerText)), {
      timeout: 20000,
    });
    await drawn('section[aria-label$="合约数据"]');
    await shot("7b-futures-overview");
    ok("the market list's futures category has the open interest and funding columns; /futures/data lists the contracts and draws the board");
  } finally {
    page.off("response", onFuturesRead);
  }

  // 8. Notifications, devices, the help centre.
  await go("/notifications");
  await page.waitForFunction(() => /全部\s*\(\d+\)/.test(document.body.innerText), { timeout: 20000 });
  await go("/account/sessions");
  await waitText("当前设备");
  await go("/help");
  await waitText("注册与登录");
  ok("notifications, devices (current one marked) and the help centre render");

  // 8b. The profile (design 2026-10-07, avatars and usernames, batch I2):
  // the username drawn at sign-up heads the page and the account menu, the
  // avatar is the built-in one of the user's ID. A new name shows at once
  // and starts the 7 days (the change button off). A picture made in the
  // page (900 × 600) is shrunk to its middle square and uploaded: the page
  // and the top bar switch to the server's WebP (256 px, the top bar the
  // 64 px one); "use default" brings the built-in one back everywhere.
  await go("/account/profile");
  await page.waitForSelector('[data-testid="profile-username"]', { visible: true, timeout: 20000 });
  const drawn = await page.$eval('[data-testid="profile-username"]', (e) => e.textContent);
  if (!/^user_[a-z0-9]{8}$/.test(drawn ?? "")) throw new Error(`the username drawn at sign-up is ${drawn}`);
  await page.waitForSelector('header svg[data-avatar-default]', { visible: true });
  await page.waitForSelector('[data-testid="profile-avatar"] svg[data-avatar-default]', { visible: true });
  await page.hover('header a[href="/account/profile"]');
  await page.waitForFunction(
    (name) => [...document.querySelectorAll('header [data-testid="my-username"]')].some((e) => e.checkVisibility() && e.textContent === name),
    { timeout: 10000 },
    drawn,
  );
  await page.mouse.move(720, 700);
  const renamed = `web_${String(run).slice(-8)}`;
  await clickButton("修改");
  await page.waitForSelector('[role="dialog"] input[name="username"]', { visible: true });
  await page.click('[role="dialog"] input[name="username"]', { clickCount: 3 });
  await page.keyboard.type(renamed);
  await clickButton("保存", '[role="dialog"]');
  await page.waitForFunction((name) => document.querySelector('[data-testid="profile-username"]')?.textContent === name, { timeout: 15000 }, renamed);
  await waitText("后可再次修改");
  if (!(await page.evaluate(() => [...document.querySelectorAll("main button")].find((b) => b.innerText.trim() === "修改")?.disabled))) {
    throw new Error("the username may change again at once");
  }
  await choosePicture(page, '[data-testid="avatar-input"]');
  await page.waitForSelector('[data-testid="profile-avatar"] img[src*="/uploads/avatars/"]', { timeout: 30000 });
  await page.waitForSelector('header img[src$="_64.webp"]', { timeout: 15000 });
  // The avatars are plain images: wait until both have come in (review GJ, F9).
  await page.waitForFunction(
    () => ['[data-testid="profile-avatar"] img', 'header img[src$="_64.webp"]'].every((s) => {
      const img = document.querySelector(s);
      return img?.complete && img.naturalWidth > 0;
    }),
    { timeout: 15000 },
  );
  const uploaded = await page.$eval('[data-testid="profile-avatar"] img', (img) => ({ src: img.getAttribute("src"), side: img.naturalWidth }));
  if (!uploaded.src?.endsWith(".webp") || uploaded.side !== 256) throw new Error(`the uploaded avatar: ${JSON.stringify(uploaded)}`);
  await shot("8b-profile");
  await clickButton("恢复默认");
  await clickButton("恢复默认", '[role="dialog"]');
  await page.waitForSelector('[data-testid="profile-avatar"] svg[data-avatar-default]', { visible: true, timeout: 15000 });
  await page.waitForSelector('header svg[data-avatar-default]', { visible: true });
  ok(`the profile: ${drawn} drawn at sign-up and the built-in avatar; renamed to ${renamed} (7 days to wait); a picture uploaded (${uploaded.side} px WebP, the top bar's 64 px) and back to the default`);

  // 8c. App downloads (design 2026-10-07, App download page, batches H3 and
  // H6): the page as the server has it (no app offered: "no app yet";
  // offered: a card each), the top bar's entry and the footer's link as the
  // console's switch says, also while no app is offered (the top bar's
  // panel then says none is yet); then with the answer of /v1/platform/apps
  // replaced by an uploaded Android app and an App Store link: a card each
  // with its QR code, the APK's facts and button, the store's button; the
  // top bar's entry opens a QR code for each, and the footer leads to the
  // page; then with the switch off: no entry, the page still opens.
  const served = await page.evaluate(async () => (await fetch("/v1/platform/apps")).json());
  const shown = served.entry?.visible ?? true;
  const entries = async () => [Boolean(await page.$('header [data-testid="download-menu"]')), Boolean(await page.$('footer a[href="/download"]'))];
  await go("/download");
  await page.waitForSelector('[data-testid="download-page"]', { visible: true, timeout: 20000 });
  if (!served.android && !served.ios) await waitText("暂未提供 App");
  else for (const p of ["android", "ios"]) if (served[p]) await page.waitForSelector(`[data-testid="app-${p}"]`, { visible: true });
  const [menu0, footer0] = await entries();
  if (menu0 !== shown || footer0 !== shown) throw new Error(`the console ${shown ? "shows" : "hides"} the download entries: the top bar's ${menu0}, the footer's ${footer0}`);
  if (shown && !served.android && !served.ios) {
    await page.hover('header [data-testid="download-menu"]');
    await page.waitForFunction(() => document.querySelector('[data-testid="download-qrs"]')?.innerText.includes("暂未提供 App"), { timeout: 10000 });
    await page.mouse.move(720, 700);
  }
  await withApps(page, APPS_OFFERED, async () => {
    await go("/download");
    await page.waitForSelector('[data-testid="app-android"]', { visible: true, timeout: 20000 });
    const cards = await page.evaluate(() =>
      ["android", "ios"].map((p) => {
        const card = document.querySelector(`[data-testid="app-${p}"]`);
        return { text: card?.innerText ?? "", qr: card?.querySelectorAll('[role="img"] svg').length ?? 0, href: card?.querySelector("a[href]")?.getAttribute("href") };
      }),
    );
    const [apk, store] = cards;
    for (const want of ["下载 APK", "48.2 MB", "Android 7.0 及以上", "1.2.0"]) if (!apk?.text.includes(want)) throw new Error(`the APK's card has no ${want}: ${apk?.text}`);
    if (!store?.text.includes("前往 App Store") || store.href !== APPS_OFFERED.ios.url) throw new Error(`the App Store card: ${JSON.stringify(store)}`);
    if (apk?.qr !== 1 || store?.qr !== 1) throw new Error(`each card has a QR code: ${apk?.qr}, ${store?.qr}`);
    await page.hover('header [data-testid="download-menu"]');
    await page.waitForSelector('[data-testid="download-qrs"]', { visible: true, timeout: 10000 });
    const menuQrs = await page.$$eval('[data-testid="download-qrs"] [role="img"] svg', (svgs) => svgs.length);
    if (menuQrs !== 2) throw new Error(`the top bar's download panel has ${menuQrs} QR codes`);
    await shot("8c-download");
    await page.mouse.move(720, 700);
    if (!(await page.$('footer a[href="/download"]'))) throw new Error("the footer does not lead to the download page");
  });
  await withApps(page, APPS_HIDDEN, async () => {
    await go("/download");
    await page.waitForSelector('[data-testid="download-page"]', { visible: true, timeout: 20000 });
    await waitText("暂未提供 App");
    const [menu, footer] = await entries();
    if (menu || footer) throw new Error(`the console hides the download entries, yet the top bar's ${menu} and the footer's ${footer} show`);
  });
  ok(`the download page: ${served.android || served.ios ? "the server's apps" : '"no app yet"'}, the entries ${shown ? "shown" : "hidden"} as the console says; with two apps, a card each (QR code, facts, button), the top bar's two QR codes and the footer's link; with the switch off no entry, the page still opens`);

  // 8d. Product lines (design 2026-10-07, product line switches, batch K2):
  // with the answer of /v1/platform/products replaced by spot and the
  // USDT-margined contracts closed (the server's switches untouched), the
  // top bar has no trade menu and its futures menu no USDT-margined entry;
  // the market list has no spot category, only coin-margined contracts and
  // no USDⓈ-M/COIN-M switch; the search finds only those; the closed
  // lines' terminals say they are not open while BTC-USD-PERP's opens, and
  // the bare /trade leads there; the assets page tells of the futures USDT
  // left from step 5, and the wind-down page offers to move it out.
  await withProducts(page, PRODUCTS_PAUSED, async () => {
    await go("/markets");
    await page.waitForSelector("main table tbody tr[data-row-id]", { visible: true, timeout: 20000 });
    const bar = await page.$$eval("header nav > div > a, header nav > a", (as) => as.map((a) => a.textContent.trim()));
    if (bar.includes("交易") || !bar.includes("合约")) throw new Error(`the top bar's menus with spot closed: ${bar.join(", ")}`);
    const menu = await page.$eval('header nav > div:has(> a[href^="/futures/"])', (m) => m.textContent);
    if (menu.includes("U本位合约") || !menu.includes("币本位合约") || !menu.includes("合约数据")) throw new Error(`the futures menu with USDⓈ-M closed: ${menu}`);
    const rail = await page.$$eval("main nav button[aria-pressed]", (bs) => bs.map((b) => b.innerText.split("\n")[0].trim()));
    if (rail.includes("现货") || !rail.includes("合约")) throw new Error(`the categories with spot closed: ${rail.join(", ")}`);
    const ids = await page.$$eval("main table tbody tr[data-row-id]", (trs) => trs.map((tr) => tr.dataset.rowId));
    const stray = ids.filter((id) => !id.endsWith("-USD-PERP"));
    if (stray.length > 0) throw new Error(`the market list shows closed lines' markets: ${stray.slice(0, 5).join(", ")}`);
    await page.$$eval("main nav button[aria-pressed]", (bs) => bs.find((b) => b.innerText.split("\n")[0].trim() === "合约")?.click());
    await page.waitForFunction(() => document.querySelector('main nav button[aria-pressed="true"]')?.innerText.startsWith("合约"), { timeout: 10000 });
    if (await page.$('main [role="radiogroup"][aria-label="合约"]')) throw new Error("the futures category switches between USDⓈ-M and COIN-M while USDⓈ-M is closed");
    await page.click('header button[aria-label="搜索币种"]');
    await page.waitForSelector('[role="dialog"] input', { visible: true, timeout: 10000 });
    await page.type('[role="dialog"] input', "BTC");
    await page.waitForFunction(() => document.querySelector('[role="dialog"] [role="option"][data-symbol="BTC-USD-PERP"]'), { timeout: 10000 });
    const found = await page.$$eval('[role="dialog"] [role="option"]', (os) => os.map((o) => o.dataset.symbol));
    if (found.some((s) => !s.endsWith("-USD-PERP"))) throw new Error(`the search finds closed lines' markets: ${found.join(", ")}`);
    await page.keyboard.press("Escape");
    for (const [path, line] of [["/trade/BTC-USDT", "币币交易"], ["/futures/BTC-USDT-PERP", "U 本位合约"]]) {
      await go(path);
      await page.waitForSelector('[data-testid="product-closed"]', { visible: true, timeout: 20000 });
      await waitText(`${line}暂未开放`);
    }
    await shot("8d-closed");
    await go("/futures/BTC-USD-PERP");
    await waitText("标记价格");
    if (await page.$('[data-testid="product-closed"]')) throw new Error("the coin-margined terminal says it is not open");
    // The bare /trade leads to an open line (F18 ④).
    await go("/trade");
    await page.waitForFunction(() => location.pathname === "/futures/BTC-USD-PERP", { timeout: 20000 });
    await go("/assets");
    await page.waitForSelector('[data-testid="wind-down-notice"]', { visible: true, timeout: 20000 });
    const notice = await page.$eval('[data-testid="wind-down-notice"]', (n) => n.innerText);
    if (!notice.includes("合约账户余额")) throw new Error(`the wind-down notice: ${notice}`);
    await page.click('[data-testid="wind-down-notice"] a[href="/assets/closed"]');
    await page.waitForSelector('[data-testid="closed-products"] a[href="/assets/transfer?from=FUTURES&asset=USDT"]', { visible: true, timeout: 20000 });
    await shot("8d-wind-down");
  });
  ok("with spot and USDⓈ-M closed: no trade menu nor USDⓈ-M entry, no spot category or switch, only coin-margined markets listed and found, their terminals not open, the futures USDT to move out under 待处置");

  // 9. Settings: English switches the site's language at once; so does
  // Traditional Chinese (design 2026-10-06 繁体中文), shown on the key pages
  // in the Traditional fonts (screenshots to check the widths), and back.
  await go("/account/settings");
  await page.waitForSelector('button[role="radio"][value="en"]', { visible: true });
  await page.click('button[role="radio"][value="en"]');
  await page.waitForFunction(() => document.querySelector("header")?.innerText.includes("Markets"), { timeout: 10000 });
  await page.click('button[role="radio"][value="zh-TW"]');
  await page.waitForFunction(() => document.documentElement.lang === "zh-TW" && document.querySelector("header")?.innerText.includes("資產"), { timeout: 10000 });
  for (const [path, text] of [["/", "漲幅榜"], ["/markets", "即時行情"], ["/trade/BTC-USDT", "限價"], ["/assets", "資產總覽"], ["/help", "幫助中心"]]) {
    await go(path);
    await waitText(text);
    await shot(`zh-TW${path === "/" ? "-home" : path.replaceAll("/", "-")}`);
  }
  const fonts = await page.evaluate(() => getComputedStyle(document.documentElement).fontFamily);
  if (!fonts.includes("PingFang TC")) throw new Error(`Traditional Chinese without its fonts: ${fonts}`);
  await go("/account/settings");
  await page.waitForSelector('button[role="radio"][value="zh-CN"]', { visible: true });
  await page.click('button[role="radio"][value="zh-CN"]');
  await page.waitForFunction(() => document.documentElement.lang === "zh-CN" && document.querySelector("header")?.innerText.includes("资产"), { timeout: 10000 });
  ok("the language setting switches the site to English, to Traditional Chinese (five key pages, its fonts) and back");

  // 10. Sign out.
  await go("/");
  await page.waitForSelector('header a[href="/account/profile"]', { visible: true });
  await page.hover('header a[href="/account/profile"]');
  await clickButton("退出登录");
  await waitText("注册", 10000);
  ok("sign out ends the session");
} catch (e) {
  await t.fail(e);
}

await t.finish();
