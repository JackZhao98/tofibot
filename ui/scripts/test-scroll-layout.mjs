// Exercise the real follower hook's callbacks with deterministic geometry and frame scheduling.
// In particular, a native scroll-anchoring notification can precede ResizeObserver after lazy work rows mount.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";
import { stripTypeScriptTypes } from "node:module";

const source = readFileSync(new URL("../src/useConversationScroll.ts", import.meta.url), "utf8");
function open(reduce = false) {
  const effects = [], frames = new Map(), handlers = new Map();
  let time = 1000, nextFrame = 0, top = 0;
  const list = { clientWidth: 700 };
  const pane = {
    clientWidth: 700, clientHeight: 600, scrollHeight: 2000,
    get scrollTop() { return top; },
    set scrollTop(value) { top = Math.max(0, Math.min(value, this.scrollHeight - this.clientHeight)); },
    querySelector: selector => selector === ".message-list" ? list : null,
    querySelectorAll: () => [],
    getBoundingClientRect: () => ({ top: 0 }),
    addEventListener: (name, fn) => handlers.set(name, fn),
    removeEventListener() {},
  };
  const modules = {
    react: { useRef: current => ({ current }), useState: value => [value, () => {}], useCallback: fn => fn, useLayoutEffect: fn => effects.push(fn) },
    "./desktop": { desktopState: () => ({}), updateDesktopState() {} },
    "./arrivalCascade": { cascadeRoom: () => 0, fadeArrival() {}, holdArrivalCascade() {}, playArrivalCascade() {} },
  };
  const context = {
    exports: {}, require: name => { assert.ok(modules[name], name); return modules[name]; },
    performance: { now: () => time },
    document: { visibilityState: "visible", addEventListener() {}, removeEventListener() {} },
    window: { matchMedia: query => ({ matches: query.includes("reduced-motion") ? reduce : true, addEventListener() {}, removeEventListener() {} }), addEventListener() {}, removeEventListener() {} },
    requestAnimationFrame: fn => { frames.set(++nextFrame, fn); return nextFrame; },
    cancelAnimationFrame: id => frames.delete(id),
    ResizeObserver: class { observe() {} disconnect() {} },
  };
  const executable = stripTypeScriptTypes(source).replace(/^import .*;$/gm, "").replace(/export /g, "");
  vm.runInNewContext(`const { useRef, useState, useCallback, useLayoutEffect } = require("react");
    const { desktopState, updateDesktopState } = require("./desktop");
    const { cascadeRoom, fadeArrival, holdArrivalCascade, playArrivalCascade } = require("./arrivalCascade");
    ${executable}
exports.useConversationScroll = useConversationScroll;`, context);
  const follower = context.exports.useConversationScroll({
    conversation: { id: "chat", read_seq: 99 }, messages: [{ id: "previous", conversation_id: "chat", seq: 1, role: "user" }],
    ready: true, hasMore: false, loadingOlder: false, mobileList: false, markRead() {},
  });
  follower.scrollRef.current = pane;
  effects[0](); // Position the loaded history.
  effects[3](); // Register reading gestures and the resize observer.
  time += 800; // Beyond initial-layout settling.
  const settle = () => {
    for (let n = 0; frames.size && n < 100; n++) {
      time += 16;
      const batch = [...frames.values()]; frames.clear();
      batch.forEach(fn => fn(time));
    }
    assert.equal(frames.size, 0, "the glide settled");
  };
  return { pane, follower, handlers, settle };
}

for (const reduced of [false, true]) {
  const { pane, follower, settle } = open(reduced);
  assert.equal(pane.scrollTop, 1400);
  // A work timeline grows above the current view; native anchoring adjusts the offset only partly.
  pane.scrollHeight += 100;
  pane.scrollTop += 20;
  follower.onScroll(); // Delivered before the resize notification.
  if (reduced) assert.equal(pane.scrollTop, 1500, "reduced motion lands during the layout notification");
  settle();
  assert.equal(pane.scrollTop, 1500, "lazy history growth retains following");
  // The draft's row then grows, through its own subscription, without a Workspace render.
  pane.scrollHeight += 80;
  follower.reposition(); // ResizeObserver callback.
  settle();
  assert.equal(pane.scrollTop, 1580, "subsequent live text growth is still followed");
}

{
  const { pane, follower, handlers, settle } = open();
  pane.scrollHeight += 100;
  handlers.get("wheel")({ deltaY: -100 });
  pane.scrollTop -= 100;
  const readingTop = pane.scrollTop;
  follower.onScroll();
  follower.reposition();
  settle();
  assert.equal(pane.scrollTop, readingTop, "an explicit reading gesture wins even with a pending layout change");
}
{
  const { pane, follower, settle } = open();
  pane.scrollTop -= 200;
  follower.onScroll(); // Scrollbar / keyboard scrolling with no intervening layout change.
  pane.scrollHeight += 80;
  follower.reposition();
  settle();
  assert.equal(pane.scrollTop, 1200, "a reader away from the bottom stays where they are");
}
console.log("scroll layout ordering checks passed");
