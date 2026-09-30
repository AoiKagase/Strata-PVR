"use strict";

// STRATA_AUDIT_PLAYWRIGHT may point to an installed Playwright package.
const { chromium } = require(process.env.STRATA_AUDIT_PLAYWRIGHT || "playwright");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const http = require("node:http");
const os = require("node:os");
const path = require("node:path");
const { test, before, after } = require("node:test");
let browser, server, baseURL;
const captures = process.env.STRATA_AUDIT_OUTPUT;
const results = [];
const now = Date.now();
const channel = { id: "audit-gr", name: "監査チャンネル", type: "GR", channel: "27", sid: 101 };
const program = { id: "audit-program", title: "監査用番組", detail: "架空の番組です", start: now - 900000, end: now + 900000, seconds: 1800, category: "news", channel };

before(async () => {
  server = http.createServer((req, res) => {
    const name = new URL(req.url, "http://localhost").pathname;
    if (name.startsWith("/api/")) {
      res.setHeader("Content-Type", "application/json");
      const key = name.slice(5);
      let data = [];
      if (key === "status") data = { operator: { alive: true }, scheduler: { alive: true } };
      if (key === "schedule") data = [{ channel, programs: [program] }];
      if (key === "schedule/broadcasting") data = [program];
      if (key === "recorded" || key === "recorded/recent") data = [program];
      if (key === "config") data = JSON.parse(fs.readFileSync(path.join(__dirname, "../config.sample.json")));
      if (key === "auth/session") data = { authenticated: false, authenticationEnabled: false };
      res.end(JSON.stringify(data));
      return;
    }
    const relative = name === "/" ? "index.html" : name === "/login" ? "login.html" : name.slice(1);
    const file = path.resolve(__dirname, relative);
    if (!file.startsWith(__dirname + path.sep) || !fs.existsSync(file) || !fs.statSync(file).isFile()) {
      res.writeHead(404).end();
      return;
    }
    res.setHeader("Content-Type", ({ ".html": "text/html", ".js": "text/javascript", ".css": "text/css" })[path.extname(file)] || "application/octet-stream");
    res.end(fs.readFileSync(file));
  });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  baseURL = "http://127.0.0.1:" + server.address().port;
  browser = await chromium.launch({ headless: true, args: ["--log-file=" + path.join(os.tmpdir(), "strata-hardening-chromium.log")] });
});

after(async () => {
  if (captures) {
    fs.mkdirSync(captures, { recursive: true });
    fs.writeFileSync(path.join(captures, "measurements.json"), JSON.stringify(results, null, 2));
  }
  if (browser) await browser.close();
  if (server) await new Promise(resolve => server.close(resolve));
});

async function pageFor(options = {}) {
  const context = await browser.newContext(options);
  context.setDefaultTimeout(2500);
  const page = await context.newPage();
  await page.addInitScript(() => {
    HTMLMediaElement.prototype.play = function () { return Promise.resolve(); };
    HTMLMediaElement.prototype.pause = function () {};
    HTMLMediaElement.prototype.load = function () {};
  });
  return { page, context };
}

async function loaded(page, route = "dashboard") {
  await page.goto(baseURL + "/#" + route);
  await page.locator("#refreshButton").waitFor({ state: "attached" });
  await page.waitForFunction(() => !document.querySelector("#refreshButton").disabled);
  await page.waitForTimeout(100);
}

async function capture(page, name) {
  if (captures) {
    await page.waitForTimeout(250);
    fs.mkdirSync(captures, { recursive: true });
    // Capture through Playwright's public CDP API: the bundled screenshot
    // helper overrides pointer media, which would invalidate touch measurements.
    const session = await page.context().newCDPSession(page);
    const size = await page.evaluate(() => ({ width: document.documentElement.scrollWidth, height: document.documentElement.scrollHeight }));
    const shot = await session.send("Page.captureScreenshot", { format: "png", captureBeyondViewport: true, clip: { x: 0, y: 0, width: size.width, height: size.height, scale: 1 } });
    fs.writeFileSync(path.join(captures, name + ".png"), Buffer.from(shot.data, "base64"));
    await session.detach();
  }
}

test("layout and contrast: desktop and touch, light and dark", async t => {
  for (const width of [1440, 375, 320]) for (const colorScheme of ["light", "dark"]) {
    await t.test(width + " " + colorScheme, async () => {
      const { page, context } = await pageFor({ viewport: { width, height: 900 }, colorScheme, isMobile: width < 1000, hasTouch: width < 1000 });
      try {
        await loaded(page);
        const contrast = async () => page.locator("#refreshButton").evaluate(e => {
          const s = getComputedStyle(e), ctx = document.createElement("canvas").getContext("2d");
          const lum = c => { ctx.fillStyle = c; ctx.fillRect(0, 0, 1, 1); const v = [...ctx.getImageData(0, 0, 1, 1).data].slice(0, 3).map(x => x / 255).map(x => x <= .04045 ? x / 12.92 : ((x + .055) / 1.055) ** 2.4); return v[0] * .2126 + v[1] * .7152 + v[2] * .0722; };
          const fg = lum(s.color), bg = lum(s.backgroundColor);
          return (Math.max(fg, bg) + .05) / (Math.min(fg, bg) + .05);
        });
        await page.mouse.move(0, 0);
        const normal = await contrast();
        await page.locator("#refreshButton").hover();
        const hover = await contrast();
        await page.mouse.move(0, 0);
        await page.locator("#refreshButton").focus();
        const focus = await contrast();
        results.push({ width, colorScheme, normal, hover, focus });
        assert.ok(Math.min(normal, hover, focus) >= 4.5, JSON.stringify({ normal, hover, focus }));
        await loaded(page, "settings");
        await page.locator("#strataMP4VideoEncoder").waitFor({ state: "visible" });
        const overflow = await page.evaluate(() => ({ viewport: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }));
        results.push({ width, colorScheme, route: "settings", ...overflow });
        assert.ok(overflow.scroll <= width, JSON.stringify(overflow));
        await page.locator("#strataMP4VideoEncoder").evaluate(e => { e.innerHTML = ""; e.add(new Option("非常に長いエンコーダー名🖥️".repeat(15), "long")); });
        const longOptionScroll = await page.evaluate(() => document.documentElement.scrollWidth);
        assert.ok(longOptionScroll <= width, "long option overflow: " + longOptionScroll);
        await loaded(page, "search");
        if (width < 1000) {
          const targets = await page.locator(".pagination-buttons button").evaluateAll(es => es.filter(e => e.getBoundingClientRect().width).map(e => ({ w: e.getBoundingClientRect().width, h: e.getBoundingClientRect().height })));
          assert.ok(targets.length >= 8);
          const coarse = await page.evaluate(() => matchMedia("(pointer: coarse)").matches);
          assert.ok(targets.every(e => e.w >= 44 && e.h >= 44), JSON.stringify({ coarse, targets }));
          results.push({ width, colorScheme, route: "search", targets });
        }
        await page.goto(baseURL + "/login");
        const login = await page.evaluate(() => ({ viewport: document.documentElement.clientWidth, scroll: document.documentElement.scrollWidth }));
        assert.ok(login.scroll <= width, JSON.stringify(login));
        results.push({ width, colorScheme, route: "login", ...login });
        await capture(page, width + "-" + colorScheme + "-login");
        // Capture after all measurements: detaching a CDP session in this
        // Chromium build resets pointer emulation to the host's mouse class.
        if (captures) {
          await loaded(page, "settings");
          await capture(page, width + "-" + colorScheme + "-settings");
          await loaded(page);
          await capture(page, width + "-" + colorScheme + "-dashboard");
        }
      } finally { await context.close(); }
    });
  }
});

test("login errors preserve input and allow retry without duplicate requests", async t => {
  for (const [status, expected] of [[401, "ユーザー名またはパスワード"], [403, "許可"], [429, "待"], [500, "サーバー"]]) {
    await t.test(String(status), async () => {
      const { page, context } = await pageFor();
      try {
        let count = 0;
        await page.route("**/api/auth/login", async route => { count++; await new Promise(r => setTimeout(r, 80)); await route.fulfill({ status }); });
        await page.goto(baseURL + "/login");
        await page.locator("#username").fill("監査利用者🙂");
        await page.locator("#password").fill("audit-password");
        await page.evaluate(() => { for (let i = 0; i < 10; i++) document.querySelector("#loginForm").dispatchEvent(new Event("submit", { cancelable: true })); });
        await page.waitForFunction(() => !document.querySelector("#submit").disabled);
        assert.equal(count, 1);
        assert.ok((await page.locator("#error").textContent()).includes(expected));
        assert.equal(await page.locator("#username").inputValue(), "監査利用者🙂");
        assert.equal(await page.locator("#password").inputValue(), "audit-password");
        await page.locator("#submit").click();
        await page.waitForFunction(() => !document.querySelector("#submit").disabled);
        assert.equal(count, 2);
      } finally { await context.close(); }
    });
  }
});

test("login network failure, timeout and success", async t => {
  for (const mode of ["network", "timeout", "timeout-no-abort", "success"]) await t.test(mode, async () => {
    const { page, context } = await pageFor();
    try {
      if (mode === "timeout-no-abort") await page.addInitScript(() => { window.AbortController = undefined; });
      await page.route("**/api/auth/login", route => mode === "network" ? route.abort() : mode === "success" ? route.fulfill({ status: 204 }) : new Promise(() => {}));
      await page.goto(baseURL + "/login");
      if (mode.startsWith("timeout")) await page.clock.install();
      await page.locator("#username").fill("user");
      await page.locator("#password").fill("password");
      await page.locator("#submit").click();
      if (mode === "success") { await page.waitForURL(baseURL + "/"); return; }
      if (mode.startsWith("timeout")) await page.clock.fastForward(15001);
      await page.waitForFunction(() => !document.querySelector("#submit").disabled, null, { timeout: 1000 });
      const message = await page.locator("#error").textContent();
      assert.ok(message.includes(mode === "network" ? "接続" : "応答"), message);
      assert.ok(message.includes("再試行"), message);
    } finally { await context.close(); }
  });
});

const mseStub = `window.__mseURLs = []; window.mpegts = {isSupported:()=>true,getFeatureList:()=>({mseLivePlayback:true}),Events:{ERROR:'error'},createPlayer:o=>{window.__mseURLs.push(o.url);return {on(){},attachMediaElement(){},load(){},play(){return Promise.resolve()},pause(){},unload(){},detachMediaElement(){},destroy(){}}}};`;

test("dialog fallback opens a valid dedicated live player", async () => {
  const { page, context } = await pageFor();
  try {
    await page.addInitScript(() => { HTMLDialogElement.prototype.showModal = undefined; });
    await page.route("**/mpegts.js", route => route.fulfill({ contentType: "text/javascript", body: mseStub }));
    await loaded(page);
    await page.locator("#onAirList button", { hasText: "視聴" }).first().click();
    await page.waitForURL(url => url.pathname === "/player.html");
    const url = new URL(page.url());
    assert.match(url.searchParams.get("src"), /\/api\/channel\/audit-gr\/watch\.m2ts\?mode=mse/);
    assert.match(url.searchParams.get("subtitles"), /mode=mse/);
  } finally { await context.close(); }
});

test("mpegts is lazy, shared and used before live URL selection", async () => {
  const { page, context } = await pageFor();
  try {
    let requests = 0;
    await page.route("**/mpegts.js", route => { requests++; return route.fulfill({ contentType: "text/javascript", body: mseStub }); });
    await loaded(page);
    assert.equal(requests, 0);
    await loaded(page, "recorded");
    await page.locator("[data-view='recorded'] button", { hasText: "視聴" }).first().click();
    assert.equal(requests, 0);
    await page.locator("#playerDialogClose").click();
    await loaded(page);
    await page.locator("#onAirList button", { hasText: "視聴" }).first().click();
    await page.waitForFunction(() => window.__mseURLs && window.__mseURLs.length === 1);
    assert.equal(requests, 1);
    assert.match(await page.evaluate(() => window.__mseURLs[0]), /watch\.m2ts\?mode=mse/);
    await page.locator("#playerDialogClose").click();
    await page.locator("#onAirList button", { hasText: "視聴" }).first().click();
    await page.waitForFunction(() => window.__mseURLs.length === 2);
    assert.equal(requests, 1);
  } finally { await context.close(); }
});

test("failed mpegts loading can retry; closing during loading never starts media", async t => {
  for (const mode of ["retry", "close", "switch", "timeout", "unsupported", "hls"]) await t.test(mode, async () => {
    const { page, context } = await pageFor();
    try {
      let requests = 0, release;
      if (mode === "hls") await page.addInitScript(() => { HTMLMediaElement.prototype.canPlayType = () => "probably"; Object.defineProperty(navigator, "userAgent", { get: () => "Mozilla/5.0 (iPhone) AppleWebKit/605.1.15 Version/17.0 Mobile Safari/604.1" }); });
      await page.route("**/mpegts.js", async route => {
        requests++;
        if (mode === "retry" && requests === 1) return route.abort();
        if (mode === "timeout") return new Promise(() => {});
        if (mode === "close" || mode === "switch") await new Promise(resolve => { release = resolve; });
        return route.fulfill({ contentType: "text/javascript", body: mode === "unsupported" ? "window.mpegts={isSupported:()=>false};" : mseStub });
      });
      await loaded(page);
      if (mode === "timeout") await page.clock.install();
      await page.locator("#onAirList button", { hasText: "視聴" }).first().click();
      if (mode === "hls") { assert.equal(requests, 0); await page.waitForFunction(() => document.querySelector("#playerVideo").src.includes("hls/index.m3u8")); return; }
      if (mode === "unsupported") { await page.waitForFunction(() => document.querySelector("#playerVideo").src.includes("watch.mp4")); return; }
      if (mode === "close" || mode === "switch") {
        await page.waitForFunction(() => document.querySelector("script[src='/mpegts.js']"));
        await page.waitForFunction(() => document.querySelector("#playerDialog").open);
        assert.equal(await page.locator("#playerOpenLink").isVisible(), false);
        if (mode === "close") {
          await page.locator("#playerDialogClose").click();
        } else {
          // A new live intent shares the pending script but invalidates the old generation.
          await page.evaluate(() => document.querySelector("#onAirList .live-channel-actions button").click());
        }
        release();
        await page.waitForFunction(() => window.mpegts);
        if (mode === "switch") await page.waitForFunction(() => window.__mseURLs.length === 1);
        assert.equal(await page.evaluate(() => window.__mseURLs.length), mode === "close" ? 0 : 1);
        assert.equal(requests, 1);
        return;
      }
      if (mode === "timeout") await page.clock.fastForward(15001);
      await page.locator("#playerRetryButton").waitFor({ state: "visible", timeout: 1500 });
      assert.ok((await page.locator("#playerStatusMessage").textContent()).includes("再試行"));
      assert.equal(await page.evaluate(() => document.querySelectorAll("script[src='/mpegts.js']").length), 0);
      if (mode === "retry") {
        await page.locator("#playerRetryButton").click();
        await page.waitForFunction(() => window.__mseURLs && window.__mseURLs.length === 1);
        assert.equal(requests, 2);
      }
    } finally { await context.close(); }
  });
});
