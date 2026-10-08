import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { promisify } from "node:util";
import { openUiModules } from "./ui-modules.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const ui = await openUiModules();
try {
  const { scheduledRunMetadata } = await ui.load("/src/scheduledRunMetadata.ts");
  const message = { run_id: "root", conversation_id: "chat", content: "Original task\nFull task instructions", created_at: "2026-09-29T00:00:01Z" };
  const run = { id: "root", status: "done", updated_at: "2026-09-29T00:01:00Z" };
  const occurrence = { root_run_id: "root", schedule_id: "schedule", scheduled_for_utc: "2026-09-29T00:00:00Z", execution_status: "done", status_run_id: "root", kind: "daily", daily_time: "17:00", timezone: "America/Los_Angeles", occurrence_number: 2, created_by: "user" };
  const schedule = { id: "schedule", conversation_id: "chat", content: "Edited after trigger", kind: "daily", daily_time: "17:00", timezone: "America/Los_Angeles", created_at: "2026-09-28T00:00:00Z" };
  const derive = (runValue = run, occurrenceValue = occurrence, scheduleValue = schedule) => scheduledRunMetadata(message, runValue, occurrenceValue, scheduleValue, "UTC");
  const exact = derive();
  assert.equal(exact.title, "定时任务", "legacy task text never becomes display metadata");
  assert.equal(derive(run, { ...occurrence, title: "Snapshot title", description: "Snapshot description" }, { ...schedule, title: "New title", description: "New description" }).description, "Snapshot description");
  assert.equal(derive(run, occurrence, { ...schedule, title: "New title" }).title, "定时任务", "legacy occurrences never borrow live metadata");
  assert.equal(exact.plannedTime, "00:00");
  assert.equal(exact.statusTime, "00:01");
  assert.match(exact.metadata, /每天 17:00（America\/Los_Angeles）/);
  assert.match(exact.metadata, /2026年9月28日创建/);
  assert.match(exact.metadata, /第 2 次 · 由你创建/);
  assert.match(derive(run, occurrence, { ...schedule, daily_time: "08:00", timezone: "UTC" }).metadata, /每天 17:00（America\/Los_Angeles）/, "schedule edits cannot rewrite the occurrence snapshot");
  const legacy = { ...occurrence, kind: undefined, daily_time: undefined, timezone: undefined, occurrence_number: undefined, created_by: undefined };
  assert.equal(derive(run, legacy).metadata, "定时触发 · 2026年9月29日 · 2026年9月28日创建", "legacy occurrences cannot borrow the current schedule frequency");
  assert.match(exact.plannedTimeDescription, /UTC/);

  const delegated = derive(run, { ...occurrence, status_run_id: "delegate" });
  assert.equal(delegated.statusTime, undefined, "a root timestamp cannot date a delegated outcome");
  const activeDelegate = derive(run, { ...occurrence, status_run_id: "delegate", execution_status: "running" });
  assert.equal(activeDelegate.runningSince, undefined, "a completed root cannot clock an active delegate");
  const inconsistent = derive({ ...run, status: "running" });
  assert.equal(inconsistent.statusTime, undefined, "stale root state must not date a terminal family outcome");
  assert.equal(derive({ ...run, id: "other" }).statusTime, undefined);
  assert.equal(derive(run, { ...occurrence, root_run_id: "other" }).plannedAt, undefined);
  assert.equal(derive(run, legacy, { ...schedule, id: "other" }).metadata, "定时触发 · 2026年9月29日");
  assert.equal(derive(run, occurrence, { ...schedule, conversation_id: "other" }).metadata, "每天 17:00（America/Los_Angeles） · 第 2 次 · 由你创建");

  const missing = scheduledRunMetadata(message, undefined, undefined, schedule, "UTC");
  assert.equal(missing.plannedAt, undefined, "message creation is not a planned trigger time");
  assert.equal(missing.plannedTime, "待确认");
  assert.equal(missing.statusAt, undefined);
  assert.equal(missing.metadata, "定时任务");
  assert.equal(derive({ ...run, updated_at: "not-a-date" }).statusTime, undefined);
  assert.equal(derive(run, { ...occurrence, scheduled_for_utc: "not-a-date" }).plannedAt, undefined);
  assert.equal(derive({ ...run, status: "running" }, { ...occurrence, execution_status: "running" }).runningSince, run.updated_at);
  assert.doesNotThrow(() => scheduledRunMetadata(message, run, occurrence, schedule, "Invalid/Timezone"));
  assert.match(derive(run, { ...occurrence, kind: "interval", interval_seconds: 7200 }).metadata, /^每 2 小时 · 第 2 次/);
  await ui.setLanguage("en");
  assert.equal(derive(run, occurrence, { ...schedule, conversation_id: "other" }).metadata, "Daily at 17:00 (America/Los_Angeles) · Run #2 · Created by you");
  assert.match(derive(run, { ...occurrence, kind: "interval", interval_seconds: 3600 }).metadata, /^Every hour · /);
  await ui.setLanguage("zh-CN");
  console.log("scheduled run metadata: PASS (exact occurrence, preserved task text, timezone, missing data, delegated/stale timestamp provenance)");
} finally {
  await ui.close();
}
