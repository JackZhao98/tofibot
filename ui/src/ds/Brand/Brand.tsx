import { cx } from "../cx";
import "./Brand.css";

export type BrandProps = {
  /** The cat's height in px (the settings canvas draws 30). The wordmark scales with it. */
  size?: number;
  /** awake: the logo (eyes open). asleep: idle and empty states. */
  mood?: "awake" | "asleep";
  /** Draw the lowercase wordmark beside the cat. */
  wordmark?: boolean;
  className?: string;
};

const FILE = { awake: "/brand/tofi-logo-calico-awake.svg?v=awake-calico-3", asleep: "/brand/tofi-logo-calico.svg" } as const;

/** Tofi's own logo: the calico cat beside the lowercase wordmark in the display face. It works on Paper and Night, because
 *  the cat carries its own outline. Outside services use BrandMark. */
export function Brand({ size = 30, mood = "awake", wordmark = true, className }: BrandProps) {
  return <span className={cx("tb-brand", className)} role="img" aria-label="Tofi" style={{ fontSize: Math.round(size * 0.6) }}>
    <img src={FILE[mood]} alt="" width={size} height={size} />
    {wordmark && <span className="tb-brand-word" aria-hidden="true">tofi</span>}
  </span>;
}
