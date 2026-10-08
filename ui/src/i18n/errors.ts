import { ApiError } from "../api";
import { i18n, type Namespace } from "./index";

/**
 * Words for a failure, from its stable server code when one is catalogued.
 * Lookup order: `<ns>:apiError.<code>`, `common:apiError.<code>`, the server's
 * own message (English prose, kept as a last resort), then `fallback`.
 * Store codes or this result at render time; do not concatenate it into sentences.
 */
export function errorText(cause: unknown, ns: Namespace, fallback: string): string {
  if (cause instanceof ApiError && cause.code && /^[a-z0-9_]+$/.test(cause.code)) {
    for (const key of [`${ns}:apiError.${cause.code}`, `common:apiError.${cause.code}`]) {
      if (i18n.exists(key)) return i18n.t(key, { defaultValue: fallback });
    }
  }
  if (cause instanceof Error && cause.message) return cause.message;
  return fallback;
}
