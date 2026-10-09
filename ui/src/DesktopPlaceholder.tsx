import { useEffect, useState } from "react";
import { BotAvatar } from "./BotAvatar";
import { useTranslation } from "./i18n";

export type DesktopPlaceholderTone = "waking" | "connecting" | "resuming" | "hibernated" | "failed";

/** Whole seconds since `key` last changed: the "· 3s" after a status line. */
function useElapsedSeconds(key: string, running: boolean) {
  const [seconds, setSeconds] = useState(0);
  useEffect(() => {
    setSeconds(0);
    if (!running) return;
    const started = Date.now();
    const timer = window.setInterval(() => setSeconds(Math.floor((Date.now() - started) / 1000)), 1000);
    return () => window.clearInterval(timer);
  }, [key, running]);
  return seconds;
}

/**
 * The Tofi desktop standing in for a screen that has no picture yet: dot texture, the cat, one status line.
 * It fills its frame, so the frame keeps the screen's aspect ratio from the first paint.
 */
export function DesktopPlaceholder({ tone, status, botId, onRetry }: { tone: DesktopPlaceholderTone; status: string; botId: string; onRetry?: () => void }) {
  const { t } = useTranslation("computer");
  const timed = tone === "waking" || tone === "connecting" || tone === "resuming";
  const seconds = useElapsedSeconds(`${tone}:${status}`, timed);
  const line = timed && seconds > 0 ? t("desktop.placeholder.elapsed", { status, seconds }) : status;
  const failed = tone === "failed";
  return <div className={`desktop-launch-preview desktop-bot-starting desktop-placeholder is-${tone}`} role={failed ? "alert" : "status"}
    aria-label={failed ? t("desktop.launch.failed_aria") : t("desktop.launch.starting_aria")}>
    <span className="desktop-placeholder-grid" aria-hidden="true" />
    <span className="desktop-placeholder-cat" aria-hidden="true"><BotAvatar id={botId} motion={tone === "hibernated" ? "sleeping" : failed ? undefined : "working"} /></span>
    <p className="desktop-placeholder-line">{line}</p>
    {failed && onRetry && <button type="button" className="desktop-placeholder-retry" onClick={event => { event.stopPropagation(); onRetry(); }}>{t("desktop.placeholder.retry")}</button>}
  </div>;
}
