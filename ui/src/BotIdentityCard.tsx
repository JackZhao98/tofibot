import { TofiIcon } from "./icons";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { useSurfacePresence } from "./InteractionSystem";
import { GazeAvatar } from "./GazeAvatar";
import type { Bot } from "./types";

/** A short dwell opens identity; moving into the card never closes it mid-flight. */
export function BotIdentityCard({ bot, children, onOpen, onMention }: { bot: Bot; children: ReactNode; onOpen?: () => void; onMention?: () => void }) {
  const anchor = useRef<HTMLSpanElement>(null);
  const timer = useRef<number | undefined>(undefined);
  const holdTimer = useRef<number | undefined>(undefined);
  const press = useRef<{ x: number; y: number } | null>(null);
  const held = useRef(false);
  const cancelHold = () => { clearTimeout(holdTimer.current); press.current = null; };
  const [position, setPosition] = useState<{ left: number; top: number } | null>(null);
  const displayedPosition = useSurfacePresence(position, 180);
  const cancel = () => clearTimeout(timer.current);
  const close = () => { cancel(); setPosition(null); };
  const open = () => { cancel(); timer.current = window.setTimeout(() => {
    if (!anchor.current) return;
    const rect = anchor.current.getBoundingClientRect();
    setPosition({ left: Math.max(12, Math.min(innerWidth - 292, rect.left)), top: Math.max(12, Math.min(innerHeight - 240, rect.bottom + 8)) });
  }, 400); };
  const leave = () => { cancel(); timer.current = window.setTimeout(() => setPosition(null), 180); };
  useEffect(() => () => { cancel(); cancelHold(); }, []);
  useEffect(() => {
    if (!position) return;
    const escape = (event: KeyboardEvent) => { if (event.key === "Escape") close(); };
    window.addEventListener("scroll", close, true); window.addEventListener("resize", close); window.addEventListener("keydown", escape);
    return () => { cancel(); window.removeEventListener("scroll", close, true); window.removeEventListener("resize", close); window.removeEventListener("keydown", escape); };
  }, [position]);
  return <><span className="identity-card-anchor" ref={anchor} onPointerEnter={(event) => { if (event.pointerType === "mouse") open(); }} onPointerDown={event => {
    if (!onMention || event.button !== 0) return;
    cancel(); cancelHold(); held.current = false;
    press.current = { x: event.clientX, y: event.clientY };
    holdTimer.current = window.setTimeout(() => {
      held.current = true; cancelHold(); close(); onMention();
    }, 500);
  }} onPointerMove={event => {
    if (press.current && Math.hypot(event.clientX - press.current.x, event.clientY - press.current.y) > 8) cancelHold();
  }} onPointerUp={() => { cancelHold(); if (!held.current) open(); }} onPointerCancel={cancelHold}
  onContextMenu={event => { if (held.current || press.current) event.preventDefault(); }}
  onClick={event => { if (held.current) { event.preventDefault(); held.current = false; } }}
  onPointerLeave={() => { cancelHold(); leave(); }} onFocus={() => { if (!press.current) open(); }} onBlur={leave} tabIndex={0} aria-label={`${bot.name} 的资料`}>{children}</span>{displayedPosition && createPortal(<section className="bot-identity-card" data-open={Boolean(position)} inert={!position || undefined} onPointerEnter={cancel} onPointerLeave={leave} onFocus={cancel} onBlur={leave} style={displayedPosition} aria-label={`${bot.name} 的资料`}><div className="identity-card-title"><GazeAvatar id={bot.id} /><div><strong>{bot.name}</strong></div></div>{bot.instructions?.trim() && <p>{bot.instructions.trim().slice(0, 140)}{bot.instructions.trim().length > 140 ? "…" : ""}</p>}{onMention && <button onClick={() => { close(); onMention(); }}><TofiIcon name="mention" size={16} style={{ verticalAlign: "middle" }} /> 提及 {bot.name}</button>}{onOpen && <button onClick={() => { close(); onOpen(); }}>打开私信 <TofiIcon name="chat" size={16} style={{ verticalAlign: "middle" }} /></button>}</section>, document.body)}</>;
}
