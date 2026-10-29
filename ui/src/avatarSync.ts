
/**
 * A Bot's look lives on the server, so it follows the person to every device and a Bot can change it with the settings tool. The
 * browser keeps a copy (avatarStore, key `tofi:bot-avatar:<id>`) for the first paint. This module is the network side, a lazy chunk.
 * It reads and writes the copy by the same key and announces a change by the same event as avatarStore, and imports nothing from it,
 * so the entry does not grow and the bundle keeps its shape (test:avatar-store keeps the two constants equal).
 */
const LOCAL_PREFIX = "tofi:bot-avatar:";
const CHANGE_EVENT = "tofi:avatar-change";
const pushed = new Set<string>();

/** The app's `request` (api.ts). It is handed in: a lazy chunk that imports api.ts itself reshuffles the entry bundle (test:shell-budget). */
export type Send = (path: string, init: { method: string; body: string }) => Promise<unknown>;

type Look = { schemaVersion?: number; shape?: string; pattern?: string; palette?: string };
type ServerBot = { id: string; avatar?: Look | null };

/** A look is saved on the server. A refusal or no connection leaves the copy; the next load hands it up again. */
export function pushBotAvatar(send: Send, botId: string, look: Look): void {
  if (pushed.has(botId)) return;
  pushed.add(botId);
  void send(`/api/bots/${encodeURIComponent(botId)}`, { method: "PATCH", body: JSON.stringify({ avatar: look }) }).catch(() => pushed.delete(botId));
}

/**
 * Brings this browser in line with the server. A Bot with a stored look gives it to every device. A Bot with none, whose look this
 * browser still holds from before the server kept it, hands it up once (the first load after the update): the person keeps what they chose.
 */
export function hydrateBotAvatars(send: Send, bots: readonly ServerBot[]): void {
  for (const bot of bots) {
    let held: string | null = null;
    try { held = localStorage.getItem(`${LOCAL_PREFIX}${bot.id}`); } catch { /* storage unavailable: the server's look still applies after a reload */ }
    if (bot.avatar) {
      const config = { schemaVersion: 1, shape: bot.avatar.shape, pattern: bot.avatar.pattern, palette: bot.avatar.palette };
      const next = JSON.stringify(config);
      let same = false;
      try { const local = held && (JSON.parse(held) as Look); same = !!local && local.shape === config.shape && local.pattern === config.pattern && local.palette === config.palette; } catch { /* damaged copy: replaced below */ }
      if (same) continue;
      try { localStorage.setItem(`${LOCAL_PREFIX}${bot.id}`, next); } catch { /* optional */ }
      window.dispatchEvent(new CustomEvent(CHANGE_EVENT, { detail: { botId: bot.id, config } }));
    } else if (held) {
      try { pushBotAvatar(send, bot.id, JSON.parse(held) as Look); } catch { /* damaged copy: the generated look stays */ }
    }
  }
}
