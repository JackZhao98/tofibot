import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { isDesktop } from "./desktop";
import { BotAvatar } from "./BotAvatar";
import { DesktopPointerGlyph } from "./DesktopPointerGlyph";
import { desktopPointerPosition, visibleDesktopPointer, type DesktopPresenceState, type DesktopPointer } from "./desktopPresence";

function WebPointer({ pointer, width, height, name }: { pointer: DesktopPointer | null; width: number; height: number; name: (id: string) => string }) {
  const [previous, setPrevious] = useState<DesktopPointer | null>(null);
  useLayoutEffect(() => { if (pointer) setPrevious(pointer); }, [pointer]);
  const retained = pointer ?? previous;
  const position = retained && desktopPointerPosition(retained, width, height);
  if (!retained || !position) return null;
  // Keep the node and its last transform through gaps between confirmed points.
  // Ownership keys below prevent travel from another Bot's or run's old position.
  return <>
    <span className={`desktop-pointer-confirmed web-desktop-pointer${pointer ? " is-visible" : ""}`}
      style={{ transform:`translate3d(${position.left}px,${position.top}px,0)` }}>
      <DesktopPointerGlyph botId={retained.bot_id} />
      <span className="web-desktop-pointer-name">{name(retained.bot_id)}</span>
    </span>
    {pointer && <span key={pointer.updated_at} className="web-desktop-click" style={position}><span className="desktop-click-ring" /></span>}
  </>;
}

export function DesktopPointerMarker({ presence, humanControlled, botLabel }: { presence: DesktopPresenceState; humanControlled: boolean; botLabel: (id: string) => string }) {
  const overlay = useRef<HTMLSpanElement>(null);
  const [bounds, setBounds] = useState({ width:0, height:0 });
  const [timestamp, setTimestamp] = useState(Date.now());
  useEffect(() => {
    const element = overlay.current;
    if (!element) return;
    const update = () => setBounds({ width:element.clientWidth, height:element.clientHeight });
    const observer = new ResizeObserver(update);
    observer.observe(element); update();
    return () => observer.disconnect();
  }, []);
  useEffect(() => {
    if (!isDesktop) return;
    setTimestamp(Date.now());
    const timer = window.setInterval(() => setTimestamp(Date.now()), 500);
    return () => window.clearInterval(timer);
  }, [presence.ownership?.pointer?.updated_at]);
  const pointer = visibleDesktopPointer(presence, humanControlled, isDesktop ? timestamp : Date.now(), isDesktop ? 5000 : Infinity);
  const position = pointer && desktopPointerPosition(pointer, bounds.width, bounds.height);
  const owner = presence.ownership?.owner;
  return <span className="desktop-pointer-overlay" ref={overlay} aria-hidden="true">
    {isDesktop ? pointer && position && <span className={`desktop-pointer-confirmed${position.left > bounds.width / 2 ? " is-right" : ""}${position.top > bounds.height - 36 ? " is-bottom" : ""}`} style={position}>
      <span key={pointer.updated_at} className="desktop-click-ring" />
      <span className="desktop-pointer-label"><BotAvatar id={pointer.bot_id} mini animated={false}/><span>{botLabel(pointer.bot_id)} · 点击</span></span>
    </span> : <WebPointer key={`${owner?.bot_id}:${owner?.run_id}:${humanControlled}`} pointer={pointer} name={botLabel} {...bounds} />}
  </span>;
}
