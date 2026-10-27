import { useRef, type ReactNode } from "react";
import { useTranslation } from "./i18n";
import { TofiIcon as Icon } from "./icons";
import { CoverContext, requestCoverClose } from "./coverGuard";
import { ModalDialog } from "./overlays/ModalDialog";
import type { CoverId } from "./surfaces";
import "./cover-shell.css";

/**
 * A cover (docs/design/widgets-and-sidebar.md section 2.3): an exclusive window over the whole workspace, with the
 * Settings window's geometry. Radix Dialog underneath (ModalDialog): focus trap, Escape, focus return, the rest hidden
 * from assistive tech. `leaving` is set while the exit plays; the workspace keeps the cover mounted for that long.
 * Every way out (the close button, Escape, a press on the scrim) goes through `requestCoverClose`, so a cover with unsaved changes
 * asks first (coverGuard.ts). The content reads `leaving` and the raw `close` from CoverContext. `new-group` is a dialog of its
 * own (NewGroupDialog): the shell draws no window for it and only hands over the state.
 * Lazy chunk: it imports Radix, which must stay out of the entry bundle.
 */
export function CoverShell({ id, leaving = false, wide = false, returnFocus, onClose, children }: {
  id: CoverId; leaving?: boolean; wide?: boolean; returnFocus?: HTMLElement | null; onClose: () => void; children: ReactNode;
}) {
  const { t } = useTranslation("chat");
  const title = t(id === "bot-settings" ? "contextCard.title.bot-edit" : id === "group-settings" ? "header.group_settings" : id === "new-group" ? "contextCard.title.group-create" : id === "artifacts" ? "contextCard.title.artifacts" : "header.remembered_approvals");
  const closeRef = useRef<HTMLButtonElement>(null);
  const state = { leaving, close: onClose, returnFocus };
  if (id === "new-group") return <CoverContext.Provider value={state}>{children}</CoverContext.Provider>;
  const close = () => requestCoverClose(onClose);
  return <CoverContext.Provider value={state}><ModalDialog onClose={close} locked={leaving} overlayClassName={`cover-scrim${leaving ? " is-leaving" : ""}`} overlayProps={{ "data-cover": id }} initialFocus={closeRef} returnFocus={returnFocus} container={document.querySelector<HTMLElement>(".workspace")}>
    <section className={`cover-window${wide ? " is-wide" : ""}${leaving ? " is-leaving" : ""}`} role="dialog" aria-label={title}>
      <header className="cover-header">
        <h2>{title}</h2>
        <button ref={closeRef} type="button" className="cover-close" aria-label={t("cover.close", { title })} data-hint={t("cover.close", { title })} onClick={close}><Icon name="close" size={18} /></button>
      </header>
      <div className="cover-body">{children}</div>
    </section>
  </ModalDialog></CoverContext.Provider>;
}
