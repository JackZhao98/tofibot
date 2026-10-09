import { createContext, Fragment, useCallback, useContext, useEffect, useId, useRef, useState, type ReactNode } from "react";
import { BrandLogo } from "./BrandLogo";
import { useAppearance } from "./InteractionSystem";
import { TofiIcon } from "./icons";
import { hydrateDesktopState, isDesktop, flushDesktopState } from "./desktop";
import { i18n, useTranslation } from "./i18n";
import { authRequest, type OwnerSession } from "./ownerAuthApi";
import { OwnerSetup, PasswordChecklist } from "./OwnerSetup";
import { AuthLanguageSwitch } from "./AuthLanguageSwitch";

export function useOwnerSession(){return useContext(SessionContext).session}
const SessionContext = createContext<{ session: OwnerSession | null; logout: () => Promise<void> }>({ session: null, logout: async () => {} });
const anonymous: OwnerSession = { enabled: false, setup_required: false, authenticated: false, password_transport_allowed: false };

export function OwnerSessionGate({ children }: { children: ReactNode }) {
  useAppearance();
  const { t } = useTranslation("auth");
  const [session, setSession] = useState<OwnerSession | null>(null);
  const [error, setError] = useState("");
  const [pending, setPending] = useState(false);
  const [expired, setExpired] = useState(false);
  const [checked, setChecked] = useState(false);
  const [instanceID, setInstanceID] = useState("");
  const observedInstance = useRef("");
  const sessionRef = useRef(session);
  sessionRef.current = session;
  const requestVersion = useRef(0);
  const loggingOut = useRef(false);
  const refresh = useCallback(async () => {
    if (loggingOut.current) return;
    const version = ++requestVersion.current;
    const controller = new AbortController();
    const timer = window.setTimeout(() => controller.abort(), 12_000);
    try {
      const response = await fetch("/api/server-info", { cache: "no-store", signal: controller.signal });
      if (!response.ok) throw new Error(i18n.t("auth:error.server_unreachable"));
      const info = await response.json();
      if (version !== requestVersion.current) return;
      if (info.service !== "tofi" || info.protocol_version !== 1 || typeof info.instance_id !== "string" || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(info.instance_id)) throw new Error(i18n.t("auth:error.not_tofi"));
      if (observedInstance.current && observedInstance.current !== info.instance_id) {
        // A confirmed replacement is not an offline refresh of the same
        // workspace. Unmount old content before checking the new owner's
        // session, including when the new instance allows anonymous access.
        sessionRef.current = null;
        setSession(null); setChecked(false); setError(""); setExpired(false);
      }
      observedInstance.current = info.instance_id;
      const next = info.auth?.mode === "none" ? anonymous : await authRequest("session", undefined, controller.signal);
      if (version !== requestVersion.current) return;
      if (!next.enabled || next.authenticated) await hydrateDesktopState(info.instance_id);
      if (version !== requestVersion.current) return;
      setInstanceID(info.instance_id); setSession(next); setError(""); setChecked(true);
    } catch (cause) {
      if (version === requestVersion.current && !sessionRef.current) setError(cause instanceof Error && cause.name !== "AbortError" ? cause.message : i18n.t("auth:error.timeout"));
      // An offline server does not log out an authenticated user or discard UI state.
    } finally { window.clearTimeout(timer); }
  }, []);
  useEffect(() => {
    void refresh();
    const unauthorized = () => {
      if (!sessionRef.current?.enabled) return;
      flushDesktopState(); setExpired(true);
      setSession(current => current ? { ...current, authenticated: false, owner: undefined } : current);
      void refresh();
    };
    const focused = () => { if (!document.hidden) void refresh(); };
    window.addEventListener("tofi:unauthorized", unauthorized);
    window.addEventListener("focus", focused);
    const timer = window.setInterval(focused, 60_000);
    return () => { ++requestVersion.current; window.clearInterval(timer); window.removeEventListener("tofi:unauthorized", unauthorized); window.removeEventListener("focus", focused); };
  }, [refresh]);
  async function logout() {
    loggingOut.current = true; ++requestVersion.current;
    flushDesktopState();
    try { const next = await authRequest("logout", {}); setExpired(false); setSession(next); }
    finally { loggingOut.current = false; }
  }
  // The POST may have consumed setup or issued a cookie, but only a fresh
  // identity/session check may open the workspace. Retire the old form so a
  // failed check exposes reconnect, not another POST.
  const rulesID = useId();
  const [newPassword, setNewPassword] = useState("");
  const finishAuth = async () => { setExpired(false); sessionRef.current = null; setSession(null); setChecked(false); await refresh(); };
  const mustChange = Boolean(session?.authenticated && session?.owner?.must_change_password);
  const allowed = checked && session && (!session.enabled || session.authenticated) && !mustChange;
  return <SessionContext.Provider value={{ session, logout }}>{allowed ? <Fragment key={`${instanceID}:${session?.owner?.id ?? "legacy"}`}>{children}</Fragment> : <div className="owner-gate">
    <div className="native-titlebar" aria-hidden="true" />
    <AuthLanguageSwitch />
    <section className="owner-card">
      <BrandLogo variant="calico" />
      {!session ? <><h1>{t("gate.connect_title")}</h1>{error ? <><p className="owner-error" role="alert">{error}</p><button className="primary-button" onClick={() => void refresh()}>{t("gate.reconnect")}</button></> : <div className="owner-loading"><span className="spinner" />{t("gate.connecting")}</div>}</> : session.setup_required && session.password_transport_allowed && !mustChange ? <OwnerSetup onDone={finishAuth} /> : <>
        <h1>{mustChange ? t("gate.title.set_password") : session.setup_required ? t("gate.title.welcome_new") : expired ? t("gate.title.sign_in_again") : t("gate.title.welcome_back")}</h1>
        <p className="owner-subtitle">{mustChange ? t("gate.subtitle.change_initial") : session.setup_required ? t("gate.subtitle.create_admin") : t("gate.subtitle.sign_in")}</p>
        {!session.password_transport_allowed ? <p className="owner-error" role="alert">{t("error.password_transport_required")}</p> : <form onSubmit={async event => {
          event.preventDefault(); if (pending) return;
          const form = event.currentTarget;
          const data = new FormData(form);
          setPending(true); setError("");
          try {
            await authRequest(mustChange ? "password" : "login", mustChange ? {current_password:data.get("current_password"),password:data.get("password")} : { identifier: data.get("identifier"), password: data.get("password") });
            form.reset(); setNewPassword("");
            await finishAuth();
          } catch (cause) { setError(cause instanceof Error ? cause.message : t("error.login_failed")); }
          finally { setPending(false); }
        }}>
          {mustChange ? <label>{t("field.current_initial_password")}<input name="current_password" type="password" autoComplete="current-password" required disabled={pending}/></label>
            : <label>{t("field.identifier")}<input name="identifier" autoComplete="username" autoCapitalize="none" spellCheck={false} required autoFocus disabled={pending} /></label>}
          <label>{t("field.password")}<input name="password" type="password" autoComplete={mustChange ? "new-password" : "current-password"} required minLength={mustChange ? 12 : undefined} maxLength={1024} disabled={pending} aria-describedby={mustChange ? rulesID : undefined} onChange={mustChange ? event => setNewPassword(event.target.value) : undefined} /></label>
          {mustChange && <PasswordChecklist id={rulesID} password={newPassword} username={session.owner?.username ?? ""} email={session.owner?.email ?? ""} />}
          {error && <p className="owner-error" role="alert">{error}</p>}
          <button className="primary-button" disabled={pending}>{pending ? t("gate.submit.wait") : mustChange ? t("gate.submit.update_password") : t("gate.submit.sign_in")}</button>
        </form>}
      </>}
      {isDesktop && <a className="owner-switch" href="/__desktop/setup">{t("gate.switch_server")}</a>}
    </section>
  </div>}</SessionContext.Provider>;
}

/** Name, initial and "email · role" line for the settings account card. Single-owner mode has no session owner, so it falls back to the product name with no email. */
export function useAccountIdentity() {
  const session = useOwnerSession();
  const { t } = useTranslation("settings");
  const owner = session?.enabled ? session.owner : undefined;
  const name = owner?.username || "Tofi";
  const email = owner && !owner.email.endsWith("@account.invalid") ? owner.email : "";
  const role = owner?.role ? (owner.role === "admin" ? t("shell.account.role.admin") : t("shell.account.role.member")) : "";
  return { name, initial: Array.from(name)[0]?.toUpperCase() ?? "T", detail: [email, role].filter(Boolean).join(" · "), signedIn: Boolean(owner) };
}

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

/** The compact identity dock shares the authenticated session with Settings. */
export function SidebarAccount({ onSettings, onUsage, onConnection, onArchive, connected = true }: { onSettings: () => void; onUsage: () => void; onConnection: () => void; onArchive: () => void; connected?: boolean }) {
  const { session, logout } = useContext(SessionContext);
  const appearance = useAppearance();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const menu = useRef<HTMLDivElement>(null);
  const { t } = useTranslation(["auth", "common"]);
  useEffect(() => {
    if (!open) return;
    const dismiss = (event: PointerEvent) => { if (event.target instanceof Node && !menu.current?.contains(event.target)) setOpen(false); };
    const escape = (event: KeyboardEvent) => { if (event.key === "Escape") setOpen(false); };
    document.addEventListener("pointerdown", dismiss);
    document.addEventListener("keydown", escape);
    return () => { document.removeEventListener("pointerdown", dismiss); document.removeEventListener("keydown", escape); };
  }, [open]);
  const name = session?.owner?.username ?? "Tofi";
  const host = isDesktop ? t("account.desktop_client") : connected ? window.location.host : t("account.offline_reconnecting");
  const initial = Array.from(name)[0]?.toUpperCase() ?? "T";
  const navigate = (action: () => void) => { setOpen(false); action(); };
  return <div className={`sidebar-account${!connected && !isDesktop ? " is-offline" : ""}`} ref={menu}>
    <button className="sidebar-account-identity" aria-label={t("account.menu_label", { name, status: connected ? t("account.live_ok") : t("account.live_lost") })} aria-expanded={open} onClick={() => setOpen(value => !value)} data-hint={t("account.settings_hint", { name })}>
      <span className="sidebar-account-monogram" aria-hidden="true">{initial}<i key={String(connected)} /></span>
      <span className="sidebar-account-copy"><strong>{name}</strong><small>{host}</small></span>
    </button>
    <button className="sidebar-account-settings" aria-label={t("common:nav.settings")} data-hint={t("common:nav.settings")} onClick={onSettings}><TofiIcon name="settings" size={20}/></button>
    {open && <div className="sidebar-account-menu" role="menu">
      <div className="sidebar-account-menu-heading"><strong>{name}</strong><small>{host}</small></div>
      <button role="menuitem" onClick={() => navigate(onSettings)}><TofiIcon name="settings" size={16}/>{t("common:nav.settings")}</button>
      <button role="menuitem" onClick={() => navigate(onUsage)}><TofiIcon name="activity" size={16}/>{t("common:nav.usage")}</button>
      <div className="sidebar-account-themes" role="group" aria-label={t("common:theme.label")}>{(["system","light","dark"] as const).map(theme => <button key={theme} aria-pressed={appearance.preference === theme} onClick={() => appearance.choose(theme)}>{t(`common:theme.${theme}`)}</button>)}</div>
      <button role="menuitem" onClick={() => navigate(onConnection)}><TofiIcon name="server" size={16}/>{t("common:nav.connection")}</button>
      <button role="menuitem" onClick={() => navigate(onArchive)}><TofiIcon name="archive" size={16}/>{t("common:nav.archived")}</button>
      {session?.enabled && <><div className="sidebar-account-menu-rule"/><button role="menuitem" className="sidebar-account-logout" disabled={busy} onClick={() => { setBusy(true); setError(""); void logout().catch(cause => setError(cause instanceof Error ? cause.message : t("account.sign_out_failed"))).finally(() => setBusy(false)); }}>{busy ? t("account.signing_out") : t("account.sign_out")}</button></>}
      {error && <p className="error-text" role="alert">{error}</p>}
    </div>}
  </div>;
}
