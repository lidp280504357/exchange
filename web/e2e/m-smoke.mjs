// Browser smoke test of the mobile site (design 2026-09-30 §7, §12.2), run
// by scripts/e2e/web.sh (task e2e):
//
//   APP=https://m.astras.vip node web/e2e/m-smoke.mjs
//
// Drives the deployed site as a phone would (390 x 844, touch, an iPhone
// user agent, so nginx does not send it to the PC site), in Chinese: a
// visitor's home and terms pages without a failed request, sign up
// through the form, the welcome funds on the assets tab, the "me" tab's
// cards signed in and out (sign-out is there), sign back in, the market
// list and its search, the first screens of the home, markets, coin,
// assets and terms pages without a failed request, a limit order
// placed from the spot terminal's order sheet and cancelled from its open
// orders, a transfer to futures and its ledger entry, a deposit address,
// the futures terminal, the candle charts' legends clear of the highest
// candle (there and on the coin page), the futures data (the terminal's
// 数据 tab, the futures category, /futures/data), notifications, devices, help, the profile
// (the drawn username, a rename, an avatar uploaded and removed), the App download page,
// closed product lines (hidden, their terminals not open, the wind-down page),
// the language switch, a first visit's language and sign-out. Script errors fail the run; every API response is checked
// against the OpenAPI contracts (lib.mjs). Screenshots go to SHOTS when set.
import { APPS_HIDDEN, APPS_OFFERED, PRODUCTS_PAUSED, choosePicture, firstVisitLocale, legendClear, ok, pickLanguage, sleep, start, withApps, withProducts } from "./lib.mjs";

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
  await page.waitForSelector('[data-testid="me-welcome"]', { visible: true, timeout: 10000 });
}

try {
  // 0. A visitor's home and terms pages ask for nothing that fails either
  // (B117, B118: the user saw the hero's 404 signed out).
  const visitorFailing = [];
  for (const path of ["/", "/legal/terms"]) {
    for (const f of await t.firstScreenFailures(path, null)) visitorFailing.push(`${path}: ${f}`);
  }
  if (visitorFailing.length) throw new Error(`requests failed while a visitor's first screens came up: ${visitorFailing.join("; ")}`);
  ok("a visitor's home and terms pages come up without a failed request");

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
  // Margin trading for this user alone (web.sh puts the switch back).
  const marginOn = await t.openMargin();
  if (marginOn) ok("margin trading is open to this user (on for everyone, or opened for it alone)");
  await shot("1-assets");

  // 2. The "me" tab (design §7.3) signed in, then out; sign back in with the password.
  const cards = async (ids) => {
    for (const id of ids) await page.waitForSelector(`[data-testid="${id}"]`, { visible: true, timeout: 20000 });
  };
  await go("/me");
  await cards(["me-identity", "me-assets", "me-quick", "me-guard"]);
  ok('the "me" tab shows the identity, assets, shortcuts and security cards');
  await shot("1-me");
  await signOut();
  await cards(["me-welcome", "me-glance", "me-quick"]);
  ok('signed out, the "me" tab welcomes the visitor with a glance at the markets');
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
  // Classic"): a few rows, ETH's among them (with Binance's perpetuals, design
  // 2026-10-06 §3.4, five pairs and six contracts once every contract trades).
  await page.waitForFunction(
    (sel) => {
      const list = [...document.querySelectorAll(sel)].map((el) => el.innerText);
      return list.length > 0 && list.length <= 16 && list.some((r) => r.includes("ETH"));
    },
    { timeout: 10000 },
    ROWS,
  );
  ok(`the market list shows the markets and the search narrows them (${(await rows()).length} rows for ETH)`);
  await shot("2-markets");

  // 3b. The first screens of the home, markets, coin, assets and terms
  // pages ask for nothing that fails (B117: the hero's 404 was red in the
  // browser's console; an article the console has not published is read
  // from its draft without asking for it).
  const failing = [];
  for (const [path, ready] of [
    ["/", null],
    ["/markets", ROWS],
    ["/coin/BTC", '[data-testid="candle-plot"] canvas'],
    ["/assets", '[data-testid="assets-total"]'],
    ["/legal/terms", null],
  ]) {
    for (const f of await t.firstScreenFailures(path, ready)) failing.push(`${path}: ${f}`);
  }
  if (failing.length) throw new Error(`requests failed while first screens came up: ${failing.join("; ")}`);
  ok("the home, markets, coin, assets and terms pages come up without a failed request");

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

  // 5b. The margin accounts (margin design 2026-10-06 §7): the cross account
  // card with its gauge. While margin trading is open to the user, 10 USDT
  // moves into the cross account from the sheet and back out; while it is
  // not, the page says so.
  await go("/assets/margin");
  await page.waitForSelector('[data-testid="margin-account-MARGIN_CROSS"]', { visible: true, timeout: 20000 });
  await page.waitForSelector('[data-testid="margin-account-MARGIN_CROSS"] [data-testid="margin-level"]', { visible: true });
  const marginClosed = await page.evaluate(() => document.body.innerText.includes("杠杆交易 · "));
  if (marginClosed && marginOn) throw new Error("margin trading was opened for this user, but the margin page says it is not open");
  if (marginClosed) {
    ok("the margin page shows the cross account and says margin trading is not open to this user");
  } else {
    // The sheet slides in (its fields are below the screen until it has)
    // and its overlay takes taps until it has slid away.
    const marginTransfer = async (direction, amount, done) => {
      await page.click('button[data-testid="margin-transfer"]');
      await sheetOpen();
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
    await page.waitForFunction(() => document.querySelector('[data-testid="margin-account-MARGIN_CROSS"] ul')?.innerText.includes("USDT"), { timeout: 20000 });
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
    await page.waitForFunction((sel) => /(^|[^\d.,])10\.00(?!\d)/.test(document.querySelector(sel)?.innerText ?? ""), { timeout: 20000 }, '[data-testid="margin-entry"]');
    await go("/assets/margin");
    await page.waitForSelector('[data-testid="margin-account-MARGIN_CROSS"]', { visible: true, timeout: 20000 });
    await marginTransfer("OUT", "10", "已划出 10 USDT");
    ok("10 USDT moves into the cross margin account from its sheet, shows in its coins (pushed on the margin channel) and in the assets overview's total, and moves back");
    // The order sheet trades from the cross account once chosen above the
    // form, with its margin level and what it may borrow; back to spot.
    await go("/trade/BTC-USDT");
    await clickButton("买入 BTC");
    await sheetOpen();
    await page.waitForSelector('[role=dialog] [data-testid="margin-bar"]', { visible: true, timeout: 20000 });
    await clickButton("全仓", '[role=dialog] [data-testid="margin-bar"]');
    await page.waitForSelector('[role=dialog] [data-testid="margin-info"] [data-testid="margin-borrowable"]', { visible: true, timeout: 10000 });
    await clickButton("现货", '[role=dialog] [data-testid="margin-bar"]');
    await page.waitForSelector('[role=dialog] [data-testid="margin-info"]', { hidden: true, timeout: 10000 });
    await page.keyboard.press("Escape");
    await page.waitForSelector("[role=dialog]", { hidden: true });
    ok("the order sheet switches to the cross margin account, showing what it may borrow, and back");
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
  const futuresChart = await clearOf("the futures terminal");
  // The coin page's daily candles (the user's report: three lines of
  // legend over a 232 px pane covered the highest candles).
  await go("/coin/ETH");
  const daily = await page.waitForFunction(
    () => [...document.querySelectorAll("button")].find((b) => b.innerText.trim() === "1日" && b.offsetParent !== null) ?? false,
    { timeout: 20000 },
  );
  if ((await daily.evaluate((b) => b.getAttribute("aria-pressed"))) !== "true") await daily.click();
  // Daily candles are dated without a time in the legend.
  await page.waitForFunction(() => /^\d{4}-\d{2}-\d{2}(?!\s*\d{1,2}:\d{2})/.test(document.querySelector('[data-testid="candle-legend"]')?.innerText ?? ""), {
    timeout: 20000,
  });
  ok(`the candle charts start below their legends (${futuresChart}; ${await clearOf("the ETH coin page, daily")})`);

  // 7b. Futures data (design 2026-10-06 §3.3, batch F4): the terminal's
  // 数据 tab draws the contract's seven statistics and lists its
  // liquidations, the platform coin's contract has none (and nothing is
  // asked for it), the market list's futures category shows each
  // contract's funding and open interest, and /futures/data opens a
  // contract's board in a sheet.
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
    await shot("7b-futures-data");
    const astra = futuresReads.length;
    await go("/futures/ASTRA-USDT-PERP");
    await clickButton("数据");
    await waitText("暂无数据");
    await sleep(1500);
    const asked = futuresReads.slice(astra).filter((r) => r.includes("ASTRA"));
    if (asked.length) throw new Error(`the platform coin's contract was asked for futures data: ${asked.join("; ")}`);
    ok("the futures terminal's 数据 tab draws the seven statistics and the liquidations, and asks nothing for ASTRA");

    await go("/markets?cat=futures");
    await page.waitForFunction(() => [...document.querySelectorAll("a")].some((a) => a.innerText.includes("BTCUSDT") && /费率\s*[+-]?\d+\.\d{4}%/.test(a.innerText)), {
      timeout: 20000,
    });
    // Searched for BTC: the windowed list draws its first rows only, and on a
    // cold service the contracts without figures yet could push BTCUSDT
    // below them (review R25, F8). A row is a button in the plain list and
    // in the windowed one.
    await go("/futures/data?q=BTC");
    const row = await page.waitForFunction(() => [...document.querySelectorAll("button")].find((b) => b.innerText.includes("BTCUSDT") && b.innerText.includes("费率")) ?? false, {
      timeout: 20000,
    });
    await row.click();
    await sheetOpen();
    await drawn("[role=dialog]");
    await shot("7b-futures-overview");
    await page.keyboard.press("Escape");
    await page.waitForSelector("[role=dialog]", { hidden: true, timeout: 5000 });
    ok("the market list's futures category shows funding and open interest; /futures/data opens a contract's board in a sheet");
  } finally {
    page.off("response", onFuturesRead);
  }

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

  // 8b. The profile (design 2026-10-07, avatars and usernames, batch I2):
  // "me" calls the user by the username drawn at sign-up, with the
  // built-in avatar of its ID; settings lead to the profile page, where a
  // new name (its sheet) shows at once and starts the 7 days. A picture
  // made in the page (600 × 900 JPEG) is shrunk to its middle square and
  // uploaded, and "me" shows the server's WebP; "use default" (after its
  // sheet) brings the built-in one back.
  await go("/me");
  await page.waitForSelector('[data-testid="me-identity"] [data-testid="my-username"]', { visible: true, timeout: 20000 });
  const drawn = await page.$eval('[data-testid="me-identity"] [data-testid="my-username"]', (e) => e.textContent);
  if (!/^user_[a-z0-9]{8}$/.test(drawn ?? "")) throw new Error(`the username drawn at sign-up is ${drawn}`);
  await page.waitForSelector('[data-testid="me-identity"] svg[data-avatar-default]', { visible: true });
  await go("/account/settings");
  await page.waitForSelector('[data-testid="settings-profile"]', { visible: true, timeout: 20000 });
  await page.click('[data-testid="settings-profile"]');
  await waitPath("/account/profile");
  await page.waitForSelector('[data-testid="profile-username"]', { visible: true, timeout: 20000 });
  const renamed = `phone_${String(run).slice(-8)}`;
  await page.click('[data-testid="profile-username-row"]');
  await sheetOpen();
  await page.click('[role="dialog"] input[name="username"]', { clickCount: 3 });
  await page.keyboard.type(renamed);
  await clickButton("保存", "[role=dialog]");
  await page.waitForFunction((name) => document.querySelector('[data-testid="profile-username"]')?.textContent === name, { timeout: 15000 }, renamed);
  await waitText("后可再次修改");
  if (!(await page.$eval('[data-testid="profile-username-row"]', (b) => b.disabled))) throw new Error("the username may change again at once");
  await choosePicture(page, '[data-testid="avatar-input"]', { width: 600, height: 900, type: "image/jpeg" });
  await page.waitForSelector('[data-testid="profile-avatar"] img[src*="/uploads/avatars/"]', { timeout: 30000 });
  await go("/me");
  await page.waitForSelector('[data-testid="me-identity"] img[src*="/uploads/avatars/"]', { timeout: 20000 });
  // A plain image: wait until it has come in (review GJ, F9).
  await page.waitForFunction(
    () => {
      const img = document.querySelector('[data-testid="me-identity"] img');
      return img?.complete && img.naturalWidth > 0;
    },
    { timeout: 15000 },
  );
  const mine = await page.$eval('[data-testid="me-identity"] [data-testid="my-username"]', (e) => e.textContent);
  if (mine !== renamed) throw new Error(`"me" calls the user ${mine}, not ${renamed}`);
  await shot("8b-me");
  await go("/account/profile");
  await page.waitForSelector('[data-testid="profile-avatar"] img[src*="/uploads/avatars/"]', { timeout: 20000 });
  await clickButton("恢复默认");
  await sheetOpen();
  await clickButton("恢复默认", "[role=dialog]");
  await page.waitForSelector('[data-testid="profile-avatar"] svg[data-avatar-default]', { visible: true, timeout: 15000 });
  ok(`the profile: ${drawn} drawn at sign-up and the built-in avatar on "me"; renamed to ${renamed} (7 days to wait); a picture uploaded, shown on "me", and back to the default`);

  // 8c. App downloads (design 2026-10-07, App download page, batches H3 and
  // H6): the page as the server has it (no app offered: "no app yet";
  // offered: a card each), the download row on "me" as the console's
  // switch says, also while no app is offered; then with the answer of
  // /v1/platform/apps replaced by an uploaded Android app and an App Store
  // link: this iPhone's platform first, marked as this phone, with the
  // store's button; the APK's card with its button and install steps; and
  // the download row on "me"; then with the switch off: no row, the page
  // still opens.
  const served = await page.evaluate(async () => (await fetch("/v1/platform/apps")).json());
  const shown = served.entry?.visible ?? true;
  await go("/download");
  await page.waitForSelector('[data-testid="download-page"]', { visible: true, timeout: 20000 });
  if (!served.android && !served.ios) await waitText("暂未提供 App");
  else for (const p of ["android", "ios"]) if (served[p]) await page.waitForSelector(`[data-testid="app-${p}"]`, { visible: true });
  await go("/me");
  await page.waitForSelector('[data-testid="me-identity"]', { visible: true, timeout: 20000 });
  if (Boolean(await page.$('a[href="/download"]')) !== shown) throw new Error(`"me" ${shown ? "lacks" : "has"} the download row while the console ${shown ? "shows" : "hides"} it`);
  await withApps(page, APPS_OFFERED, async () => {
    await go("/download");
    await page.waitForSelector('[data-testid="app-android"]', { visible: true, timeout: 20000 });
    const cards = await page.$$eval('[data-testid^="app-"]', (all) => all.map((c) => ({ id: c.dataset.testid, text: c.innerText })));
    if (cards[0]?.id !== "app-ios" || !cards[0].text.includes("本机") || !cards[0].text.includes("前往 App Store")) {
      throw new Error(`the iPhone's own platform does not lead: ${JSON.stringify(cards)}`);
    }
    for (const want of ["下载 APK", "48.2 MB", "安装说明"]) if (!cards[1]?.text.includes(want)) throw new Error(`the APK's card has no ${want}`);
    await shot("8c-download");
    await go("/me");
    await page.waitForSelector('a[href="/download"]', { visible: true, timeout: 20000 });
  });
  await withApps(page, APPS_HIDDEN, async () => {
    await go("/me");
    await page.waitForSelector('[data-testid="me-identity"]', { visible: true, timeout: 20000 });
    if (await page.$('a[href="/download"]')) throw new Error('"me" has the download row while the console hides it');
    await go("/download");
    await page.waitForSelector('[data-testid="download-page"]', { visible: true, timeout: 20000 });
    await waitText("暂未提供 App");
  });
  ok(`the download page: ${served.android || served.ios ? "the server's apps" : '"no app yet"'}, the row on "me" ${shown ? "shown" : "hidden"} as the console says; with two apps, this iPhone's first, the APK's steps, the row on "me"; with the switch off no row, the page still opens`);

  // 8d. Product lines (design 2026-10-07, product line switches, batch K2):
  // with the answer of /v1/platform/products replaced by spot and the
  // USDT-margined contracts closed (the server's switches untouched), the
  // market list has no spot pill, only coin-margined contracts (searched
  // too) and no USDⓈ-M/COIN-M switch; the trade tab leads to a
  // coin-margined terminal; the closed lines' terminals say they are not
  // open while BTC-USD-PERP's opens without the switch to USDⓈ-M; the
  // assets tab tells of the futures USDT left from step 5, and the
  // wind-down page offers to move it out.
  await withProducts(page, PRODUCTS_PAUSED, async () => {
    await go("/markets");
    await page.waitForFunction((sel) => document.querySelectorAll(sel).length >= 1, { timeout: 20000 }, ROWS);
    const PILLS = '[role="group"][aria-label="分类"] button';
    const pills = await page.$$eval(PILLS, (bs) => bs.map((b) => b.innerText.trim()));
    if (pills.includes("现货") || !pills.includes("合约")) throw new Error(`the category pills with spot closed: ${pills.join(", ")}`);
    const hrefs = () => page.$$eval(ROWS, (els) => els.map((el) => el.querySelector("a[href]")?.getAttribute("href") ?? ""));
    const listed = (await hrefs()).filter((h) => !h.endsWith("-USD-PERP"));
    if (listed.length > 0) throw new Error(`the market list shows closed lines' markets: ${listed.slice(0, 5).join(", ")}`);
    await typeInto('input[placeholder="搜索币种名称或代码"]', "BTC");
    await page.waitForFunction((sel) => [...document.querySelectorAll(sel)].some((el) => el.querySelector('a[href="/futures/BTC-USD-PERP"]')), { timeout: 10000 }, ROWS);
    const found = (await hrefs()).filter((h) => !h.endsWith("-USD-PERP"));
    if (found.length > 0) throw new Error(`the search finds closed lines' markets: ${found.join(", ")}`);
    await page.$$eval(PILLS, (bs) => bs.find((b) => b.innerText.trim() === "合约")?.click());
    await page.waitForFunction((sel) => [...document.querySelectorAll(sel)].some((b) => b.innerText.trim() === "合约" && b.getAttribute("aria-pressed") === "true"), { timeout: 10000 }, PILLS);
    if (await page.$('[role="radiogroup"][aria-label="合约"]')) throw new Error("the futures category switches between USDⓈ-M and COIN-M while USDⓈ-M is closed");
    const tab = await page.$$eval("a[href]", (as) => as.find((a) => a.innerText.trim() === "交易")?.getAttribute("href"));
    if (!tab?.startsWith("/futures/") || !tab.endsWith("-USD-PERP")) throw new Error(`the trade tab leads to ${tab}`);
    for (const [path, line] of [["/trade/BTC-USDT", "币币交易"], ["/futures/BTC-USDT-PERP", "U 本位合约"]]) {
      await go(path);
      await page.waitForSelector('[data-testid="product-closed"]', { visible: true, timeout: 20000 });
      await waitText(`${line}暂未开放`);
    }
    await shot("8d-closed");
    await go("/futures/BTC-USD-PERP");
    await waitText("标记价格");
    if (await page.$('[data-testid="product-closed"]')) throw new Error("the coin-margined terminal says it is not open");
    const kinds = await page.$$eval('[role="radiogroup"]', (gs) => gs.filter((g) => g.textContent.includes("U 本位") && g.textContent.includes("币本位")).length);
    if (kinds > 0) throw new Error("the terminal switches to USDⓈ-M while USDⓈ-M is closed");
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
  ok("with spot and USDⓈ-M closed: no spot pill or switch, only coin-margined markets listed and found, the trade tab to one of them, the closed terminals not open, the futures USDT to move out under 待处置");

  // 9. Settings: English switches the site's language at once (the page
  // and its header); so does Traditional Chinese (design 2026-10-06
  // 繁体中文), shown on the key pages in the Traditional fonts
  // (screenshots to check the widths), and back. The language is a dropdown
  // of each language's own and English name (F30).
  await go("/account/settings");
  await pickLanguage(page, "语言", "English");
  await waitText("Time zone", 10000);
  await pickLanguage(page, "Language", "繁體中文");
  await waitText("時區", 10000);
  for (const [path, text] of [["/", "總資產估值"], ["/markets", "現貨"], ["/trade/BTC-USDT", "買入"], ["/assets", "總資產估值"], ["/help", "幫助中心"]]) {
    await go(path);
    await waitText(text);
    await shot(`zh-TW${path === "/" ? "-home" : path.replaceAll("/", "-")}`);
  }
  const fonts = await page.evaluate(() => getComputedStyle(document.documentElement).fontFamily);
  if (!fonts.includes("PingFang TC")) throw new Error(`Traditional Chinese without its fonts: ${fonts}`);
  await go("/account/settings");
  await pickLanguage(page, "語言", "简体中文");
  await waitText("时区", 10000);
  ok("the language setting switches the site to English, to Traditional Chinese (five key pages, its fonts) and back");

  // 9b. A first visit's language (F30, F33): the first of the browser's languages the site has,
  // else the platform's fallback language, already on the first screen.
  const fallbackLocale = (await (await fetch(`${API}/v1/platform/profile`)).json()).default_locale;
  for (const [tags, want] of [
    [["ja"], fallbackLocale],
    [["zh-HK", "en"], "zh-TW"],
    [["fr", "en-GB"], "en"],
  ]) {
    const got = await firstVisitLocale(page, APP, "/", tags, phone);
    if (got !== want) throw new Error(`a first visit with ${tags.join(",")} starts in ${got}, not ${want}`);
  }
  ok(`a first visit starts in its browser's language, else the platform's fallback (${fallbackLocale}): ja → ${fallbackLocale}, zh-HK → zh-TW, fr,en-GB → en`);

  // 10. Sign out.
  await signOut();
  ok("sign out ends the session");
} catch (e) {
  await t.fail(e);
}

await t.finish();
