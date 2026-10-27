// Dialogs, menus, hints, popovers and list boxes. A dialog that takes props is rendered on its own (overlay state); the
// menus and popovers that live inside the workspace are opened in the real App (app state).
import { openSettings } from "../drive";
import { useState, type ReactNode } from "react";
import { DeleteConversationDialog, type DeleteTarget } from "../../DeleteConversationDialog";
import { ReassignPrimaryDialog } from "../../ReassignPrimaryDialog";
import { AdminSheet, CopyButton, SecretField } from "../../AdminSheets";
import { TakeoverDialog } from "../../TakeoverDialog";
import { Onboarding } from "../../Onboarding";
import { BotIdentityCard } from "../../BotIdentityCard";
import { BotAvatar } from "../../BotAvatar";
import { BOTS, bot } from "../world";
import type { CatalogSection } from "../types";
import type { AppOptions } from "../world";

const DM: AppOptions = { path: "/b/bot_mochi" };
const GROUP: AppOptions = { path: "/g/grp_launch" };
/** The list view: on a phone the sidebar is the whole screen; on a wide screen it is the same as a chat. */
const LIST: AppOptions = { path: "/" };
const noop = () => {};
const never = () => new Promise<void>(() => {});
const ROW = (id: string) => `[data-sidebar-flip="${id}"]`;

/** Keeps a closed dialog closed, with one way back: the catalog's own reopen button (the keyboard check uses it). */
function Reopen({ label, children }: { label: string; children: (close: () => void) => ReactNode }) {
  const [open, setOpen] = useState(true);
  return open ? <>{children(() => setOpen(false))}</> : <button type="button" className="secondary-button catalog-reopen" onClick={() => setOpen(true)}>{label}</button>;
}
const REOPEN = { focus: ".catalog-reopen", key: "Enter" };

const deleteBot: DeleteTarget = { id: "bot_miso", conversationId: "dm_miso", name: "Miso", kind: "bot" };
const deleteGroup: DeleteTarget = { id: "grp_launch", conversationId: "grp_launch", name: "Launch crew", kind: "group" };
const deletePrimary: DeleteTarget = { id: "bot_mochi", conversationId: "dm_mochi", name: "Mochi", kind: "bot", isPrimary: true };
const confirmDelete = ".delete-dialog-submit";

export const dialogs: CatalogSection = {
  id: "dialogs", number: "8", title: "Dialogs",
  states: [
    { id: "delete-bot", kind: "overlay", label: "Delete a Bot", probe: ".delete-dialog", keyboard: { close: "Escape", reopen: REOPEN },
      render: () => <Reopen label="Delete Miso">{close => <DeleteConversationDialog target={deleteBot} bots={BOTS} onDelete={async () => {}} onClose={close} />}</Reopen> },
    { id: "delete-group", kind: "overlay", label: "Delete a group", probe: ".delete-dialog",
      render: () => <DeleteConversationDialog target={deleteGroup} bots={BOTS} onDelete={async () => {}} onClose={noop} /> },
    { id: "delete-busy", kind: "overlay", label: "Delete, working", note: "buttons disabled; Escape and the scrim do nothing", probe: ".delete-dialog",
      drive: step => step.click(confirmDelete).then(() => undefined),
      render: () => <DeleteConversationDialog target={deleteBot} bots={BOTS} onDelete={never} onClose={noop} /> },
    { id: "delete-error", kind: "overlay", label: "Delete, failed", probe: ".delete-dialog",
      drive: async step => { await step.click(confirmDelete); await step.find(".delete-dialog .error-text"); },
      render: () => <DeleteConversationDialog target={deleteBot} bots={BOTS} onDelete={() => Promise.reject(new Error("Synthetic: the server refused"))} onClose={noop} /> },
    { id: "delete-primary", kind: "overlay", label: "Delete the Primary Assistant", note: "a successor must be named", probe: ".delete-dialog",
      render: () => <DeleteConversationDialog target={deletePrimary} bots={BOTS} onDelete={async () => {}} onClose={noop} /> },
    { id: "reassign", kind: "overlay", label: "Hide the Primary Assistant: pick a successor", probe: ".delete-dialog", keyboard: { close: "Escape", reopen: REOPEN },
      render: () => <Reopen label="Hide Mochi">{close => <ReassignPrimaryDialog bot={bot("bot_mochi")} bots={BOTS} onConfirm={async () => {}} onClose={close} />}</Reopen> },
    { id: "admin-sheet", kind: "overlay", label: "Admin sheet", probe: ".admin-sheet", keyboard: { close: "Escape", reopen: REOPEN },
      render: () => <Reopen label="Open the sheet">{close => <AdminSheet title="Reset password for synthetic-user" onClose={close}>
        <SecretField label="Temporary password" value="synthetic-temporary-password" note="Shown once. Copy it now." />
        <div className="sheet-actions"><CopyButton value="synthetic-temporary-password" /><button type="button" className="primary-button" onClick={close}>Done</button></div>
      </AdminSheet>}</Reopen> },
    { id: "admin-sheet-locked", kind: "overlay", label: "Admin sheet, locked", note: "work in flight: Escape and outside clicks do nothing", probe: ".admin-sheet",
      render: () => <AdminSheet title="Creating synthetic-user…" locked onClose={noop}><p>Working…</p></AdminSheet> },
    { id: "takeover", kind: "overlay", label: "Take over the screen", note: "no Escape: keys go to the remote computer", probe: ".handover-dialog",
      render: () => <TakeoverDialog botId="bot_mochi" questionId="q_takeover" site="https://login.example.test" prompt="Enter the verification code for the synthetic account." busy={false} error="" onResult={noop} onClose={noop} /> },
    { id: "takeover-error", kind: "overlay", label: "Take over the screen, failed", probe: ".handover-dialog",
      render: () => <TakeoverDialog botId="bot_mochi" questionId="q_takeover" site="https://login.example.test" prompt="Enter the verification code for the synthetic account." busy={false} error="Synthetic: the answer was not saved" onResult={noop} onClose={noop} /> },
    { id: "view-only-card", kind: "app", label: "Related chat, card (wide web)", note: "opened from \"From Miso\" in Mochi's chat", app: DM, widths: ["desktop"], probe: ".view-only-chat", keyboard: { close: "Escape" },
      drive: async step => { await step.click(".related-message-link"); await step.find(".view-only-chat"); await step.wait(300); } },
    { id: "view-only-modal", kind: "app", label: "Related chat, modal (narrow)", app: DM, widths: ["phone"], probe: ".view-only-chat", keyboard: { close: "Escape" },
      drive: async step => { await step.click(".related-message-link"); await step.find(".view-only-chat"); await step.wait(300); } },
    ...([1, 2, 3] as const).map(initialStep => ({ id: `onboarding-${initialStep}`, kind: "overlay" as const, label: `First-run setup, step ${initialStep}`, probe: ".onb-sheet",
      render: () => <Onboarding initialStep={initialStep} modelConfigured={initialStep === 3} bot={initialStep === 3 ? bot("bot_mochi") : undefined} onProgress={noop} onModelChanged={async () => {}} onBot={noop} onSkip={noop} onFinish={noop} /> })),
    { id: "settings-modal", kind: "app", label: "Settings window", note: "the detail pane as a modal; see also section 17", app: DM, probe: ".settings-shell", keyboard: { close: "Escape" },
      drive: async step => { await openSettings(step); } },
    { id: "panel-modal", kind: "app", label: "Bot settings cover (narrow)", note: "on a phone the cover is the whole screen", app: DM, widths: ["phone"], probe: ".cover-window",
      drive: async step => { await step.click(".chat-identity"); await step.wait(500); } },
  ],
  gaps: [
    "Big computer window and full-screen sheet: section 20.",
    "VM sign-in (MCP OAuth in the computer): needs a live OAuth session id from the server.",
    "Terminal close / interrupt confirms: need an open terminal session.",
    "Permission coach: desktop client only (window.tofiDesktop).",
    "Image lightbox and file preview: the image is served from /api, which the catalog never calls.",
    "Settings leave-without-saving and computer restart confirms (Radix since step 3): open Settings in a frame, change a field, then close.",
  ],
};

export const menus: CatalogSection = {
  id: "menus", number: "9", title: "Menus and context menus",
  states: [
    { id: "sidebar", kind: "app", label: "Sidebar row menu", note: "right-click, long-press, Menu key or Shift+F10", app: LIST, probe: ".sidebar-context-menu",
      keyboard: { close: "Escape", reopen: { focus: ROW("dm_miso"), key: "F10", shift: true } },
      drive: async step => { await step.contextMenu(ROW("dm_miso")); await step.find(".sidebar-context-menu"); } },
    { id: "attach", kind: "app", label: "Attachment menu", app: DM, probe: ".composer-add-menu", keyboard: { close: "Escape" },
      drive: async step => { await step.click(".composer-add-wrap > button"); await step.find(".composer-add-menu"); } },
    { id: "account", kind: "app", label: "Account menu", app: LIST, probe: ".sidebar-account-menu", keyboard: { close: "Escape" },
      drive: async step => { await step.click(".sidebar-account-identity"); await step.find(".sidebar-account-menu"); } },
    { id: "reactions", kind: "app", label: "Reaction menu", app: DM, probe: ".reaction-context-menu", keyboard: { close: "Escape" },
      drive: async step => {
        const host = await step.find('.message-block[data-message-id="m_dm_mochi_2"] .message-hover-host');
        await step.hover(host);
        await step.click('.message-block[data-message-id="m_dm_mochi_2"] [data-action="react"]');
        await step.find(".reaction-context-menu");
      } },
    { id: "emoji-picker", kind: "app", label: "Full emoji picker", note: "lazy loaded from the reaction menu", app: DM, probe: ".reaction-context-menu",
      drive: async step => {
        await step.hover(await step.find('.message-block[data-message-id="m_dm_mochi_2"] .message-hover-host'));
        await step.click('.message-block[data-message-id="m_dm_mochi_2"] [data-action="react"]');
        const menu = await step.find(".reaction-context-menu");
        const buttons = menu.querySelectorAll<HTMLButtonElement>(".reaction-options button");
        buttons[buttons.length - 1]?.click();
        await step.find(".reaction-context-menu.has-emoji-picker");
        await step.wait(600);
      } },
    { id: "create", kind: "app", label: "New menu (Bot, group, import)", app: LIST, probe: ".create-menu", keyboard: { close: "Escape" },
      drive: async step => { await step.click(".create-menu-anchor > button"); await step.wait(250); } },
    { id: "more", kind: "app", label: "Header \"More\" menu", app: DM, probe: ".more-menu", keyboard: { close: "Escape" },
      drive: async step => { await step.click(".header-more > button"); await step.wait(250); } },
  ],
  gaps: ["MCP server row \"More\" (in Settings › Connections): needs synthetic MCP servers."],
};

export const hints: CatalogSection = {
  id: "hints", number: "10", title: "Hover hints",
  states: [
    { id: "action-hint", kind: "app", label: "Icon button hint (ActionHints)", note: "keyboard focus shows it after 150 ms, hover after 500 ms", app: LIST, probe: ".action-hint",
      drive: async step => { await step.focus(".sidebar-account-settings"); await step.find(".action-hint"); } },
    { id: "hint-near-top", kind: "app", label: "Icon button hint near the top edge", note: "flips below the button", app: DM, probe: ".action-hint",
      drive: async step => { await step.focus(".header-more > button"); await step.find(".action-hint"); } },
    { id: "read-names", kind: "app", label: "Read-by names (group)", note: "section 3; the receipt mark's data-hint in the quiet tone", app: GROUP, probe: ".action-hint",
      drive: async step => { await step.hover(await step.find('.message-block[data-message-id="m_grp_launch_7"] .message-hover-host')); await step.focus(".send-receipt-mark.has-tip"); await step.find(".action-hint.is-quiet"); } },
  ],
};

export const popovers: CatalogSection = {
  id: "popovers", number: "11", title: "Popovers",
  states: [
    { id: "identity", kind: "overlay", label: "Bot identity card", note: "keyboard focus on the avatar; hover after 400 ms", probe: ".bot-identity-card", keyboard: { close: "Escape" },
      drive: async step => { await step.focus(".identity-card-anchor"); await step.find(".bot-identity-card"); await step.wait(250); },
      render: () => <div style={{ padding: 40 }}><BotIdentityCard bot={bot("bot_miso")} onOpen={noop} onMention={noop}><BotAvatar id="bot_miso" mini /></BotIdentityCard></div> },
    { id: "identity-in-chat", kind: "app", label: "Bot identity card in a group", app: GROUP, probe: ".bot-identity-card",
      drive: async step => { await step.wait(800); await step.focus(".sender-avatar .identity-card-anchor"); await step.find(".bot-identity-card"); await step.wait(250); } },
    { id: "widget-computer", kind: "app", label: "Widget: computer (≥1100 px)", note: "the header computer button opens the operable desktop in the widget column inside the chat (docs/design/widgets-and-sidebar.md)", app: DM, widths: ["desktop"], probe: ".context-card",
      drive: async step => { await step.click("[data-computer-toggle]"); await step.find(".context-card[data-card='computer']"); await step.wait(1200); } },
    { id: "widget-artifacts", kind: "app", label: "Widget: Artifacts (≥1100 px)", note: "the latest files of this conversation, one column; All opens the full view as a cover", app: DM, widths: ["desktop"], probe: ".context-card",
      drive: async step => { await step.click("button[aria-label='Open artifacts']"); await step.find(".context-card[data-card='artifacts'] .artifact-row"); await step.wait(700); } },
    { id: "widgets-stacked", kind: "app", label: "Widgets: computer and Artifacts stacked", note: "fixed order, computer first, whatever was opened first", app: DM, widths: ["desktop"], probe: ".context-card",
      drive: async step => { await step.click("button[aria-label='Open artifacts']"); await step.find(".context-card[data-card='artifacts'] .artifact-row"); await step.click("[data-computer-toggle]"); await step.find(".context-card[data-card='computer']"); await step.wait(1200); } },
    { id: "widget-folded", kind: "app", label: "Widgets: one folded", note: "a folded widget shows only its header; the content stays mounted", app: DM, widths: ["desktop"], probe: ".context-card",
      drive: async step => { await step.click("[data-computer-toggle]"); await step.find(".context-card[data-card='computer']"); await step.click("button[aria-label='Open artifacts']"); await step.find(".context-card[data-card='artifacts'] .artifact-row"); await step.click(".context-card[data-card='computer'] .context-card-fold"); await step.wait(700); } },
    { id: "sidebar-tasks", kind: "app", label: "Sidebar: Tasks (≥1100 px)", note: "a real grid track at the window edge that pushes the chat; the widgets shrink with it", app: DM, widths: ["desktop"], probe: ".sidebar-pane",
      drive: async step => { await step.click("button[aria-label='Tasks']"); await step.find(".sidebar-pane .sidebar-shell"); await step.wait(800); } },
    { id: "sidebar-expanded", kind: "app", label: "Sidebar: expanded", note: "clamp(480px, 50vw, 760px); the chat keeps at least 480px", app: DM, widths: ["desktop"], probe: ".sidebar-pane",
      drive: async step => { await step.click("button[aria-label='Tasks']"); await step.find(".sidebar-pane .sidebar-shell"); await step.click(".sidebar-shell-expand"); await step.wait(800); } },
    { id: "cover-bot-settings", kind: "app", label: "Cover: Bot settings", note: "an exclusive full-window panel on a ModalDialog; the widgets and the sidebar are hidden while it is open", app: DM, probe: ".cover-window",
      drive: async step => { await step.click(".chat-identity"); await step.find(".cover-window"); await step.wait(700); } },
    { id: "cover-artifacts", kind: "app", label: "Cover: Artifacts", note: "the widget's All button; the whole browser with scope and type filters", app: DM, widths: ["desktop"], probe: ".cover-window",
      drive: async step => { await step.click("button[aria-label='Open artifacts']"); await step.click(".artifacts-widget-all"); await step.find(".cover-window .artifact-card"); await step.wait(700); } },
  ],
  gaps: ["Full emoji picker: section 9.", "Related chat in the sidebar: section 8.", "Sidebar: Terminal, Memory, Archive and the Members / Group settings views: need their own synthetic reads."],
};

export const listboxes: CatalogSection = {
  id: "listboxes", number: "12", title: "List boxes",
  states: [
    { id: "mention", kind: "app", label: "@ mention menu", app: GROUP, probe: ".mention-menu", keyboard: { close: "Escape" },
      drive: async step => { await step.type(".composer textarea", "@"); await step.find(".mention-menu"); } },
    { id: "mention-filtered", kind: "app", label: "@ mention menu, filtered", app: GROUP, probe: ".mention-menu",
      drive: async step => { await step.type(".composer textarea", "@mi"); await step.find(".mention-menu"); } },
    { id: "select", kind: "app", label: "Native select (Settings)", note: "the browser draws the open list", app: DM,
      drive: async step => { await step.click(".sidebar-account-settings"); await step.find(".settings-shell select"); }, force: [{ selector: ".settings-shell select", state: "focus" }] },
  ],
};
