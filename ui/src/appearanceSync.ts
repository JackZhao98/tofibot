
/**
 * The theme lives on the server, so it follows the person to every device and a Bot can change it with the settings tool. The browser
 * keeps a copy (`tofi:appearance`) for the first paint; every `useAppearance` in the page listens for the storage event this writes.
 * A lazy chunk: the entry only reads the copy.
 */
const KEY = "tofi:appearance";
/** The app's `request` (api.ts), handed in: a lazy chunk that imports api.ts itself reshuffles the entry bundle (test:shell-budget). */
export type Send = <T>(path: string, init?: { method: string; body: string }) => Promise<T>;
const REFRESH_MS = 30_000;
type Theme = "system" | "light" | "dark";
const valid = (value: unknown): value is Theme => value === "system" || value === "light" || value === "dark";

function localTheme(): Theme | null {
  try { const value = localStorage.getItem(KEY); return valid(value) ? value : null; } catch { return null; }
}

let applying = false;
/** Puts a theme in this browser the way a choice made here would: every open `useAppearance` follows. It is not pushed back. */
export function applyTheme(value: Theme): void {
  applying = true;
  try { localStorage.setItem(KEY, value); window.dispatchEvent(new StorageEvent("storage", { key: KEY, newValue: value })); } catch { /* the page keeps its in-memory choice */ } finally { applying = false; }
}

/** A choice made here goes to the server. A refusal or a missing sign-in changes nothing: the copy stays and the next pull settles it. */
export function pushTheme(send: Send, value: Theme): Promise<void> {
  return send("/api/preferences", { method: "PUT", body: JSON.stringify({ appearance: value }) }).then(() => undefined, () => undefined);
}

/** Reads the server's theme. A chosen theme wins over this browser's copy; with none chosen, a choice this browser holds is handed up once. */
export async function pullTheme(send: Send): Promise<void> {
  let server: unknown;
  try { server = (await send<{ appearance?: string }>("/api/preferences")).appearance; } catch { return; }
  const local = localTheme();
  if (valid(server)) { if ((local ?? "system") !== server) applyTheme(server); return; }
  if (local && local !== "system") await pushTheme(send, local);
}

let started = false;
/** Pulls now and again when the window gets attention (at most every 30 s), so a change on another device shows up on its own. */
export function startThemeSync(send: Send): void {
  if (started) return;
  started = true;
  let last = Date.now();
  void pullTheme(send);
  // A choice made on this page (`useAppearance.choose` writes the copy and announces it) goes to the server. A theme this module applied does not.
  window.addEventListener("storage", event => { if (event.key === KEY && !applying && valid(event.newValue)) void pushTheme(send, event.newValue); });
  window.addEventListener("focus", () => { if (Date.now() - last >= REFRESH_MS) { last = Date.now(); void pullTheme(send); } });
}
