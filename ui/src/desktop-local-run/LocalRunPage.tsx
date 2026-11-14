import { useEffect, useRef, useState, type FormEvent } from "react";
import type { LocalRunEvent, LocalRunResult, LocalRunSelection, LocalRunStatus } from "../desktop";
import { useTranslation } from "../i18n";
import { Banner, SettingsCard, SettingsRow, SettingsSection } from "../settings/components";
import { detail, PageShell } from "../desktop-pages/shared";

const LIMIT = 64 * 1024;
// Keep a UTF-8 tail of at most 64 KiB, without starting inside a code point.
function tail(text: string) {
  const bytes = new TextEncoder().encode(text);
  let start = Math.max(0, bytes.length - LIMIT);
  while (start < bytes.length && (bytes[start] & 0xc0) === 0x80) start++;
  return new TextDecoder().decode(bytes.subarray(start));
}
type Attempt = {id?: string; phase: "preparing" | "running" | "stopping"; native: boolean};
type Streams = {stdout: string; stderr: string};
const empty = {stdout: "", stderr: ""};

export function LocalRunPage() {
  const {t} = useTranslation("settings");
  const api = window.tofiDesktop;
  const [status, setStatus] = useState<LocalRunStatus | null>(null);
  const [selection, setSelection] = useState<LocalRunSelection | null>(null);
  const [command, setCommand] = useState("");
  const [paths, setPaths] = useState("");
  const [pending, setPending] = useState(false);
  const [running, setRunning] = useState(false);
  const [stopping, setStopping] = useState(false);
  const [error, setError] = useState("");
  const [note, setNote] = useState("");
  const [result, setResult] = useState<LocalRunResult | null>(null);
  const [streams, setStreams] = useState<Streams>(empty);
  const [clipped, setClipped] = useState({stdout: false, stderr: false});
  const mounted = useRef(false);
  const lock = useRef(false);
  const active = useRef<Attempt | null>(null);
  const finished = useRef(new Set<string>());
  const buffers = useRef<Streams>({...empty});
  const clips = useRef({stdout: false, stderr: false});
  const frame = useRef<number | null>(null);
  const ready = !!(api?.getLocalRunStatus && api.chooseLocalRunDirectory && api.localRun && api.localRunStop && api.onLocalRunEvent);
  const native = status?.mode === "native";
  const available = ready && status?.available === true && (!native || !status.faulted) && (native || !!api?.localRunSaveFile);
  const busy = running || status?.running === true;
  function stream(name: keyof Streams, text: string, append = false) {
    const next = (append ? buffers.current[name] : "") + text;
    buffers.current[name] = tail(next);
    clips.current[name] = (append && clips.current[name]) || new TextEncoder().encode(next).length > LIMIT;
    if (frame.current === null) frame.current = requestAnimationFrame(() => {
      frame.current = null;
      if (mounted.current) { setStreams({...buffers.current}); setClipped({...clips.current}); }
    });
  }
  async function refresh() {
    setSelection(null);
    try {
      const next = await api!.getLocalRunStatus!();
      if (mounted.current) setStatus(next);
    } catch (cause) {
      if (mounted.current) { setStatus(null); setError(t("desktop_pages.local.error", {message: detail(cause)})); }
    }
  }
  async function operation(work: () => Promise<void>) {
    if (lock.current || active.current) return;
    lock.current = true; setPending(true); setError("");
    try { await work(); }
    catch (cause) { if (mounted.current) { setSelection(null); setError(t("desktop_pages.local.error", {message: detail(cause)})); } }
    finally { lock.current = false; if (mounted.current) setPending(false); }
  }
  const refreshRef = useRef(refresh); refreshRef.current = refresh;
  useEffect(() => {
    mounted.current = true;
    if (!ready) return () => { mounted.current = false; };
    let off: (() => void) | undefined;
    try {
      off = api!.onLocalRunEvent!((event: LocalRunEvent) => {
        const attempt = active.current;
        if (!mounted.current || !attempt || !event.runID || finished.current.has(event.runID) || (event.mode && (event.mode === "native") !== attempt.native) || (attempt.id && attempt.id !== event.runID)) return;
        if (event.type !== "state" && event.type !== "output") return;
        if (event.type === "output" && (!["stdout", "stderr"].includes(event.stream) || typeof event.text !== "string")) return;
        if (event.type === "state" && !["preparing", "running", "stopping"].includes(event.phase)) return;
        attempt.id = event.runID;
        if (event.type === "output") stream(event.stream, event.text, true);
        else {
          const order = ["preparing", "running", "stopping"];
          if (order.indexOf(event.phase) < order.indexOf(attempt.phase)) return;
          attempt.phase = event.phase;
          if (event.phase === "stopping") setStopping(true);
          setNote(t(`desktop_pages.local.${event.phase}`));
        }
      });
      void operation(() => refreshRef.current());
    } catch (cause) { setError(t("desktop_pages.local.error", {message: detail(cause)})); }
    const dispose = () => { mounted.current = false; off?.(); if (frame.current !== null) cancelAnimationFrame(frame.current); frame.current = null; };
    window.addEventListener("pagehide", dispose, {once: true});
    return () => { dispose(); window.removeEventListener("pagehide", dispose); };
  }, [ready, api, t]);
  const choose = () => operation(async () => {
    setSelection(null); setResult(null); setNote("");
    const next = await api!.chooseLocalRunDirectory!();
    if (!mounted.current || !next) return;
    if (!next.selectionID || (native ? next.mode !== "native" || !next.root : next.mode === "native" || !Array.isArray(next.files))) throw new Error(t("desktop_pages.local.invalid_selection"));
    // Picking never grants native directory authority, even if a stale bridge claims otherwise.
    setSelection({...next, authorized: false});
  });
  const authorize = () => operation(async () => {
    if (!selection || !native || !api?.authorizeLocalRunDirectory) return;
    const next = await api.authorizeLocalRunDirectory(selection.selectionID);
    if (!mounted.current) return;
    if (!next) { setNote(t("desktop_pages.local.cancelled")); return; }
    if (next.mode !== "native" || next.selectionID !== selection.selectionID || next.root !== selection.root || next.authorized !== true) throw new Error(t("desktop_pages.local.invalid_selection"));
    setSelection({...selection, authorized: true}); setNote(t("desktop_pages.local.authorized"));
  });
  async function run(event: FormEvent) {
    event.preventDefault();
    if (lock.current || active.current || busy || !available || !selection || (native && (!selection.authorized || !api?.authorizeLocalRunDirectory)) || !command.trim()) return;
    setError("");
    if (new TextEncoder().encode(command).length > 8192) { setError(t("desktop_pages.local.command_limit")); return; }
    const exportPaths = [...new Set(paths.split(/\r?\n/).map(line => line.trim()).filter(Boolean))];
    if (!native && (exportPaths.length > 32 || exportPaths.some(path => /[\\\x00-\x1f\x7f:]/.test(path) || path.split("/").some(part => !part || part === "." || part === "..")))) { setError(t("desktop_pages.local.invalid_paths")); return; }
    const attempt: Attempt = {native, phase: "preparing"};
    active.current = attempt; setRunning(true); setStopping(false); setResult(null); setNote(t("desktop_pages.local.preparing"));
    stream("stdout", ""); stream("stderr", "");
    let failed = false;
    try {
      const next = await api!.localRun!({selectionID: selection.selectionID, command, ...(!native ? {exportPaths} : {})});
      if (!mounted.current || active.current !== attempt) return;
      if (next?.runID) finished.current.add(next.runID);
      if (!next || !["completed", "failed", "stopped"].includes(next.status) || (native && (next.mode !== "native" || next.effects !== "direct" || next.containment !== "unverified" || (attempt.id && attempt.id !== next.runID)))) throw new Error(t("desktop_pages.local.invalid_result"));
      if (typeof next.stdout === "string") stream("stdout", next.stdout);
      if (typeof next.stderr === "string") stream("stderr", next.stderr);
      setResult(next); setStatus(current => current ? {...current, running: false} : current);
      setNote(t(`desktop_pages.local.${next.status}`));
      if (native) {
        const cleaned = next.cleanup?.scope === "managed-process-group" && ["absent", "not-started"].includes(next.cleanup.status);
        if (next.status === "failed" || next.faulted || !cleaned) setSelection(null);
        if (next.faulted || !cleaned) setStatus(current => current ? {...current, available: false, faulted: true} : current);
      }
    } catch (cause) {
      failed = true;
      if (mounted.current) { setSelection(null); setError(t("desktop_pages.local.error", {message: detail(cause)})); setNote(t("desktop_pages.local.failed")); }
    } finally {
      if (attempt.id) finished.current.add(attempt.id);
      if (active.current === attempt) active.current = null;
      if (mounted.current) { setRunning(false); setStopping(false); if (failed) await refresh(); }
    }
  }
  async function stop() {
    if (!busy || stopping) return;
    const attempt = active.current;
    if (attempt) attempt.phase = "stopping";
    setStopping(true); setError("");
    try {
      const accepted = await api!.localRunStop!();
      if (!mounted.current || active.current !== attempt) return;
      if (!attempt) { setStopping(false); await operation(refresh); }
      else { setStopping(accepted === true); setNote(t(accepted ? "desktop_pages.local.stopping" : "desktop_pages.local.stop_refused")); }
    } catch (cause) {
      if (mounted.current && active.current === attempt) { setStopping(false); if (native) setSelection(null); setError(t("desktop_pages.local.error", {message: detail(cause)})); }
    }
  }
  return <PageShell title={t("desktop_pages.local.title")} back>
    <p>{t(native ? "desktop_pages.local.native_intro" : "desktop_pages.local.intro")}</p>
    {error && <Banner tone="error" title={error}/>}
    <SettingsCard><SettingsRow label={t("desktop_pages.local.runtime")} description={<span role="status">{!ready ? t("desktop_pages.local.no_api") : !status ? t("mac.loading") : status.faulted ? t("desktop_pages.local.cleanup_unknown") : available ? t("desktop_pages.local.available") : status.reason || t("desktop_pages.local.no_api")}</span>} control={<button className="st-button" disabled={!ready || pending || running || stopping} onClick={() => void operation(refresh)}>{t("desktop_pages.local.refresh")}</button>}/></SettingsCard>
    {status?.lab && !native && <p>{t("desktop_pages.local.lab")}</p>}
    <SettingsSection title={t("desktop_pages.local.directory")}><SettingsCard>
      <SettingsRow label={selection?.name || t("desktop_pages.local.none")} description={selection?.root && <code dir="auto">{selection.root}</code>} control={<button className="st-button" disabled={pending || busy || !available} onClick={() => void choose()}>{t("desktop_pages.local.choose")}</button>}/>
      {native && <SettingsRow label={t(selection?.authorized ? "desktop_pages.local.authorized" : "desktop_pages.local.needs_authorization")} control={<button className="st-button" disabled={pending || busy || !available || !selection || selection.authorized || !api?.authorizeLocalRunDirectory} onClick={() => void authorize()}>{t("desktop_pages.local.authorize")}</button>}/>}
    </SettingsCard>
      {!native && <><p>{t("desktop_pages.local.snapshot_hint")}</p>{selection && <p>{t("desktop_pages.local.manifest", {count: selection.files?.length ?? 0, bytes: selection.totalBytes ?? 0, excluded: selection.excludedCount ?? 0})}</p>}
      <ul className="desktop-files" aria-label={t("desktop_pages.local.snapshot_files")}>{selection?.files?.map(file => <li key={file.path}><code dir="auto">{file.path}</code> · {file.bytes} B</li>)}</ul></>}
    </SettingsSection>
    <form onSubmit={event => void run(event)}>
      <label>{t("desktop_pages.local.command")}<textarea rows={3} maxLength={8192} spellCheck={false} autoCapitalize="off" autoComplete="off" value={command} disabled={busy} onChange={event => setCommand(event.target.value)} /></label>
      <p>{t("desktop_pages.local.command_hint")}</p>
      {!native && <label>{t("desktop_pages.local.paths")}<textarea rows={2} value={paths} disabled={busy} onChange={event => setPaths(event.target.value)}/></label>}
      <p id="run-boundary">{t(native ? "desktop_pages.local.native_boundary" : "desktop_pages.local.snapshot_boundary")}</p>
      <div className="desktop-actions"><button className="primary-button" type="submit" aria-describedby="run-boundary" disabled={pending || busy || !available || !selection || (native && (!selection.authorized || !api?.authorizeLocalRunDirectory)) || !command.trim()}>{t("desktop_pages.local.run")}</button>
      <button className="st-button" type="button" disabled={!busy || stopping || !api?.localRunStop} onClick={() => void stop()}>{t(stopping ? "desktop_pages.local.stopping" : "desktop_pages.local.stop")}</button></div>
    </form>
    <p role="status">{note}{Number.isInteger(result?.exitCode) && <> · {t("desktop_pages.local.exit", {code: result!.exitCode})}</>}{result?.reason && <> · {detail(result.reason)}</>}</p>
    {result?.mode === "native" && <p role="status">{t(result.cleanup?.scope === "managed-process-group" && result.cleanup.status === "not-started" && !result.faulted ? "desktop_pages.local.not_started" : result.cleanup?.scope === "managed-process-group" && result.cleanup.status === "absent" ? "desktop_pages.local.cleaned" : "desktop_pages.local.cleanup_unknown")} {t("desktop_pages.local.effects_remain")}</p>}
    {(["stdout", "stderr"] as const).map(name => <SettingsSection key={name} title={t(`desktop_pages.local.${name}`)}><Output name={name} label={t(`desktop_pages.local.${name}`)} text={streams[name]}/>{clipped[name] && <p>{t("desktop_pages.local.clipped")}</p>}</SettingsSection>)}
    {!native && <SettingsSection title={t("desktop_pages.local.results")}>
      {!result?.files?.length && <p>{t("desktop_pages.local.no_files")}</p>}
      {result?.files?.map(file => <SaveFile key={file.handle} file={file}/>)}
    </SettingsSection>}
  </PageShell>;
}
function Output({name, label, text}: {name: string; label: string; text: string}) {
  const ref = useRef<HTMLPreElement>(null);
  const follow = useRef(true);
  useEffect(() => { const node = ref.current; if (node && follow.current) node.scrollTop = node.scrollHeight; }, [text]);
  return <pre ref={ref} data-stream={name} tabIndex={0} aria-label={label} onScroll={event => { const node = event.currentTarget; follow.current = node.scrollHeight - node.scrollTop - node.clientHeight < 32; }}>{text}</pre>;
}
function SaveFile({file}: {file: NonNullable<LocalRunResult["files"]>[number]}) {
  const {t} = useTranslation("settings");
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");
  async function save() {
    if (busy) return;
    setBusy(true);
    try { const saved = await window.tofiDesktop!.localRunSaveFile!(file.handle); setNote(t(saved ? "desktop_pages.local.saved" : "desktop_pages.local.save_cancelled")); }
    catch (cause) { setNote(t("desktop_pages.local.error", {message: detail(cause)})); }
    finally { setBusy(false); }
  }
  return <SettingsCard><SettingsRow label={<code dir="auto">{file.path}</code>} description={<span role="status">{note}</span>} control={<button className="st-button" disabled={busy || !window.tofiDesktop?.localRunSaveFile} onClick={() => void save()}>{t("desktop_pages.local.save")}</button>}/></SettingsCard>;
}
