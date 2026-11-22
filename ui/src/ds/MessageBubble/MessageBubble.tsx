import type { MouseEventHandler, ReactNode } from "react";
import { cx } from "../cx";
import { Icon } from "../Icon/Icon";
import "../bot-context.css";
import "./MessageBubble.css";

/** A Bot's hue, or `you` for the person (blue is theirs alone). */
export type BubbleTone = "mustard" | "tomato" | "teal" | "grape" | "lime" | "you";

export type MessageQuote = {
  author: string;
  text: string;
  /** The quoted sender's colour; `you` is the person's blue. */
  tone?: BubbleTone;
  onJump?: MouseEventHandler<HTMLButtonElement>;
  jumpLabel?: string;
};

export type MessageBubbleProps = {
  /** `user` is you (right); `bot` is anyone else, Bots and other members (left). */
  role?: "user" | "bot";
  /** A Bot's coat: tints its bubble and name. */
  botColor?: Exclude<BubbleTone, "you">;
  /** The sender's name. Shown above the first bubble of a run, for others only. */
  author?: ReactNode;
  /** The sender's avatar. Shown beside the last bubble of a run, for others only. */
  avatar?: ReactNode;
  /** Not the first bubble of a run: the corners on the sender's side tighten. */
  joined?: boolean;
  /** Not the last bubble of a run. */
  continued?: boolean;
  quote?: MessageQuote;
  attachments?: ReactNode;
  reactions?: ReactNode;
  /** A failure line only: no delivery ticks, no times. */
  meta?: ReactNode;
  failed?: boolean;
  /** Dimmed while it goes out. */
  sending?: boolean;
  /** Hover actions beside the bubble, on the open side. Their look belongs to MessageActions. */
  actions?: ReactNode;
  showActions?: boolean;
  /** A quote jumped here: the bubble frames itself once. */
  flash?: boolean;
  /** Just arrived: settles in once, 6px up over 220ms. Never on history load. */
  fresh?: boolean;
  /** A streaming caret after the content. */
  caret?: boolean;
  /** Reserve the avatar column for others' bubbles (default). A direct chat has none. */
  avatarColumn?: boolean;
  className?: string;
  children?: ReactNode;
};

/** A message: a bubble in a run (name over the first, avatar beside the last), with its quote, reactions and hover actions. */
export function MessageBubble({
  role = "bot", botColor, author, avatar, joined, continued, quote, attachments, reactions, meta, failed, sending, actions, showActions, flash, fresh, caret, avatarColumn = true, className, children,
}: MessageBubbleProps) {
  const other = role !== "user";
  const bubble = children ? (
    <div className="tb-bubble">
      {quote ? (
        <button type="button" className={cx("tb-quote", quote.tone && `tb-tone--${quote.tone}`)} onClick={quote.onJump} title={quote.jumpLabel} aria-label={quote.jumpLabel}>
          <span className="tb-quote-author">{quote.author}</span>
          <span className="tb-quote-text">{quote.text}</span>
        </button>
      ) : null}
      {children}
      {caret ? <span className="tb-caret" aria-hidden="true" /> : null}
    </div>
  ) : attachments ? (
    <div className="tb-msg-attach">{attachments}</div>
  ) : null;
  return (
    <div className={cx("tb-msg", `tb-msg--${role}`, botColor && `tb-bot--${botColor}`, joined && "is-joined", continued && "is-continued", !!meta && "has-foot", sending && "is-sending", showActions && "is-hover", flash && "is-flash", fresh && "is-fresh", !!reactions && "has-react", className)}>
      {other && avatarColumn ? (avatar && !continued ? <span className="tb-msg-avatar">{avatar}</span> : <span className="tb-msg-avatar tb-msg-avatar--gap" />) : null}
      <div className="tb-msg-col">
        {other && author && !joined ? <div className="tb-msg-author">{author}</div> : null}
        {bubble ? (
          <div className="tb-msg-line">
            {bubble}
            {reactions ? <div className="tb-msg-react">{reactions}</div> : null}
            {actions ? <div className="tb-msg-side">{actions}</div> : null}
          </div>
        ) : null}
        {children && attachments ? <div className="tb-msg-attach">{attachments}</div> : null}
        {meta ? <div className={cx("tb-msg-meta", failed && "is-failed")}>{failed ? <Icon name="error" size={12} /> : null}{meta}</div> : null}
      </div>
    </div>
  );
}
