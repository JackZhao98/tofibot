import { useSyncExternalStore } from "react";

/**
 * "Install app" state for the account menu. Chrome and Edge fire `beforeinstallprompt` once, early, so the listener is
 * installed from main.tsx (this file stays tiny) and the lazy account menu reads the captured event later. Safari on
 * iOS has no such event; there the menu shows a how-to sheet. Nothing here touches a service worker: the app has none.
 */
type InstallPromptEvent = Event & { prompt: () => Promise<void>; userChoice: Promise<{ outcome: "accepted" | "dismissed" }> };
export type InstallKind = "none" | "prompt" | "ios";

let deferred: InstallPromptEvent | null = null;
let standalone = false;
const listeners = new Set<() => void>();
const emit = () => listeners.forEach(listener => listener());

export function isStandalone() {
  if (typeof window === "undefined") return false;
  return window.matchMedia?.("(display-mode: standalone)").matches === true || (navigator as Navigator & { standalone?: boolean }).standalone === true;
}

export function isIosBrowser() {
  if (typeof navigator === "undefined") return false;
  return /iPad|iPhone|iPod/.test(navigator.userAgent) || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
}

/** Start listening. Call once at startup. */
export function watchInstallPrompt() {
  if (typeof window === "undefined") return;
  standalone = isStandalone();
  window.addEventListener("beforeinstallprompt", event => { event.preventDefault(); deferred = event as InstallPromptEvent; emit(); });
  window.addEventListener("appinstalled", () => { deferred = null; standalone = true; emit(); });
  window.matchMedia?.("(display-mode: standalone)").addEventListener?.("change", () => { standalone = isStandalone(); emit(); });
}

function kind(): InstallKind {
  if (standalone) return "none";
  if (deferred) return "prompt";
  return isIosBrowser() ? "ios" : "none";
}

function subscribe(listener: () => void) { listeners.add(listener); return () => { listeners.delete(listener); }; }

export function useInstallKind(): InstallKind { return useSyncExternalStore(subscribe, kind, () => "none"); }

/** Shows the browser's own install prompt. Resolves true when the person accepted. */
export async function promptInstall(): Promise<boolean> {
  const event = deferred;
  if (!event) return false;
  deferred = null; emit(); // The event is single use.
  await event.prompt();
  return (await event.userChoice).outcome === "accepted";
}
