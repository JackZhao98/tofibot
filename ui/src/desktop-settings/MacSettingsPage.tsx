import { useEffect, useRef, useState } from "react";
// Types only: importing the runtime half of ../desktop would bind workspace IPC this page must not touch.
import type { BrowserSettings, BrowserStatus, DesktopDeviceStatus } from "../desktop";
import { TofiIcon } from "../icons";
import { useTranslation } from "../i18n";
import { Banner, SettingsCard, SettingsRow, SettingsSection, SettingsSwitch } from "../settings/components";
import { RemoteControlBlock } from "../RemoteControl";
import { useRemoteControl } from "../useRemoteControl";

// Isolation (enforced by scripts/test-desktop-settings-isolation.mjs): this page's only data source is
// window.tofiDesktop. No server calls, no chat or artifact code, nothing rendered as HTML; Bot names are text.
// Folders for Bots live in the workspace (Settings > Environments), not here.

const POLL_MS = 1500;
const message = (cause: unknown) => String((cause as { message?: unknown } | null)?.message || cause || "").slice(0, 2000);

export function MacSettingsPage() {
  const { t } = useTranslation("settings");
  const api = window.tofiDesktop;
  const canRead = typeof api?.getSettings === "function";

  // The same switch as Settings > Environments: a new app on an older server has no switch in its workspace, and that
  // server's "Your Mac" dialog sends people here. The app answers from this document too, and the dialog is its own.
  const rc = useRemoteControl(true);
  const [status, setStatus] = useState<DesktopDeviceStatus | null>(null);
  const [error, setError] = useState("");
  const mounted = useRef(true);

  useEffect(() => {
    mounted.current = true;
    if (!canRead) return;
    let reading = false;
    const read = async (first: boolean) => {
      if (reading) return;
      reading = true;
      try {
        const next = await api!.getSettings!();
        if (!mounted.current) return;
        if (!next) throw new Error(t("mac.error.noStatus"));
        setStatus(next);
        if (first) setError("");
      } catch (cause) {
        if (mounted.current && first) setError(t("mac.error.action", { message: message(cause) }));
      } finally { reading = false; }
    };
    void read(true);
    const timer = window.setInterval(() => { if (!document.hidden) void read(false); }, POLL_MS);
    return () => { mounted.current = false; window.clearInterval(timer); };
  }, [api, canRead, t]);
  useEffect(() => { document.title = `${t("mac.title")} · Tofi`; }, [t]);

  return <div className="workspace mac-settings">
    <div className="mac-settings-titlebar" aria-hidden="true" />
    <main className="settings-page-body"><div className="mac-settings-inner">
      <header className="mac-settings-head">
        <h1>{t("mac.title")}<span className="mac-dev-tag">{t("mac.inDevelopment")}</span></h1>
        <a className="st-button mac-settings-back" href="/"><TofiIcon name="arrow-left" size={16} aria-hidden="true" />{t("mac.back")}</a>
      </header>

      {!canRead && <Banner tone="warn" title={t("mac.unavailable")} />}
      {error && <Banner tone="error" title={error} />}
      {status?.lastError && <Banner tone="error" title={t("mac.error.last")}>{message(status.lastError)}</Banner>}
      {canRead && !status && !error && <p className="settings-sec-desc" role="status">{t("mac.loading")}</p>}

      <RemoteControlBlock rc={rc} offered onOpenFiles={null} onOpenDenied={() => {}} menu={false} />

      <BotBrowserSettings />

      <SettingsSection title={t("mac.local.title")} description={t("mac.local.description")}>
        <SettingsCard>
          <SettingsRow label={t("mac.local.run")} control={<a className="st-button" href="/__desktop/local-run"><TofiIcon name="terminal" size={16} aria-hidden="true" />{t("mac.local.run")}</a>} />
        </SettingsCard>
      </SettingsSection>
    </div></main>
  </div>;
}

/** Local permissions are changed only through the current document's native bridge. */
export function BotBrowserSettings() {
  const {t} = useTranslation('settings');
  const bridge = window.tofiDesktop?.botBrowser;
  const [status, setStatus] = useState<BrowserStatus | null>(null);
  const [code, setCode] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [cleared, setCleared] = useState(false);
  const mounted = useRef(false);
  const pending = useRef(false);
  const generation = useRef(0);
  useEffect(() => {
    mounted.current = true;
    const id = ++generation.current;
    void bridge?.status().then(s => { if (mounted.current && generation.current === id) { setStatus(s); setCode(s.code); } }, () => { if (mounted.current) setCode('browser_control_lost'); });
    const off = bridge?.onStatus(s => { generation.current++; if (mounted.current) { setStatus(s); setCode(s.code); } });
    return () => { mounted.current = false; generation.current++; off?.(); };
  }, [bridge]);
  async function act(patch?: Partial<BrowserSettings>, clear = false) {
    if (!bridge || pending.current) return;
    pending.current = true; generation.current++; setBusy(true); setCleared(false); setCode(null);
    try {
      const result = clear ? await bridge.clear() : patch ? await bridge.update(patch) : await bridge.open();
      if (!mounted.current) return;
      setStatus(result.status);
      setCode(result.ok ? result.status.code : result.code || 'browser_control_lost');
      setCleared(clear && result.ok);
    } catch { if (mounted.current) setCode(clear ? 'browser_clear_failed' : 'browser_control_lost'); }
    finally { pending.current = false; if (mounted.current) setBusy(false); }
  }
  const settings: (keyof BrowserSettings)[] = ['botBrowserEnabled', 'botBrowserRetainLogin', 'botBrowserShareFrames'];
  return <details className="bot-browser-settings">
    <summary>{t('bot_browser.advanced')}</summary>
    {!bridge ? <p role="status">{t(window.tofiDesktop ? 'bot_browser.browser_client_unsupported' : 'bot_browser.in_app')}</p> : <SettingsCard>
      {settings.map(key => <SettingsRow key={key} inline label={t(`bot_browser.${key}`)} description={key === 'botBrowserRetainLogin' ? t('bot_browser.login_hint') : key === 'botBrowserShareFrames' ? t('bot_browser.frames_hint') : undefined} control={<SettingsSwitch label={t(`bot_browser.${key}`)} checked={status?.settings[key] === true} disabled={busy || !status || !status.supported || status.code === 'no_connection'} showSaved={false} onChange={value => act({[key]: value})}/>} />)}
      <SettingsRow label={t('bot_browser.login_actions')} control={<>
        <button type="button" className="st-button" disabled={busy || !status?.supported || status.code === 'no_connection'} onClick={() => void act()}>{t('bot_browser.open')}</button>
        <button type="button" className="st-button" disabled={busy || !status?.supported || status.code === 'no_connection'} onClick={() => void act(undefined, true)}>{t('bot_browser.clear')}</button>
      </>}/>
      {code && <p className="settings-card-note" role="alert">{t(`bot_browser.${code}` as 'bot_browser.browser_control_lost')}</p>}
      {cleared && <p className="settings-card-note" role="status">{t('bot_browser.cleared')}</p>}
    </SettingsCard>}
  </details>;
}
