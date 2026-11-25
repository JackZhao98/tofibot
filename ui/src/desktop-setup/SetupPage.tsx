import { useState } from "react";
import { useTranslation } from "../i18n";
import { Disclosure } from "../InteractionPrimitives";
import { Banner } from "../settings/components";
import { Button } from "../ds/Button/Button";
import { Field } from "../ds/Field/Field";
import { TextInput } from "../ds/TextInput/TextInput";
import { PageShell, StateNotice, usePageState } from "../desktop-pages/shared";

export type SetupState = {error: string; values: {connectionMode: "https" | "ssh"; serverURL?: string; sshAlias?: string; remotePort?: string}; checking: boolean};
export function SetupPage() {
  const {t} = useTranslation("settings");
  const {state, error} = usePageState<SetupState>("/__desktop/setup/state");
  const [submitting, setSubmitting] = useState(false);
  const busy = submitting || state?.checking === true;
  return <PageShell title={t("desktop_pages.setup.title")}>
    <p>{t("desktop_pages.setup.intro")}</p>
    {!state ? <StateNotice error={error}/> : <>
      {state.error && <Banner tone="error" title={state.error}/>}
      {state.checking && <p role="status">{t("desktop_pages.setup.checking")}</p>}
      <form method="post" action="/__desktop/setup" onSubmit={() => setSubmitting(true)}>
        <fieldset disabled={state.checking}>
          <input type="hidden" name="connectionMode" value="https"/>
          <Field label={t("desktop_pages.setup.server")}><TextInput name="serverURL" inputMode="url" autoComplete="url" defaultValue={state.values.serverURL} placeholder={t("desktop_pages.setup.url_placeholder")} required autoFocus/></Field>
          <Button variant="primary" type="submit" disabled={busy}>{t(busy ? "desktop_pages.setup.connecting" : "desktop_pages.setup.connect")}</Button>
        </fieldset>
      </form>
      <Disclosure title={t("desktop_pages.setup.ssh")} defaultOpen={state.values.connectionMode === "ssh"}>
        <form method="post" action="/__desktop/setup" onSubmit={() => setSubmitting(true)}><fieldset disabled={state.checking}>
          <input type="hidden" name="connectionMode" value="ssh"/>
          <Field label={t("desktop_pages.setup.alias")}><TextInput name="sshAlias" autoComplete="off" defaultValue={state.values.sshAlias} required/></Field>
          <Field label={t("desktop_pages.setup.port")}><TextInput name="remotePort" type="number" min="1" max="65535" defaultValue={state.values.remotePort ?? "8333"} required/></Field>
          <Button variant="secondary" type="submit" disabled={busy}>{t(busy ? "desktop_pages.setup.connecting" : "desktop_pages.setup.ssh")}</Button>
        </fieldset></form>
      </Disclosure>
      <p className="settings-sec-desc">{t("desktop_pages.setup.note")}</p>
    </>}
  </PageShell>;
}
