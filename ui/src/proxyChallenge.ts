/**
 * A reverse proxy in front of TOFI (Cloudflare's managed challenge) can answer a background fetch with an HTML
 * "Just a moment..." page once its clearance cookie expires. That is not a TOFI error: proxyChallengeRecover.ts names
 * it (`proxy_challenge`), tries once to renew the cookie in a hidden same-origin frame and retries once; otherwise this
 * flag goes up and one notice (ProxyChallengeSlot) shows instead of an error box per request.
 * deploy/self-host/README.md, "Behind Cloudflare". No React here: the lazy chunk shares this file.
 */
const EVENT = "tofi:proxy-challenge";

export let proxyChallenged = false;

/** Raised when a request is blocked, cleared when any request gets through again. The catalog raises it by dispatching the event. */
export function setProxyChallenge(next: boolean) {
  if (proxyChallenged !== next) window.dispatchEvent(new CustomEvent(EVENT, { detail: next }));
  proxyChallenged = next;
}

export function subscribeProxyChallenge(onChange: () => void) {
  const sync = (event: Event) => { proxyChallenged = (event as CustomEvent<boolean>).detail; onChange(); };
  window.addEventListener(EVENT, sync);
  return () => window.removeEventListener(EVENT, sync);
}
