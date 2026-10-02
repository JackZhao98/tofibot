import type { FormAnswer, Question, QuestionAnswer, QuestionField } from "./questionTimeline";

export const USER_FORM_LIMITS = { fields: 12, passwords: 8, textCharacters: 4000, passwordBytes: 65536 } as const;

/** Metadata only: drafts never become React keys, persisted state, or diagnostics. */
export function userFormIdentity(item: Question): string {
  return JSON.stringify([item.conversation_id, item.bot_id, item.run_id, item.question_id,
    item.created_at, item.question, item.source_url, item.fields, item.status]);
}

export function userFormSchemaError(fields: QuestionField[] | undefined): string {
  if (!fields?.length || fields.length > USER_FORM_LIMITS.fields) return "表单需包含 1–12 个字段，请让 Bot 重新发起。";
  if (fields.filter(field => field?.type === "password").length > USER_FORM_LIMITS.passwords) return "私密字段不能超过 8 个，请让 Bot 重新发起。";
  const ids = new Set<string>();
  for (const field of fields) {
    if (!field || typeof field.id !== "string" || !field.id.trim() || ids.has(field.id)
      || typeof field.label !== "string" || !field.label.trim()
      || !["text", "email", "password", "textarea"].includes(field.type)
      || typeof field.required !== "boolean") return "表单字段无效，请让 Bot 重新发起。";
    ids.add(field.id);
  }
  return "";
}

// Matches the single-address syntax of an HTML email input; validation does not
// normalize the payload. In particular, password whitespace is always significant.
const emailPattern = /^[a-zA-Z0-9.!#$%&'*+/=?^_`{|}~-]+@[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?(?:\.[a-zA-Z0-9](?:[a-zA-Z0-9-]*[a-zA-Z0-9])?)*$/;

export function validateUserForm(fields: QuestionField[], values: Record<string, string>): Record<string, string> {
  const errors: [string, string][] = [];
  for (const field of fields) {
    const value = Object.hasOwn(values, field.id) ? values[field.id] : "";
    const present = field.type === "password" ? value.length > 0 : value.trim().length > 0;
    if (field.required && !present) errors.push([field.id, "请填写此项。"]);
    else if (field.type === "password" && new TextEncoder().encode(value).length > USER_FORM_LIMITS.passwordBytes) errors.push([field.id, "密码不能超过 65,536 字节，请缩短后重试。"]);
    else if (field.type !== "password" && [...value].length > USER_FORM_LIMITS.textCharacters) errors.push([field.id, "不能超过 4,000 个字符，请缩短后重试。"]);
    else if (field.type === "email" && present && !emailPattern.test(value.trim())) errors.push([field.id, "请输入有效的邮箱地址。"]);
  }
  return Object.fromEntries(errors);
}

/** Never stringify an answer object or expose a password, even in a malformed response. */
export function userFormAnswerRows(fields: QuestionField[], answer: QuestionAnswer | undefined) {
  const values: FormAnswer = answer && typeof answer === "object" && !Array.isArray(answer) && !("values" in answer && Array.isArray(answer.values)) ? answer as FormAnswer : {};
  return fields.map(field => {
    const value = Object.hasOwn(values, field.id) ? values[field.id] : undefined;
    const missing = value === undefined || value === "";
    return { id: field.id, label: field.label, value: missing ? "未填写"
      : field.type === "password" || typeof value === "object" ? "已私密提交"
        : typeof value === "string" ? value : "未填写" };
  });
}
