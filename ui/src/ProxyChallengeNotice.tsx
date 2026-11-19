import { Toast } from "./ds/Toast/Toast";
import { useTranslation } from "./i18n";
import { InTopLayer } from "./layers";

/** The site's protection (e.g. Cloudflare) wants a fresh check and silent renewal did not work. The same kit Toast as WorkspaceToast (tone error, role="status" because the page is not broken, only waiting); "Verify again" reloads the page at the current URL. Loaded only when raised (ProxyChallengeSlot). */
export function ProxyChallengeBanner() {
  const { t } = useTranslation("common");
  return <InTopLayer><Toast tone="error" role="status" className="workspace-status workspace-toast proxy-challenge-toast" action={t("action.verify_again")} onAction={() => window.location.reload()}>{t("apiError.proxy_challenge")}</Toast></InTopLayer>;
}
