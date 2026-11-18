import { catalog, normalizeConfig, paletteOptions, type AvatarConfig } from "./lib/tofi-avatar/index.js";

const STORAGE_PREFIX = "tofi:bot-avatar:";
const AVATAR_CHANGE_EVENT = "tofi:avatar-change";
const GROUND_PREFIX = "tofi:bot-ground:";
const GROUND_CHANGE_EVENT = "tofi:ground-change";

/** The colours a Bot's ground may take (avatar schemaVersion 2). The server list is avatarGrounds in internal/app/avatar.go. */
export const GROUNDS = ["tomato", "mustard", "teal", "grape", "lime"] as const;
export type Ground = (typeof GROUNDS)[number];
export function isGround(value: unknown): value is Ground {
  return typeof value === "string" && (GROUNDS as readonly string[]).includes(value);
}

function hash(value: string) {
  let result = 2166136261;
  for (let index = 0; index < value.length; index += 1) result = Math.imul(result ^ value.charCodeAt(index), 16777619);
  return result >>> 0;
}

function chooseConfig(seed: number): AvatarConfig {
  const shape = catalog.shapes[seed % catalog.shapes.length].id;
  const pattern = catalog.patterns[(seed >>> 4) % catalog.patterns.length].id;
  const palettes = paletteOptions(pattern);
  return normalizeConfig({ shape, pattern, palette: palettes[(seed >>> 8) % palettes.length].id });
}

/** The colour a Bot with none stored gets: the same id gives the same ground on every read. It is never stored. */
export function defaultBotGround(botId: string): Ground {
  return GROUNDS[hash(botId) % GROUNDS.length];
}

/** The ground this browser holds for the Bot (a copy of the server's), or undefined when none was ever chosen. */
export function getStoredBotGround(botId: string): Ground | undefined {
  try {
    const stored = localStorage.getItem(`${GROUND_PREFIX}${botId}`);
    if (isGround(stored)) return stored;
  } catch { /* storage unavailable: the default applies */ }
  return undefined;
}

export function getBotGround(botId: string): Ground {
  return getStoredBotGround(botId) ?? defaultBotGround(botId);
}

export function saveBotGround(botId: string, ground: Ground) {
  try { localStorage.setItem(`${GROUND_PREFIX}${botId}`, ground); } catch { /* local persistence is optional */ }
  window.dispatchEvent(new CustomEvent(GROUND_CHANGE_EVENT, { detail: { botId, ground } }));
  return ground;
}

export function subscribeBotGround(botId: string, listener: (ground: Ground) => void) {
  const handleChange = (event: Event) => {
    const custom = event as CustomEvent<{ botId?: string; ground?: Ground }>;
    if (custom.detail?.botId === botId && custom.detail.ground) listener(custom.detail.ground);
  };
  const handleStorage = (event: StorageEvent) => {
    if (event.key === `${GROUND_PREFIX}${botId}` && isGround(event.newValue)) listener(event.newValue);
  };
  window.addEventListener(GROUND_CHANGE_EVENT, handleChange);
  window.addEventListener("storage", handleStorage);
  return () => { window.removeEventListener(GROUND_CHANGE_EVENT, handleChange); window.removeEventListener("storage", handleStorage); };
}

/**
 * Reads a look from an export, a Bot package or the server: schemaVersion 1 (no ground) or 2 (with one). The drawing library only knows
 * version 1, so the ground is split off. A ground outside GROUNDS is refused, like any other invalid field.
 */
export function parseLook(value: unknown): { config: AvatarConfig; ground?: Ground } {
  if (!value || typeof value !== "object") throw new Error("invalid avatar");
  const { schemaVersion, ground, ...rest } = value as Record<string, unknown>;
  if (schemaVersion !== undefined && schemaVersion !== 1 && schemaVersion !== 2) throw new Error("invalid avatar schemaVersion");
  if (ground !== undefined && !isGround(ground)) throw new Error("invalid avatar ground");
  return { config: normalizeConfig({ ...rest, schemaVersion: 1 } as Partial<AvatarConfig>), ...(ground ? { ground } : {}) };
}

/** What an export writes for a Bot: the look, as version 2 with the ground when this Bot has a stored one. */
export function exportBotLook(botId: string): Record<string, unknown> {
  const config = getBotAvatarConfig(botId);
  const ground = getStoredBotGround(botId);
  return ground ? { ...config, schemaVersion: 2, ground } : { ...config };
}

export function getBotAvatarConfig(botId: string): AvatarConfig {
  try {
    const stored = localStorage.getItem(`${STORAGE_PREFIX}${botId}`);
    if (stored) return normalizeConfig(JSON.parse(stored) as Partial<AvatarConfig>);
  } catch {
    // Fall through to a stable generated appearance if local data is invalid.
  }
  return chooseConfig(hash(botId));
}

export function saveBotAvatarConfig(botId: string, value: Partial<AvatarConfig>) {
  const config = normalizeConfig(value);
  try { localStorage.setItem(`${STORAGE_PREFIX}${botId}`, JSON.stringify(config)); } catch { /* local persistence is optional */ }
  window.dispatchEvent(new CustomEvent(AVATAR_CHANGE_EVENT, { detail: { botId, config } }));
  return config;
}

export function randomBotAvatarConfig(botId: string) {
  const seed = (Math.random() * 0xffffffff) >>> 0;
  const config = chooseConfig(seed ^ hash(botId));
  return saveBotAvatarConfig(botId, config);
}

export function subscribeBotAvatar(botId: string, listener: (config: AvatarConfig) => void) {
  const handleChange = (event: Event) => {
    const custom = event as CustomEvent<{ botId?: string; config?: AvatarConfig }>;
    if (custom.detail?.botId === botId && custom.detail.config) listener(custom.detail.config);
  };
  const handleStorage = (event: StorageEvent) => {
    if (event.key !== `${STORAGE_PREFIX}${botId}` || !event.newValue) return;
    try { listener(normalizeConfig(JSON.parse(event.newValue) as Partial<AvatarConfig>)); } catch { /* Ignore stale local data. */ }
  };
  window.addEventListener(AVATAR_CHANGE_EVENT, handleChange);
  window.addEventListener("storage", handleStorage);
  return () => { window.removeEventListener(AVATAR_CHANGE_EVENT, handleChange); window.removeEventListener("storage", handleStorage); };
}
