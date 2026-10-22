import { i18n, type Namespace } from "./index";

/**
 * Words for a stable server code, from `<ns>:apiError.<code>` then
 * `common:apiError.<code>`. Undefined when the code is not catalogued, so the
 * caller can show the server's English sentence instead. Kept apart from
 * errors.ts so api.ts can use it without importing ApiError's own module back.
 */
export function codeText(code: string | undefined, ns: Namespace = "common"): string | undefined {
  if (!code || !/^[a-z0-9_]+$/.test(code)) return undefined;
  for (const key of [`${ns}:apiError.${code}`, `common:apiError.${code}`]) {
    if (i18n.exists(key)) return i18n.t(key, { defaultValue: code });
  }
  return undefined;
}
