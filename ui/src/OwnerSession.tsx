import { createContext, Fragment, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { BrandLogo } from "./BrandLogo";
import { useAppearance } from "./InteractionSystem";
import { TofiIcon } from "./icons";
import { hydrateDesktopState, isDesktop, flushDesktopState } from "./desktop";

type OwnerSession = { enabled: boolean; setup_required: boolean; authenticated: boolean; password_transport_allowed: boolean; multi_account?: boolean; owner?: { id?: string; must_change_password?: boolean; username: string; email: string; role?: string } };
export function useOwnerSession(){return useContext(SessionContext).session}
const SessionContext = createContext<{ session: OwnerSession | null; logout: () => Promise<void> }>({ session: null, logout: async () => {} });
const anonymous: OwnerSession = { enabled: false, setup_required: false, authenticated: false, password_transport_allowed: false };

async function authRequest(path: string, body?: unknown, signal?: AbortSignal): Promise<OwnerSession> {
  const controller = new AbortController();
  const timeout = window.setTimeout(() => controller.abort(), 15_000);
  const aborted = () => controller.abort();
  signal?.addEventListener("abort", aborted, { once: true });
  if (signal?.aborted) controller.abort();
  try {
  const response = await fetch(`/api/auth/${path}`, { method: body ? "POST" : "GET", credentials: "same-origin", cache: "no-store", signal: controller.signal,
    headers: body ? { "Content-Type": "application/json" } : undefined, body: body ? JSON.stringify(body) : undefined });
  const value = await response.json().catch(() => null);
  if (!response.ok) {
    const errors: Record<string,string> = { invalid_credentials: "用户名、邮箱或密码不正确。", invalid_bootstrap: "初始化密钥无效或已使用。", setup_unavailable: "账号已创建，请刷新后登录。", rate_limited: "尝试次数过多，请稍后再试。", password_transport_required: "服务器未允许当前连接登录，请检查内网 HTTP、HTTPS 或 SSH 配置。", invalid_request: path === "setup" ? "请检查用户名、邮箱和密码（至少 12 位）。" : "请检查输入的登录信息。" };
    throw new Error(errors[value?.error?.code] || "连接暂时不可用，请重试。");
  }
  if (!value || typeof value.enabled !== "boolean") throw new Error("服务器返回了无效的登录状态。");
  return value;
  } catch (cause) { if (controller.signal.aborted) throw new Error("连接超时，请重试。"); throw cause; }
  finally { window.clearTimeout(timeout); signal?.removeEventListener("abort", aborted); }
}

export function OwnerSessionGate({ children }: { children: ReactNode }) {
  useAppearance();
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
      if (!response.ok) throw new Error("暂时无法连接服务器。");
      const info = await response.json();
      if (version !== requestVersion.current) return;
      if (info.service !== "tofi" || info.protocol_version !== 1 || typeof info.instance_id !== "string" || !/^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(info.instance_id)) throw new Error("这不是可用的 Tofi 服务器。");
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
      if (version === requestVersion.current && !sessionRef.current) setError(cause instanceof Error && cause.name !== "AbortError" ? cause.message : "连接超时，请重试。");
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
  const mustChange = Boolean(session?.authenticated && session?.owner?.must_change_password);
  const allowed = checked && session && (!session.enabled || session.authenticated) && !mustChange;
  return <SessionContext.Provider value={{ session, logout }}>{allowed ? <Fragment key={`${instanceID}:${session?.owner?.id ?? "legacy"}`}>{children}</Fragment> : <div className="owner-gate">
    <div className="native-titlebar" aria-hidden="true" />
    <section className="owner-card">
      <BrandLogo variant="calico" />
      {!session ? <><h1>连接你的工作空间</h1>{error ? <><p className="owner-error" role="alert">{error}</p><button className="primary-button" onClick={() => void refresh()}>重新连接</button></> : <div className="owner-loading"><span className="spinner" />正在连接…</div>}</> : <>
        <h1>{mustChange ? "设置你的密码" : session.setup_required ? "欢迎来到 Tofi" : expired ? "重新登录" : "欢迎回来"}</h1>
        <p className="owner-subtitle">{mustChange ? "首次登录需要更换初始密码" : session.setup_required ? "创建第一个 Admin 账号" : "登录你的工作空间"}</p>
        {!session.password_transport_allowed ? <p className="owner-error" role="alert">服务器未允许当前连接登录，请检查内网 HTTP、HTTPS 或 SSH 配置。</p> : <form onSubmit={async event => {
          event.preventDefault(); if (pending) return;
          const form = event.currentTarget;
          const data = new FormData(form);
          setPending(true); setError("");
          try {
            await authRequest(mustChange ? "password" : session.setup_required ? "setup" : "login", mustChange ? {current_password:data.get("current_password"),password:data.get("password")} : session.setup_required
              ? { username: data.get("username"), email: data.get("email"), password: data.get("password"), bootstrap_secret: data.get("bootstrap_secret") }
              : { identifier: data.get("identifier"), password: data.get("password") });
            form.reset(); setExpired(false);
            // The POST may have consumed setup or issued a cookie, but only a
            // fresh identity/session check may open the workspace. Retire the
            // old form so a failed check exposes reconnect, not another POST.
            sessionRef.current = null;
            setSession(null); setChecked(false);
            await refresh();
          } catch (cause) { setError(cause instanceof Error ? cause.message : "登录失败，请重试。"); }
          finally { setPending(false); }
        }}>
          {mustChange ? <label>当前初始密码<input name="current_password" type="password" autoComplete="current-password" required disabled={pending}/></label> : session.setup_required ? <>
            <label>用户名<input name="username" autoComplete="username" autoCapitalize="none" spellCheck={false} required maxLength={64} disabled={pending} /></label>
            <label>邮箱<input name="email" type="email" autoComplete="email" autoCapitalize="none" spellCheck={false} required disabled={pending} /></label>
          </> : <label>用户名或邮箱<input name="identifier" autoComplete="username" autoCapitalize="none" spellCheck={false} required autoFocus disabled={pending} /></label>}
          <label>密码<input name="password" type="password" autoComplete={session.setup_required || mustChange ? "new-password" : "current-password"} required minLength={session.setup_required || mustChange ? 12 : undefined} maxLength={1024} disabled={pending} /></label>
          {session.setup_required && <label>初始化密钥<input name="bootstrap_secret" type="password" autoComplete="off" spellCheck={false} required disabled={pending} /><small>来自服务器的 owner-bootstrap.secret，只使用一次。</small></label>}
          {error && <p className="owner-error" role="alert">{error}</p>}
          <button className="primary-button" disabled={pending}>{pending ? "请稍候…" : mustChange ? "更新密码并继续" : session.setup_required ? "创建账号并继续" : "登录"}</button>
        </form>}
      </>}
      {isDesktop && <a className="owner-switch" href="/__desktop/setup">切换服务器</a>}
    </section>
  </div>}</SessionContext.Provider>;
}

export function OwnerAccount() {
  const { session, logout } = useContext(SessionContext);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  if (!session?.enabled || !session.owner) return null;
  return <section className="owner-account"><div><strong>{session.owner.username}</strong><span>{session.owner.email.endsWith("@account.invalid")?"":session.owner.email}{session.owner.role === "admin" && " · admin"}</span></div><button className="secondary-button" disabled={busy} onClick={() => {
    setBusy(true); setError(""); void logout().catch(cause => setError(cause.message)).finally(() => setBusy(false));
  }}>{busy ? "退出中…" : "退出登录"}</button>{error && <p className="owner-error" role="alert">{error}</p>}</section>;
}

/** The compact identity dock shares the authenticated session with Settings. */
export function SidebarAccount({ onSettings, onUsage, onConnection, onArchive, connected = true }: { onSettings: () => void; onUsage: () => void; onConnection: () => void; onArchive: () => void; connected?: boolean }) {
  const { session, logout } = useContext(SessionContext);
  const appearance = useAppearance();
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const menu = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const dismiss = (event: PointerEvent) => { if (event.target instanceof Node && !menu.current?.contains(event.target)) setOpen(false); };
    const escape = (event: KeyboardEvent) => { if (event.key === "Escape") setOpen(false); };
    document.addEventListener("pointerdown", dismiss);
    document.addEventListener("keydown", escape);
    return () => { document.removeEventListener("pointerdown", dismiss); document.removeEventListener("keydown", escape); };
  }, [open]);
  const name = session?.owner?.username ?? "Tofi";
  const host = isDesktop ? "桌面客户端" : connected ? window.location.host : "实时连接中断 · 正在重连";
  const initial = Array.from(name)[0]?.toUpperCase() ?? "T";
  const navigate = (action: () => void) => { setOpen(false); action(); };
  return <div className={`sidebar-account${!connected && !isDesktop ? " is-offline" : ""}`} ref={menu}>
    <button className="sidebar-account-identity" aria-label={`${name} · ${connected ? "实时连接正常" : "实时连接中断"} · 账户菜单`} aria-expanded={open} onClick={() => setOpen(value => !value)} data-hint={`${name} · 设置`}>
      <span className="sidebar-account-monogram" aria-hidden="true">{initial}<i key={String(connected)} /></span>
      <span className="sidebar-account-copy"><strong>{name}</strong><small>{host}</small></span>
    </button>
    <button className="sidebar-account-settings" aria-label="设置" data-hint="设置" onClick={onSettings}><TofiIcon name="settings" size={20}/></button>
    {open && <div className="sidebar-account-menu" role="menu">
      <div className="sidebar-account-menu-heading"><strong>{name}</strong><small>{host}</small></div>
      <button role="menuitem" onClick={() => navigate(onSettings)}><TofiIcon name="settings" size={16}/>设置</button>
      <button role="menuitem" onClick={() => navigate(onUsage)}><TofiIcon name="activity" size={16}/>用量</button>
      <div className="sidebar-account-themes" role="group" aria-label="外观主题">{(["system","light","dark"] as const).map(theme => <button key={theme} aria-pressed={appearance.preference === theme} onClick={() => appearance.choose(theme)}>{theme === "system" ? "系统" : theme === "light" ? "浅色" : "深色"}</button>)}</div>
      <button role="menuitem" onClick={() => navigate(onConnection)}><TofiIcon name="server" size={16}/>服务器与连接</button>
      <button role="menuitem" onClick={() => navigate(onArchive)}><TofiIcon name="archive" size={16}/>已归档的对话</button>
      {session?.enabled && <><div className="sidebar-account-menu-rule"/><button role="menuitem" className="sidebar-account-logout" disabled={busy} onClick={() => { setBusy(true); setError(""); void logout().catch(cause => setError(cause instanceof Error ? cause.message : "退出失败")).finally(() => setBusy(false)); }}>{busy ? "退出中…" : "退出登录"}</button></>}
      {error && <p className="error-text" role="alert">{error}</p>}
    </div>}
  </div>;
}
