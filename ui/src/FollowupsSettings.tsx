import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "./api";
import { useTranslation } from "./i18n";
import { formatDateTime, formatNumber, formatRelativeTime } from "./i18n/format";
import { PrimaryBadge } from "./PrimaryAssistant";
import { useSettingsActive } from "./settingsActivity";
import { Banner, SettingsCard, SettingsRow, SettingsSection } from "./settings/components";
import { useUserTimezone } from "./UserTimezone";
import type { FollowupLoop, FollowupMode, FollowupOverview, FollowupVerdict } from "./types";
import "./followups.css";
import { Select } from "./Select";

const kindKeys = { awaiting_other: "followups.kind.waiting", owner_promise: "followups.kind.owner_promise", owner_reminder: "followups.kind.owner_reminder" } as const;
const verdictKeys = { useful: "followups.verdict.useful", annoying: "followups.verdict.annoying", handled: "followups.verdict.handled" } as const;
const capChoices = [1, 2, 3, 4, 5];
const budgetChoices = [0.25, 0.5, 1, 2];

/** Settings › Follow-ups (preview): shadow results and the owner's review buttons. Nothing here posts to a chat. */
export function FollowupsSettings() {
  const { t } = useTranslation("settings");
  const active = useSettingsActive();
  const { timezone } = useUserTimezone();
  const [data, setData] = useState<FollowupOverview | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState("");
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const load = useCallback(async (signal?: AbortSignal) => {
    try { const next = await api.followups(signal); if (mounted.current && !signal?.aborted) { setData(next); setError(""); } }
    catch { if (mounted.current && !signal?.aborted) setError(t("followups.load_failed")); }
  }, [t]);
  useEffect(() => {
    if (!active) return;
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [active, load]);

  async function act(key: string, work: () => Promise<unknown>) {
    if (busy) return;
    setBusy(key); setError("");
    try { await work(); await load(); }
    catch { if (mounted.current) setError(t("followups.save_failed")); }
    finally { if (mounted.current) setBusy(""); }
  }

  if (!data) return <div className="settings-stack">{error ? <Banner tone="error" title={error} action={{ label: t("action.retry"), onClick: () => void load() }} /> : <p className="settings-card-note">{t("action.loading_settings")}</p>}</div>;

  const previewing = data.bots.filter(bot => bot.mode !== "off");
  const nudges = data.loops.filter(loop => loop.band === "nudge" && isLive(loop));
  const digest = data.loops.filter(loop => loop.band === "digest" && isLive(loop));
  const closed = data.loops.filter(loop => !isLive(loop));
  const money = (value: number) => formatNumber(value, { style: "currency", currency: "USD", maximumFractionDigits: value < 1 ? 2 : 0 });
  const stats = data.stats;

  return <div className="settings-stack followups-page">
    <Banner tone="info" title={t("followups.banner_title")}>{t("followups.banner_body")}</Banner>
    {!data.model_configured && <Banner tone="warn" title={t("followups.no_model_title")}>{t("followups.no_model_body")}</Banner>}
    {data.budget_paused && <Banner tone="warn" title={t("followups.paused_title")}>{t("followups.paused_body", { limit: money(data.account.daily_budget_usd) })}</Banner>}
    {error && <p className="error-text" role="alert">{error}</p>}

    <SettingsSection title={t("followups.bots_title")} description={t("followups.bots_description")}>
      <SettingsCard>
        {data.bots.length === 0 && <p className="settings-card-note">{t("followups.no_bots")}</p>}
        {data.bots.map(bot => <SettingsRow key={bot.bot_id} labelFor={`followup-mode-${bot.bot_id}`}
          label={<span className="followup-bot-name"><span className="followup-bot-label">{bot.name}</span>{bot.is_primary && <PrimaryBadge />}</span>}
          description={bot.mode === "off" ? t("followups.bot_off") : bot.last_sweep_at ? t("followups.last_scan", { when: formatRelativeTime(bot.last_sweep_at) }) : t("followups.not_scanned")}
          control={<Select id={`followup-mode-${bot.bot_id}`} value={bot.mode} disabled={Boolean(busy)} onValueChange={value => void act(`bot-${bot.bot_id}`, () => api.setFollowupBot(bot.bot_id, { mode: value as FollowupMode }))}>
            <option value="off">{t("followups.mode.off")}</option>
            <option value="shadow">{t("followups.mode.shadow")}</option>
          </Select>} />)}
      </SettingsCard>
    </SettingsSection>

    <SettingsSection title={t("followups.nudges_title")} hint={nudges.length ? formatNumber(nudges.length) : undefined} description={t("followups.nudges_description")}>
      {renderLoops(nudges, previewing.length > 0)}
    </SettingsSection>
    <SettingsSection title={t("followups.digest_title")} hint={digest.length ? formatNumber(digest.length) : undefined} description={t("followups.digest_description")}>
      {renderLoops(digest, previewing.length > 0)}
    </SettingsSection>
    {closed.length > 0 && <SettingsSection title={t("followups.closed_title")}>{renderLoops(closed, true)}</SettingsSection>}

    <SettingsSection title={t("followups.today_title")}>
      <SettingsCard>
        <SettingsRow inline label={t("followups.stat.scans")} control={<span className="followup-stat">{formatNumber(stats.sweeps)}</span>} />
        <SettingsRow inline label={t("followups.stat.found")} control={<span className="followup-stat">{formatNumber(stats.loops_created)}</span>} />
        <SettingsRow inline label={t("followups.stat.skipped")} description={t("followups.stat.skipped_hint")} control={<span className="followup-stat">{formatNumber(stats.rejected_invalid + stats.rejected_band)}</span>} />
        <SettingsRow inline label={t("followups.stat.spent")} control={<span className="followup-stat">{money(stats.spent_usd)}</span>} />
        <SettingsRow inline label={t("followups.stat.feedback")} description={t("followups.stat.feedback_hint")} control={<span className="followup-stat">{t("followups.stat.feedback_value", { useful: stats.useful, annoying: stats.annoying, handled: stats.handled })}</span>} />
      </SettingsCard>
    </SettingsSection>

    <SettingsSection title={t("followups.limits_title")} description={t("followups.limits_description")}>
      <SettingsCard>
        <SettingsRow labelFor="followup-cap" label={t("followups.limit.cap")} description={t("followups.limit.cap_hint")}
          control={<Select id="followup-cap" value={String(data.account.max_nudges_per_day)} disabled={Boolean(busy)} onValueChange={value => void act("cap", () => api.setFollowupAccount({ max_nudges_per_day: Number(value) }))}>
            {(capChoices.includes(data.account.max_nudges_per_day) ? capChoices : [...capChoices, data.account.max_nudges_per_day].sort()).map(n => <option key={n} value={n}>{t("followups.limit.per_day", { n })}</option>)}
          </Select>} />
        <SettingsRow labelFor="followup-budget" label={t("followups.limit.budget")} description={t("followups.limit.budget_hint")}
          control={<Select id="followup-budget" value={String(data.account.daily_budget_usd)} disabled={Boolean(busy)} onValueChange={value => void act("budget", () => api.setFollowupAccount({ daily_budget_usd: Number(value) }))}>
            {(budgetChoices.includes(data.account.daily_budget_usd) ? budgetChoices : [...budgetChoices, data.account.daily_budget_usd].sort((a, b) => a - b)).map(n => <option key={n} value={n}>{money(n)}</option>)}
          </Select>} />
        <SettingsRow inline label={t("followups.limit.quiet")} description={t("followups.limit.quiet_hint")} control={<span className="followup-stat">{data.account.quiet_start}–{data.account.quiet_end}</span>} />
      </SettingsCard>
    </SettingsSection>
  </div>;

  function renderLoops(loops: FollowupLoop[], scanning: boolean) {
    if (!loops.length) return <SettingsCard><p className="settings-card-note">{scanning ? t("followups.empty_scanning") : t("followups.empty_off")}</p></SettingsCard>;
    return <SettingsCard>{loops.map(loop => <article key={loop.id} className="followup-item" data-state={loop.state}>
      <div className="followup-head"><strong>{loop.topic}</strong>{loop.verdict && <span className="followup-verdict">{t(verdictKeys[loop.verdict])}</span>}</div>
      <p className="followup-meta">{[t(kindKeys[loop.kind]), loop.counterparty_name, loop.bot_name, t("followups.due", { when: formatDateTime(loop.expected_by, { dateStyle: "medium", timeStyle: "short", timeZone: timezone }) })].filter(Boolean).join(" · ")}</p>
      {loop.quote && <blockquote className="followup-quote">{loop.quote}</blockquote>}
      {loop.suggested_next && <p className="followup-next">{t("followups.next", { step: loop.suggested_next })}</p>}
      {isLive(loop) && <div className="followup-actions" role="group" aria-label={t("followups.review_label", { topic: loop.topic })}>
        {(["useful", "annoying", "handled"] as FollowupVerdict[]).map(verdict => <button key={verdict} type="button" className="secondary-button" aria-pressed={loop.verdict === verdict} disabled={Boolean(busy)} onClick={() => void act(`loop-${loop.id}`, () => api.followupFeedback(loop.id, verdict))}>{t(verdictKeys[verdict])}</button>)}
      </div>}
    </article>)}</SettingsCard>;
  }
}

function isLive(loop: FollowupLoop) {
  return loop.state === "open" || loop.state === "digest_only" || loop.state === "nudged" || loop.state === "snoozed";
}
