import { useTranslation } from "./i18n";
import { InTopLayer } from "./layers";

/** "Couldn't refresh the workspace" with Retry. Above every modal (--z-toast), and rendered inside an open native
 * <dialog> (image lightbox, file preview) so it is not hidden under the browser's top layer (tokens.md Z7). */
export function WorkspaceToast({ onRetry }: { onRetry: () => void }) {
  const { t } = useTranslation(["chat", "common"]);
  return <InTopLayer><div className="stream-status workspace-status workspace-toast" role="alert">{t("workspace.refresh_failed")}<button className="secondary-button" onClick={onRetry}>{t("common:action.retry")}</button></div></InTopLayer>;
}
