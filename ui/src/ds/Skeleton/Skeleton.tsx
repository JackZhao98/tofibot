import { useTranslation } from "../../i18n";
import { cx } from "../cx";
import "./Skeleton.css";

export type SkeletonProps = {
  lines?: number;
  /** The accessible name; defaults to "Loading". */
  label?: string;
  className?: string;
};

/** Placeholder lines for the first load of a list or card. On a refresh keep showing the last data instead. */
export function Skeleton({ lines = 3, label, className }: SkeletonProps) {
  const { t } = useTranslation();
  return <div className={cx("tb-skel", className)} role="status" aria-busy="true" aria-label={label ?? t("ds.loading")}>
    {Array.from({ length: lines }, (_, i) => <span key={i} className="tb-skel-line" style={{ width: `${i === lines - 1 ? 62 : 100 - i * 6}%` }} />)}
  </div>;
}
