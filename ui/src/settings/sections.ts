/**
 * The stable ids a link can scroll to. This file mirrors the Go settings registry (internal/app/settings_registry.go): the
 * registry is the source, `ui/src/generated/settingsRegistry.json` is its export, and `npm --prefix ui run test:settings-registry`
 * fails when the two disagree or when a listed id is missing from the component that owns it.
 */
export const SETTINGS_SECTIONS = {
  general: ["account", "appearance", "region", "chats"],
  connections: ["list", "web"],
  skills: ["library"],
  permissions: ["mode", "standing", "remembered", "advanced"],
  models: ["providers", "defaults", "more"],
  computer: ["resources", "environment", "keys"],
  devices: ["devices"],
  usage: ["spend", "usage", "storage"],
  // System holds Behavior (the automatic behaviors) as a subpage: "behavior" is its common rows, the five group ids are its
  // folded groups, "followups" its Follow-ups subpage.
  advanced: ["version", "server", "behavior", "groups", "replies", "reviews", "tasks", "computer", "followups", "data", "diagnostics", "danger"],
  admin: ["accounts"],
  "admin-groups": ["policy", "members"],
  "admin-resources": ["models", "computers", "web"],
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

/**
 * Older settings positions ("tab" or "tab#section") and where the setting lives now (settings-simplify section 7). The Go registry
 * carries the same table (`settingsLegacyLinks`); `test:settings-registry` compares them. "general#primary" is not listed: it opens the
 * primary Bot's settings (`readSettingsLink`), or General when there is no Bot.
 */
export const LEGACY_SETTINGS_LINKS: Record<string, string> = {
  approvals: "permissions", devices: "computer",
  allowances: "permissions", "allowances#how": "permissions", "allowances#standing": "permissions#standing",
  "allowances#remembered": "permissions#remembered", "allowances#reviewer": "permissions#advanced",
  followups: "advanced#followups", tools: "connections#web", keys: "computer#keys",
  "usage#health": "advanced#diagnostics", "advanced#changes": "advanced#data", "advanced#debug": "advanced#diagnostics",
  "models#dictation": "models#more",
  automation: "advanced#behavior", "automation#models": "models#more", "automation#history": "advanced#replies",
  "automation#checks": "advanced#reviews", "automation#schedules": "advanced#reviews", "automation#reliability": "advanced#tasks",
  "automation#replies": "advanced#replies", "automation#computer": "advanced#computer", "automation#widgets": "advanced#behavior",
};
