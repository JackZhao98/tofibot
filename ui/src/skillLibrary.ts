import { i18n } from "./i18n";
import { formatDateTime } from "./i18n/format";

// Skill library types and helpers (decision 0058). The server owns the truth;
// these are the shapes it sends and the folder reading the browser does.

export type SkillInstaller = { kind: "owner" | "bot" | "legacy"; bot_id?: string; bot_name?: string };
export type SkillVersion = { version: number; created_at: string; created_by: SkillInstaller; note?: string; current: boolean };
export type LibrarySkill = {
  name: string; description: string; builtin?: boolean; bundle_id?: string;
  installed_by?: SkillInstaller; installed_at?: string; version?: number; updated?: boolean; updated_at?: string;
  versions?: SkillVersion[]; enabled_bots: number; bots_total: number;
};
export type LibraryBundle = { id: string; description?: string; version?: string; installed_by?: SkillInstaller; installed_at: string; skills: string[] };
export type LibraryListing = { skills: LibrarySkill[]; bundles: LibraryBundle[]; diagnostics?: { message: string }[] };

export type BotSkill = { name: string; description: string; builtin?: boolean; bundle_id?: string; enabled: boolean; set_by?: "owner" | "bot" | "migration"; set_at?: string };
export type BotSkillListing = { skills: BotSkill[]; bundles: LibraryBundle[] };

export type PickedSkill = { kind: "skill"; name: string; files: Record<string, string> } | { kind: "bundle"; name: string; files: Record<string, string> };

const MAX_FILES = 256;
const MAX_BUNDLE_FILES = 1024;
const MAX_BYTES = 1024 * 1024;
const MAX_BUNDLE_BYTES = 1536 * 1024;
const PLUGIN_MANIFEST = ".claude-plugin/plugin.json";

/** Who put a skill in the library, as a short phrase. */
export function installerName(by: SkillInstaller | undefined): string {
  if (!by || by.kind === "legacy") return i18n.t("extensions:skills.library.by.legacy");
  if (by.kind === "owner") return i18n.t("extensions:skills.library.by.owner");
  return by.bot_name || i18n.t("extensions:skills.library.by.bot_unknown");
}

export function installedLine(by: SkillInstaller | undefined, at: string | undefined): string {
  if (!by || by.kind === "legacy") return i18n.t("extensions:skills.library.installed_legacy");
  return i18n.t("extensions:skills.library.installed_by", { who: installerName(by), time: at ? formatDateTime(at) : "" });
}

/** "set by X at T" for one Bot's choice on one skill. */
export function setByLine(skill: BotSkill): string {
  if (!skill.set_by) return "";
  if (skill.set_by === "migration") return i18n.t("bots:skills.set_by.migration");
  const time = skill.set_at ? formatDateTime(skill.set_at) : "";
  return i18n.t(`bots:skills.set_by.${skill.set_by}_${skill.enabled ? "on" : "off"}`, { time });
}

function frontmatterName(text: string): string {
  const head = /^\s*---\r?\n([\s\S]*?)\r?\n---/.exec(text)?.[1] ?? "";
  const match = /^name:\s*["']?([^"'\r\n]+?)["']?\s*$/m.exec(head);
  return match?.[1]?.trim() ?? "";
}

function pluginName(text: string): string {
  try { const value = JSON.parse(text) as { name?: unknown }; return typeof value.name === "string" ? value.name.trim() : ""; } catch { return ""; }
}

/**
 * Reads a folder picked with webkitdirectory. A plugin folder (a
 * .claude-plugin/plugin.json and skills/<name>/SKILL.md) is a bundle; a folder
 * with SKILL.md at its root is one skill. Throws an Error whose message is a
 * `skills.error.*` key suffix.
 */
export async function readSkillFolder(list: File[]): Promise<PickedSkill> {
  const files: Record<string, string> = {};
  let total = 0, count = 0;
  const root = list[0]?.webkitRelativePath.split("/")[0] ?? "";
  for (const file of list) {
    const path = file.webkitRelativePath.split("/").slice(1).join("/") || file.name;
    const hidden = path.split("/").some(part => part.startsWith(".") && part !== ".claude-plugin");
    if (hidden) continue;
    count += 1;
    total += file.size;
    if (count > MAX_BUNDLE_FILES) throw new Error("too_many_files");
    if (total > MAX_BUNDLE_BYTES) throw new Error("too_large");
    files[path] = await file.text();
  }
  if (files[PLUGIN_MANIFEST] !== undefined) {
    const name = pluginName(files[PLUGIN_MANIFEST]);
    if (!name) throw new Error("bad_plugin");
    return { kind: "bundle", name, files };
  }
  if (files["SKILL.md"] === undefined) {
    throw new Error(Object.keys(files).some(path => /^skills\/[^/]+\/SKILL\.md$/.test(path)) ? "no_plugin_manifest" : "no_skill_md");
  }
  if (count > MAX_FILES) throw new Error("too_many_files");
  if (total > MAX_BYTES) throw new Error("too_large");
  return { kind: "skill", name: frontmatterName(files["SKILL.md"]) || root, files };
}

/** Skill names that appear on a Bot's list under a bundle id, in listing order. */
export function bundleMembers(skills: { name: string; bundle_id?: string }[], bundleId: string): string[] {
  return skills.filter(skill => skill.bundle_id === bundleId).map(skill => skill.name);
}
