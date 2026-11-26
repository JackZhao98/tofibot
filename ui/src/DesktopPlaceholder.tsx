import "./desktop-placeholder.css";
import { BotAvatar } from "./BotAvatar";
import { Button } from "./ds/Button/Button";
import { useTranslation } from "./i18n";

export type DesktopPlaceholderTone = "waking" | "connecting" | "resuming" | "hibernated" | "browser" | "failed";

/**
 * What a ScreenPanel shows while the computer has no picture yet: the cat and one status line (the dot ground is the panel's own).
 * It sits in the panel's `placeholder` (or `overlay`) slot, so the frame keeps the screen's aspect ratio from the first paint.
 */
export function DesktopPlaceholder({ tone, status, botId, onRetry, action }: { tone: DesktopPlaceholderTone; status: string; botId: string; onRetry?: () => void; action?: { label: string; onClick: () => void } }) {
  const { t } = useTranslation("computer");
  const failed = tone === "failed";
  return <div className={`desktop-placeholder is-${tone}`} role={failed ? "alert" : "status"}
    aria-label={failed ? t("desktop.launch.failed_aria") : t("desktop.launch.starting_aria")}>
    <span className="desktop-placeholder-cat" aria-hidden="true"><BotAvatar id={botId} motion={tone === "hibernated" ? "sleeping" : failed ? undefined : "working"} /></span>
    <p className="desktop-placeholder-line">{status}</p>
    {failed && onRetry && <Button size="sm" className="desktop-placeholder-retry" onClick={event => { event.stopPropagation(); onRetry(); }}>{t("desktop.placeholder.retry")}</Button>}
    {!failed && action && <Button size="sm" className="desktop-placeholder-retry desktop-placeholder-action" onClick={event => { event.stopPropagation(); action.onClick(); }}>{action.label}</Button>}
  </div>;
}
