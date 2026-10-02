export type DesktopCommand = "new-conversation" | "search" | "settings" | "toggle-sidebar" | "focus-composer";
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

declare global {
  interface Window {
    tofiDesktop?: {
      platform: string;
      themePreference: "system" | "light" | "dark";
      onCommand: (listener: (command: DesktopCommand) => void) => () => void;
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
