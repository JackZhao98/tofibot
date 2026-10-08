import type { Bot } from "./types";
import { normalizeConfig, type AvatarConfig } from "./lib/tofi-avatar/index.js";
import { getBotAvatarConfig } from "./avatarStore";
import { i18n } from "./i18n";

export const BOT_PACKAGE_FORMAT = "tofi.bot" as const;
export const BOT_PACKAGE_VERSION = 1 as const;

export interface PortableBotPackage {
  format: typeof BOT_PACKAGE_FORMAT;
  version: typeof BOT_PACKAGE_VERSION;
  included: ["bot_config"];
  bot: {
    name: string;
    instructions: string;
    model: string;
    reasoning_effort?: string;
    avatar?: AvatarConfig;
  };
}

export function buildBotPackage(bot: Bot): PortableBotPackage {
  return {
    format: BOT_PACKAGE_FORMAT,
    version: BOT_PACKAGE_VERSION,
    included: ["bot_config"],
    bot: {
      name: bot.name,
      instructions: bot.instructions,
      model: bot.model,
      avatar: getBotAvatarConfig(bot.id),
      ...(bot.reasoning_effort ? { reasoning_effort: bot.reasoning_effort } : {}),
    },
  };
}

export function parseBotPackage(source: string): PortableBotPackage {
  let value: unknown;
  try {
    value = JSON.parse(source);
  } catch {
    throw new Error(i18n.t("bots:package.invalid_json"));
  }
  if (!value || typeof value !== "object") throw new Error(i18n.t("bots:package.invalid_format"));
  const candidate = value as { format?: unknown; version?: unknown; included?: unknown; bot?: unknown };
  if (candidate.format !== BOT_PACKAGE_FORMAT || candidate.version !== BOT_PACKAGE_VERSION || !Array.isArray(candidate.included) || candidate.included.length !== 1 || candidate.included[0] !== "bot_config") {
    throw new Error(i18n.t("bots:package.unsupported_version"));
  }
  if (!candidate.bot || typeof candidate.bot !== "object") throw new Error(i18n.t("bots:package.missing_bot"));
  const bot = candidate.bot as { name?: unknown; instructions?: unknown; model?: unknown; reasoning_effort?: unknown; avatar?: unknown };
  if (typeof bot.name !== "string" || !bot.name.trim() || bot.name.length > 200) throw new Error(i18n.t("bots:package.invalid_name"));
  if (typeof bot.instructions !== "string" || bot.instructions.length > 200_000) throw new Error(i18n.t("bots:package.invalid_instructions"));
  if (typeof bot.model !== "string" || bot.model.length > 200) throw new Error(i18n.t("bots:package.invalid_model"));
  if (bot.reasoning_effort !== undefined && typeof bot.reasoning_effort !== "string") throw new Error(i18n.t("bots:package.invalid_reasoning"));
  let avatar: AvatarConfig | undefined;
  if (bot.avatar !== undefined) {
    try { avatar = normalizeAvatar(bot.avatar); } catch { throw new Error(i18n.t("bots:package.invalid_avatar")); }
  }
  return {
    format: BOT_PACKAGE_FORMAT,
    version: BOT_PACKAGE_VERSION,
    included: ["bot_config"],
    bot: {
      name: bot.name.trim(),
      instructions: bot.instructions,
      model: bot.model,
      ...(avatar ? { avatar } : {}),
      ...(bot.reasoning_effort ? { reasoning_effort: bot.reasoning_effort } : {}),
    },
  };
}

function normalizeAvatar(value: unknown): AvatarConfig {
  if (!value || typeof value !== "object") throw new Error("invalid avatar");
  return normalizeConfig(value as Partial<AvatarConfig>);
}

export function downloadBotPackage(botPackage: PortableBotPackage) {
  const safeName = botPackage.bot.name.trim().replace(/[\\/:*?"<>|\u0000-\u001f]+/g, "-").slice(0, 80) || "tofi-bot";
  const blob = new Blob([`${JSON.stringify(botPackage, null, 2)}\n`], { type: "application/json" });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = `${safeName}.tofi-bot.json`;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
}
