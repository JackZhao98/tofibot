import { Toast } from "./ds/Toast/Toast";
import { useTranslation } from "./i18n";
import { InTopLayer } from "./layers";

/** A workspace notice that stays until its cause is gone: "Couldn't refresh the workspace" with Retry, or a named Bot browser
 * failure. The kit Toast (tone error, one action, role="alert"), above every modal (--z-toast), and rendered inside an open native
 * <dialog> (image lightbox, file preview) so it is not hidden under the browser's top layer (tokens.md Z7). Not Radix Toast: a Toast
 * is a dismissable layer that takes Escape from the dialog under it, and Escape would hide an error the person has not dealt with. */
export function WorkspaceToast({ onRetry, message, failure }: { onRetry?: () => void; message?: string; failure?: string }) {
  const { t } = useTranslation(["chat", "common"]);
  return <InTopLayer><Toast tone="error" className="workspace-status workspace-toast" data-browser-panel-failure={failure} action={onRetry ? t("common:action.retry") : undefined} onAction={onRetry}>{message ?? t("workspace.refresh_failed")}</Toast></InTopLayer>;
}
