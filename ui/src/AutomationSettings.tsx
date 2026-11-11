import {memo, useCallback, useEffect, useRef, useState} from "react";
import type {TFunction} from "i18next";
import {request} from "./api";
import {useHasComputer} from "./computerCapability";
import {modelName, groupModels} from "./modelCatalog";
import type {ModelCatalog} from "./types";
import {useModelCatalog} from "./useModelCatalog";
import {useTranslation} from "./i18n";
import {FoldGroup} from "./settings/subpages";
import {ChangedNote, SavedNote, SettingsCard, SettingsRow, SettingsSection, SettingsSwitch} from "./settings/components";
import "./settings/automation.css";
import { Select } from "./Select";

/** Every automatic behavior the server reports, in the order the page lists them. */
export type AutomationKey =
  | "conversation_model" | "background_model" | "structured_output" | "web_search" | "web_fetch_limit"
  | "group_exchange_gists" | "own_replies_verbatim" | "completion_review" | "answer_gate" | "mcp_auto_review"
  | "remembered_approval_hours" | "provider_retries" | "schedule_receipt_review" | "desktop_idle_seconds"
  | "group_triage" | "group_triage_model" | "group_followup_to_working" | "task_retention_days" | "upload_retention_days" | "bots_can_keep_files" | "command_check_minutes" | "computer_idle_off_minutes" | "task_stall_rounds" | "task_tree_idle_message_limit"
  | "task_fanout_width" | "task_fanout_depth" | "task_part_deadline_minutes" | "task_part_deadline_scheduled_minutes"
  | "widget_artifacts_auto" | "widget_computer_auto" | "browser_engine" | "desktop_engine" | "personal_input_review"
  | "work_trace" | "trace_retention_days" | "trace_private_origins" | "monthly_spend_limit_usd";
/** Where a row is shown (the server's `group`): the Behavior page's own groups, or beside the setting it belongs with. */
export type AutomationGroup = "common" | "groups" | "replies" | "reviews" | "tasks" | "computer" | "defaults" | "models" | "remembered" | "permissions" | "environment" | "changes" | "usage";
/** Behavior's folded groups, in order; `common` is always shown. */
const foldedGroups = ["groups", "replies", "reviews", "tasks", "computer"] as const;
/** The common rows, in order. */
const commonOrder: AutomationKey[] = ["web_search", "structured_output", "widget_artifacts_auto", "widget_computer_auto", "computer_idle_off_minutes", "work_trace", "provider_retries"];
/** Rows that do nothing without a Bot computer (the whole `computer` group too). */
const computerOnly = new Set<AutomationKey>(["widget_computer_auto", "computer_idle_off_minutes", "work_trace"]);

export type AutomationItem = {
  key: AutomationKey;
  group: AutomationGroup;
  kind: "bool" | "int" | "enum" | "model" | "readonly" | "text";
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
const minutePresets = [1, 5, 10, 15, 30, 45, 60, 90, 120, 240, 480, 1440];

function intChoices(item: AutomationItem, current: number): number[] {
  const min = item.min ?? 0, max = item.max ?? min;
  const base = max - min <= 24 ? Array.from({length: max - min + 1}, (_, index) => min + index) : (item.unit === "days" ? dayPresets : item.unit === "minutes" ? minutePresets : item.key === "task_tree_idle_message_limit" ? countPresets : hourPresets).filter(value => value >= min && value <= max);
  return base.includes(current) ? base : [...base, current].sort((a, b) => a - b);
}

function formatValue(item: AutomationItem, value: Value, {t, catalog}: Names): string {
  if (item.kind === "bool") return value ? t("automation.on") : t("automation.off");
  if (item.kind === "enum" && item.key === "browser_engine") return t(`automation.option.browser_engine.${String(value) as "classic" | "playwright"}`);
  if (item.kind === "enum" && item.key === "desktop_engine") return t(`automation.option.desktop_engine.${String(value) as "classic" | "a11y"}`);
  if (item.kind === "enum" && item.key === "personal_input_review") return t(`automation.option.personal_input_review.${String(value) as "declared" | "commands" | "all"}`);
  if (item.kind === "enum") return t(`automation.option.mcp_auto_review.${String(value) as "off" | "shadow" | "auto"}`);
  if (item.kind === "model") {
    const id = String(value ?? "");
    return catalog ? modelName(catalog.models, id) : id;
  }
  if (item.kind === "text") return typeof value === "string" && value.trim() ? value.split("\n").join(", ") : t("automation.empty_list");
  if (typeof value !== "number") return t("automation.not_reported");
  if (item.key === "provider_retries" && value === 0) return t("automation.retries_none");
  if (item.key === "desktop_idle_seconds") {
    if (value === 0) return t("automation.idle_off");
    return value % 60 === 0 ? t("automation.minutes", {count: value / 60}) : t("automation.seconds", {count: value});
  }
  if ((item.key === "task_retention_days" || item.key === "trace_retention_days" || item.key === "upload_retention_days") && value === 0) return t("automation.keep_all");
  if (item.unit) return t(`automation.${item.unit}`, {count: value});
  return String(value);
}

/** A short list, one entry per line; the change is saved when the field loses focus. */
function TextControl({id, value, disabled, label, onCommit}: {id: string; value: string; disabled: boolean; label: string; onCommit: (value: string) => void}) {
  const {t} = useTranslation("settings");
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  return <textarea id={id} className="auto-text" rows={3} spellCheck={false} aria-label={label} placeholder={t("automation.origins_placeholder")} value={draft} disabled={disabled} onChange={event => setDraft(event.target.value)} onBlur={() => {if (draft !== value) onCommit(draft);}}/>;
}

function Control({item, catalog, busy, label, onChange, onOpenPermissions}: {item: AutomationItem; catalog: ModelCatalog | null; busy: boolean; label: string; onChange: (value: Value) => void; onOpenPermissions?: () => void}) {
  const {t} = useTranslation("settings");
  const id = `auto-${item.key}`;
  const disabled = busy || item.locked === true;
  if (item.kind === "bool") {
    // The row shows its own "Saved" and the failure; the switch stays quiet.
    return <SettingsSwitch id={id} label={label} checked={item.value === true} disabled={disabled} busy={busy} showSaved={false} onChange={onChange}/>;
  }
  if (item.kind === "readonly") {
    return <span className="auto-static">{formatValue(item, item.value, {t, catalog})}</span>;
  }
  if (item.kind === "text") return <TextControl id={id} value={typeof item.value === "string" ? item.value : ""} disabled={disabled} label={label} onCommit={value => onChange(value)}/>;
  // The reviewer mode lives in one place, Settings > Permissions; this row shows the value (D15) and links there.
  if (item.kind === "enum" && (item.key === "browser_engine" || item.key === "desktop_engine" || item.key === "personal_input_review")) {
    return <Select id={id} value={String(item.value ?? item.default ?? "")} disabled={disabled} onValueChange={value => onChange(value)}>{(item.options ?? []).map(choice => <option key={choice} value={choice}>{formatValue(item, choice, {t, catalog})}</option>)}</Select>;
  }
  if (item.kind === "enum") {
    return <span className="auto-static auto-linked">{formatValue(item, item.value, {t, catalog})}{onOpenPermissions && <button type="button" className="text-button" onClick={onOpenPermissions}>{t("automation.in_permissions")}</button>}</span>;
  }
  if (item.kind === "int") {
    const current = typeof item.value === "number" ? item.value : 0;
    return <Select id={id} value={String(current)} disabled={disabled} onValueChange={value => onChange(Number(value))}>{intChoices(item, current).map(choice => <option key={choice} value={choice}>{formatValue(item, choice, {t, catalog})}</option>)}</Select>;
  }
  // Model: the catalog's groups, plus the value in force when the catalog lacks it. The background model also offers "Automatic".
  const models = catalog?.models ?? [];
  const groups = groupModels(models);
  const current = String(item.value ?? "");
  const automatic = item.key === "background_model" || item.key === "group_triage_model";
  const known = models.some(option => option.id === current);
  const autoModel = String(item.default ?? "");
  return <Select id={id} value={current} disabled={disabled || (!catalog && !current && !automatic)} onValueChange={value => onChange(value)}>
    {automatic && <option value="">{t("automation.auto", {model: catalog ? modelName(models, autoModel) : autoModel})}</option>}
    {current && !known && <option value={current}>{current}</option>}
    {groups.map(group => <optgroup key={group.provider} label={group.label}>{group.models.map(option => <option key={option.id} value={option.id}>{option.label}</option>)}</optgroup>)}
  </Select>;
}

type RowProps = {onOpenPermissions?: () => void; item: AutomationItem; catalog: ModelCatalog | null; busy: boolean; saved: boolean; error: string; onChange: (key: AutomationKey, value: Value) => void};

const AutomationRow = memo(function AutomationRow({item, catalog, busy, saved, error, onChange, onOpenPermissions}: RowProps) {
  const {t} = useTranslation("settings");
  const name = t(`automation.item.${item.key}.name`);
  const changed = item.source === "stored";
  const defaultText = item.key === "background_model" || item.key === "group_triage_model" ? t("automation.auto", {model: catalog ? modelName(catalog.models, String(item.default ?? "")) : String(item.default ?? "")}) : formatValue(item, item.default, {t, catalog});
  const resettable = changed && !item.locked && item.kind !== "readonly" && (item.kind !== "enum" || item.key === "browser_engine" || item.key === "desktop_engine" || item.key === "personal_input_review");
  // A value equal to its default says nothing; a changed one reads "Changed · default X · Reset".
  const meta = <span className="auto-meta" data-source={item.source}>
    {item.kind === "readonly" && <span>{t("automation.managed")}</span>}
    {item.key === "browser_engine" && item.source === "default" && <span className="auto-note" data-testid="default-changed">{t("automation.default_changed")}</span>}
    {item.locked && <span className="auto-chip is-env" title={item.env}>{t("automation.env")}</span>}
    {resettable && <ChangedNote defaultValue={defaultText} disabled={busy} resetLabel={t("automation.reset")} onReset={() => onChange(item.key, null)}/>}
    {changed && !item.locked && !resettable && item.kind !== "readonly" && <span className="st-changed">{t("changed.note", {value: defaultText})}</span>}
  </span>;
  return <div className="auto-row" data-key={item.key} data-source={item.source} aria-busy={busy || undefined}>
    <SettingsRow inline={item.kind === "bool"} labelFor={item.kind === "bool" || item.kind === "readonly" ? undefined : `auto-${item.key}`} label={name}
      description={<>{t(`automation.item.${item.key}.what`)}{meta}{error && <span className="error-text auto-error" role="alert">{error}</span>}</>}
      control={<><SavedNote show={saved}/><Control item={item} catalog={catalog} busy={busy} label={name} onChange={value => onChange(item.key, value)} onOpenPermissions={onOpenPermissions}/></>}/>
  </div>;
});

/** Reads the automation items and writes one at a time; every instance on a page keeps its own copy. */
function useAutomation() {
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
      // The workspace reads the two widget switches; tell it now instead of at its next load.
      window.dispatchEvent(new Event("tofi:automation-changed"));
    } catch (cause) {
      setErrors(current => ({...current, [key]: cause instanceof Error ? cause.message : t("action.save_failed")}));
    } finally {
      setBusy(null);
    }
  }, [t]);
  return {items, catalog, loadError, retry: () => setVersion(current => current + 1), busy, saved, errors, change};
}
type Automation = ReturnType<typeof useAutomation>;

function Loading({automation}: {automation: Automation}) {
  const {t} = useTranslation("settings");
  return automation.loadError ? <p className="error-text" role="alert">{automation.loadError}<button type="button" className="text-button" onClick={automation.retry}>{t("action.retry")}</button></p> : <p className="muted">{t("action.loading_settings")}</p>;
}

/** The rows of the given groups, as one card (a group that lives beside the setting it belongs with: Models > More, Permissions, ...). */
export function AutomationRows({groups, flat = false}: {groups: AutomationGroup[]; /** Rows only, for a place that already is a card (a folded group). */ flat?: boolean}) {
  const automation = useAutomation();
  const hasComputer = useHasComputer();
  if (!automation.items) return <Loading automation={automation}/>;
  const rows = automation.items.filter(item => groups.includes(item.group) && item.key !== "mcp_auto_review" && (hasComputer || (item.group !== "computer" && item.group !== "environment" && !computerOnly.has(item.key))));
  const list = rows.map(item => <Row key={item.key} automation={automation} item={item}/>);
  return !rows.length ? null : flat ? <>{list}</> : <SettingsCard>{list}</SettingsCard>;
}

const Row = ({automation, item}: {automation: Automation; item: AutomationItem}) =>
  <AutomationRow item={item} catalog={automation.catalog} busy={automation.busy === item.key} saved={automation.saved === item.key} error={automation.errors[item.key] ?? ""} onChange={automation.change}/>;

/** System > Behavior: the common rows, then one folded line per group (a count and how many were changed), then Follow-up reminders. */
export function BehaviorPage({onOpenFollowups}: {onOpenFollowups: () => void}) {
  const {t} = useTranslation("settings");
  const hasComputer = useHasComputer();
  const automation = useAutomation();
  if (!automation.items) return <Loading automation={automation}/>;
  const common = commonOrder.map(key => automation.items!.find(item => item.key === key)).filter((item): item is AutomationItem => Boolean(item) && (hasComputer || !computerOnly.has(item!.key)));
  return <div className="settings-stack auto-page">
    <SettingsSection id="behavior" title={t("automation.common")}>
      <SettingsCard>{common.map(item => <Row key={item.key} automation={automation} item={item}/>)}</SettingsCard>
    </SettingsSection>
    <SettingsSection title={t("automation.more")}>
      <SettingsCard className="st-folds">
        {foldedGroups.filter(group => hasComputer || group !== "computer").map(group => {
          const rows = automation.items!.filter(item => item.group === group);
          const changed = rows.filter(item => item.source === "stored").length;
          return <FoldGroup key={group} tab="advanced" id={group} title={t(`automation.group.${group}.title`)} summary={changed ? t("automation.fold.changed", {n: rows.length, changed}) : t("automation.fold.items", {n: rows.length})}>
            {rows.map(item => <Row key={item.key} automation={automation} item={item}/>)}
          </FoldGroup>;
        })}
        <SettingsRow label={t("shell.page.followups.name")} link={{label: t("shell.page.followups.name"), onOpen: onOpenFollowups}}/>
      </SettingsCard>
    </SettingsSection>
  </div>;
}
