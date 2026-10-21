// The workspace frame: sidebar rows and pins, notices, the boot screen, the computer window, the phone push and the
// smaller patterns. Mostly the real App; the cards that take props are rendered on their own.
import { ScheduledTaskRow } from "../../ScheduledTaskRow";
import { MailDraftCard } from "../../MailDraftCard";
import { TaskIssueCard } from "../../TaskIssueCard";
import { presentTaskIssue } from "../../taskIssuePresentation";
import { CATS, EmptyCat, EmptyState } from "../../EmptyCat";
import { at, bot, mailDraft } from "../world";
import type { Message, Run } from "../../types";
import type { CatalogSection, CatalogState } from "../types";
import type { AppOptions } from "../world";

const DM: AppOptions = { path: "/b/bot_mochi" };
const GROUP: AppOptions = { path: "/g/grp_launch" };
const LIST: AppOptions = { path: "/" };
const ROW = (id: string) => `[data-sidebar-flip="${id}"]`;
const PIN = ".pin-dock button";

export const rows: CatalogSection = {
  id: "rows", number: "6", title: "Sidebar conversation rows",
  states: [
    { id: "list", kind: "app", label: "Rows", note: "group (working), unread count, a question waiting (?), hidden Bots folded at the end", app: LIST },
    { id: "active", kind: "app", label: "Current row", app: GROUP, widths: ["desktop"] },
    { id: "hover", kind: "app", label: "Hover", app: LIST, force: [{ selector: ROW("dm_miso"), state: "hover" }] },
    { id: "focus", kind: "app", label: "Keyboard focus", app: LIST, drive: async step => { await step.focus(ROW("dm_miso")); } },
    { id: "pressed", kind: "app", label: "Pressed", app: LIST, force: [{ selector: ROW("dm_nori"), state: "active" }] },
    { id: "hidden", kind: "app", label: "Hidden Bots open", app: LIST, drive: async step => { await step.click(".hidden-bots-toggle"); await step.wait(200); } },
    { id: "search", kind: "app", label: "Search with no match", app: LIST, drive: async step => { await step.type(".search-box input", "zzz"); await step.wait(200); } },
    { id: "collapsed", kind: "app", label: "Sidebar collapsed", note: "avatars only; names move into hints", app: LIST, widths: ["desktop"],
      drive: async step => { await step.click(".sidebar-toggle"); await step.wait(400); } },
  ],
  gaps: ["Dragging, settling and the drop announcement: drag a row in a frame (or Alt+↑/↓ on a focused row)."],
};

export const pins: CatalogSection = {
  id: "pins", number: "7", title: "Pinned tiles (PinTile / PinDock / PinDropZone)",
  states: [
    { id: "current", kind: "app", label: "Pinned tile, current", app: DM, widths: ["desktop"] },
    { id: "idle", kind: "app", label: "Pinned tile, not current", app: GROUP, widths: ["desktop"] },
    { id: "list", kind: "app", label: "Pinned tiles in the list (phone)", app: LIST, widths: ["phone"] },
    { id: "focus", kind: "app", label: "Pinned tile, keyboard focus", app: LIST, drive: async step => { await step.focus(PIN); } },
    { id: "collapsed", kind: "app", label: "Sidebar collapsed: one column, no names", app: LIST, widths: ["desktop"], drive: async step => { await step.click(".sidebar-toggle"); await step.wait(400); } },
  ],
  gaps: ["Drop zone over the search box: it appears only while a row is dragged."],
};

export const notices: CatalogSection = {
  id: "notices", number: "18", title: "Notices, toasts and banners",
  states: [
    { id: "toast", kind: "app", label: "Workspace refresh failed (toast)", app: { ...DM, refreshFails: true }, probe: ".workspace-toast",
      drive: async step => { window.dispatchEvent(new Event("tofi:portability-imported")); await step.find(".workspace-toast", 8000); } },
    { id: "computer-status", kind: "app", label: "Shared computer status strip", app: { ...DM, computer: "starting" }, drive: async step => { await step.find(".computer-preparing"); } },
    { id: "update", kind: "app", label: "New version banner", note: "admins only; dismissal is remembered per version", app: { ...DM, update: true }, drive: async step => { await step.find(".update-banner", 8000); } },
    { id: "no-model", kind: "app", label: "\"Connect a model\" above the composer", app: { ...DM, modelConfigured: false } },
  ],
  gaps: ["Settings banners: section 17.", "Panel error bar and the in-chat system notice: need a failing panel load or a failed run."],
};

export const boot: CatalogSection = {
  id: "boot", number: "19", title: "Boot screen (BootScreen / BootDissolve)",
  states: [
    { id: "loading", kind: "app", label: "Loading", note: "after 300 ms: the blurred skeleton and the breathing logo, no spinner", app: { ...DM, boot: "hang" }, probe: ".boot",
      drive: async step => { await step.find(".boot", 4000); await step.wait(400); } },
    { id: "loading-list", kind: "app", label: "Loading, list view", note: "the skeleton follows the route", app: { ...LIST, boot: "hang" }, probe: ".boot",
      drive: async step => { await step.find(".boot", 4000); await step.wait(400); } },
    { id: "failed", kind: "app", label: "Could not connect", app: { ...DM, boot: "fail" }, probe: ".boot",
      drive: async step => { await step.find(".boot button", 8000); } },
  ],
  gaps: ["The dissolve into the workspace (280 ms) and the warm hand-over between two boot screens: reload a chat frame."],
};

// The small window docks above the composer, measured when it opens. Opened in the first few hundred milliseconds after
// the workspace renders it measured the composer mid-layout and docked off screen (seen 2026-10-20), so wait first.
const openComputer = async (step: Parameters<NonNullable<CatalogState["drive"]>>[0]) => { await step.wait(1000); await step.click("[data-computer-toggle]"); await step.find(".desktop-floating-shell"); await step.wait(500); };
export const computer: CatalogSection = {
  id: "computer", number: "20", title: "Bot computer (BotDesktopPanel / FloatingDesktop)",
  states: [
    { id: "small", kind: "app", label: "Small window", app: DM, widths: ["desktop"], probe: ".desktop-floating-shell", drive: openComputer },
    { id: "expanded", kind: "app", label: "Big window", note: "Escape steps back to the small window", app: DM, widths: ["desktop"], probe: ".desktop-floating-shell.is-expanded", keyboard: { close: "Escape" },
      drive: async step => { await openComputer(step); await step.click(".desktop-control-expand"); await step.wait(500); } },
    { id: "sheet", kind: "app", label: "Full-screen sheet (phone)", app: DM, widths: ["phone"], probe: ".desktop-floating-shell", keyboard: { close: "Escape" }, drive: openComputer },
    { id: "starting", kind: "app", label: "Starting", app: { ...DM, computer: "starting" }, widths: ["desktop"], probe: ".desktop-floating-shell", drive: openComputer },
  ],
  gaps: ["Hibernating, connection lost and your-control states: need the live desktop stream."],
};

export const push: CatalogSection = {
  id: "push", number: "21", title: "Phone push (list ↔ chat)",
  states: [
    { id: "list", kind: "app", label: "List", note: "tap a row in the frame to watch the push in", app: LIST, widths: ["phone"] },
    { id: "chat", kind: "app", label: "Chat", note: "tap the back arrow to watch it go back", app: DM, widths: ["phone"] },
    { id: "back-pressed", kind: "app", label: "Back button pressed", app: DM, widths: ["phone"], force: [{ selector: ".back-button", state: "active" }] },
  ],
  gaps: ["Swipe from the left edge: not built yet (C38; experiment in migration step 4, D33)."],
};

const scheduled = (status: Run["status"], error?: string): { message: Message; run: Run } => ({
  message: { id: `m_sched_${status}`, conversation_id: "dm_mochi", seq: 1, role: "user", kind: "scheduled_task", content: "Send me the weekly sign-up numbers", created_at: at(-60), run_id: `run_sched_${status}` },
  run: { id: `run_sched_${status}`, conversation_id: "dm_mochi", bot_id: "bot_mochi", status, kind: "schedule", created_at: at(-60), updated_at: at(-58), ...(error ? { error } : {}) },
});
const scheduledState = (status: Run["status"], label: string, error?: string): CatalogState => {
  const { message, run } = scheduled(status, error);
  return { id: `scheduled-${status}`, kind: "inline", label, context: ["catalog-column"], render: () => <ScheduledTaskRow message={message} run={run} timezone="UTC" onRetry={async () => {}} /> };
};
const failedRun: Run = { id: "run_failed", conversation_id: "dm_mochi", bot_id: "bot_mochi", status: "failed", created_at: at(-20), updated_at: at(-19), failure: { code: "model_quota_exhausted", source: "runtime", message: "Synthetic: quota" } };

export const other: CatalogSection = {
  id: "other", number: "23", title: "Other patterns",
  states: [
    { id: "file-drop", kind: "app", label: "Dropping a file on the window", app: DM, probe: ".web-drop-overlay.is-dragging .web-drop-zone",
      drive: async step => { const data = new DataTransfer(); data.items.add(new File(["synthetic"], "synthetic.txt", { type: "text/plain" })); document.body.dispatchEvent(new DragEvent("dragenter", { bubbles: true, cancelable: true, dataTransfer: data })); await step.find(".web-drop-overlay.is-dragging", 3000); await step.wait(300); } },
    scheduledState("queued", "Scheduled task, queued"),
    scheduledState("running", "Scheduled task, running"),
    scheduledState("done", "Scheduled task, done"),
    scheduledState("failed", "Scheduled task, failed", "Synthetic: the provider timed out"),
    { id: "task-issue", kind: "inline", label: "Task issue card", context: ["catalog-column"], render: () => { const issue = presentTaskIssue({ run: failedRun, recordsComplete: true }); return issue ? <TaskIssueCard issue={issue} onAction={() => {}} onRetry={() => {}} /> : <p>no issue</p>; } },
    ...(["pending", "sent", "declined"] as const).map(status => ({ id: `mail-${status}`, kind: "inline" as const, label: `Mail draft, ${status}`, context: ["catalog-column"],
      render: () => <MailDraftCard draft={mailDraft(status)} bot={bot("bot_mochi")} group={false} onChanged={async () => {}} /> })),
    { id: "empty-cats", kind: "inline", label: "Empty-state cats", render: () => <div style={{ display: "flex", gap: 24, flexWrap: "wrap", alignItems: "end" }}>
      {(["asleep", "awake", "curious"] as const).map(pose => <EmptyCat key={pose} look={CATS.mochi} pose={pose} name={`catalog-${pose}`} size={72} />)}</div> },
    { id: "empty-state", kind: "inline", label: "Empty state", render: () => <EmptyState name="catalog-empty" look={CATS.miso} pose="curious"><p>Nothing here yet.</p></EmptyState> },
    { id: "first-run", kind: "app", label: "First run: no Bots yet", app: { path: "/", empty: true } },
  ],
  gaps: ["Send flight and mail send flight (640 ms moments): send a message or a draft in a frame.", "Soft keyboard: needs a phone."],
};
