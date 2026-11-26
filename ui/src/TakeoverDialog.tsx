import { useEffect, useRef, useState } from "react";
import { api } from "./api";
import { DesktopVideo, type DesktopVideoState } from "./DesktopVideo";
import { RemoteDesktopControl, type RemoteControlHandle } from "./RemoteDesktopControl";
import { useTranslation } from "./i18n";
import "./handover.css";
import "./desktop-screen.css";
import { BotAvatar } from "./BotAvatar";
import { Button } from "./ds/Button/Button";
import { Dialog, DIALOG_SCRIM } from "./ds/Dialog/Dialog";
import { DriverSwitch } from "./ds/DriverSwitch/DriverSwitch";
import { ScreenPanel } from "./ds/ScreenPanel/ScreenPanel";
import { ModalDialog } from "./overlays/ModalDialog";

type Props = { botId: string; questionId: string; site: string; prompt: string; busy: boolean; error: string; onResult: (result: "done" | "cannot") => void; onClose: () => void };

/**
 * The person's side of a takeover card: the Bot's shared computer in a modal.
 * A modal (not the side panel) because the ask lives in the chat, the panel does
 * not exist on a phone, and the person must leave explicitly: Done, Can't do it,
 * or back to chat with the request still open. Keystrokes and clipboard go to
 * the computer only; the answer sent to the server is one of two words.
 */
export function TakeoverDialog({ botId, questionId, site, prompt, busy, error, onResult, onClose }: Props) {
  const { t } = useTranslation("tasks");
  const { t: tc } = useTranslation("computer");
  const control = useRef<RemoteControlHandle | null>(null);
  const pane = useRef<HTMLDivElement>(null);
  const [owned, setOwned] = useState(false);
  const [poster, setPoster] = useState("");
  const [videoState, setVideoState] = useState<DesktopVideoState>("connecting");
  const video = useRef(videoState); video.current = videoState;

  useEffect(() => { void control.current?.acquire(); }, []);

  useEffect(() => {
    let alive = true;
    let timer: ReturnType<typeof setTimeout>;
    const capture = async () => {
      if (!document.hidden && video.current !== "live") {
        try {
          const response = await api.computerAction({ bot_id: botId, action: "desktop.capture", args: {} });
          const url = (response.result as { image_url?: string } | undefined)?.image_url;
          if (response.ok && url?.startsWith("data:image/")) {
            const frame = new Image(); frame.src = url; await frame.decode();
            if (alive) setPoster(url);
          }
        } catch { /* Keep the last frame while the live stream reconnects. */ }
      }
      if (alive) timer = setTimeout(capture, 1500);
    };
    void capture();
    return () => { alive = false; clearTimeout(timer); };
  }, [botId]);


  // Hand the screen back before the answer travels, so the resumed run never queues behind this hold.
  async function finish(result: "done" | "cannot") { await control.current?.release(); onResult(result); }
  async function leave() { await control.current?.release(); onClose(); }

  // The shared Radix modal keeps Tab inside and returns focus. Escape and a press outside never close it: the keyboard
  // belongs to the remote computer, and the person leaves with Back to chat, Can't do it or Done. The device is the person's
  // (driver 你); flipping its switch back hands the computer over and returns to the chat.
  return <ModalDialog onClose={() => void leave()} overlayClassName={DIALOG_SCRIM} closeOnEscape={false} closeOnOutside={false} initialFocus={pane}>
    <Dialog ref={pane} className="takeover-dialog" aria-label={t("handover.dialog.label", { site })} tabIndex={-1} data-question-id={questionId} busy={busy}
      title={site || t("handover.dialog.subtitle")} onClose={() => void leave()} closeLabel={t("handover.dialog.close")}
      actions={<>
        <Button variant="secondary" disabled={busy} onClick={() => void finish("cannot")}>{t("handover.cannot")}</Button>
        <Button disabled={busy} onClick={() => void finish("done")}>{t("handover.done")}</Button>
      </>}>
      <p className="takeover-dialog-ask">{prompt}</p>
      <ScreenPanel size="large" driver="you" title={t("handover.dialog.screen_alt")}
        chin={<DriverSwitch driver="you" disabled={busy} label={tc("desktop.control.release")} youLabel={tc("desktop.control.you")} bot={<BotAvatar id={botId} mini />} onToggle={() => void leave()} />}>
        <RemoteDesktopControl botId={botId} enabled expanded handoverQuestion={questionId} onControlChange={setOwned} controlRef={control}>
          {videoState === "fallback" && poster && <img className="computer-screen" src={poster} alt={t("handover.dialog.screen_alt")} draggable={false} />}
          <DesktopVideo botId={botId} enabled cursor={owned ? "hidden" : "visible"} poster={poster} className={`computer-screen${videoState === "fallback" ? " handover-video-hidden" : ""}`} onState={setVideoState} />
        </RemoteDesktopControl>
      </ScreenPanel>
      <p className="takeover-dialog-note" role="status">{error || t("handover.dialog.footer")}</p>
    </Dialog>
  </ModalDialog>;
}
