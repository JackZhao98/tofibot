import { BotAvatar } from "./BotAvatar";
import { useTranslation } from "./i18n";

export type DesktopPlaceholderTone = "waking" | "connecting" | "resuming" | "hibernated" | "browser" | "failed";

/**
 * The Tofi desktop standing in for a screen that has no picture yet: dot texture, the cat, one status line.
 * It fills its frame, so the frame keeps the screen's aspect ratio from the first paint.
 */
export function DesktopPlaceholder({ tone, status, botId, onRetry }: { tone: DesktopPlaceholderTone; status: string; botId: string; onRetry?: () => void }) {
  const { t } = useTranslation("computer");
  const failed = tone === "failed";
  return <div className={`desktop-launch-preview desktop-bot-starting desktop-placeholder is-${tone}`} role={failed ? "alert" : "status"}
    aria-label={failed ? t("desktop.launch.failed_aria") : t("desktop.launch.starting_aria")}>
    <span className="desktop-placeholder-grid" aria-hidden="true" />
    <span className="desktop-placeholder-cat" aria-hidden="true"><BotAvatar id={botId} motion={tone === "hibernated" ? "sleeping" : failed ? undefined : "working"} /></span>
    <p className="desktop-placeholder-line">{status}</p>
    {failed && onRetry && <button type="button" className="desktop-placeholder-retry" onClick={event => { event.stopPropagation(); onRetry(); }}>{t("desktop.placeholder.retry")}</button>}
  </div>;
}
