import "./desktop-presence.css";
import { useEffect, useState } from 'react';
import { useTranslation } from './i18n';
import { TofiIcon } from './icons';
import { BotBrowserVideo } from './BotBrowserVideo';
import { browserStatusKey, canShowBrowserFrame, type BrowserSource } from './botBrowserSource';

/** The Mac browser is observation only: no input listeners or desktop controls. */
export function BotBrowserPanel({source, expanded, folded = false, onClose, onExpand}: {source: BrowserSource; expanded: boolean; folded?: boolean; onClose: () => void; onExpand: () => void}) {
  const {t} = useTranslation('computer');
  const [live, setLive] = useState(source);
  const [error, setError] = useState<string | null>(null);
  const [visible, setVisible] = useState(!window.tofiDesktop && !document.hidden || expanded && !document.hidden);
  const bridge = window.tofiDesktop?.botBrowser;
  const [localDeviceID, setLocalDeviceID] = useState<string | null>(null);
  const oldShell = !!window.tofiDesktop && !expanded && !window.tofiDesktop.pip;
  useEffect(() => { setLive(source); }, [source]);
  useEffect(() => {
    let alive = true;
    void bridge?.status().then(s => { if (alive) setLocalDeviceID(s.deviceID); }, () => {});
    const off = bridge?.onStatus(s => setLocalDeviceID(s.deviceID));
    return () => { alive = false; off?.(); };
  }, [bridge]);
  useEffect(() => {
    let external = false;
    const sync = () => setVisible(window.tofiDesktop && !expanded ? external : !document.hidden);
    const status = (s: {visible: boolean; source_key: string | null}) => { external = s.visible && s.source_key === `mac:${source.computer_id}`; sync(); };
    let pushed = false;
    const off = window.tofiDesktop?.pip?.onStatus(s => { pushed = true; status(s); });
    void window.tofiDesktop?.pip?.status().then(s => { if (!pushed) status(s); }, () => {});
    sync();
    const failed = () => setError('browser_client_unsupported');
    if (document.querySelector('[data-pip-error]')) failed();
    document.addEventListener('visibilitychange', sync);
    window.addEventListener('tofi:pip-error', failed);
    return () => { off?.(); document.removeEventListener('visibilitychange', sync); window.removeEventListener('tofi:pip-error', failed); };
  }, [source.computer_id, expanded]);
  const key = error || (oldShell ? 'browser_client_unsupported' : browserStatusKey(live));
  const statusKey = `browser.status.${key}` as 'browser.status.idle';
  return <section className={`computer-detail bot-browser-panel${expanded ? " is-expanded" : ""}`} data-browser-source={source.computer_id} data-desktop-frame="">
    <div className="desktop-window-controls">
      <button type="button" className="desktop-control-button" aria-label={t('browser.close')} onClick={onClose}><TofiIcon name="close" size={16}/></button>
      <button type="button" className="desktop-control-button" aria-label={t(expanded ? 'browser.shrink' : 'browser.enlarge')} onClick={onExpand}><span className={`desktop-expand-glyph${expanded ? " is-expanded" : ""}`} aria-hidden="true"/></button>
    </div>
    <div className="computer-screen-wrap bot-browser-screen" onClick={expanded ? undefined : onExpand}>
      <BotBrowserVideo source={live} visible={visible && !folded && !oldShell && !error} onStatus={setLive}/>
      {(!canShowBrowserFrame(live) || oldShell || error) && <p className="bot-browser-state" role="status">{t(statusKey)}</p>}
      {!expanded && <button type="button" className="desktop-enlarge" aria-label={t('browser.enlarge')} onClick={event => {event.stopPropagation(); onExpand();}}/>}
    </div>
    <div className="desktop-chin"><span>{t('browser.source')}</span><span role="status">{t(statusKey)}</span>
      {bridge && localDeviceID === source.computer_id && expanded && <button type="button" className="st-button" onClick={() => { void bridge.open().then(result => { if (!result.ok) setError(result.code || 'browser_control_lost'); }, () => setError('browser_control_lost')); }}>{t('browser.open')}</button>}
    </div>
  </section>;
}
