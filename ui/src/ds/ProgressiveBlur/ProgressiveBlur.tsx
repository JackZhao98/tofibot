import { cx } from "../cx";
import "./ProgressiveBlur.css";

export type ProgressiveBlurProps = {
  edge?: "top" | "bottom";
  /** Sits at the chrome layer instead of the edge-blur layer, to cover a column that is under the header. */
  above?: boolean;
  /** px (a number) or any CSS length, default 72px. */
  height?: number | string;
  className?: string;
};

/** Frosted glass that fades out from one edge of the message pane: a light and a heavy blur layer. The pane gives its list padding
 *  at that edge. One band per edge; a decoration, so it is hidden from screen readers. */
export function ProgressiveBlur({ edge = "bottom", height, above, className }: ProgressiveBlurProps) {
  return <div className={cx("tb-pblur", `tb-pblur--${edge}`, above && "tb-pblur--chrome", className)} style={height ? { height } : undefined} aria-hidden="true" />;
}
