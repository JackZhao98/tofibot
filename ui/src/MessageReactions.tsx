import { lazy, Suspense, useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import type { Message, Reaction } from "./types";
import { TofiIcon } from "./icons";
import { useTranslation } from "./i18n";
import "./message-reactions.css";
const EmojiPicker = lazy(() => import("./ReactionEmojiPicker"));
const choices = ["👍", "❤️", "😂", "🎉", "👀", "🙌", "🤔", "🐱"];
type MenuPosition = { x: number; y: number } | null;

export function MessageReactions({ message, botNames, onSet, menuPosition, onCloseMenu }: { message: Message; botNames: Map<string, string>; onSet: (emoji: string, present: boolean) => Promise<void>; menuPosition: MenuPosition; onCloseMenu: () => void }) {
  const { t } = useTranslation("chat");
  const [pending, setPending] = useState(false);
  const pendingRef = useRef(false);
  const [showPicker, setShowPicker] = useState(false);
  const [error, setError] = useState("");
  const [position, setPosition] = useState({ left: 12, top: 12 });
  const menu = useRef<HTMLDivElement>(null);
  const closeRef = useRef(onCloseMenu);
  closeRef.current = onCloseMenu;

  useEffect(() => {
    if (!menuPosition) return;
    setError(""); setShowPicker(false);
    const previousFocus = document.activeElement as HTMLElement | null;
    menu.current?.querySelector<HTMLButtonElement>("button")?.focus();
    const closeOutside = (event: PointerEvent) => { if (!menu.current?.contains(event.target as Node)) closeRef.current(); };
    const escape = (event: KeyboardEvent) => { if (event.key === "Escape") { event.preventDefault(); closeRef.current(); } };
    // Category/emoji scrolling inside the picker must not dismiss it.
    const scroll = (event: Event) => { if (!event.composedPath().includes(menu.current as EventTarget)) closeRef.current(); };
    document.addEventListener("pointerdown", closeOutside);
    document.addEventListener("keydown", escape, true);
    window.addEventListener("scroll", scroll, true);
    return () => {
      document.removeEventListener("pointerdown", closeOutside); document.removeEventListener("keydown", escape, true); window.removeEventListener("scroll", scroll, true);
      if (document.activeElement === document.body || menu.current?.contains(document.activeElement)) {
        if (previousFocus?.isConnected) previousFocus.focus({ preventScroll: true });
      }
    };
  }, [menuPosition]);

  useLayoutEffect(() => {
    if (!menuPosition || !menu.current) return;
    const place = () => {
      const rect = menu.current!.getBoundingClientRect();
      setPosition({ left: Math.max(12, Math.min(menuPosition.x, window.innerWidth - rect.width - 12)), top: Math.max(12, Math.min(menuPosition.y, window.innerHeight - rect.height - 12)) });
    };
    place();
    const observer = new ResizeObserver(place); observer.observe(menu.current);
    window.addEventListener("resize", place);
    return () => { observer.disconnect(); window.removeEventListener("resize", place); };
  }, [menuPosition]);

  const grouped = new Map<string, Reaction[]>();
  for (const reaction of message.reactions ?? []) grouped.set(reaction.emoji, [...(grouped.get(reaction.emoji) ?? []), reaction]);
  const mine = (emoji: string) => (grouped.get(emoji) ?? []).some(actor => actor.actor_type === "user");
  const toggle = async (emoji: string, present: boolean) => {
    if (pendingRef.current) return;
    pendingRef.current = true; setPending(true); setError("");
    try { await onSet(emoji, present); closeRef.current(); }
    catch (cause) { setError(cause instanceof Error ? cause.message : t("reactions.failed")); }
    finally { pendingRef.current = false; setPending(false); }
  };

  return <>
    {grouped.size > 0 && <div className={`message-reactions${message.role === "user" ? " is-user" : ""}`}>
      {[...grouped].map(([emoji, actors]) => {
        const names = actors.map(actor => actor.actor_type === "user" ? t("message.you") : botNames.get(actor.actor_id ?? "") ?? t("reactions.assistant")).join(t("list.separator"));
        return <button key={emoji} type="button" className={`reaction-chip${mine(emoji) ? " mine" : ""}`} title={names} aria-label={mine(emoji) ? t("reactions.chip_mine", { emoji, count: actors.length }) : t("reactions.chip_join", { emoji, count: actors.length })} aria-pressed={mine(emoji)} disabled={pending} onClick={() => void toggle(emoji, !mine(emoji))}><span>{emoji}</span><span className="reaction-count">{actors.length}</span></button>;
      })}
    </div>}
    {!menuPosition && error && <span className="reaction-error" role="alert">{error}</span>}
    {menuPosition && createPortal(<div ref={menu} className={`reaction-context-menu${showPicker ? " has-emoji-picker" : ""}`} role="dialog" aria-label={t("reactions.menu")} style={position} aria-busy={pending}>
      {!showPicker ? <div className="reaction-options" role="group" aria-label={t("reactions.common")}>{choices.map(emoji => <button key={emoji} type="button" aria-label={t("reactions.react_with", { emoji })} aria-pressed={mine(emoji)} disabled={pending} onClick={() => void toggle(emoji, !mine(emoji))}>{emoji}</button>)}<button type="button" aria-label={t("reactions.more")} title={t("reactions.more")} disabled={pending} onClick={() => setShowPicker(true)}><TofiIcon name="plus" size={18} /></button></div> : <div inert={pending}><Suspense fallback={<p className="reaction-picker-loading" role="status">{t("reactions.loading")}</p>}><EmojiPicker onSelect={emoji => void toggle(emoji, !mine(emoji))} /></Suspense></div>}
      {error && <p role="alert">{error}</p>}
    </div>, document.body)}
  </>;
}
