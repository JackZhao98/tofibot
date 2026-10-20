import { useCallback, useEffect, useRef, useState } from "react";
import { ApiError, request } from "./api";
import { BotAvatar } from "./BotAvatar";
import { isDesktop } from "./desktop";
import { useTranslation } from "./i18n";
import { MessageMarkdown } from "./MessageMarkdown";
import { mcpOAuthRoute, type OAuthOptions } from "./mcpOAuthRoute";
import { reconcileQuestion, type Question } from "./questionTimeline";
import { canAnswerQuestion } from "./taskIssuePresentation";
import { TakeoverDialog } from "./TakeoverDialog";
import { VMOAuthDialog, type VMOAuthSession } from "./VMOAuthDialog";
import type { Bot } from "./types";
import "./handover.css";

type CardProps = { item: Question; bot?: Bot; group?: boolean; archived?: boolean; onChanged: (resolved?: Question) => Promise<void> };
type Result = "done" | "cannot" | "connected" | "failed" | "declined";

/** One answer path for both cards: the body is a single enumerated word, never typed content. */
function useHandoverAnswer(item: Question, onChanged: CardProps["onChanged"]) {
  const { t } = useTranslation("tasks");
  const [resolved, setResolved] = useState<Question | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const sending = useRef(false);
  const mounted = useRef(true);
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const send = useCallback(async (result: Result) => {
    if (sending.current) return false;
    sending.current = true; setBusy(true); setError("");
    try {
      const response = await request<{ question: Question }>(`/api/questions/${encodeURIComponent(item.question_id)}/answer`, { method: "POST", body: JSON.stringify({ result }) });
      if (!mounted.current) return true;
      setResolved(response.question);
      try { await onChanged(response.question); } catch { if (mounted.current) setError(t("question.answered_refresh_failed")); }
      return true;
    } catch (cause) {
      if (!mounted.current) return false;
      if (cause instanceof ApiError && (cause.status === 404 || cause.status === 409) && cause.code !== "connection_not_ready") {
        setError(t("handover.state_changed"));
        try { await onChanged(); } catch { /* The next poll shows the real state. */ }
      } else setError(cause instanceof ApiError && cause.code === "connection_not_ready" ? t("handover.connection.error.not_ready") : t("handover.send_failed"));
      return false;
    } finally { sending.current = false; if (mounted.current) setBusy(false); }
  }, [item.question_id, onChanged, t]);
  return { current: reconcileQuestion(item, resolved), busy, error, setError, send };
}

function resolutionLabel(current: Question, t: (key: any) => string) {
  if (current.status === "answered") return null;
  return current.status === "expired" ? t("status.expired") : current.status === "run_done" ? t("question.resolution.run_done") : t("status.cancelled");
}

function Shell({ item, bot, group, children, active, badge }: { item: Question; bot?: Bot; group?: boolean; active: boolean; badge: string; children: React.ReactNode }) {
  const { t } = useTranslation("tasks");
  return <article className={`question-message${group ? " is-group" : " is-dm"}`} data-question-id={item.question_id} data-question-type={item.question_type} tabIndex={-1} aria-label={t("question.aria", { name: bot?.name ?? "Bot" })}>
    {group && <BotAvatar id={item.bot_id} mini />}
    <div className="question-body">{group && <div className="message-meta"><strong>{bot?.name ?? "Bot"}</strong></div>}
      <section className={`question-card ${active ? "is-pending" : "is-resolved"}`}>
        {active && <span className="question-state">{badge}</span>}
        <div className="question-prompt"><MessageMarkdown content={item.question} /></div>
        {children}
      </section>
    </div>
  </article>;
}

export function TakeoverCard({ item, bot, group, archived, onChanged }: CardProps) {
  const { t } = useTranslation("tasks");
  const { current, busy, error, send } = useHandoverAnswer(item, onChanged);
  const [open, setOpen] = useState(false);
  const active = canAnswerQuestion(current, Boolean(archived));
  // The modal belongs to the open request: when the card ends for any reason it goes away with it.
  useEffect(() => { if (current.status !== "pending") setOpen(false); }, [current.status]);
  const ended = resolutionLabel(current, t);
  const answered = current.status === "answered" ? (current.answer === "done" ? t("handover.takeover.resolved_done") : t("handover.takeover.resolved_cannot")) : null;
  return <Shell item={item} bot={bot} group={group} active={active} badge={t("handover.takeover.badge")}>
    {item.source_url && <p className="handover-site"><span>{t("handover.site")}</span><span>{item.source_url}</span></p>}
    {active ? <>
      <p className="handover-hint">{t("handover.takeover.hint")}</p>
      <div className="question-actions handover-actions">
        <button className="text-button" type="button" disabled={busy} onClick={() => void send("cannot")}>{t("handover.cannot")}</button>
        <button className="secondary-button" type="button" disabled={busy} onClick={() => void send("done")}>{t("handover.done")}</button>
        <button className="primary-button" type="button" disabled={busy} onClick={() => setOpen(true)}>{t("handover.takeover.take")}</button>
      </div>
      {open && <TakeoverDialog botId={item.bot_id} questionId={item.question_id} site={item.source_url ?? ""} prompt={item.question} busy={busy} error={error}
        onResult={result => void send(result)} onClose={() => setOpen(false)} />}
    </> : archived && current.status === "pending" ? <p className="field-note">{t("question.note.archived")}</p> : <div className="question-result" role="status"><span>{answered ?? ended}</span></div>}
    {error && <p className="error-text" role="alert">{error}</p>}
  </Shell>;
}

type Phase = "idle" | "starting" | "waiting";

/**
 * Starts the same connect flow Settings uses for this client (native app,
 * popup on https, or the shared computer on plain http) for one service.
 */
function useConnect(name: string, botId: string, onConnected: () => void, onBlocked: (note: string) => void) {
  const { t } = useTranslation("tasks");
  const [options, setOptions] = useState<OAuthOptions | null>(null);
  const [phase, setPhase] = useState<Phase>("idle");
  const [error, setError] = useState("");
  const [vm, setVM] = useState<VMOAuthSession | null>(null);
  const popup = useRef<Window | null>(null);
  const alive = useRef(true);
  const operation = useRef(0);
  const connected = useRef(onConnected); connected.current = onConnected;
  const blocked = useRef(onBlocked); blocked.current = onBlocked;
  const base = `/api/extensions/mcp/${encodeURIComponent(name)}`;
  useEffect(() => {
    alive.current = true;
    let live = true;
    void request<OAuthOptions>("/api/extensions/oauth-options").then(result => { if (live) setOptions(result); }).catch(() => { if (live) setOptions({ vm_available: false, web_callback_origin: "", desktop_redirect_uri: "" }); });
    return () => { live = false; alive.current = false; operation.current++; if (window.tofiDesktop?.cancelMCPAuthorization) void window.tofiDesktop.cancelMCPAuthorization(name).catch(() => {}); };
  }, [name]);
  const isConnected = useCallback(async () => {
    try {
      const list = await request<{ servers: { name: string; oauth?: { connected?: boolean } }[] }>("/api/extensions/mcp");
      return Boolean(list.servers?.find(server => server.name === name)?.oauth?.connected);
    } catch { return false; }
  }, [name]);
  // Connected elsewhere (Settings, another tab) while the card is open.
  useEffect(() => {
    const check = () => { if (!document.hidden) void isConnected().then(yes => { if (yes && alive.current) connected.current(); }); };
    check();
    window.addEventListener("focus", check); document.addEventListener("visibilitychange", check);
    return () => { window.removeEventListener("focus", check); document.removeEventListener("visibilitychange", check); };
  }, [isConnected]);
  // Web popup: the provider returns to Tofi; the server flips the connection, so watch it.
  useEffect(() => {
    if (phase !== "waiting" || vm || isDesktop) return;
    const timer = window.setInterval(() => { void isConnected().then(yes => { if (yes && alive.current) { popup.current = null; setPhase("idle"); connected.current(); } }); }, 2000);
    const timeout = window.setTimeout(() => { setPhase("idle"); setError(t("handover.connection.error.timeout")); }, 10 * 60 * 1000);
    return () => { window.clearInterval(timer); window.clearTimeout(timeout); };
  }, [phase, vm, isConnected, t]);
  const post = (body: unknown) => ({ method: "POST", body: JSON.stringify(body) });
  const start = async () => {
    const id = ++operation.current;
    setError(""); setPhase("starting");
    const route = mcpOAuthRoute(window.location.protocol, isDesktop, Boolean(window.tofiDesktop?.authorizeMCP), options);
    if (route.mode === "blocked") { setPhase("idle"); blocked.current(route.note); return; }
    if (route.mode === "desktop") {
      setPhase("waiting");
      try { await window.tofiDesktop!.authorizeMCP!(name); if (alive.current && id === operation.current) { setPhase("idle"); connected.current(); } }
      catch { if (alive.current && id === operation.current) { setPhase("idle"); setError(t("handover.connection.error.incomplete")); } }
      return;
    }
    if (route.mode === "vm") {
      try {
        const session = await request<VMOAuthSession>(`${base}/oauth/vm/start`, post({ bot_id: botId }));
        if (!alive.current || id !== operation.current) { void request(`${base}/oauth/vm/${encodeURIComponent(session.session_id)}/cancel`, { ...post({}), keepalive: true }).catch(() => {}); return; }
        setVM(session); setPhase("waiting");
      } catch { if (alive.current && id === operation.current) { setPhase("idle"); setError(t("handover.connection.error.start")); } }
      return;
    }
    // Opened inside the click: many browsers block a window opened after an await.
    let opened: Window | null = null;
    try {
      opened = window.open(`${base}/oauth/pending`, "_blank"); popup.current = opened; if (opened) opened.opener = null;
      const result = await request<{ authorization_url: string }>(`${base}/oauth/start`, post({}));
      if (!alive.current || id !== operation.current) { opened?.close(); return; }
      const url = new URL(result.authorization_url);
      if (!["https:", "http:"].includes(url.protocol)) throw new Error("scheme");
      setPhase("waiting"); if (opened && !opened.closed) opened.location.replace(url.href);
    } catch { opened?.close(); if (alive.current && id === operation.current) { setPhase("idle"); setError(t("handover.connection.error.start")); } }
  };
  const finishVM = (status: string) => {
    setVM(null); setPhase("idle");
    if (status === "complete") connected.current();
    else if (status !== "cancelled") setError(t("handover.connection.error.incomplete"));
  };
  return { phase, error, vm, start, finishVM, ready: options !== null, setError };
}

export function ConnectionCard({ item, bot, group, archived, onChanged }: CardProps) {
  const { t } = useTranslation("tasks");
  const { current, busy, error: sendError, send } = useHandoverAnswer(item, onChanged);
  const active = canAnswerQuestion(current, Boolean(archived));
  const service = item.target ?? "";
  const connect = useConnect(service, item.bot_id, () => { if (active) void send("connected"); }, note => { connect.setError(note); void send("failed"); });
  const ended = resolutionLabel(current, t);
  const answered = current.status === "answered" ? t(`handover.connection.resolved_${current.answer === "connected" ? "connected" : current.answer === "declined" ? "declined" : "failed"}`) : null;
  const waiting = connect.phase !== "idle";
  return <Shell item={item} bot={bot} group={group} active={active} badge={t("handover.connection.badge")}>
    <p className="handover-site"><span>{t("handover.service")}</span><span>{service}</span></p>
    {active ? <>
      <p className="handover-hint">{t("handover.connection.hint")}</p>
      <div className="question-actions handover-actions">
        <button className="text-button" type="button" disabled={busy || waiting} onClick={() => void send("declined")}>{t("handover.connection.decline")}</button>
        <button className="primary-button" type="button" disabled={busy || waiting || !connect.ready} onClick={() => void connect.start()}>{waiting ? t("handover.connection.waiting") : connect.error || sendError ? t("handover.connection.retry") : t("handover.connection.connect")}</button>
      </div>
      {connect.vm && <VMOAuthDialog session={connect.vm} serverName={service} title={service} onFinish={status => connect.finishVM(status)} />}
    </> : archived && current.status === "pending" ? <p className="field-note">{t("question.note.archived")}</p> : <div className="question-result" role="status"><span>{answered ?? ended}</span></div>}
    {(connect.error || sendError) && <p className="error-text" role="alert">{connect.error || sendError}</p>}
  </Shell>;
}

