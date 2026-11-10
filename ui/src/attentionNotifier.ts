/**
 * The web outlet of server-side notifications (cloud-services 10, D78 phase 2).
 *
 * The rules (per-kind switches, already read, answered cards, merging, quiet, title, preview) are the server's:
 * GET /api/notifications?view=delivery (internal/app/attention_delivery.go). This module keeps only what the
 * device knows: whether the person is looking at the conversation, how to show an item, and where a click goes.
 *
 * The outlet is chosen by whether the Mac app's notification bridge exists, never by `isDesktop`:
 *  - bridge present: the Mac app's main process reads the view and notifies. This module reads nothing, never
 *    calls `new Notification`, and only reports the conversation in view through `bridge.setView`.
 *  - no bridge (a browser): this module reads the view on the 5 s workspace beat (no long poll: HTTP/1.1 gives
 *    an origin six connections) and shows each delivered item with `Notification`.
 * A server without delivery support (`/api/server-info` has no `notifications`) is named and never asked.
 */
import { useSyncExternalStore } from "react";
import { request } from "./api";
import type { DesktopNotificationsBridge } from "./desktop";

export type DeliveryItem = {
  id: number;
  kind: "reply" | "task_result" | "needs_you" | "spend_alert";
  detail: string;
  conversation_id?: string;
  bot_id?: string;
  message_seq: number | null;
  count: number;
  quiet: boolean;
  title: string;
  bot_name: string;
  preview: string;
  created_at: string;
};
export type DeliveryResponse = { items: DeliveryItem[]; next_after: number; language: string };
export type NotifierState = "starting" | "on" | "waiting_permission" | "server_unsupported" | "offline";

/** Rows older than this are not shown (a tab that slept through a night does not replay it). */
export const NOTIFICATION_MAX_AGE_MS = 10 * 60_000;

export type NotifierEnv = {
  /** GET /api/notifications?<query>. */
  read: (query: string) => Promise<DeliveryResponse>;
  serverInfo: () => Promise<{ notifications?: { delivery?: number } }>;
  bridge: () => DesktopNotificationsBridge | undefined;
  permission: () => NotificationPermission | "unsupported";
  show: (title: string, options: NotificationOptions, onClick: () => void) => void;
  /** The tab is visible and shows this conversation. */
  watching: (conversationId: string) => boolean;
  text: (item: DeliveryItem) => { title: string; body: string };
  open: (item: DeliveryItem) => void;
  now: () => number;
};

export type AttentionNotifier = {
  poll: () => Promise<void>;
  /** Forget the cursor (sign-out, another account): the next poll starts from the tail again. */
  reset: () => void;
  /** Report the conversation in view to the Mac app. A no-op in a browser. */
  setView: (conversationId: string | null) => void;
  state: () => NotifierState;
  subscribe: (listener: () => void) => () => void;
};

export function createAttentionNotifier(env: NotifierEnv): AttentionNotifier {
  let cursor: number | null = null;
  let supported: boolean | undefined;
  let busy = false;
  let state: NotifierState = "starting";
  let generation = 0;
  const listeners = new Set<() => void>();
  const set = (next: NotifierState) => { if (state !== next) { state = next; listeners.forEach(listener => listener()); } };

  async function poll() {
    if (busy || env.bridge()) return;
    if (env.permission() !== "granted") { cursor = null; set("waiting_permission"); return; }
    busy = true;
    const mine = generation;
    try {
      if (supported === undefined) {
        const info = await env.serverInfo();
        supported = Number(info.notifications?.delivery) >= 1;
      }
      if (!supported) { set("server_unsupported"); return; }
      if (cursor === null) {
        // Start at the tail: events from before this page are not shown.
        const tail = await env.read("view=delivery&tail=1");
        if (mine === generation) cursor = tail.next_after;
        set("on");
        return;
      }
      const result = await env.read(`view=delivery&after=${cursor}`);
      if (mine !== generation) return;
      cursor = Math.max(cursor, result.next_after);
      set("on");
      const at = env.now();
      for (const item of result.items) {
        const age = at - Date.parse(item.created_at);
        if (Number.isFinite(age) && age > NOTIFICATION_MAX_AGE_MS) continue;
        if (item.conversation_id && env.watching(item.conversation_id)) continue;
        const { title, body } = env.text(item);
        const tag = item.conversation_id ? `tofi-${item.conversation_id}` : `tofi-${item.kind}-${item.detail}`;
        // A quiet item replaces the banner of its conversation (same tag) without sound.
        env.show(title, { body, tag, ...(item.quiet ? { silent: true } : {}) }, () => env.open(item));
      }
    } catch {
      set("offline");
    } finally {
      busy = false;
    }
  }

  return {
    poll,
    reset() { generation++; cursor = null; busy = false; },
    setView(conversationId) { env.bridge()?.setView({ conversationId }); },
    state: () => state,
    subscribe(listener) { listeners.add(listener); return () => { listeners.delete(listener); }; },
  };
}

type Translate = (key: string, options?: Record<string, unknown>) => string;

/** Title and body of an item. `needs_you` and `spend_alert` words are fixed sentences; the server gives only the kind. */
export function deliveryText(item: DeliveryItem, t: Translate): { title: string; body: string } {
  const title = item.title || "Tofi";
  if (item.kind === "needs_you") return { title, body: t("chat:notifications.needs_you") };
  if (item.kind === "spend_alert") return { title, body: t(item.detail === "spend_cap" ? "chat:notifications.spend_cap" : "chat:notifications.spend_warning") };
  // A group's Bot is named; in a direct chat the title already is the Bot.
  const from = item.bot_name && item.bot_name !== item.title ? `${item.bot_name}: ` : "";
  const text = item.preview || t("chat:notifications.new_message");
  return { title, body: item.count > 1 ? `${t("chat:notifications.new_messages", { n: item.count })} · ${from}${text}` : `${from}${text}` };
}

// The settings page shows what the workspace's notifier last found (a server that cannot deliver is named there).
let published: NotifierState = "starting";
const publishedListeners = new Set<() => void>();
export function useBrowserNotifierState(): NotifierState {
  return useSyncExternalStore((listener) => { publishedListeners.add(listener); return () => { publishedListeners.delete(listener); }; }, () => published, () => "starting");
}

/** The workspace's notifier: the beat, the page and `Notification` wired to the pure core. */
export function browserNotifier(parts: { watching: (conversationId: string) => boolean; t: Translate; open: (item: DeliveryItem) => void }): AttentionNotifier {
  const notifier = createAttentionNotifier({
    read: (query) => request<DeliveryResponse>(`/api/notifications?${query}`),
    serverInfo: () => request("/api/server-info"),
    bridge: () => window.tofiDesktop?.notifications,
    permission: () => typeof window !== "undefined" && "Notification" in window ? Notification.permission : "unsupported",
    show: (title, options, onClick) => {
      const notice = new Notification(title, options);
      notice.onclick = () => { window.focus(); onClick(); notice.close(); };
    },
    watching: parts.watching,
    text: (item) => deliveryText(item, parts.t),
    open: parts.open,
    now: () => Date.now(),
  });
  notifier.subscribe(() => { published = notifier.state(); publishedListeners.forEach((listener) => listener()); });
  return notifier;
}

/** True when a Mac app notifies from its main process. */
export function notificationsOwnedByDesktop(): boolean { return Boolean(window.tofiDesktop?.notifications); }
