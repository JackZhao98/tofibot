import assert from "node:assert/strict";
import { openUiModules } from "./ui-modules.mjs";

// Load through Vite so the module's i18n catalogs resolve; assertions pin the shipped zh-CN copy.
const ui = await openUiModules({ language: "zh-CN" });
try {
  const { userFormSchemaError, validateUserForm, userFormAnswerRows, userFormIdentity } = await ui.load("/src/userForm.ts");
  const field = (id, type = "text", required = true) => ({ id, label: id, type, required });
  const fields = [field("name"), field("email", "email"), field("password", "password"), field("note", "textarea", false)];
  const valid = { name: "  Ada  ", email: "ada+test@example.test", password: "  ", note: "" };
  assert.equal(userFormSchemaError(fields), "");
  assert.deepEqual(validateUserForm(fields, valid), {}, "password whitespace is significant");
  assert.equal(valid.name, "  Ada  ", "validation must not normalize the payload");
  assert.deepEqual(Object.keys(validateUserForm(fields, { ...valid, name: " \n\t", password: "" })), ["name", "password"]);
  for (const email of ["missing-at", "two@@example.test", "a b@example.test", "a@-example.test", "a@example..test", "Name <a@example.test>"]) {
    assert.ok(validateUserForm(fields, { ...valid, email }).email, `reject invalid email: ${email}`);
  }
  assert.deepEqual(validateUserForm([field("email", "email", false)], { email: "   " }), {}, "optional blank email is allowed");
  for (const type of ["text", "textarea"]) {
    assert.deepEqual(validateUserForm([field("value", type)], { value: "😀".repeat(4000) }), {}, "text limit counts Unicode code points, not UTF-16 code units");
    assert.ok(validateUserForm([field("value", type)], { value: "😀".repeat(4001) }).value);
  }
  assert.deepEqual(validateUserForm([field("value", "password")], { value: "😀".repeat(16384) }), {}, "65,536 UTF-8 bytes allowed");
  assert.ok(validateUserForm([field("value", "password")], { value: "😀".repeat(16384) + "a" }).value);
  assert.deepEqual(validateUserForm([field("value", "password")], { value: "a".repeat(65536) }), {});
  assert.ok(validateUserForm([field("value", "password")], { value: "a".repeat(65537) }).value);
  assert.ok(validateUserForm([field("value", "email", false)], { value: "a".repeat(4001) }).value);
  assert.ok(userFormSchemaError(undefined));
  assert.ok(userFormSchemaError([]));
  assert.equal(userFormSchemaError(Array.from({ length: 12 }, (_, i) => field(`f${i}`, i < 8 ? "password" : "text"))), "");
  assert.ok(userFormSchemaError(Array.from({ length: 13 }, (_, i) => field(`f${i}`))));
  assert.ok(userFormSchemaError(Array.from({ length: 9 }, (_, i) => field(`f${i}`, "password"))));
  assert.ok(userFormSchemaError([field("a"), field("a")]));
  assert.ok(userFormSchemaError([field("a", "unsupported")]));
  assert.ok(userFormSchemaError([{ ...field("a"), required: undefined }]));
  const special = [field("__proto__"), field("constructor")];
  assert.equal(userFormSchemaError(special), "");
  assert.deepEqual(Object.keys(validateUserForm(special, {})), ["__proto__", "constructor"], "prototype names must not count as supplied values");
  assert.deepEqual(validateUserForm(special, Object.fromEntries(special.map(f => [f.id, "value"]))), {});
  const answer = { name: "Ada", email: "ada@example.test", password: { secret_ref: "synthetic-reference-DO-NOT-RENDER", value_hidden: true }, note: "Line 1\nLine 2", extra: "ignored" };
  const rows = userFormAnswerRows(fields, answer);
  assert.deepEqual(rows.map(row => row.value), ["Ada", "ada@example.test", "已私密提交", "Line 1\nLine 2"]);
  assert.equal(rows.length, 4, "unknown answer keys are not displayed");
  assert.ok(!JSON.stringify(rows).includes(answer.password.secret_ref));
  assert.equal(userFormAnswerRows(fields, { password: "unexpected-plaintext" })[2].value, "已私密提交", "never render raw password values from a malformed response");
  assert.equal(userFormAnswerRows(fields, { name: answer.password })[0].value, "已私密提交", "never stringify a hidden object in an ordinary field");
  assert.ok(userFormAnswerRows(fields, undefined).every(row => row.value === "未填写"));
  const item = { question_id: "q", conversation_id: "c", bot_id: "b", run_id: "r", created_at: "2026-09-25T00:00:00Z", question: "Fill this", fields, status: "pending" };
  const identity = userFormIdentity(item);
  assert.equal(userFormIdentity({ ...item, updated_at: "later", answer }), identity, "polling does not reset a draft or include answers in identity");
  for (const key of ["question_id", "conversation_id", "bot_id", "run_id", "created_at", "question", "source_url", "status"]) {
    assert.notEqual(userFormIdentity({ ...item, [key]: "changed" }), identity, `${key} changes clear the old draft`);
  }
  assert.notEqual(userFormIdentity({ ...item, fields: [field("name", "password")] }), identity, "field schema changes clear old input");
  // Shipped copy: limits are formatted in the UI language.
  assert.equal(userFormSchemaError([]), "表单需包含 1–12 个字段，请让 Bot 重新发起。");
  assert.equal(validateUserForm([field("value", "password")], { value: "a".repeat(65537) }).value, "密码不能超过 65,536 字节，请缩短后重试。");
  assert.equal(validateUserForm([field("value")], { value: "a".repeat(4001) }).value, "不能超过 4,000 个字符，请缩短后重试。");
  assert.equal(validateUserForm([field("value")], {}).value, "请填写此项。");
  await ui.setLanguage("en");
  assert.equal(validateUserForm([field("value")], {}).value, "Fill in this field.");
  assert.equal(userFormAnswerRows(fields, undefined)[0].value, "Not filled in");
  await ui.setLanguage("zh-CN");
  console.log("user form checks: PASS (schema, required/email, Unicode/byte boundaries, private summaries, identity, zh-CN/en copy)");
} finally {
  await ui.close();
}
