import "./desktop-presence.css";
import "./desktop-screen.css";
import { useEffect, useState } from 'react';
import { useTranslation } from './i18n';
import { ScreenPanel } from './ds/ScreenPanel/ScreenPanel';
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
  return <section className={`computer-detail bot-browser-panel${expanded ? " is-expanded" : ""}`} data-browser-source={source.computer_id}>
    <ScreenPanel size={expanded ? "large" : "small"} source={t('browser.source')} expanded={expanded} onExpand={onExpand} expandLabel={t(expanded ? 'browser.shrink' : 'browser.enlarge')}
      onClose={onClose} closeLabel={t('browser.close')} screenClassName="bot-browser-screen" screenProps={{"data-desktop-frame": "", onClick: expanded ? undefined : onExpand}}
      chin={<span role="status">{t(statusKey)}</span>}>
      <BotBrowserVideo source={live} visible={visible && !folded} onStatus={setLive}/>
      {!canShowBrowserFrame(live) && <p className="bot-browser-state" role="status">{t(statusKey)}</p>}
      {!expanded && <button type="button" className="desktop-enlarge" aria-label={t('browser.enlarge')} onClick={event => {event.stopPropagation(); onExpand();}}/>}
    </ScreenPanel>
  </section>;
}
