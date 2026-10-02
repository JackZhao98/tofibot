import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { api, request } from "./api";
import { DesktopVideo, type DesktopVideoState } from "./DesktopVideo";
import { RemoteDesktopControl, type RemoteControlHandle } from "./RemoteDesktopControl";

export type VMOAuthSession = { session_id: string; bot_id: string; status: string; error?: string };
type Props = { session: VMOAuthSession; serverName: string; title: string; onFinish: (status: string, error?: string) => void };

// The browser only receives lifecycle status. Authorization codes, state and
// tokens stay in the guest/control-plane OAuth flow.
export function VMOAuthDialog({ session, serverName, title, onFinish }: Props) {
  const base = `/api/extensions/mcp/${encodeURIComponent(serverName)}/oauth/vm/${encodeURIComponent(session.session_id)}`;
  const control = useRef<RemoteControlHandle | null>(null);
  const finish = useRef(onFinish); finish.current = onFinish;
  const terminal = useRef(false);
  const cancellation = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const pane = useRef<HTMLDivElement>(null);
  const [owned, setOwned] = useState(false);
  const [closing, setClosing] = useState(false);
  const [connectionError, setConnectionError] = useState("");
  const [poster, setPoster] = useState("");
  const [videoState, setVideoState] = useState<DesktopVideoState>("connecting");
  const video = useRef(videoState); video.current = videoState;

  useEffect(() => {
    clearTimeout(cancellation.current);
    let alive = true;
    let timer: ReturnType<typeof setTimeout>;
    const controller = new AbortController();
    const poll = async () => {
      try {
        const state = await request<VMOAuthSession>(base + "/status", { signal: AbortSignal.any([controller.signal, AbortSignal.timeout(10000)]) });
        if (!alive) return;
        setConnectionError("");
        if (!["pending", "preparing"].includes(state.status)) {
          terminal.current = true;
          await control.current?.release();
          if (alive) finish.current(state.status, state.error || ({ denied: "授权已取消，可重新连接。", expired: "授权已超时，请重新连接。", error: "授权未完成，请重试。" } as Record<string, string>)[state.status]);
          return;
        }
      } catch {
        if (alive) setConnectionError("连接暂时中断，正在重试…");
      }
      if (alive) timer = setTimeout(poll, 1500);
    };
    void poll();
    return () => {
      alive = false; controller.abort(); clearTimeout(timer);
      // Deferring lets React's development effect replay retain the session.
      cancellation.current = setTimeout(() => {
        if (!terminal.current) void request(base + "/cancel", { method: "POST", body: "{}", keepalive: true }).catch(() => {});
      }, 0);
    };
  }, [base]);

  useEffect(() => {
    let alive = true;
    let timer: ReturnType<typeof setTimeout>;
    const capture = async () => {
      if (!document.hidden && video.current !== "live") {
        try {
          const response = await api.computerAction({ bot_id: session.bot_id, action: "desktop.capture", args: {} });
          const url = (response.result as { image_url?: string } | undefined)?.image_url;
          if (response.ok && url?.startsWith("data:image/")) {
            const frame = new Image(); frame.src = url; await frame.decode();
            if (alive) setPoster(url);
          }
        } catch { /* Keep the last decoded frame while the live stream reconnects. */ }
      }
      if (alive) timer = setTimeout(capture, 1500);
    };
    void capture();
    return () => { alive = false; clearTimeout(timer); };
  }, [session.bot_id]);

  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null;
    pane.current?.focus();
    const keys = (event: KeyboardEvent) => {
      // Escape belongs to the remote computer while the user controls it.
      if (event.key === "Escape" || event.key !== "Tab" || event.defaultPrevented) return;
      const targets = Array.from(pane.current?.querySelectorAll<HTMLElement>('button:not([disabled]), textarea:not([tabindex="-1"])') ?? []).filter(el => el.getClientRects().length);
      const i = targets.indexOf(document.activeElement as HTMLElement);
      if (i < 0 || (!event.shiftKey && i === targets.length - 1) || (event.shiftKey && i === 0)) {
        event.preventDefault(); (event.shiftKey ? targets.at(-1) : targets[0])?.focus();
      }
    };
    document.addEventListener("keydown", keys);
    return () => { document.removeEventListener("keydown", keys); previous?.focus(); };
  }, []);

  async function cancel() {
    setClosing(true);
    try {
      await control.current?.release();
      await request(base + "/cancel", { method: "POST", body: "{}", signal: AbortSignal.timeout(15000) });
      terminal.current = true; finish.current("cancelled");
    } catch { setConnectionError("暂时无法取消，请重试。授权会在超时后自动结束。"); setClosing(false); }
  }

  return createPortal(<div className="mcp-oauth-overlay">
    <div ref={pane} className="mcp-oauth-dialog mcp-settings" role="dialog" aria-modal="true" aria-label={`${title} 授权`} tabIndex={-1}>
      <header><div><strong>{title}</strong><span>在共享电脑中完成授权</span></div><button className="mcp-button" disabled={closing} onClick={() => void cancel()}>{closing ? "取消中…" : "取消授权"}</button></header>
      <RemoteDesktopControl botId={session.bot_id} enabled={!closing} expanded onControlChange={setOwned} controlRef={control}>
        {videoState === "fallback" && poster && <img className="computer-screen" src={poster} alt="共享电脑" draggable={false}/>}
        <DesktopVideo botId={session.bot_id} enabled cursor={owned ? "hidden" : "visible"} poster={poster} className={`computer-screen${videoState === "fallback" ? " mcp-oauth-video-hidden" : ""}`} onState={setVideoState}/>
      </RemoteDesktopControl>
      <footer role="status">{connectionError || "完成后自动返回；其他 Bot 的图形操作正在排队。"}</footer>
    </div>
  </div>, document.body);
}
