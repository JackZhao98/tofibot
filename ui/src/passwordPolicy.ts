import blocklist from "./password-blocklist.json";

/**
 * Mirror of internal/app/password_policy.go. The blocklist file is byte-identical
 * to the server's (a Go test enforces it); the server stays the authority, this
 * only drives the live checklist.
 */
export const PASSWORD_MIN_CHARS = 12;
export const PASSWORD_MAX_BYTES = 1024;
const IDENTITY_CONTAINS_MIN = 3;

const common = new Set((blocklist as string[]).map(item => item.toLowerCase()));

export function identityTokens(username: string, email: string): string[] {
  const tokens: string[] = [];
  const add = (value: string) => { const v = value.trim().toLowerCase(); if (v) tokens.push(v); };
  add(username);
  add(email);
  const at = email.lastIndexOf("@");
  if (at > 0) add(email.slice(0, at));
  return tokens;
}

/** A short unit repeated ("aaaaaaaaaaaa") or one straight run ("abcdefghijklm"). */
function isPattern(lower: string): boolean {
  const runes = Array.from(lower);
  for (let unit = 1; unit <= 4 && unit < runes.length; unit++) {
    if (runes.length % unit !== 0) continue;
    if (runes.every((rune, i) => i < unit || rune === runes[i - unit])) return true;
  }
  if (runes.length > 1) {
    const codes = runes.map(rune => rune.codePointAt(0)!);
    const step = codes[1] - codes[0];
    if ((step === 1 || step === -1) && codes.every((code, i) => i < 2 || code - codes[i - 1] === step)) return true;
  }
  return false;
}

export function isCommonPassword(password: string): boolean {
  const lower = password.toLowerCase();
  return common.has(lower) || isPattern(lower);
}

export type PasswordRules = { length: boolean; tooLong: boolean; common: boolean; identity: boolean };

/** Each flag is true when the rule is satisfied. */
export function passwordRules(password: string, username: string, email: string): PasswordRules {
  const bytes = new TextEncoder().encode(password).length;
  const lower = password.toLowerCase();
  const identity = !identityTokens(username, email).some(token => lower === token || (Array.from(token).length >= IDENTITY_CONTAINS_MIN && lower.includes(token)));
  return {
    length: Array.from(password).length >= PASSWORD_MIN_CHARS && bytes <= PASSWORD_MAX_BYTES,
    tooLong: bytes > PASSWORD_MAX_BYTES,
    common: !isCommonPassword(password),
    identity,
  };
}

export const USERNAME_PATTERN = /^[\p{L}\p{N}_.-]{3,64}$/u;
/** Loose on purpose: the server parses addresses strictly and answers per field. */
export const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+$/;
