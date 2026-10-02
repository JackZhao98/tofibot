import { catalog, normalizeConfig, paletteOptions, type AvatarConfig } from "./lib/tofi-avatar/index.js";

const STORAGE_PREFIX = "tofi:bot-avatar:";
const AVATAR_CHANGE_EVENT = "tofi:avatar-change";

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
