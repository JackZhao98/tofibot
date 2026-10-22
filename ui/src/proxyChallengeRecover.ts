/** Everything about a proxy challenge except the shared flag (proxyChallenge.ts): classification, silent renewal, the one retry. Loaded on demand by api.ts, only after a 403/503. */
import { setProxyChallenge } from "./proxyChallenge";

const RECOVER_MS = 10_000;
/** After a failed renewal, requests fail fast for this long instead of each loading a frame. */
const COOLDOWN_MS = 30_000;

/** 403/503 carrying Cloudflare's `cf-mitigated` header, or an HTML body on an /api/ path that should answer JSON. A JSON 403 (TOFI's own `csrf`) is not a challenge. */
export function isProxyChallenge(path: string, response: Response): boolean {
  if (response.status !== 403 && response.status !== 503) return false;
  return response.headers.has("cf-mitigated") || (path.startsWith("/api/") && (response.headers.get("content-type") ?? "").includes("text/html"));
}

let recovery: Promise<boolean> | null = null;
let failedAt = 0;

/** One shared attempt: a hidden frame loads a cheap same-origin URL so the challenge can run and set a fresh cookie. */
export function recoverFromChallenge(): Promise<boolean> {
  if (recovery) return recovery;
  if (Date.now() - failedAt < COOLDOWN_MS) return Promise.resolve(false);
  recovery = new Promise<boolean>((resolve) => {
    const frame = document.createElement("iframe");
    frame.hidden = true;
    frame.setAttribute("aria-hidden", "true");
    frame.tabIndex = -1;
    const finish = (ok: boolean) => {
      window.clearTimeout(timer);
      frame.remove();
      if (!ok) failedAt = Date.now();
      resolve(ok);
    };
    const timer = window.setTimeout(() => finish(false), RECOVER_MS);
    // The challenge page loads first; the frame has passed once the real JSON answer replaces it.
    frame.addEventListener("load", () => { try { if (frame.contentDocument && !frame.contentDocument.contentType.includes("html")) finish(true); } catch { /* unreadable: keep waiting */ } });
    frame.src = "/api/server-info";
    document.body.append(frame);
  }).finally(() => { recovery = null; });
  return recovery;
}

/** For a 403/503 `response`: the response to carry on with (the original when it is not a challenge, the retry after a renewal), or null when the challenge persists and the notice is up. */
export async function settle(path: string, response: Response, send: () => Promise<Response>): Promise<Response | null> {
  if (!isProxyChallenge(path, response)) return response;
  await recoverFromChallenge();
  const retry = await send();
  if (retry.ok || !isProxyChallenge(path, retry)) return retry;
  setProxyChallenge(true);
  return null;
}
