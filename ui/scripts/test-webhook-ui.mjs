// Finite tests of shared origin helpers, real API methods and the compiled panel.
// Every response is synthetic; no listener, provider or dependency install is used.
import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";
import vm from "node:vm";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const output = await mkdtemp(join(tmpdir(), "tofi-webhook-ui-"));
const requests = [], responses = [];
const issued = (id, version = 1) => ({ configured: true, enabled: true, hook_id: `synthetic-${id}`, version, url: `https://synthetic.invalid/api/webhooks/synthetic-${id}`, secret: `SYNTHETIC_SECRET_${id}_${version}` });
const absent = { configured: false, enabled: false };
const globals = { fetch: globalThis.fetch, window: globalThis.window, navigator: Object.getOwnPropertyDescriptor(globalThis, "navigator") };
try {
  await promisify(execFile)(join(root, "node_modules/.bin/tsc"), ["src/messageOrigin.ts", "src/botActivity.ts", "src/api.ts", "src/WebhookPanel.tsx", "src/App.tsx", "src/desktop.ts", "--jsx", "react-jsx", "--moduleResolution", "bundler", "--types", "vite/client", "--allowSyntheticDefaultImports", "true", "--ignoreConfig", "--target", "ES2022", "--module", "ES2022", "--outDir", output, "--skipLibCheck", "--declaration", "false"], { cwd: root });
  const { isWebhookMessage, messageSourceLabel, messageOriginKey, isRetryBoundaryMessage } = await import(pathToFileURL(join(output, "messageOrigin.js")));
  const { latestHumanMessageTime } = await import(pathToFileURL(join(output, "botActivity.js")));
  const { api, ApiError } = await import(pathToFileURL(join(output, "api.js")));
  const human = { id: "human", role: "user", seq: 1, conversation_id: "A", content: "Synthetic human", created_at: "2026-10-04T00:00:00Z" };
  const webhook = { ...human, id: "event", kind: "webhook_event", seq: 2, created_at: "2026-10-04T01:00:00Z" };
  assert.equal(isWebhookMessage(webhook), true);
  assert.equal(messageSourceLabel(webhook, "Spoofed name"), "Webhook");
  assert.equal(messageSourceLabel(human), "你");
  assert.notEqual(messageOriginKey(webhook), messageOriginKey(human));
  assert.equal(isRetryBoundaryMessage(webhook), false);
  assert.equal(isRetryBoundaryMessage(human), true);
  assert.equal(latestHumanMessageTime([human, webhook], []), Date.parse(human.created_at));
  assert.equal(latestHumanMessageTime([webhook], []), 0);

  const windowEvents = new EventTarget();
  globalThis.window = { setTimeout, clearTimeout, addEventListener: windowEvents.addEventListener.bind(windowEvents), removeEventListener: windowEvents.removeEventListener.bind(windowEvents), dispatchEvent: windowEvents.dispatchEvent.bind(windowEvents) };
  const copies = [];
  Object.defineProperty(globalThis, "navigator", { configurable: true, value: { clipboard: { writeText: async text => { copies.push(text); } } } });
  globalThis.fetch = async (path, init) => {
    requests.push({ path, method: init?.method ?? "GET", body: init?.body, signal: init?.signal, cache: init?.cache, headers: init?.headers });
    assert.ok(responses.length, `Unexpected synthetic request ${path}`);
    const next = responses.shift();
    return typeof next === "function" ? next() : next;
  };

  const source = await readFile(join(output, "WebhookPanel.js"), "utf8");
  const compiled = source.replace(/^import .*;\n/gm, "").replace("export function WebhookPanel", "function WebhookPanel") + "\nglobalThis.component = WebhookPanel;";
  function mount(initial) {
    const slots = [], effects = [];
    let cursor = 0, dirty = true, tree, props = initial;
    const context = vm.createContext({ api, ApiError, window: globalThis.window, navigator: globalThis.navigator, AbortController, Number, JSON, Error, flushSync: callback => callback(),
      _jsx: (type, props) => ({ type, props }), _jsxs: (type, props) => ({ type, props }), _Fragment: "Fragment", ConfirmAction: "ConfirmAction", TofiIcon: "TofiIcon",
      useState: initial => { const index = cursor++; if (!(index in slots)) slots[index] = initial; return [slots[index], next => { slots[index] = typeof next === "function" ? next(slots[index]) : next; dirty = true; }]; },
      useRef: initial => { const index = cursor++; return slots[index] ??= { current: initial }; },
      useLayoutEffect: (callback, deps) => { const index = cursor++, previous = slots[index]; if (!previous || deps.some((value, i) => value !== previous.deps[i])) effects.push(() => { previous?.cleanup?.(); slots[index] = { deps, cleanup: callback() }; }); },
    });
    vm.runInContext(compiled, context);
    function render() { do { dirty = false; cursor = 0; tree = context.component(props); while (effects.length) effects.shift()(); } while (dirty); return tree; }
    async function flush() { for (let step = 0; step < 8; step++) { await new Promise(resolve => setImmediate(resolve)); if (dirty) render(); } return tree; }
    render();
    return { render: next => { props = next; dirty = true; return render(); }, flush, tree: () => tree, unmount: () => { for (const slot of slots) slot?.cleanup?.(); } };
  }
  function nodes(tree) { if (!tree || typeof tree !== "object") return []; if (Array.isArray(tree)) return tree.flatMap(nodes); return [tree, ...nodes(tree.props?.children)]; }
  function text(tree) { if (typeof tree === "string") return tree; if (Array.isArray(tree)) return tree.map(text).join(""); return tree && typeof tree === "object" ? text(tree.props?.children) : ""; }
  const app = await readFile(join(output, "App.js"), "utf8");
  const bubbleSource = app.slice(app.indexOf("export function MessageBubble("), app.indexOf("export function RunStatusAnnouncement("));
  const markerSource = app.slice(app.indexOf("function readReplyMarker("), app.indexOf("export function MessageBubble("));
  const display = vm.createContext({ _jsx: (type, props) => ({ type, props }), _jsxs: (type, props) => ({ type, props }), _Fragment: "Fragment", isWebhookMessage, messageSourceLabel,
    useUserTimezone: () => ({ timezone: "UTC" }), useRef: () => ({ current: null }), useLayoutEffect() {}, isDesktop: false, SmoothStreamMarkdown: "SmoothStreamMarkdown", MessageAttachment: "MessageAttachment", previewText: value => value,
  });
  vm.runInContext(markerSource + bubbleSource.replace("export function MessageBubble", "function MessageBubble") + "\nglobalThis.bubble = MessageBubble;", display);
  const marker = "[message_id=11111111-1111-4111-8111-111111111111] SYNTHETIC_EXTERNAL_EVENT";
  const externalTree = display.bubble({ message: { ...webhook, content: marker }, showAvatar: false, showIdentity: false, compact: true, replyTarget: human, replyTargetName: "你" });
  assert.equal(externalTree.props["data-message-origin"], "webhook_event");
  assert.ok(text(externalTree).includes("Webhook外部事件"), "external label survives DM/compact presentation");
  assert.ok(!text(externalTree).includes("你"));
  assert.equal(nodes(externalTree).find(node => node.type === "SmoothStreamMarkdown").props.content, marker, "external reply markers remain literal payload");
  assert.equal(nodes(externalTree).filter(node => node.props?.className === "message-reply-quote").length, 0, "external payload cannot create a trusted quote");
  const humanTree = display.bubble({ message: human, showAvatar: false });
  assert.ok(humanTree.props.className.includes("message-user"));
  const replyTree = display.bubble({ message: { ...human, role: "assistant" }, replyTarget: webhook, replyTargetName: messageSourceLabel(webhook), showAvatar: false });
  assert.ok(text(replyTree).includes("回复 Webhook"));
  const button = (panel, name) => { const node = nodes(panel.tree()).find(node => node.type === "button" && text(node) === name); assert.ok(node, `Missing button ${name}`); return node; };
  const inputSecret = panel => nodes(panel.tree()).find(node => node.props?.["aria-label"] === "Webhook 一次性密钥");
  const props = { conversationId: "A", conversationName: "Synthetic A", accountKey: "synthetic-owner", active: true, onClose() {} };
  responses.push(Response.json(absent));
  const panel = mount(props);
  await panel.flush();
  assert.equal(requests.length, 1);
  assert.equal(requests[0].method, "GET", "mount must never issue a grant");
  assert.equal(inputSecret(panel), undefined);
  responses.push(Response.json(issued("A"), { status: 201 }));
  button(panel, "创建 Webhook").props.onClick(); await panel.flush();
  assert.equal(inputSecret(panel).props.value, "SYNTHETIC_SECRET_A_1");
  assert.equal(copies.length, 0, "issuance must not auto-copy");
  button(panel, "复制密钥").props.onClick(); await panel.flush();
  assert.deepEqual(copies, ["SYNTHETIC_SECRET_A_1"]);
  button(panel, "隐藏密钥").props.onClick(); await panel.flush();
  assert.equal(inputSecret(panel), undefined);
  responses.push(Response.json({ ...issued("A"), secret: "GET_MUST_NOT_REVEAL" }));
  button(panel, "重新载入").props.onClick(); await panel.flush();
  assert.equal(inputSecret(panel), undefined, "GET cannot reveal even a malformed extra secret field");
  const rotate = nodes(panel.tree()).find(node => node.type === "ConfirmAction" && node.props.label === "轮换密钥");
  responses.push(Response.json(issued("A", 2)));
  rotate.props.onConfirm(); await panel.flush();
  assert.equal(inputSecret(panel).props.value, "SYNTHETIC_SECRET_A_2");
  assert.deepEqual(JSON.parse(requests.at(-1).body), { expected_version: 1 });
  const revoke = nodes(panel.tree()).find(node => node.type === "ConfirmAction" && node.props.label === "撤销 Webhook");
  responses.push(new Response(null, { status: 204 }));
  revoke.props.onConfirm(); await panel.flush();
  assert.deepEqual(JSON.parse(requests.at(-1).body), { expected_version: 2 });
  assert.equal(requests.at(-1).method, "DELETE");
  assert.equal(inputSecret(panel), undefined, "revocation wipes any previously shown secret");
  assert.ok(button(panel, "重新启用 Webhook"));
  responses.push(Response.json(issued("A", 3), { status: 201 }));
  button(panel, "重新启用 Webhook").props.onClick(); await panel.flush();
  const staleRotate = nodes(panel.tree()).find(node => node.type === "ConfirmAction" && node.props.label === "轮换密钥");
  responses.push(Response.json({ error: { code: "webhook_conflict", message: "SYNTHETIC_UNTRUSTED_ERROR" } }, { status: 409 }));
  staleRotate.props.onConfirm(); await panel.flush();
  assert.equal(inputSecret(panel), undefined, "stale/failing operation wipes issuance");
  assert.ok(text(panel.tree()).includes("状态已更改"));
  assert.ok(!text(panel.tree()).includes("SYNTHETIC_UNTRUSTED_ERROR"), "server error text is never echoed");
  panel.render({ ...props, active: false });
  assert.equal(inputSecret(panel), undefined, "close hides before the presence exit animation");

  responses.push(Response.json(absent));
  panel.render({ ...props, conversationId: "B", active: true }); await panel.flush();
  let release;
  responses.push(() => new Promise(resolve => { release = resolve; }));
  button(panel, "创建 Webhook").props.onClick(); await panel.flush();
  const staleSignal = requests.at(-1).signal;
  responses.push(Response.json(absent));
  panel.render({ ...props, conversationId: "G", active: true }); await panel.flush();
  assert.equal(staleSignal.aborted, true);
  release(Response.json(issued("B"), { status: 201 })); await panel.flush();
  assert.equal(inputSecret(panel), undefined, "an aborted response cannot fill another conversation");
  assert.ok(button(panel, "创建 Webhook"));
  responses.push(Response.json(issued("G"), { status: 201 }));
  button(panel, "创建 Webhook").props.onClick(); await panel.flush();
  assert.equal(inputSecret(panel).props.value, "SYNTHETIC_SECRET_G_1");
  window.dispatchEvent(new Event("pagehide")); await panel.flush();
  assert.equal(inputSecret(panel), undefined, "pagehide clears before a back/forward cache snapshot");
  responses.push(Response.json(issued("G", 2)));
  nodes(panel.tree()).find(node => node.type === "ConfirmAction" && node.props.label === "轮换密钥").props.onConfirm(); await panel.flush();
  assert.equal(inputSecret(panel).props.value, "SYNTHETIC_SECRET_G_2");
  responses.push(Response.json(absent));
  panel.render({ ...props, conversationId: "G", accountKey: "synthetic-second-owner" }); await panel.flush();
  assert.equal(inputSecret(panel), undefined, "account changes wipe the one-time secret");
  panel.unmount();
  assert.ok(requests.every(request => request.cache === "no-store"));
  assert.ok(requests.every(request => !request.headers.has("Authorization")));
  assert.ok(requests.every(request => !JSON.stringify([request.path, request.body]).includes("SYNTHETIC_SECRET")));
  assert.equal(responses.length, 0);
  console.log("webhook UI: PASS (compiled panel/bubble + actual API; explicit issuance/copy, metadata-only reload, close/pagehide/account/target secret wipe, delayed-response abort, rotate/revoke version CAS, conflict secret wipe, provenance/retry/activity)");
  console.log("Browser fixture: test-fixtures/runtime/webhook-audit.html (desktop/narrow focus, full-App labels and navigation require browser acceptance).");
} finally {
  globalThis.fetch = globals.fetch;
  if (globals.window === undefined) delete globalThis.window; else globalThis.window = globals.window;
  if (globals.navigator) Object.defineProperty(globalThis, "navigator", globals.navigator); else delete globalThis.navigator;
  await rm(output, { recursive: true, force: true });
}
