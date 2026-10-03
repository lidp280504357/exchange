// Browser smoke test of the admin console (admin.astras.vip, design §10
// and design 2026-10-02): an administrator signs in (the flag
// admin.login_without_totp is on in the test environment) and walks every
// section: the overview with the services' health and HOUSE, users with a
// user's page and its tabs (profile, security, risk …), the identity
// requests, orders and trades, deposits (those to handle, the backfills,
// the backfill form), the withdrawal queue and a withdrawal's detail, the
// custodians (the stand-in UDUNMOCK's page, its callbacks and the custody
// fees), assets and pairs (a status
// change is confirmed and canceled, never done; a pair's editor; the
// listing wizard's preview, canceled), futures, every user's positions,
// the liquidation log, HOUSE, the flags, the ledger's reconciliation, the
// audit trail (an entry's detail, the CSV export), the reports, the
// administrators (the roles' permissions; the creation form, canceled),
// system health, the operations pages, the simulated market (overview,
// price control with an event's impact, never started; events, bots, the
// coin's holders; the bots' orders), the fund operations (approval mode,
// form, records), the settings and the event stream, the account page; the
// search opens a user; signing out from the account menu ends the session,
// and the setup page without a link says so. Every admin API response is
// checked against api/admin/admin.yaml.
//
//   ADMIN_EMAIL=... ADMIN_PASSWORD=... node admin-smoke.mjs
//
// APP is the console (https://admin.astras.vip by default). scripts/e2e/
// web.sh creates a throwaway administrator for it.
import { ok, sleep, start } from "./lib.mjs";

const APP = process.env.APP ?? "https://admin.astras.vip";
const EMAIL = process.env.ADMIN_EMAIL;
const PASSWORD = process.env.ADMIN_PASSWORD;
if (!EMAIL || !PASSWORD) {
  console.log("SKIP admin console: ADMIN_EMAIL and ADMIN_PASSWORD are not set");
  process.exit(0);
}

const t = await start({ app: APP, api: APP, name: "admin", device: { viewport: { width: 1440, height: 900 } }, apiPrefix: "/admin/v1/" });
const { page, go, waitText, waitPath, clickButton, typeInto } = t;

/** rows waits for at least n rows in the page's (first) table body. */
const rows = (n, scope = "main") =>
  page.waitForFunction((s, k) => document.querySelectorAll(`${s} tbody tr`).length >= k && !document.querySelector(`${s} [aria-busy=true]`), { timeout: 30000 }, scope, n);
const noError = async (what) => {
  const text = await page.evaluate(() => document.querySelector("main")?.innerText ?? "");
  if (/重试|出错了/.test(text) && /错误|失败|不可用/.test(text)) throw new Error(`${what} shows an error`);
};

try {
  // 1. Sign in with the password alone.
  await go("/login");
  // The form waits for its options (whether to ask for the code).
  await page.waitForFunction(() => document.querySelector("form button[type=submit]")?.disabled === false, { timeout: 20000 });
  await typeInto('input[autocomplete="username"]', EMAIL);
  await typeInto('input[autocomplete="current-password"]', PASSWORD);
  await page.keyboard.press("Enter");
  await waitPath("/");
  ok("signs in with the password (admin.login_without_totp)");

  // 2. Overview: the figures, every service ready, HOUSE.
  await waitText("注册用户");
  await page.waitForFunction(() => /\d+ 个服务全部就绪|\d+ 个服务未就绪/.test(document.body.innerText), { timeout: 30000 });
  const health = await page.evaluate(() => document.body.innerText.match(/\d+ 个服务(全部就绪|未就绪)/)?.[0]);
  await waitText("库存估值");
  await page.waitForSelector("main svg[role=img]");
  await t.shot("1-overview");
  ok(`the overview: figures, trend chart, ${health}, HOUSE`);

  // 3. Users: the list; a row opens the user's page with its tabs.
  await go("/users");
  await rows(3);
  await t.clickLive("main tbody tr");
  await page.waitForFunction(() => /^\/users\/[0-9a-f-]{36}$/.test(location.pathname), { timeout: 20000 });
  const userId = await page.evaluate(() => location.pathname.split("/").pop());
  await waitText("UID");
  await waitText("最近登录");
  await waitText("已同意的文件");
  await clickButton("安全", "main");
  await waitText("身份验证器");
  await waitText("登录记录");
  await noError("the security tab");
  await clickButton("余额与资金", "main");
  await waitText("总估值");
  await waitText("风控冻结");
  await waitText("调整余额");
  await clickButton("订单", "main");
  await waitText("合约当前委托");
  await clickButton("仓位", "main");
  await sleep(1000);
  await noError("the positions tab");
  for (const tab of ["成交", "提现", "风控", "备注与标签", "审计"]) await clickButton(tab, "main");
  await waitText("UID");
  await t.shot("2-user");
  ok(`users: the list, a user's page (${userId.slice(0, 8)}…) with its tabs (profile, security, risk …)`);

  // 3b. The identity requests (the queue waiting for a decision).
  await go("/identity-requests");
  await waitText("身份变更申请");
  await sleep(1000);
  await noError("identity requests");
  ok("the identity requests");

  // 4. The search opens the same user; the drawer's old address leads there too.
  await go("/");
  await typeInto(`header input`, userId);
  await page.keyboard.press("Enter");
  await waitPath(`/users/${userId}`);
  await go(`/orders?user=${userId}`);
  await waitPath(`/users/${userId}`);
  ok("the search finds a user by ID; ?user= leads to the user's page");

  // 5. Orders and trades (HOUSE shows as a party).
  await go("/orders");
  await rows(1);
  await clickButton("成交");
  await rows(1);
  await noError("trades");
  ok("orders and trades");

  // 6. Deposits (every one, those to handle, the backfills waiting for
  // their callback, the backfill form up to its check) and the withdrawal
  // queue with its filters.
  await go("/deposits");
  await rows(1);
  await go("/deposits?view=attention");
  await waitText("待处理充值");
  await sleep(1000);
  await noError("deposits to handle");
  await go("/deposits?view=manual");
  await waitText("回调到来时自动核对");
  await sleep(1000);
  await noError("backfills waiting for their callback");
  await clickButton("补记充值", "main");
  await waitText("托管方交易号");
  await waitText("系统无法向托管方查询交易");
  await t.shot("2a-backfill");
  await page.keyboard.press("Escape");
  await page.waitForFunction(() => !document.querySelector("[role=dialog]"));
  await go("/withdrawals?held=false&min_risk=0");
  await sleep(1500);
  await noError("withdrawals");
  // A withdrawal's detail: its address book record and the user's others.
  await go("/withdrawals?status=ALL");
  await rows(1);
  await t.clickLive("main tbody tr");
  await waitText("地址簿");
  await waitText("该用户最近的提现");
  await page.waitForFunction(() => getComputedStyle(document.querySelector("[role=dialog]")).transform === "none");
  await t.shot("2b-withdrawal");
  await page.keyboard.press("Escape");
  await page.waitForFunction(() => !document.querySelector("[role=dialog]"));
  ok("deposits (to handle, backfills, the backfill form), the withdrawal queue with its filters and a withdrawal's detail");

  // 6b. The custodians. UDUN's page loads whichever gateway it talks to
  // (the stand-in until the real switch); the stand-in custodian UDUNMOCK
  // (ADR-0017, the hidden test asset TUSD) in full: reachable, its coin, its
  // own reconciliation row and callbacks, a callback's request as received
  // with the addresses it came from; then the custodians' fees.
  await go("/custody");
  await waitText("对账（不变量 4）");
  await noError("UDUN's custody page");
  await go("/custody?provider=UDUNMOCK");
  await waitText("替身托管方 UDUNMOCK 只服务");
  await waitText("可访问");
  await waitText("TQQCuyVcUEknTGyfSRKhcUuLZfEe93qWpy".slice(0, 12));
  await waitText("托管方 · UDUNMOCK");
  await rows(1, 'main table[aria-label="custody callbacks"]');
  await t.clickLive('main table[aria-label="custody callbacks"] tbody tr');
  await page.waitForSelector("[role=dialog]");
  await waitText("原始请求");
  await waitText("来源地址");
  await page.keyboard.press("Escape");
  await page.waitForFunction(() => !document.querySelector("[role=dialog]"));
  await t.shot("2c-custody");
  await waitText("托管方手续费");
  await clickButton("全部", 'main [aria-label="状态"]');
  await page.waitForSelector('main table[aria-label="custody fees"]');
  await page.waitForFunction(() => !document.querySelector('main table[aria-label="custody fees"][aria-busy=true]'), { timeout: 20000 });
  await noError("the custody fees");
  ok("the custodians: UDUN's page; the stand-in UDUNMOCK reachable, its coin, reconciliation, a callback as received with its addresses; the fees");

  // 7. Pairs: 50 and more; a status change asks for a confirmation (canceled).
  await go("/instruments");
  await rows(50);
  await typeInto('main input[placeholder="搜索"]', "SOL-BTC");
  await page.waitForFunction(() => document.querySelectorAll("main tbody tr").length === 1);
  await clickButton("操作", "main");
  await page.waitForFunction(() => [...document.querySelectorAll("[role=menuitem]")].some((e) => e.textContent.includes("交易中")));
  await page.evaluate(() => [...document.querySelectorAll("[role=menuitem]")].find((e) => e.textContent.includes("交易中")).click());
  await page.waitForSelector("[role=dialog]");
  await waitText("SOL-BTC");
  await waitText("分钟生效"); // the server's preview: opening a pair waits
  await t.shot("3-confirm");
  await clickButton("取消", "[role=dialog]");
  await page.waitForFunction(() => !document.querySelector("[role=dialog]"));
  // A pair's editor, and the listing wizard's preview (canceled, nothing applied).
  await t.clickLive("main tbody tr");
  await waitText("价格保护带");
  await page.keyboard.press("Escape");
  await page.waitForFunction(() => !document.querySelector("[role=dialog]"));
  await go("/instruments?tab=wizard");
  await clickButton("填入示例", "main");
  await waitText("解析出 1 项");
  await clickButton("预览变化", "main");
  await page.waitForFunction(() => document.body.innerText.includes("确认生效") || document.body.innerText.includes("没有变化"), { timeout: 20000 });
  if (await page.$("[role=dialog]")) {
    await t.shot("3b-listing-preview");
    await clickButton("取消", "[role=dialog]");
  }
  await go("/instruments?tab=changes");
  await waitText("生效前任何管理员都可以取消");
  await noError("the pending changes");
  // An asset's drawer with its profile and logo (C4c).
  await go("/instruments?tab=assets");
  await typeInto('main input[placeholder="搜索"]', "ASTRA");
  await page.waitForFunction(() => document.querySelectorAll("main tbody tr").length === 1);
  await t.clickLive("main tbody tr");
  await page.waitForSelector("[data-testid=asset-profile]");
  await waitText("资料与图标");
  await page.keyboard.press("Escape");
  await page.waitForFunction(() => !document.querySelector("[role=dialog]"));
  ok("assets and pairs: 50+ pairs, a status change previewed by the server waits for its confirmation (canceled); the pending changes; a pair's editor; the listing wizard's preview; an asset's profile");

  // 8. Futures (every user's positions, the liquidation log), HOUSE, flags.
  await go("/derivatives");
  await waitText("BTC-USDT-PERP");
  await go("/positions");
  await page.waitForSelector('main table[aria-label="positions"]');
  await sleep(1000);
  await noError("positions");
  await go("/liquidations?days=7");
  await page.waitForSelector('main table[aria-label="liquidations"]');
  await sleep(1000);
  await noError("liquidations");
  await go("/house");
  await waitText("近 30 日盈亏");
  await waitText("敞口");
  await waitText("各交易对");
  await rows(3);
  // Every contract has a row, flat or not.
  await waitText("各合约净头寸");
  await waitText("ETH-USDT-PERP");
  await go("/risk");
  await waitText("market.house_liquidity");
  await t.shot("3b-house");
  ok("futures, every user's positions and the liquidation log, HOUSE (results, exposure, inventory, pairs, every contract's net position), and the flags");

  // 9. Ledger: the reconciliation; audit; reports.
  await go("/ledger");
  await clickButton("对账");
  await waitText("分录借贷平衡");
  await go("/audit");
  await rows(1);
  await page.click("main tbody tr");
  await page.waitForSelector("[data-testid=audit-detail]");
  await waitText("原始事件");
  await page.keyboard.press("Escape");
  await page.waitForFunction(() => !document.querySelector("[data-testid=audit-detail]"));
  const exported = await page.evaluate(async (actor) => {
    const r = await fetch(`/admin/v1/audit-logs/export?actor=${encodeURIComponent(actor)}`);
    const text = await r.text();
    return { status: r.status, type: r.headers.get("Content-Type"), header: text.split("\n")[0].replace(/^\uFEFF/, "").trim() };
  }, EMAIL);
  if (exported.status !== 200 || !exported.type?.startsWith("text/csv") || exported.header !== "occurred_at,event_type,actor,target,action,reason,details,event_id") {
    throw new Error(`audit export: ${JSON.stringify(exported)}`);
  }
  await go("/reports");
  await page.waitForSelector("main svg[role=img]");
  await t.shot("4-reports");
  for (const tab of ["用户增长", "HOUSE 盈亏"]) {
    await clickButton(tab, "main");
    await page.waitForFunction((s) => document.querySelector("main h2")?.textContent === s && document.querySelector("main svg[role=img]"), { timeout: 20000 }, tab);
  }
  await noError("the users' and HOUSE's reports");
  ok("the ledger's reconciliation, the audit trail with an entry's detail and its CSV export, and the reports (the users, HOUSE's result)");

  // 9b. System: the administrators (this one marked, the roles' permissions),
  // the creation form (canceled), every service's health with details.
  await go("/admins");
  await waitText(EMAIL);
  await waitText("你自己");
  await page.waitForSelector("[data-testid=role-matrix]");
  await waitText("admins.manage");
  await clickButton("新建管理员", "main");
  await page.waitForSelector("#new-admin-email");
  await clickButton("取消", "[role=dialog]");
  await page.waitForFunction(() => !document.querySelector("[role=dialog]"));
  await go("/health");
  await waitText("ledger-service");
  await waitText("Kafka 滞后");
  await waitText("对账");
  await waitText("行情源");
  await page.waitForFunction(() => /^[0-9a-f]{7}/.test([...document.querySelectorAll("main td")].map((td) => td.textContent).find((s) => /^[0-9a-f]{7}/.test(s)) ?? ""), { timeout: 20000 });
  await t.shot("4b-health");
  ok("the administrators with the roles' permissions and the creation form (canceled); system health with versions, Kafka lag, reconciliation and the feed");

  // 9c. Operations: the announcements, the editor with its preview (closed
  // unsaved), the help articles, the messages and their form (closed unsent).
  await go("/announcements");
  await page.waitForSelector('main table[aria-label="公告"]');
  await clickButton("新建公告", "main");
  await page.waitForSelector("#article-slug");
  // The drawer slides in: its tabs at the right edge take clicks once it stands.
  await page.waitForFunction(() => getComputedStyle(document.querySelector("[role=dialog]")).transform === "none");
  await page.type("#article-title-zh-CN", "冒烟测试");
  await page.type("#article-body-zh-CN", "## 小标题\n\n正文");
  await clickButton("预览", "[role=dialog]");
  await page.waitForSelector("[data-testid=article-preview] h2");
  await t.shot("4c-article");
  await page.keyboard.press("Escape");
  await page.waitForFunction(() => !document.querySelector("[role=dialog]"));
  await go("/help-articles");
  await page.waitForSelector('main table[aria-label="帮助中心"]');
  await go("/broadcasts");
  await page.waitForSelector('main table[aria-label="站内信"]');
  await clickButton("发送站内信", "main");
  await page.waitForSelector("#broadcast-user");
  await page.waitForFunction(() => getComputedStyle(document.querySelector("[role=dialog]")).transform === "none");
  await page.keyboard.press("Escape");
  await page.waitForFunction(() => !document.querySelector("[role=dialog]"));
  ok("operations: the announcements with the editor's preview (closed unsaved), the help articles, the messages and their form (closed unsent)");

  // 9d. The simulated market: the overview with its chart, price control
  // with an event's impact (the confirmation closed, nothing started), the
  // events, the bots and the coin's holders; the bots filter on orders.
  await go("/sim");
  await page.waitForSelector("[data-testid=sim-target]");
  await page.waitForSelector("main svg[role=img]", { timeout: 30000 });
  await t.shot("4d-sim");
  await go("/sim/control");
  await waitText("价格模型");
  await page.$eval("#sim-size", (el) => el.select());
  await page.type("#sim-size", "-5");
  await page.click("[data-testid=sim-event-start]");
  await page.waitForSelector("[data-testid=sim-impact]", { timeout: 20000 });
  await t.shot("4e-sim-impact");
  await clickButton("取消", "[role=dialog]");
  await page.waitForFunction(() => !document.querySelector("[role=dialog]"));
  await go("/sim/events");
  await waitText("排队与进行中");
  await page.waitForFunction(() => !document.querySelector("main [aria-busy=true]"), { timeout: 20000 });
  await noError("the events");
  await go("/sim/bots");
  await page.waitForSelector("[data-testid=sim-role-MAKER]");
  await rows(1);
  await waitText("sim.enabled");
  await t.shot("4f-sim-bots");
  await go("/sim/token");
  await page.waitForSelector("[data-testid=sim-token-bots]", { timeout: 30000 });
  await page.waitForSelector("[data-testid=asset-profile]");
  await rows(1);
  await t.shot("4g-sim-token");
  await go("/orders?accounts=bots");
  await rows(1);
  await waitText("机器人");
  await noError("the bots' orders");
  ok("the simulated market: overview, price control with an event's impact (not started), events, bots, the coin's holders; the bots' orders");

  // 10. Fund operations: the approval mode with its limits, the form, the
  // records; the settings; the counts pushed on the event stream.
  await go("/adjustments");
  await page.waitForFunction(() => /单人模式|双人模式/.test(document.querySelector("main")?.innerText ?? ""), { timeout: 20000 });
  await waitText("调整余额");
  await rows(1);
  await go("/approvals");
  await sleep(1000);
  await noError("approvals");
  await go("/settings");
  await waitText("单笔上限");
  await waitText("每人 24 小时累计上限");
  await waitText("每页条数");
  const stream = await page.evaluate(
    () =>
      new Promise((resolve) => {
        const es = new EventSource("/admin/v1/events");
        const done = (v) => {
          es.close();
          resolve(v);
        };
        es.addEventListener("todo", (e) => done(JSON.parse(e.data)));
        setTimeout(() => done(null), 15000);
      }),
  );
  if (
    !stream || typeof stream.withdrawals !== "number" || typeof stream.approvals !== "number" || typeof stream.identity_requests !== "number" ||
    typeof stream.deposits !== "number"
  ) {
    throw new Error(`event stream: ${JSON.stringify(stream)}`);
  }
  await t.shot("5-settings");
  ok(`fund operations: the approval mode, the form and the records; the settings; the event stream (${JSON.stringify(stream)})`);

  // 10b. The account page: one's own password and authenticator (nothing changed).
  await go("/account");
  await waitText("修改口令");
  await waitText("身份验证器");
  await noError("the account page");
  ok("the account page");

  // 11. Sign out from the account menu.
  await page.click('header button[aria-label="账户菜单"]');
  await page.waitForFunction(() => [...document.querySelectorAll("[role=menuitem]")].some((e) => e.textContent.includes("退出")));
  await page.evaluate(() => [...document.querySelectorAll("[role=menuitem]")].find((e) => e.textContent.includes("退出")).click());
  await waitPath("/login");
  ok("signing out ends the session");

  // 11b. The setup page without its link says the link is incomplete.
  await go("/setup");
  await waitText("链接不完整");
  ok("the setup page without a link");
} catch (e) {
  await t.fail(e);
}
await t.finish();
