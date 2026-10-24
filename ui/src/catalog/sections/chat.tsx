// Conversation surfaces. They exist only inside the workspace, so every state is the real App on the synthetic server
// (world.ts), driven to the state with real events.
import type { Drive } from "../drive";
import type { CatalogSection } from "../types";
import type { AppOptions } from "../world";
import { processStates } from "./process";

const DM: AppOptions = { path: "/b/bot_mochi" };
const GROUP: AppOptions = { path: "/g/grp_launch" };
const block = (id: string) => `.message-block[data-message-id="${id}"]`;
const ANSWER = block("m_dm_mochi_2");
const LAST_USER = ".message-list .message-block:last-of-type";

export async function send(step: Drive, text: string) {
  await step.type(".composer textarea", text);
  await step.click(".composer .send-button");
}
async function hoverMessage(step: Drive, selector: string) {
  await step.hover(await step.find(`${selector} .message-hover-host`));
  await step.find(`${selector} .message-hover-actions`);
}

export const bubbles: CatalogSection = {
  id: "bubbles", number: "1", title: "Message bubble (MessageBubble / MessageRow)",
  states: [
    { id: "dm", kind: "app", label: "Direct chat", note: "user and Bot messages, Markdown with a table and code, a reaction, a related-chat link, a resume notice", app: DM },
    { id: "group", kind: "app", label: "Group chat", note: "avatars and names, consecutive messages from one sender merged, a handoff line with its brief", app: GROUP },
    { id: "streaming", kind: "app", label: "Streaming", note: "a reply still being written: live text and cursor, the cat working, the stop button", app: { ...DM, running: true } },
    { id: "sending", kind: "app", label: "Sending", note: "the server has not answered: the bubble is dimmed", app: { ...DM, send: "hang" }, drive: step => send(step, "Is the room booked?") },
    { id: "failed", kind: "app", label: "Failed to send", note: "\"Not sent · Retry\" under the bubble", app: { ...DM, send: "fail" }, drive: async step => { await send(step, "Is the room booked?"); await step.find(".send-receipt-retry"); } },
    { id: "brief", kind: "app", label: "Handoff brief open", app: GROUP, drive: async step => { await step.click(".assignment-brief summary"); } },
    // The process line: live and finished, collapsed and opened, task block and web tool row (process.tsx).
    ...processStates,
  ],
  gaps: ["Jump target (a 1.7 s flash): click a reply quote in a frame to see it.", "Display cards (ui_card) under an answer: no synthetic card yet.", "Desktop-client tool rows (DesktopToolActivityRun): Electron only."],
};

export const actions: CatalogSection = {
  id: "actions", number: "2", title: "Message actions row (MessageHoverActions)",
  states: [
    { id: "hover", kind: "app", label: "Shown on hover", note: "copy, react, reply, time; no frame, fill or shadow", app: DM, drive: step => hoverMessage(step, ANSWER) },
    { id: "hover-button", kind: "app", label: "Hover on a button", app: DM, drive: step => hoverMessage(step, ANSWER), force: [{ selector: `${ANSWER} .message-hover-actions [data-action="react"]`, state: "hover" }] },
    { id: "keyboard", kind: "app", label: "Keyboard focus", note: "the list is one tab stop; Tab enters the bar", app: DM,
      drive: async step => { await step.focus(LAST_USER); await step.focus(await step.find(`${LAST_USER} .message-hover-actions button`)); } },
    { id: "copied", kind: "app", label: "Copied", note: "the icon turns into a tick for 1.2 s", app: DM,
      drive: async step => { await hoverMessage(step, ANSWER); await step.click(`${ANSWER} [data-action="copy"]`); await step.find(`${ANSWER} [data-action="copy"][aria-label$="ied"], ${ANSWER} [data-action="copy"]`); } },
  ],
  gaps: ["Tap to open on a touch screen (the row pushes the next message down): needs DevTools device mode, where (hover: none) applies."],
};

export const receipts: CatalogSection = {
  id: "receipts", number: "3", title: "Send receipts (SendReceipt / SendReceiptMark)",
  states: [
    { id: "delivered", kind: "app", label: "Delivered", note: "one tick at the end of the action row; the run is still queued", app: { ...DM, queued: true }, drive: step => hoverMessage(step, LAST_USER) },
    { id: "read", kind: "app", label: "Read", note: "two ticks", app: DM, drive: step => hoverMessage(step, LAST_USER) },
    { id: "read-group", kind: "app", label: "Read in a group, names on hover", app: GROUP,
      drive: step => hoverMessage(step, block("m_grp_launch_7")), force: [{ selector: `${block("m_grp_launch_7")} .send-receipt-mark.has-tip`, state: "hover" }] },
    { id: "sending", kind: "app", label: "Sending", note: "no mark; the bubble is dimmed and a screen reader hears \"Sending\"", app: { ...DM, send: "hang" }, drive: step => send(step, "Is the room booked?") },
    { id: "failed", kind: "app", label: "Failed, retry", app: { ...DM, send: "fail" }, drive: async step => { await send(step, "Is the room booked?"); await step.find(".send-receipt-retry"); },
      force: [{ selector: ".send-receipt-retry", state: "focus" }] },
  ],
};

export const composer: CatalogSection = {
  id: "composer", number: "4", title: "Composer",
  states: [
    { id: "empty", kind: "app", label: "Empty", note: "the send button is quiet until there is something to send", app: DM },
    { id: "text", kind: "app", label: "With text", app: DM, drive: async step => { await step.type(".composer textarea", "Book the small room for Thursday at 15:00, please."); } },
    { id: "running", kind: "app", label: "Bot working", note: "the stop button", app: { ...DM, running: true } },
    { id: "stop-menu", kind: "app", label: "Stop menu", note: "Stop current step / Stop all; focus returns to the Stop button", app: { ...DM, running: true }, probe: ".composer-stop-menu", keyboard: { close: "Escape" },
      drive: async step => { await step.click(".composer-stop"); await step.find(".composer-stop-menu"); } },
    { id: "queued-send", kind: "app", label: "Sent while the Bot works", note: "the composer stays open; the message waits: \"Queued, read at next step\"", app: { ...DM, running: true, send: "queued" },
      drive: async step => { await send(step, "Also check the projector."); await step.find(".send-receipt-note"); } },
    { id: "no-model", kind: "app", label: "No model connected", note: "the banner above the composer links to Settings", app: { ...DM, modelConfigured: false } },
    { id: "reply", kind: "app", label: "Replying to a message", app: DM, drive: async step => { await hoverMessage(step, ANSWER); await step.click(`${ANSWER} [data-action="reply"]`); await step.find(".composer-reply"); } },
    { id: "mention", kind: "app", label: "@ mention menu", note: "the one listbox (section 12)", app: GROUP, probe: ".mention-menu", keyboard: { close: "Escape" },
      drive: async step => { await step.type(".composer textarea", "@"); await step.find(".mention-menu"); } },
    { id: "attach", kind: "app", label: "Attachment menu", note: "section 9", app: DM, probe: ".composer-add-menu", keyboard: { close: "Escape" },
      drive: async step => { await step.click(".composer-add-wrap > button"); await step.find(".composer-add-menu"); } },
    { id: "focus-send", kind: "app", label: "Send button, keyboard focus", app: DM,
      drive: async step => { await step.type(".composer textarea", "Ready to send"); }, force: [{ selector: ".composer .send-button", state: "focus" }] },
  ],
  gaps: ["Read-only (archived conversation): the archived Bot is not reachable from the synthetic sidebar yet.", "Dictation (recording, transcribing): needs a microphone.", "Uploading attachment ring: needs a file upload in flight; drop a file on a frame to try it."],
};

export const perch: CatalogSection = {
  id: "perch", number: "5", title: "Cat on the composer (ComposerPerch)",
  states: [
    { id: "idle", kind: "app", label: "Idle", app: DM },
    { id: "work", kind: "app", label: "Working", app: { ...DM, running: true } },
    { id: "group", kind: "app", label: "Group chat: no cat", app: GROUP },
  ],
  gaps: ["\"look\" after sending and \"happy\" when a reply lands are moments: send a message in a frame to see them."],
};

export const edges: CatalogSection = {
  id: "edges", number: "22", title: "Edge blur, header glass and the \"new messages\" pill",
  states: [
    { id: "blur", kind: "app", label: "Bottom blur and header glass", note: "messages scroll under the header and fade into the blur above the composer", app: { ...DM, longHistory: true } },
    { id: "pill", kind: "app", label: "Jump to latest", app: { ...DM, longHistory: true }, probe: ".jump-latest",
      drive: async step => { await step.scrollTo(".message-scroll", 0); await step.find(".jump-latest"); } },
    { id: "pill-count", kind: "app", label: "Jump to latest, with new messages", app: { ...DM, longHistory: true }, probe: ".jump-latest",
      drive: async step => {
        await step.scrollTo(".message-scroll", 0);
        await step.find(".jump-latest");
        step.emit("message", { conversation_id: "dm_mochi", message: { id: "m_dm_mochi_new", conversation_id: "dm_mochi", seq: 99, role: "assistant", content: "The review is at 15:00.", created_at: new Date().toISOString(), sender_bot_id: "bot_mochi", sender_bot_name: "Mochi" } });
        await step.find(".jump-new-count");
      } },
  ],
  gaps: ["Reduced transparency fallback: macOS Settings › Accessibility › Display › Reduce transparency."],
};
