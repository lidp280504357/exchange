// The admin console's part of the user's checklist (docs/runbook/
// ui-checklist.md: the general items that apply to it and A1-A3; docs/
// 阶段4验收报告.md §8), run by scripts/e2e/webflows.sh after the smokes
// with a throwaway ADMIN and a throwaway AUDITOR it creates and disables:
//
//   ADMIN_EMAIL=... ADMIN_PASSWORD=... AUDITOR_EMAIL=... AUDITOR_PASSWORD=... node web/e2e/admin-flows.mjs
//
// Nothing is changed: every dangerous action is opened, filled and
// canceled, and the audit trail then holds nothing of the ADMIN but its
// sign-in and sign-out. The console is a desktop site at 1024 / 1280 /
// 1920, light and dark. The user sites' items that the console does not
// have (rise and fall colours, token renewal, a new account's empty
// lists, its own time zone setting) are left to them.
import { contrastIssues, desktop, flows, longAnimations, overflowX, scrollThrough, truncatedWithoutHint } from "./flows-lib.mjs";

const APP = (process.env.APP ?? "https://admin.astras.vip").replace(/\/$/, "");
const ADMIN = { email: process.env.ADMIN_EMAIL, password: process.env.ADMIN_PASSWORD };
const AUDITOR = { email: process.env.AUDITOR_EMAIL, password: process.env.AUDITOR_PASSWORD };
if (!ADMIN.email || !ADMIN.password || !AUDITOR.email || !AUDITOR.password) {
  console.log("SKIP admin console flows: ADMIN_EMAIL/ADMIN_PASSWORD and AUDITOR_EMAIL/AUDITOR_PASSWORD are not set");
  process.exit(0);
}

const f = await flows({ site: "admin", app: APP, api: APP, apiPrefix: "/admin/v1/" });
const began = new Date(Date.now() - 60_000).toISOString();
const WIDTHS = [1024, 1280, 1920];

// --- helpers -----------------------------------------------------------------

async function nav(tab, path) {
  await tab.page.evaluate((p) => {
    history.pushState({}, "", p);
    dispatchEvent(new PopStateEvent("popstate"));
  }, path);
  await tab.page.waitForFunction((p) => location.pathname + location.search === p, { timeout: 10000 }, path);
  await tab.settled();
}

/** signIn signs in with the password alone (admin.login_without_totp) and waits for the console at next. */
async function signIn(tab, who, next = "/") {
  await tab.go(next === "/" ? "/login" : `/login?next=${encodeURIComponent(next)}`);
  await tab.page.waitForFunction(() => document.querySelector("form button[type=submit]")?.disabled === false, { timeout: 20000 });
  await tab.typeInto('input[autocomplete="username"]', who.email);
  await tab.typeInto('input[autocomplete="current-password"]', who.password);
  await tab.page.keyboard.press("Enter");
  await tab.page.waitForFunction((p) => location.pathname + location.search === p, { timeout: 30000 }, next);
  await tab.page.waitForSelector("aside a[href]", { timeout: 20000 });
}

/** rows waits for the main table to hold rows (not its empty state) and to be done loading. */
const rows = (tab, n = 1) =>
  tab.page.waitForFunction((k) => document.querySelectorAll("main tbody tr[data-row-id]").length >= k && !document.querySelector("main table[aria-busy=true]"), { timeout: 30000 }, n);

/** dialog is the open confirmation (the one with a reason). */
const DIALOG = '[role=dialog]:has(textarea[id$="-reason"])';

/**
 * confirmation checks the open confirmation asks for a reason and a word
 * (confirm off until both are right), fills both, and cancels it.
 */
async function confirmation(tab, what) {
  await tab.page.waitForSelector(DIALOG, { visible: true, timeout: 15000 });
  const state = () =>
    tab.page.$eval(DIALOG, (d) => ({
      word: d.querySelector('label[for$="-word"] code')?.innerText.trim() ?? "",
      confirm: [...d.querySelectorAll("button")].find((b) => b.innerText.trim() === "确认")?.disabled,
    }));
  const s = await state();
  if (!s.word) throw new Error(`${what}: no confirmation word`);
  if (s.confirm !== true) throw new Error(`${what}: confirm is on before a reason and the word`);
  await tab.page.type(`${DIALOG} textarea[id$="-reason"]`, "too short");
  await tab.page.type(`${DIALOG} input[id$="-word"]`, s.word);
  if ((await state()).confirm !== true) throw new Error(`${what}: confirm is on with a reason under 10 characters`);
  await tab.page.type(`${DIALOG} textarea[id$="-reason"]`, " - the checklist flows, canceled");
  await tab.page.waitForFunction((sel) => [...document.querySelector(sel).querySelectorAll("button")].find((b) => b.innerText.trim() === "确认")?.disabled === false, { timeout: 5000 }, DIALOG);
  await tab.clickButton("取消", DIALOG);
  await tab.page.waitForSelector(DIALOG, { hidden: true, timeout: 5000 });
}

/** menuItem opens a row's 操作 menu and picks the item whose text starts with label. */
async function menuItem(tab, rowSelector, label) {
  const trigger = await tab.page.waitForFunction(
    (sel) => [...document.querySelectorAll(`${sel} button`)].find((b) => b.innerText.trim() === "操作") ?? null,
    { timeout: 15000 },
    rowSelector,
  );
  await trigger.asElement().click();
  const item = await tab.page.waitForFunction((l) => [...document.querySelectorAll("[role=menuitem]")].find((m) => m.innerText.trim().startsWith(l)) ?? null, { timeout: 5000 }, label);
  await item.asElement().click();
}

const sectionPaths = (tab) => tab.page.$$eval("aside a[href]", (as) => [...new Set(as.map((a) => a.getAttribute("href")).filter((h) => h.startsWith("/")))]);

// --- the administrators ---------------------------------------------------------------

const A = await f.open({ name: "admin", device: desktop(1440) });
await f.step("—", "the throwaway ADMIN signs in", () => signIn(A, ADMIN), { fatal: true });
const AU = await f.open({ name: "auditor", device: desktop(1440) });
await f.step("—", "the throwaway AUDITOR signs in", () => signIn(AU, AUDITOR), { fatal: true });

// --- 1: navigation ----------------------------------------------------------------------

await f.step("1", "every section of the sidebar opens its page; a visitor sent to sign in comes back to the page asked for", async () => {
  const paths = await sectionPaths(A);
  if (paths.length < 20) throw new Error(`only ${paths.length} sections: ${paths.join(" ")}`);
  const broken = [];
  for (const p of paths) {
    await nav(A, p);
    await A.page.waitForSelector("main h1", { timeout: 15000 }).catch(() => {});
    const at = new URL(A.page.url()).pathname;
    const head = await A.page.evaluate(() => document.querySelector("main h1")?.innerText.trim() ?? "");
    if (at !== p || !head) broken.push(`${p} -> ${at} "${head}"`);
  }
  if (broken.length) throw new Error(`sections that do not open: ${broken.join(", ")}`);
  const V = await f.open({ name: "visitor", device: desktop(1280) });
  try {
    await V.go("/users?status=FROZEN");
    await V.page.waitForFunction(() => location.pathname === "/login" && new URLSearchParams(location.search).get("next") === "/users?status=FROZEN", { timeout: 15000 });
    await V.page.waitForFunction(() => document.querySelector("form button[type=submit]")?.disabled === false, { timeout: 20000 });
    await V.typeInto('input[autocomplete="username"]', AUDITOR.email);
    await V.typeInto('input[autocomplete="current-password"]', AUDITOR.password);
    await V.page.keyboard.press("Enter");
    await V.page.waitForFunction(() => location.pathname + location.search === "/users?status=FROZEN", { timeout: 30000 });
  } finally {
    await V.close();
  }
});

// --- 2: no sideways scrolling -----------------------------------------------------------------

await f.step(
  "2",
  `no page scrolls sideways at ${WIDTHS.join(" / ")}, each scrolled down three screens (the lists load more as they scroll)`,
  async () => {
    const paths = await sectionPaths(A);
    const problems = [];
    await Promise.all(
      WIDTHS.map(async (w) => {
        const tab = await f.open({ name: `w${w}`, device: desktop(w) });
        try {
          await tab.go("/login");
          await scrollThrough(tab, 3);
          for (const o of await overflowX(tab.page)) problems.push(`${w} /login: ${o}`);
          await signIn(tab, ADMIN);
          for (const p of paths) {
            await nav(tab, p);
            await scrollThrough(tab, 3);
            for (const o of await overflowX(tab.page)) problems.push(`${w} ${p}: ${o}`);
          }
        } finally {
          await tab.close();
        }
      }),
    );
    if (problems.length) throw new Error(`sideways scrolling:\n  ${problems.join("\n  ")}`);
  },
  { timeout: 420_000 },
);

// --- 3: contrast, light and dark -----------------------------------------------------------------

await f.step("3", "text contrast at least 4.5:1 (3:1 for large text), in the light and the dark theme", async () => {
  const found = new Map();
  for (const theme of ["light", "dark"]) {
    await A.page.evaluate((th) => localStorage.setItem("admin.theme", th), theme);
    await A.page.reload({ waitUntil: "domcontentloaded" });
    await A.settled();
    await A.page.waitForFunction((th) => document.documentElement.dataset.theme === th, { timeout: 10000 }, theme);
    for (const p of ["/", "/users", "/withdrawals?status=ALL", "/audit", "/launch"]) {
      await nav(A, p);
      for (const g of await contrastIssues(A.page)) {
        const key = `${theme}: ${g.pair}`;
        const prev = found.get(key) ?? { ...g, pair: key, count: 0, pages: [] };
        prev.count += g.count;
        prev.pages.push(p);
        found.set(key, prev);
      }
    }
  }
  await A.page.evaluate(() => localStorage.setItem("admin.theme", "light"));
  await A.page.reload({ waitUntil: "domcontentloaded" });
  await A.settled();
  if (found.size) {
    const lines = [...found.values()].sort((a, b) => b.count - a.count).map((g) => `${g.pair}: ${g.ratio}:1 < ${g.need}:1, ${g.count} texts on ${g.pages.join(" ")}, e.g. ${g.samples.map((s) => `"${s}"`).join(", ")}`);
    throw new Error(`low contrast:\n  ${lines.join("\n  ")}`);
  }
});

// --- 5: reduced motion ------------------------------------------------------------------------

await f.step("5", "with reduced motion, page changes, list entrances and drawers do not animate", async () => {
  const R = await f.open({ name: "reduced", device: desktop(1440), media: [{ name: "prefers-reduced-motion", value: "reduce" }] });
  try {
    await signIn(R, AUDITOR);
    const found = [];
    for (const p of ["/users", "/audit", "/launch"]) {
      await R.page.evaluate((path) => {
        history.pushState({}, "", path);
        dispatchEvent(new PopStateEvent("popstate"));
      }, p);
      await R.frames(2);
      for (const a of await longAnimations(R)) found.push(`${p}: ${a}`);
    }
    await nav(R, "/audit");
    await rows(R);
    await R.page.click("main tbody tr[data-row-id]");
    await R.page.waitForSelector("[role=dialog]", { visible: true });
    for (const a of await longAnimations(R)) found.push(`drawer: ${a}`);
    if (found.length) throw new Error(`animations under reduced motion:\n  ${[...new Set(found)].slice(0, 20).join("\n  ")}`);
  } finally {
    await R.close();
  }
});

// --- 6: offline ----------------------------------------------------------------------------------

await f.step(
  "6",
  "offline shows 网络不可用 at once; a list asked for offline loads by itself back online, without a reload",
  async () => {
    const O = await f.open({ name: "offline", device: desktop(1280) });
    try {
      await signIn(O, AUDITOR, "/users");
      await rows(O);
      await O.page.evaluate(() => {
        window.__flowsMarker = "kept";
      });
      await O.page.setOfflineMode(true);
      await O.page.waitForFunction(() => [...document.querySelectorAll("[role=status]")].some((el) => el.innerText.includes("网络不可用")), { timeout: 10000 });
      // A list asked for while offline waits, and is read as soon as the network is back.
      await O.page.evaluate(() => {
        history.pushState({}, "", "/users?status=ACTIVE");
        dispatchEvent(new PopStateEvent("popstate"));
      });
      await O.page.waitForFunction(() => location.search === "?status=ACTIVE", { timeout: 10000 });
      const read = O.page.waitForResponse((r) => r.url().includes("/admin/v1/users?") && r.url().includes("status=ACTIVE") && r.ok(), { timeout: 30000 });
      await O.page.setOfflineMode(false);
      await read;
      await O.page.waitForFunction(() => ![...document.querySelectorAll("[role=status]")].some((el) => el.innerText.includes("网络不可用")), { timeout: 10000 });
      await rows(O);
      if ((await O.page.evaluate(() => window.__flowsMarker)) !== "kept") throw new Error("the page reloaded");
    } finally {
      await O.close();
    }
  },
  { allowErrors: [/ERR_INTERNET_DISCONNECTED|net::ERR|Failed to fetch|NetworkError|EventSource/i] },
);

// --- 8: errors next to their fields ------------------------------------------------------------

await f.step("8", "form errors sit under their fields: the adjustment's amount, a confirmation's reason and word", async () => {
  await nav(A, "/adjustments");
  await A.page.waitForSelector('input[aria-label="金额"]', { visible: true, timeout: 15000 });
  await A.typeInto('input[aria-label="金额"]', "abc");
  await A.page.keyboard.press("Tab");
  const amount = await A.page.evaluate(() => {
    const input = document.querySelector('input[aria-label="金额"]');
    const msg = (input.getAttribute("aria-describedby") ?? "").split(/\s+/).map((id) => document.getElementById(id)).find((el) => el?.innerText.trim());
    return input.getAttribute("aria-invalid") === "true" && msg ? msg.innerText.trim() : null;
  });
  if (!amount) throw new Error("an amount that is not a number shows no error under the field");
  // A confirmation: the word that does not match says so under it.
  await nav(A, "/risk");
  await rows(A, 3);
  await A.page.click("main tbody tr[data-row-id] button[role=switch]");
  await A.page.waitForSelector(DIALOG, { visible: true });
  await A.page.type(`${DIALOG} input[id$="-word"]`, "not the word");
  await A.page.waitForFunction((sel) => document.querySelector(sel)?.querySelector('p[id$="-word-msg"]')?.innerText.includes("确认词不一致"), { timeout: 5000 }, DIALOG);
  await A.clickButton("取消", DIALOG);
});

// --- 10: truncated text --------------------------------------------------------------------------

await f.step("10", "text cut short in the lists (IDs, emails, addresses) can be read whole: a title, a tooltip or a copy button", async () => {
  const N = await f.open({ name: "narrow", device: desktop(1024) });
  try {
    await signIn(N, AUDITOR);
    const found = [];
    for (const p of ["/users", "/audit", "/withdrawals?status=ALL", "/orders", "/deposits"]) {
      await nav(N, p);
      await rows(N).catch(() => {});
      for (const x of await truncatedWithoutHint(N.page)) found.push(`${p}: ${x}`);
    }
    if (found.length) throw new Error(`cut short with no way to read it:\n  ${found.join("\n  ")}`);
  } finally {
    await N.close();
  }
});

// --- 12: numbers ---------------------------------------------------------------------------------

await f.step("12", "amounts, prices and quantities right-aligned in tabular digits", async () => {
  const problems = [];
  for (const [p, headers] of [["/orders", ["价格", "数量"]], ["/withdrawals?status=ALL", ["金额", "折合"]]]) {
    await nav(AU, p);
    await rows(AU).catch(() => {});
    const cells = await AU.page.evaluate((hs) => {
      const out = [];
      const table = document.querySelector("main table");
      const ths = [...(table?.tHead?.rows[0]?.cells ?? [])];
      for (const h of hs) {
        const i = ths.findIndex((th) => th.innerText.trim().startsWith(h));
        if (i < 0) continue;
        for (const row of [...(table.tBodies[0]?.rows ?? [])].filter((r) => r.dataset.rowId).slice(0, 5)) {
          const s = getComputedStyle(row.cells[i]);
          out.push({ h, text: row.cells[i].innerText.trim(), align: s.textAlign, nums: s.fontVariantNumeric });
        }
      }
      return out;
    }, headers);
    if (!cells.length) problems.push(`${p}: none of ${headers.join(", ")} with rows`);
    for (const c of cells) {
      if (c.align !== "right") problems.push(`${p} ${c.h} "${c.text}" aligned ${c.align}`);
      if (!c.nums.includes("tabular-nums")) problems.push(`${p} ${c.h} "${c.text}" without tabular digits`);
    }
  }
  if (problems.length) throw new Error(problems.join("\n  "));
});

// --- A1: lists ---------------------------------------------------------------------------------------

await f.step("A1", "lists: filters live in the address and survive a reload; the next page loads on scroll; sortable columns sort", async () => {
  // A filter.
  await nav(AU, "/users");
  await rows(AU);
  await AU.page.click('button[role=combobox][aria-label="状态"]');
  const option = await AU.page.waitForFunction(() => [...document.querySelectorAll("[role=option]")].find((o) => o.innerText.trim() === "正常") ?? null, { timeout: 5000 });
  const asked = AU.page.waitForRequest((r) => r.url().includes("/admin/v1/users") && new URL(r.url()).searchParams.get("status") === "ACTIVE", { timeout: 15000 });
  await option.asElement().click();
  await asked;
  await AU.page.waitForFunction(() => new URLSearchParams(location.search).get("status") === "ACTIVE", { timeout: 5000 });
  const again = AU.page.waitForRequest((r) => r.url().includes("/admin/v1/users") && new URL(r.url()).searchParams.get("status") === "ACTIVE", { timeout: 15000 });
  await AU.page.reload({ waitUntil: "domcontentloaded" });
  await again;
  const shown = await (await AU.page.waitForFunction(() => document.querySelector('button[role=combobox][aria-label="状态"]')?.innerText.trim() || null, { timeout: 15000 })).jsonValue();
  if (shown !== "正常") throw new Error(`after a reload the filter shows "${shown}"`);
  // Paging: 20 a page, the next one as the list scrolls to its end.
  await AU.page.evaluate(() => localStorage.setItem("admin.page_size", "20"));
  await AU.page.goto(APP + "/audit", { waitUntil: "domcontentloaded" });
  await rows(AU, 20);
  const next = AU.page.waitForRequest((r) => r.url().includes("/admin/v1/audit-logs") && new URL(r.url()).searchParams.get("cursor"), { timeout: 20000 });
  await AU.page.evaluate(() => window.scrollTo(0, document.documentElement.scrollHeight));
  await next;
  await rows(AU, 21);
  await AU.page.evaluate(() => localStorage.removeItem("admin.page_size"));
  // Sorting, where a list sorts (the lists the server pages do not).
  let sorted = false;
  for (const p of ["/instruments", "/house", "/reports", "/risk"]) {
    await nav(AU, p);
    const header = await AU.page.$("main th[aria-sort] button");
    if (!header) continue;
    const before = await AU.page.$eval("main th[aria-sort]", (th) => th.getAttribute("aria-sort"));
    await header.click();
    await AU.page.waitForFunction((b) => document.querySelector("main th[aria-sort]")?.getAttribute("aria-sort") !== b, { timeout: 5000 }, before);
    sorted = true;
    break;
  }
  if (!sorted) throw new Error("no sortable column on the plain lists");
});

// --- A2: every dangerous action asks first, and nothing is written ------------------------------------------

await f.step("A2", "every dangerous action opens a confirmation with a reason and a word (all canceled); the audit trail holds nothing of it", async () => {
  // A user to act on: the first of the list.
  await nav(A, "/users");
  await rows(A);
  const userId = await A.page.$eval("main tbody tr[data-row-id]", (r) => r.dataset.rowId);
  await nav(A, `/users/${userId}`);
  await A.clickButton("修改状态");
  await confirmation(A, "the account's status");
  await A.clickButton("撤销全部挂单");
  await confirmation(A, "cancel all orders");
  // A ledger adjustment (two-person or single, either way a confirmation).
  await nav(A, `/adjustments?uid=${userId}`);
  await A.page.waitForSelector('input[aria-label="金额"]', { visible: true, timeout: 15000 });
  await A.typeInto('input[aria-label="金额"]', "1");
  await A.page.waitForFunction(() => [...document.querySelectorAll("main button")].some((b) => /执行调整|提交审批/.test(b.innerText) && !b.disabled), { timeout: 15000 });
  await A.page.evaluate(() => [...document.querySelectorAll("main button")].find((b) => /执行调整|提交审批/.test(b.innerText))?.click());
  await confirmation(A, "a ledger adjustment");
  // A flag.
  await nav(A, "/risk");
  await rows(A, 3);
  await A.page.click("main tbody tr[data-row-id] button[role=switch]");
  await confirmation(A, "a flag");
  // A contract's status (previewed, which writes nothing) and a pair's.
  await nav(A, "/derivatives");
  await rows(A);
  await menuItem(A, "main tbody tr[data-row-id]", "改为");
  await confirmation(A, "a contract's status");
  await nav(A, "/instruments?tab=pairs");
  await rows(A);
  await menuItem(A, "main tbody tr[data-row-id]", "改为");
  await confirmation(A, "a pair's status");
  // A withdrawal waiting for review, when there is one.
  await nav(A, "/withdrawals");
  await A.settled();
  const pending = await A.page.$("main tbody tr[data-row-id]");
  if (pending) {
    await pending.click();
    await A.page.waitForSelector("[role=dialog]", { visible: true });
    await A.clickButton("批准", "[role=dialog]");
    await confirmation(A, "a withdrawal's approval");
    await A.page.keyboard.press("Escape");
  } else {
    console.log("     (no withdrawal waits for review: its approval was not opened this run)");
  }
  // Sign out; the AUDITOR reads the ADMIN's trail once its sign-out is there.
  await A.page.click('header button[aria-label="账户菜单"]');
  const out = await A.page.waitForFunction(() => [...document.querySelectorAll("[role=menuitem]")].find((m) => m.innerText.trim() === "退出") ?? null, { timeout: 5000 });
  await out.asElement().click();
  await A.waitPath("/login", 15000);
  const trail = await AU.page.waitForFunction(
    async (actor, from) => {
      const r = await fetch(`/admin/v1/audit-logs?actor=${encodeURIComponent(actor)}&from=${encodeURIComponent(from)}&limit=100`, { credentials: "include" });
      if (!r.ok) return null;
      const items = (await r.json()).items ?? [];
      const actions = items.map((i) => i.payload?.action ?? i.event_type);
      return actions.includes("admin.logout") ? actions : null;
    },
    { timeout: 90000, polling: 2000 },
    ADMIN.email,
    began,
  );
  const actions = await trail.jsonValue();
  const other = actions.filter((a) => a !== "admin.login" && a !== "admin.logout");
  if (other.length) throw new Error(`the ADMIN's trail holds more than its sign-in and sign-out: ${other.join(", ")}`);
});

// --- A3: what an AUDITOR cannot do is hidden, or off with the permission named ------------------------------
// Thirteen sections, some polling (the simulated market): slow to go quiet on a slow network.

await f.step("A3", "for an AUDITOR, actions are hidden, or disabled with the permission they need named", async () => {
  const problems = [];
  // Hidden.
  await nav(AU, "/users");
  await rows(AU);
  const userId = await AU.page.$eval("main tbody tr[data-row-id]", (r) => r.dataset.rowId);
  for (const [p, labels] of [
    [`/users/${userId}`, ["修改状态", "撤销全部挂单"]],
    ["/withdrawals", ["批量批准", "批量拒绝"]],
    ["/announcements", ["新建公告"]],
    ["/help-articles", ["新建帮助文章"]],
    ["/sim/token", ["编辑"]],
  ]) {
    await nav(AU, p);
    const shown = await AU.page.evaluate((ls) => [...document.querySelectorAll("main button")].filter((b) => ls.includes(b.innerText.trim())).map((b) => b.innerText.trim()), labels);
    if (shown.length) problems.push(`${p}: ${shown.join(", ")} offered`);
  }
  // Sections it may not use are not there: the address leads to the overview.
  for (const p of ["/adjustments", "/admins"]) {
    await AU.page.evaluate((path) => {
      history.pushState({}, "", path);
      dispatchEvent(new PopStateEvent("popstate"));
    }, p);
    await AU.page.waitForFunction(() => location.pathname === "/", { timeout: 10000 }).catch(() => problems.push(`${p} opens for an AUDITOR`));
  }
  // Disabled, with the permission named.
  for (const p of ["/risk", "/settings", "/platform", "/sim/control", "/sim/bots", "/pages"]) {
    await nav(AU, p);
    await AU.page.waitForSelector("main h1", { timeout: 15000 });
    const view = await AU.page.evaluate(() => {
      const off = [...document.querySelectorAll("main button:disabled, main input:disabled, main textarea:disabled, main [aria-disabled=true]")].filter((el) => el.getBoundingClientRect().width > 0).length;
      const note = document.querySelector('main [data-testid="read-only"]');
      return { off, note: note?.innerText ?? "", perm: note?.querySelector("code")?.innerText ?? "" };
    });
    if (view.off && !/^[a-z]+(\.[a-z_]+)+$/.test(view.perm)) problems.push(`${p}: ${view.off} controls off with no permission named`);
  }
  if (problems.length) throw new Error(problems.join("\n  "));
}, { timeout: 420_000 });

await f.done();
