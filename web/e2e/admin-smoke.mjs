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
// coin's holders; the bots' orders), the margin pages (hidden while
// margin.enabled is off; read and an editor opened while it is on), the fund
// operations (approval mode, form, records), the settings and the event
// stream, the account page; the
// search opens a user; signing out from the account menu ends the session,
// and the setup page without a link says so. A section first opened from
// the sidebar fetches its own chunk alone and shows a skeleton while that
// chunk is late (A40). Every admin API response is checked against
// api/admin/admin.yaml.
//
//   ADMIN_EMAIL=... ADMIN_PASSWORD=... node admin-smoke.mjs
//
// APP is the console (https://admin.astras.vip by default). scripts/e2e/
// web.sh creates a throwaway administrator for it.
import { execFileSync } from "node:child_process";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
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

// Step 2b holds the audit page's chunk back from the start (the sidebar
// prefetches the pages when the browser is idle) until it has seen the
// page's skeleton; every other request goes on at once. Interception turns
// the browser's cache off: it ends with step 2b.
const AUDIT_CHUNK = /\/assets\/Audit-[\w-]+\.js$/;
// Step 2c keeps the paths of the console's script requests (the pages fetched ahead).
const fetched = new Set();
const fetchLog = [];
const began = Date.now();
page.on("request", (r) => {
  const path = new URL(r.url()).pathname;
  if (r.resourceType() === "script" && path.startsWith("/assets/")) {
    fetched.add(path);
    fetchLog.push(`${((Date.now() - began) / 1000).toFixed(1)}s ${path.slice(8)}`);
  }
});
let heldChunk = null;
let chunkFree = false;
const hold = (r) => {
  if (r.isInterceptResolutionHandled()) return;
  if (!chunkFree && AUDIT_CHUNK.test(new URL(r.url()).pathname)) {
    heldChunk = r;
    return;
  }
  void r.continue();
};
await page.setRequestInterception(true);
page.on("request", hold);

/**
 * pressRow presses the first real row a selector of table rows finds (one
 * with a data-row-id, as rows() counts: a loading table's skeleton rows
 * have none) and waits for opened(timeout) to see what the press opens.
 * Each try waits up to 10 s for such a row (page.click would fail at once
 * while a list reloads into its skeleton, review BW) and 5 s for what it
 * opens; four tries at most, 60 s at worst. The tries after the first are
 * insurance: the one miss seen (the custody callbacks) was a skeleton row
 * pressed. The lists ask every 15 s whether anything is newer (useNewer)
 * and never replace their rows unless 有新数据 is clicked (reviews BS-BX).
 */
async function pressRow(selector, opened) {
  const row = `${selector}[data-row-id]`;
  const at = () => new URL(page.url()).pathname;
  for (let i = 1; ; i++) {
    try {
      await page.waitForSelector(row, { visible: true, timeout: 10000 });
    } catch (e) {
      if (e?.name !== "TimeoutError") throw e;
      if (i >= 4) throw new Error(`no real row (data-row-id) within 10 s on ${at()}, 4 tries: ${row}`);
      continue;
    }
    await t.clickLive(row);
    try {
      await opened(5000);
      return;
    } catch (e) {
      if (e?.name !== "TimeoutError") throw e;
      if (await page.$("[role=dialog]")) {
        // What it opens came just late (openRow's drawer), or another
        // dialog did: pressed again behind it, the press would close it.
        try {
          await opened(500);
          return;
        } catch (late) {
          if (late?.name !== "TimeoutError") throw late;
        }
        throw new Error(`${row} on ${at()} opened a dialog, not what it should`);
      }
      if (i >= 4) throw new Error(`${row} on ${at()} opened nothing after 4 presses (up to 60 s)`);
    }
  }
}

/** openRow presses a row whose press opens a drawer (pressRow). */
const openRow = (selector) => pressRow(selector, (timeout) => page.waitForSelector("[role=dialog]", { timeout }));

/**
 * rows waits for at least n of the table's own rows in scope (data-row-id:
 * a loading table's skeleton rows have none) with nothing loading there
 * (DataTable marks the table itself aria-busy).
 */
const rows = (n, scope = "main") =>
  page.waitForFunction(
    (s, k) => document.querySelectorAll(`${s} tbody tr[data-row-id]`).length >= k && !document.querySelector(`${s}[aria-busy=true], ${s} [aria-busy=true]`),
    { timeout: 30000 },
    scope,
    n,
  );
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
  // The figures are the humans' (L1): the trend says so, the bots trading in
  // small print - while the simulated market runs and they traded in the
  // last 24 hours (A113).
  await page.waitForSelector("[data-testid=trend-kinds]");
  const botsTrading = await page.evaluate(async () => {
    const r = await fetch("/admin/v1/dashboard?days=7");
    return r.ok ? ((await r.json()).trading.other_traders_24h ?? []).some((k) => k.kind === "BOT" && k.count > 0) : false;
  });
  let tradersNote = "no bot traded in the last 24 hours";
  if (botsTrading) {
    tradersNote = await page
      .waitForSelector("[data-testid=stat-note-traders24h]", { timeout: 20000 })
      .then((el) => el.evaluate((e) => e.textContent.trim()));
    if (!/机器人/.test(tradersNote)) throw new Error(`the traders' small print names no bots: ${tradersNote}`);
  }
  await t.shot("1-overview");
  ok(`the overview: figures (the humans', ${tradersNote}), trend chart, ${health}, HOUSE`);

  // 2b. A section first opened from the sidebar (A40): while its chunk is
  // late the content area shows the page's skeleton, never nothing; and its
  // chunk needs no file the signed-in shell has not loaded (the shared code
  // is the shell's "kit"): one file at most, none once prefetched.
  await page.click('aside a[href="/audit"]');
  await page.waitForSelector('main [data-testid="page-skeleton"]', { visible: true, timeout: 10000 });
  await t.shot("1b-skeleton");
  chunkFree = true;
  await heldChunk?.continue();
  await page.waitForFunction(
    () => document.querySelector("main h1")?.innerText.includes("审计日志") && !document.querySelector('main [data-testid="page-skeleton"]'),
    { timeout: 30000 },
  );
  const files = await page.evaluate(async () => {
    // The files a chunk imports statically, followed to the end.
    const follow = async (roots) => {
      const seen = new Set();
      const queue = [...roots];
      while (queue.length) {
        const path = queue.pop();
        if (!path || seen.has(path)) continue;
        seen.add(path);
        const src = await (await fetch(path)).text();
        for (const m of src.matchAll(/(?:import|from)\s*["']\.\/([\w.-]+\.js)["']/g)) queue.push(`/assets/${m[1]}`);
      }
      return seen;
    };
    const loaded = performance.getEntriesByType("resource").map((e) => new URL(e.name).pathname);
    const entry = document.querySelector("script[type=module][src]")?.getAttribute("src");
    const shell = await follow([entry, loaded.find((p) => /^\/assets\/SignedIn-[\w-]+\.js$/.test(p))]);
    const audit = loaded.find((p) => /^\/assets\/Audit-[\w-]+\.js$/.test(p));
    return { audit, extra: [...(await follow([audit]))].filter((p) => p !== audit && !shell.has(p)) };
  });
  if (!files.audit || files.extra.length) throw new Error(`the audit page needs files beyond its chunk ${files.audit}: ${files.extra.join(", ")}`);
  page.off("request", hold);
  await page.setRequestInterception(false);
  ok("a section first opened from the sidebar shows its skeleton while its chunk is late and needs that one file alone");

  // 2c. The pages fetched ahead (A40, review BJ ⑩): once signed in, the
  // idle browser fetches every section's page, one after another in the
  // sidebar's order (the settings last), each next one within 8 s of the
  // last (a round trip and an idle moment, about a second apiece from here:
  // the time in all grows with the sections, 36 with margin trading's, so a
  // fixed 30 s became the network's coin toss), all within two minutes;
  // three sections never opened (the system group's) then fetch no JS file
  // at all.
  // The console fetches nothing ahead when the browser asks to save data
  // or while the tab is hidden (preload.ts): said at once, not after the wait.
  const why = await page.evaluate(() =>
    navigator.connection?.saveData ? "the browser asks to save data" : document.visibilityState === "hidden" ? "the tab is hidden" : "",
  );
  if (why) throw new Error(`the console fetches no page ahead here: ${why}`);
  const chunkOf = (name) => [...fetched].some((p) => new RegExp(`^/assets/${name}-[\\w-]+\\.js$`).test(p));
  const ahead = [["/reports", "报表", "Reports"], ["/health", "系统健康", "Health"], ["/platform", "平台设置", "Platform"]];
  const aheadBy = Date.now() + 120_000;
  let seen = fetched.size;
  let movedAt = Date.now();
  while (!chunkOf("Settings") || ahead.some(([, , name]) => !chunkOf(name))) {
    if (fetched.size !== seen) [seen, movedAt] = [fetched.size, Date.now()];
    const stalled = Date.now() - movedAt > 8_000;
    if (stalled || Date.now() > aheadBy) {
      // A tab hidden or a request to save data meanwhile stops the fetching ahead too.
      const late = await page.evaluate(() => [navigator.connection?.saveData && "the browser asks to save data", document.visibilityState === "hidden" && "the tab is hidden"].filter(Boolean).join(", "));
      throw new Error(
        `${stalled ? "8 s without a page fetched ahead" : "two minutes on"}, the idle console had not fetched every section's page${late ? ` (${late})` : ""}; ` +
          `the scripts since the start: ${fetchLog.join(", ")}`,
      );
    }
    await sleep(250);
  }
  const before = fetchLog.length;
  for (const [href, title] of ahead) {
    await page.click(`aside a[href="${href}"]`);
    await page.waitForFunction((h) => document.querySelector("main h1")?.innerText.includes(h), { timeout: 20000 }, title);
  }
  if (fetchLog.length !== before) throw new Error(`three sections opened after the idle time fetched scripts: ${fetchLog.slice(before).join(", ")}`);
  ok(`the pages are fetched ahead while the browser is idle (every section's by ${fetchLog.find((l) => l.includes(" Settings-"))?.split(" ")[0]}): three sections never opened fetch no script`);

  // 3. Users: the list (each with its username and avatar, I3: a user who
  // uploaded none has the sites' built-in avatar of their ID, A79); a row
  // opens the user's page with its tabs.
  await go("/users");
  await rows(1);
  // The accounts' kinds (L1): the humans by default, each with its kind;
  // 类型 → 机器人 lists the bots only, → 全部 every kind.
  const kinds = () => page.$$eval("main tbody tr[data-row-id] td:nth-child(4)", (cells) => cells.map((c) => c.textContent.trim()));
  const chooseKind = async (option) => {
    await page.click('main button[role=combobox][aria-label="类型"]');
    await page.waitForSelector("[role=option]");
    await page.evaluate((o) => [...document.querySelectorAll("[role=option]")].find((e) => e.innerText.trim() === o)?.click(), option);
    await page.waitForFunction((o) => document.querySelector('main button[role=combobox][aria-label="类型"]')?.innerText.trim() === o, { timeout: 5000 }, option);
    await page.waitForFunction(() => !document.querySelector("main [aria-busy=true]"), { timeout: 20000 });
  };
  const humans = await kinds();
  if (humans.some((k) => k !== "真人")) throw new Error(`the default users list has ${[...new Set(humans)].join(", ")}`);
  await chooseKind("机器人");
  await page.waitForFunction(() => new URL(location.href).searchParams.get("kind") === "BOT", { timeout: 5000 });
  await rows(1);
  const bots = await kinds();
  if (bots.some((k) => k !== "机器人")) throw new Error(`the bots listed ${[...new Set(bots)].join(", ")}`);
  await chooseKind("全部");
  await rows(3);
  const every = new Set(await kinds());
  ok(`users: the humans by default (${humans.length}), the bots by kind (${bots.length}), every kind (${[...every].join(", ")})`);
  // The test accounts cleared out (L4) are listed only when asked, marked
  // (the end-to-end scripts clear theirs out when they end).
  await go("/users?kind=TEST&include_purged=true");
  await page.waitForSelector("main tbody [data-testid=user-purged]", { timeout: 20000 });
  // One's page says when and offers no money operation: no adjustment,
  // whatever the role (the ADMIN here may adjust any other).
  await pressRow("main tbody tr:has([data-testid=user-purged])", (timeout) =>
    page.waitForFunction(() => /^\/users\/[0-9a-f-]{36}$/.test(location.pathname), { timeout }),
  );
  const purgedAt = await page.waitForSelector("aside [data-testid=user-purged]", { timeout: 20000 }).then((el) => el.evaluate((e) => e.textContent.trim()));
  await go(`${new URL(page.url()).pathname}?tab=balances`);
  await waitText("总估值");
  await page.waitForFunction(() => !document.querySelector("main [aria-busy=true]"), { timeout: 20000 });
  if (await page.evaluate(() => [...document.querySelectorAll("main h2, main h3")].some((h) => h.textContent.trim() === "调整余额"))) {
    throw new Error("an account cleared out offers an adjustment");
  }
  ok(`users: the test accounts cleared out, when asked for, marked; one's page says "${purgedAt}" and offers no adjustment`);
  await go("/users?kind=ALL");
  await rows(3);
  // The search box and the region filter (A93): a keyword that names no
  // account filters the list by it, kept in the address; the region
  // applies as it is typed, without Enter. The reset clears both (and the
  // kind: the humans again).
  await typeInto("[data-testid=users-search]", "e2e-");
  await page.keyboard.press("Enter");
  await page.waitForSelector("[data-testid=users-keyword]", { timeout: 20000 });
  await page.waitForFunction(() => new URL(location.href).searchParams.get("q") === "e2e-", { timeout: 5000 });
  await rows(1);
  await typeInto('main input[aria-label="地区"]', "sg");
  await page.waitForFunction(() => new URL(location.href).searchParams.get("region") === "sg", { timeout: 5000 });
  await page.waitForFunction(() => !document.querySelector("main [aria-busy=true]"), { timeout: 20000 });
  const regions = await page.$$eval("main tbody tr[data-row-id] td:nth-child(5)", (cells) => cells.map((c) => c.textContent.trim()));
  if (regions.some((r) => r !== "SG")) throw new Error(`the region filter SG listed ${[...new Set(regions)].join(", ")}`);
  await clickButton("重置", "main");
  await page.waitForFunction(
    () => !new URL(location.href).searchParams.get("q") && !new URL(location.href).searchParams.get("kind") && !document.querySelector("[data-testid=users-keyword]"),
    { timeout: 5000 },
  );
  await rows(1);
  ok(`users: a keyword filters the list (in the address), the region SG as typed (${regions.length} rows), the reset clears both`);
  await page.waitForSelector("main tbody [data-testid=user-identity]");
  await page.waitForSelector("main tbody [data-testid=user-identity] svg[data-avatar-default]");
  await pressRow("main tbody tr", (timeout) => page.waitForFunction(() => /^\/users\/[0-9a-f-]{36}$/.test(location.pathname), { timeout }));
  const userId = await page.evaluate(() => location.pathname.split("/").pop());
  await waitText("UID");
  await page.waitForSelector("[data-testid=user-username]");
  await page.waitForSelector("[data-testid=user-kind][data-kind]"); // its kind (L1)
  // The username's reset (I3): its dialog opened and closed, nothing reset.
  await page.click("[data-testid=user-reset-username]");
  const resetDialog = '[role=dialog]:has(textarea[id$="-reason"])';
  await page.waitForSelector(resetDialog);
  await waitText("改为随机的 user_");
  await clickButton("取消", resetDialog);
  await page.waitForFunction((sel) => !document.querySelector(sel), { timeout: 5000 }, resetDialog);
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
  ok(`users: the list with usernames and avatars, a user's page (${userId.slice(0, 8)}…) with its tabs (profile, security, risk …) and the username's reset dialog`);

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
  // The humans' by default (L1); the test server's deposits are mostly the
  // end-to-end scripts' accounts', listed by kind.
  await go("/deposits");
  await page.waitForFunction(() => document.querySelector('main button[role=combobox][aria-label="类型"]')?.innerText.trim() === "真人", { timeout: 20000 });
  await page.waitForFunction(() => !document.querySelector("main [aria-busy=true]"), { timeout: 20000 });
  await go("/deposits?kind=TEST");
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
  // A withdrawal's detail: its address book record and the user's others
  // (the test server's withdrawals are the end-to-end scripts' accounts':
  // 类型 测试, L1).
  await go("/withdrawals?status=ALL&kind=TEST");
  await rows(1);
  await openRow("main tbody tr");
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
  await openRow('main table[aria-label="custody callbacks"] tbody tr');
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
  // The product lines above them (K3): three switches; closing (or
  // opening) the coin-margined one asks for a confirmation (canceled).
  await page.waitForSelector("[data-testid=products] [data-testid=product-coin_m]", { timeout: 20000 });
  if ((await page.$$("[data-testid=products] [data-testid^=product-switch-]")).length !== 3) throw new Error("three product lines with their switches");
  await page.click("[data-testid=product-switch-coin_m]");
  await page.waitForFunction(() => /币本位合约/.test(document.querySelector("[role=dialog]")?.textContent ?? ""));
  await t.shot("3-products");
  await clickButton("取消", "[role=dialog]");
  await page.waitForFunction(() => !document.querySelector("[role=dialog]"));
  await typeInto('main input[placeholder="搜索"]', "SOL-BTC");
  await page.waitForFunction(() => document.querySelectorAll("main tbody tr[data-row-id]").length === 1);
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
  await openRow("main tbody tr");
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
  await page.waitForFunction(() => document.querySelectorAll("main tbody tr[data-row-id]").length === 1);
  await openRow("main tbody tr");
  await page.waitForSelector("[data-testid=asset-profile]");
  await waitText("资料与图标");
  await page.keyboard.press("Escape");
  await page.waitForFunction(() => !document.querySelector("[role=dialog]"));
  ok("assets and pairs: the product lines (a switch confirmed, canceled), 50+ pairs, a status change previewed by the server waits for its confirmation (canceled); the pending changes; a pair's editor; the listing wizard's preview; an asset's profile");

  // 8. Futures (every user's positions, the liquidation log), HOUSE, flags.
  await go("/derivatives");
  await waitText("BTC-USDT-PERP");
  // G5 (review ER ③): both margin types with their settlement asset; a
  // coin's contracts closed together (the server's preview, canceled:
  // nothing changes); the insurance fund of every settlement asset, a
  // contribution's asset chosen (not sent).
  await waitText("BTC-USD-PERP");
  await waitText("结算币");
  await clickButton("按币", "main");
  await page.waitForSelector("[data-testid=coin-contract-BTC-USD-PERP]");
  await page.waitForSelector("[data-testid=close-coin-BTC]:not([disabled])", { timeout: 10000 });
  await page.click("[data-testid=close-coin-BTC]");
  await page.waitForSelector("[role=dialog] [data-testid=trading-params]");
  await waitText("关闭 BTC 的合约");
  await waitText("分钟生效");
  const closing = await page.$$eval("[role=dialog] [data-testid=trading-params] tr", (rows) => rows.map((r) => r.cells[0]?.innerText.trim()));
  if (!closing.includes("BTC-USDT-PERP") || !closing.includes("BTC-USD-PERP")) throw new Error(`closing BTC moves ${JSON.stringify(closing)}`);
  await t.shot("3c-close-coin");
  await clickButton("取消", "[role=dialog]");
  await page.waitForFunction(() => !document.querySelector("[role=dialog]"));
  await clickButton("保险基金", "main");
  await page.waitForSelector('main table[aria-label="insurance funds"] tbody tr[data-row-id]');
  const funds = await page.$$eval('main table[aria-label="insurance funds"] tbody tr[data-row-id]', (rows) => rows.map((r) => r.dataset.rowId));
  if (funds[0] !== "USDT" || !funds.includes("BTC")) throw new Error(`the insurance funds are ${JSON.stringify(funds)}`);
  await page.click('main button[role=combobox][aria-label="资产"]');
  await page.waitForSelector("[role=option]");
  await page.evaluate(() => [...document.querySelectorAll("[role=option]")].find((o) => o.innerText.trim() === "BTC")?.click());
  await page.waitForFunction(() => document.querySelector('main button[role=combobox][aria-label="资产"]')?.innerText.trim() === "BTC", { timeout: 5000 });
  await go("/instruments?tab=contracts");
  await page.waitForSelector('main table[aria-label="contracts"]');
  await waitText("面值");
  await waitText("币本位");
  ok("contracts of both margin types with their settlement asset; a coin's contracts closed together (previewed, canceled); the insurance funds by asset, BTC chosen to contribute; the instruments' contract columns");
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
  // HOUSE's caps (A69): the six of them, each with its purpose and what
  // lowering or raising it does (user 06:0x; a row each since A94, what
  // moving it does once opened); the request dialog lists a change from
  // and to with what it does, then is closed, nothing asked (a request may
  // wait already: then no button).
  await page.waitForSelector("[data-testid=house-caps] [data-testid=house-cap-contract_leverage]", { timeout: 20000 });
  await waitText("每个盘口每一档最多报出的数量");
  await page.click("[data-testid=house-cap-contract_leverage] button[aria-expanded]");
  await waitText("极端行情下合约权益可能被打穿");
  // Beside the per-asset and total caps, what HOUSE holds now (review R18).
  await page.waitForSelector("[data-testid=house-cap-symbol-held]", { timeout: 20000 });
  await page.waitForSelector("[data-testid=house-cap-total-held]");
  if (await page.$("[data-testid=house-caps-request]")) {
    await page.click("[data-testid=house-caps-request]");
    await page.waitForSelector('[role=dialog] input[aria-label="safety"]');
    // The ten-times step covers the leverage too (review R18 ①), up to the contracts' highest (A97).
    await page.waitForFunction(() => /10 → 100 → \d+/.test(document.querySelector("[data-testid=house-caps-step-hint]")?.textContent ?? ""), { timeout: 10000 });
    const safety = await page.$eval('[role=dialog] input[aria-label="safety"]', (el) => el.value);
    await page.$eval('[role=dialog] input[aria-label="safety"]', (el) => el.select());
    await page.type('[role=dialog] input[aria-label="safety"]', String(Number(safety) + 1));
    await page.waitForFunction(
      () => /安全边际[\s\S]*调高：可报的数量减少/.test(document.querySelector("[data-testid=house-caps-preview]")?.textContent ?? ""),
      { timeout: 10000 },
    );
    await t.shot("3c-house-caps");
    await clickButton("取消", "[role=dialog]");
    await page.waitForFunction(() => !document.querySelector("[role=dialog]"));
  }
  await go("/risk");
  await waitText("market.house_liquidity");
  // The flags described in Chinese (A103, A104): account.transfer's.
  await waitText("现货与合约账户之间的划转");
  await t.shot("3b-house");
  ok("futures, every user's positions and the liquidation log, HOUSE (results, exposure, inventory, pairs, every contract's net position, its caps with what each does and a change's preview), and the flags");

  // 9. Ledger: the reconciliation; audit; reports.
  await go("/ledger");
  await clickButton("对账");
  await waitText("分录借贷平衡");
  await go("/audit");
  await rows(1);
  await pressRow("main tbody tr", (timeout) => page.waitForSelector("[data-testid=audit-detail]", { timeout }));
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
  // Of the humans by default (L1); 类型 is there for every report but HOUSE's result.
  await page.waitForFunction(() => document.querySelector('main button[role=combobox][aria-label="类型"]')?.innerText.trim() === "真人", { timeout: 10000 });
  await t.shot("4-reports");
  for (const tab of ["用户增长", "HOUSE 盈亏"]) {
    await clickButton(tab, "main");
    await page.waitForFunction((s) => document.querySelector("main h2")?.textContent === s && document.querySelector("main svg[role=img]"), { timeout: 20000 }, tab);
  }
  if (await page.$('main button[role=combobox][aria-label="类型"]')) throw new Error("HOUSE's result offers a kind");
  await noError("the users' and HOUSE's reports");
  ok("the ledger's reconciliation, the audit trail with an entry's detail and its CSV export, and the reports (the humans' by default; the users, HOUSE's result)");

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
  // The launch checklist and the platform settings (design 2026-10-04, D2), read only.
  await go("/launch");
  await page.waitForSelector("[data-testid=launch-verdict]");
  await page.waitForSelector("[data-testid=launch-admin_totp]");
  // G5's two items (review ER ③): the contracts open by type, the coin-margined switch.
  await page.waitForSelector("[data-testid=launch-insurance]");
  await page.waitForSelector("[data-testid=launch-coin_m]");
  await waitText("开放中 U 本位");
  await waitText("derivatives.coin_m");
  await t.shot("4b-launch");
  await go("/platform");
  await waitText("注册赠送");
  // The Traditional Chinese texts say they are optional where they are written (review EV).
  await waitText("繁體中文（可选）");
  await page.waitForFunction(() => /平台资料|读不到/.test(document.querySelector("main")?.innerText ?? ""), { timeout: 20000 });
  ok("the launch checklist with its verdict, and the platform settings");
  // 平台设置 → App 下载 (design 2026-10-07 §3, H2): both platforms with the
  // OTA hint; Android's mode switched and left unsaved; a minimal .apk
  // (scripts/e2e/appfixture) uploaded in parts, checked by the server and
  // listed, then deleted - Android goes back to off or its link.
  await go("/platform/apps");
  await page.waitForSelector("[data-testid=app-ANDROID] [data-testid=app-settings-ANDROID]", { timeout: 20000 });
  await page.waitForSelector("[data-testid=app-IOS] [data-testid=app-settings-IOS]");
  await page.waitForSelector("[data-testid=apps-ota-hint]");
  // The download entries' switch (H5): its state, and its confirmation
  // opened and canceled (admin.sh switches it).
  await page.waitForSelector("[data-testid=apps-entry][data-state]");
  await page.click("[data-testid=apps-entry-switch]");
  await page.waitForFunction(() => /下载入口/.test(document.querySelector("[role=dialog]")?.textContent ?? ""));
  await clickButton("取消", "[role=dialog]");
  await page.waitForFunction(() => !document.querySelector("[role=dialog]"));
  const androidMode = '[data-testid=app-settings-ANDROID] [role=radiogroup]';
  await page.evaluate((sel) => [...document.querySelectorAll(`${sel} [role=radio]`)].find((r) => r.innerText.trim() === "外部链接")?.click(), androidMode);
  await page.waitForFunction(() => document.querySelector("[data-testid=app-save-ANDROID]")?.innerText.includes("保存"), { timeout: 5000 });
  const root = new URL("../..", import.meta.url).pathname;
  const fixtures = JSON.parse(
    execFileSync("go", ["run", "./scripts/e2e/appfixture", "-out", mkdtempSync(join(tmpdir(), "smoke-apps-")), "-version", "9.9.9"], { cwd: root }).toString(),
  );
  const fileInput = await page.$("[data-testid=app-upload-file-ANDROID-APP]");
  await fileInput.uploadFile(fixtures.apk.path);
  const appDialog = '[role=dialog]:has(textarea[id$="-reason"])';
  await page.waitForSelector(appDialog, { timeout: 20000 });
  await waitText(fixtures.apk.sha256);
  await page.type(`${appDialog} textarea[id$="-reason"]`, "smoke test: an app uploaded and deleted");
  await page.type(`${appDialog} input[id$="-word"]`, "android");
  await clickButton("确认", appDialog);
  await page.waitForFunction((sel) => !document.querySelector(sel), { timeout: 60000 }, appDialog);
  const uploadedRow = await page.waitForFunction(
    () =>
      [...document.querySelectorAll("[data-testid=app-ANDROID] [data-testid=app-file-row]")]
        .find((r) => r.innerText.includes("e2e.apk") && r.innerText.includes("9.9.9"))
        ?.querySelector("[data-testid^=app-delete-]")
        ?.getAttribute("data-testid"),
    { timeout: 20000 },
  );
  const deleteId = await uploadedRow.jsonValue();
  await waitText("vip.astras.e2e");
  await t.shot("4b-apps");
  // The row to the middle first: at the bottom of the window (the 下载入口
  // card above it since H5) the upload's toast covers its delete button,
  // which a click there would miss.
  await page.$eval(`[data-testid=${deleteId}]`, (el) => el.scrollIntoView({ block: "center" }));
  await page.click(`[data-testid=${deleteId}]`);
  await page.waitForSelector(appDialog);
  await page.type(`${appDialog} textarea[id$="-reason"]`, "smoke test: the app deleted");
  await page.type(`${appDialog} input[id$="-word"]`, deleteId.slice(-4));
  await clickButton("确认", appDialog);
  await page.waitForFunction((sel) => !document.querySelector(sel), { timeout: 20000 }, appDialog);
  await page.waitForFunction((tid) => !document.querySelector(`[data-testid=${tid}]`), { timeout: 20000 }, deleteId);
  ok(
    "the App downloads: the download entries' switch (confirmation canceled), both platforms, the mode switched unsaved, a minimal .apk uploaded in parts, checked, listed and deleted",
  );
  // The fixed pages: the six legal pages and the home hero, each with what
  // the sites show; the hero's editor opens from its default (closed unsaved).
  await go("/pages");
  // Each page in test mode and live (design 2026-10-04 §4.4): what the sites show in each.
  await page.waitForFunction(
    () =>
      ["test", "formal"].every((m) => {
        const shown = [...document.querySelectorAll(`[data-testid^=fixed-${m}-][data-onsite]`)].map((el) => el.getAttribute("data-onsite"));
        return shown.length === 7 && shown.every((s) => s !== "none");
      }),
    { timeout: 20000 },
  );
  // The column the sites use now is the exchange's mode (the platform profile's test mode).
  const testMode = await page.evaluate(async () => (await (await fetch("/admin/v1/platform/profile")).json()).test_mode.enabled);
  const now = testMode ? "test" : "formal";
  await page.waitForFunction(
    (m) => document.querySelector(`[data-testid=fixed-mode-${m}]`)?.dataset.current === "true" && !!document.querySelector("[data-testid=fixed-now]"),
    { timeout: 10000 },
    now,
  );
  const marked = await page.$$eval("[data-testid^=fixed-mode-][data-current=true]", (els) => els.map((el) => el.dataset.testid));
  if (marked.length !== 1) throw new Error(`the fixed pages mark ${JSON.stringify(marked)} as the column in use`);
  await page.click('[data-testid="fixed-edit-home-hero"]');
  await waitText("副标题");
  await page.waitForFunction(() => document.querySelector("#article-title-zh-CN")?.value.length > 0, { timeout: 10000 });
  // The default draft in Traditional Chinese is the generated zh-TW file's (G7b; review EV ②).
  const heroLanguages = '[role=dialog] [role=radiogroup][aria-label="语言"]';
  await page.evaluate((sel) => [...document.querySelectorAll(`${sel} [role=radio]`)].find((r) => r.innerText.trim() === "繁體中文")?.click(), heroLanguages);
  await page.waitForFunction(() => document.querySelector("#article-title-zh-TW")?.value.length > 0, { timeout: 10000 });
  const forMode = await page.evaluate(() => document.querySelector('[role=dialog] [role=radiogroup][aria-label="适用模式"] [aria-checked="true"]')?.innerText.trim());
  if (forMode !== "正式模式" && forMode !== "通用") throw new Error(`the hero's live column opens an editor for "${forMode}"`);
  await page.keyboard.press("Escape");
  await page.waitForFunction(() => !document.querySelector("[role=dialog]"), { timeout: 10000 });
  ok(`the fixed pages: the legal pages and the home hero with what the sites show in test mode and live (the ${now} column marked in use), the hero's live editor (${forMode})`);

  // 9c. Operations: the announcements, the editor with its preview (closed
  // unsaved), the help articles, the messages and their form (closed unsent).
  await go("/announcements");
  await page.waitForSelector('main table[aria-label="公告"]');
  // The list of one mode (A43 ⑪): in the address, every row marked for it.
  const filter = 'main [role=radiogroup][aria-label="按适用模式筛选"]';
  await page.waitForSelector(filter);
  await page.evaluate((sel) => [...document.querySelectorAll(`${sel} [role=radio]`)].find((r) => r.innerText.trim() === "测试模式")?.click(), filter);
  await page.waitForFunction(() => new URLSearchParams(location.search).get("modes") === "TEST", { timeout: 5000 });
  const listed = await page.waitForFunction(
    () => {
      const table = document.querySelector('main table[aria-label="公告"]');
      if (!table || table.getAttribute("aria-busy") === "true") return null;
      const rows = [...(table.tBodies[0]?.rows ?? [])].filter((r) => r.cells.length > 1);
      if (rows.length) return rows.map((r) => r.cells[0].innerText.includes("测试模式"));
      return /没有标为「测试模式」的稿|还没有/.test(table.innerText) ? [] : null;
    },
    { timeout: 10000 },
  );
  const marks = await listed.jsonValue();
  if (marks.some((m) => !m)) throw new Error("the list of test mode's announcements shows one not marked 测试模式");
  await page.evaluate((sel) => [...document.querySelectorAll(`${sel} [role=radio]`)].find((r) => r.innerText.trim() === "全部")?.click(), filter);
  await page.waitForFunction(() => !new URLSearchParams(location.search).has("modes"), { timeout: 5000 });
  await clickButton("新建公告", "main");
  await page.waitForSelector("#article-slug");
  // The drawer slides in: its tabs at the right edge take clicks once it stands.
  await page.waitForFunction(() => getComputedStyle(document.querySelector("[role=dialog]")).transform === "none");
  await page.type("#article-title-zh-CN", "冒烟测试");
  await page.type("#article-body-zh-CN", ":::test\n只在测试模式\n:::\n\n:::formal\n只在正式模式\n:::\n\n## 小标题\n\n正文");
  // The 繁體 tab (G7b, review EV): optional, its hint shown; half written,
  // saving says it is the Traditional Chinese (refused before any request:
  // nothing is saved).
  const language = '[role=dialog] [role=radiogroup][aria-label="语言"]';
  const pickLanguage = async (label) => {
    await page.evaluate((sel, l) => [...document.querySelectorAll(`${sel} [role=radio]`)].find((r) => r.innerText.trim() === l)?.click(), language, label);
    await page.waitForFunction((sel, l) => document.querySelector(`${sel} [aria-checked="true"]`)?.innerText.trim() === l, { timeout: 5000 }, language, label);
  };
  await pickLanguage("繁體中文");
  await waitText("繁体可以不写");
  await page.type("#article-title-zh-TW", "冒煙測試");
  await page.type("#article-slug", "smoke-tw-half");
  await page.click("[data-testid=article-save]");
  const confirmDialog = '[role=dialog]:has(textarea[id$="-reason"])';
  await page.waitForSelector(confirmDialog);
  await page.type(`${confirmDialog} textarea[id$="-reason"]`, "smoke test: a Traditional Chinese title alone");
  await page.type(`${confirmDialog} input[id$="-word"]`, "smoke-tw-half");
  await clickButton("确认", confirmDialog);
  await waitText("繁体的标题与正文要一起写");
  await clickButton("取消", confirmDialog);
  await page.waitForFunction((sel) => !document.querySelector(sel), { timeout: 5000 }, confirmDialog);
  await pickLanguage("中文");
  // The page's mode (design 2026-10-04 §4.4): a draft is for both; picked for
  // test mode, its preview keeps the test block and drops the live one, and
  // without a summary shows the lists' one, the first paragraph shown in
  // that mode (review BK ③).
  const modes = '[role=dialog] [role=radiogroup][aria-label="适用模式"]';
  const chosen = () => page.evaluate((sel) => document.querySelector(`${sel} [aria-checked="true"]`)?.innerText.trim(), modes);
  if ((await chosen()) !== "通用") throw new Error(`a new page is for "${await chosen()}", not both modes`);
  await page.evaluate((sel) => [...document.querySelectorAll(`${sel} [role=radio]`)].find((r) => r.innerText.trim() === "测试模式")?.click(), modes);
  await page.waitForFunction((sel) => document.querySelector(`${sel} [aria-checked="true"]`)?.innerText.trim() === "测试模式", { timeout: 5000 }, modes);
  await clickButton("预览", "[role=dialog]");
  await page.waitForSelector("[data-testid=article-preview] h2");
  const preview = await page.$eval("[data-testid=article-preview]", (el) => ({ mode: el.dataset.mode, text: el.innerText }));
  if (preview.mode !== "test" || !preview.text.includes("只在测试模式") || preview.text.includes("只在正式模式")) {
    throw new Error(`the test-mode preview shows ${JSON.stringify(preview)}`);
  }
  const derived = await page.$eval("[data-testid=article-preview-summary]", (el) => el.innerText).catch(() => "");
  if (!derived.startsWith("只在测试模式")) throw new Error(`the preview's summary of a draft without one is "${derived}"`);
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
  ok(`operations: the announcements (filtered to test mode: ${marks.length} marked), the editor's mode and preview (closed unsaved), the help articles, the messages and their form (closed unsent)`);

  // 9d. The simulated market: the overview with its chart, price control
  // with an event's impact (the confirmation closed, nothing started), the
  // events, the bots and the coin's holders; the bots filter on orders.
  await go("/sim");
  await page.waitForSelector("[data-testid=sim-target]");
  await page.waitForSelector("main svg[role=img]", { timeout: 30000 });
  await t.shot("4d-sim");
  const simTarget = Number(await page.$eval("[data-testid=sim-target]", (el) => el.textContent));
  await go("/sim/control");
  await waitText("价格模型");
  // The page's own form comes with the simulated market's state; the card
  // of price events on any pair above it, whose help names the price
  // model too, comes at once (J3).
  await page.waitForSelector("#sim-size", { timeout: 20000 });
  await page.$eval("#sim-size", (el) => el.select());
  await page.type("#sim-size", "-5");
  await page.click("[data-testid=sim-event-start]");
  await page.waitForSelector("[data-testid=sim-impact]", { timeout: 20000 });
  await t.shot("4e-sim-impact");
  await clickButton("取消", "[role=dialog]");
  await page.waitForFunction(() => !document.querySelector("[role=dialog]"));
  // The threshold target's form (A6): 3% above the target within the
  // default 30 minutes, previewed reachable with its envelope; nothing
  // asked for.
  await page.click("[data-testid=sim-type-TARGET]");
  await page.type("#sim-target-level", (Math.floor(simTarget * 1.03 * 10000) / 10000).toFixed(4));
  await page.waitForSelector("[data-testid=sim-target-preview] svg[role=img]", { timeout: 20000 });
  await waitText("能按时到达");
  await t.shot("4e-sim-target");
  // Price events on any pair (J3): the leverage reached by default, its
  // hint always there; BTC-USDT chosen shows its price now and the
  // target's; the confirmation says it too, then is closed, nothing started.
  await page.waitForSelector("[data-testid=overlay-card] [data-testid=overlay-open]", { timeout: 20000 });
  await page.waitForFunction(() => /强平/.test(document.querySelector("[data-testid=overlay-risk-hint]")?.textContent ?? ""));
  if ((await page.$eval("#overlay-spare", (el) => el.getAttribute("data-state"))) !== "unchecked") {
    throw new Error("a price event spares the perpetuals and the leverage only when ticked");
  }
  await page.click("[data-testid=overlay-add-pair]");
  const pairSearch = 'input[role=combobox][placeholder="搜索交易对"]';
  await page.waitForSelector(pairSearch);
  await page.type(pairSearch, "BTC-USDT");
  await page.keyboard.press("Enter");
  await page.waitForSelector("[data-testid=overlay-pair-BTC-USDT]");
  await page.waitForFunction(
    () => /BTC-USDT\s*平台现价 [0-9,.]+\s*目标 [0-9,.]+\s*\+1\.00%/.test(document.querySelector("[data-testid=overlay-lines]")?.textContent ?? ""),
    { timeout: 20000 },
  );
  await page.click("[data-testid=overlay-start]");
  await page.waitForSelector("[role=dialog] [data-testid=overlay-lines]");
  // In the dialog itself: the box outside it says 不连带合约与杠杆 (A81 ④).
  await page.waitForFunction(() => /(^|[^不])连带合约与杠杆/.test(document.querySelector("[role=dialog]")?.textContent ?? ""));
  await t.shot("4e-sim-overlay");
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
  // The bots' orders by kind; a kind from the address in any letter case (A107).
  await go("/orders?kind=bot");
  await page.waitForFunction(() => document.querySelector('main button[role=combobox][aria-label="类型"]')?.innerText.trim() === "机器人", { timeout: 10000 });
  await rows(1);
  await noError("the bots' orders");
  ok("the simulated market: overview, price control with an event's impact (not started), a target's preview and a price event on any pair (its leverage hint, confirmation closed), events, bots, the coin's holders; the bots' orders");

  // 9e. Margin trading (design 2026-10-06 §8, E5): its pages sit behind
  // margin.enabled. Off, the sidebar has none of them and their addresses
  // lead to the overview. On, each page reads margin-service's terms and
  // accounts or the read models; the editors open and close without a
  // change (nothing is asked for), a pair's new leverage offers its
  // suggested thresholds without filling them in (A57).
  const marginOn = await page.evaluate(async () => {
    const r = await fetch("/admin/v1/flags");
    return r.ok && (await r.json()).items.some((f) => f.key === "margin.enabled" && f.enabled);
  });
  if (marginOn) {
    await go("/margin/params");
    await rows(1);
    await waitText("USDT");
  }
  // A console from before E5 shows sample data under a preview banner.
  const marginSample = marginOn && (await page.$("[data-testid=margin-preview]"));
  if (marginSample) {
    console.log("SKIP margin pages: this console is from before E5 (sample data under a preview banner)");
  } else if (marginOn) {
    await noError("the margin assets");
    await t.shot("4h-margin-params");
    // The lists re-render as their queries settle: the button is found again if a render replaced it.
    await t.clickLive("[data-testid=margin-edit-USDT]");
    await page.waitForSelector("[role=dialog] [data-testid=margin-asset-save]");
    if (!(await page.$eval("[data-testid=margin-asset-save]", (b) => b.disabled))) throw new Error("the USDT editor offers to save nothing");
    // Closed with Esc: a drawer slides in from the right, and its footer's buttons are past the edge until it is in.
    await page.keyboard.press("Escape");
    await page.waitForFunction(() => !document.querySelector("[role=dialog]"));
    await go("/margin/params?tab=pairs");
    await rows(1);
    await noError("the margin pairs");
    const pair = await page.$eval('main tbody tr[data-row-id] [data-testid^="margin-pair-"]', (b) => b.dataset.testid);
    await t.clickLive(`[data-testid="${pair}"]`);
    await page.waitForSelector("[role=dialog] [data-testid=margin-pair-save]");
    const levels = () => page.$$eval("[role=dialog] input[inputmode=decimal]", (inputs) => inputs.slice(0, 2).map((i) => i.value).join(" / "));
    const before = await levels();
    // Another leverage than the pair's: its thresholds stay as they are, the suggestion is offered.
    const other = await page.$$eval("[role=dialog] [role=radio]", (radios) => {
      const r = radios.find((x) => x.getAttribute("data-state") !== "on");
      r?.click();
      return r?.textContent ?? "";
    });
    if (!other) throw new Error("the pair editor offers no other leverage");
    await page.waitForSelector("[role=dialog] [data-testid=margin-suggested]");
    if ((await levels()) !== before) throw new Error(`a new leverage overwrote the thresholds: ${before} -> ${await levels()}`);
    await t.shot("4h-margin-pair");
    await page.keyboard.press("Escape");
    await page.waitForFunction(() => !document.querySelector("[role=dialog]"));
    await go("/margin/params?tab=settings");
    await waitText("全仓条款");
    await noError("the cross terms");
    // A page's table, there and loaded (before its chunk renders, nothing in main is busy yet).
    const loaded = (label) =>
      page.waitForFunction((l) => !!document.querySelector(`main table[aria-label="${l}"]`) && !document.querySelector("main [aria-busy=true]"), {
        timeout: 30000,
      }, label);
    // web.sh names a user whose cross account holds 10 USDT (MARGIN_USER_ID):
    // that account's detail, each tab rendered; without one (a run by hand)
    // the first account that holds or owes something, when there is one.
    const marginUser = process.env.MARGIN_USER_ID ?? "";
    await go(marginUser ? `/margin/accounts?user_id=${marginUser}` : "/margin/accounts");
    await loaded("margin-accounts");
    await noError("the margin accounts");
    const account = !!(await page.$("main table[aria-label=margin-accounts] tbody tr[data-row-id]"));
    if (marginUser && !account) throw new Error(`the cross margin account of ${marginUser} (10 USDT moved in) is not listed`);
    if (account) {
      await openRow("main table[aria-label=margin-accounts] tbody tr");
      await page.waitForSelector("[role=dialog] [data-testid=margin-account] table");
      // In place before a tab is pressed: the drawer slides in from the right.
      await page.waitForFunction(() => {
        const d = document.querySelector("[role=dialog]")?.getBoundingClientRect();
        return d && Math.abs(d.right - innerWidth) < 2;
      });
      // Each tab selected and its own table rendered (review DX (a)): a tab that fails to render is not hidden by the last one's.
      for (const [tab, table] of [
        ["借款与借还记录", "margin-loan-changes"],
        ["计息", "margin-interest"],
        ["杠杆强平", "margin-account-liquidations"],
        ["资产与负债", "margin-balances"],
      ]) {
        await clickButton(tab, "[role=dialog]");
        await page.waitForFunction(
          (name, label) =>
            document.querySelector('[role=dialog] [role=tab][aria-selected="true"]')?.textContent?.trim() === name &&
            !!document.querySelector(`[role=dialog] table[aria-label="${label}"]`),
          { timeout: 10000 },
          tab,
          table,
        );
      }
      await t.shot("4h-margin-account");
      await page.keyboard.press("Escape");
      await page.waitForFunction(() => !document.querySelector("[role=dialog]"));
    }
    await go("/margin/liquidations");
    await loaded("margin-liquidations");
    await noError("the margin liquidations");
    await go("/margin/interest");
    await loaded("margin-interest-report");
    await noError("the margin interest");
    ok(
      "margin trading: the parameters (an editor opened and closed unchanged; a pair's new leverage keeps its thresholds and offers the suggested), " +
        `the accounts (${account ? "an account's detail, every tab" : "none holds anything: no detail"}), the liquidations and the interest`,
    );
  } else {
    await go("/margin/params");
    await waitPath("/");
    if (await page.$('aside a[href^="/margin/"]')) throw new Error("the sidebar links a margin page while margin.enabled is off");
    ok("margin trading stays hidden while margin.enabled is off: no sidebar entry, its addresses lead to the overview");
  }

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
  // A wait's own error ("Waiting for selector … failed") keeps why in its cause.
  if (e?.cause?.message) console.error("cause:", e.cause.message);
  await t.fail(e);
}
await t.finish();
