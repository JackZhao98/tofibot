import { useContext, useState } from "react";
import { Avatar } from "./ds/Avatar/Avatar";
import { Button } from "./ds/Button/Button";
import { SettingsRow } from "./ds/SettingsRow/SettingsRow";
import { SettingsSection } from "./ds/SettingsSection/SettingsSection";
import { useTranslation } from "./i18n";
import { SessionContext, useAccountIdentity } from "./OwnerSession";
import "./settings/general.css";

/** The account block on Settings > General: name, email and role. It lives apart from OwnerSession so the sign-in gate, which every start loads, does not carry it. */
export function OwnerAccount() {
  const { session } = useContext(SessionContext);
  const { t } = useTranslation("settings");
  const identity = useAccountIdentity();
  if (!session?.enabled || !session.owner) return null;
  const email = session.owner.email.endsWith("@account.invalid") ? "" : session.owner.email;
  const role = session.owner.role ? t(session.owner.role === "admin" ? "shell.account.role.admin" : "shell.account.role.member") : "";
  return <SettingsSection title={t("general.account")}>
    <SettingsRow fill label={t("general.name")} data-setting="account-name"><span className="gen-who"><Avatar name={identity.name} tone="cobalt" size={32} /><span>{identity.name}</span></span></SettingsRow>
    {email ? <SettingsRow fill label={t("general.email")} data-setting="account-email"><code className="gen-mono">{email}</code></SettingsRow> : null}
    {role ? <SettingsRow label={t("general.role")} data-setting="account-role"><span>{role}</span></SettingsRow> : null}
  </SettingsSection>;
}

/** Sign out, under the last section. Not offered on a server without sign-in. */
export function SignOut() {
  const { session, logout } = useContext(SessionContext);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const { t } = useTranslation("auth");
  if (!session?.enabled || !session.owner) return null;
  return <div className="gen-signout">
    <Button variant="ghost" icon="logout" busy={busy} disabled={busy} onClick={() => {
      setBusy(true); setError(""); void logout().catch(cause => setError(cause.message)).finally(() => setBusy(false));
    }}>{busy ? t("account.signing_out") : t("account.sign_out")}</Button>
    {error && <span className="gen-note gen-note--danger" role="alert">{error}</span>}
  </div>;
}
