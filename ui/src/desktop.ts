export type DesktopCommand = "new-conversation" | "search" | "settings" | "toggle-sidebar" | "focus-composer"
  /** A system notification was clicked: show this conversation. `seq` is sent but not used yet. */
  | { type: "open-conversation"; id: string; seq?: number }
  /** A spend notification without a conversation was clicked: open Settings > Usage. */
  | { type: "open-usage"; detail?: string };

/** What the Mac app's notifier reports about itself (cloud-services 10.3 item 7). Only the Mac app writes these words. */
export type DesktopNotifierState = "on" | "off" | "not_connected" | "server_unsupported" | "signed_out" | "offline" | "blocked_by_system";
/** The three device-level switches; they live in the Mac app, never on the server. */
export type DesktopNotifierSettings = { enabled: boolean; content: boolean; openAtLogin: boolean };
export type DesktopNotifierStatus = {
  state: DesktopNotifierState;
  /** Why the Mac app is in this state (`connecting`, `too_many_waiters`, ...). Optional; unknown values are ignored. */
  reason?: string;
  /** Epoch milliseconds of the last successful read, when the state is offline. */
  lastSuccessAt?: number;
  settings: DesktopNotifierSettings;
};
/**
 * The notification bridge. When it exists the Mac app's main process reads the delivery view and shows the
 * notifications (contracts/notifications-delivery.md); the page never calls `new Notification` and never reads the view.
 */
export type DesktopNotificationsBridge = {
  status: () => Promise<DesktopNotifierStatus>;
  update: (patch: Partial<DesktopNotifierSettings>) => Promise<DesktopNotifierStatus>;
  /** The conversation in view (null: none). The main process decides whether the window is focused. */
  setView: (view: { conversationId: string | null }) => void;
  onStatus: (listener: (status: DesktopNotifierStatus) => void) => () => void;
};
export type DesktopState = { drafts?: unknown; activeConversation?: string; readingPositions?: Record<string, unknown>; sidebarCollapsed?: boolean };
export type NativeFile = { handle: string; name: string };
export type DesktopPermissionKind = "microphone" | "screen" | "accessibility";
export type DesktopPermissionStatus = "granted" | "denied" | "restricted" | "not-determined" | "unknown";
export type DesktopPermissionSnapshot = {
  supported: boolean;
  microphone: DesktopPermissionStatus;
  screen: DesktopPermissionStatus;
  accessibility: boolean;
};

/**
 * Remote control this Mac (environments-simplify.md): the one switch that replaces the pairing code. The Mac app's main
 * process answers every method to workspace documents only and shows the native confirmation itself; the page only asks.
 * A failure carries a named `code`, never the server's text, and nothing here ever carries a pairing code or token.
 */
export type RemoteControlFailure = "no_session" | "personal_disabled" | "rate_limited" | "limit_reached" | "connection_changed" | "keychain_unavailable" | "offline" | "no_connection" | "permission_missing" | "failed";
export type RemoteControlReason = RemoteControlFailure | "half_on" | "needs_accessibility" | "needs_screen" | "needs_both" | "needs_relaunch" | "connecting" | "paused_stop" | "paused_takeover" | "revoked" | "removal_pending";
export type RemoteControlStatus = {
  enabled: boolean; paired: boolean; deviceID: string | null; name: string;
  permissions: { screen: boolean; accessibility: boolean }; needsRelaunch: boolean;
  phase: "idle" | "active" | "lingering" | "paused"; botName: string; pausedUntil: number | null; hotkeyOk: boolean;
  reason: RemoteControlReason | null;
};
export type RemoteControlResult = { ok: true; status: RemoteControlStatus } | { ok: false; code: RemoteControlFailure | "cancelled" | "off" | "not_needed"; status: RemoteControlStatus };
export type RemoteControlBridge = {
  status: () => Promise<RemoteControlStatus>;
  /** Turning on shows the macOS confirmation; a cancel answers `{ok:false, code:"cancelled"}` and changes nothing. */
  set: (on: boolean) => Promise<RemoteControlResult>;
  authorize: () => Promise<RemoteControlResult>;
  stop: () => Promise<RemoteControlResult>;
  relaunch: () => Promise<RemoteControlResult>;
  /** Bots the person denied on this Mac before; a local veto. */
  deniedBots: () => Promise<{ ok: true; bots: { bot_id: string; bot_name: string }[] }>;
  /** Lifting a veto also asks in a native dialog. */
  undoDeny: (botID: string) => Promise<{ ok: boolean; code?: string; bots: { bot_id: string; bot_name: string }[] }>;
  onStatus: (listener: (status: RemoteControlStatus) => void) => () => void;
};
export type DesktopSandboxStatus = {
  enabled?: boolean; root?: string | null; connectionID?: string | null; bound?: boolean;
  available?: boolean; reason?: string; reasonCode?: string; faulted?: boolean; running?: boolean;
};
/** What the Mac settings page reads: the device bridge status. Only the privileged settings page may call it. */
export type DesktopDeviceStatus = {
  name: string; workspaceRoot: string; fileAccess: boolean; screenCapture?: boolean; desktopControl?: boolean;
  paired: boolean; deviceID?: string | null; capabilities?: string[];
  permissions?: { screen?: boolean; accessibility?: boolean };
  sandbox: DesktopSandboxStatus;
  lastError?: string | null;
  control?: { phase?: "idle" | "active" | "lingering" | "paused" | string; reason?: string };
};
export type DesktopSettingsInput = { name?: string; workspaceRoot?: string; fileAccess?: boolean };
export type DesktopSandboxSelection = { selectionID: string; root: string; serverLabel: string };

export type BrowserSettings = {
  botBrowserEnabled: boolean; botBrowserRetainLogin: boolean; botBrowserShareFrames: boolean;
};
export type BrowserStatus = {
  supported: boolean; code: string | null; deviceID: string | null;
  phase: 'idle'|'bot'|'human'|'stopped'|'closed'; settings: BrowserSettings;
};
export type BrowserResult = {ok: boolean; code: string | null; status: BrowserStatus};
export type BrowserPanelState = {open: boolean; width: number};
export type BotBrowserBridge = {
  panel(open: boolean): Promise<void>;
  onPanel(listener: (state: BrowserPanelState) => void): () => void;
  panelState(): Promise<BrowserPanelState>;
  status(): Promise<BrowserStatus>;
  update(patch: Partial<BrowserSettings>): Promise<BrowserResult>;
  clear(): Promise<BrowserResult>;
  open(): Promise<BrowserResult>;
  onStatus(listener: (status: BrowserStatus) => void): () => void;
};

export type AppUpdateStatus = {
  state: "idle" | "checking" | "available" | "downloading" | "downloaded" | "waiting_idle" | "installing" | "error";
  version: string | null;
};
export type AppUpdateBridge = {
  status(): Promise<AppUpdateStatus>;
  onStatus(listener: (status: AppUpdateStatus) => void): () => void;
  install(): Promise<void>;
};

declare global {
  interface Window {
    tofiDesktop?: {
      platform: string;
      themePreference: "system" | "light" | "dark";
      onCommand: (listener: (command: DesktopCommand) => void) => () => void;
      /** Present in a Mac app that notifies from its main process. Its existence picks the outlet (attentionNotifier.ts). */
      notifications?: DesktopNotificationsBridge;
      appUpdate?: AppUpdateBridge;
      serverAddress?: () => Promise<string | null>;
      /** Present in a Mac app that has the one-switch remote control. Without it (a browser, an older app) the page shows no switch. */
      remoteControl?: RemoteControlBridge;
      botBrowser?: BotBrowserBridge;
      onWindowState: (listener: (state: { focused: boolean; fullscreen: boolean }) => void) => () => void;
      appearance: (preference: "light" | "dark" | "system") => Promise<void>;
      requestMicrophoneAccess: () => Promise<boolean>;
      getPermissionStatus: () => Promise<DesktopPermissionSnapshot>;
      requestPermission: (kind: DesktopPermissionKind) => Promise<boolean>;
      openPrivacySettings: (kind: DesktopPermissionKind) => Promise<boolean>;
      startAccessibilityDrag: () => void;
      remoteInputFocus: (focused: boolean) => void;
      onFlushState: (listener: () => Promise<void> | void) => () => void;
      readWorkspaceState: (instance: string) => Promise<DesktopState>;
      authorizeMCP?: (name: string) => Promise<{ ok: boolean }>;
      cancelMCPAuthorization?: (name: string) => Promise<void>;
      writeWorkspaceState: (instance: string, state: DesktopState) => Promise<void>;
      prepareAttachment: (id: string) => Promise<NativeFile>;
      saveAttachment: (handle: string) => Promise<boolean>;
      revealAttachment: (handle: string) => Promise<void>;
      dragAttachment: (handle: string) => void;
      /** Workspace pages only: navigates the window to the privileged Mac settings page. */
      openDesktopSettings?: () => Promise<void>;
      // Privileged methods: the main process answers them only to the Mac settings page (/__desktop/settings).
      getSettings?: () => Promise<DesktopDeviceStatus>;
      updateSettings?: (settings: DesktopSettingsInput) => Promise<DesktopDeviceStatus>;
      chooseWorkspace?: () => Promise<string | null>;
      openPermissionGuide?: (kind: "screen" | "accessibility") => Promise<boolean>;
      controlStop?: () => Promise<DesktopDeviceStatus>;
      controlResume?: () => Promise<DesktopDeviceStatus>;
      /** Quits and reopens Tofi (Mac settings page only); macOS applies a new Screen Recording grant to a fresh process. */
      relaunch?: () => Promise<boolean>;
      chooseSandboxRoot?: () => Promise<DesktopSandboxSelection | null>;
      authorizeSandboxRoot?: (selectionID: string) => Promise<DesktopDeviceStatus | null>;
      sandboxRevoke?: () => Promise<DesktopDeviceStatus | null>;
    };
  }
}

let instanceID = "";
let state: DesktopState = {};
let flushTimer: number | undefined;
let hydrationGeneration = 0;
let hydration: { instance: string; promise: Promise<void> } | undefined;
export const isDesktop = Boolean(window.tofiDesktop);

export function hydrateDesktopState(instance: string): Promise<void> {
  const api = window.tofiDesktop;
  if (!api || instanceID === instance) return Promise.resolve();
  if (hydration?.instance === instance) return hydration.promise;
  // Preserve the pending final edit under its original instance. The preload's
  // document context still decides whether that write is authorized.
  if (flushTimer !== undefined) void flushDesktopState();
  const generation = ++hydrationGeneration;
  instanceID = "";
  state = {};
  const promise = Promise.resolve().then(() => api.readWorkspaceState(instance)).then(next => {
    if (generation !== hydrationGeneration) return;
    state = next;
    instanceID = instance;
  }, () => {
    // A failed/stale native read must not publish an empty writable snapshot.
    // Leave the instance unhydrated so a later request can retry.
  }).finally(() => {
    if (generation === hydrationGeneration) hydration = undefined;
  });
  hydration = { instance, promise };
  return promise;
}
export function desktopState() { return state; }
export function updateDesktopState(patch: Partial<DesktopState>) {
  if (!window.tofiDesktop || !instanceID) return;
  state = { ...state, ...patch };
  window.clearTimeout(flushTimer);
  const generation = hydrationGeneration;
  flushTimer = window.setTimeout(() => {
    if (generation === hydrationGeneration) return flushDesktopState();
  }, 180);
}
export function flushDesktopState() {
  window.clearTimeout(flushTimer);
  flushTimer = undefined;
  if (instanceID) return window.tofiDesktop?.writeWorkspaceState(instanceID, state).catch(() => {});
}

if (isDesktop) {
  document.documentElement.dataset.desktop = window.tofiDesktop!.platform;
  window.tofiDesktop!.onFlushState(flushDesktopState);
  window.tofiDesktop!.onWindowState(({ focused, fullscreen }) => {
    document.documentElement.dataset.windowFocused = String(focused);
    document.documentElement.dataset.fullscreen = String(fullscreen);
  });
  window.addEventListener("pagehide", flushDesktopState);
  // Native menu accelerators must not swallow keys intended for the remote
  // desktop or terminal. The focused remote surface owns its keyboard input.
  const updateRemoteFocus = () => window.tofiDesktop!.remoteInputFocus(Boolean(document.activeElement?.closest(".remote-desktop-surface, .remote-desktop.in-control, .xterm")));
  document.addEventListener("focusin", updateRemoteFocus);
  document.addEventListener("focusout", () => queueMicrotask(updateRemoteFocus));
  window.addEventListener("focus", updateRemoteFocus);
  window.addEventListener("pagehide", () => window.tofiDesktop!.remoteInputFocus(false));
  document.addEventListener("visibilitychange", () => { if (document.hidden) flushDesktopState(); });
  // Prevent a Finder drop outside the composer from navigating the shell away.
  window.addEventListener("dragover", event => { if (event.dataTransfer?.types.includes("Files")) event.preventDefault(); });
  window.addEventListener("drop", event => { if (event.dataTransfer?.types.includes("Files")) event.preventDefault(); });
}
