import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react";
import "./UpdateNotice.css";
import "./settings/automation.css";
import { ApiError, request } from "./api";
import { Trans, useTranslation } from "./i18n";
import { formatDateTime } from "./i18n/format";
import { TofiIcon } from "./icons";
import { useOwnerSession } from "./OwnerSession";
import { useUserTimezone } from "./UserTimezone";
import { SettingsCard, SettingsRow, StatusBadge } from "./settings/components";

const UpdateDialog = lazy(() => import("./UpdateDialog").then(module => ({ default: module.UpdateDialog })));

export type UpdateState = "queued" | "waiting_for_runs" | "downloading" | "verifying" | "switching" | "done" | "failed" | "rolled_back";
export type UpdateChannel = "stable" | "rc";
/** What the host wrote to status.json, as the server passes it on (decision D69). */
export type UpdateStatus = { state: UpdateState; version?: string; from?: string; channel?: UpdateChannel; progress: number | null; code?: string; rolled_back_to?: string; requested_at?: string; started_at?: string; updated_at?: string; finished_at?: string };
export type SystemUpdate = {
  current: string; latest: string; update_available: boolean; notes_url: string; checked_at: string; auto_update: "patch" | "off";
  /** The fields below arrive from servers that can update from the page; older servers omit them. */
  channel_rc_enabled?: boolean; latest_rc?: string; latest_rc_notes_url?: string; host_update?: boolean;
  request?: { version: string; channel: UpdateChannel; requested_at: string } | null; status?: UpdateStatus | null;
};

const dismissKey = "tofi.update-notice.dismissed";
const versionPattern = /^v(\d+)\.(\d+)\.(\d+)(?:-rc\.(\d+))?$/;
const POLL_MS = 2000;
const WATCH_EVENT = "tofi:update-watch";
/** A request nobody has answered for this long is dropped from the page (the host ignored it, e.g. a repeated one). */
const WATCH_MS = 15 * 60_000;
/** A failure the host wrote before this page was open is still worth showing for a while. */
const RECENT_MS = 10 * 60_000;
const activeStates = new Set<UpdateState>(["queued", "waiting_for_runs", "downloading", "verifying", "switching"]);

/** The server also enforces this: multi-account mode needs an Admin, single-owner mode the signed-in owner. */
export function useIsUpdateAdmin(): boolean {
  const session = useOwnerSession();
  if (!session) return false;
  if (session.multi_account) return session.owner?.role === "admin";
  return !session.enabled || session.authenticated;
}

/** A fix release: same major.minor, higher patch, and not a release candidate. This is what TOFI_AUTO_UPDATE=patch installs. */
export function isPatchBump(current: string, latest: string): boolean {
  const a = versionPattern.exec(current), b = versionPattern.exec(latest);
  return Boolean(a && b && !b[4] && a[1] === b[1] && a[2] === b[2] && Number(b[3]) > Number(a[3]));
}

function readDismissed(): string {
  try { return window.localStorage.getItem(dismissKey) ?? ""; } catch { return ""; }
}

type Watch = { version: string; channel: UpdateChannel; at: number };
export type UpdateView =
  | { kind: "none" }
  | { kind: "active"; state: UpdateState; version: string; progress: number | null; restarting: boolean }
  | { kind: "done"; version: string }
  | { kind: "failed"; version: string; code: string; rolledBackTo: string };

const stamp = (value?: string) => (value ? Date.parse(value) : NaN);

/** What the page shows for the update in flight (or just ended). Pure: the polling hook feeds it. */
export function deriveUpdateView(info: SystemUpdate | null, watch: Watch | null, restarting: boolean, now: number): UpdateView {
  const live = watch && now - watch.at < WATCH_MS ? watch : null;
  const st = info?.status ?? null;
  const fresh = Boolean(st) && (!live || (stamp(st!.updated_at) >= live.at - 5000 && (!st!.version || st!.version === live.version)));
  if (st && fresh && activeStates.has(st.state)) return { kind: "active", state: st.state, version: st.version ?? live?.version ?? "", progress: st.progress, restarting };
  if (st && fresh && live && st.state === "done") return { kind: "done", version: st.version ?? live.version };
  if (st && fresh && (st.state === "failed" || st.state === "rolled_back") && (live || now - stamp(st.finished_at ?? st.updated_at) < RECENT_MS)) {
    return { kind: "failed", version: st.version ?? "", code: st.code ?? "", rolledBackTo: st.rolled_back_to ?? "" };
  }
  if (info?.request) return { kind: "active", state: "queued", version: info.request.version, progress: 5, restarting };
  if (live) return { kind: "active", state: "queued", version: live.version, progress: 5, restarting };
  return { kind: "none" };
}

export type UpdateFlow = { info: SystemUpdate | null; view: UpdateView; watch: (version: string, channel: UpdateChannel) => void; dismissView: () => void; setInfo: (value: SystemUpdate) => void };

/**
 * Reads /api/system/update. The server answers from its cache, so a first read can be empty; ask again shortly.
 * While an update is requested or running it polls every 2 s and rides out the App restarting: a failed read then
 * only turns "Restarting…" on, and the poll goes on until the host reports the end.
 */
export function useSystemUpdate(enabled: boolean): UpdateFlow {
  const [info, setInfo] = useState<SystemUpdate | null>(null);
  const [watch, setWatch] = useState<Watch | null>(null);
  const [restarting, setRestarting] = useState(false);
  const [dismissed, setDismissed] = useState("");
  const [round, setRound] = useState(0);
  const watchRef = useRef<Watch | null>(null);
  const busyRef = useRef(false);
  watchRef.current = watch;
  useEffect(() => {
    if (!enabled) { setInfo(null); return; }
    let disposed = false;
    let timer = 0;
    const load = (attempt: number) => {
      request<SystemUpdate>("/api/system/update", { cache: "no-store" }).then(value => {
        if (disposed) return;
        setInfo(value);
        setRestarting(false);
        const busy = deriveUpdateView(value, watchRef.current, false, Date.now()).kind === "active";
        busyRef.current = busy;
        if (busy) timer = window.setTimeout(() => load(attempt), POLL_MS);
        else if (!value.checked_at && attempt < 4) timer = window.setTimeout(() => load(attempt + 1), 3000);
      }).catch(() => {
        if (disposed || !(busyRef.current || watchRef.current)) return;
        setRestarting(true);
        timer = window.setTimeout(() => load(attempt), POLL_MS);
      });
    };
    load(0);
    return () => { disposed = true; window.clearTimeout(timer); };
  }, [enabled, round]);
  const view = deriveUpdateView(info, watch, restarting, Date.now());
  const hidden = view.kind === "failed" && dismissed !== "" && dismissed === (info?.status?.updated_at ?? "");
  const follow = useCallback((version: string, channel: UpdateChannel) => {
    busyRef.current = true;
    setDismissed("");
    setWatch({ version, channel, at: Date.now() });
    setRound(value => value + 1);
  }, []);
  // The banner and the Version card are separate readers; a request made in one starts the other's polling too.
  const startWatch = useCallback((version: string, channel: UpdateChannel) => {
    follow(version, channel);
    window.dispatchEvent(new CustomEvent<{ version: string; channel: UpdateChannel }>(WATCH_EVENT, { detail: { version, channel } }));
  }, [follow]);
  useEffect(() => {
    if (!enabled) return;
    const onWatch = (event: Event) => {
      const detail = (event as CustomEvent<{ version: string; channel: UpdateChannel }>).detail;
      if (!watchRef.current) follow(detail.version, detail.channel);
    };
    window.addEventListener(WATCH_EVENT, onWatch);
    return () => window.removeEventListener(WATCH_EVENT, onWatch);
  }, [enabled, follow]);
  const dismissView = useCallback(() => { setDismissed(info?.status?.updated_at ?? ""); setWatch(null); }, [info]);
  return { info, view: hidden ? { kind: "none" } : view, watch: startWatch, dismissView, setInfo };
}

function safeNotes(url: string): string {
  return url.startsWith("https://") ? url : "";
}

const NOTHING_CHANGED = new Set(["request_invalid", "request_too_large", "request_stale", "channel_mismatch", "not_newer", "rejected_before", "not_latest_stable", "version_unpublished", "release_check_failed", "runs_still_active", "host_busy", "schema_change_blocked", "insufficient_disk"]);
const knownCodes = new Set([...NOTHING_CHANGED, "update_rejected", "rollback_failed", "update_failed"]);

/** Step text, a progress bar while it runs, the reload button when it is over, the code and the rollback when it failed. */
export function UpdateProgress({ view, onReload, onDismiss }: { view: Exclude<UpdateView, { kind: "none" }>; onReload: () => void; onDismiss: () => void }) {
  const { t } = useTranslation("settings");
  if (view.kind === "done") {
    return <span className="update-progress" role="status" data-update-state="done">
      <TofiIcon name="check-circle" size={16} aria-hidden="true" /><span>{t("update.progress.done", { version: view.version })}</span>
      <button type="button" className="secondary-button update-btn" onClick={onReload}>{t("update.progress.reload")}</button>
    </span>;
  }
  if (view.kind === "failed") {
    const known = knownCodes.has(view.code);
    return <span className="update-progress is-failed" role="alert" data-update-state="failed">
      <TofiIcon name="alert" size={16} aria-hidden="true" />
      <span className="update-progress-copy">
        <strong>{t("update.failed.title", { version: view.version || "" })}</strong>
        <span>{t(`update.error.${known ? view.code as "update_failed" : "generic"}`)}</span>
        <span className="update-progress-meta">
          {view.rolledBackTo ? t("update.failed.rolled_back", { version: view.rolledBackTo }) : NOTHING_CHANGED.has(view.code) ? t("update.failed.unchanged") : null}
          {view.code && <code>{t("update.failed.code", { code: view.code })}</code>}
        </span>
      </span>
      <button type="button" className="secondary-button update-btn" onClick={onDismiss}>{t("update.failed.dismiss")}</button>
    </span>;
  }
  const label = view.restarting ? t("update.progress.restarting")
    : view.state === "downloading" || view.state === "switching" ? t(`update.progress.${view.state}`, { version: view.version })
    : t(`update.progress.${view.state === "verifying" || view.state === "waiting_for_runs" ? view.state : "queued"}`);
  const percent = view.restarting ? 90 : Math.max(0, Math.min(100, view.progress ?? 5));
  return <span className="update-progress" role="status" aria-live="polite" data-update-state={view.restarting ? "restarting" : view.state}>
    <TofiIcon name="loading" size={16} aria-hidden="true" />
    <span>{label}</span>
    <span className="update-progress-bar" role="progressbar" aria-label={t("update.progress.label")} aria-valuemin={0} aria-valuemax={100} aria-valuenow={percent}><i style={{ width: `${percent}%` }} /></span>
  </span>;
}

type Offer = { version: string; channel: UpdateChannel; notes: string };

/** The buttons that open the confirm dialog, and the dialog. Admin only (the callers check). */
function useUpdateOffers(flow: UpdateFlow) {
  const { info, watch, setInfo } = flow;
  const [open, setOpen] = useState<Offer | null>(null);
  const stable: Offer | null = info?.host_update && info.update_available ? { version: info.latest, channel: "stable", notes: safeNotes(info.notes_url) } : null;
  const rc: Offer | null = info?.host_update && info.latest_rc ? { version: info.latest_rc, channel: "rc", notes: safeNotes(info.latest_rc_notes_url ?? "") } : null;
  const confirm = useCallback(async (offer: Offer) => {
    await request("/api/system/update/request", { method: "POST", body: JSON.stringify({ version: offer.version, channel: offer.channel }) });
    watch(offer.version, offer.channel);
    setOpen(null);
  }, [watch]);
  const dialog = open && <Suspense fallback={null}><UpdateDialog offer={open} onConfirm={() => confirm(open)} onClose={() => setOpen(null)} /></Suspense>;
  return { stable, rc, open: setOpen, dialog, setInfo };
}

function OfferButton({ offer, onOpen }: { offer: Offer; onOpen: (offer: Offer) => void }) {
  const { t } = useTranslation("settings");
  return <button type="button" className="secondary-button update-btn" data-update-action={offer.channel} onClick={() => onOpen(offer)}>
    {offer.channel === "rc" ? t("update.action.try_rc", { version: offer.version }) : t("update.action.update", { version: offer.version })}
  </button>;
}

/** Dismissible strip above the workspace. Dismissal is remembered per version; a running update is never hidden. */
export function UpdateBanner() {
  const { t } = useTranslation("settings");
  const admin = useIsUpdateAdmin();
  const flow = useSystemUpdate(admin);
  const { info, view } = flow;
  const offers = useUpdateOffers(flow);
  const [dismissed, setDismissed] = useState(readDismissed);
  const dismiss = useCallback((version: string) => {
    try { window.localStorage.setItem(dismissKey, version); } catch { /* storage unavailable: hide for this page view only */ }
    setDismissed(version);
  }, []);
  if (!admin) return null;
  if (view.kind !== "none") {
    return <div className="update-banner" data-update-banner data-update-running>
      <UpdateProgress view={view} onReload={() => window.location.reload()} onDismiss={flow.dismissView} />
    </div>;
  }
  if (!info?.update_available || dismissed === info.latest) return null;
  const notes = safeNotes(info.notes_url);
  const automatic = info.auto_update === "patch" && isPatchBump(info.current, info.latest);
  return <div className="update-banner" role="status" data-update-banner>
    <TofiIcon name="info" size={16} aria-hidden="true" />
    <p>{automatic
      ? t("update.banner.automatic", { version: info.latest })
      : info.host_update ? t("update.banner.available_short", { version: info.latest })
      : <Trans t={t} i18nKey="update.banner.available" values={{ version: info.latest }} components={{ code: <code /> }} />}</p>
    {offers.stable && <OfferButton offer={offers.stable} onOpen={offers.open} />}
    {notes && <a className="update-banner-link" href={notes} target="_blank" rel="noopener noreferrer">{t("update.banner.whats_new")}</a>}
    <button type="button" className="update-banner-dismiss" aria-label={t("update.banner.dismiss")} onClick={() => dismiss(info.latest)}><TofiIcon name="close" size={16} /></button>
    {offers.dialog}
  </div>;
}

/** Settings -> Advanced -> Version. Auto-update is toggled on the server, so the card shows the command. */
export function VersionCard() {
  const { t } = useTranslation("settings");
  const { timezone } = useUserTimezone();
  const admin = useIsUpdateAdmin();
  const flow = useSystemUpdate(admin);
  const { info, view } = flow;
  const offers = useUpdateOffers(flow);
  const [rcBusy, setRcBusy] = useState(false);
  const [rcError, setRcError] = useState("");
  if (!admin) return null;
  const known = Boolean(info?.latest);
  const notes = info ? safeNotes(info.notes_url) : "";
  const rcNotes = info ? safeNotes(info.latest_rc_notes_url ?? "") : "";
  const status = !info || !known ? null : info.update_available ? <StatusBadge state="need">{t("update.card.status_available")}</StatusBadge> : <StatusBadge state="ok">{t("update.card.status_current")}</StatusBadge>;
  const fromPage = Boolean(info?.host_update);
  async function setRc(next: boolean) {
    setRcBusy(true); setRcError("");
    try { flow.setInfo(await request<SystemUpdate>("/api/system/update/settings", { method: "PUT", body: JSON.stringify({ channel_rc: next }) })); }
    catch (cause) { setRcError(cause instanceof ApiError ? cause.message : t("update.rc.save_failed")); }
    finally { setRcBusy(false); }
  }
  return <SettingsCard className="version-card">
    <SettingsRow label={t("update.card.current")} control={<span className="version-value"><code>{info?.current ?? "-"}</code>{status}</span>} />
    <SettingsRow label={t("update.card.latest")} description={info?.update_available && !fromPage ? <Trans t={t} i18nKey="update.card.install_hint" components={{ code: <code /> }} /> : undefined}
      control={<span className="version-value">{known ? <code>{info!.latest}</code> : <span className="version-muted">{t("update.card.unknown")}</span>}{notes && <a href={notes} target="_blank" rel="noopener noreferrer">{t("update.card.notes")}</a>}</span>} />
    {info?.latest_rc && <SettingsRow label={t("update.rc.latest")}
      control={<span className="version-value"><code>{info.latest_rc}</code>{rcNotes && <a href={rcNotes} target="_blank" rel="noopener noreferrer">{t("update.card.notes")}</a>}</span>} />}
    {fromPage && (view.kind !== "none" || offers.stable || offers.rc) && <SettingsRow label={t("update.card.update")} description={t("update.card.update_hint")}
      control={view.kind !== "none"
        ? <UpdateProgress view={view} onReload={() => window.location.reload()} onDismiss={flow.dismissView} />
        : <span className="version-value">{offers.stable && <OfferButton offer={offers.stable} onOpen={offers.open} />}{offers.rc && <OfferButton offer={offers.rc} onOpen={offers.open} />}</span>} />}
    {fromPage && <SettingsRow label={t("update.rc.label")} labelFor="update-rc" description={rcError || t("update.rc.hint")}
      control={<label className="debug-toggle auto-switch"><input id="update-rc" type="checkbox" role="switch" aria-label={t("update.rc.label")} checked={info?.channel_rc_enabled === true} disabled={rcBusy} onChange={event => void setRc(event.target.checked)} /></label>} />}
    <SettingsRow label={t("update.card.checked")} control={<span className="version-value">{info?.checked_at ? formatDateTime(info.checked_at, { dateStyle: "medium", timeStyle: "short", ...(timezone ? { timeZone: timezone } : {}) }) : <span className="version-muted">{t("update.card.never")}</span>}</span>} />
    <SettingsRow label={t("update.card.auto")} description={<Trans t={t} i18nKey="update.card.auto_hint" components={{ on: <code />, off: <code /> }} />}
      control={<span className="version-value" data-auto-update={info?.auto_update ?? "off"}>{info?.auto_update === "patch" ? t("update.card.auto_on") : t("update.card.auto_off")}</span>} />
    {offers.dialog}
  </SettingsCard>;
}
