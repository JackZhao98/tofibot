/** Metadata only. Page content and preview frames never belong in this model. */
export type BrowserSource = {
  computer_id: string; kind: 'mac_browser'; label_key: 'browser.source'; supported: boolean;
  enabled: boolean; share_frames: boolean;
  state: 'idle'|'working'|'human'|'stopped'|'closed'|'disabled'|'offline'|'unsupported'|'minimized'|'locked'|'unavailable';
  code: string | null; connection_epoch: string | null; session_epoch: string | null;
  target_id: string | null; viewport_epoch: number;
  activity: null | { bot_id: string; run_id: string; conversation_id: string };
};
export type ComputerSource = { kind: 'bot_computer'; bot_id: string } | { kind: 'mac_browser'; computer_id: string };
export const sourceKey = (source: ComputerSource) => source.kind === 'mac_browser' ? `mac:${source.computer_id}` : `bot:${source.bot_id}`;
export const validSourceKey = (key: string) => /^(mac|bot):[A-Za-z0-9_:-]+$/.test(key) && key.length <= 160;
const states = new Set(['idle','working','human','stopped','closed','disabled','offline','unsupported','minimized','locked','unavailable']);
export const isBrowserUUID = (value: unknown): value is string => typeof value === 'string' && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i.test(value);
const nullableString = (value: unknown) => value === null || typeof value === 'string';
export function isBrowserSource(value: unknown): value is BrowserSource {
  if (!value || typeof value !== 'object') return false;
  const s = value as BrowserSource;
  return Object.keys(s).sort().join(',') === 'activity,code,computer_id,connection_epoch,enabled,kind,label_key,session_epoch,share_frames,state,supported,target_id,viewport_epoch' && typeof s.computer_id === 'string' && validSourceKey(`mac:${s.computer_id}`) && s.kind === 'mac_browser' && s.label_key === 'browser.source'
    && typeof s.supported === 'boolean' && typeof s.enabled === 'boolean' && typeof s.share_frames === 'boolean' && states.has(s.state)
    && nullableString(s.code) && [s.connection_epoch,s.session_epoch,s.target_id].every(id => id === null || isBrowserUUID(id))
    && Number.isSafeInteger(s.viewport_epoch) && s.viewport_epoch >= 0 && (s.activity === null || !!s.activity && Object.keys(s.activity).sort().join(',') === 'bot_id,conversation_id,run_id' && ['bot_id','run_id','conversation_id'].every(k => typeof s.activity?.[k as keyof NonNullable<BrowserSource['activity']>] === 'string'));
}
export const canShowBrowserFrame = (source: BrowserSource) => source.supported && source.enabled && source.share_frames && !source.code && (source.state === 'working' || source.state === 'idle') && isBrowserUUID(source.connection_epoch) && isBrowserUUID(source.session_epoch) && isBrowserUUID(source.target_id) && source.viewport_epoch > 0;
export function browserStatusKey(source: BrowserSource): string {
  if (!source.share_frames && source.supported && source.enabled && !['human','stopped','offline','unsupported'].includes(source.state)) return 'frames_disabled';
  return source.code || source.state;
}
export const supportsBotBrowser = (value: unknown) => {
  const feature = (value as { bot_browser?: { version?: unknown; upload?: unknown; view?: unknown } } | null)?.bot_browser;
  return feature?.version === 1 && feature.upload === 'websocket-json' && feature.view === 'ndjson-jpeg';
};
