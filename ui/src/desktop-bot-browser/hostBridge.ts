import { useSyncExternalStore } from "react";
import { currentLanguage, isLanguage, setLanguage } from "../i18n";

export type HostData = {
  active: string | null;
  url: string;
  targets: { target_id: string; url: string; title: string }[];
  phase: "idle" | "bot" | "human" | "stopped" | "closed";
  acting: boolean;
  loading: boolean;
  canGoBack: boolean;
  canGoForward: boolean;
  /** The chat page's language and theme: this page has its own storage partition, so it cannot read the person's choice. */
  look?: { lang?: string; theme?: string };
};

declare global {
  interface Window { renderHost: (data: HostData) => void }
}

let snapshot: HostData = {
  active: null, url: "", targets: [], phase: "closed",
  acting: false, loading: false, canGoBack: false, canGoForward: false,
};
const listeners = new Set<() => void>();
const subscribe = (listener: () => void) => {
  listeners.add(listener);
  return () => { listeners.delete(listener); };
};
const getSnapshot = () => snapshot;

// Register while the module evaluates, before React mounts or catalogs finish loading.
window.renderHost = data => {
  snapshot = data;
  const { lang, theme } = data.look ?? {};
  if (isLanguage(lang) && lang !== currentLanguage()) void setLanguage(lang);
  if (theme === "light" || theme === "dark") document.documentElement.dataset.themeFixed = document.documentElement.dataset.theme = theme;
  listeners.forEach(listener => listener());
};

export const useHostData = () => useSyncExternalStore(subscribe, getSnapshot, getSnapshot);

export const signal = (value: string) => {
  history.replaceState(null, "", location.pathname);
  location.hash = value;
};

// The one place a typed address becomes a URL: as typed, a host name over HTTPS, this machine or a private address over HTTP, else a web search.
export function addressURL(raw: string): string | null {
  const value = String(raw || "").trim();
  if (!value) return null;
  if (/^https?:\/\//i.test(value)) return value;
  if (/^(localhost|127\.\d+\.\d+\.\d+|10\.\d+\.\d+\.\d+|192\.168\.\d+\.\d+|172\.(1[6-9]|2\d|3[01])\.\d+\.\d+)(:\d+)?(\/\S*)?$/i.test(value)) return `http://${value}`;
  if (!/\s/.test(value) && /^[^/?#]+\.[a-z][a-z0-9-]+(:\d+)?([/?#]\S*)?$/i.test(value)) return `https://${value}`;
  return `https://www.google.com/search?q=${encodeURIComponent(value)}`;
}
