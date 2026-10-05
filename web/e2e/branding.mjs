// Browser check of the platform profile on the user sites (design
// 2026-10-04 §4.1, §4.3, §4.4), run by scripts/e2e/branding.sh and
// launch-drill.sh while they have changed the profile:
//
//   SITE=pc|m BRAND=<name> FAVICON=1|0 TESTMODE=1|0 BANNER=<text> [LAUNCH=1 [TERMS_TITLE=<title>]] [CONTENT=test|formal] node web/e2e/branding.mjs
//
// Opens the deployed site and waits until it shows what the profile now
// says, without a build: the name in the top bar and the page title, the
// uploaded favicon (or the site's own), and the test-mode banner (TESTMODE=1)
// or none. With LAUNCH=1 (launch-drill.sh) also what a live exchange shows:
// no 测试模式 badge and no welcome-credit copy on the home and sign-up
// pages, the console's terms on the terms page (titled TERMS_TITLE when
// given), and the help centre's live content (CONTENT is formal then).
// CONTENT=test checks the help centre's test content instead.
// Every API response is checked against the OpenAPI contracts.
import { ok, start } from "./lib.mjs";

const SITE = process.env.SITE ?? "pc";
const BRAND = process.env.BRAND ?? "";
const FAVICON = process.env.FAVICON === "1";
const TESTMODE = process.env.TESTMODE === "1";
const BANNER = process.env.BANNER ?? "";
const LAUNCH = process.env.LAUNCH === "1";
const CONTENT = process.env.CONTENT ?? (LAUNCH ? "formal" : "");
const TERMS_TITLE = process.env.TERMS_TITLE ?? "";
// What a site in test mode says that a live one must not: the 测试模式
// badges, the welcome-credit promises and the test environment's words
// (zh-CN, the language of the run).
const TEST_ONLY = ["测试模式", "注册即得", "注册即领", "测试环境", "模拟", "Sepolia", "测试网"];
// The help pages' test-only words (design §4.4: the drafts' :::test blocks).
const HELP_TEST_WORDS = ["测试环境", "模拟", "Sepolia", "10,000"];
const HELP = { fees: "费率说明", deposit: "如何充值", faq: "常见问题" };
const APP = SITE === "m" ? "https://m.astras.vip" : "https://astras.vip";

const device =
  SITE === "m"
    ? {
        viewport: { width: 390, height: 844, deviceScaleFactor: 3, isMobile: true, hasTouch: true },
        userAgent:
          "Mozilla/5.0 (iPhone; CPU iPhone OS 18_5 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.5 Mobile/15E148 Safari/604.1",
      }
    : { viewport: { width: 1440, height: 900 } };
const t = await start({ app: APP, api: APP, name: `branding-${SITE}`, device });
const { page, go } = t;

try {
  await go(SITE === "m" ? "/" : "/markets");
  // The profile is read at start: the name replaces the built-in one in the top bar.
  await page.waitForFunction(
    (name) => (document.querySelector("header")?.innerText ?? "").toLowerCase().includes(name.toLowerCase()),
    { timeout: 20000 },
    BRAND,
  );
  ok(`${SITE}: the top bar shows ${BRAND}`);
  if (SITE === "pc") {
    await page.waitForFunction((name) => document.title.endsWith(name), { timeout: 10000 }, BRAND);
    ok(`${SITE}: the page title ends with ${BRAND}`);
  }
  const icon = await page.$eval('link[rel="icon"]', (l) => l.getAttribute("href") ?? "");
  if (FAVICON !== icon.startsWith("/v1/platform/images/favicon")) throw new Error(`favicon ${icon}`);
  ok(`${SITE}: the favicon is ${FAVICON ? "the uploaded one" : "the site's own"} (${icon})`);
  const notes = await page.$$eval("[role=note]", (els) => els.map((e) => e.innerText.trim()));
  if (TESTMODE !== notes.some((n) => n.includes(BANNER))) throw new Error(`banner: ${JSON.stringify(notes)}`);
  ok(`${SITE}: ${TESTMODE ? "the test-mode banner says the profile's text" : "no test-mode banner"}`);
  if (LAUNCH) {
    for (const path of ["/", "/register"]) {
      await go(path);
      if (path === "/") {
        await page.waitForFunction((name) => (document.querySelector("header")?.innerText ?? "").toLowerCase().includes(name.toLowerCase()), { timeout: 20000 }, BRAND);
      }
      // The home page's hero or welcome card, and the sign-up form's subtitle, have rendered.
      await page.waitForFunction(() => document.querySelector("h1, h2") !== null, { timeout: 20000 });
      const text = await page.evaluate(() => document.body.innerText);
      const found = TEST_ONLY.filter((s) => text.includes(s));
      if (found.length) throw new Error(`${path} still says ${JSON.stringify(found)}`);
    }
    ok(`${SITE}: no 测试模式 badge, welcome-credit copy or test-environment words on the home and sign-up pages`);
    // The terms page shows the console's article: the page reads it from
    // the API (not the bundled draft) and heads it with the article's
    // title, the one the drill published (TERMS_TITLE) when it did.
    const served = page.waitForResponse((r) => new URL(r.url()).pathname === "/v1/legal/terms", { timeout: 20000 });
    served.catch(() => {});
    await go("/legal/terms");
    const terms = await served;
    if (terms.status() !== 200) throw new Error(`the terms page read /v1/legal/terms: ${terms.status()}`);
    const title = (await terms.json()).title;
    if (TERMS_TITLE && title !== TERMS_TITLE) throw new Error(`the terms are titled ${title}, not ${TERMS_TITLE}`);
    await page.waitForFunction((want) => [...document.querySelectorAll("article h1")].some((h) => h.textContent?.trim() === want), { timeout: 20000 }, title);
    ok(`${SITE}: the terms page shows the console's article (${title})`);
  }
  if (CONTENT) {
    // The help pages as the mode renders them: live without the test-only
    // words, in test mode with their test blocks, which all name Sepolia
    // (the page's own test-mode notice says 模拟, so that word proves
    // nothing there). The article is up once its heading is: the sidebar
    // lists the same titles.
    for (const [slug, title] of Object.entries(HELP)) {
      await go(`/help/${slug}`);
      await page.waitForFunction((t) => [...document.querySelectorAll("h1")].some((h) => h.textContent?.trim() === t), { timeout: 20000 }, title);
      if (CONTENT === "formal") {
        const text = await page.evaluate(() => document.querySelector("main")?.innerText ?? document.body.innerText);
        const found = HELP_TEST_WORDS.filter((w) => text.includes(w));
        if (found.length) throw new Error(`/help/${slug} live still says ${JSON.stringify(found)}`);
      } else {
        // The site renders before the profile says test mode: wait for it.
        await page
          .waitForFunction(() => (document.querySelector("main")?.innerText ?? document.body.innerText).includes("Sepolia"), { timeout: 20000 })
          .catch(() => {
            throw new Error(`/help/${slug} in test mode does not say Sepolia`);
          });
      }
    }
    ok(`${SITE}: the help pages (fees, deposit, FAQ) show the ${CONTENT === "formal" ? "live" : "test"} content`);
  }
} catch (e) {
  await t.fail(e);
}

await t.finish();
