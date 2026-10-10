import { useCallback, useEffect, useId, useRef, useState, type ReactNode } from "react";
import { api, ApiError, request } from "./api";
import type { CatHandle } from "./CatStage";
import { CATS, EmptyCat } from "./EmptyCat";
import { TofiIcon } from "./icons";
import { Disclosure } from "./InteractionSystem";
import { effortLabels } from "./ModelSettings";
import { OnboardingServices, type ServicesStatus } from "./OnboardingServices";
import { effortForModel, groupModels, modelEfforts, providerForModel, providerLabels, shortEffortLabel } from "./modelCatalog";
import { ONBOARDING_STEPS, type OnboardingStep } from "./onboardingFlow";
import { useTranslation } from "./i18n";
import type { Bot, ModelCatalog } from "./types";
import "./onboarding.css";

type Meta = { connected: boolean; queue: ServicesStatus };

export type OnboardingProps = {
  initialStep: OnboardingStep;
  modelConfigured: boolean;
  /** The account's first Bot, once there is one. */
  bot?: Bot;
  /** Records progress on the server. */
  onProgress: (update: { step?: number; skipped?: boolean; completed?: boolean }) => void;
  /** Re-read the workspace configuration after a model was connected or changed. */
  onModelChanged: () => Promise<void>;
  /** A Bot was created for the account; add it to the lists. */
  onBot: (bot: Bot) => void;
  /** Close without finishing: the chip and the composer banner take over. */
  onSkip: () => void;
  /** Setup is done: close and open the first Bot. */
  onFinish: (botId?: string) => void;
};

/** Runs `play` on a cat as soon as its live slot has mounted it (a cat off screen never mounts). */
function useCatAction(handle: { current: CatHandle | null }, action: "wave" | "happy" | "wake", delay: number, enabled = true) {
  useEffect(() => {
    if (!enabled || typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    let cancelled = false;
    let attempts = 0;
    const tick = () => {
      if (cancelled) return;
      const cat = handle.current?.get();
      if (!cat) { if (++attempts < 30) timer = window.setTimeout(tick, 100); return; }
      void (async () => { if (action === "wave" && cat.getState().idle === "asleep") await handle.current?.play("wake"); await handle.current?.play(action); })();
    };
    let timer = window.setTimeout(tick, delay);
    return () => { cancelled = true; window.clearTimeout(timer); };
  }, [handle, action, delay, enabled]);
}

export function Onboarding(props: OnboardingProps) {
  const { t } = useTranslation("chat");
  const [step, setStepState] = useState<OnboardingStep>(props.initialStep);
  const [direction, setDirection] = useState<"forward" | "back">("forward");
  const [meta, setMeta] = useState<Meta>({ connected: props.modelConfigured, queue: null });
  const sheet = useRef<HTMLElement>(null);
  const titleId = useId();
  // Stable callbacks that return the same state when nothing changed, so a child's effect cannot loop.
  const onConnected = useCallback((connected: boolean) => setMeta(current => current.connected === connected ? current : { ...current, connected }), []);
  const onQueue = useCallback((queue: Meta["queue"]) => setMeta(current => JSON.stringify(current.queue) === JSON.stringify(queue) ? current : { ...current, queue }), []);
  const go = useCallback((next: OnboardingStep) => {
    setDirection(next > step ? "forward" : "back");
    setStepState(next);
    props.onProgress({ step: next, skipped: false });
  }, [step, props]);
  useEffect(() => { if (props.initialStep === 1) props.onProgress({ step: 1 }); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, []);
  useEffect(() => { sheet.current?.querySelector<HTMLElement>("[data-autofocus]")?.focus(); }, [step]);
  // Keep Tab inside the sheet: the workspace behind it is out of reach while it is open.
  const trap = (event: React.KeyboardEvent) => {
    if (event.key !== "Tab" || !sheet.current) return;
    const items = [...sheet.current.querySelectorAll<HTMLElement>("button:not([disabled]),a[href],input:not([disabled]),select:not([disabled]),[tabindex]:not([tabindex='-1'])")].filter(node => node.offsetParent !== null);
    if (!items.length) return;
    const first = items[0], last = items[items.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  };
  const status = step === 1 ? "" : step === 2 ? t(meta.connected ? "onboarding.status_done" : "onboarding.status_required") : meta.queue?.kind === "walk" ? t("onboarding.status_app", { name: meta.queue.name, index: meta.queue.index, total: meta.queue.total }) : meta.queue?.kind === "summary" ? t("onboarding.status_done") : t("onboarding.status_optional");
  const skipLabel = step === 1 ? t("onboarding.skip") : step === 2 ? t("onboarding.not_now") : t("onboarding.skip");
  const skip = () => step === 3 ? props.onFinish(props.bot?.id) : props.onSkip();
  return (
    <div className="onb-scrim" data-onboarding="true" data-step={step}>
      <section ref={sheet} className="onb-sheet" role="dialog" aria-modal="true" aria-labelledby={titleId} onKeyDown={trap}>
        <header className="onb-head">
          <div className="onb-progress" role="progressbar" aria-valuemin={1} aria-valuemax={ONBOARDING_STEPS} aria-valuenow={step} aria-valuetext={t("onboarding.step_of", { step, total: ONBOARDING_STEPS })}>
            {[1, 2, 3].map(index => <i key={index} data-state={index < step ? "done" : index === step ? "current" : "todo"} />)}
          </div>
          <span className="onb-count">{t("onboarding.step_of", { step, total: ONBOARDING_STEPS })}{status && <> · {status}</>}</span>
          <button type="button" className="onb-skip" onClick={skip}>{skipLabel}</button>
        </header>
        <div className="onb-step" data-dir={direction} key={step}>
          {step === 1 && <Welcome titleId={titleId} onStart={() => go(2)} onSkip={props.onSkip} />}
          {step === 2 && <ModelStep titleId={titleId} {...props} onBack={() => go(1)} onNext={() => go(3)} onConnected={onConnected} />}
          {step === 3 && <OnboardingServices titleId={titleId} botId={props.bot?.id} onQueue={onQueue} onFinish={() => props.onFinish(props.bot?.id)} />}
        </div>
      </section>
    </div>
  );
}

function Welcome({ titleId, onStart, onSkip }: { titleId: string; onStart: () => void; onSkip: () => void }) {
  const { t } = useTranslation("chat");
  const left = useRef<CatHandle | null>(null), middle = useRef<CatHandle | null>(null), right = useRef<CatHandle | null>(null);
  // The three wake and wave one after another, 250ms apart; the bubbles pop with them.
  useCatAction(left, "wave", 250);
  useCatAction(middle, "wave", 500);
  useCatAction(right, "wave", 750);
  return (
    <>
      <div className="onb-body onb-welcome">
        <div className="onb-cats" aria-hidden="true">
          <span className="onb-cat-slot"><i className="onb-bubble" style={{ "--n": 0 } as React.CSSProperties}>{t("onboarding.bubble_hi")}</i><EmptyCat look={CATS.miso} pose="awake" name="onboarding-miso" className="onb-cat" ref={left} /></span>
          <span className="onb-cat-slot"><i className="onb-bubble" style={{ "--n": 1 } as React.CSSProperties}>{t("onboarding.bubble_welcome")}</i><EmptyCat look={CATS.yuzu} pose="awake" name="onboarding-yuzu" className="onb-cat" ref={middle} /></span>
          <span className="onb-cat-slot"><i className="onb-bubble" style={{ "--n": 2 } as React.CSSProperties}>{t("onboarding.bubble_hello")}</i><EmptyCat look={CATS.nori} pose="awake" name="onboarding-nori" className="onb-cat" ref={right} /></span>
        </div>
        <h2 id={titleId} className="onb-title">{t("onboarding.welcome_title")}</h2>
        <p className="onb-lede">{t("onboarding.welcome_body")}</p>
      </div>
      <footer className="onb-foot onb-foot-welcome">
        <button type="button" className="onb-quiet" onClick={onSkip}>{t("onboarding.skip_for_now")}</button>
        <button type="button" className="primary-button onb-primary" data-autofocus onClick={onStart}>{t("onboarding.welcome_start")} <TofiIcon name="arrow-right" size={16} /></button>
      </footer>
    </>
  );
}

/** A short radiogroup: the thinking levels of a model, or the two key providers. */
function OnbSegmented<T extends string>({ value, options, onChange, label, disabled = false }: { value: T; options: readonly { value: T; label: ReactNode }[]; onChange: (value: T) => void; label: string; disabled?: boolean }) {
  return (
    <div className="onb-seg" role="radiogroup" aria-label={label}>
      {options.map(option => <button key={option.value} type="button" role="radio" aria-checked={value === option.value} disabled={disabled} onClick={() => onChange(option.value)}>{option.label}</button>)}
    </div>
  );
}

type KeyProvider = "openai" | "anthropic";
const keyHelp: Record<KeyProvider, { prefix: string; keys: string; billing: string }> = {
  openai: { prefix: "sk-", keys: "https://platform.openai.com/api-keys", billing: "https://platform.openai.com/settings/organization/billing/overview" },
  anthropic: { prefix: "sk-ant-", keys: "https://console.anthropic.com/settings/keys", billing: "https://console.anthropic.com/settings/billing" },
};
const keyLabels: Record<KeyProvider, string> = { openai: "OpenAI", anthropic: "Anthropic" };
type Phase = "idle" | "waiting" | "checking" | "error" | "connected";
type Via = "chatgpt" | KeyProvider;

function ModelStep({ titleId, modelConfigured, bot, onModelChanged, onBot, onBack, onNext, onConnected }: OnboardingProps & { titleId: string; onBack: () => void; onNext: () => void; onConnected: (connected: boolean) => void }) {
  const { t } = useTranslation(["chat", "settings"]);
  const [choice, setChoice] = useState<"chatgpt" | "key">("chatgpt");
  const [phase, setPhase] = useState<Phase>(modelConfigured ? "connected" : "idle");
  const [via, setVia] = useState<Via | null>(null);
  const [session, setSession] = useState<{ url: string; code: string } | null>(null);
  const [copied, setCopied] = useState(false);
  const [provider, setProvider] = useState<KeyProvider>("openai");
  const [key, setKey] = useState("");
  const [workspace, setWorkspace] = useState("");
  const [error, setError] = useState<{ text: string; billing?: boolean } | null>(null);
  const [slow, setSlow] = useState(false);
  const [defaults, setDefaults] = useState<{ model: string; reasoning_effort: string } | null>(null);
  const [catalog, setCatalog] = useState<ModelCatalog | null>(null);
  const [botNote, setBotNote] = useState(false);
  const [saveError, setSaveError] = useState(false);
  const run = useRef(0);
  const alive = useRef(true);
  const cat = useRef<CatHandle | null>(null);
  const keyInput = useRef<HTMLInputElement>(null);
  useEffect(() => { alive.current = true; return () => { alive.current = false; run.current++; }; }, []);
  useEffect(() => { onConnected(phase === "connected"); }, [phase, onConnected]);
  useEffect(() => { if (phase === "waiting" || phase === "checking") { setSlow(false); const timer = window.setTimeout(() => setSlow(true), 8000); return () => window.clearTimeout(timer); } }, [phase]);

  const loadDefaults = useCallback(async () => {
    const [settings, models] = await Promise.all([
      request<{ model: string; reasoning_effort: string }>("/api/model-settings").catch(() => null),
      api.models().catch(() => null),
    ]);
    if (!alive.current) return;
    if (models) setCatalog(models);
    let current = settings;
    // A default that belongs to a provider that is not connected cannot run: pick the first model the connected one offers.
    if (models?.models.length && !models.models.some(item => item.id === current?.model)) {
      const first = models.models[0];
      const next = { model: first.id, reasoning_effort: first.default_reasoning ?? "" };
      try { await request("/api/model-settings", { method: "PUT", body: JSON.stringify(next) }); current = next; } catch { /* the Advanced picker stays available */ }
    }
    if (alive.current && current) setDefaults(current);
  }, []);

  const ensureFirstBot = useCallback(async () => {
    if (bot) return;
    try { const result = await api.firstBot(); if (alive.current) onBot(result.bot); }
    catch { if (alive.current) setBotNote(true); }
  }, [bot, onBot]);

  // An account that already has a model lands here "connected": show what it has, and make sure the Bot exists.
  useEffect(() => { if (modelConfigured) { void loadDefaults(); void ensureFirstBot(); } /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, []);

  const finishConnect = useCallback(async (how: Via) => {
    await onModelChanged();
    await loadDefaults();
    await ensureFirstBot();
    if (!alive.current) return;
    setVia(how); setPhase("connected");
  }, [onModelChanged, loadDefaults, ensureFirstBot]);

  async function startChatGPT() {
    const token = ++run.current;
    setPhase("waiting"); setError(null); setSession(null); setCopied(false);
    // Opened inside the click so the browser allows it; pointed at the sign-in page once the server has one.
    const popup = window.open("about:blank", "_blank");
    try {
      const result = await api.codexConnect();
      if (run.current !== token) { popup?.close(); return; }
      setSession({ url: result.verification_url, code: result.user_code });
      if (popup && !popup.closed) { popup.opener = null; popup.location.replace(result.verification_url); }
      const delay = Math.max(1000, result.interval * 1000);
      while (Date.now() < result.expires_at) {
        await new Promise(resolve => window.setTimeout(resolve, delay));
        if (run.current !== token) return;
        const status = await api.codexPoll(result.session_id);
        if (run.current !== token) return;
        if (status.connected) { await finishConnect("chatgpt"); return; }
        if (!status.pending) break;
      }
      if (run.current === token) { setPhase("error"); setError({ text: t("onboarding.sign_in_expired") }); }
    } catch {
      popup?.close();
      if (run.current === token) { setPhase("error"); setError({ text: t("onboarding.sign_in_expired") }); }
    }
  }
  function cancelChatGPT() { run.current++; setPhase("idle"); setSession(null); setError(null); }
  async function copyCode() {
    try { await navigator.clipboard.writeText(session?.code ?? ""); setCopied(true); window.setTimeout(() => alive.current && setCopied(false), 1800); } catch { /* the code stays on screen to copy by hand */ }
  }

  async function checkKey() {
    const value = key.trim();
    if (!value || phase === "checking") return;
    const token = ++run.current;
    setPhase("checking"); setError(null);
    try {
      await api.setProviderKey(provider, value, provider === "anthropic" ? workspace.trim() || undefined : undefined);
      if (run.current !== token) return;
      const models = await api.models().catch(() => null);
      if (run.current !== token) return;
      if (models && !models.models.length) { setPhase("error"); setError({ text: t("onboarding.key_no_models"), billing: true }); return; }
      setKey("");
      await finishConnect(provider);
    } catch (cause) {
      if (run.current !== token) return;
      const label = keyLabels[provider];
      setPhase("error");
      setError({ text: cause instanceof ApiError && cause.code === "invalid_workspace" ? t("settings:providers.invalid_workspace") : cause instanceof ApiError && cause.code === "invalid_key" ? t("onboarding.key_invalid", { prefix: keyHelp[provider].prefix }) : cause instanceof ApiError && cause.status >= 500 ? t("onboarding.key_unreachable", { provider: label }) : t("onboarding.key_failed") });
      keyInput.current?.select();
    }
  }

  async function saveDefaults(model: string, reasoning_effort: string) {
    const previous = defaults;
    setDefaults({ model, reasoning_effort }); setSaveError(false);
    try { await request("/api/model-settings", { method: "PUT", body: JSON.stringify({ model, reasoning_effort }) }); await onModelChanged(); }
    catch { setDefaults(previous); setSaveError(true); }
  }

  useCatAction(cat, "happy", 450, phase === "connected");

  const connected = phase === "connected";
  const busy = phase === "waiting" || phase === "checking";
  const labels = effortLabels();
  const selectedModel = catalog?.models.find(item => item.id === defaults?.model);
  const efforts = modelEfforts(selectedModel);
  const providerName = via === "chatgpt" ? "ChatGPT" : via ? keyLabels[via] : defaults?.model ? providerLabels[providerForModel(defaults.model)] : "";
  const continueLabel = choice === "chatgpt" ? t("onboarding.model_continue_chatgpt") : t("onboarding.key_connect");

  let body: ReactNode;
  if (connected) {
    body = (
      <>
        <div className="onb-connected" role="status">
          <EmptyCat look={CATS.yuzu} pose="awake" name="onboarding-connected" className="onb-connected-cat" ref={cat} />
          <div>
            <strong><TofiIcon name="check-circle" size={18} /> {providerName || t("onboarding.model_generic")}</strong>
            <p>{t("onboarding.connected_ready")}</p>
            {defaults?.model && <p className="onb-chips"><code>{t("onboarding.chip_default", { model: selectedModel?.name ?? defaults.model })}</code>{efforts.length > 0 && defaults.reasoning_effort && <code>{t("onboarding.chip_thinking", { effort: shortEffortLabel(defaults.reasoning_effort, labels) })}</code>}</p>}
          </div>
        </div>
        {botNote && <p className="onb-note" role="status">{t("onboarding.first_bot_failed")}</p>}
        <div className="onb-advanced">
          <Disclosure title={t("onboarding.advanced")}>
            {defaults && catalog && (
              <div className="onb-adv-rows">
                <div className="onb-adv-row">
                  <div><label htmlFor="model-default-select">{t("onboarding.adv_model")}</label><p>{t("onboarding.adv_model_hint")}</p></div>
                  <select id="model-default-select" value={defaults.model} onChange={event => { const next = catalog.models.find(item => item.id === event.target.value); void saveDefaults(event.target.value, effortForModel(next, defaults.reasoning_effort)); }}>
                    {!selectedModel && <option value={defaults.model}>{defaults.model}</option>}
                    {groupModels(catalog.models).map(group => <optgroup key={group.provider} label={group.label}>{group.models.map(item => <option key={item.id} value={item.id}>{item.label}</option>)}</optgroup>)}
                  </select>
                </div>
                {efforts.length > 0 && (
                  <div className="onb-adv-row">
                    <div><label htmlFor="model-effort-select">{t("onboarding.adv_thinking")}</label><p>{t("onboarding.adv_thinking_hint")}</p></div>
                    {efforts.length <= 4
                      ? <OnbSegmented label={t("onboarding.adv_thinking")} value={defaults.reasoning_effort} options={efforts.map(value => ({ value, label: shortEffortLabel(value, labels) }))} onChange={value => void saveDefaults(defaults.model, value)} />
                      : <select id="model-effort-select" value={efforts.includes(defaults.reasoning_effort) ? defaults.reasoning_effort : ""} onChange={event => void saveDefaults(defaults.model, event.target.value)}>
                        {!efforts.includes(defaults.reasoning_effort) && <option value="">{t("settings:model.model_default")}</option>}
                        {efforts.map(value => <option key={value} value={value}>{labels[value] ?? value}</option>)}
                      </select>}
                  </div>
                )}
              </div>
            )}
            {saveError && <p className="onb-error" role="alert">{t("onboarding.defaults_failed")}</p>}
          </Disclosure>
        </div>
      </>
    );
  } else {
    body = (
      <>
        <p className="onb-lede onb-lede-left">{t("onboarding.model_body")}</p>
        <div className="onb-choices" role="radiogroup" aria-label={t("onboarding.model_title")}>
          <button type="button" role="radio" aria-checked={choice === "chatgpt"} className="onb-choice" disabled={busy} onClick={() => { setChoice("chatgpt"); setError(null); }}>
            <span className="onb-choice-badge">{t("onboarding.easiest")}</span>
            <span className="onb-choice-icon"><TofiIcon name="user" size={18} /></span>
            <strong>{t("onboarding.chatgpt_title")}</strong>
            <small>{t("onboarding.chatgpt_body")}</small>
          </button>
          <button type="button" role="radio" aria-checked={choice === "key"} className="onb-choice" disabled={busy} onClick={() => { setChoice("key"); setError(null); }}>
            <span className="onb-choice-icon"><TofiIcon name="key" size={18} /></span>
            <strong>{t("onboarding.key_title")}</strong>
            <small>{t("onboarding.key_body")}</small>
          </button>
        </div>
        {choice === "chatgpt" && (phase === "waiting" || (phase === "error" && !key)) && (
          <div className="onb-panel" aria-live="polite">
            {phase === "waiting" ? <>
              <p className="onb-waiting"><i aria-hidden="true" />{t("onboarding.wait_title")}</p>
              <p className="onb-small">{t("onboarding.wait_body")}</p>
              {session && <>
                <span className="onb-code-label">{t("onboarding.code_label")}</span>
                <code className="onb-code">{session.code}</code>
                <div className="onb-code-actions">
                  <button type="button" className="secondary-button" onClick={() => void copyCode()}><TofiIcon name="copy" size={15} /> {copied ? t("onboarding.copied") : t("onboarding.copy_code")}</button>
                  <a className="onb-link" href={session.url} target="_blank" rel="noopener noreferrer">{t("onboarding.open_sign_in")}</a>
                </div>
              </>}
              {slow && <p className="onb-small">{t("onboarding.taking_longer")}</p>}
            </> : error && <p className="onb-error" role="alert"><TofiIcon name="error" size={15} /> {error.text}</p>}
          </div>
        )}
        {choice === "key" && (
          <div className="onb-panel onb-keyform">
            <OnbSegmented label={t("onboarding.key_provider")} value={provider} disabled={busy} options={[{ value: "openai", label: "OpenAI" }, { value: "anthropic", label: "Anthropic" }]} onChange={value => { setProvider(value); setError(null); setPhase("idle"); }} />
            <div className="onb-keyhead"><label htmlFor="onb-key">{t("onboarding.key_field", { provider: keyLabels[provider] })}</label><a className="onb-link" href={keyHelp[provider].keys} target="_blank" rel="noopener noreferrer">{t("onboarding.key_where")}</a></div>
            <form className="onb-keyrow" onSubmit={event => { event.preventDefault(); void checkKey(); }}>
              <input id="onb-key" ref={keyInput} type="password" name={`${provider}-api-key`} autoComplete="off" spellCheck={false} autoCapitalize="off" autoCorrect="off" placeholder={provider === "openai" ? "sk-…" : "sk-ant-…"} value={key} disabled={busy} aria-invalid={phase === "error"} aria-describedby={phase === "error" ? "onb-key-error" : undefined} onChange={event => { setKey(event.target.value); if (phase === "error") { setPhase("idle"); setError(null); } }} data-autofocus />
              <button type="submit" className={phase === "error" ? "primary-button" : "secondary-button"} disabled={busy || !key.trim() || provider === "anthropic" && !workspace.trim()}>{phase === "checking" ? t("onboarding.key_checking_short") : phase === "error" ? t("onboarding.key_try_again") : t("onboarding.key_connect")}</button>
            </form>
            {provider === "anthropic" && <div className="onb-keyhead onb-workspace"><label htmlFor="onb-workspace">{t("settings:providers.workspace_label")}</label><input id="onb-workspace" type="text" autoComplete="off" spellCheck={false} placeholder="wrkspc_…" value={workspace} disabled={busy} onChange={event => { setWorkspace(event.target.value); setError(null); }} /></div>}
            {phase === "checking" && <p className="onb-waiting" role="status"><i aria-hidden="true" />{slow ? t("onboarding.taking_longer") : t("onboarding.key_checking")}</p>}
            {phase === "error" && error && <p id="onb-key-error" className="onb-error" role="alert"><TofiIcon name="error" size={15} /> {error.text}{error.billing && <> <a className="onb-link" href={keyHelp[provider].billing} target="_blank" rel="noopener noreferrer">{t("onboarding.open_billing")}</a></>}</p>}
          </div>
        )}
      </>
    );
  }

  let foot: ReactNode;
  if (connected) foot = <><span /><button type="button" className="primary-button onb-primary" data-autofocus onClick={onNext}>{t("onboarding.next")} <TofiIcon name="arrow-right" size={16} /></button></>;
  else if (phase === "waiting" && choice === "chatgpt") foot = <><button type="button" className="onb-quiet" onClick={cancelChatGPT}>{t("onboarding.cancel")}</button><button type="button" className="primary-button onb-primary" disabled>{t("onboarding.waiting")}</button></>;
  else if (choice === "chatgpt") foot = <><span className="onb-lock"><TofiIcon name="lock" size={14} /> {t("onboarding.private_note")}</span><button type="button" className="primary-button onb-primary" data-autofocus onClick={() => void startChatGPT()}>{phase === "error" ? t("onboarding.sign_in_again") : continueLabel}</button></>;
  else foot = <><button type="button" className="onb-quiet" disabled={busy} onClick={onBack}>{t("onboarding.back")}</button><button type="button" className="primary-button onb-primary" disabled>{t("onboarding.next")}</button></>;

  return (
    <>
      <div className="onb-body">
        <h2 id={titleId} className="onb-title">{connected ? t("onboarding.connected_title") : t("onboarding.model_title")}</h2>
        {body}
      </div>
      <footer className="onb-foot">{foot}</footer>
    </>
  );
}
