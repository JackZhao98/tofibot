import { i18n } from "./i18n";

export type OAuthOptions = { vm_available: boolean; web_callback_origin: string; desktop_redirect_uri: string };
export type OAuthRoute = { mode: "desktop" | "web" | "vm" | "paste" | "blocked"; note: string };

/**
 * Which sign-in route this client takes. `loopbackOnly` is the connection's own property (catalog `oauthRedirect`,
 * stored as `oauth.redirect_policy`, or learned when the provider refused a public redirect): the provider accepts only
 * a 127.0.0.1 redirect, so the https popup is never an option. The native app already redirects to 127.0.0.1; every
 * other client signs in in its own browser and pastes the redirected address back. That needs nothing from the server
 * but the fixed port, so it works on every install (decision 0055).
 */
export function mcpOAuthRoute(protocol: string, desktop: boolean, nativeOAuth: boolean, options: OAuthOptions | null, loopbackOnly = false): OAuthRoute {
  if (desktop) return nativeOAuth
    ? { mode: "desktop", note: i18n.t("extensions:route.desktop") }
    : { mode: "blocked", note: i18n.t("extensions:route.desktop_outdated") };
  if (loopbackOnly) return { mode: "paste", note: i18n.t("extensions:route.loopback_paste") };
  if (protocol === "https:") return options?.web_callback_origin
    ? { mode: "web", note: i18n.t("extensions:route.web") }
    : { mode: "blocked", note: options === null ? i18n.t("extensions:route.loading") : i18n.t("extensions:route.public_origin_missing") };
  return options?.vm_available
    ? { mode: "vm", note: i18n.t("extensions:route.vm") }
    : { mode: "blocked", note: options === null ? i18n.t("extensions:route.loading") : i18n.t("extensions:route.http_blocked") };
}
