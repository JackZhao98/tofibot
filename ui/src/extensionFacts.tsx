import type { TFunction } from "i18next";
import type { ReactNode } from "react";
import { i18n } from "./i18n";
import { linkRuntime } from "./settings/deepLinks";
import type { ApprovalFacts } from "./questionTimeline";

type Translate = TFunction<"tasks">;
type Row = { label: string; value: ReactNode };

/** Rows for the server-verified section of an approval card. Values are plain text, never markup. */
export function approvalFactRows(facts: ApprovalFacts, t: Translate): Row[] {
  if (facts.setting) return settingFactRows(facts.setting, t);
  const rows: Row[] = [
    { label: t("record.facts.kind"), value: t(`record.facts.kinds.${facts.kind}`) },
    { label: t("record.facts.name"), value: <code>{facts.name}</code> },
  ];
  const list = (items?: string[]) => items && items.length > 0 ? items.join(", ") : "";
  if (facts.source) rows.push({ label: t("record.facts.source"), value: <code>{facts.source}</code> });
  if (facts.previous_source && facts.previous_source !== facts.source) rows.push({ label: t("record.facts.previous_source"), value: <code>{facts.previous_source}</code> });
  if (facts.transport) rows.push({ label: t("record.facts.transport"), value: <code>{facts.transport}</code> });
  if (facts.kind === "mcp_create" || facts.kind === "mcp_update") {
    rows.push({ label: t("record.facts.tools"), value: list(facts.allow_tools) ? t("record.facts.only_tools", { tools: list(facts.allow_tools) }) : t("record.facts.all_tools") });
    if (list(facts.deny_tools)) rows.push({ label: t("record.facts.blocked_tools"), value: list(facts.deny_tools) });
    rows.push({ label: t("record.facts.headers"), value: list(facts.header_names) || t("record.facts.none") });
    if (facts.oauth_metadata || list(facts.oauth_scopes)) rows.push({ label: t("record.facts.oauth"), value: [facts.oauth_metadata, list(facts.oauth_scopes)].filter(Boolean).join(" · ") });
  }
  const skill = facts.skill;
  if (skill) {
    if (skill.description) rows.push({ label: t("record.facts.skill_description"), value: skill.description });
    if (skill.excerpt) rows.push({ label: t("record.facts.skill_excerpt"), value: skill.excerpt });
    rows.push({ label: t("record.facts.skill_tools"), value: list(skill.allowed_tools) || t("record.facts.none") });
    rows.push({ label: t("record.facts.skill_files"), value: list(skill.files) || t("record.facts.none") });
    if (skill.has_scripts) rows.push({ label: t("record.facts.skill_scripts"), value: <span className="approval-warn">{t("record.facts.skill_scripts_present")}</span> });
    if (facts.kind === "skill_publish_version") {
      // The diff against the current files, counted by the server; the Bot's wording is not part of it.
      if (skill.new_version) rows.push({ label: t("record.facts.skill_new_version"), value: `v${skill.new_version}` });
      if (list(skill.added)) rows.push({ label: t("record.facts.skill_added"), value: list(skill.added) });
      if (list(skill.removed)) rows.push({ label: t("record.facts.skill_removed"), value: list(skill.removed) });
      if (list(skill.changed)) rows.push({ label: t("record.facts.skill_changed"), value: list(skill.changed) });
      rows.push({ label: t("record.facts.skill_enabled_bots"), value: String(skill.enabled_bots ?? 0) });
    }
  }
  if (facts.change_id) rows.push({ label: t("record.facts.change_id"), value: <code>{facts.change_id}</code> });
  return rows;
}

/** A settings change: what, for whom, from what to what, the effect in the person's language, and the page where it lives. */
function settingFactRows(setting: NonNullable<ApprovalFacts["setting"]>, t: Translate): Row[] {
  const catalog = i18n.t as (key: string) => string;
  const label = i18n.exists(setting.label_key) ? catalog(setting.label_key) : setting.key;
  const effectKey = `record.facts.effects.${setting.effect_code}`;
  const value = (text: string) => text || t("record.facts.setting_empty");
  const rows: Row[] = [
    { label: t("record.facts.kind"), value: t("record.facts.kinds.setting_change") },
    { label: t("record.facts.setting_label"), value: label },
  ];
  if (setting.target_name) rows.push({ label: t("record.facts.setting_target"), value: setting.target_name });
  if (setting.old !== "" || setting.new !== "") {
    rows.push({ label: t("record.facts.setting_from"), value: value(setting.old) }, { label: t("record.facts.setting_to"), value: <strong>{value(setting.new)}</strong> });
  }
  rows.push({ label: t("record.facts.setting_effect"), value: i18n.exists(`tasks:${effectKey}`) ? t(effectKey as never) : setting.effect });
  if (setting.loosens) rows.push({ label: t("record.facts.setting_loosens"), value: <span className="approval-warn">{t("record.facts.setting_loosens_value")}</span> });
  rows.push({ label: t("record.facts.setting_page"), value: <button type="button" className="text-button" onClick={() => void linkRuntime().then(module => module.openHref(setting.link))}>{t("record.facts.setting_open")}</button> });
  return rows;
}
