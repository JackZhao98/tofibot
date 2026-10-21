import { useEffect, useRef, useState } from "react";
import { api, request } from "./api";
import { DesktopVideo, type DesktopVideoState } from "./DesktopVideo";
import { RemoteDesktopControl, type RemoteControlHandle } from "./RemoteDesktopControl";
import { i18n, useTranslation } from "./i18n";
import { ModalDialog } from "./overlays/ModalDialog";

export type VMOAuthSession = { session_id: string; bot_id: string; status: string; error?: string };
const endKeys = { denied: "extensions:vm.end.denied", expired: "extensions:vm.end.expired", error: "extensions:vm.end.error" } as const;
const endText = (status: string) => status in endKeys ? i18n.t(endKeys[status as keyof typeof endKeys]) : undefined;

type Props = { session: VMOAuthSession; serverName: string; title: string; onFinish: (status: string, error?: string) => void };

// The browser only receives lifecycle status. Authorization codes, state and
// tokens stay in the guest/control-plane OAuth flow.
export function VMOAuthDialog({ session, serverName, title, onFinish }: Props) {
  const { t } = useTranslation("extensions");
  const base = `/api/extensions/mcp/${encodeURIComponent(serverName)}/oauth/vm/${encodeURIComponent(session.session_id)}`;
  const control = useRef<RemoteControlHandle | null>(null);
  const finish = useRef(onFinish); finish.current = onFinish;
  const terminal = useRef(false);
  const cancellation = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
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
          if (alive) finish.current(state.status, state.error || endText(state.status));
          return;
        }
      } catch {
        if (alive) setConnectionError(i18n.t("extensions:vm.reconnecting"));
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

  async function cancel() {
    setClosing(true);
    try {
      await control.current?.release();
      await request(base + "/cancel", { method: "POST", body: "{}", signal: AbortSignal.timeout(15000) });
      terminal.current = true; finish.current("cancelled");
    } catch { setConnectionError(t("vm.cancel_failed")); setClosing(false); }
  }

  // Escape belongs to the remote computer while the person controls it, and only Cancel ends the sign-in, so neither
  // Escape nor a press outside closes this. Tab stays inside (Radix), but the keyboard receiver stops its own keys
  // from bubbling, so Tab typed into the remote computer is not trapped.
  return <ModalDialog onClose={noop} closeOnEscape={false} closeOnOutside={false} overlayClassName="mcp-oauth-overlay">
    <div className="mcp-oauth-dialog mcp-settings" role="dialog" aria-label={t("vm.dialog_label", { title })} tabIndex={-1}>
      <header><div><strong>{title}</strong><span>{t("vm.subtitle")}</span></div><button className="mcp-button" disabled={closing} onClick={() => void cancel()}>{closing ? t("vm.cancelling") : t("vm.cancel")}</button></header>
      <RemoteDesktopControl botId={session.bot_id} enabled={!closing} expanded onControlChange={setOwned} controlRef={control}>
        {videoState === "fallback" && poster && <img className="computer-screen" src={poster} alt={t("vm.screen_alt")} draggable={false}/>}
        <DesktopVideo botId={session.bot_id} enabled cursor={owned ? "hidden" : "visible"} poster={poster} className={`computer-screen${videoState === "fallback" ? " mcp-oauth-video-hidden" : ""}`} onState={setVideoState}/>
      </RemoteDesktopControl>
      <footer role="status">{connectionError || t("vm.footer")}</footer>
    </div>
  </ModalDialog>;
}

const noop = () => {};
