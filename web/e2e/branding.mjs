// Browser check of the platform profile on the user sites (design
// 2026-10-04 §4.1, §4.3), run by scripts/e2e/branding.sh while it has
// changed the profile:
//
//   SITE=pc|m BRAND=<name> FAVICON=1|0 LEARNING=1|0 BANNER=<text> node web/e2e/branding.mjs
//
// Opens the deployed site and waits until it shows what the profile now
// says, without a build: the name in the top bar and the page title, the
// uploaded favicon (or the site's own), and the learning-mode banner (or
// none). Every API response is checked against the OpenAPI contracts.
import { ok, start } from "./lib.mjs";

const SITE = process.env.SITE ?? "pc";
const BRAND = process.env.BRAND ?? "";
const FAVICON = process.env.FAVICON === "1";
const LEARNING = process.env.LEARNING === "1";
const BANNER = process.env.BANNER ?? "";
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
  if (LEARNING !== notes.some((n) => n.includes(BANNER))) throw new Error(`banner: ${JSON.stringify(notes)}`);
  ok(`${SITE}: ${LEARNING ? "the learning banner says the profile's text" : "no learning banner"}`);
} catch (e) {
  await t.fail(e);
}

await t.finish();
