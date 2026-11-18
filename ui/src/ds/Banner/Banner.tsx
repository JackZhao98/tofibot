import type { ReactNode } from "react";
import { Icon, type IconName } from "../Icon/Icon";
import { cx } from "../cx";
import "./Banner.css";

export type BannerTone = "info" | "warning" | "error" | "done";
const TONE_ICON: Record<BannerTone, IconName> = { info: "info", warning: "alert", error: "error", done: "check-circle" };

export type BannerProps = {
  tone?: BannerTone;
  title?: ReactNode;
  /** One optional action node. */
  action?: ReactNode;
  className?: string;
  children?: ReactNode;
};

/** An inline notice inside a page or card: a mark tile, a title and one sentence. Keep to one banner per view. */
export function Banner({ tone = "info", title, action, className, children }: BannerProps) {
  return <div className={cx("tb-banner", `tb-banner--${tone}`, className)} role={tone === "error" ? "alert" : "note"}>
    <span className="tb-banner-mark" aria-hidden="true"><Icon name={TONE_ICON[tone]} size={16} /></span>
    <div className="tb-banner-text">{title ? <strong>{title}</strong> : null}{children ? <span>{children}</span> : null}</div>
    {action ? <div className="tb-banner-action">{action}</div> : null}
  </div>;
}
