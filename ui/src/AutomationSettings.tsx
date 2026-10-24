import {memo, useCallback, useEffect, useRef, useState} from "react";
import type {TFunction} from "i18next";
import {request} from "./api";
import {modelName, groupModels} from "./modelCatalog";
import type {ModelCatalog} from "./types";
import {useModelCatalog} from "./useModelCatalog";
import {useTranslation} from "./i18n";
import {SettingsCard, SettingsRow, SettingsSection} from "./settings/components";
import {StandingAllowances} from "./StandingAllowances";
import "./settings/automation.css";

/** Every automatic behavior the server reports, in the order the page lists them. */
export type AutomationKey =
  | "conversation_model" | "background_model" | "structured_output" | "force_say_haiku_dm" | "web_search"
  | "group_exchange_gists" | "own_replies_verbatim" | "completion_review" | "answer_gate" | "mcp_auto_review"
  | "remembered_approval_hours" | "provider_retries" | "schedule_receipt_review" | "desktop_idle_seconds"
  | "group_triage" | "group_triage_model" | "task_retention_days" | "command_check_minutes" | "task_stall_rounds" | "task_tree_idle_message_limit";
export type AutomationGroup = "models" | "replies" | "history" | "checks" | "reliability" | "schedules" | "computer";
const groupOrder: AutomationGroup[] = ["models", "replies", "history", "checks", "reliability", "schedules", "computer"];

export type AutomationItem = {
  key: AutomationKey;
  group: AutomationGroup;
  kind: "bool" | "int" | "enum" | "model" | "readonly";
  default: boolean | number | string | null;
  value: boolean | number | string | null;
  /** The model actually in force (model items). */
  effective?: string;
  source: "default" | "stored" | "env";
  env?: string;
  locked?: boolean;
  read_only?: boolean;
  options?: string[];
  min?: number;
  max?: number;
  unit?: "hours" | "minutes" | "seconds" | "days";
};
type Value = boolean | number | string | null;
type Names = {t: TFunction<"settings">; catalog: ModelCatalog | null};

const hourPresets = [1, 4, 12, 24, 48, 72, 168, 720];
const countPresets = [10, 20, 30, 60, 120, 250, 500];
const dayPresets = [0, 7, 14, 30, 60, 90, 180, 365, 730, 3650];

function intChoices(item: AutomationItem, current: number): number[] {
  const min = item.min ?? 0, max = item.max ?? min;
  const base = max - min <= 24 ? Array.from({length: max - min + 1}, (_, index) => min + index) : (item.unit === "days" ? dayPresets : item.key === "task_tree_idle_message_limit" ? countPresets : hourPresets).filter(value => value >= min && value <= max);
  return base.includes(current) ? base : [...base, current].sort((a, b) => a - b);
}

function formatValue(item: AutomationItem, value: Value, {t, catalog}: Names): string {
  if (item.kind === "bool") return value ? t("automation.on") : t("automation.off");
  if (item.kind === "enum") return t(`automation.option.mcp_auto_review.${String(value) as "off" | "shadow" | "auto"}`);
  if (item.kind === "model") {
    const id = String(value ?? "");
    return catalog ? modelName(catalog.models, id) : id;
  }
  if (typeof value !== "number") return t("automation.not_reported");
  if (item.key === "provider_retries" && value === 0) return t("automation.retries_none");
  if (item.key === "desktop_idle_seconds") {
    if (value === 0) return t("automation.idle_off");
    return value % 60 === 0 ? t("automation.minutes", {count: value / 60}) : t("automation.seconds", {count: value});
  }
  if (item.key === "task_retention_days" && value === 0) return t("automation.keep_all");
  if (item.unit) return t(`automation.${item.unit}`, {count: value});
  return String(value);
}

function Control({item, catalog, busy, label, onChange}: {item: AutomationItem; catalog: ModelCatalog | null; busy: boolean; label: string; onChange: (value: Value) => void}) {
  const {t} = useTranslation("settings");
  const id = `auto-${item.key}`;
  const disabled = busy || item.locked === true;
  if (item.kind === "bool") {
    return <label className="debug-toggle auto-switch"><input id={id} type="checkbox" role="switch" aria-label={label} checked={item.value === true} disabled={disabled} onChange={event => onChange(event.target.checked)}/></label>;
  }
  if (item.kind === "readonly") {
    return <span className="auto-static">{formatValue(item, item.value, {t, catalog})}</span>;
  }
  if (item.kind === "enum") {
    return <select id={id} value={String(item.value)} disabled={disabled} onChange={event => onChange(event.target.value)}>{(item.options ?? []).map(option => <option key={option} value={option}>{t(`automation.option.mcp_auto_review.${option as "off" | "shadow" | "auto"}`)}</option>)}</select>;
  }
  if (item.kind === "int") {
    const current = typeof item.value === "number" ? item.value : 0;
    return <select id={id} value={String(current)} disabled={disabled} onChange={event => onChange(Number(event.target.value))}>{intChoices(item, current).map(choice => <option key={choice} value={choice}>{formatValue(item, choice, {t, catalog})}</option>)}</select>;
  }
  // Model: the catalog's groups, plus the value in force when the catalog lacks it. The background model also offers "Automatic".
  const models = catalog?.models ?? [];
  const groups = groupModels(models);
  const current = String(item.value ?? "");
  const automatic = item.key === "background_model" || item.key === "group_triage_model";
  const known = models.some(option => option.id === current);
  const autoModel = String(item.default ?? "");
  return <select id={id} value={current} disabled={disabled || (!catalog && !current && !automatic)} onChange={event => onChange(event.target.value)}>
    {automatic && <option value="">{t("automation.auto", {model: catalog ? modelName(models, autoModel) : autoModel})}</option>}
    {current && !known && <option value={current}>{current}</option>}
    {groups.map(group => <optgroup key={group.provider} label={group.label}>{group.models.map(option => <option key={option.id} value={option.id}>{option.label}</option>)}</optgroup>)}
  </select>;
}

type RowProps = {item: AutomationItem; catalog: ModelCatalog | null; busy: boolean; saved: boolean; error: string; onChange: (key: AutomationKey, value: Value) => void};

const AutomationRow = memo(function AutomationRow({item, catalog, busy, saved, error, onChange}: RowProps) {
  const {t} = useTranslation("settings");
  const name = t(`automation.item.${item.key}.name`);
  const changed = item.source === "stored";
  const defaultText = item.key === "background_model" || item.key === "group_triage_model" ? t("automation.auto", {model: catalog ? modelName(catalog.models, String(item.default ?? "")) : String(item.default ?? "")}) : formatValue(item, item.default, {t, catalog});
  const meta = <span className="auto-meta" data-source={item.source}>
    {item.kind !== "readonly" && <span>{t("automation.default_is", {value: defaultText})}</span>}
    {item.kind === "readonly" && <span>{t("automation.managed")}</span>}
    {item.locked && <span className="auto-chip is-env" title={item.env}>{t("automation.env")}</span>}
    {changed && !item.locked && <span className="auto-chip">{t("automation.changed")}</span>}
    {changed && !item.locked && item.kind !== "readonly" && <button type="button" className="text-button auto-reset" disabled={busy} onClick={() => onChange(item.key, null)}>{t("automation.reset")}</button>}
    {saved && <span role="status" className="auto-saved">{t("automation.saved")}</span>}
  </span>;
  return <div className="auto-row" data-key={item.key} data-source={item.source} aria-busy={busy || undefined}>
    <SettingsRow inline={item.kind === "bool"} labelFor={item.kind === "bool" || item.kind === "readonly" ? undefined : `auto-${item.key}`} label={name}
      description={<>{t(`automation.item.${item.key}.what`)}{meta}{error && <span className="error-text auto-error" role="alert">{error}</span>}</>}
      control={<Control item={item} catalog={catalog} busy={busy} label={name} onChange={value => onChange(item.key, value)}/>}/>
  </div>;
});

export function AutomationSettings() {
  const {t} = useTranslation("settings");
  const [items, setItems] = useState<AutomationItem[] | null>(null);
  const {catalog} = useModelCatalog();
  const [loadError, setLoadError] = useState("");
  const [version, setVersion] = useState(0);
  const [busy, setBusy] = useState<AutomationKey | null>(null);
  const [saved, setSaved] = useState<AutomationKey | null>(null);
  const [errors, setErrors] = useState<Partial<Record<AutomationKey, string>>>({});
  const itemsRef = useRef<AutomationItem[] | null>(null);
  itemsRef.current = items;

  useEffect(() => {
    const controller = new AbortController();
    setLoadError("");
    request<{items: AutomationItem[]}>("/api/automation-settings", {signal: controller.signal}).then(body => setItems(body.items)).catch(cause => {if (!controller.signal.aborted) setLoadError(cause instanceof Error ? cause.message : t("action.save_failed"));});
    return () => controller.abort();
  }, [version, t]);

  const change = useCallback(async (key: AutomationKey, value: Value) => {
    const before = itemsRef.current?.find(item => item.key === key);
    if (!before) return;
    setBusy(key); setSaved(null);
    setErrors(current => current[key] ? {...current, [key]: ""} : current);
    try {
      const body = await request<{items: AutomationItem[]}>("/api/automation-settings", {method: "PUT", body: JSON.stringify({values: {[key]: value}})});
      // Keep every other row's object identity so only the changed row renders again.
      setItems(current => current && current.map(item => {
        const next = body.items.find(candidate => candidate.key === item.key);
        return next && JSON.stringify(next) !== JSON.stringify(item) ? next : item;
      }));
      setSaved(key);
    } catch (cause) {
      setErrors(current => ({...current, [key]: cause instanceof Error ? cause.message : t("action.save_failed")}));
    } finally {
      setBusy(null);
    }
  }, [t]);

  if (!items) {
    return <SettingsSection>{loadError ? <p className="error-text" role="alert">{loadError}<button type="button" className="text-button" onClick={() => setVersion(current => current + 1)}>{t("action.retry")}</button></p> : <p className="muted">{t("action.loading_settings")}</p>}</SettingsSection>;
  }
  return <div className="settings-stack auto-page">
    <p className="settings-sec-desc auto-intro">{t("automation.intro")}</p>
    {groupOrder.map(group => {
      const rows = items.filter(item => item.group === group);
      if (!rows.length) return null;
      return <SettingsSection key={group} title={t(`automation.group.${group}.title`)} description={t(`automation.group.${group}.description`)}>
        <SettingsCard>{rows.map(item => <AutomationRow key={item.key} item={item} catalog={catalog} busy={busy === item.key} saved={saved === item.key} error={errors[item.key] ?? ""} onChange={change}/>)}</SettingsCard>
      </SettingsSection>;
    })}
    <StandingAllowances/>
  </div>;
}
