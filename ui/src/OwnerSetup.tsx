import { useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import { Trans, useTranslation } from "./i18n";
import { AuthError, authRequest, verifySetupKey, type AuthField } from "./ownerAuthApi";
import { EMAIL_PATTERN, passwordRules, validUsername } from "./passwordPolicy";
import "./owner-setup.css";

type Field = AuthField | "confirm" | "key";
type Values = { username: string; email: string; password: string; confirm: string };

/** Live password checklist. States stay neutral until the first keystroke, then follow every change. */
export function PasswordChecklist({ id, password, username, email }: { id: string; password: string; username: string; email: string }) {
  const { t } = useTranslation("auth");
  const rules = useMemo(() => passwordRules(password, username, email), [password, username, email]);
  const typed = password.length > 0;
  const items = [
    { key: "length", ok: rules.length, label: t("setup.rule.length") },
    { key: "common", ok: rules.common, label: t("setup.rule.common") },
    { key: "identity", ok: rules.identity, label: t("setup.rule.identity") },
  ] as const;
  return <ul id={id} className="pw-rules" aria-label={t("setup.rules_label")}>
    {items.map(item => {
      const state = !typed ? "idle" : item.ok ? "ok" : "bad";
      return <li key={item.key} data-rule={item.key} data-state={state}>
        <span className="pw-rule-mark" aria-hidden="true">{state === "ok" ? "✓" : state === "bad" ? "✕" : "○"}</span>
        <span>{item.label}</span>
        <span className="sr-only">{t(`setup.rule_state.${state}`)}</span>
      </li>;
    })}
  </ul>;
}

function FieldShell({ id, label, error, hint, children }: { id: string; label: string; error?: string; hint?: ReactNode; children: ReactNode }) {
  return <div className="owner-field" data-invalid={error ? "true" : undefined}>
    <label htmlFor={id}>{label}</label>
    {children}
    {hint}
    {error && <p id={`${id}-error`} className="field-error" role="alert">{error}</p>}
  </div>;
}

/** First-run setup: step 1 verifies the one-time key, step 2 registers the first Admin. */
export function OwnerSetup({ onDone }: { onDone: () => Promise<void> }) {
  const { t } = useTranslation("auth");
  const uid = useId();
  const [step, setStep] = useState<1 | 2>(1);
  const [key, setKey] = useState("");
  const [keyError, setKeyError] = useState("");
  const [values, setValues] = useState<Values>({ username: "", email: "", password: "", confirm: "" });
  const [touched, setTouched] = useState<Partial<Record<Field, boolean>>>({});
  const [submitted, setSubmitted] = useState(false);
  const [server, setServer] = useState<Partial<Record<AuthField, string>>>({});
  const [formError, setFormError] = useState("");
  const [pending, setPending] = useState(false);
  const keyRef = useRef<HTMLInputElement>(null);
  const usernameRef = useRef<HTMLInputElement>(null);
  const ids = { key: `${uid}-key`, username: `${uid}-username`, email: `${uid}-email`, password: `${uid}-password`, confirm: `${uid}-confirm`, rules: `${uid}-rules` };

  useEffect(() => { (step === 1 ? keyRef : usernameRef).current?.focus(); }, [step]);

  const rules = passwordRules(values.password, values.username.trim(), values.email.trim());
  const shown = (field: Field) => touched[field] || submitted;
  const clientErrors: Partial<Record<Field, string>> = {};
  if (!values.username.trim()) clientErrors.username = t("setup.error.username_required");
  else if (!validUsername(values.username.trim())) clientErrors.username = t("error.invalid_username");
  if (!values.email.trim()) clientErrors.email = t("setup.error.email_required");
  else if (!EMAIL_PATTERN.test(values.email.trim())) clientErrors.email = t("error.invalid_email");
  if (!values.password) clientErrors.password = t("setup.error.password_required");
  else if (rules.tooLong) clientErrors.password = t("setup.error.password_too_long");
  else if (!rules.length || !rules.common || !rules.identity) clientErrors.password = t("setup.error.password_rules");
  if (!values.confirm) clientErrors.confirm = t("setup.error.confirm_required");
  else if (values.confirm !== values.password) clientErrors.confirm = t("setup.error.confirm_mismatch");
  const errorFor = (field: AuthField | "confirm"): string | undefined => {
    if (field !== "confirm" && server[field]) return server[field];
    return shown(field) ? clientErrors[field] : undefined;
  };
  const set = (field: keyof Values, value: string) => {
    setValues(current => ({ ...current, [field]: value }));
    if (field !== "confirm") setServer(current => ({ ...current, [field]: undefined }));
    // An identity change can flip a password finding the server reported.
    if (field === "username" || field === "email") setServer(current => ({ ...current, password: undefined }));
    setFormError("");
  };
  const blur = (field: Field) => setTouched(current => ({ ...current, [field]: true }));
  const invalid = (field: AuthField | "confirm") => errorFor(field) ? true : undefined;
  const describedBy = (field: AuthField | "confirm", extra?: string) => [extra, errorFor(field) ? `${ids[field]}-error` : undefined].filter(Boolean).join(" ") || undefined;

  async function verify(event: React.FormEvent) {
    event.preventDefault();
    if (pending) return;
    if (!key.trim()) { setKeyError(t("setup.error.key_required")); keyRef.current?.focus(); return; }
    setPending(true); setKeyError("");
    try { await verifySetupKey(key.trim()); setStep(2); }
    catch (cause) { setKeyError(cause instanceof Error ? cause.message : t("error.unavailable")); keyRef.current?.focus(); }
    finally { setPending(false); }
  }

  async function register(event: React.FormEvent) {
    event.preventDefault();
    if (pending) return;
    setSubmitted(true); setFormError("");
    const firstBad = (["username", "email", "password", "confirm"] as const).find(field => clientErrors[field]);
    if (firstBad) { document.getElementById(ids[firstBad])?.focus(); return; }
    setPending(true);
    try {
      await authRequest("setup", { username: values.username.trim(), email: values.email.trim(), password: values.password, bootstrap_secret: key.trim() });
      // The workspace opens only after a fresh session check (the gate's job).
      await onDone();
    } catch (cause) {
      if (cause instanceof AuthError && cause.field) {
        setServer({ [cause.field]: cause.message });
        document.getElementById(ids[cause.field])?.focus();
      } else if (cause instanceof AuthError && cause.code === "invalid_bootstrap") {
        setStep(1); setKeyError(cause.message);
      } else setFormError(cause instanceof Error ? cause.message : t("error.login_failed"));
    } finally { setPending(false); }
  }

  const head = <>
    <h1>{t("gate.title.welcome_new")}</h1>
    <p className="owner-step" aria-hidden="true">{t("setup.step", { current: step, total: 2 })}</p>
    <p className="owner-subtitle">{step === 1 ? t("setup.key_subtitle") : t("gate.subtitle.create_admin")}</p>
  </>;

  if (step === 1) return <>
    {head}
    <form onSubmit={verify} noValidate data-step="key">
      <p id={`${ids.key}-hint`} className="owner-hint"><Trans t={t} i18nKey="setup.key_hint" components={{ code: <code /> }} /></p>
      <FieldShell id={ids.key} label={t("field.setup_key")} error={keyError || undefined}>
        <input ref={keyRef} id={ids.key} name="bootstrap_secret" type="password" autoComplete="off" spellCheck={false} autoCapitalize="none" required disabled={pending}
          value={key} aria-invalid={keyError ? true : undefined} aria-describedby={[`${ids.key}-hint`, keyError ? `${ids.key}-error` : ""].filter(Boolean).join(" ")}
          onChange={event => { setKey(event.target.value); setKeyError(""); }} />
      </FieldShell>
      <button className="primary-button" disabled={pending}>{pending ? t("setup.verifying") : t("setup.continue")}</button>
    </form>
  </>;

  return <>
    {head}
    <form onSubmit={register} noValidate data-step="register">
      <FieldShell id={ids.username} label={t("field.username")} error={errorFor("username")}>
        <input ref={usernameRef} id={ids.username} name="username" autoComplete="username" autoCapitalize="none" spellCheck={false} required disabled={pending}
          value={values.username} aria-invalid={invalid("username")} aria-describedby={describedBy("username")}
          onChange={event => set("username", event.target.value)} onBlur={() => blur("username")} />
      </FieldShell>
      <FieldShell id={ids.email} label={t("field.email")} error={errorFor("email")}>
        <input id={ids.email} name="email" type="email" autoComplete="email" autoCapitalize="none" spellCheck={false} required disabled={pending}
          value={values.email} aria-invalid={invalid("email")} aria-describedby={describedBy("email")}
          onChange={event => set("email", event.target.value)} onBlur={() => blur("email")} />
      </FieldShell>
      <FieldShell id={ids.password} label={t("field.password")} error={errorFor("password")}
        hint={<PasswordChecklist id={ids.rules} password={values.password} username={values.username.trim()} email={values.email.trim()} />}>
        <input id={ids.password} name="password" type="password" autoComplete="new-password" required disabled={pending}
          value={values.password} aria-invalid={invalid("password")} aria-describedby={describedBy("password", ids.rules)}
          onChange={event => set("password", event.target.value)} onBlur={() => blur("password")} />
      </FieldShell>
      <FieldShell id={ids.confirm} label={t("field.confirm_password")} error={errorFor("confirm")}>
        <input id={ids.confirm} name="confirm_password" type="password" autoComplete="new-password" required disabled={pending}
          value={values.confirm} aria-invalid={invalid("confirm")} aria-describedby={describedBy("confirm")}
          onChange={event => set("confirm", event.target.value)} onBlur={() => blur("confirm")} />
      </FieldShell>
      {formError && <p className="owner-error" role="alert">{formError}</p>}
      <button className="primary-button" disabled={pending}>{pending ? t("gate.submit.wait") : t("gate.submit.create_account")}</button>
    </form>
  </>;
}
