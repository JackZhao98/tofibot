import { i18n } from "./i18n";

// Safe defaults are shared by the UI and its finite synthetic contract checks.
export const ENVIRONMENT_CATEGORY = "vault_environment";
const categoryKeys = {
  bot_config: "portability.category.bot_config", conversations: "portability.category.conversations", chats: "portability.category.chats", memories: "portability.category.memories", schedules: "portability.category.schedules",
  attachments: "portability.category.attachments", attachment_bindings: "portability.category.attachment_bindings", missing_attachments: "portability.category.missing_attachments", settings: "portability.category.settings", vault_environment: "portability.category.vault_environment",
} as const;
/** Display name for a bundle category or count; unknown server names show as sent. */
export function portabilityLabel(category: string): string {
  const key = categoryKeys[category as keyof typeof categoryKeys];
  return key ? i18n.t(key, { ns: "settings" }) : category;
}
export const exportDefaultCategories = ["bot_config", "chats", "memories", "schedules", "attachments"];
export function importDefaultCategories(included: string[]): string[] { return included.filter(c => c !== "settings" && c !== ENVIRONMENT_CATEGORY); }
export function checkPortableInput(source: string, encrypted: boolean): void {
  let parsed: { format?: string; version?: number; included?: unknown; vault_environment?: unknown };
  try { parsed = JSON.parse(source); } catch { throw new Error(i18n.t("settings:portability.error.unreadable")); }
  if (!parsed || !["tofi.bundle", "tofi.bot"].includes(parsed.format ?? "") || !(parsed.version === 1 || (parsed.format === "tofi.bundle" && [2, 3].includes(parsed.version ?? 0))) || !Array.isArray(parsed.included)) throw new Error(i18n.t("settings:portability.error.unsupported"));
  if (!encrypted && (parsed.version === 3 || parsed.included.includes(ENVIRONMENT_CATEGORY) || (Array.isArray(parsed.vault_environment) && parsed.vault_environment.length > 0))) throw new Error(i18n.t("settings:portability.error.environment_needs_encryption"));
}
