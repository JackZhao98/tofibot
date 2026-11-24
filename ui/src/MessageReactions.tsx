import { useEffect, useRef, useState } from "react";
import type { Message, Reaction } from "./types";
import { useTranslation } from "./i18n";
import "./message-reactions.css";
import { OverlayPopover } from "./overlays/OverlayHost";
import { Button } from "./ds/Button/Button";
import { ReactionPicker } from "./ds/ReactionPicker/ReactionPicker";
type MenuPosition = { x: number; y: number; h?: number } | null;

export function MessageReactions({ message, botNames, onSet, menuPosition, onCloseMenu, onActivity, activityOpen = false }: { /** V3 4.3: 工作过程 in the menu (desktop has no hover bar). */ onActivity?: () => void; activityOpen?: boolean; message: Message; botNames: Map<string, string>; onSet: (emoji: string, present: boolean) => Promise<void>; menuPosition: MenuPosition; onCloseMenu: () => void }) {
  const { t } = useTranslation("chat");
  // Only repeated toggles of the same emoji wait for each other; the menu and every other emoji stay live.
  const [pendingEmoji, setPendingEmoji] = useState<ReadonlySet<string>>(new Set());
  const pendingRef = useRef(new Set<string>());
  const [error, setError] = useState("");
  const anchor = useRef<{ getBoundingClientRect(): DOMRect } | null>(null);
  const closeRef = useRef(onCloseMenu);
  closeRef.current = onCloseMenu;
  // The menu sits under the point (or the button's box, so a flip lands above it rather than over it).
  if (menuPosition) anchor.current = { getBoundingClientRect: () => new DOMRect(menuPosition.x, menuPosition.y, 0, menuPosition.h ?? 0) };
  useEffect(() => { if (menuPosition) { setError(""); } }, [menuPosition]);

  const grouped = new Map<string, Reaction[]>();
  for (const reaction of message.reactions ?? []) grouped.set(reaction.emoji, [...(grouped.get(reaction.emoji) ?? []), reaction]);
  const mine = (emoji: string) => (grouped.get(emoji) ?? []).some(actor => actor.actor_type === "user");
  const toggle = async (emoji: string, present: boolean) => {
    if (pendingRef.current.has(emoji)) return;
    pendingRef.current.add(emoji); setPendingEmoji(new Set(pendingRef.current)); setError("");
    // The caller applies the change locally before the request starts, so the menu closes at once; a failure rolls it back and shows the error beside the chips.
    const request = onSet(emoji, present);
    closeRef.current();
    try { await request; }
    catch (cause) { setError(cause instanceof Error ? cause.message : t("reactions.failed")); }
    finally { pendingRef.current.delete(emoji); setPendingEmoji(new Set(pendingRef.current)); }
  };

  return <>
    {grouped.size > 0 && <div className={`message-reactions${message.role === "user" ? " is-user" : ""}`}>
      {[...grouped].map(([emoji, actors]) => {
        const names = actors.map(actor => actor.actor_type === "user" ? t("message.you") : botNames.get(actor.actor_id ?? "") ?? t("reactions.assistant")).join(t("list.separator"));
        return <button key={emoji} type="button" className={`reaction-chip${mine(emoji) ? " mine" : ""}`} title={names} data-pending={pendingEmoji.has(emoji) || undefined} aria-label={mine(emoji) ? t("reactions.chip_mine", { emoji, count: actors.length }) : t("reactions.chip_join", { emoji, count: actors.length })} aria-pressed={mine(emoji)} disabled={pendingEmoji.has(emoji)} onClick={() => void toggle(emoji, !mine(emoji))}><span>{emoji}</span><span className="reaction-count">{actors.length}</span></button>;
      })}
    </div>}
    {!menuPosition && error && <span className="reaction-error" role="alert">{error}</span>}
    {menuPosition && <OverlayPopover open onOpenChange={open => { if (!open) closeRef.current(); }} anchor={anchor} closeOnScroll className="reaction-context-menu" aria-label={t("reactions.menu")} aria-busy={pendingEmoji.size > 0}>
      <ReactionPicker bare label={t("reactions.common")} selected={[...grouped.keys()].filter(mine)} disabled={[...pendingEmoji]} onPick={emoji => void toggle(emoji, !mine(emoji))} />
      {onActivity && <Button variant="ghost" size="sm" className="reaction-menu-activity" onClick={() => { closeRef.current(); onActivity(); }}>{t(activityOpen ? "hover.hide_activity" : "hover.view_activity")}</Button>}
      {error && <p role="alert">{error}</p>}
    </OverlayPopover>}
  </>;
}
