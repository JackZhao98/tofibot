import { useRef } from "react";
import { TofiIcon } from "./icons";
import { useTranslation } from "./i18n";
import { OPEN_COMPUTER_SETTINGS_EVENT } from "./settings/deepLinks";
import type { AttachmentProblem } from "./filePreviewModel";
import "./attachment-notice.css";

/**
 * Why a stored file will not open: its bytes are only on a computer that is detached, or the computer no longer has
 * them (computer-decoupling S1/S2). One sentence, plus a way to the computer settings when attaching helps. It opens
 * and closes by animating its own height and opacity (--m-panel-in / --m-panel-out); `problem === null` closes it.
 * `onNavigate` runs before Settings opens, so a file viewer can step out of the way.
 */
export function AttachmentNotice({ problem, onNavigate }: { problem: AttachmentProblem | null; onNavigate?: () => void }) {
  const { t } = useTranslation("chat");
  // Keep the last problem's words while the notice closes.
  const last = useRef<AttachmentProblem | null>(problem);
  if (problem) last.current = problem;
  const shown = last.current;
  if (!shown) return null;
  const open = problem !== null;
  return <div className="attachment-notice-wrap" data-open={open} inert={!open}>
    <div className="attachment-notice-body">
      <div className="attachment-notice" role={open ? "alert" : undefined}>
        <TofiIcon name={shown === "file_on_detached_computer" ? "unplug" : "alert"} size={16} aria-hidden="true" />
        <p>{t(`attachment.problem.${shown}`)}</p>
        {shown === "file_on_detached_computer" && <button type="button" className="text-button" onClick={() => { onNavigate?.(); window.dispatchEvent(new Event(OPEN_COMPUTER_SETTINGS_EVENT)); }}>{t("attachment.problem.open_computer")}</button>}
      </div>
    </div>
  </div>;
}
