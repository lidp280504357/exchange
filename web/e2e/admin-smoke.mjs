// Browser smoke test of the admin console (admin.astras.vip, design §10):
// an administrator signs in (the flag admin.login_without_totp is on in
// the test environment) and walks every section: the overview with the
// services' health and HOUSE, users with a user's drawer and its tabs,
// orders and trades, deposits, the withdrawal queue, assets and pairs (a
// status change is confirmed and canceled, never done), futures, HOUSE,
// the flags, the ledger's reconciliation, the audit trail and the
// reports; the search opens a user; signing out ends the session. Every
// admin API response is checked against api/admin/admin.yaml.
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

  // 3. Users: the list; a row opens the drawer with balances and tabs.
  await go("/users");
  await rows(3);
  await t.clickLive("main tbody tr");
  await page.waitForSelector("[role=dialog]");
  await waitText("余额");
  const userId = await page.evaluate(() => new URL(location.href).searchParams.get("user"));
  if (!userId) throw new Error("the drawer did not put the user in the address");
  for (const tab of ["订单", "成交", "提现", "审计"]) await clickButton(tab, "[role=dialog]");
  await t.shot("2-user");
  await page.keyboard.press("Escape");
  await page.waitForFunction(() => !document.querySelector("[role=dialog]"));
  ok(`users: the list, a user's drawer (${userId.slice(0, 8)}…) with its tabs`);

  // 4. The search opens the same user.
  await typeInto(`header input`, userId);
  await page.keyboard.press("Enter");
  await page.waitForSelector("[role=dialog]");
  await page.keyboard.press("Escape");
  ok("the search finds a user by ID");

  // 5. Orders and trades (HOUSE shows as a party).
  await go("/orders");
  await rows(1);
  await clickButton("成交");
  await rows(1);
  await noError("trades");
  ok("orders and trades");

  // 6. Deposits and the withdrawal queue.
  await go("/deposits");
  await rows(1);
  await go("/withdrawals");
  await sleep(1500);
  await noError("withdrawals");
  ok("deposits and the withdrawal queue");

  // 6b. The custodian: reachable, its coins, the reconciliation and the
  // callback log, a callback's request as received.
  await go("/custody");
  await waitText("可访问");
  await waitText("TR7NHqjeKQxGTCi8q8ZY4pL8otSzgjLj6t".slice(0, 12));
  await rows(1, 'main table[aria-label="custody callbacks"]');
  await t.clickLive('main table[aria-label="custody callbacks"] tbody tr');
  await page.waitForSelector("[role=dialog]");
  await waitText("原始请求");
  await page.keyboard.press("Escape");
  await page.waitForFunction(() => !document.querySelector("[role=dialog]"));
  await t.shot("2b-custody");
  ok("the custodian: reachable, coins, reconciliation and a callback as received");

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
  await t.shot("3-confirm");
  await clickButton("取消", "[role=dialog]");
  await page.waitForFunction(() => !document.querySelector("[role=dialog]"));
  ok("assets and pairs: 50+ pairs, a status change waits for its confirmation (canceled)");

  // 8. Futures, HOUSE, flags.
  await go("/derivatives");
  await waitText("BTC-USDT-PERP");
  await go("/house");
  await waitText("各交易对");
  await rows(3);
  await go("/risk");
  await waitText("market.house_liquidity");
  ok("futures, HOUSE's book and the flags");

  // 9. Ledger: the reconciliation; audit; reports.
  await go("/ledger");
  await clickButton("对账");
  await waitText("分录借贷平衡");
  await go("/audit");
  await rows(1);
  await go("/reports");
  await page.waitForSelector("main svg[role=img]");
  await t.shot("4-reports");
  ok("the ledger's reconciliation, the audit trail and the reports");

  // 10. Sign out.
  await clickButton("退出");
  await waitPath("/login");
  ok("signing out ends the session");
} catch (e) {
  await t.fail(e);
}
await t.finish();
