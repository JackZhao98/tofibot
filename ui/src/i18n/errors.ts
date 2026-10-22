import { ApiError } from "../api";
import { type Namespace } from "./index";
import { codeText } from "./codeText";

export { codeText };

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
