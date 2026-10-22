// A reverse proxy's challenge (Cloudflare managed challenge) must be named, renewed once silently, then shown as one notice.
// Mocked fetch and a minimal window/document: no browser. Usage: npm run test:proxy-challenge
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createElement } from "react";
import { renderToStaticMarkup } from "react-dom/server";
import { openUiModules, uiRoot } from "./ui-modules.mjs";

// ---- minimal browser surface: events, timers, and a hidden-iframe double whose load the test scripts
const frames = [];
let frameScript = () => {};
class FakeFrame extends EventTarget {
  hidden = false; src = ""; contentDocument = null; removed = false;
  setAttribute() {}
  remove() { this.removed = true; }
}
const realSetTimeout = globalThis.setTimeout;
const longTimers = [];
globalThis.window = Object.assign(new EventTarget(), {
  // The 10 s recovery bound is fired by hand; shorter timers are real.
  setTimeout: (fn, ms) => { if (ms >= 10_000) { longTimers.push(fn); return longTimers.length; } return realSetTimeout(fn, ms); },
  clearTimeout: () => {},
  location: { reload() {} },
});
globalThis.document = {
  documentElement: {},
  createElement: () => new FakeFrame(),
  body: { append(frame) { frames.push(frame); queueMicrotask(() => frameScript(frame)); } },
};
const raised = [];
window.addEventListener("tofi:proxy-challenge", (event) => raised.push(event.detail ? "raise" : "clear"));
const sleep = (ms) => new Promise((resolve) => realSetTimeout(resolve, ms));

const ui = await openUiModules({ language: "zh-CN" });
const realFetch = globalThis.fetch;
try {
  const { request, ApiError } = await ui.load("/src/api.ts");
  const { isProxyChallenge } = await ui.load("/src/proxyChallengeRecover.ts");
  const { ProxyChallengeBanner } = await ui.load("/src/ProxyChallengeNotice.tsx");

  const html = "<!DOCTYPE html><title>Just a moment...</title>";
  const challenge = () => new Response(html, { status: 403, headers: { "content-type": "text/html; charset=UTF-8", "cf-mitigated": "challenge" } });
  const csrf = () => new Response(JSON.stringify({ error: { code: "csrf", message: "origin rejected" } }), { status: 403, headers: { "content-type": "application/json" } });
  const ok = () => new Response(JSON.stringify({ ok: true }), { status: 200, headers: { "content-type": "application/json" } });

  // ---- classification
  assert.equal(isProxyChallenge("/api/x", challenge()), true, "403 + cf-mitigated");
  assert.equal(isProxyChallenge("/api/x", new Response(html, { status: 403, headers: { "content-type": "text/html" } })), true, "403 + HTML on /api/");
  assert.equal(isProxyChallenge("/api/x", new Response(html, { status: 503, headers: { "content-type": "text/html" } })), true, "503 + HTML");
  assert.equal(isProxyChallenge("/api/x", new Response("", { status: 503, headers: { "cf-mitigated": "challenge" } })), true, "503 + cf-mitigated");
  assert.equal(isProxyChallenge("/api/x", csrf()), false, "TOFI's own JSON csrf 403 is not a challenge");
  assert.equal(isProxyChallenge("/api/x", new Response("{}", { status: 503, headers: { "content-type": "application/json" } })), false, "JSON 503 is TOFI's own");
  assert.equal(isProxyChallenge("/assets/a.js", new Response(html, { status: 403, headers: { "content-type": "text/html" } })), false, "HTML off /api/ without the header is not judged");
  assert.equal(isProxyChallenge("/api/x", new Response(html, { status: 200, headers: { "content-type": "text/html" } })), false, "only 403/503");

  // ---- csrf stays its own named case
  globalThis.fetch = async () => csrf();
  let error = await request("/api/x", { method: "POST" }).then(() => null, (cause) => cause);
  assert.ok(error instanceof ApiError);
  assert.equal(error.code, "csrf");
  assert.match(error.message, /跨站请求/, "csrf has its own words, not 请求失败（403）");
  assert.deepEqual(raised, [], "csrf raises no proxy notice");

  // ---- concurrent failures share one recovery frame, then each retries once and succeeds
  let renewed = false;
  globalThis.fetch = async () => (renewed ? ok() : challenge());
  frameScript = (frame) => realSetTimeout(() => {
    renewed = true;
    frame.contentDocument = { contentType: "text/html" }; frame.dispatchEvent(new Event("load")); // the challenge page itself
    frame.contentDocument = { contentType: "application/json" }; frame.dispatchEvent(new Event("load")); // the real answer
  }, 5);
  const results = await Promise.all([request("/api/a"), request("/api/b"), request("/api/c")]);
  assert.equal(results.length, 3);
  assert.equal(frames.length, 1, "three concurrent failures load one frame");
  assert.ok(frames[0].removed, "the frame is removed afterwards");
  assert.equal(frames[0].src, "/api/server-info", "a cheap same-origin URL, not the app shell");
  assert.deepEqual(raised, [], "silent recovery raises no notice");

  // ---- renewal never lands: the bound expires, the request is retried once, then one named notice
  let attempts = 0;
  globalThis.fetch = async () => (attempts++, challenge());
  frames.length = 0;
  frameScript = () => {};
  const pending = request("/api/a").then(() => null, (cause) => cause);
  await sleep(20);
  assert.equal(frames.length, 1, "one frame for one attempt");
  longTimers.forEach((fn) => fn());
  error = await pending;
  assert.ok(error instanceof ApiError);
  assert.equal(error.code, "proxy_challenge");
  assert.equal(error.status, 403);
  assert.ok(!error.message.includes("<") && !error.message.includes("请求失败"), `message is neither HTML nor the generic box: ${error.message}`);
  assert.equal(error.message, "站点防护（Cloudflare）需要重新校验一下。");
  assert.ok(frames[0].removed, "the frame is removed on timeout");
  assert.equal(attempts, 2, "the original request plus exactly one retry");
  assert.deepEqual(raised, ["raise"], "one notice raised");

  // ---- no loop: during the cooldown new failures load no frame and do not raise again
  attempts = 0;
  await request("/api/b").then(() => assert.fail("must fail"), (cause) => assert.equal(cause.code, "proxy_challenge"));
  assert.equal(frames.length, 1, "cooldown: no second frame");
  assert.equal(attempts, 2);
  assert.deepEqual(raised, ["raise"], "the notice is not raised twice");

  // ---- a request that gets through clears the notice
  globalThis.fetch = async () => ok();
  await request("/api/a");
  assert.deepEqual(raised, ["raise", "clear"]);

  // ---- the notice: named, short, with a Verify-again button; the per-request box is gated on it
  const banner = renderToStaticMarkup(createElement(ProxyChallengeBanner));
  assert.ok(banner.includes("站点防护（Cloudflare）需要重新校验一下。") && banner.includes("重新验证"), banner);
  await ui.setLanguage("en");
  const english = renderToStaticMarkup(createElement(ProxyChallengeBanner));
  assert.ok(english.includes("Your site&#x27;s protection (Cloudflare) needs a quick re-check.") && english.includes("Verify again"), english);
  const app = readFileSync(`${uiRoot}/src/App.tsx`, "utf8");
  assert.ok(app.includes('{error && !proxyChallenged && <div className="error-banner system-notice"'), "App hides the per-request error box while the notice is up");
  assert.ok(app.includes("workspaceErrorShown && indexInitialized.current && !proxyChallenged"), "App hides the refresh-failed toast while the notice is up");
  console.log("proxy challenge: classification, shared recovery, retry-once, notice, suppression ok");
} finally {
  globalThis.fetch = realFetch;
  await ui.close();
}
