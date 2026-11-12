import { useEffect, useState } from 'react';
import { request } from './api';
import { isBrowserSource, supportsBotBrowser, type BrowserSource } from './botBrowserSource';

/** A list is not a viewer. Only the mounted video subscribes to frames. */
export function useBotBrowserSources(chatVisible: boolean) {
  const [sources, setSources] = useState<BrowserSource[]>([]);
  const [code, setCode] = useState<string | null>(null);
  useEffect(() => {
    let alive = true;
    let timer = 0;
    let externalVisible = false;
    let supported: boolean | undefined;
    let busy = false;
    let controller: AbortController | undefined;
    const visible = () => chatVisible && !document.hidden || externalVisible;
    const poll = async () => {
      window.clearTimeout(timer);
      if (!alive || busy || !visible() || supported === false) return;
      busy = true;
      controller = new AbortController();
      try {
        if (supported === undefined) supported = supportsBotBrowser(await request('/api/server-info', {signal: controller.signal}));
        if (!supported) {
          // Legacy metadata can name the Mac, but must never authorize a new browser route.
          const legacy = await request<{computers?: {id: string; kind: string}[]}>('/api/computers', {signal: controller.signal});
          if (alive) {
            setSources((legacy.computers ?? []).filter(s => s.kind === 'personal').map(s => ({computer_id: s.id, kind: 'mac_browser', label_key: 'browser.source', supported: false, enabled: false, share_frames: false, state: 'unsupported', code: 'browser_server_unsupported', connection_epoch: null, session_epoch: null, target_id: null, viewport_epoch: 0, activity: null})));
            setCode('browser_server_unsupported');
          }
          return;
        }
        const body = await request<{v: number; sources: unknown[]}>('/api/browser-sources', {signal: controller.signal, cache: 'no-store'});
        if (body.v !== 1 || !Array.isArray(body.sources) || !body.sources.every(isBrowserSource)) throw {code: 'browser_server_unsupported'};
        if (alive) { setSources(current => JSON.stringify(current) === JSON.stringify(body.sources) ? current : body.sources as BrowserSource[]); setCode(null); }
      } catch (cause) {
        const failureCode = (cause as {code?: string})?.code === 'browser_server_unsupported' ? 'browser_server_unsupported' : 'browser_transport_unavailable';
        if (alive && !controller.signal.aborted) { setSources(current => current.map(s => ({...s, state: 'offline', code: failureCode, activity: null, connection_epoch: null, session_epoch: null, target_id: null, viewport_epoch: 0}))); setCode(failureCode); }
      } finally { busy = false; if (alive && visible() && supported !== false) timer = window.setTimeout(() => void poll(), 1000); }
    };
    const visibility = () => { if (visible()) void poll(); else { controller?.abort(); window.clearTimeout(timer); } };
    const pip = window.tofiDesktop?.pip;
    const sync = (s: {visible: boolean}) => { externalVisible = s.visible; visibility(); };
    const off = pip?.onStatus(sync);
    void pip?.status().then(sync, () => {});
    document.addEventListener('visibilitychange', visibility);
    const unauthorized = () => { controller?.abort(); setSources([]); alive = false; window.clearTimeout(timer); };
    window.addEventListener('tofi:unauthorized', unauthorized);
    void poll();
    return () => { alive = false; controller?.abort(); window.clearTimeout(timer); off?.(); document.removeEventListener('visibilitychange', visibility); window.removeEventListener('tofi:unauthorized', unauthorized); };
  }, [chatVisible]);
  return {sources, code};
}
