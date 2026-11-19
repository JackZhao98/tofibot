import type { ReactNode, Ref } from "react";
import { cx } from "../cx";
import "./ChatHeader.css";

export type ChatHeaderPosition = "top" | "scrolled" | "aside";

export type ChatHeaderProps = {
  /** top: the start of the conversation, a big centred cat. scrolled: a small centred cat. aside: a cover is open, the cat steps left. */
  position?: ChatHeaderPosition;
  title: ReactNode;
  /** A group's member count. Shown at the top position only. */
  subtitle?: ReactNode;
  /** The cats: one for a Bot, up to three for a group. Bare artwork, no tile. */
  avatars: ReactNode[];
  /** Tapping the cat. */
  onOpen?: () => void;
  openLabel?: string;
  /** The left pill: the way back to the list. */
  menu?: ReactNode;
  /** `narrow` shows the left pill only where the conversation list is hidden (up to 767 px). */
  menuMode?: "always" | "narrow";
  /** Icon buttons, at most three. */
  actions?: ReactNode;
  /** Draw the header's own blur band (a standalone header). The chat pane has its own band, so the product leaves it off. */
  blur?: boolean;
  className?: string;
  /** The product hooks the window-drag strip and the height measurement on the root. */
  headerRef?: Ref<HTMLElement>;
  /** The first child of the root, behind everything: the macOS window-drag strip. */
  drag?: ReactNode;
};

/** The top of a conversation, with no bar: the Bot's cat floats over progressive blur and moves with the view; one pill of
 *  actions sits on the right. The cat is the one place a Bot moves (thinking sways, working bobs, waiting leans in; an error
 *  stops still, asleep dims), and its dot names the state. */
export function ChatHeader({ blur, position = "scrolled", title, subtitle, avatars, onOpen, openLabel, menu, menuMode = "always", actions, className, headerRef, drag }: ChatHeaderProps) {
  return <header ref={headerRef} className={cx("tb-chathead", `is-${position}`, blur && "has-blur", !!menu && menuMode === "narrow" && "has-narrow-menu", className)}>
    {drag}
    {menu && <div className="tb-chathead-left">{menu}</div>}
    <button type="button" className="tb-chathead-cat chat-identity" onClick={onOpen} aria-label={openLabel}>
      <span className="tb-chathead-face member-avatar-stack">{avatars.slice(0, 3).map((cat, index) => <span key={index} className="tb-chathead-face-i">{cat}</span>)}</span>
      <span className="tb-chathead-name chat-name-pill">
        <h1 className="tb-chathead-title">{title}</h1>
        {subtitle ? <span className="tb-chathead-sub">{subtitle}</span> : null}
      </span>
    </button>
    {actions && <div className="tb-chathead-actions header-actions">{actions}</div>}
  </header>;
}
