import { i18n } from "./i18n";

export type OAuthOptions = { vm_available: boolean; web_callback_origin: string; desktop_redirect_uri: string };
export type OAuthRoute = { mode: "desktop" | "web" | "vm" | "blocked"; note: string };

export function mcpOAuthRoute(protocol: string, desktop: boolean, nativeOAuth: boolean, options: OAuthOptions | null): OAuthRoute {
  if (desktop) return nativeOAuth
    ? { mode: "desktop", note: i18n.t("extensions:route.desktop") }
    : { mode: "blocked", note: i18n.t("extensions:route.desktop_outdated") };
  if (protocol === "https:") return options?.web_callback_origin
    ? { mode: "web", note: i18n.t("extensions:route.web") }
    : { mode: "blocked", note: options === null ? i18n.t("extensions:route.loading") : i18n.t("extensions:route.public_origin_missing") };
  return options?.vm_available
    ? { mode: "vm", note: i18n.t("extensions:route.vm") }
    : { mode: "blocked", note: options === null ? i18n.t("extensions:route.loading") : i18n.t("extensions:route.http_blocked") };
}
