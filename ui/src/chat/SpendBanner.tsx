import { useEffect, useState } from "react";
import { request } from "../api";
import { useTranslation } from "../i18n";
import { Banner } from "../ds/Banner/Banner";
import { Button } from "../ds/Button/Button";
import type { SpendStatus } from "../SpendCap";
import "./spend-banner.css";

/** Re-reads at most this often while the window is in view. */
const REFRESH_MS = 60_000;

/**
 * Spend shows once, here: a Banner above the composer when this month's spend is near the limit or past it. Under 80% and with
 * no limit set it draws nothing. Everything else about spend lives in Settings › Usage. A read that fails draws nothing.
 * `running` flips when a run starts or ends, which is when the figure moves.
 */
export default function SpendBanner({ running, onOpen }: { running: boolean; onOpen: () => void }) {
  const { t } = useTranslation(["chat", "settings", "common"]);
  const [status, setStatus] = useState<SpendStatus | null>(null);
  useEffect(() => {
    const abort = new AbortController();
    const read = () => { if (!document.hidden) request<SpendStatus>("/api/usage/spend", { signal: abort.signal }).then(setStatus, () => {}); };
    read();
    const timer = window.setInterval(read, REFRESH_MS);
    return () => { abort.abort(); window.clearInterval(timer); };
  }, [running]);
  if (!status || (status.state !== "near" && status.state !== "reached")) return null;
  const percent = status.limit_usd > 0 ? Math.min(100, Math.round(status.spend_usd / status.limit_usd * 100)) : 0;
  const reached = status.state === "reached";
  return <Banner tone={reached ? "error" : "warning"} className="spend-banner" action={<Button variant="ghost" size="sm" onClick={onOpen}>{t("chat:header.usage")}</Button>}>
    {reached ? t("settings:spend.state.reached") : t("settings:spend.state.near", { percent })}
  </Banner>;
}
