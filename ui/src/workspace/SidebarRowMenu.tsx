import type { TFunction } from "i18next";
import { TofiIcon as Icon } from "../icons";
import { OverlayMenu } from "../overlays/OverlayHost";
import type { Bot, Conversation } from "../types";

/** The New menu rides in the same chunk, so one fetch serves both sidebar menus. */
export { default as CreateMenu } from "./CreateMenu";

export type RowMenuTarget = { conversation: Conversation; x: number; y: number };
export type RowMenuActions = {
  close: () => void;
  togglePinned: (c: Conversation) => void;
  sections: Record<string, string>;
  setSection: (id: string, name: string) => void;
  markUnread: (id: string, seq: number) => Promise<void>;
  fail: (cause: unknown) => void;
  openBotSettings: (c: Conversation) => void;
  makePrimary: (id: string) => Promise<void> | void;
  copyLink: (c: Conversation) => Promise<void> | void;
  archive: (c: Conversation, archived: boolean) => Promise<void>;
  confirmDelete: (c: Conversation) => void;
};

/** The right-click menu of a sidebar row. A lazy chunk, preloaded in idle time: the entry bundle has no room for its twelve items. */
export default function SidebarRowMenu({ menu, t, bot, a }: { menu: RowMenuTarget; t: TFunction<readonly ["chat", "common"], undefined>; bot: Bot | undefined; a: RowMenuActions }) {
  const c = menu.conversation;
  return <OverlayMenu open onOpenChange={open => { if (!open) a.close(); }} className="sidebar-context-menu" label={c.name} point={menu} key={`${c.id}:${menu.x}:${menu.y}`}>
    <button onClick={() => a.togglePinned(c)}><Icon name="pin" size={17} />{c.pinned ? t("sidebar.unpin") : t("sidebar.pin")}</button>
    <button onClick={() => { const name = window.prompt(t("sidebar.move_to_section"), a.sections[c.id] ?? ""); if (name !== null) a.setSection(c.id, name.trim()); a.close(); }}><Icon name="plus" size={17} />{t("sidebar.move_to_section")}</button>
    {!c.archived && <button onClick={() => { const seq = Math.max(0, (c.last_message?.seq ?? 1) - 1); void a.markUnread(c.id, seq).catch(a.fail); a.close(); }}><Icon name="bell" size={17} />{t("sidebar.mark_unread")}</button>}
    <div className="tb-menu-sep" role="separator" />
    {c.kind === "dm" && <button onClick={() => a.openBotSettings(c)}><Icon name="edit" size={17} />{t("sidebar.rename_bot")}</button>}
    {c.kind === "dm" && !c.archived && bot && !bot.is_primary && <button onClick={() => { a.close(); void a.makePrimary(bot.id); }}><Icon name="star" size={17} />{t("primary.make")}</button>}
    <button onClick={() => { void a.copyLink(c); a.close(); }}><Icon name="copy" size={17} />{t("sidebar.copy_link")}</button>
    <button onClick={() => { void navigator.clipboard?.writeText(c.id); a.close(); }}><Icon name="copy" size={17} />{t("sidebar.copy_id")}</button>
    <button onClick={() => void a.archive(c, !c.archived)}><Icon name="eye-off" size={17} />{c.archived ? t("sidebar.show") : t("sidebar.hide")}</button>
    <button className="delete-menu-action is-danger" onClick={() => { a.confirmDelete(c); a.close(); }}><Icon name="trash" size={17} />{t("sidebar.delete")}</button>
  </OverlayMenu>;
}
