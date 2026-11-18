import { cx } from "../cx";
import "./ProgressiveBlur.css";

export type ProgressiveBlurProps = {
  edge?: "top" | "bottom";
  /** px, default 72. */
  height?: number;
  className?: string;
};

/** Frosted glass that fades out from one edge of the message pane: a light and a heavy blur layer. The pane gives its list padding
 *  at that edge. One band per edge; a decoration, so it is hidden from screen readers. */
export function ProgressiveBlur({ edge = "bottom", height, className }: ProgressiveBlurProps) {
  return <div className={cx("tb-pblur", `tb-pblur--${edge}`, className)} style={height ? { height } : undefined} aria-hidden="true" />;
}
