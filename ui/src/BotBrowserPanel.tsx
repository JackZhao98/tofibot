import "./desktop-presence.css";
import { useEffect, useState } from 'react';
import { useTranslation } from './i18n';
import { TofiIcon } from './icons';
import { BotBrowserVideo } from './BotBrowserVideo';
import { browserStatusKey, canShowBrowserFrame, type BrowserSource } from './botBrowserSource';

/** The web view of a Mac browser is observation only: no input listeners or desktop controls. The Mac app shows its own browser in a native side panel instead. */
export function BotBrowserPanel({source, expanded, folded = false, onClose, onExpand}: {source: BrowserSource; expanded: boolean; folded?: boolean; onClose: () => void; onExpand: () => void}) {
  const {t} = useTranslation('computer');
  const [live, setLive] = useState(source);
  const [visible, setVisible] = useState(!document.hidden);
  useEffect(() => { setLive(source); }, [source]);
  useEffect(() => {
    const sync = () => setVisible(!document.hidden);
    document.addEventListener('visibilitychange', sync);
    return () => document.removeEventListener('visibilitychange', sync);
  }, []);
  const key = browserStatusKey(live);
  const statusKey = `browser.status.${key}` as 'browser.status.idle';
  return <section className={`computer-detail bot-browser-panel${expanded ? " is-expanded" : ""}`} data-browser-source={source.computer_id} data-desktop-frame="">
    <div className="desktop-window-controls">
      <button type="button" className="desktop-control-button" aria-label={t('browser.close')} onClick={onClose}><TofiIcon name="close" size={16}/></button>
      <button type="button" className="desktop-control-button" aria-label={t(expanded ? 'browser.shrink' : 'browser.enlarge')} onClick={onExpand}><span className={`desktop-expand-glyph${expanded ? " is-expanded" : ""}`} aria-hidden="true"/></button>
    </div>
    <div className="computer-screen-wrap bot-browser-screen" onClick={expanded ? undefined : onExpand}>
      <BotBrowserVideo source={live} visible={visible && !folded} onStatus={setLive}/>
      {!canShowBrowserFrame(live) && <p className="bot-browser-state" role="status">{t(statusKey)}</p>}
      {!expanded && <button type="button" className="desktop-enlarge" aria-label={t('browser.enlarge')} onClick={event => {event.stopPropagation(); onExpand();}}/>}
    </div>
    <div className="desktop-chin"><span>{t('browser.source')}</span><span role="status">{t(statusKey)}</span></div>
  </section>;
}
