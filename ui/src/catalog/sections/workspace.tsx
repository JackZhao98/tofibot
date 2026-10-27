// The workspace frame: sidebar rows and pins, notices, the boot screen, the computer window, the phone push and the
// smaller patterns. Mostly the real App; the cards that take props are rendered on their own.
import { ScheduledTaskRow } from "../../ScheduledTaskRow";
import type { ScheduleOccurrence } from "../../scheduleOccurrences";
import { MailDraftCard } from "../../MailDraftCard";
import { TaskIssueCard } from "../../TaskIssueCard";
import { presentTaskIssue } from "../../taskIssuePresentation";
import { FileTile } from "../../FileTile";
import { fileKind } from "../../filePreviewModel";
import { CATS, EmptyCat, EmptyState } from "../../EmptyCat";
import GroupAvatar from "../../GroupAvatar";
import { catalog, normalizeConfig, paletteOptions, type AvatarConfig } from "../../lib/tofi-avatar/index.js";
import { at, bot, mailDraft } from "../world";
import type { Message, Run } from "../../types";
import type { CatalogSection, CatalogState } from "../types";
import type { AppOptions } from "../world";

const DM: AppOptions = { path: "/b/bot_mochi" };
const GROUP: AppOptions = { path: "/g/grp_launch" };
const LIST: AppOptions = { path: "/" };
const ROW = (id: string) => `[data-sidebar-flip="${id}"]`;
const PIN = ".pin-dock button";

/** A look for member `n`: shape and coat both vary with it, so a cluster never repeats a cat. */
const look = (n: number): AvatarConfig => {
  const shape = catalog.shapes[n % catalog.shapes.length].id;
  const pattern = catalog.patterns[(n * 3) % catalog.patterns.length].id;
  const palettes = paletteOptions(pattern);
  return normalizeConfig({ shape, pattern, palette: palettes[(n * 2) % palettes.length].id });
};
const cluster = (count: number, offset: number) => ({ ids: Array.from({ length: count }, (_, i) => `catalog_member_${offset + i}`), configs: Array.from({ length: count }, (_, i) => look(offset + i)) });
const Box = ({ size, ...props }: { size: number; ids: string[]; configs: AvatarConfig[] }) => <span style={{ display: "inline-block", width: size, height: size }}><GroupAvatar {...props} /></span>;
const groupAvatars = (id: string, label: string, offset: number): CatalogState => ({ id, kind: "inline", label,
  note: "1, 2, 3 and 4 members at the sidebar size (44 px) and the pin size (64 px); static, no autoplay",
  render: () => <div style={{ display: "grid", gap: 18 }}>{[44, 64].map(size => <div key={size} style={{ display: "flex", gap: 22, alignItems: "center" }}>
    {[1, 2, 3, 4].map(count => <Box key={count} size={size} {...cluster(count, offset + count * 4)} />)}</div>)}</div> });

export const rows: CatalogSection = {
  id: "rows", number: "6", title: "Sidebar conversation rows",
  states: [
    { id: "list", kind: "app", label: "Rows", note: "group (working), unread count, a question waiting (?)", app: LIST },
    { id: "indicators", kind: "app", label: "One status per row", note: "Miso: waiting (?) over 3 unread. Nori: failed (!) over 1 unread. Juno: 2 unread. Pip: working dot. Kit: finished task, nothing shown. The title stays bold while there are unread messages.", app: { path: "/", indicators: true } },
    { id: "indicators-collapsed", kind: "app", label: "One status per row, collapsed rail", app: { path: "/", indicators: true }, widths: ["desktop"], drive: async step => { await step.click(".sidebar-toggle"); await step.wait(400); } },
    { id: "rail-selected-dm", kind: "app", label: "Collapsed rail: selected Bot", note: "soft filled pill and the accent bar on the rail edge; no outline, no hard shadow", app: { ...DM, indicators: true }, widths: ["desktop"], drive: async step => { await step.click(".sidebar-toggle"); await step.wait(400); } },
    { id: "rail-selected-group", kind: "app", label: "Collapsed rail: selected group", note: "the heads apart in the pill, none overlapping", app: { ...GROUP, indicators: true }, widths: ["desktop"], drive: async step => { await step.click(".sidebar-toggle"); await step.wait(400); } },
    { id: "rail-unread", kind: "app", label: "Collapsed rail: unread count", note: "Juno: the count badge, top-right of the avatar", app: { path: "/", indicators: true }, widths: ["desktop"], drive: async step => { await step.click(".sidebar-toggle"); await step.wait(400); await step.focus(ROW("dm_juno")); } },
    { id: "rail-question", kind: "app", label: "Collapsed rail: question waiting", note: "Miso: the ? badge, same size and place as the count", app: { path: "/", indicators: true }, widths: ["desktop"], drive: async step => { await step.click(".sidebar-toggle"); await step.wait(400); await step.focus(ROW("dm_miso")); } },
    { id: "rail-hover", kind: "app", label: "Collapsed rail: hover tooltip", note: "lighter fill, the name in a black hint to the right of the rail", app: { path: "/", indicators: true }, widths: ["desktop"], drive: async step => { await step.click(".sidebar-toggle"); await step.wait(400); await step.hover(ROW("dm_nori")); await step.wait(800); } },
    groupAvatars("group-avatars", "Group avatars, set A", 0),
    groupAvatars("group-avatars-b", "Group avatars, set B (other shapes and coats)", 9),
    { id: "group-avatar-shapes", kind: "inline", label: "Every shape as one head", note: "each cat cropped to its face at 44 px and 64 px",
      render: () => <div style={{ display: "grid", gap: 18 }}>{[44, 64].map(size => <div key={size} style={{ display: "flex", gap: 14, alignItems: "center" }}>
        {catalog.shapes.map((shape, index) => { const config = normalizeConfig({ shape: shape.id, pattern: catalog.patterns[index % catalog.patterns.length].id, palette: paletteOptions(catalog.patterns[index % catalog.patterns.length].id)[0].id }); return <Box key={shape.id} size={size} ids={[`catalog_shape_${shape.id}`]} configs={[config]} />; })}</div>)}</div> },
    { id: "group-avatar-empty", kind: "inline", label: "Group with no Bot member", note: "keeps the people icon", render: () => <span style={{ display: "inline-grid", width: 44, height: 44, placeItems: "center" }}><GroupAvatar ids={[]} /></span> },
    { id: "active", kind: "app", label: "Current row", app: GROUP, widths: ["desktop"] },
    { id: "hover", kind: "app", label: "Hover", app: LIST, force: [{ selector: ROW("dm_miso"), state: "hover" }] },
    { id: "focus", kind: "app", label: "Keyboard focus", app: LIST, drive: async step => { await step.focus(ROW("dm_miso")); } },
    { id: "pressed", kind: "app", label: "Pressed", app: LIST, force: [{ selector: ROW("dm_nori"), state: "active" }] },
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
    { id: "proxy-challenge", kind: "app", label: "Site protection needs a re-check (Cloudflare challenge)", note: "after silent renewal failed; Verify again reloads the page", app: { ...DM, challenge: true }, probe: ".proxy-challenge-toast",
      drive: async step => { await step.find(".workspace", 8000); window.dispatchEvent(new Event("tofi:portability-imported")); await step.wait(300); window.dispatchEvent(new CustomEvent("tofi:proxy-challenge", { detail: true })); await step.find(".proxy-challenge-toast", 8000); } },
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
      drive: async step => { await openComputer(step); await step.click(".desktop-enlarge"); await step.wait(500); } },
    { id: "sheet", kind: "app", label: "Full-screen sheet (phone)", app: DM, widths: ["phone"], probe: ".desktop-floating-shell", keyboard: { close: "Escape" }, drive: openComputer },
    { id: "starting", kind: "app", label: "Starting", app: { ...DM, computer: "starting" }, widths: ["desktop"], probe: ".desktop-floating-shell", drive: openComputer },
    { id: "waking", kind: "app", label: "Waking the computer (hibernated)", note: "opening the window wakes it; no \"Paused\" caption", app: { ...DM, computer: "hibernated" }, widths: ["desktop"], probe: ".desktop-floating-shell", drive: openComputer },
    { id: "waking-restoring", kind: "app", label: "Waking the computer (restoring)", app: { ...DM, computer: "resuming" }, widths: ["desktop"], probe: ".desktop-floating-shell", drive: openComputer },
    { id: "desktop-starting", kind: "app", label: "Starting the desktop", note: "computer is up, desktop.start in flight; covers the stale frame", app: { ...DM, computer: "desktop-starting" }, widths: ["desktop"], probe: ".desktop-floating-shell", drive: openComputer },
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
const scheduledState = (status: Run["status"], label: string, error?: string, id = `scheduled-${status}`, result?: { posted: boolean }): CatalogState => {
  const { message, run } = scheduled(status, error);
  // A done occurrence carries the run's recorded conclusion; posted decides whether a chat message exists (D52).
  const occurrence: ScheduleOccurrence | undefined = result ? {
    root_run_id: run.id, schedule_id: "0f0f0f0f-0000-4000-8000-000000000001", scheduled_for_utc: at(-60), execution_status: "done", status_run_id: run.id,
    result_in_conversation: result.posted, result_posted: result.posted, completed_at: at(-58),
    result_text: "Nothing was restocked. Checked the two product pages.", result_sources: ["https://shop.example/item-a", "https://shop.example/item-b"],
  } : undefined;
  return { id, kind: "inline", label, context: ["catalog-column"], render: () => <ScheduledTaskRow message={message} run={run} occurrence={occurrence} timezone="UTC" onRetry={async () => {}} /> };
};
const failedRun: Run = { id: "run_failed", conversation_id: "dm_mochi", bot_id: "bot_mochi", status: "failed", created_at: at(-20), updated_at: at(-19), failure: { code: "model_quota_exhausted", source: "runtime", message: "Synthetic: quota" } };

export const other: CatalogSection = {
  id: "other", number: "23", title: "Other patterns",
  states: [
    { id: "file-drop", kind: "app", label: "Dragging a file over the window (composer lifts, Drop to add)", app: DM, probe: ".composer[data-file-drag] .composer-drop",
      drive: async step => { const data = new DataTransfer(); data.items.add(new File(["synthetic"], "synthetic.txt", { type: "text/plain" })); document.body.dispatchEvent(new DragEvent("dragenter", { bubbles: true, cancelable: true, dataTransfer: data })); await step.find(".composer[data-file-drag]", 3000); await step.wait(300); } },
    { id: "file-tiles", kind: "inline", label: "File type tiles, every family (40 px, 30 px, 56 px)", context: ["catalog-column"],
      render: () => <div style={{ display: "grid", gap: 14 }}>{[40, 30, 56].map(size => <div key={size} style={{ display: "flex", flexWrap: "wrap", gap: 12, alignItems: "end" }}>
        {([["report.pdf", ""], ["photo.png", "image/png"], ["model.xlsx", ""], ["data.csv", ""], ["brief.docx", ""], ["notes.txt", ""], ["README.md", ""], ["deck.pptx", ""], ["tool.py", ""], ["config.json", ""], ["bundle.zip", ""], ["take.mp3", ""], ["clip.mp4", ""], ["blob.bin", ""]] as const).map(([name, mime]) => <FileTile key={name} name={name} kind={fileKind(name, mime)} size={size} />)}</div>)}</div> },
    scheduledState("queued", "Scheduled task, queued"),
    scheduledState("running", "Scheduled task, running"),
    scheduledState("done", "Scheduled task, done"),
    scheduledState("done", "Scheduled task, done, result posted", undefined, "scheduled-done-posted", { posted: true }),
    scheduledState("done", "Scheduled task, done quietly (nothing posted)", undefined, "scheduled-done-quiet", { posted: false }),
    scheduledState("failed", "Scheduled task, failed", "Synthetic: the provider timed out"),
    { id: "task-issue", kind: "inline", label: "Task issue card", context: ["catalog-column"], render: () => { const issue = presentTaskIssue({ run: failedRun, recordsComplete: true }); return issue ? <TaskIssueCard issue={issue} onAction={() => {}} onRetry={() => {}} /> : <p>no issue</p>; } },
    { id: "mail-pending-demo", kind: "inline", label: "Mail draft, pending (demo)", context: ["catalog-column"],
      render: () => <MailDraftCard draft={mailDraft("pending", { demo: true })} bot={bot("bot_mochi")} group={false} onChanged={async () => {}} /> },
    ...(["pending", "sent", "declined"] as const).map(status => ({ id: `mail-${status}`, kind: "inline" as const, label: `Mail draft, ${status}`, context: ["catalog-column"],
      render: () => <MailDraftCard draft={mailDraft(status)} bot={bot("bot_mochi")} group={false} onChanged={async () => {}} /> })),
    { id: "empty-cats", kind: "inline", label: "Empty-state cats", render: () => <div style={{ display: "flex", gap: 24, flexWrap: "wrap", alignItems: "end" }}>
      {(["asleep", "awake", "curious"] as const).map(pose => <EmptyCat key={pose} look={CATS.mochi} pose={pose} name={`catalog-${pose}`} size={72} />)}</div> },
    { id: "empty-state", kind: "inline", label: "Empty state", render: () => <EmptyState name="catalog-empty" look={CATS.miso} pose="curious"><p>Nothing here yet.</p></EmptyState> },
    { id: "first-run", kind: "app", label: "First run: no Bots yet", app: { path: "/", empty: true } },
  ],
  gaps: ["Send flight and mail send flight (640 ms moments): send a message or a draft in a frame.", "Soft keyboard: needs a phone."],
};
