// The schedule editor sends only the timing that changed, with the baseline it was opened
// against, and the create form builds its fields from the same draft (src/ScheduleTimingFields.tsx).
import assert from "node:assert/strict";
import { openUiModules } from "./ui-modules.mjs";

const ui = await openUiModules({ language: "en" });
try {
  const { localInputValue, timingDraftOf, timingEditPatch, timingCreateFields, defaultTimingDraft } = await ui.load("/src/ScheduleTimingFields.tsx");
  const base = { id: "s1", conversation_id: "c", bot_id: "b", content: "x", status: "active", created_at: "", updated_at: "" };
  const daily = { ...base, kind: "daily", timezone: "America/Los_Angeles", daily_time: "02:30", next_at_utc: "2026-10-23T09:30:00Z" };
  const weekly = { ...base, kind: "weekly", timezone: "UTC", daily_time: "08:00", weekdays: ["mon", "wed"], next_at_utc: "2026-10-26T08:00:00Z" };
  const once = { ...base, kind: "once", timezone: "Asia/Tokyo", next_at_utc: "2026-11-01T00:30:00Z" };

  assert.equal(localInputValue("2026-11-01T00:30:00Z", "Asia/Tokyo"), "2026-11-01T09:30", "the picker shows the wall clock of the schedule zone");
  assert.equal(localInputValue("", "UTC"), "");

  const open = timingDraftOf(daily);
  assert.equal(timingEditPatch(daily, open, { ...open }), undefined, "an untouched draft sends no timing");

  let patch = timingEditPatch(daily, open, { ...open, dailyTime: "06:00" });
  assert.deepEqual(Object.keys(patch).sort(), ["daily_time", "expected_timing"], "a daily time change sends the time and its baseline only");
  assert.equal(patch.daily_time, "06:00");
  assert.equal(patch.expected_timing.daily_time, "02:30");
  assert.equal(patch.expected_timing.timezone, "America/Los_Angeles");
  assert.equal(patch.expected_timing.next_at_utc, undefined, "a recurring cursor is not a baseline");

  const w = timingDraftOf(weekly);
  patch = timingEditPatch(weekly, w, { ...w, weekdays: ["mon", "wed", "fri"] });
  assert.deepEqual(patch.weekdays, ["mon", "wed", "fri"]);
  assert.equal(patch.daily_time, undefined);

  patch = timingEditPatch(weekly, w, { ...w, kind: "daily" });
  assert.equal(patch.kind, "daily");
  assert.equal(patch.daily_time, "08:00", "a kind change names the fields the new kind needs");
  assert.equal(patch.weekdays, undefined);

  const o = timingDraftOf(once);
  patch = timingEditPatch(once, o, { ...o, runAt: "2026-11-02T10:00" });
  assert.equal(patch.run_at, "2026-11-02T10:00");
  assert.equal(patch.expected_timing.next_at_utc, once.next_at_utc, "a one-time run is guarded by its instant");
  patch = timingEditPatch(once, o, { ...o, zone: "UTC" });
  assert.equal(patch.timezone, "UTC");
  assert.equal(patch.run_at, o.runAt, "a zone change on a one-time run keeps its wall clock");

  patch = timingEditPatch(daily, open, { ...open, kind: "interval", interval: "30", runAt: "" });
  assert.equal(patch.interval_seconds, 1800);
  assert.equal(patch.run_at, undefined, "an interval with no first run starts one interval from now on the server");

  assert.deepEqual(timingCreateFields({ ...defaultTimingDraft, kind: "weekly", weekdays: ["fri", "mon"], dailyTime: "07:15" }, "UTC"), { kind: "weekly", timezone: "UTC", daily_time: "07:15", weekdays: ["mon", "fri"] });
  assert.deepEqual(timingCreateFields({ ...defaultTimingDraft, kind: "interval", runAt: "2026-11-01T00:00", interval: "90" }, "UTC"), { kind: "interval", timezone: "UTC", run_at: "2026-11-01T00:00", interval_seconds: 5400 });

  console.log("schedule timing: PASS");
} finally { await ui.close(); }
