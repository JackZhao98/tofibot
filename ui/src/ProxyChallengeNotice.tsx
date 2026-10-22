import { useTranslation } from "./i18n";
import { InTopLayer } from "./layers";

/** The site's protection (e.g. Cloudflare) wants a fresh check and silent renewal did not work. Same shape as WorkspaceToast; "Verify again" reloads the page at the current URL. Loaded only when raised (ProxyChallengeSlot). */
export function ProxyChallengeBanner() {
  const { t } = useTranslation("common");
  return <InTopLayer><div className="stream-status workspace-status workspace-toast proxy-challenge-toast" role="status">{t("apiError.proxy_challenge")}<button className="secondary-button" onClick={() => window.location.reload()}>{t("action.verify_again")}</button></div></InTopLayer>;
}
