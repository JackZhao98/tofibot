import { Suspense, lazy, useEffect, useRef, useState, type FormEvent } from "react";
// Types only: importing the runtime half of ../desktop would bind workspace IPC this page must not touch.
import type { DesktopDeviceStatus, DesktopSandboxSelection } from "../desktop";
import { TofiIcon } from "../icons";
import { useTranslation } from "../i18n";
import { Banner, SettingsCard, SettingsRow, SettingsSection } from "../settings/components";
import { RemoteControlBlock } from "../RemoteControl";
import { useRemoteControl } from "../useRemoteControl";

// Isolation (enforced by scripts/test-desktop-settings-isolation.mjs): this page's only data source is
// window.tofiDesktop. No server calls, no chat or artifact code, nothing rendered as HTML; Bot names are text.
const MacConfirm = lazy(() => import("./MacConfirm"));

const POLL_MS = 1500;
const message = (cause: unknown) => String((cause as { message?: unknown } | null)?.message || cause || "").slice(0, 2000);

type Pending = "" | "load" | "refresh" | "workspace" | "save" | "choose" | "authorize" | "revoke";
type NoteKey = "idle" | "pending" | "noApi" | "cancelled" | "changed" | "unknown" | "authorized" | "unconfirmed" | "revoked" | "revoking" | "revokePending";
type Confirm = { kind: "revoke" } | null;
type Draft = { workspaceRoot: string; files: boolean };

function Switch({ checked, label, disabled, onChange }: { checked: boolean; label: string; disabled: boolean; onChange: (value: boolean) => void }) {
  return <label className="debug-toggle auto-switch"><input type="checkbox" role="switch" aria-label={label} checked={checked} disabled={disabled} onChange={event => onChange(event.target.checked)} /></label>;
}

export function MacSettingsPage() {
  const { t } = useTranslation("settings");
  const api = window.tofiDesktop;
  const has = (name: "getSettings" | "chooseWorkspace" | "chooseSandboxRoot" | "authorizeSandboxRoot" | "sandboxRevoke" | "updateSettings") => typeof api?.[name] === "function";

  // The same switch as Settings > Environments: a new app on an older server has no switch in its workspace, and that
  // server's "Your Mac" dialog sends people here. The app answers from this document too, and the dialog is its own.
  const rc = useRemoteControl(true);
  const [status, setStatus] = useState<DesktopDeviceStatus | null>(null);
  const [known, setKnownState] = useState(false);
  const [draft, setDraft] = useState<Draft>({ workspaceRoot: "", files: false });
  const [error, setError] = useState("");
  const [pending, setPendingState] = useState<Pending>("");
  const [selection, setSelectionState] = useState<DesktopSandboxSelection | null>(null);
  const [note, setNote] = useState<NoteKey>("idle");
  const [confirm, setConfirm] = useState<Confirm>(null);

  // Truth for async continuations; the state above only drives rendering.
  const mounted = useRef(true);
  const busy = useRef<Pending>("");
  const readID = useRef(0);
  const passive = useRef(0);
  const knownRef = useRef(false);
  const statusRef = useRef<DesktopDeviceStatus | null>(null);
  const selectionRef = useRef<DesktopSandboxSelection | null>(null);
  const setKnown = (value: boolean) => { knownRef.current = value; setKnownState(value); };
  const setSelection = (value: DesktopSandboxSelection | null) => { selectionRef.current = value; setSelectionState(value); };

  function applyStatus(next: DesktopDeviceStatus, hydrate = false) {
    if (!next || typeof next.sandbox !== "object" || !next.sandbox) throw new Error(t("mac.error.noStatus"));
    const before = statusRef.current?.sandbox;
    const sandbox = next.sandbox;
    if (knownRef.current && before && (before.connectionID !== sandbox.connectionID || before.enabled !== sandbox.enabled || before.bound !== sandbox.bound || before.root !== sandbox.root || sandbox.available !== true || sandbox.faulted === true)) {
      setSelection(null);
      setNote("changed");
    }
    statusRef.current = next;
    setStatus(next);
    setKnown(true);
    if (hydrate) setDraft({ workspaceRoot: next.workspaceRoot, files: next.fileAccess });
  }

  async function refresh(hydrate = false, quiet = false) {
    if (!api?.getSettings) return;
    const id = ++readID.current;
    if (quiet) passive.current = id;
    try {
      const next = await api.getSettings();
      if (!mounted.current || id !== readID.current) return;
      applyStatus(next, hydrate);
    } catch (cause) {
      if (!mounted.current || id !== readID.current) return;
      setKnown(false);
      setSelection(null);
      setNote("unknown");
      throw cause;
    } finally {
      if (passive.current === id) passive.current = 0;
    }
  }

  async function recover(cause: unknown) {
    if (!mounted.current) return;
    setKnown(false);
    setSelection(null);
    setNote("idle");
    try { await refresh(); } catch { /* refresh already fails closed and leaves the recovery state. */ }
    if (mounted.current) setError(t("mac.error.action", { message: message(cause) }));
  }

  async function operation(kind: Exclude<Pending, "">, work: () => Promise<void>) {
    if (!mounted.current || busy.current) return;
    // A slow passive read cannot publish pre-operation state over this operation's result.
    readID.current++;
    passive.current = 0;
    busy.current = kind;
    setPendingState(kind);
    setError("");
    try { await work(); }
    catch (cause) { await recover(cause); }
    finally {
      if (mounted.current) { busy.current = ""; setPendingState(""); }
    }
  }

  const latest = useRef({ refresh, operation });
  latest.current = { refresh, operation };
  useEffect(() => {
    mounted.current = true;
    if (has("getSettings")) void latest.current.operation("load", () => latest.current.refresh(true));
    const timer = window.setInterval(() => {
      if (document.hidden || busy.current || passive.current) return;
      void latest.current.refresh(false, true).catch(() => { /* The status rows show recovery guidance; drafts stay. */ });
    }, POLL_MS);
    return () => { mounted.current = false; window.clearInterval(timer); };
  }, []);
  useEffect(() => { document.title = `${t("mac.title")} · Tofi`; }, [t]);

  const sandbox = status?.sandbox;
  const locked = !!pending || !status;
  const running = sandbox?.running === true;
  const bound = known && sandbox?.enabled === true && sandbox.bound === true && !!sandbox.root;
  const available = known && sandbox?.available === true && sandbox.faulted !== true;

  const chooseFolder = () => operation("choose", async () => {
    const value = await api?.chooseSandboxRoot?.();
    if (!mounted.current || !value) return;
    if (typeof value.selectionID !== "string" || !value.selectionID || typeof value.root !== "string" || !value.root || typeof value.serverLabel !== "string" || !value.serverLabel) throw new Error(t("mac.error.invalidSelection"));
    setSelection({ selectionID: value.selectionID, root: value.root, serverLabel: value.serverLabel });
    setNote("pending");
  });
  const authorize = () => {
    const selected = selectionRef.current;
    if (!selected) return;
    return operation("authorize", async () => {
      const next = await api?.authorizeSandboxRoot?.(selected.selectionID);
      if (!mounted.current) return;
      if (next === null || next === undefined) { setNote("cancelled"); return; }
      applyStatus(next);
      setSelection(null);
      const after = statusRef.current?.sandbox;
      setNote(after?.enabled === true && after.bound === true && after.root === selected.root ? "authorized" : "unconfirmed");
    });
  };
  const revoke = () => operation("revoke", async () => {
    setSelection(null);
    setKnown(false);
    setNote("revoking");
    const next = await api?.sandboxRevoke?.();
    if (!mounted.current) return;
    if (next?.sandbox) applyStatus(next); else await refresh();
    const after = statusRef.current?.sandbox;
    setNote(!after?.enabled && !after?.bound ? "revoked" : "revokePending");
  });
  const save = (event: FormEvent) => {
    event.preventDefault();
    return operation("save", async () => {
      // Directory authority is issued only by the separate native confirmation, never by this form.
      await api?.updateSettings?.({ workspaceRoot: draft.workspaceRoot, fileAccess: draft.files });
      if (mounted.current) await refresh(true);
    });
  };
  const summary = !known ? t("mac.local.summary.unknown")
    : sandbox?.faulted ? t("mac.local.summary.faulted", { reason: message(sandbox.reason) || t("mac.local.reason.faulted") })
    : !sandbox?.available ? t("mac.local.summary.unavailable", { reason: message(sandbox?.reason) || t("mac.local.reason.unavailable") })
    : !sandbox.enabled ? t("mac.local.summary.disabled")
    : !bound ? t("mac.local.summary.unbound")
    : running ? t("mac.local.summary.running") : t("mac.local.summary.ready");
  const noteText = !has("authorizeSandboxRoot") ? t("mac.local.select.noApi") : t(`mac.local.select.${selection && note === "idle" ? "pending" : note}`);
  return <div className="workspace mac-settings">
    <div className="mac-settings-titlebar" aria-hidden="true" />
    <main className="settings-page-body"><div className="mac-settings-inner">
      <header className="mac-settings-head">
        <h1>{t("mac.title")}<span className="mac-dev-tag">{t("mac.inDevelopment")}</span></h1>
        <a className="secondary-button mac-settings-back" href="/"><TofiIcon name="arrow-left" size={16} aria-hidden="true" />{t("mac.back")}</a>
      </header>

      {!has("getSettings") && <Banner tone="warn" title={t("mac.unavailable")} />}
      {error && <Banner tone="error" title={error} />}
      {status?.lastError && <Banner tone="error" title={t("mac.error.last")}>{message(status.lastError)}</Banner>}
      {has("getSettings") && !status && !error && <p className="settings-sec-desc" role="status">{t("mac.loading")}</p>}

      <RemoteControlBlock rc={rc} offered onOpenFiles={null} onOpenDenied={() => {}} menu={false} />

      <form className="mac-form" onSubmit={save}>
        <SettingsSection title={t("mac.caps.title")} description={t("mac.caps.description")}>
          <SettingsCard>
            <SettingsRow label={t("mac.device.folder")} description={<>{t("mac.device.folderHint")}<code dir="auto">{draft.workspaceRoot}</code></>} control={
              <button type="button" className="secondary-button" disabled={locked || running || !has("chooseWorkspace")} onClick={() => void operation("workspace", async () => {
                const value = await api?.chooseWorkspace?.();
                if (mounted.current && value) setDraft(current => ({ ...current, workspaceRoot: value }));
              })}><TofiIcon name="folder-open" size={16} aria-hidden="true" />{pending === "workspace" ? t("mac.device.choosing") : t("mac.device.choose")}</button>} />
            <SettingsRow inline label={t("mac.caps.files.label")} description={t("mac.caps.files.hint")} control={<Switch checked={draft.files} label={t("mac.caps.files.label")} disabled={locked} onChange={files => setDraft({ ...draft, files })} />} />
            <div className="settings-row-v2 mac-save-row"><button type="submit" className="primary-button" disabled={locked || running || !has("updateSettings")}>{pending === "save" ? t("mac.caps.saving") : t("mac.caps.save")}</button></div>
          </SettingsCard>
        </SettingsSection>
      </form>

      <SettingsSection title={t("mac.local.title")} description={t("mac.local.description")}>
        <SettingsCard>
          <SettingsRow label={t("mac.local.root")} description={<><span role="status">{summary}</span><code dir="auto">{!known ? t("mac.local.summary.unknown") : bound ? sandbox?.root : t("mac.local.noRoot")}</code></>} />
          <SettingsRow label={t("mac.local.selected")} description={<><code dir="auto">{selection?.root || t("mac.local.none")}</code>{t("mac.local.server")}: <code dir="auto">{selection?.serverLabel || t("mac.local.serverNone")}</code></>} control={<>
            <button type="button" className="secondary-button" disabled={locked || running || !available || !has("chooseSandboxRoot")} onClick={() => void chooseFolder()}>{pending === "choose" ? t("mac.local.choosing") : t("mac.local.choose")}</button>
            <button type="button" className="secondary-button" disabled={locked || running || !available || !selection || !has("authorizeSandboxRoot")} aria-describedby="mac-local-boundary" onClick={() => void authorize()}>{pending === "authorize" ? t("mac.local.authorizing") : t("mac.local.authorize")}</button>
            <button type="button" className="secondary-button" disabled={locked || !has("sandboxRevoke") || !(sandbox?.enabled || sandbox?.bound || running || selection)} onClick={() => setConfirm({ kind: "revoke" })}>{pending === "revoke" ? t("mac.local.revoking") : t("mac.local.revoke")}</button>
          </>} />
          <p className="settings-card-note" role="status">{noteText}</p>
          <p className="settings-card-note" id="mac-local-boundary">{t("mac.local.boundary")}</p>
          <p className="settings-card-note">{t("mac.local.persist")}</p>
          <SettingsRow label={t("mac.local.run")} control={<a className="secondary-button" href="/__desktop/local-run"><TofiIcon name="terminal" size={16} aria-hidden="true" />{t("mac.local.run")}</a>} />
        </SettingsCard>
      </SettingsSection>
    </div></main>

    {confirm && <Suspense fallback={null}><MacConfirm
      title={t("mac.confirm.revoke.title")}
      body={t("mac.confirm.revoke.body")}
      confirmLabel={t("mac.confirm.revoke.confirm")}
      onCancel={() => setConfirm(null)}
      onConfirm={() => { setConfirm(null); void revoke(); }} /></Suspense>}
  </div>;
}
