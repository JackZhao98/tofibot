import { Children, type ComponentPropsWithoutRef, type ReactNode } from "react";
import { Icon } from "../Icon/Icon";
import { cx } from "../cx";
import "./ScreenPanel.css";

export type ScreenPanelProps = {
  /** large: the enlarged view and the picture-in-picture window. small: the widget column. Same 16:10 frame in both.
   *  strip: the folded widget, a 56px row with a thumbnail of the screen and the chin's text beside it. */
  size?: "large" | "small" | "strip";
  /** Who holds the mouse: the whole device takes that colour. None: dark grey at rest. */
  driver?: "bot" | "you" | null;
  /** The frame. */
  src?: string;
  alt?: string;
  /** Live content on the screen (the desktop video and its pointer marks, a browser stream). It fills the 16:10 frame and sits under the overlay and the corner controls. */
  children?: ReactNode;
  /** The power control, first in the corner group: a button with the `tb-device-btn` class. aria-pressed lights it; `is-changing` makes it breathe. */
  power?: ReactNode;
  /** Extra class and attributes on the screen element (a data hook, a click that enlarges). */
  screenClassName?: string;
  screenProps?: ComponentPropsWithoutRef<"div"> & Record<`data-${string}`, string | undefined>;
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
export function ScreenPanel({ size = "large", driver = null, src, alt = "", children, power, screenClassName, screenProps, placeholder, overlay, source, chin, expanded, expandLabel, onExpand, closeLabel, onClose, title, className }: ScreenPanelProps) {
  const strip = size === "strip";
  return <section className={cx("tb-device", `tb-device--${size}`, driver && `is-${driver}`, className)} aria-label={title}>
    <div className="tb-device-bezel">
      <div {...screenProps} className={cx("tb-device-screen", screenClassName, screenProps?.className)}>
        {children}
        {src ? <img src={src} alt={alt} /> : Children.toArray(children).length && !placeholder ? null : <div className="tb-device-empty">{placeholder}</div>}
        {overlay ? <div className="tb-device-overlay">{overlay}</div> : null}
        {source && !strip ? <span className="tb-device-source">{source}</span> : null}
        {!strip && (power || expandLabel || closeLabel) ? <div className="tb-device-ctl" onClick={event => event.stopPropagation()}>
          {power}
          {expandLabel ? <button type="button" className="tb-device-btn" aria-label={expandLabel} title={expandLabel} onClick={onExpand}><span className={cx("tb-expand-glyph", expanded && "is-expanded")} aria-hidden="true" /></button> : null}
          {closeLabel ? <button type="button" className="tb-device-btn" aria-label={closeLabel} title={closeLabel} onClick={onClose}><Icon name="close" size={16} /></button> : null}
        </div> : null}
      </div>
    </div>
    {chin ? <div className="tb-device-chin">{chin}</div> : null}
  </section>;
}
