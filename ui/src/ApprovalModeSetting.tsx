import {useCallback, useEffect, useRef, useState} from "react";
import {api, request} from "./api";
import {BotAvatar} from "./BotAvatar";
import {useSettingsActive} from "./settingsActivity";
import {useTranslation} from "./i18n";
import {approvalChoiceFor, reviewModeFor, type ApprovalChoice, type ReviewMode} from "./approvalMode";
import {SettingsCard, SettingsRow, SettingsSection, Segmented} from "./settings/components";
import {groupModels, modelName} from "./modelCatalog";
import {useModelCatalog} from "./useModelCatalog";
import type {Bot} from "./types";
import {Select} from "./Select";

type Top = ApprovalChoice | "bot";
const modeOf = (bot: Bot): ApprovalChoice => (bot.approval_mode === "auto" ? "auto" : "ask");

/**
 * Settings > Permissions. The top control is a bulk control over every Bot: Auto and Ask before edits write each Bot's own
 * setting at once and lock the per-Bot list; Per Bot unlocks it. Nothing remembers an earlier per-Bot choice: when the Bots
 * differ the top reads Per Bot, and choosing Auto or Ask overwrites all of them.
 */
export function ApprovalModeSetting() {
  const {t} = useTranslation("settings");
  const active = useSettingsActive();
  const [bots, setBots] = useState<Bot[] | null>(null);
  const [account, setAccount] = useState<ReviewMode | null>(null);
  const [unlocked, setUnlocked] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [version, setVersion] = useState(0);
  const writing = useRef(false);
  // Re-read whenever the page is shown: Advanced > Tool rules writes the account mode, a Bot's page can add Bots.
  useEffect(() => {
    if (!active || writing.current) return;
    const controller = new AbortController();
    Promise.all([api.bots(false), request<{mode: ReviewMode}>("/api/auto-review-settings", {signal: controller.signal})])
      .then(([found, settings]) => { setBots(found.bots.filter(bot => !bot.archived)); setAccount(settings.mode); setError(""); })
      .catch(cause => { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : t("action.save_failed")); });
    return () => controller.abort();
  }, [active, version, t]);

  const list = bots ?? [];
  const derived: Top = !bots || account === null ? "bot" : list.length === 0 ? approvalChoiceFor(account) : list.every(bot => modeOf(bot) === "auto") ? "auto" : list.every(bot => modeOf(bot) === "ask") ? "ask" : "bot";
  const top: Top = unlocked ? "bot" : derived;
  const locked = top !== "bot";

  const patch = (id: string, mode: ApprovalChoice) => request<Bot>(`/api/bots/${encodeURIComponent(id)}`, {method: "PATCH", body: JSON.stringify({approval_mode: mode})});
  async function run(work: () => Promise<void>) {
    if (writing.current) return;
    writing.current = true; setBusy(true); setError("");
    try { await work(); }
    catch (cause) { setError(cause instanceof Error ? cause.message : t("action.save_failed")); setVersion(current => current + 1); }
    finally { writing.current = false; setBusy(false); }
  }
  const chooseTop = (next: Top) => {
    if (busy || bots === null || account === null || next === top) return;
    if (next === "bot") { setUnlocked(true); return; }
    void run(async () => {
      // Every Bot gets the setting written; the account mode follows so a Bot added later starts the same way.
      for (const bot of list) if (modeOf(bot) !== next) await patch(bot.id, next);
      const target = reviewModeFor(next, account);
      if (target !== account) await request("/api/auto-review-settings", {method: "PUT", body: JSON.stringify({mode: target})});
      setAccount(target);
      setBots(current => current && current.map(bot => ({...bot, approval_mode: next})));
      setUnlocked(false);
    });
  };
  const chooseBot = (bot: Bot, next: ApprovalChoice) => {
    if (busy || locked || modeOf(bot) === next) return;
    void run(async () => {
      const saved = await patch(bot.id, next);
      setBots(current => current && current.map(item => item.id === bot.id ? {...item, approval_mode: saved.approval_mode ?? next} : item));
    });
  };
  const options = (["auto", "ask"] as const).map(value => ({value, label: t(`approval.${value}`)}));
  const topOptions: {value: Top; label: string}[] = [...options, {value: "bot", label: t("approval.bot")}];

  return <div className="settings-stack">
    <SettingsCard>
      <SettingsRow label={t("approval.label")} control={bots === null ? <span className="muted">{t("action.loading_settings")}</span> : <Segmented label={t("approval.label")} value={top} disabled={busy} options={topOptions} onChange={chooseTop}/>}/>
    </SettingsCard>
    {error && <p className="error-text" role="alert">{error}{bots === null && <button type="button" className="text-button" onClick={() => setVersion(current => current + 1)}>{t("action.retry")}</button>}</p>}
    {bots !== null && <SettingsSection title={t("approval.bots_title")} description={locked ? t("approval.bots_locked") : t("approval.bots_description")}>
      <SettingsCard className="approval-bots">
        {list.length === 0 ? <p className="settings-card-note">{t("approval.no_bots")}</p> : list.map(bot => <div className="approval-bot-row" key={bot.id} data-bot-id={bot.id}>
          <span className="approval-bot-who"><BotAvatar id={bot.id} mini still/><strong>{bot.name}</strong></span>
          <Segmented label={t("approval.bot_label", {name: bot.name})} value={modeOf(bot)} disabled={busy || locked} options={options} onChange={value => chooseBot(bot, value)}/>
        </div>)}
      </SettingsCard>
    </SettingsSection>}
    <ReviewerModel/>
  </div>;
}

type BackgroundItem = {key: string; value: string | null; default: string | null; locked?: boolean};

/** The reviewer is the background model (Settings > Behavior lists the same setting): "Automatic" or a chosen model. */
function ReviewerModel() {
  const {t} = useTranslation("settings");
  const active = useSettingsActive();
  const {catalog} = useModelCatalog();
  const [item, setItem] = useState<BackgroundItem | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [saved, setSaved] = useState(false);
  const pick = useCallback((items: BackgroundItem[]) => items.find(candidate => candidate.key === "background_model") ?? null, []);
  useEffect(() => {
    if (!active) return;
    const controller = new AbortController();
    request<{items: BackgroundItem[]}>("/api/automation-settings", {signal: controller.signal}).then(body => { setItem(pick(body.items)); setError(""); }).catch(cause => { if (!controller.signal.aborted) setError(cause instanceof Error ? cause.message : t("action.save_failed")); });
    return () => controller.abort();
  }, [active, pick, t]);
  async function change(value: string) {
    if (busy) return;
    setBusy(true); setError(""); setSaved(false);
    try {
      const body = await request<{items: BackgroundItem[]}>("/api/automation-settings", {method: "PUT", body: JSON.stringify({values: {background_model: value === "" ? null : value}})});
      setItem(pick(body.items)); setSaved(true);
    } catch (cause) { setError(cause instanceof Error ? cause.message : t("action.save_failed")); }
    finally { setBusy(false); }
  }
  const models = catalog?.models ?? [];
  const current = String(item?.value ?? "");
  const known = models.some(model => model.id === current);
  const automatic = String(item?.default ?? "");
  return <SettingsSection title={t("approval.reviewer_title")}>
    <SettingsCard>
      <SettingsRow labelFor="approval-reviewer-model" label={t("approval.reviewer_label")} description={t("approval.reviewer_description")} control={item === null
        ? <span className="muted">{error || t("action.loading_settings")}</span>
        : <Select id="approval-reviewer-model" value={current} disabled={busy || item.locked === true} onValueChange={value => void change(value)}>
          <option value="">{t("automation.auto", {model: catalog ? modelName(models, automatic) : automatic})}</option>
          {current && !known && <option value={current}>{current}</option>}
          {groupModels(models).map(group => <optgroup key={group.provider} label={group.label}>{group.models.map(option => <option key={option.id} value={option.id}>{option.label}</option>)}</optgroup>)}
        </Select>}/>
    </SettingsCard>
    {saved && <span role="status" className="settings-feedback">{t("automation.saved")}</span>}
    {error && item !== null && <p className="error-text" role="alert">{error}</p>}
  </SettingsSection>;
}
