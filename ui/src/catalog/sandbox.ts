// The component catalog's sandbox. It is the first module the catalog evaluates, before i18n or any product module, so:
// - storage is in memory: nothing a catalog page does reaches this origin's localStorage, where the real app (same
//   dev origin, often open in the next tab) keeps its theme, language, drafts and sidebar layout;
// - every /api and /health request is answered here from synthetic data (catalog/world.ts). Under `npm run dev:remote`
//   the dev server proxies /api to a real TOFI with the owner's session cookie, so a request that slipped through
//   would read or change real data. scripts/test-catalog.mjs fails if any /api request reaches the network;
// - the event streams, sockets and popups the product opens go nowhere.
// Dev only (docs/decisions/0034-component-catalog-dev-only.md): this file must never be imported by product code.

/** A string unique to the catalog. scripts/test-catalog.mjs fails if it appears anywhere in a production build. */
export const CATALOG_MARKER = "tofi-component-catalog-dev-only";

class MemoryStorage implements Storage {
  #items = new Map<string, string>();
  get length() { return this.#items.size; }
  clear() { this.#items.clear(); }
  getItem(key: string) { return this.#items.get(String(key)) ?? null; }
  key(index: number) { return [...this.#items.keys()][index] ?? null; }
  removeItem(key: string) { this.#items.delete(String(key)); }
  setItem(key: string, value: string) { this.#items.set(String(key), String(value)); }
}

export type Theme = "light" | "dark";
const params = new URLSearchParams(location.search);
export const frameParams = {
  /** Set in a frame (an iframe the shell renders, or a page a test opens): the section to render. */
  section: params.get("frame") ?? "",
  state: params.get("state") ?? "",
  theme: (params.get("theme") === "dark" ? "dark" : "light") as Theme,
  language: params.get("lng") ?? "en",
};

for (const name of ["localStorage", "sessionStorage"] as const) {
  Object.defineProperty(window, name, { value: new MemoryStorage(), configurable: true });
}
// Read by i18n at module load and by the boot screen before the app applies its appearance.
window.localStorage.setItem("tofi.language", frameParams.language);
window.localStorage.setItem("tofi:appearance", frameParams.theme);
document.documentElement.dataset.theme = frameParams.theme;
document.documentElement.style.colorScheme = frameParams.theme;

export type Synthetic = (url: URL, init: RequestInit | undefined) => Response | Promise<Response>;
let answer: Synthetic = () => json({});
/** The frame's synthetic server. Unknown reads answer `{}`; writes answer `{}` too and change nothing anywhere. */
export function setSynthetic(next: Synthetic) { answer = next; }

export function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });
}

const realFetch = window.fetch.bind(window);
window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
  const url = new URL(typeof input === "string" ? input : input instanceof URL ? input.href : input.url, location.href);
  if (url.origin !== location.origin) throw new TypeError(`catalog: request to ${url.origin} refused`);
  if (url.pathname.startsWith("/api/") || url.pathname === "/health") {
    const signal = init?.signal ?? (input instanceof Request ? input.signal : undefined);
    if (signal?.aborted) throw new DOMException("Aborted", "AbortError");
    const method = (init?.method ?? (input instanceof Request ? input.method : "GET")).toUpperCase();
    const result = answer(url, { ...init, method });
    // A synthetic answer that never settles models a slow server; it still honours the caller's abort.
    return await new Promise<Response>((resolve, reject) => {
      signal?.addEventListener("abort", () => reject(new DOMException("Aborted", "AbortError")), { once: true });
      Promise.resolve(result).then(resolve, reject);
    });
  }
  return realFetch(input, init);
};

type Listener = (event: MessageEvent<string>) => void;
/** Event streams never connect. `window.__catalogEmit(type, data)` delivers a frame to every open stream. */
class SyntheticEventSource extends EventTarget {
  static readonly CONNECTING = 0; static readonly OPEN = 1; static readonly CLOSED = 2;
  readonly CONNECTING = 0; readonly OPEN = 1; readonly CLOSED = 2;
  static open = new Set<SyntheticEventSource>();
  readyState = 1;
  readonly withCredentials = false;
  onopen: ((event: Event) => void) | null = null;
  onmessage: Listener | null = null;
  onerror: ((event: Event) => void) | null = null;
  constructor(readonly url: string) {
    super();
    SyntheticEventSource.open.add(this);
    setTimeout(() => this.onopen?.(new Event("open")), 0);
  }
  close() { this.readyState = 2; SyntheticEventSource.open.delete(this); }
}
let eventId = 1000;
Object.assign(window, {
  EventSource: SyntheticEventSource,
  WebSocket: class { constructor() { throw new Error("catalog: sockets are disabled"); } },
  open: () => null,
  __catalogEmit(type: string, data: unknown) {
    eventId += 1;
    for (const source of SyntheticEventSource.open) source.dispatchEvent(new MessageEvent(type, { data: JSON.stringify(data), lastEventId: String(eventId) }));
  },
});
