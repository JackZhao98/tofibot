import { cx } from "../cx";
import "./Ground.css";

/** The dot ground of a pane, drawn on its own absolutely placed layer. The pane places it (inset, z-index); a decoration. */
export function Ground({ className }: { className?: string }) {
  return <div className={cx("tb-dots", "tb-ground", className)} aria-hidden="true" />;
}
