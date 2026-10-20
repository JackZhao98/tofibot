import { ApiError } from "./api";
import { i18n } from "./i18n";

/** fetch rejects with a TypeError when no response arrived; a timed-out read rejects with a TimeoutError. */
export function isNetworkFailure(error: unknown) {
  return error instanceof TypeError || (error instanceof DOMException && (error.name === "TimeoutError" || error.name === "NetworkError"));
}

export function errorText(error: unknown) {
  if (isNetworkFailure(error)) return i18n.t("chat:error.network_unreachable");
  if (error instanceof ApiError && error.code === "archive_busy") return i18n.t("chat:apiError.archive_busy");
  if (error instanceof ApiError && error.code === "archive_blocked") return i18n.t("chat:apiError.archive_blocked");
  if (error instanceof ApiError && error.code === "no_active_members") return i18n.t("chat:apiError.no_active_members");
  if (error instanceof ApiError && error.code === "ambiguous_recipient") return i18n.t("chat:apiError.ambiguous_recipient");
  if (error instanceof ApiError && error.status === 503) {
    return error.code === "model_unconfigured" ? i18n.t("chat:apiError.model_unconfigured") : i18n.t("chat:error.service_unavailable");
  }
  return error instanceof Error ? error.message : i18n.t("chat:error.generic");
}
