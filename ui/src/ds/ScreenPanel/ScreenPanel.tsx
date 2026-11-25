import type { ReactNode } from "react";
import { Icon } from "../Icon/Icon";
import { cx } from "../cx";
import "./ScreenPanel.css";

export type ScreenPanelProps = {
  /** large: the enlarged view and the picture-in-picture window. small: the widget column. Same 16:10 frame in both. */
  size?: "large" | "small";
  /** Who holds the mouse: the whole device takes that colour. None: dark grey at rest. */
  driver?: "bot" | "you" | null;
  /** The frame. */
  src?: string;
  alt?: string;
  /** Shown instead of a frame: the cat and one phase line while it boots, the sleeping cat when it is off. */
  placeholder?: ReactNode;
  /** Over the screen, centred (a sign-in notice). */
  overlay?: ReactNode;
  /** A glass label on the screen ("Your Mac"). */
  source?: string;
  /** The chin: a DriverSwitch, or the one action such as Turn on. */
  chin?: ReactNode;
  expanded?: boolean;
  expandLabel?: string;
  onExpand?: () => void;
  closeLabel?: string;
  onClose?: () => void;
  /** The accessible name of the device. */
  title?: string;
  className?: string;
};

/** The Bot computer (or the Mac Bot browser) as one device: a dark bezel holds the screen with no outline, a chin hangs under it
 *  with the driver switch, and the whole device takes the driver's colour. The switch is the status: no "Bot is working" line and no
 *  Take control button beside it. On the small device the corner controls and the source label show on hover (always on touch). */
export function ScreenPanel({ size = "large", driver = null, src, alt = "", placeholder, overlay, source, chin, expanded, expandLabel, onExpand, closeLabel, onClose, title, className }: ScreenPanelProps) {
  return <section className={cx("tb-device", `tb-device--${size}`, driver && `is-${driver}`, className)} aria-label={title}>
    <div className="tb-device-bezel">
      <div className="tb-device-screen">
        {src ? <img src={src} alt={alt} /> : <div className="tb-device-empty">{placeholder}</div>}
        {overlay ? <div className="tb-device-overlay">{overlay}</div> : null}
        {source ? <span className="tb-device-source">{source}</span> : null}
        {expandLabel || closeLabel ? <div className="tb-device-ctl">
          {expandLabel ? <button type="button" className="tb-device-btn" aria-label={expandLabel} title={expandLabel} onClick={onExpand}><span className={cx("tb-expand-glyph", expanded && "is-expanded")} aria-hidden="true" /></button> : null}
          {closeLabel ? <button type="button" className="tb-device-btn" aria-label={closeLabel} title={closeLabel} onClick={onClose}><Icon name="close" size={16} /></button> : null}
        </div> : null}
      </div>
    </div>
    {chin ? <div className="tb-device-chin">{chin}</div> : null}
  </section>;
}
