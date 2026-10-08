import type { FormAnswer, Question, QuestionAnswer, QuestionField } from "./questionTimeline";
import { i18n } from "./i18n";
import { formatNumber } from "./i18n/format";

export const USER_FORM_LIMITS = { fields: 12, passwords: 8, textCharacters: 4000, passwordBytes: 65536 } as const;

const formT = () => i18n.getFixedT(null, "tasks");

/** Metadata only: drafts never become React keys, persisted state, or diagnostics. */
export function userFormIdentity(item: Question): string {
  return JSON.stringify([item.conversation_id, item.bot_id, item.run_id, item.question_id,
    item.created_at, item.question, item.source_url, item.fields, item.status]);
}

export function userFormSchemaError(fields: QuestionField[] | undefined): string {
  const t = formT();
  if (!fields?.length || fields.length > USER_FORM_LIMITS.fields) return t("form.schema.field_count", { min: 1, max: USER_FORM_LIMITS.fields });
  if (fields.filter(field => field?.type === "password").length > USER_FORM_LIMITS.passwords) return t("form.schema.too_many_private", { max: USER_FORM_LIMITS.passwords });
  const ids = new Set<string>();
  for (const field of fields) {
    if (!field || typeof field.id !== "string" || !field.id.trim() || ids.has(field.id)
      || typeof field.label !== "string" || !field.label.trim()
      || !["text", "email", "password", "textarea"].includes(field.type)
      || typeof field.required !== "boolean") return t("form.schema.invalid_field");
    ids.add(field.id);
  }
  return "";
}

// Matches the single-address syntax of an HTML email input; validation does not
// normalize the payload. In particular, password whitespace is always significant.
const emailPattern = /^[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?)*$/;

export function validateUserForm(fields: QuestionField[], values: Record<string, string>): Record<string, string> {
  const t = formT();
  const errors: [string, string][] = [];
  for (const field of fields) {
    const value = Object.hasOwn(values, field.id) ? values[field.id] : "";
    const present = field.type === "password" ? value.length > 0 : value.trim().length > 0;
    if (field.required && !present) errors.push([field.id, t("form.error.required")]);
    else if (field.type === "password" && new TextEncoder().encode(value).length > USER_FORM_LIMITS.passwordBytes) errors.push([field.id, t("form.error.password_too_long", { limit: formatNumber(USER_FORM_LIMITS.passwordBytes) })]);
    else if (field.type !== "password" && [...value].length > USER_FORM_LIMITS.textCharacters) errors.push([field.id, t("form.error.text_too_long", { limit: formatNumber(USER_FORM_LIMITS.textCharacters) })]);
    else if (field.type === "email" && present && !emailPattern.test(value.trim())) errors.push([field.id, t("form.error.invalid_email")]);
  }
  return Object.fromEntries(errors);
}

/** Never stringify an answer object or expose a password, even in a malformed response. */
export function userFormAnswerRows(fields: QuestionField[], answer: QuestionAnswer | undefined) {
  const t = formT();
  const values: FormAnswer = answer && typeof answer === "object" && !Array.isArray(answer) && !("values" in answer && Array.isArray(answer.values)) ? answer as FormAnswer : {};
  return fields.map(field => {
    const value = Object.hasOwn(values, field.id) ? values[field.id] : undefined;
    const missing = value === undefined || value === "";
    return { id: field.id, label: field.label, value: missing ? t("form.answer.missing")
      : field.type === "password" || typeof value === "object" ? t("form.answer.private")
        : typeof value === "string" ? value : t("form.answer.missing") };
  });
}
