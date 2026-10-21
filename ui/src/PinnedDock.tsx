import type { MouseEvent, ReactNode } from "react";
import { BotAvatar, type AvatarMotion } from "./BotAvatar";
import { TofiIcon as Icon } from "./icons";

export type PinTileModel = {
  id: string;
  name: string;
  /** Bot id whose cat is drawn; groups use the group mark instead. */
  avatarId: string;
  group: boolean;
  motion: AvatarMotion;
  active: boolean;
  unread: number;
  working: boolean;
  /** Spoken label: name plus unread and task state. */
  label: string;
  unreadLabel: string;
  workingLabel: string;
};

/** One pinned conversation: the Bot's cat at large size (the group mark for a group) over its short name. */
export function PinTile({ tile, onOpen, onMenu, onWarm }: {
  tile: PinTileModel;
  onOpen: () => void;
  onMenu: (event: MouseEvent) => void;
  onWarm: () => void;
}) {
  return <button type="button" className={`pin-tile${tile.active ? " active" : ""}`} data-dnd-id={tile.id} data-dnd-zone="dock" aria-keyshortcuts="Alt+ArrowLeft Alt+ArrowRight" data-hint={tile.name}
    aria-label={tile.label} aria-current={tile.active ? "true" : undefined}
    onClick={onOpen} onContextMenu={event => { event.preventDefault(); onMenu(event); }} onPointerDown={onWarm}>
    <span className="pin-tile-art">
      {tile.group ? <span className="avatar group-avatar pin-tile-group" aria-hidden="true"><Icon name="group" size={30} /></span> : <BotAvatar id={tile.avatarId} motion={tile.motion} />}
      {tile.unread > 0 ? <span className="pin-tile-badge" data-status="unread" aria-hidden="true">{Math.min(tile.unread, 99)}</span>
        : tile.working ? <span className="pin-tile-badge" data-status="working" aria-hidden="true" /> : null}
    </span>
    <span className="pin-tile-name">{tile.name}</span>
  </button>;
}

/**
 * Where a dragged row is dropped to pin it. It is the first block of the scroll area, in the flow directly under the search
 * bar, and shown only while a row is dragged: the rows below move down for it (the drag engine glides them) rather than being
 * covered. It is hit-tested by the drag hook, so it takes no pointer events and is never in the tab order.
 */
export function PinDropZone({ label, hint }: { label: string; hint: string }) {
  return <div className="pin-dropzone-anchor" data-dnd-dropzone aria-hidden="true">
    <div className="pin-dropzone"><Icon name="pin" size={24} /><strong>{label}</strong><small>{hint}</small></div>
  </div>;
}

/** The pinned tiles: the first block of the scroll area, absent while nothing is pinned. */
export function PinDock({ children, label }: { children: ReactNode; label: string }) {
  return children ? <div className="pin-dock" data-dnd-dock role="group" aria-label={label}>{children}</div> : null;
}
