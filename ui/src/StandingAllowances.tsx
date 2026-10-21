import { useCallback, useEffect, useState } from "react";
import { request } from "./api";
import { BotAvatar } from "./BotAvatar";
import { useTranslation } from "./i18n";
import { intlLocale } from "./i18n/format";
import { SettingsCard, SettingsSection } from "./settings/components";
import { useUserTimezone } from "./UserTimezone";

/** "Always allow": one Bot, one tool on one server. Server record; no arguments, URL or credential. */
export interface StandingAllowance {
  id: string;
  bot_id: string;
  bot_name: string;
  server: string;
  tool: string;
  /** server_changed: the server's address or transport changed, so the allowance no longer applies. */
  state: "active" | "server_changed";
  created_at: string;
  use_count: number;
  last_used_at?: string;
}
/** A grant from "Approve and remember in this chat". */
export interface RememberedEntry {
  id: string;
  conversation_id: string;
  bot_id: string;
  bot_name: string;
  conversation_name: string;
  server: string;
  tool: string;
  target_kind: "identifier" | "exact";
  target_summary?: string;
  expires_at: string;
  use_count: number;
}
type Listing = { allowances: StandingAllowance[]; remembered: RememberedEntry[] };

/** Settings > Automatic behavior: every place the system uses a tool without asking, each with Revoke. */
export function StandingAllowances() {
  const { t } = useTranslation("settings");
  const { timezone } = useUserTimezone();
  const [listing, setListing] = useState<Listing | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const load = useCallback(async (signal?: AbortSignal) => {
    try {
      const body = await request<Partial<Listing>>("/api/standing-allowances", { signal });
      setListing({ allowances: body.allowances ?? [], remembered: body.remembered ?? [] });
      setError("");
    } catch (cause) {
      if (!signal?.aborted) setError(cause instanceof Error ? cause.message : t("automation.standing.load_failed"));
    }
  }, [t]);
  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal);
    const changed = () => void load();
    window.addEventListener("tofi:standing-allowances-changed", changed);
    window.addEventListener("tofi:remembered-approvals-changed", changed);
    return () => {
      controller.abort();
      window.removeEventListener("tofi:standing-allowances-changed", changed);
      window.removeEventListener("tofi:remembered-approvals-changed", changed);
    };
  }, [load]);
  async function revoke(key: string, path: string) {
    if (busy) return;
    setBusy(key); setError("");
    try {
      await request(path, { method: "DELETE" });
      await load();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : t("automation.standing.revoke_failed"));
    } finally { setBusy(null); }
  }
  const when = (value: string) => new Intl.DateTimeFormat(intlLocale(), { timeZone: timezone, dateStyle: "medium", timeStyle: "short" }).format(new Date(value));
  const uses = (count: number) => count > 0 ? ` · ${t("automation.standing.used", { count })}` : "";
  return <>
    <SettingsSection title={t("automation.standing.title")} description={t("automation.standing.description")}>
      <SettingsCard>
        {listing === null && !error && <p className="muted standing-loading" aria-live="polite">{t("action.loading_settings")}</p>}
        {listing !== null && listing.allowances.length === 0 && <p className="muted standing-empty">{t("automation.standing.empty")}</p>}
        {listing !== null && listing.allowances.length > 0 && <ul className="standing-list" aria-label={t("automation.standing.title")}>
          {listing.allowances.map(item => <li key={item.id} className="standing-item" data-allowance-id={item.id} data-state={item.state}>
            <span className="standing-avatar"><BotAvatar id={item.bot_id} mini /></span>
            <div className="standing-main">
              <strong><span className="standing-bot">{item.bot_name}</span><code>{item.tool}</code><span className="standing-server">{item.server}</span></strong>
              <small className="standing-meta">{t("automation.standing.granted", { time: when(item.created_at) })} · {item.last_used_at ? t("automation.standing.last_used", { time: when(item.last_used_at) }) : t("automation.standing.never_used")}{uses(item.use_count)}</small>
              {item.state === "server_changed" && <small className="standing-warning">{t("automation.standing.server_changed")}</small>}
            </div>
            <button type="button" className="secondary-button standing-revoke" disabled={Boolean(busy)} onClick={() => void revoke(item.id, `/api/standing-allowances/${encodeURIComponent(item.id)}`)}>{busy === item.id ? t("automation.standing.revoking") : t("automation.standing.revoke")}</button>
          </li>)}
        </ul>}
        {error && <p role="alert" className="error-text standing-error">{error}</p>}
      </SettingsCard>
    </SettingsSection>
    <SettingsSection title={t("automation.standing.remembered.title")} description={t("automation.standing.remembered.description")}>
      <SettingsCard>
        {listing !== null && listing.remembered.length === 0 && <p className="muted standing-empty">{t("automation.standing.remembered.empty")}</p>}
        {listing !== null && listing.remembered.length > 0 && <ul className="standing-list" aria-label={t("automation.standing.remembered.title")}>
          {listing.remembered.map(item => <li key={item.id} className="standing-item" data-remembered-id={item.id}>
            <span className="standing-avatar"><BotAvatar id={item.bot_id} mini /></span>
            <div className="standing-main">
              <strong><span className="standing-bot">{item.bot_name}</span><code>{item.tool}</code><span className="standing-server">{item.server}</span></strong>
              <small className="standing-meta">{t("automation.standing.remembered.chat", { name: item.conversation_name || t("automation.standing.remembered.unknown_chat") })} · {item.target_kind === "identifier" && item.target_summary ? t("automation.standing.remembered.target_identifier", { target: item.target_summary }) : t("automation.standing.remembered.target_exact")}</small>
              <small className="standing-meta">{t("automation.standing.remembered.ends", { time: when(item.expires_at) })}{uses(item.use_count)}</small>
            </div>
            <button type="button" className="secondary-button standing-revoke" disabled={Boolean(busy)} onClick={() => void revoke(item.id, `/api/conversations/${encodeURIComponent(item.conversation_id)}/remembered-approvals/${encodeURIComponent(item.id)}`)}>{busy === item.id ? t("automation.standing.revoking") : t("automation.standing.revoke")}</button>
          </li>)}
        </ul>}
      </SettingsCard>
    </SettingsSection>
  </>;
}
