import { useEffect, useLayoutEffect, useRef, useState } from "react";
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
  useEffect(() => {
    const element = overlay.current;
    if (!element) return;
    const update = () => setBounds({ width:element.clientWidth, height:element.clientHeight });
    const observer = new ResizeObserver(update);
    observer.observe(element); update();
    return () => observer.disconnect();
  }, []);
  const pointer = visibleDesktopPointer(presence, humanControlled, Date.now(), Infinity);
  const owner = presence.ownership?.owner;
  return <span className="desktop-pointer-overlay" ref={overlay} aria-hidden="true">
    <WebPointer key={`${owner?.bot_id}:${owner?.run_id}:${humanControlled}`} pointer={pointer} name={botLabel} {...bounds} />
  </span>;
}
