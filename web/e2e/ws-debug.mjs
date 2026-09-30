// ws-debug.mjs: sign in on the deployed site and print the WebSocket frames (debugging aid, not a test).
import { existsSync } from "node:fs";
import puppeteer from "puppeteer-core";

const APP = process.env.APP ?? "https://astras.vip";
const [email, password] = [process.env.EMAIL, process.env.PASSWORD];
const CHROME = ["/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"].find((p) => existsSync(p));
const browser = await puppeteer.launch({ executablePath: CHROME, headless: true, args: ["--lang=zh-CN"] });
const page = await browser.newPage();
await page.setViewport({ width: 1440, height: 900 });
const cdp = await page.createCDPSession();
await cdp.send("Network.enable");
const t0 = Date.now();
const log = (dir, text) => {
  let s = text;
  try {
    const j = JSON.parse(text);
    if (j.op) s = JSON.stringify({ op: j.op, ok: j.ok, code: j.code, args: j.args });
    else if (j.channel) s = JSON.stringify({ channel: j.channel, type: j.type, seq: j.seq });
  } catch {}
  if (!/"channel":"(depth|ticker|tickers|trades|candles|mark-price)/.test(s)) console.log(((Date.now() - t0) / 1000).toFixed(1), dir, s.slice(0, 200));
};
cdp.on("Network.webSocketFrameSent", (e) => log(">>", e.response.payloadData));
cdp.on("Network.webSocketFrameReceived", (e) => log("<<", e.response.payloadData));
cdp.on("Network.webSocketCreated", (e) => console.log("ws created", e.url));
cdp.on("Network.webSocketClosed", () => console.log("ws closed"));
await page.goto(APP + "/login", { waitUntil: "networkidle2" });
await page.type('input[autocomplete="username"]', email);
await page.type('input[autocomplete="current-password"]', password);
await page.keyboard.press("Enter");
await page.waitForFunction(() => location.pathname !== "/login", { timeout: 20000 });
await page.goto(APP + "/trade/BTC-USDT", { waitUntil: "networkidle2" });
await page.waitForFunction(() => Number(document.querySelector('input[aria-label="价格"]')?.value) > 0, { timeout: 20000 });
await new Promise((r) => setTimeout(r, 3000));
const last = await page.$eval('input[aria-label="价格"]', (el) => Number(el.value));
const price = (Math.floor(last * 0.95 * 100) / 100).toFixed(2);
const fill = async (sel, v) => { await page.click(sel, { clickCount: 3 }); await page.keyboard.press("Backspace"); await page.type(sel, v); };
await fill('input[aria-label="价格"]', price);
await fill('input[aria-label="数量"]', "0.0002");
console.log("placing", price);
await page.evaluate(() => [...document.querySelectorAll("button")].find((b) => b.innerText.trim() === "买入 BTC")?.click());
await page.waitForSelector("[role=dialog]");
await page.evaluate(() => [...document.querySelectorAll("[role=dialog] button")].find((b) => b.innerText.trim() === "买入")?.click());
await new Promise((r) => setTimeout(r, 5000));
console.log("cancelling");
await page.evaluate(() => [...document.querySelectorAll("button")].find((b) => b.innerText.trim() === "撤单")?.click());
await new Promise((r) => setTimeout(r, 8000));
console.log("tabs", await page.evaluate(() => [...document.querySelectorAll("[role=tab]")].map((t) => t.innerText.replace(/\s+/g, "")).join(" ")));
await browser.close();
