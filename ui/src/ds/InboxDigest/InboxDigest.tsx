import { cx } from "../cx";
import "./InboxDigest.css";

export type InboxItem = {
  from: string;
  subject: string;
  /** tone: tomato for "needs a reply", quiet for notices, none (mustard) for e.g. bills. */
  tag?: { label: string; tone?: "tomato" | "quiet" };
  time?: string;
  /** The one letter that needs you; first in the list, at most one. */
  lifted?: boolean;
};
export type InboxDigestProps = { items: InboxItem[]; /** The accessible name of the tray. */ label?: string; className?: string };

/** The mail a Bot sorted for you, as a quiet tray of envelopes. Only the letter that needs you is lifted. A digest has no actions. */
export function InboxDigest({ items, label, className }: InboxDigestProps) {
  return <div className={cx("tb-inbox", className)} role="list" aria-label={label}>
    {items.map((item, i) => <div key={i} role="listitem" className={cx("tb-env", item.lifted && "is-lifted")}>
      <span className="tb-env-from">{item.from}</span>
      <span className="tb-env-subject">{item.subject}</span>
      {item.tag ? <span className={cx("tb-env-tag", item.tag.tone && `tb-env-tag--${item.tag.tone}`)}>{item.tag.label}</span> : null}
      {item.time ? <small>{item.time}</small> : null}
    </div>)}
  </div>;
}
