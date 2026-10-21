import { TofiIcon } from "./icons";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { useSurfacePresence } from "./InteractionSystem";
import { BotAvatar } from "./BotAvatar";
import type { Bot } from "./types";
import { useTranslation } from "./i18n";
import { OverlayPopover } from "./overlays/OverlayHost";

/** A short dwell opens identity; moving into the card never closes it mid-flight. The card is a Radix Popover that
 * leaves focus where it is; scrolling, resizing, Escape or a press elsewhere close it. */
export function BotIdentityCard({ bot, children, onOpen, onMention }: { bot: Bot; children: ReactNode; onOpen?: () => void; onMention?: () => void }) {
  const { t } = useTranslation("bots");
  const anchor = useRef<HTMLSpanElement>(null);
  const timer = useRef<number | undefined>(undefined);
  const holdTimer = useRef<number | undefined>(undefined);
  const press = useRef<{ x: number; y: number } | null>(null);
  const held = useRef(false);
  const cancelHold = () => { clearTimeout(holdTimer.current); press.current = null; };
  const [open, setOpen] = useState(false);
  const shown = useSurfacePresence(open || null, 180);
  const cancel = () => clearTimeout(timer.current);
  const close = () => { cancel(); setOpen(false); };
  const show = () => { cancel(); timer.current = window.setTimeout(() => setOpen(true), 400); };
  const leave = () => { cancel(); timer.current = window.setTimeout(() => setOpen(false), 180); };
  useEffect(() => () => { cancel(); cancelHold(); }, []);
  return <><span className="identity-card-anchor" ref={anchor} onPointerEnter={(event) => { if (event.pointerType === "mouse") show(); }} onPointerDown={event => {
    if (!onMention || event.button !== 0) return;
    cancel(); cancelHold(); held.current = false;
    press.current = { x: event.clientX, y: event.clientY };
    holdTimer.current = window.setTimeout(() => {
      held.current = true; cancelHold(); close(); onMention();
    }, 500);
  }} onPointerMove={event => {
    if (press.current && Math.hypot(event.clientX - press.current.x, event.clientY - press.current.y) > 8) cancelHold();
  }} onPointerUp={() => { cancelHold(); if (!held.current) show(); }} onPointerCancel={cancelHold}
  onContextMenu={event => { if (held.current || press.current) event.preventDefault(); }}
  onClick={event => { if (held.current) { event.preventDefault(); held.current = false; } }}
  onPointerLeave={() => { cancelHold(); leave(); }} onFocus={() => { if (!press.current) show(); }} onBlur={leave} tabIndex={0} aria-label={t("identity.profile_aria", { name: bot.name })}>{children}</span>{shown && <OverlayPopover open={open} onOpenChange={value => { if (!value) close(); }} anchor={anchor} takeFocus={false} closeOnScroll sideOffset={8} className="bot-identity-card" onPointerEnter={cancel} onPointerLeave={leave} onFocus={cancel} onBlur={leave} aria-label={t("identity.profile_aria", { name: bot.name })}><div className="identity-card-title"><BotAvatar id={bot.id} /><div><strong>{bot.name}</strong></div></div>{bot.instructions?.trim() && <p>{bot.instructions.trim().slice(0, 140)}{bot.instructions.trim().length > 140 ? "…" : ""}</p>}{onMention && <button onClick={() => { close(); onMention(); }}><TofiIcon name="mention" size={16} style={{ verticalAlign: "middle" }} /> {t("identity.mention", { name: bot.name })}</button>}{onOpen && <button onClick={() => { close(); onOpen(); }}>{t("identity.open_dm")} <TofiIcon name="chat" size={16} style={{ verticalAlign: "middle" }} /></button>}</OverlayPopover>}</>;
}
