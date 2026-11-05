import { Suspense, lazy, useEffect, useRef, useState, type FormEvent } from "react";
// Types only: importing the runtime half of ../desktop would bind workspace IPC this page must not touch.
import type { DesktopDeviceStatus, DesktopSandboxSelection } from "../desktop";
import { TofiIcon } from "../icons";
import { useTranslation } from "../i18n";
import { Banner, SettingsCard, SettingsRow, SettingsSection, StatusBadge } from "../settings/components";

// Isolation (enforced by scripts/test-desktop-settings-isolation.mjs): this page's only data source is
// window.tofiDesktop. No server calls, no chat or artifact code, nothing rendered as HTML; Bot names are text.
const MacConfirm = lazy(() => import("./MacConfirm"));

const POLL_MS = 1500;
const message = (cause: unknown) => String((cause as { message?: unknown } | null)?.message || cause || "").slice(0, 2000);

type Pending = "" | "load" | "refresh" | "workspace" | "save" | "pair" | "bot" | "control" | "permission" | "choose" | "authorize" | "revoke" | "relaunch";
type NoteKey = "idle" | "pending" | "noApi" | "cancelled" | "changed" | "unknown" | "authorized" | "unconfirmed" | "revoked" | "revoking" | "revokePending";
type Confirm = { kind: "deny"; botID: string; name: string } | { kind: "revoke" } | null;
type Draft = { name: string; workspaceRoot: string; files: boolean; screen: boolean; control: boolean };
type PermissionKind = "screen" | "accessibility";

function Switch({ checked, label, disabled, onChange }: { checked: boolean; label: string; disabled: boolean; onChange: (value: boolean) => void }) {
  return <label className="debug-toggle auto-switch"><input type="checkbox" role="switch" aria-label={label} checked={checked} disabled={disabled} onChange={event => onChange(event.target.checked)} /></label>;
}

export function MacSettingsPage() {
  const { t } = useTranslation("settings");
  const api = window.tofiDesktop;
  const has = (name: "getSettings" | "chooseWorkspace" | "openPermissionGuide" | "chooseSandboxRoot" | "authorizeSandboxRoot" | "sandboxRevoke" | "setBotDecision" | "controlStop" | "controlResume" | "updateSettings" | "pair" | "relaunch") => typeof api?.[name] === "function";

  const [status, setStatus] = useState<DesktopDeviceStatus | null>(null);
  const [known, setKnownState] = useState(false);
  const [draft, setDraft] = useState<Draft>({ name: "", workspaceRoot: "", files: false, screen: false, control: false });
  const [code, setCode] = useState("");
  const [error, setError] = useState("");
  const [pending, setPendingState] = useState<Pending>("");
  const [selection, setSelectionState] = useState<DesktopSandboxSelection | null>(null);
  const [note, setNote] = useState<NoteKey>("idle");
  const [guiding, setGuiding] = useState<"" | PermissionKind>("");
  // Kinds whose System Settings pane this page opened; the stale-entry and reopen notes only make sense after that.
  const [guided, setGuided] = useState<PermissionKind[]>([]);
  const [leaving, setLeaving] = useState(false);
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
    if (hydrate) setDraft({ name: next.name, workspaceRoot: next.workspaceRoot, files: next.fileAccess, screen: next.screenCapture, control: next.desktopControl });
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
  // The permission note stays until the permission it explains is granted.
  useEffect(() => { if (guiding && status?.permissions?.[guiding]) setGuiding(""); }, [guiding, status]);

  const sandbox = status?.sandbox;
  const locked = !!pending || !status || leaving;
  const running = sandbox?.running === true;
  const bound = known && sandbox?.enabled === true && sandbox.bound === true && !!sandbox.root;
  const available = known && sandbox?.available === true && sandbox.faulted !== true;
  const phase = status?.control?.phase;

  const guide = (kind: PermissionKind) => operation("permission", async () => {
    const opened = await api?.openPermissionGuide?.(kind);
    if (!mounted.current) return;
    setGuiding(opened ? kind : "");
    if (opened) setGuided(list => list.includes(kind) ? list : [...list, kind]);
  });
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
  const decide = (botID: string, decision: "allow" | "deny" | "forget") => operation("bot", async () => {
    const next = await api?.setBotDecision?.(botID, decision);
    if (next && mounted.current) applyStatus(next);
  });
  const control = (action: "controlStop" | "controlResume") => operation("control", async () => {
    const next = await api?.[action]?.();
    if (next && mounted.current) applyStatus(next);
  });
  const save = (event: FormEvent) => {
    event.preventDefault();
    return operation("save", async () => {
      // Directory authority is issued only by the separate native confirmation, never by this form.
      await api?.updateSettings?.({ name: draft.name, workspaceRoot: draft.workspaceRoot, fileAccess: draft.files, screenCapture: draft.screen, desktopControl: draft.control });
      if (mounted.current) await refresh(true);
    });
  };
  const pair = (event: FormEvent) => {
    event.preventDefault();
    return operation("pair", async () => {
      await api?.pair?.(code);
      if (mounted.current) { setCode(""); await refresh(true); }
    });
  };

  const summary = !known ? t("mac.local.summary.unknown")
    : sandbox?.faulted ? t("mac.local.summary.faulted", { reason: message(sandbox.reason) || t("mac.local.reason.faulted") })
    : !sandbox?.available ? t("mac.local.summary.unavailable", { reason: message(sandbox?.reason) || t("mac.local.reason.unavailable") })
    : !sandbox.enabled ? t("mac.local.summary.disabled")
    : !bound ? t("mac.local.summary.unbound")
    : running ? t("mac.local.summary.running") : t("mac.local.summary.ready");
  const noteText = !has("authorizeSandboxRoot") ? t("mac.local.select.noApi") : t(`mac.local.select.${selection && note === "idle" ? "pending" : note}`);
  const controlText = phase === "paused" ? t(status?.control?.reason === "user_takeover" ? "mac.bots.control.paused_takeover" : "mac.bots.control.paused_stop")
    : phase === "active" || phase === "lingering" ? t("mac.bots.control.active") : t("mac.bots.control.idle");
  const decisionText = (decision?: string) => decision === "allow" ? t("mac.bots.allowed") : decision === "deny" ? t("mac.bots.denied") : t("mac.bots.waiting");

  const permission = (kind: PermissionKind) => {
    const granted = status?.permissions?.[kind] === true;
    return <SettingsRow key={kind} label={t(`mac.perm.${kind}.label`)} description={t(`mac.perm.${kind}.hint`)} inline control={<>
      <StatusBadge state={granted ? "ok" : "need"}>{granted ? t("mac.perm.granted") : t("mac.perm.missing")}</StatusBadge>
      {!granted && <button type="button" className="secondary-button" disabled={locked || !has("openPermissionGuide")} onClick={() => void guide(kind)}>{t("mac.perm.grant")}</button>}
    </>} />;
  };

  return <div className="workspace mac-settings">
    <div className="mac-settings-titlebar" aria-hidden="true" />
    <main className="settings-page-body"><div className="mac-settings-inner">
      <header className="mac-settings-head">
        <h1>{t("mac.title")}</h1>
        <a className="secondary-button mac-settings-back" href="/"><TofiIcon name="arrow-left" size={16} aria-hidden="true" />{t("mac.back")}</a>
      </header>

      {!has("getSettings") && <Banner tone="warn" title={t("mac.unavailable")} />}
      {error && <Banner tone="error" title={error} />}
      {status?.lastError && <Banner tone="error" title={t("mac.error.last")}>{message(status.lastError)}</Banner>}
      {has("getSettings") && !status && !error && <p className="settings-sec-desc" role="status">{t("mac.loading")}</p>}

      <form className="mac-form" onSubmit={save}>
        <SettingsSection title={t("mac.device.title")} description={t("mac.device.description")}>
          <SettingsCard>
            <SettingsRow label={t("mac.device.name")} description={t("mac.device.nameHint")} labelFor="mac-name" control={
              <input id="mac-name" className="mac-input" maxLength={80} required value={draft.name} disabled={locked} onChange={event => setDraft({ ...draft, name: event.target.value })} />} />
            <SettingsRow label={t("mac.device.folder")} description={<>{t("mac.device.folderHint")}<code dir="auto">{draft.workspaceRoot}</code></>} control={
              <button type="button" className="secondary-button" disabled={locked || running || !has("chooseWorkspace")} onClick={() => void operation("workspace", async () => {
                const value = await api?.chooseWorkspace?.();
                if (mounted.current && value) setDraft(current => ({ ...current, workspaceRoot: value }));
              })}><TofiIcon name="folder-open" size={16} aria-hidden="true" />{pending === "workspace" ? t("mac.device.choosing") : t("mac.device.choose")}</button>} />
          </SettingsCard>
        </SettingsSection>

        <SettingsSection title={t("mac.caps.title")} description={t("mac.caps.description")}>
          <SettingsCard>
            <SettingsRow inline label={t("mac.caps.files.label")} description={t("mac.caps.files.hint")} control={<Switch checked={draft.files} label={t("mac.caps.files.label")} disabled={locked} onChange={files => setDraft({ ...draft, files })} />} />
            <SettingsRow inline label={t("mac.caps.screen.label")} description={t("mac.caps.screen.hint")} control={<Switch checked={draft.screen} label={t("mac.caps.screen.label")} disabled={locked} onChange={screen => setDraft({ ...draft, screen })} />} />
            <SettingsRow inline label={t("mac.caps.control.label")} description={t("mac.caps.control.hint")} control={<Switch checked={draft.control} label={t("mac.caps.control.label")} disabled={locked} onChange={value => setDraft({ ...draft, control: value })} />} />
            <div className="settings-row-v2 mac-save-row"><button type="submit" className="primary-button" disabled={locked || running || !has("updateSettings")}>{pending === "save" ? t("mac.caps.saving") : t("mac.caps.save")}</button></div>
          </SettingsCard>
        </SettingsSection>
      </form>

      <SettingsSection title={t("mac.perm.title")} description={t("mac.perm.description")}>
        <SettingsCard>
          {permission("screen")}
          {permission("accessibility")}
          {guiding && <p className="settings-card-note" role="status">{t(`mac.perm.hint.${guiding}`)}</p>}
          {status && guided.some(kind => !status.permissions?.[kind]) && <p className="settings-card-note">{t("mac.perm.stale")}</p>}
          {status && !status.permissions?.screen && guided.includes("screen") && has("relaunch") && <SettingsRow inline label={t("mac.perm.reopen.label")} description={t("mac.perm.reopen.hint")} control={
            <button type="button" className="secondary-button" disabled={locked} onClick={() => void operation("relaunch", async () => {
              // Tofi takes a moment to flush and quit; a second click would reach a quitting app and show an error.
              if (await api?.relaunch?.() && mounted.current) setLeaving(true);
            })}>{pending === "relaunch" || leaving ? t("mac.perm.reopen.busy") : t("mac.perm.reopen.action")}</button>} />}
          <p className="settings-card-note">{t("mac.perm.monthly")}</p>
          <div className="settings-row-v2 mac-save-row"><button type="button" className="secondary-button" disabled={locked || !has("getSettings")} onClick={() => void operation("refresh", () => refresh())}>{pending === "refresh" ? t("mac.loading") : t("mac.refresh")}</button></div>
        </SettingsCard>
      </SettingsSection>

      <SettingsSection title={t("mac.bots.title")} description={t("mac.bots.description")}>
        <SettingsCard>
          {(status?.bots ?? []).map(bot => <SettingsRow key={bot.bot_id} label={bot.bot_name || t("mac.bots.unnamed")} description={decisionText(bot.decision)} control={<>
            {bot.decision !== "allow" && <button type="button" className="secondary-button" disabled={locked || !has("setBotDecision")} onClick={() => void decide(bot.bot_id, "allow")}>{t("mac.bots.allow")}</button>}
            {bot.decision !== "deny" && <button type="button" className="secondary-button" disabled={locked || !has("setBotDecision")} onClick={() => setConfirm({ kind: "deny", botID: bot.bot_id, name: bot.bot_name || t("mac.bots.unnamed") })}>{t("mac.bots.deny")}</button>}
            <button type="button" className="secondary-button" disabled={locked || !has("setBotDecision")} onClick={() => void decide(bot.bot_id, "forget")}>{t("mac.bots.remove")}</button>
          </>} />)}
          {status && !status.bots?.length && <p className="settings-card-note">{t("mac.bots.empty")}</p>}
          <SettingsRow label={<span role="status">{controlText}</span>} description={t("mac.bots.shortcut")} control={<>
            <button type="button" className="secondary-button" disabled={locked || !has("controlStop")} onClick={() => void control("controlStop")}><TofiIcon name="stop" size={16} aria-hidden="true" />{t("mac.bots.stop")}</button>
            <button type="button" className="secondary-button" disabled={locked || !has("controlResume")} onClick={() => void control("controlResume")}><TofiIcon name="play" size={16} aria-hidden="true" />{t("mac.bots.resume")}</button>
          </>} />
        </SettingsCard>
      </SettingsSection>

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

      <SettingsSection title={t("mac.pair.title")} description={t("mac.pair.description")}>
        <form onSubmit={pair}><SettingsCard>
          <SettingsRow label={status?.paired ? t("mac.device.paired", { id: status.deviceID ?? "" }) : t("mac.device.unpaired")} description={status?.paired && status.capabilities?.length ? t("mac.device.capabilities", { list: status.capabilities.join(", ") }) : undefined} control={status && <StatusBadge state={status.paired ? "ok" : "need"} />} />
          <SettingsRow label={t("mac.pair.code")} labelFor="mac-code" control={<input id="mac-code" className="mac-input" autoComplete="off" maxLength={256} required value={code} disabled={locked || running} onChange={event => setCode(event.target.value)} />} />
          <div className="settings-row-v2 mac-save-row"><button type="submit" className="primary-button" disabled={locked || running || !has("pair")}>{pending === "pair" ? t("mac.pair.pairing") : t("mac.pair.submit")}</button></div>
          <p className="settings-card-note">{t("mac.pair.note")}</p>
        </SettingsCard></form>
      </SettingsSection>
    </div></main>

    {confirm && <Suspense fallback={null}><MacConfirm
      title={confirm.kind === "deny" ? t("mac.confirm.deny.title", { name: confirm.name }) : t("mac.confirm.revoke.title")}
      body={confirm.kind === "deny" ? t("mac.confirm.deny.body") : t("mac.confirm.revoke.body")}
      confirmLabel={confirm.kind === "deny" ? t("mac.confirm.deny.confirm") : t("mac.confirm.revoke.confirm")}
      onCancel={() => setConfirm(null)}
      onConfirm={() => { const action = confirm; setConfirm(null); if (action.kind === "deny") void decide(action.botID, "deny"); else void revoke(); }} /></Suspense>}
  </div>;
}
