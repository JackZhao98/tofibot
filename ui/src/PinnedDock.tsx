import type { MouseEvent, ReactNode } from "react";
import { BotAvatar, type AvatarMotion } from "./BotAvatar";
import { TofiIcon as Icon } from "./icons";
import { LazyGroupAvatar as GroupAvatar } from "./LazyGroupAvatar";
import type { RowIndicatorKind } from "./rowIndicator";

export type PinTileModel = {
  id: string;
  name: string;
  /** Bot id whose cat is drawn; groups use the group mark instead. */
  avatarId: string;
  group: boolean;
  /** Member Bot ids of a group, in membership order; its avatar draws the first four heads. */
  members?: string[];
  motion: AvatarMotion;
  active: boolean;
  unread: number;
  /** The one corner status (see rowIndicator.ts). */
  indicator: RowIndicatorKind | null;
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
      {tile.group ? <span className="avatar group-avatar pin-tile-group" aria-hidden="true"><GroupAvatar ids={tile.members ?? []} iconSize={30} /></span> : <BotAvatar id={tile.avatarId} motion={tile.motion} />}
      {tile.indicator && <span className="pin-tile-badge" data-status={tile.indicator} aria-hidden="true">{tile.indicator === "unread" ? Math.min(tile.unread, 99) : null}</span>}
    </span>
    <span className="pin-tile-name">{tile.name}</span>
  </button>;
}

/**
 * The pinned area is the drop target. This frame wraps the whole dock of big tiles (or stands empty in its place when nothing
 * is pinned), directly under the search bar and above every row. It is plain at rest; while a row is dragged it gets the dashed
 * outline and the hint inside it, and lights up while the row hovers over it. It is hit-tested by the drag hook, never in the
 * tab order, and nothing is layered over it: the rows are below it in the flow, and the dragged row is the only thing above.
 */
export function PinDropZone({ label, hint, children }: { label: string; hint: string; children?: ReactNode }) {
  return <div className="pin-frame" data-dnd-dropzone data-empty={children ? undefined : ""}>
    {children}
    <div className="pin-frame-hint" aria-hidden="true"><Icon name="pin" size={22} /><strong>{label}</strong><small>{hint}</small></div>
  </div>;
}

/** The pinned tiles: the first block of the scroll area, absent while nothing is pinned. */
export function PinDock({ children, label }: { children: ReactNode; label: string }) {
  return children ? <div className="pin-dock" data-dnd-dock role="group" aria-label={label}>{children}</div> : null;
}
