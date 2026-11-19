import type { ReactNode } from "react";
import { TofiIcon as Icon } from "./icons";

/**
 * The pinned area is the drop target. This frame wraps the pinned group (or stands empty in its place when nothing is
 * pinned), directly under the search bar and above every row. It is plain at rest; while a row is dragged it gets the dashed
 * outline and the hint inside it, and lights up while the row hovers over it. It is hit-tested by the drag hook, never in the
 * tab order, and nothing is layered over it: the rows are below it in the flow, and the dragged row is the only thing above.
 */
export function PinDropZone({ label, hint, children }: { label: string; hint: string; children?: ReactNode }) {
  return <div className="pin-frame" data-dnd-dropzone data-empty={children ? undefined : ""}>
    {children}
    <div className="pin-frame-hint" aria-hidden="true"><Icon name="pin" size={22} /><strong>{label}</strong><small>{hint}</small></div>
  </div>;
}

/** The pinned rows under their eyebrow: the first block of the scroll area, absent while nothing is pinned. */
export function PinDock({ children, label }: { children: ReactNode; label: string }) {
  return children ? <div className="pin-dock" data-dnd-dock role="group" aria-label={label}><div className="tb-eyebrow sidebar-eyebrow" aria-hidden="true">{label}</div>{children}</div> : null;
}
