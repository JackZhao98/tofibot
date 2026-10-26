import { request } from "./api";
import { errorText } from "./i18n/errors";
import { preferenceToServer, setLanguagePreference, type LanguagePreference } from "./i18n";

/**
 * A language choice applies at once on this device, then saves to the account so other devices follow.
 * Shared by Settings > General and the account menu. Resolves to the failure text, or "" when saved.
 */
export async function chooseLanguage(next: LanguagePreference, failed: string): Promise<string> {
  await setLanguagePreference(next);
  try { await request("/api/preferences", { method: "PUT", body: JSON.stringify({ language: preferenceToServer(next) }) }); return ""; }
  catch (cause) { return errorText(cause, "settings", failed); }
}
