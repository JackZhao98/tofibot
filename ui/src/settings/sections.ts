/**
 * The stable ids a link can scroll to. This file mirrors the Go settings registry (internal/app/settings_registry.go): the
 * registry is the source, `ui/src/generated/settingsRegistry.json` is its export, and `npm --prefix ui run test:settings-registry`
 * fails when the two disagree or when a listed id is missing from the component that owns it.
 */
export const SETTINGS_SECTIONS = {
  general: ["account", "appearance", "primary", "region", "chats"],
  connections: ["list"],
  skills: ["library"],
  permissions: ["mode"],
  allowances: ["reviewer", "how", "standing", "remembered"],
  followups: ["bots", "nudges", "digest", "today", "limits"],
  models: ["providers", "defaults", "dictation"],
  tools: ["web"],
  automation: ["models", "replies", "history", "checks", "reliability", "schedules", "computer", "widgets"],
  computer: ["resources", "environment"],
  devices: ["devices"],
  keys: ["keys"],
  usage: ["health", "usage"],
  advanced: ["version", "server", "data", "changes", "debug", "danger"],
  admin: ["accounts"],
} as const;

export type SettingsTabId = keyof typeof SETTINGS_SECTIONS;
export const SETTINGS_TAB_IDS = Object.keys(SETTINGS_SECTIONS) as SettingsTabId[];

/** Sections of one Bot's settings cover (`BotSettingsPanel`). */
export const BOT_SETTINGS_SECTIONS = ["identity", "role", "model", "tools", "secrets", "advanced"] as const;
export type BotSettingsSection = (typeof BOT_SETTINGS_SECTIONS)[number];

/** Sections of a group's settings cover (`GroupSettingsPanel`). */
export const GROUP_SETTINGS_SECTIONS = ["general", "members"] as const;
export type GroupSettingsSection = (typeof GROUP_SETTINGS_SECTIONS)[number];

/** Sections of one connection's page. */
export const CONNECTION_SECTIONS = ["status", "tools", "advanced", "danger"] as const;

/** Settings tab ids that were renamed: the old id still opens the page that replaced it. */
export const LEGACY_SETTINGS_TABS: Record<string, SettingsTabId> = { approvals: "permissions" };
