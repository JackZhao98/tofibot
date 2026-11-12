import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { useTranslation } from './i18n';
import { canShowBrowserFrame, isBrowserSource, isBrowserUUID, type BrowserSource } from './botBrowserSource';

export const MAX_BROWSER_LINE = 352 * 1024;
const safeInt = (n: unknown) => Number.isSafeInteger(n) && (n as number) >= 0;
type Frame = {type: 'frame'; v: 1; connection_epoch: string; observation_id: string; session_epoch: string; target_id: string; viewport_epoch: number; seq: number; captured_at_ms: number; received_at_ms: number; width: number; height: number; jpeg: string};
export function validBrowserFrame(value: unknown, source: BrowserSource, observation: string | null, seq: number): value is Frame {
  const f = value as Frame;
  return !!f && Object.keys(f).sort().join(',') === 'captured_at_ms,connection_epoch,height,jpeg,observation_id,received_at_ms,seq,session_epoch,target_id,type,v,viewport_epoch,width'
    && f.type === 'frame' && f.v === 1 && canShowBrowserFrame(source)
    && isBrowserUUID(f.connection_epoch) && isBrowserUUID(f.session_epoch) && isBrowserUUID(f.target_id)
    && f.connection_epoch === source.connection_epoch && f.session_epoch === source.session_epoch && f.target_id === source.target_id && f.viewport_epoch === source.viewport_epoch
    && isBrowserUUID(f.observation_id) && (observation === null || observation === f.observation_id)
    && safeInt(f.seq) && f.seq > seq && safeInt(f.captured_at_ms) && safeInt(f.received_at_ms)
    && safeInt(f.width) && f.width > 0 && f.width <= 1280 && safeInt(f.height) && f.height > 0 && f.height <= 800
    && typeof f.jpeg === 'string' && f.jpeg.length <= Math.ceil(256 * 1024 / 3) * 4 && f.jpeg.length % 4 === 0 && /^[A-Za-z0-9+/]+={0,2}$/.test(f.jpeg);
}

/** One decoder plus one replaceable pending frame. No frame enters React state. */
export function BotBrowserVideo({source, visible, onStatus}: {source: BrowserSource; visible: boolean; onStatus: (source: BrowserSource) => void}) {
  const {t} = useTranslation('computer');
  const canvas = useRef<HTMLCanvasElement>(null);
  const statusCallback = useRef(onStatus);
  statusCallback.current = onStatus;
  const syncSource = useRef<(source: BrowserSource) => void>(() => {});
  useLayoutEffect(() => { syncSource.current(source); }, [source]);
  const [code, setCode] = useState<string | null>(null);
  useEffect(() => {
    const surface = canvas.current;
    if (!surface) return;
    const context = surface.getContext('2d');
    let generation = 0;
    let stopped = false;
    let current = source;
    let observation: string | null = null;
    let sequence = 0;
    let pending: Frame | null = null;
    let decoding = false;
    let lastMessage = performance.now();
    let watchdog = 0;
    const abort = new AbortController();
    const clear = () => { generation++; pending = null; context?.clearRect(0, 0, surface.width, surface.height); };
    const update = (next: BrowserSource) => {
      if (next.connection_epoch !== current.connection_epoch || next.session_epoch !== current.session_epoch || next.target_id !== current.target_id || next.viewport_epoch !== current.viewport_epoch || !canShowBrowserFrame(next)) { clear(); observation = null; sequence = 0; }
      current = next;
    };
    syncSource.current = update;
    const fail = (reason = 'browser_transport_unavailable') => { if (stopped || abort.signal.aborted) return; clear(); setCode(reason); abort.abort(); window.clearInterval(watchdog); };
    const decode = async () => {
      if (decoding || stopped || !pending) return;
      const frame = pending; pending = null; decoding = true;
      const version = generation;
      let bitmap: ImageBitmap | undefined;
      try {
        const bytes = Uint8Array.from(atob(frame.jpeg), c => c.charCodeAt(0));
        if (bytes.length > 256 * 1024 || bytes[0] !== 255 || bytes[1] !== 216 || bytes[bytes.length - 2] !== 255 || bytes[bytes.length - 1] !== 217) throw new Error();
        bitmap = await createImageBitmap(new Blob([bytes], {type: 'image/jpeg'}));
        if (bitmap.width !== frame.width || bitmap.height !== frame.height) throw new Error();
        if (!stopped && !abort.signal.aborted && version === generation) {
          surface.width = frame.width; surface.height = frame.height;
          context?.drawImage(bitmap, 0, 0);
        }
      } catch { fail('browser_control_lost'); }
      finally { bitmap?.close(); decoding = false; if (pending) void decode(); }
    };
    const message = (line: string) => {
      if (abort.signal.aborted) return;
      if (new TextEncoder().encode(line).length > MAX_BROWSER_LINE) throw new Error();
      const value = JSON.parse(line);
      if (value.v !== 1) { fail('browser_server_unsupported'); throw new Error(); }
      if (value.type === 'status' && Object.keys(value).sort().join(',') === 'source,type,v' && isBrowserSource(value.source) && value.source.computer_id === source.computer_id) {
        const next: BrowserSource = value.source;
        update(next); statusCallback.current(next);
      } else if (value.type === 'ping' && Object.keys(value).sort().join(',') === 'server_time_ms,type,v' && safeInt(value.server_time_ms)) {
        // A still page remains connected through server pings.
      } else if (value.type === 'frame') {
        if (!validBrowserFrame(value, current, observation, sequence)) return;
        observation = value.observation_id; sequence = value.seq; pending = value; void decode();
      } else throw new Error();
      lastMessage = performance.now();
    };
    setCode(null); clear();
    if (!visible || !source.supported) return () => { stopped = true; clear(); };
    watchdog = window.setInterval(() => { if (performance.now() - lastMessage >= 3000) fail(); }, 250);
    void (async () => {
      try {
        const response = await fetch(`/api/computers/${encodeURIComponent(source.computer_id)}/browser/stream`, {credentials: 'same-origin', cache: 'no-store', signal: abort.signal});
        if (!response.ok) { if (response.status === 401) window.dispatchEvent(new Event('tofi:unauthorized')); const body = await response.json().catch(() => null); fail(body?.error?.code === 'browser_preview_busy' ? 'browser_preview_busy' : 'browser_transport_unavailable'); return; }
        if (!response.body || !response.headers.get('content-type')?.includes('application/x-ndjson')) throw new Error();
        const reader = response.body.getReader();
        const decoder = new TextDecoder('utf-8', {fatal: true});
        let buffer = '';
        try {
          while (!abort.signal.aborted) {
            const {value, done} = await reader.read();
            if (done) throw new Error();
            buffer += decoder.decode(value, {stream: true});
            let newline: number;
            while ((newline = buffer.indexOf('\n')) >= 0) { message(buffer.slice(0, newline)); buffer = buffer.slice(newline + 1); }
            if (new TextEncoder().encode(buffer).length > MAX_BROWSER_LINE) throw new Error();
          }
        } finally { await reader.cancel().catch(() => {}); reader.releaseLock(); }
      } catch { if (!abort.signal.aborted) fail(); }
    })();
    return () => { syncSource.current = () => {}; stopped = true; clear(); abort.abort(); window.clearInterval(watchdog); };
    // Status messages update metadata in place; they never reconnect or remount the decoder.
  }, [source.computer_id, source.supported, visible]);
  return <><canvas ref={canvas} className="bot-browser-video" aria-label={t('browser.source')} />{code && <p className="bot-browser-stream-error" role="status">{t(`browser.status.${code}` as 'browser.status.browser_transport_unavailable')}</p>}</>;
}
