import { useEffect, useRef, useState, type ReactNode } from "react";
import { GazeAvatar } from "./GazeAvatar";
import { ApiError, request } from "./api";
import { isDesktop } from "./desktop";

type InputEvent = { type: string; [key: string]: unknown };
export type RemoteControlHandle = { release: () => Promise<void>; acquire: () => Promise<void> };
type Props = { botId: string; enabled: boolean; blocked?: boolean; takeoverRun?: { id: string; name: string; botId?: string }; expanded: boolean; onExpand?: () => void; waiting?: boolean; onControlChange: (owned: boolean) => void; controlRef: React.RefObject<RemoteControlHandle | null>; children: ReactNode };
// Mac Command shortcuts intentionally map to Linux Control (Cmd+A/L/T/C/V).
const keys: Record<string, string> = { Enter: "Return", Escape: "Escape", Backspace: "BackSpace", Delete: "Delete", Tab: "Tab", ArrowLeft: "Left", ArrowRight: "Right", ArrowUp: "Up", ArrowDown: "Down", Home: "Home", End: "End", PageUp: "Prior", PageDown: "Next", Control: "Control_L", Meta: "Control_L", Alt: "Alt_L", Shift: "Shift_L", " ": "space" };

// Input ownership is separate from watching video. One ordered queue delivers
// edges/text; only adjacent pointer moves may be coalesced.
export function RemoteDesktopControl({ botId, enabled, blocked = false, takeoverRun, expanded, onExpand, waiting = false, onControlChange, controlRef, children }: Props) {
  const touchInput = useRef(false);
  const activePointer = useRef<number | null>(null);
  const pointerControl = useRef(false);
  const [keyboardOpen, setKeyboardOpen] = useState(false);
  const touchMode = typeof window.matchMedia === "function" && window.matchMedia("(pointer: coarse)").matches;
  const [owned, setOwned] = useState(false);
  const acquiring = useRef(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [copyText, setCopyText] = useState<string | null>(null);
  const surface = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLTextAreaElement>(null);
  const session = useRef("");
  const epoch = useRef(0);
  const seq = useRef(0);
  const queue = useRef<InputEvent[]>([]);
  const sending = useRef(false);
  const sendingActivity = useRef(false);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const composing = useRef(false);
  const committed = useRef("");
  const lastActivity = useRef(0);
  const pressed = useRef(new Map<string, string>());
  useEffect(() => { onControlChange(owned); return () => onControlChange(false); }, [owned, onControlChange]);
  const call = async (action: string, args: Record<string, unknown>, keepalive = false) => {
    const response = await request<{ ok: boolean; result?: Record<string, unknown>; error?: string }>("/api/computers/firecracker/actions", { method: "POST", body: JSON.stringify({ bot_id: botId, action: `desktop.control.${action}`, args }), keepalive, signal: AbortSignal.timeout(10000) });
    if (!response.ok) throw new Error(response.error || "桌面控制失败");
    return response.result ?? {};
  };
  async function release() {
    const id = session.current;
    session.current = "";
    epoch.current++;
    queue.current = [];
    clearTimeout(timer.current);
    timer.current = undefined;
    committed.current = "";
    pressed.current.clear();
    composing.current = false;
    activePointer.current = null;
    pointerControl.current = false;
    setKeyboardOpen(false);
    input.current?.blur?.();
    setOwned(false);
    acquiring.current = false;
    setPending(false);
    setNotice("");
    if (input.current) input.current.value = "";
    if (id) await call("release", { control_id: id }, true).catch(() => {});
  }
  const acquireRef = useRef<() => Promise<void>>(async () => {});
  acquireRef.current = acquire;
  async function acquire() {
    if (!enabled || (blocked && !takeoverRun) || !expanded || session.current || acquiring.current) return;
    const version = ++epoch.current;
    acquiring.current = true;
    setPending(true); setError(""); setNotice(takeoverRun ? "正在等待当前操作完成…" : "");
    try {
      if (epoch.current !== version) return;
      const result = await call("acquire", takeoverRun ? { expected_owner: takeoverRun.id } : {});
      const id = String(result.control_id ?? "");
      if (!id) throw new Error("没有取得桌面控制权");
      if (epoch.current !== version) { void call("release", { control_id: id }, true); return; }
      session.current = id; lastActivity.current = Date.now(); seq.current = 0; sending.current = false;
      setOwned(true);
      setNotice("");
      if (!touchMode && !touchInput.current) input.current?.focus({ preventScroll: true });
    } catch (cause) {
      if (epoch.current !== version) return;
      if (cause instanceof ApiError && (cause.status === 409 || cause.code === "computer_busy")) {
        setNotice("桌面刚刚仍在切换控制，请稍后再试。");
        setError("");
      } else setError(cause instanceof Error ? cause.message : "无法接管桌面");
    }
    finally { if (epoch.current === version) { acquiring.current = false; setPending(false); } }
  }
  async function flush() {
    if (sending.current || !session.current || !queue.current.length) return;
    const id = session.current;
    const version = epoch.current;
    const isCurrent = () => session.current === id && epoch.current === version;
    sending.current = true;
    sendingActivity.current = false;
    try {
      if (queue.current[0]?.type === "clipboard-barrier") {
        queue.current.shift();
        sendingActivity.current = true;
        const result = await call("clipboard.read", { control_id: id });
        if (!isCurrent()) return;
        if (result.changed === false) return;
        const text = String(result.text ?? "");
        try { await navigator.clipboard.writeText(text); } catch { setCopyText(text); }
      } else {
        const barrier = queue.current.findIndex(event => event.type === "clipboard-barrier");
        const events = queue.current.splice(0, Math.min(64, barrier < 0 ? 64 : barrier));
        sendingActivity.current = events.some(event => event.type !== "move");
        const result = await call("input", { control_id: id, seq: ++seq.current, events });
        if (isCurrent() && result.warning === "text_not_accepted") setError("当前窗口未接收文本，请先点击输入位置");
        else if (isCurrent() && events.some(event => event.type === "text")) setError("");
      }
    }
    catch (cause) { if (isCurrent()) { setError(cause instanceof Error ? cause.message : "连接中断，请重新接管"); release(); } }
    finally { if (isCurrent()) { sending.current = false; sendingActivity.current = false; if (queue.current.length) void flush(); } }
  }
  function enqueue(event: InputEvent) {
    if (!session.current) return;
    if (event.type !== "move" || activePointer.current !== null) lastActivity.current = Date.now();
    if (event.type === "move" && queue.current.at(-1)?.type === "move") queue.current[queue.current.length - 1] = event;
    else if (event.type === "text" && queue.current.at(-1)?.type === "text") queue.current[queue.current.length - 1].text = String(queue.current.at(-1)?.text ?? "") + String(event.text ?? "");
    else queue.current.push(event);
    if (queue.current.length > 512) { setError("输入连接过慢，请重新接管"); release(); return; }
    if (timer.current === undefined) timer.current = setTimeout(() => { timer.current = undefined; void flush(); }, 16);
  }
  useEffect(() => {
    controlRef.current = { release, acquire: () => acquireRef.current() };
    const heartbeat = setInterval(() => {
      const id = session.current;
      const version = epoch.current;
      if (id) void call("renew", { control_id: id, interacting: Date.now() - lastActivity.current < 15000 || composing.current || pressed.current.size > 0 || activePointer.current !== null || queue.current.some(event => event.type !== "move") || sendingActivity.current }).then(result => {
        if (session.current === id && epoch.current === version && result.released === true) { void release(); setNotice("已交给等待中的 Bot"); }
      }).catch(() => { if (session.current === id && epoch.current === version) { setError("控制连接已断开，请重新接管"); release(); } });
    }, 5000);
    const lostFocus = () => release();
    const visibility = () => { if (document.hidden) release(); };
    window.addEventListener("blur", lostFocus);
    window.addEventListener("pagehide", lostFocus);
    document.addEventListener("visibilitychange", visibility);
    return () => { controlRef.current = null; clearInterval(heartbeat); clearTimeout(timer.current); window.removeEventListener("blur", lostFocus); window.removeEventListener("pagehide", lostFocus); document.removeEventListener("visibilitychange", visibility); void release(); };
  // Each mounted identity owns its own lease and cleanup closure.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [botId]);
  useEffect(() => { if (!enabled || !expanded) release(); }, [enabled, expanded]);
  useEffect(() => {
    const element = surface.current;
    const wheel = (event: WheelEvent) => {
      if (!session.current) return;
      event.preventDefault(); event.stopPropagation();
      enqueue({ type: "wheel", delta_x: Math.sign(event.deltaX) * Math.min(20, Math.ceil(Math.abs(event.deltaX) / 60)), delta_y: Math.sign(event.deltaY) * Math.min(20, Math.ceil(Math.abs(event.deltaY) / 60)) });
    };
    element?.addEventListener("wheel", wheel, { passive: false });
    return () => element?.removeEventListener("wheel", wheel);
  }, [botId]);
  function point(clientX: number, clientY: number) {
    const media = surface.current?.querySelector("video,img");
    if (!media) return null;
    const rect = media.getBoundingClientRect();
    const width = media instanceof HTMLVideoElement ? media.videoWidth || 1280 : (media as HTMLImageElement).naturalWidth || 1280;
    const height = media instanceof HTMLVideoElement ? media.videoHeight || 800 : (media as HTMLImageElement).naturalHeight || 800;
    const scale = Math.min(rect.width / width, rect.height / height);
    const x = (clientX - rect.left - (rect.width - width * scale) / 2) / scale;
    const y = (clientY - rect.top - (rect.height - height * scale) / 2) / scale;
    if (!scale || x < 0 || y < 0 || x >= width || y >= height) return null;
    return { x: Math.min(1279, Math.floor(x * 1280 / width)), y: Math.min(799, Math.floor(y * 800 / height)) };
  }
  function copy(cut: boolean) {
    if (!session.current) return;
    const controlHeld = ["ControlLeft", "ControlRight"].some(code => pressed.current.has(code));
    if (!controlHeld) enqueue({ type: "keydown", key: "Control_L" });
    enqueue({ type: "keydown", key: cut ? "x" : "c" });
    enqueue({ type: "keyup", key: cut ? "x" : "c" });
    if (!controlHeld) enqueue({ type: "keyup", key: "Control_L" });
    // Keep later typing behind the clipboard read: text input also uses the
    // guest clipboard, so merely waiting for an empty queue loses this order.
    enqueue({ type: "clipboard-barrier" });
  }
  return <div className={`remote-desktop ${owned ? "in-control" : ""}`}>
    {expanded && isDesktop && <div className="remote-control-bar"><span className="remote-owner-status">{!owned && takeoverRun && <GazeAvatar id={takeoverRun.botId ?? botId} mini animated={false} />}<span>{owned ? "你正在控制" : pending ? "等待操作完成…" : takeoverRun ? `${takeoverRun.name} 正在控制` : blocked ? "其他窗口正在控制" : !enabled ? "正在连接…" : "点击画面接管"}</span></span><div className="remote-control-actions">{owned && (touchMode || touchInput.current) && <button className="secondary-button" aria-label={keyboardOpen ? "收起远程键盘" : "打开远程键盘"} onPointerDown={event => event.preventDefault()} onClick={() => { if (keyboardOpen) input.current?.blur(); else input.current?.focus({ preventScroll: true }); setKeyboardOpen(!keyboardOpen); }}>{keyboardOpen ? "收起键盘" : "键盘"}</button>}{owned && <button className="secondary-button" onClick={() => void release()}>释放控制</button>}</div></div>}
    {expanded && !isDesktop && owned && (touchMode || touchInput.current) && <div className="remote-control-bar is-touch-keys"><button className="secondary-button" aria-label={keyboardOpen ? "收起远程键盘" : "打开远程键盘"} onPointerDown={event => event.preventDefault()} onClick={() => { if (keyboardOpen) input.current?.blur(); else input.current?.focus({ preventScroll: true }); setKeyboardOpen(!keyboardOpen); }}>{keyboardOpen ? "收起键盘" : "键盘"}</button></div>}
    {owned && waiting && <p className="field-note" role="status">Bot 请求使用电脑，将在你停止操作 15 秒后接手。</p>}
    {notice && <p className="field-note" role="status">{notice}</p>}
    {error && <p className="error-text">{error}</p>}
    <div ref={surface} className="remote-desktop-surface" role="group" aria-label={expanded ? "远程桌面操作区域" : "共享电脑画面预览；拖动可移动"}
      onClick={() => { if (!expanded) onExpand?.(); }}
      onContextMenu={event => event.preventDefault()}
      onPointerDown={event => {
        if (event.pointerType === "touch" || event.pointerType === "pen") touchInput.current = true;
        if (!expanded) return;
        if (!session.current) { event.preventDefault(); void acquire(); return; }
        if (activePointer.current !== null) return;
        const p = point(event.clientX, event.clientY); if (!p) return;
        if (input.current) { const rect = event.currentTarget.getBoundingClientRect(); input.current.style.left = `${event.clientX - rect.left}px`; input.current.style.top = `${event.clientY - rect.top}px`; input.current.style.bottom = "auto"; }
        event.preventDefault(); activePointer.current = event.pointerId; event.currentTarget.setPointerCapture(event.pointerId);
        if ((!touchMode && !touchInput.current) || keyboardOpen) input.current?.focus({ preventScroll: true });
        pointerControl.current = event.metaKey && !["ControlLeft", "ControlRight"].some(code => pressed.current.has(code));
        if (pointerControl.current) enqueue({ type: "keydown", key: "Control_L" });
        enqueue({ type: "down", button: event.button === 2 ? 3 : event.button === 1 ? 2 : 1, ...p });
      }}
      onPointerMove={event => { const p = point(event.clientX, event.clientY); if (p && owned && expanded && (event.pointerType === "mouse" || activePointer.current === event.pointerId)) enqueue({ type: "move", ...p }); }}
      onPointerUp={event => { if (owned && expanded && activePointer.current === event.pointerId) { enqueue({ type: "up", button: event.button === 2 ? 3 : event.button === 1 ? 2 : 1, ...point(event.clientX, event.clientY) }); if (pointerControl.current) enqueue({ type: "keyup", key: "Control_L" }); pointerControl.current = false; } if (activePointer.current === event.pointerId) activePointer.current = null; if (event.currentTarget.hasPointerCapture(event.pointerId)) event.currentTarget.releasePointerCapture(event.pointerId); }}
      onPointerCancel={() => { activePointer.current = null; pointerControl.current = false; enqueue({ type: "reset" }); }}>
      {children}
      <textarea ref={input} tabIndex={owned ? 0 : -1} className="remote-keyboard-input" aria-label="远程桌面键盘输入" autoCapitalize="off" autoCorrect="off" spellCheck={false}
        onBlur={() => { composing.current = false; committed.current = ""; if (input.current) input.current.value = ""; pressed.current.clear(); enqueue({ type: "reset" }); }}
        onKeyDown={event => {
          if (!session.current) return;
          event.stopPropagation();
          lastActivity.current = Date.now();
          if (event.nativeEvent.isComposing || composing.current || event.nativeEvent.keyCode === 229 || event.key === "Process" || event.key === "Dead") return;
          committed.current = "";
          // Command is a shortcut modifier, never a held guest key. macOS can
          // omit its own keydown as well as the modified character's keyup.
          if (event.key === "Meta") { event.preventDefault(); return; }
          if ((event.ctrlKey || event.metaKey) && ["c", "x"].includes(event.key.toLowerCase())) { event.preventDefault(); void copy(event.key.toLowerCase() === "x"); return; }
          if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "v") return;
          const key = (event.key === " " && !event.ctrlKey && !event.metaKey && !event.altKey ? "" : keys[event.key]) || (/^F\d{1,2}$/.test(event.key) ? event.key : (event.ctrlKey || event.metaKey || event.altKey) && event.key.length === 1 ? event.key : "");
          if (key) {
            event.preventDefault();
            if (event.metaKey && !["Control", "Alt", "Shift"].includes(event.key)) {
              const controlHeld = pointerControl.current || ["ControlLeft", "ControlRight"].some(code => pressed.current.has(code));
              if (!controlHeld) enqueue({ type: "keydown", key: "Control_L" });
              enqueue({ type: "keydown", key });
              enqueue({ type: "keyup", key });
              if (!controlHeld) enqueue({ type: "keyup", key: "Control_L" });
            } else { pressed.current.set(event.code, key); enqueue({ type: "keydown", key }); }
          }
        }}
        onKeyUp={event => { if (!session.current) return; event.stopPropagation(); const key = pressed.current.get(event.code); if (key) { pressed.current.delete(event.code); event.preventDefault(); enqueue({ type: "keyup", key }); } }}
        onCompositionStart={() => { committed.current = ""; composing.current = true; pressed.current.clear(); enqueue({ type: "reset" }); }}
        onCompositionEnd={event => { lastActivity.current = Date.now(); composing.current = false; const text = event.currentTarget.value || event.data; committed.current = text; if (text) enqueue({ type: "text", text }); event.currentTarget.value = ""; }}
        onInput={event => { if (composing.current || (event.nativeEvent as globalThis.InputEvent).isComposing) return; const native = event.nativeEvent as globalThis.InputEvent;
          const text = event.currentTarget.value;
          // Browsers finish composition either before or after compositionend.
          // Consume only its trailing commit; a later ordinary insert of the
          // same text is a new edit, regardless of event-loop timing.
          const duplicate = !!committed.current && text === committed.current && (native.inputType?.includes("Composition") || native.inputType === "insertText" || !native.inputType);
          committed.current = "";
          if (text && !duplicate) enqueue({ type: "text", text });
          else if (!text && native.inputType === "deleteContentBackward") { enqueue({ type: "keydown", key: "BackSpace" }); enqueue({ type: "keyup", key: "BackSpace" }); }
          else if (!text && native.inputType === "deleteContentForward") { enqueue({ type: "keydown", key: "Delete" }); enqueue({ type: "keyup", key: "Delete" }); }
          event.currentTarget.value = ""; }}
        onPaste={event => { committed.current = ""; event.preventDefault(); event.stopPropagation(); const text = event.clipboardData.getData("text/plain"); if (text) enqueue({ type: "text", text }); }}
        onCopy={event => { event.preventDefault(); event.stopPropagation(); void copy(false); }}
        onCut={event => { event.preventDefault(); event.stopPropagation(); void copy(true); }} />
    </div>
    {copyText !== null && <div className="remote-copy-fallback"><p>浏览器未允许自动写入本机剪贴板，请复制以下文字。</p><textarea aria-label="从远程桌面复制的文字" readOnly value={copyText} onFocus={event => event.currentTarget.select()} /><button className="secondary-button" onClick={() => setCopyText(null)}>关闭</button></div>}
  </div>;
}
