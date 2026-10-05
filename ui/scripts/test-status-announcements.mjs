import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import vm from "node:vm";
import { createServer } from "vite";
const server = await createServer({configFile:false, root:dirname(dirname(fileURLToPath(import.meta.url))),server:{middlewareMode:true,hmr:false,ws:false},logLevel:"error"});
const helpers = await server.ssrLoadModule("/src/taskIssuePresentation.ts");
const {buildRetryFamilies,isTerminalRun} = await server.ssrLoadModule("/src/runFamily.ts");

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const out = await mkdtemp(join(tmpdir(), "tofi-status-announcements-"));

// Exercise the actual small components' effects and rendered live-region text.
// This does not emulate screen-reader speech or claim browser accessibility QA.
function mount(source, name, nextFunction) {
  const start = source.indexOf(`export function ${name}(`);
  const end = source.indexOf(nextFunction, start);
  assert(start >= 0 && end > start);
  const slots = [];
  let index = 0, pending = [], dirty = false;
  const useRef = initial => slots[index++] ??= { current: initial };
  const useState = initial => {
    const key = index++;
    slots[key] ??= { value: initial };
    return [slots[key].value, value => {
      if (slots[key].value !== value) { slots[key].value = value; dirty = true; }
    }];
  };
  const useEffect = (effect, deps) => {
    const key = index++;
    const old = slots[key];
    if (!old || deps.some((value, i) => !Object.is(value, old[i]))) pending.push(effect);
    slots[key] = deps;
  };
  const component = vm.runInNewContext(`${source.slice(start, end).replace("export function", "function")}\n${name}`, {
    useRef, useState, useEffect, _jsx: (tag, props) => ({ tag, props }), Map, Set, isTerminalRun, window:{addEventListener(){},removeEventListener(){}}, buildRetryFamilies, taskLocale:helpers.taskLocale, taskPhaseLabel:helpers.taskPhaseLabel, presentTaskIssue:helpers.presentTaskIssue,
  });
  const mutations = [];
  let last;
  return {
    mutations,
    render(props) {
      let tree;
      for (let count = 0; count < 10; count++) {
        index = 0; pending = []; dirty = false;
        tree = component(props);
        assert.equal(tree.props.role, "status");
        assert.equal(tree.props["aria-live"], "polite");
        assert.equal(tree.props["aria-atomic"], "true");
        if (last !== tree.props.children) { last = tree.props.children; mutations.push(last); }
        pending.forEach(effect => effect());
        if (!dirty) return tree.props.children;
      }
      throw new Error("announcement effect did not settle");
    },
  };
}

try {
  await promisify(execFile)(join(root, "node_modules/.bin/tsc"), [
    "src/App.tsx", "src/BotDesktopPanel.tsx", "--ignoreConfig", "--target", "ES2022",
    "--jsx", "react-jsx", "--module", "ESNext", "--moduleResolution", "Bundler",
    "--types", "vite/client", "--resolveJsonModule", "true",
    "--outDir", out, "--skipLibCheck", "--declaration", "false", "--pretty", "false",
  ], { cwd: root });
  const app = await readFile(join(out, "App.js"), "utf8");
  const desktop = await readFile(join(out, "BotDesktopPanel.js"), "utf8");
  assert((await readFile(join(root, "src/App.tsx"), "utf8")).includes("<RunStatusAnnouncement key={active.id}"), "conversation identity must remount the announcer");
  assert((await readFile(join(root, "src/BotDesktopPanel.tsx"), "utf8")).includes("<DesktopStatusAnnouncement key={selectedBotId}"), "Bot identity must remount the announcer");
  const runMount = () => mount(app, "RunStatusAnnouncement", "\nfunction WorkingMembers");
  const viewerMount = () => mount(desktop, "DesktopStatusAnnouncement", "\nexport function BotDesktopPanel");
  const botById = new Map([["bot-a", { name: "Alpha" }]]);
  const run = (id, status, conversation_id = "a") => ({ id, status, conversation_id, bot_id: "bot-a" });
  const old = run("old", "failed");
  const props = { conversationId: "a", botById, runs: [old], ready: false };
  const live = runMount();
  assert.equal(live.render(props), "");
  props.ready = true;
  assert.equal(live.render(props), "", "initial failed history is silent");
  props.runs = [old, run("new", "running")];
  assert.match(live.render(props), /Alpha.*正在处理/);
  const count = live.mutations.length;
  for (let token = 0; token < 50; token++) {
    props.runs = props.runs.map(value => ({ ...value, updated_at: token }));
    live.render(props);
  }
  assert.equal(live.mutations.length, count, "same statuses never mutate the live region per token");
  props.runs = [old, run("new", "failed")];
  assert.match(live.render(props), /已停止/);
  props.taskAnnouncements = new Map([["new", "执行前检查缺少必要信息。本次工具调用未执行。"]]);
  assert.match(live.render(props), /缺少必要信息/);
  const semanticCount = live.mutations.length;
  props.taskAnnouncements = new Map(props.taskAnnouncements);
  live.render(props);
  assert.equal(live.mutations.length, semanticCount, "duplicate semantic SSE stays silent");
  props.taskAnnouncements = new Map([["new", "执行前检查缺少必要信息。本次工具调用未执行。随后模型服务繁忙。"]]);
  assert.match(live.render(props), /随后模型服务繁忙/);
  props.taskAnnouncements = undefined;
  props.runs = [old, run("new", "done")];
  assert.match(live.render(props), /本轮已结束/);
  props.runs.push(run("late-other-conversation", "failed", "b"));
  props.runs = [...props.runs];
  const previousCount = live.mutations.length;
  live.render(props);
  assert.equal(live.mutations.length, previousCount, "other conversation events stay silent");
  const switched = runMount();
  assert.equal(switched.render({ ...props, conversationId: "b" }), "", "switching never replays previous speech/history");
  props.ready = false;
  assert.equal(live.render(props), "");
  props.ready = true;
  assert.equal(live.render(props), "", "a fresh snapshot resets the silent baseline");

  const viewer = viewerMount();
  assert.equal(viewer.render({ ready: true, text: "未连接" }), "");
  assert.equal(viewer.render({ ready: true, text: "连接中" }), "连接中");
  assert.equal(viewer.render({ ready: true, text: "实时视频" }), "实时视频");
  const frames = viewer.mutations.length;
  for (let frame = 0; frame < 100; frame++) viewer.render({ ready: true, text: "实时视频" });
  assert.equal(viewer.mutations.length, frames, "frames never update speech");
  assert.equal(viewer.render({ ready: true, text: "" }), "", "existing alert owns connection failure");
  assert.equal(viewer.render({ ready: true, text: "实时视频" }), "实时视频", "recovery is announced");
  assert.equal(viewer.render({ ready: true, text: "截图查看" }), "截图查看");
  assert.equal(viewerMount().render({ ready: true, text: "截图查看" }), "", "another Bot starts silently");
  console.log("status announcements: PASS (snapshot silence, run transitions, identity, token/frame silence, recovery)");
} finally {
  await rm(out, { recursive: true, force: true });
  await server.close();
}
