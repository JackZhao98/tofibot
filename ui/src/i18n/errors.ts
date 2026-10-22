import { ApiError } from "../api";
import { i18n, type Namespace } from "./index";

/**
 * Words for a failure, from its stable server code when one is catalogued.
 * Lookup order: `<ns>:apiError.<code>`, `common:apiError.<code>`, the server's
 * own message (English prose, kept as a last resort), then `fallback`.
 * Store codes or this result at render time; do not concatenate it into sentences.
 */
export function errorText(cause: unknown, ns: Namespace, fallback: string): string {
  if (cause instanceof ApiError) {
    const text = codeText(cause.code, ns);
    if (text) return text;
  }
  if (cause instanceof Error && cause.message) return cause.message;
  return fallback;
}

/**
 * Words for a stable server code carried outside an error response (a task's
 * `failure_code`, an execution's `error_code`, a diagnostic's `code`), from the
 * same catalogue as `errorText`. Undefined when the code is not catalogued, so
 * the caller can show the server's English sentence instead.
 */
export function codeText(code: string | undefined, ns: Namespace): string | undefined {
  if (!code || !/^[a-z0-9_]+$/.test(code)) return undefined;
  for (const key of [`${ns}:apiError.${code}`, `common:apiError.${code}`]) {
    if (i18n.exists(key)) return i18n.t(key, { defaultValue: code });
  }
  return undefined;
}
