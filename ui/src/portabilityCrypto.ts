import { i18n } from "./i18n";

// Passwords remain in the user's browser. Never send them to an API, persist
// them, or include them in an error. This envelope encrypts the whole bundle.
const MAX_CLEAR_BYTES = 16 * 1024 * 1024;
export const MAX_PORTABLE_FILE_BYTES = 24 * 1024 * 1024;
const ITERATIONS = 310_000;
const aad = new TextEncoder().encode("tofi.encrypted:1");
type Envelope = { format: "tofi.encrypted"; version: 1; kdf: "PBKDF2-SHA256"; iterations: number; salt: string; iv: string; ciphertext: string };

function base64(data: Uint8Array): string {
  let text = "";
  for (let i = 0; i < data.length; i += 8192) text += String.fromCharCode(...data.subarray(i, i + 8192));
  return btoa(text);
}
function unbase64(text: string): Uint8Array<ArrayBuffer> {
  if (!/^[A-Za-z0-9+/]*={0,2}$/.test(text) || text.length % 4 !== 0) throw new Error("invalid encoding");
  return Uint8Array.from(atob(text), c => c.charCodeAt(0));
}
async function key(password: string, salt: Uint8Array<ArrayBuffer>) {
  if (!globalThis.crypto?.subtle) throw new Error(i18n.t("settings:portability.error.insecure_context"));
  if (password.length < 12 || password.length > 1024) throw new Error(i18n.t("settings:portability.error.password_short"));
  const material = await crypto.subtle.importKey("raw", new TextEncoder().encode(password), "PBKDF2", false, ["deriveKey"]);
  return crypto.subtle.deriveKey({ name: "PBKDF2", hash: "SHA-256", salt, iterations: ITERATIONS }, material, { name: "AES-GCM", length: 256 }, false, ["encrypt", "decrypt"]);
}

export async function encryptPortable(source: string, password: string): Promise<string> {
  const clear = new TextEncoder().encode(source);
  if (clear.length > MAX_CLEAR_BYTES) throw new Error(i18n.t("settings:portability.error.bundle_too_large"));
  const salt = crypto.getRandomValues(new Uint8Array(16));
  const iv = crypto.getRandomValues(new Uint8Array(12));
  try {
    const ciphertext = await crypto.subtle.encrypt({ name: "AES-GCM", iv, additionalData: aad }, await key(password, salt), clear);
    const envelope: Envelope = { format: "tofi.encrypted", version: 1, kdf: "PBKDF2-SHA256", iterations: ITERATIONS, salt: base64(salt), iv: base64(iv), ciphertext: base64(new Uint8Array(ciphertext)) };
    return JSON.stringify(envelope);
  } finally { clear.fill(0); }
}

export function isEncryptedPortable(source: string): boolean {
  try { return (JSON.parse(source) as { format?: string }).format === "tofi.encrypted"; } catch { return false; }
}
/** The forms of a typed passphrase to try: as typed, then without dashes/spaces in upper case (the form
 *  Tofi-generated export passphrases derive from, so they open with or without their dashes). */
export function passphraseCandidates(typed: string): string[] {
  const trimmed = typed.trim();
  const canonical = trimmed.replace(/[\s-]+/g, "").toUpperCase();
  return Array.from(new Set([typed, trimmed, canonical].filter(value => value.length > 0)));
}
export async function decryptPortable(source: string, password: string): Promise<string> {
  if (new TextEncoder().encode(source).length > MAX_PORTABLE_FILE_BYTES) throw new Error(i18n.t("settings:portability.error.file_oversized"));
  let clear: Uint8Array<ArrayBuffer> | undefined;
  let salt: Uint8Array<ArrayBuffer>, iv: Uint8Array<ArrayBuffer>, ciphertext: Uint8Array<ArrayBuffer>;
  try {
    const envelope = JSON.parse(source) as Envelope;
    if (envelope.format !== "tofi.encrypted" || envelope.version !== 1 || envelope.kdf !== "PBKDF2-SHA256" || envelope.iterations !== ITERATIONS || Object.keys(envelope).length !== 7) throw new Error("invalid envelope");
    salt = unbase64(envelope.salt); iv = unbase64(envelope.iv); ciphertext = unbase64(envelope.ciphertext);
    if (salt.length !== 16 || iv.length !== 12 || ciphertext.length > MAX_CLEAR_BYTES + 16 || ciphertext.length < 16) throw new Error("invalid envelope");
  } catch { throw new Error(i18n.t("settings:portability.error.file_damaged")); }
  try {
    for (const candidate of passphraseCandidates(password)) {
      try {
        clear = new Uint8Array(await crypto.subtle.decrypt({ name: "AES-GCM", iv, additionalData: aad }, await key(candidate, salt), ciphertext));
        return new TextDecoder("utf-8", { fatal: true }).decode(clear);
      } catch (cause) { if (cause instanceof Error && cause.message === i18n.t("settings:portability.error.insecure_context")) throw cause; }
    }
    throw new Error(i18n.t("settings:portability.error.decrypt_failed"));
  } finally { clear?.fill(0); }
}
