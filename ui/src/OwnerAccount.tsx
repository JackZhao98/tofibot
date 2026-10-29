import { useContext, useState } from "react";
import { TofiIcon } from "./icons";
import { useTranslation } from "./i18n";
import { SessionContext, useAccountIdentity } from "./OwnerSession";

/** The account block on Settings > General. It lives apart from OwnerSession so the sign-in gate, which every start loads, does not carry it. */
export function OwnerAccount() {
  const { session, logout } = useContext(SessionContext);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const { t } = useTranslation("auth");
  const identity = useAccountIdentity();
  if (!session?.enabled || !session.owner) return null;
  return <section className="owner-account"><span className="owner-account-avatar" aria-hidden="true">{identity.initial}</span><div><strong>{identity.name}</strong>{identity.detail && <span>{identity.detail}</span>}</div><button className="settings-ghost-button" disabled={busy} onClick={() => {
    setBusy(true); setError(""); void logout().catch(cause => setError(cause.message)).finally(() => setBusy(false));
  }}><TofiIcon name="logout" size={16} />{busy ? t("account.signing_out") : t("account.sign_out")}</button>{error && <p className="owner-error" role="alert">{error}</p>}</section>;
}
