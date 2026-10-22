import { lazy, Suspense, useSyncExternalStore } from "react";
import { proxyChallenged, subscribeProxyChallenge } from "./proxyChallenge";

const Banner = lazy(() => import("./ProxyChallengeNotice").then((m) => ({ default: m.ProxyChallengeBanner })));
const read = () => proxyChallenged;

/** True while a proxy challenge blocks requests; the app hides its per-request error boxes meanwhile. */
export const useProxyChallenge = () => useSyncExternalStore(subscribeProxyChallenge, read, read);

/** Mounted once next to the app; costs the entry bundle only this wrapper, the banner is its own chunk. */
export function ProxyChallengeSlot() {
  return useProxyChallenge() ? <Suspense fallback={null}><Banner /></Suspense> : null;
}
