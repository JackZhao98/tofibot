import type { TFunction } from "i18next";
import type { ReactNode } from "react";
import type { ApprovalFacts } from "./questionTimeline";

type Translate = TFunction<"tasks">;
type Row = { label: string; value: ReactNode };

/** Rows for the server-verified section of an approval card. Values are plain text, never markup. */
export function approvalFactRows(facts: ApprovalFacts, t: Translate): Row[] {
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
  }
  if (facts.change_id) rows.push({ label: t("record.facts.change_id"), value: <code>{facts.change_id}</code> });
  return rows;
}
