import { useState } from "react";
import { useTranslation } from "../i18n";
import { Disclosure } from "../InteractionPrimitives";
import { Banner } from "../settings/components";
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
          <label>{t("desktop_pages.setup.server")}<input name="serverURL" inputMode="url" autoComplete="url" defaultValue={state.values.serverURL} placeholder={t("desktop_pages.setup.url_placeholder")} required autoFocus/></label>
          <button className="primary-button" type="submit" disabled={busy}>{t(busy ? "desktop_pages.setup.connecting" : "desktop_pages.setup.connect")}</button>
        </fieldset>
      </form>
      <Disclosure title={t("desktop_pages.setup.ssh")} defaultOpen={state.values.connectionMode === "ssh"}>
        <form method="post" action="/__desktop/setup" onSubmit={() => setSubmitting(true)}><fieldset disabled={state.checking}>
          <input type="hidden" name="connectionMode" value="ssh"/>
          <label>{t("desktop_pages.setup.alias")}<input name="sshAlias" autoComplete="off" defaultValue={state.values.sshAlias} required/></label>
          <label>{t("desktop_pages.setup.port")}<input name="remotePort" type="number" min="1" max="65535" defaultValue={state.values.remotePort ?? "8333"} required/></label>
          <button className="st-button" type="submit" disabled={busy}>{t(busy ? "desktop_pages.setup.connecting" : "desktop_pages.setup.ssh")}</button>
        </fieldset></form>
      </Disclosure>
      <p className="settings-sec-desc">{t("desktop_pages.setup.note")}</p>
    </>}
  </PageShell>;
}
