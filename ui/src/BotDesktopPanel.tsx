import { useDebugMode } from "./debugMode";
import { useEffect, useRef, useState } from "react";
import { api, ApiError } from "./api";
import { GazeAvatar } from "./GazeAvatar";
import { DesktopPointerMarker } from "./DesktopPointerMarker";
import { isDesktop } from "./desktop";
import { useDesktopPresence, type DesktopPresenceState } from "./desktopPresence";
import "./desktop-presence.css";
import { DesktopVideo, type DesktopVideoState } from "./DesktopVideo";
import { TofiIcon } from "./icons";
import { RemoteDesktopControl, type RemoteControlHandle } from "./RemoteDesktopControl";

type Props = { autoConnect?: boolean; presence?: DesktopPresenceState; botId: string; botName: string; members?: Array<{ id: string; name: string }>; onClose: (reason?: "hide" | "shutdown") => void; onReadyChange?: (ready: boolean) => void; onExpandedChange?: (expanded: boolean) => void };
type ComputerInfo = { kind: string; state: string; phase?: string; workspace_root?: string; browser?: string; error?: string };
type ViewerState = "disconnected" | "connecting" | "connected";
type FrameStatus = "shown" | "busy" | "not-running" | "failed";

export function computerErrorText(error: unknown) {
  const details = error && typeof error === "object" ? error as { status?: unknown; code?: unknown; message?: unknown } : {};
  const status = typeof details.status === "number" ? details.status : undefined;
  const code = typeof details.code === "string" ? details.code : "";
  const message = error instanceof Error ? error.message : typeof details.message === "string" ? details.message : String(error ?? "");
  const raw = `${code} ${message}`.toLowerCase();
  const resourceLimit = /desktop resource limit reached|desktop_resource_limit|too many active desktop sessions/.test(raw);
  if (resourceLimit) {
    const count = message.match(/\((\d+)\s+active sessions\)/i)?.[1];
    return count
      ? `共享桌面资源暂不可用（${count} 个活动会话），请稍后重试。`
      : "共享桌面资源暂不可用，请稍后重试。";
  }
  if (/desktop is stopping|desktop_stopping|stopping desktop/.test(raw)) return "共享桌面正在停止，请稍后再试。";
  if (code === "computer_busy" || /computer is busy|desktop is busy|bot is working|bot 正在工作/.test(raw)) {
    return "电脑正在执行其他操作，请稍后再试。";
  }
  if (code === "not_configured" || /computer vm is not configured|shared computer is not configured/.test(raw)) return "共享电脑尚未配置，请先完成服务器安装。";
  return message || "电脑操作失败，请稍后重试。";
}

function errorText(error: unknown) {
  return computerErrorText(error);
}

function isTransientDesktopError(error: unknown) {
  const details = error && typeof error === "object" ? error as { status?: unknown; code?: unknown } : {};
  const status = typeof details.status === "number" ? details.status : 0;
  const code = typeof details.code === "string" ? details.code : "";
  return status === 409 || status === 502 || status === 503 || ["computer_busy", "desktop_stream_busy", "control_expired", "control_unavailable"].includes(code);
}

function resultImage(result: unknown) {
  if (!result || typeof result !== "object") return "";
  const value = (result as { image_url?: unknown }).image_url;
  return typeof value === "string" && value.startsWith("data:image/") ? value : "";
}

// Decode a frame off-screen before replacing the visible image. This keeps the
// previous frame on screen while a one-second poll downloads/decodes the next
// one, avoiding a blank flash between two data URLs.
function decodeFrame(src: string): Promise<boolean> {
  return new Promise((resolve) => {
    const image = new Image();
    let settled = false;
    const finish = (ok: boolean) => { if (!settled) { settled = true; resolve(ok); } };
    image.onload = () => finish(image.naturalWidth > 0 && image.naturalHeight > 0);
    image.onerror = () => finish(false);
    image.src = src;
    if (image.complete) finish(image.naturalWidth > 0 && image.naturalHeight > 0);
  });
}

// A cancelled request must release only its own in-flight slot. A late result
// from an older Bot or recovery attempt must never clear a newer capture.
export function frameOperationIsCurrent(active: object | null, operation: object) {
  return active === operation;
}

const PASSIVE_VIEWER_MAX_ATTEMPTS = 30;
const PASSIVE_VIEWER_WINDOW_MS = 90_000;
const PASSIVE_VIEWER_RETRY_DELAY_MS = 2500;
const VIEWER_CONNECT_ATTEMPTS = 6;
const VIEWER_CONNECT_RETRY_DELAY_MS = 350;

const phaseText: Record<string, string> = { checking: "检查环境", storage: "准备存储", network: "准备网络", booting: "启动虚拟机", verifying: "验证 Linux 与 Chrome", ready: "就绪" };
const stateText: Record<string, string> = { starting: "正在准备共享电脑", ready: "电脑已就绪", error: "电脑准备失败", stopped: "电脑已停止" };
const viewerText: Record<ViewerState, string> = { disconnected: "未连接查看", connecting: "连接中", connected: "已连接" };

export function DesktopStatusAnnouncement({ ready, text }: { ready: boolean; text: string }) {
  const previous = useRef<string | null>(null);
  const [announcement, setAnnouncement] = useState("");
  useEffect(() => {
    if (!ready) {
      previous.current = null;
      setAnnouncement("");
      return;
    }
    const before = previous.current;
    previous.current = text;
    // Keep this region separate from frames, tool output and existing alerts.
    if (before !== null && before !== text) setAnnouncement(text);
  }, [ready, text]);
  return <div className="sr-only" role="status" aria-live="polite" aria-atomic="true">{announcement}</div>;
}


export function BotDesktopPanel({ botId, botName, members = [], autoConnect = false, presence, onClose, onReadyChange, onExpandedChange }: Props) {
  const debug = useDebugMode();
  // A shared viewer keeps its transport/lease identity while chats change.
  const [selectedBotId] = useState(botId);
  const localPresence = useDesktopPresence(!presence);
  const desktopPresence = presence ?? localPresence;
  const { ownership, unavailable: ownershipUnavailable } = desktopPresence;
  const opened = useRef(false);
  const bootRequested = useRef(false);
  const passiveAttempts = useRef(new Set<string>());
  const passiveRetryAt = useRef(new Map<string, number>());
  const passiveAttemptCounts = useRef(new Map<string, number>());
  const passiveStartedAt = useRef(new Map<string, number>());
  const ownerRef = useRef(ownership?.owner);
  ownerRef.current = ownership?.owner;
  const botContextRef = useRef(botId);
  botContextRef.current = botId;
  const botLabel = (id: string) => members.find(member => member.id === id)?.name ?? (id === botId ? botName : "其他 Bot");
  const [info, setInfo] = useState<ComputerInfo | null>(null);
  const [imageURL, setImageURL] = useState("");
  const [command, setCommand] = useState("");
  const [url, setURL] = useState("");
  const [busy, setBusy] = useState(false);
  const [desktopActive, setDesktopActive] = useState(false);
  const [viewerState, setViewerState] = useState<ViewerState>("disconnected");
  const [expanded, setExpanded] = useState(false);
  const [humanControlled, setHumanControlled] = useState(false);
  const [error, setError] = useState("");
  const [connectionLost, setConnectionLost] = useState(false);
  const [output, setOutput] = useState("");
  const [videoState, setVideoState] = useState<DesktopVideoState>("connecting");
  const [videoSession, setVideoSession] = useState(0);
  const [fallbackFrameReady, setFallbackFrameReady] = useState(false);
  const videoStateRef = useRef<DesktopVideoState>("connecting");
  const generation = useRef(0);
  const busyRef = useRef(false);
  const infoRef = useRef<ComputerInfo | null>(null);
  const desktopStartedRef = useRef(false);
  const desktopActiveRef = useRef(false);
  const viewerStateRef = useRef<ViewerState>("disconnected");
  const viewerAttemptRef = useRef(0);
  const viewerIntentRef = useRef(false);
  const preserveViewerRef = useRef(false);
  const autoRecoveryRef = useRef(false);
  const autoRecoveryTokenRef = useRef(0);
  const frameInFlightRef = useRef(false);
  const framePromiseRef = useRef<Promise<FrameStatus> | null>(null);
  const frameOperationRef = useRef<object | null>(null);
  const frameRequestRef = useRef(0);
  const expandedChangeRef = useRef(onExpandedChange);
  const controlRef = useRef<RemoteControlHandle | null>(null);

  function setViewerConnection(next: ViewerState) {
    viewerStateRef.current = next;
    setViewerState(next);
  }

  function setDesktopRunning(next: boolean) {
    desktopStartedRef.current = next;
    desktopActiveRef.current = next;
    setDesktopActive(next);
  }

  useEffect(() => { expandedChangeRef.current = onExpandedChange; }, [onExpandedChange]);
  useEffect(() => { onReadyChange?.(info?.state === "ready"); }, [info?.state, onReadyChange]);
  useEffect(() => () => onReadyChange?.(false), [onReadyChange]);
  useEffect(() => {
    expandedChangeRef.current?.(expanded);
    return () => { expandedChangeRef.current?.(false); };
  }, [expanded]);

  useEffect(() => {
    const current = ++generation.current;
    infoRef.current = null;
    desktopStartedRef.current = false;
    desktopActiveRef.current = false;
    busyRef.current = false;
    viewerIntentRef.current = false;
    preserveViewerRef.current = false;
    autoRecoveryTokenRef.current++;
    autoRecoveryRef.current = false;
    frameRequestRef.current++;
    viewerAttemptRef.current++;
    frameInFlightRef.current = false;
    framePromiseRef.current = null;
    frameOperationRef.current = null;
    setBusy(false);
    setDesktopActive(false);
    setViewerConnection("disconnected");
    videoStateRef.current = "connecting";
    setVideoState("connecting");
    setVideoSession(0);
    setFallbackFrameReady(false);
    setExpanded(false);
    setInfo(null);
    setImageURL("");
    setOutput("");
    setCommand("");
    setURL("");
    setError("");
    setConnectionLost(false);
    let alive = true;
    let infoTimer = 0;

    const refresh = async (quiet: boolean) => {
      try {
        const next = await api.computerInfo();
        if (!alive || generation.current !== current) return;
        const recoveringViewer = viewerIntentRef.current && preserveViewerRef.current;
        infoRef.current = next;
        setInfo(next);
        if (next.state !== "ready") {
          viewerIntentRef.current = false;
          preserveViewerRef.current = false;
          autoRecoveryTokenRef.current++;
          autoRecoveryRef.current = false;
          viewerAttemptRef.current++;
          frameRequestRef.current++;
          setDesktopRunning(false);
          setViewerConnection("disconnected");
          setImageURL("");
          setExpanded(false);
          setConnectionLost(false);
        } else if (!recoveringViewer) {
          setConnectionLost(false);
        } else if (!document.hidden) {
          void recoverViewer();
        }
        if (!quiet) setError("");
      } catch (cause) {
        if (!alive || generation.current !== current) return;
        if (viewerIntentRef.current) {
          preserveViewerRef.current = true;
          viewerAttemptRef.current++;
          frameRequestRef.current++;
          setViewerConnection("connecting");
          setConnectionLost(true);
          return;
        }
        infoRef.current = null;
        viewerIntentRef.current = false;
        preserveViewerRef.current = false;
        setDesktopRunning(false);
        setViewerConnection("disconnected");
        setImageURL("");
        setInfo(null);
        if (quiet) setConnectionLost(true);
        else setError(errorText(cause));
      }
    };

    const pollInfo = async (quiet: boolean) => {
      if (!alive) return;
      if (document.visibilityState === "visible") await refresh(quiet);
      if (alive) infoTimer = window.setTimeout(() => void pollInfo(true), 1_500);
    };
    void pollInfo(false);
    return () => {
      alive = false;
      generation.current++;
      frameRequestRef.current++;
      frameInFlightRef.current = false;
      framePromiseRef.current = null;
      frameOperationRef.current = null;
      window.clearTimeout(infoTimer);
    };
  // The viewer identity lasts until this shared panel unmounts. Chat switches
  // do not restart video, clear the last frame, or release human control.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedBotId]);



  async function captureViewerFrameRequest(): Promise<FrameStatus> {
    const current = generation.current;
    const request = ++frameRequestRef.current;
    const operation = {};
    frameOperationRef.current = operation;
    frameInFlightRef.current = true;
    try {
      const response = await api.computerAction({ bot_id: selectedBotId, action: "desktop.capture", args: {} });
      if (generation.current !== current || frameRequestRef.current !== request || viewerStateRef.current === "disconnected" || infoRef.current?.state !== "ready") return "failed";
      if (!response.ok) throw new Error(response.error || "电脑截图失败");
      const image = resultImage(response.result);
      if (image) {
        if (!await decodeFrame(image)) return "failed";
        if (generation.current !== current || frameRequestRef.current !== request || infoRef.current?.state !== "ready") return "failed";
        setImageURL(image);
        if (videoStateRef.current === "fallback") {
          if (preserveViewerRef.current) {
            setFallbackFrameReady(false);
            videoStateRef.current = "connecting";
            setVideoState("connecting");
            setVideoSession(value => value + 1);
          } else {
            setFallbackFrameReady(true);
          }
        }
        setDesktopRunning(true);
        setConnectionLost(false);
        if (viewerStateRef.current === "connecting" || preserveViewerRef.current) setError("");
        return "shown";
      }
      return "failed";
    } catch (cause) {
      if (generation.current !== current || frameRequestRef.current !== request || viewerStateRef.current === "disconnected" || infoRef.current?.state !== "ready") return "failed";
      // A Bot can be running while a read-only viewer reconnects. Do not turn
      // this normal lease response into a flashing error banner.
      if (cause instanceof ApiError && cause.status === 409) return "busy";
      if (cause instanceof Error && cause.message.toLowerCase().includes("desktop is not running")) {
        // Idle collection or a Bot stopping its desktop is not a network fault.
        // Never keep presenting the last frame as a live, connected screen.
        if (viewerStateRef.current === "connected" && !preserveViewerRef.current) {
          setDesktopRunning(false);
          setViewerConnection("disconnected");
          setImageURL("");
          setExpanded(false);
          setConnectionLost(false);
          setError("");
        }
        return "not-running";
      }
      // HTTPS/proxy churn and stream handoffs are recoverable. Keep the last
      // frame and let the bounded reconnect loop decide whether this is real.
      if (isTransientDesktopError(cause)) return "failed";
      if (preserveViewerRef.current && viewerIntentRef.current) {
        setConnectionLost(true);
        return "failed";
      }
      setConnectionLost(true);
      setDesktopRunning(false);
      setViewerConnection("disconnected");
      setImageURL("");
      return "failed";
    } finally {
      if (frameOperationIsCurrent(frameOperationRef.current, operation)) {
        frameInFlightRef.current = false;
        frameOperationRef.current = null;
      }
    }
  }

  function captureViewerFrame(allowDisconnected = false): Promise<FrameStatus> {
    if (busyRef.current || (!allowDisconnected && viewerStateRef.current !== "connected") || (!allowDisconnected && connectionLost) || infoRef.current?.state !== "ready" || (!allowDisconnected && !desktopStartedRef.current)) return Promise.resolve("failed");
    if (frameInFlightRef.current) return framePromiseRef.current ?? Promise.resolve("failed");
    const promise = captureViewerFrameRequest();
    framePromiseRef.current = promise;
    return promise.finally(() => {
      if (framePromiseRef.current === promise) framePromiseRef.current = null;
    });
  }

  async function recoverViewer() {
    if (autoRecoveryRef.current || !viewerIntentRef.current || !preserveViewerRef.current || document.hidden || infoRef.current?.state !== "ready") return;
    autoRecoveryRef.current = true;
    const token = ++autoRecoveryTokenRef.current;
    const current = generation.current;
    const viewerAttempt = ++viewerAttemptRef.current;
    let status: FrameStatus = "failed";
    try {
      for (let retryCount = 0; retryCount < 6; retryCount++) {
        if (generation.current !== current || viewerAttemptRef.current !== viewerAttempt || !viewerIntentRef.current || document.hidden) return;
        status = await captureViewerFrame(true);
        if (generation.current !== current || viewerAttemptRef.current !== viewerAttempt || !viewerIntentRef.current || document.hidden) return;
        if (status === "shown" || status === "not-running") break;
        await new Promise(resolve => window.setTimeout(resolve, 350));
      }
      if (generation.current !== current || viewerAttemptRef.current !== viewerAttempt || !viewerIntentRef.current) return;
      if (status === "shown") {
        preserveViewerRef.current = false;
        setConnectionLost(false);
        setError("");
        setViewerConnection("connected");
      } else if (status === "not-running") {
        viewerIntentRef.current = false;
        preserveViewerRef.current = false;
        setDesktopRunning(false);
        setViewerConnection("disconnected");
        setImageURL("");
        setConnectionLost(false);
        setError("");
      } else if (status === "failed") {
        // Only advertise a connection loss after all recovery attempts fail.
        setConnectionLost(true);
      }
    } finally {
      if (autoRecoveryTokenRef.current === token) autoRecoveryRef.current = false;
    }
  }

  useEffect(() => {
    if (viewerState !== "connected" || videoState === "live") return;
    let cancelled = false;
    let timer = 0;
    const pollFrame = async () => {
      if (cancelled) return;
      if (document.visibilityState === "visible" && !connectionLost) await captureViewerFrame();
      if (!cancelled) timer = window.setTimeout(() => void pollFrame(), 1_000);
    };
    void pollFrame();
    return () => { cancelled = true; window.clearTimeout(timer); };
  // The viewer state starts/stops the serial, one-second screen poll. The
  // generation guard inside captureViewerFrame handles identity changes.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedBotId, viewerState, videoState, connectionLost]);

  useEffect(() => {
    if (!expanded) return;
    const onKeyDown = (event: KeyboardEvent) => { if (event.key === "Escape") { event.preventDefault(); setExpanded(false); } };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [expanded]);

  async function act(action: string, args: Record<string, unknown> = {}) {
    if (busyRef.current || connectionLost || infoRef.current?.state !== "ready") return null;
    if (action === "desktop.stop") await controlRef.current?.release();
    const current = generation.current;
    const actionAttempt = viewerAttemptRef.current;
    busyRef.current = true;
    setBusy(true);
    setError("");
    try {
      // A viewer frame uses the same per-Bot action lease. Wait for that
      // read-only request to settle before a human mutation so a transient
      // 409 never silently drops a click or key press.
      const pendingFrame = framePromiseRef.current;
      if (pendingFrame) await pendingFrame.catch(() => "failed" as FrameStatus);
      const viewerRecovery = viewerIntentRef.current && preserveViewerRef.current;
      const viewerAction = action.startsWith("desktop.") && action !== "desktop.start" && action !== "desktop.stop";
      if (generation.current !== current || viewerAttemptRef.current !== actionAttempt || infoRef.current?.state !== "ready" || (viewerRecovery && action !== "desktop.start" && action !== "desktop.stop") || (viewerAction && viewerStateRef.current !== "connected") || (action.startsWith("browser.") && viewerStateRef.current !== "connected")) return null;
      const response = await api.computerAction({ bot_id: action === "shell.exec" ? botId : selectedBotId, action, args });
      if (action === "shell.exec" && botContextRef.current !== botId) return null;
      if (generation.current !== current || viewerAttemptRef.current !== actionAttempt) return null;
      if (!response.ok) throw new Error(response.error || "电脑操作失败");
      const image = resultImage(response.result);
      if (generation.current === current) {
        if (image && await decodeFrame(image) && generation.current === current) setImageURL(image);
        if (action === "shell.exec") setOutput(JSON.stringify(response.result, null, 2));
        if (action === "desktop.start" || ((action === "desktop.capture" || action === "browser.snapshot") && image)) setDesktopRunning(true);
      }
      if (generation.current === current && action === "desktop.stop") {
        viewerIntentRef.current = false;
        preserveViewerRef.current = false;
        autoRecoveryTokenRef.current++;
        autoRecoveryRef.current = false;
        setDesktopRunning(false);
        setViewerConnection("disconnected");
        frameRequestRef.current++;
        setImageURL("");
        setExpanded(false);
      }
      if (generation.current === current && action === "desktop.start") {
        // Starting is a foreground operation, so keep the panel busy while
        // the first frame is fetched. Later frames use the read-only poll.
        try {
          const snapshot = await api.computerAction({ bot_id: selectedBotId, action: "desktop.capture", args: {} });
          const snapshotImage = snapshot.ok ? resultImage(snapshot.result) : "";
          if (generation.current === current && snapshotImage && await decodeFrame(snapshotImage) && generation.current === current) setImageURL(snapshotImage);
        } catch (cause) {
          if (generation.current === current && !isTransientDesktopError(cause)) setError(errorText(cause));
        }
      }
      return response.result;
    } catch (cause) {
      if (generation.current === current && !isTransientDesktopError(cause)) setError(errorText(cause));
      return null;
    } finally {
      if (generation.current === current) { busyRef.current = false; setBusy(false); }
    }
  }

  async function perform(action: string, args: Record<string, unknown> = {}) {
    const result = await act(action, args);
    if (videoStateRef.current !== "live" && result !== null && action !== "desktop.capture" && action !== "desktop.start" && action !== "desktop.stop") void captureViewerFrame();
    return result;
  }

  async function connectViewer(retry = false, passiveRunId?: string) {
    if (passiveRunId && (ownerRef.current?.kind !== "bot" || ownerRef.current.run_id !== passiveRunId)) return;
    if (busyRef.current || infoRef.current?.state !== "ready" || (!retry && viewerStateRef.current !== "disconnected")) return;
    if (passiveRunId) {
      const startedAt = passiveStartedAt.current.get(passiveRunId) ?? Date.now();
      passiveStartedAt.current.set(passiveRunId, startedAt);
      if (Date.now() - startedAt >= PASSIVE_VIEWER_WINDOW_MS || (passiveAttemptCounts.current.get(passiveRunId) ?? 0) >= PASSIVE_VIEWER_MAX_ATTEMPTS) return;
      passiveAttemptCounts.current.set(passiveRunId, (passiveAttemptCounts.current.get(passiveRunId) ?? 0) + 1);
    }
    viewerIntentRef.current = true;
    preserveViewerRef.current = false;
    const current = generation.current;
    const viewerAttempt = ++viewerAttemptRef.current;
    const finishPassiveAttempt = () => {
      if (!passiveRunId || generation.current !== current || viewerAttemptRef.current !== viewerAttempt) return;
      // A real VM may need a minute before its first frame. Keep retrying only
      // within this owner's bounded startup window; never power it on here.
      if ((passiveAttemptCounts.current.get(passiveRunId) ?? 0) < PASSIVE_VIEWER_MAX_ATTEMPTS && Date.now() - (passiveStartedAt.current.get(passiveRunId) ?? 0) < PASSIVE_VIEWER_WINDOW_MS) {
        passiveRetryAt.current.set(passiveRunId, Date.now() + PASSIVE_VIEWER_RETRY_DELAY_MS);
        passiveAttempts.current.delete(passiveRunId);
      }
      viewerIntentRef.current = false;
      setViewerConnection("disconnected");
    };
    setError("");
    videoStateRef.current = "connecting";
    setVideoState("connecting");
    setFallbackFrameReady(false);
    setViewerConnection("connecting");
    let status: FrameStatus = "failed";
    for (let retryCount = 0; retryCount < VIEWER_CONNECT_ATTEMPTS; retryCount++) {
      if (generation.current !== current || viewerAttemptRef.current !== viewerAttempt || viewerStateRef.current !== "connecting") return;
      status = await captureViewerFrame(true);
      if (generation.current !== current || viewerAttemptRef.current !== viewerAttempt) return;
      if (viewerStateRef.current !== "connecting") {
        // Capture failure can disconnect the viewer before returning its status.
        // Explicit disconnect increments viewerAttempt and is rejected above.
        if (passiveRunId && status === "failed") finishPassiveAttempt();
        return;
      }
      if (status === "shown" || status === "not-running") break;
      if (retryCount + 1 < VIEWER_CONNECT_ATTEMPTS) await new Promise(resolve => window.setTimeout(resolve, VIEWER_CONNECT_RETRY_DELAY_MS));
    }
    if (generation.current !== current || viewerAttemptRef.current !== viewerAttempt || viewerStateRef.current !== "connecting") return;
    if (status === "busy") {
      if (passiveRunId) { finishPassiveAttempt(); return; }
      window.setTimeout(() => { if (generation.current === current && viewerAttemptRef.current === viewerAttempt && viewerStateRef.current === "connecting") void connectViewer(true, passiveRunId); }, 700);
      return;
    }
    if (status === "shown") {
      setViewerConnection("connected");
      return;
    }
    if (status === "not-running") {
      if (passiveRunId) {
        finishPassiveAttempt();
        return;
      }
      const started = await act("desktop.start");
      if (generation.current === current && viewerAttemptRef.current === viewerAttempt && viewerStateRef.current === "connecting" && started !== null) {
        setDesktopRunning(true);
        setViewerConnection("connected");
        await captureViewerFrame();
      } else if (generation.current === current && viewerAttemptRef.current === viewerAttempt && viewerStateRef.current === "connecting") {
        setError("桌面正在切换或准备中，请稍后重试。");
        setViewerConnection("disconnected");
      }
      return;
    }
    if (passiveRunId) { finishPassiveAttempt(); return; }
    if (generation.current === current && viewerAttemptRef.current === viewerAttempt && viewerStateRef.current === "connecting") {
      setError("桌面暂时连接不上，请稍后重试。");
      setViewerConnection("disconnected");
    }
  }

  useEffect(() => {
    if (bootRequested.current || info?.state !== "stopped" || ownershipUnavailable || ownership?.owner || busy) return;
    bootRequested.current = true;
    void retry();
  // Reuse the existing authorized preparation path, only for a stopped machine.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [info?.state, ownershipUnavailable, ownership?.owner, busy]);

  useEffect(() => {
    if (opened.current || info?.state !== "ready" || ownershipUnavailable || connectionLost || busy) return;
    opened.current = true;
    if (ownership?.owner?.kind === "bot") return; // Existing passive owner path.
    void connectViewer();
  // Opening this authorized workspace starts its normal desktop connection once.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [info?.state, ownershipUnavailable, connectionLost, busy, ownership?.owner?.kind]);

  useEffect(() => {
    const owner = ownership?.owner;
    if (!autoConnect || ownershipUnavailable || owner?.kind !== "bot" || info?.state !== "ready" || viewerState !== "disconnected" || busy || passiveAttempts.current.has(owner.run_id) || Date.now() < (passiveRetryAt.current.get(owner.run_id) ?? 0)) return;
    passiveAttempts.current.add(owner.run_id);
    void connectViewer(false, owner.run_id);
  // Connecting is passive: desktop.capture proves a screen exists; it never powers it on.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [autoConnect, ownershipUnavailable, ownership, info?.state, viewerState, busy]);

  function disconnectViewer() {
    const owner = ownerRef.current;
    if (owner?.kind === "bot") {
      passiveAttempts.current.add(owner.run_id);
      passiveRetryAt.current.delete(owner.run_id);
    }
    void controlRef.current?.release();
    viewerIntentRef.current = false;
    preserveViewerRef.current = false;
    autoRecoveryTokenRef.current++;
    autoRecoveryRef.current = false;
    viewerAttemptRef.current++;
    frameRequestRef.current++;
    frameInFlightRef.current = false;
    framePromiseRef.current = null;
    frameOperationRef.current = null;
    setViewerConnection("disconnected");
    setImageURL("");
  }

  async function retry() {
    const current = generation.current;
    busyRef.current = true;
    setBusy(true);
    setError("");
    try {
      await api.computerRetry();
      if (generation.current === current) {
        const next = await api.computerInfo();
        if (generation.current === current) { infoRef.current = next; setInfo(next); setConnectionLost(false); }
      }
    } catch (cause) {
      if (generation.current === current) setError(errorText(cause));
    } finally {
      if (generation.current === current) { busyRef.current = false; setBusy(false); }
    }
  }

  const ready = info?.state === "ready";
  const botWorking = ownership?.owner?.kind === "bot" && !humanControlled;
  // Ownership can briefly outlive the VM's boot transition. Do not present
  // that stale lease as another window blocking the viewer while the machine
  // is still preparing or the first frame is being established.
  const showOwnershipStatus = ready && viewerState !== "connecting" && !busy && !connectionLost;
  const controlsReady = ready && viewerState === "connected" && !connectionLost;
  // Cursor policy changes reconnect the observer stream. Keep an already
  // acquired human lease through that short connecting state; otherwise the
  // connecting notification releases the lease and flips cursor back, causing
  // an endless visible/hidden reconnect loop.
  const screenReady = controlsReady && ((humanControlled && videoState === "connecting") || videoState === "live" || (videoState === "fallback" && fallbackFrameReady));
  const viewerAnnouncement = connectionLost || error ? "" : viewerState === "connecting" ? "正在连接桌面" : viewerState === "disconnected" ? "已断开桌面查看" : videoState === "live" ? "桌面已连接，正在显示实时视频" : videoState === "fallback" && fallbackFrameReady ? "桌面已连接，已切换为截图查看" : "桌面已连接，正在恢复画面";
  function videoStateChanged(next: DesktopVideoState) {
    if (viewerStateRef.current !== "connected" && !(preserveViewerRef.current && viewerIntentRef.current)) return;
    if (next === "fallback") {
      // Discard captures begun before the stream failed. Keep the video mounted
      // with its final frame until a new screenshot has finished decoding.
      frameRequestRef.current++;
      frameInFlightRef.current = false;
      framePromiseRef.current = null;
      frameOperationRef.current = null;
      setFallbackFrameReady(false);
    }
    videoStateRef.current = next;
    setVideoState(next);
    if (next === "stopped") {
      viewerIntentRef.current = false;
      preserveViewerRef.current = false;
      autoRecoveryTokenRef.current++;
      autoRecoveryRef.current = false;
      frameRequestRef.current++;
      setDesktopRunning(false);
      setViewerConnection("disconnected");
      setImageURL("");
      setConnectionLost(false);
      setError("");
    }
  }

  function closePanel(reason: "hide" | "shutdown" = "hide") {
    void controlRef.current?.release();
    viewerIntentRef.current = false;
    preserveViewerRef.current = false;
    autoRecoveryTokenRef.current++;
    autoRecoveryRef.current = false;
    generation.current++;
    viewerAttemptRef.current++;
    frameRequestRef.current++;
    frameOperationRef.current = null;
    onClose(reason);
  }

  const windowActions = <div className="computer-detail-heading-actions computer-floating-actions">
    <button type="button" className="desktop-control-button desktop-control-expand" aria-label={expanded ? "缩小共享电脑" : "放大共享电脑"} title={expanded ? "缩小" : "放大"} onClick={() => setExpanded(!expanded)}><svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d={expanded ? "M4 9h5V4m11 5h-5V4M4 15h5v5m11-5h-5v5" : "M9 4H4v5m11-5h5v5M4 15v5h5m11-5v5h-5"} /></svg></button>
    <button type="button" className="desktop-control-button computer-hide-button" aria-label="隐藏共享电脑" title="最小化" onClick={() => closePanel("hide")}><TofiIcon name="minus" size={16} /></button>

  </div>;
  // The overlay is a sibling of the launch button / remote input surface.
  // Its containing block is always the screen, never status or panel chrome.
  const previewControls = !expanded && <div className="desktop-preview-controls">
    {windowActions}
  </div>;

  // Design system: one capsule on the bezel says who drives; the frame color follows it.
  const driver = humanControlled ? "you" : botWorking && imageURL ? "bot" : "";
  const capsule = error ? { tone: "error", text: "连接失败" }
    : !imageURL ? { tone: "booting", text: info?.state === "starting" && info.phase ? `正在开机 · ${phaseText[info.phase] ?? info.phase}` : "正在开机…" }
    : humanControlled ? { tone: "you", text: "你在控制" }
    : botWorking ? { tone: "bot", text: `${botLabel(ownership?.owner?.bot_id ?? botId)} 在操作` }
    : { tone: "idle", text: "共享电脑空闲" };
  return <div className={`detail-content computer-detail ${expanded ? "is-expanded" : ""}${driver ? ` is-driven-by-${driver}` : ""}`}>
    <DesktopStatusAnnouncement key={selectedBotId} ready={info !== null} text={viewerAnnouncement} />
    {!expanded && <p className={`desktop-status-capsule is-${capsule.tone}`} aria-hidden="true"><span className="desktop-status-dot" />{capsule.text}</p>}
    {expanded && <div className="detail-heading computer-floating-heading"><div className="computer-heading-copy"><h2 className="sr-only">共享电脑</h2></div>{windowActions}</div>}

    {(error || connectionLost) && <p className="error-banner" role="alert">{connectionLost ? "电脑连接暂时不可用，正在重试…" : error}</p>}
    {!botWorking && info && (debug || info.state !== "ready") && <div className="computer-info"><div className="computer-info-main"><strong>{stateText[info.state] ?? `电脑${info.state}`}</strong><small>{info.phase && info.state !== "error" && info.state !== "stopped" ? `阶段：${phaseText[info.phase] ?? info.phase}` : "状态已同步"}</small></div>{info.error && <small className="error-text">{info.error}</small>}{(info.state === "error" || info.state === "stopped") && <button className="secondary-button" disabled={busy} onClick={() => void retry()}>重试准备</button>}</div>}
    {imageURL && <div className={`computer-screen-wrap desktop-presence-frame desktop-preview-surface desktop-live-screen${humanControlled ? " is-human-controlled" : ""}`}>{(debug || connectionLost) && <div className="computer-screen-toolbar"><small className="computer-screen-caption">{connectionLost ? "连接中断 · 保留最后画面" : videoState === "live" ? "实时桌面" : videoState === "fallback" && fallbackFrameReady ? "截图查看 · 画面静默更新" : "正在恢复桌面画面…"}</small></div>}<RemoteDesktopControl key={selectedBotId} controlRef={controlRef} onControlChange={setHumanControlled} botId={selectedBotId} enabled={!!screenReady && !busy} blocked={showOwnershipStatus && Boolean(ownership?.owner && !humanControlled)} takeoverRun={showOwnershipStatus && !ownershipUnavailable && ownership?.owner?.kind === "bot" ? { id: ownership.owner.run_id, name: botLabel(ownership.owner.bot_id), botId: ownership.owner.bot_id } : undefined} expanded={expanded} onExpand={() => setExpanded(true)} waiting={!!ownership?.waiting.length}>{videoState !== "stopped" && (videoState !== "fallback" || !fallbackFrameReady) ? <DesktopVideo key={`${selectedBotId}:${videoSession}`} botId={selectedBotId} cursor={isDesktop && !humanControlled ? "visible" : "hidden"} enabled={!!ready && (viewerState === "connected" || preserveViewerRef.current) && desktopActive} poster={imageURL} onState={videoStateChanged}  /> : <img className="computer-screen" src={imageURL} alt="共享电脑屏幕"  />}<DesktopPointerMarker presence={desktopPresence} humanControlled={humanControlled || !screenReady || busy} botLabel={botLabel} /></RemoteDesktopControl>{previewControls}</div>}
    {!imageURL && <div className="desktop-preview-surface desktop-launch-surface" onClick={() => { if (!expanded) setExpanded(true); }}>
      <div className="desktop-launch-preview desktop-bot-starting" role="status" aria-label={error ? "电脑连接失败" : "正在启动电脑"}>{error ? <span>连接失败，请稍后重新打开</span> : <GazeAvatar id={ownership?.owner?.bot_id ?? botId} motion="working" />}</div>
      {previewControls}
    </div>}
    {debug && <fieldset className="computer-aux-fields" disabled={humanControlled}><details className="computer-tools"><summary>辅助操作</summary><div className="computer-tools-body"><div className="computer-key-row"><button className="secondary-button" disabled={busy || !controlsReady} onClick={() => void perform("desktop.key", { key: "Return" })}>Enter</button><button className="secondary-button" disabled={busy || !controlsReady} onClick={() => void perform("desktop.key", { key: "Escape" })}>Esc</button><button className="secondary-button" disabled={busy || !controlsReady} onClick={() => void perform("desktop.key", { key: "c", modifiers: ["ctrl"] })}>Ctrl+C</button></div><label>打开浏览器<input disabled={busy || !controlsReady} value={url} onChange={event => setURL(event.target.value)} onKeyDown={event => { if (event.key === "Enter" && controlsReady && !busyRef.current && url.trim()) { event.preventDefault(); void perform("browser.navigate", { url }); } }} placeholder="https://…" /></label><label>运行 Shell（{botName} 工作区）<textarea disabled={busy || connectionLost || !ready} value={command} onChange={event => setCommand(event.target.value)} rows={3} placeholder="例如：pwd" /></label><button className="primary-button" disabled={busy || connectionLost || !ready || !command.trim()} onClick={() => void perform("shell.exec", { command })}>运行命令</button>{output && <pre className="computer-output">{output}</pre>}{info && <details className="computer-technical"><summary>技术详情</summary><dl><div><dt>工作区</dt><dd>{info.workspace_root || "/workspace"}</dd></div><div><dt>浏览器</dt><dd>{info.browser || "Google Chrome"}</dd></div></dl></details>}</div></details></fieldset>}
  </div>;
}
