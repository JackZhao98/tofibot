import { lazy, Suspense, useCallback, useEffect, useId, useRef, useState, type ReactNode } from "react";
import { api, ApiError, request } from "./api";
import { Badge } from "./ds/Badge/Badge";
import { Banner } from "./ds/Banner/Banner";
import { Button } from "./ds/Button/Button";
import { Card } from "./ds/Card/Card";
import { Field } from "./ds/Field/Field";
import { Icon } from "./ds/Icon/Icon";
import { Segmented } from "./ds/Segmented/Segmented";
import { SettingsRow } from "./ds/SettingsRow/SettingsRow";
import { Tag } from "./ds/Tag/Tag";
import { TextInput } from "./ds/TextInput/TextInput";
import { Disclosure } from "./InteractionPrimitives";
import { effortLabels } from "./ModelSettings";
import { OnboardingServices, type ServicesStatus } from "./OnboardingServices";
import { effortForModel, groupModels, modelEfforts, providerForModel, providerLabels, shortEffortLabel } from "./modelCatalog";
import { ONBOARDING_STEPS, type OnboardingStep } from "./onboardingFlow";
import { useTranslation } from "./i18n";
import type { Bot } from "./types";
import { loadModelCatalog, useModelCatalog } from "./useModelCatalog";
import "./onboarding.css";
import { ModalDialog } from "./overlays/ModalDialog";
import { usePolicy } from "./capabilityPolicy";
import { Select } from "./Select";
import { Panel, Waiting } from "./onboardingParts";

const OnboardingServer = lazy(() => import("./OnboardingServer"));

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
  const policy = usePolicy();
  const skipModel = policy.provided("ai_models") && props.modelConfigured;
  const go = useCallback((requested: OnboardingStep) => {
    // Models are provided by the organization and available: the model step connects nothing and passes itself on (after it makes sure the first Bot exists), so going back from the last step skips it.
    const next: OnboardingStep = skipModel && requested === 2 && step > 2 ? 1 : requested;
    setDirection(next > step ? "forward" : "back");
    setStepState(next);
    props.onProgress({ step: next, skipped: false });
  }, [step, props, skipModel]);
  useEffect(() => { if (props.initialStep === 1) props.onProgress({ step: 1 }); /* eslint-disable-next-line react-hooks/exhaustive-deps */ }, []);
  useEffect(() => { sheet.current?.querySelector<HTMLElement>("[data-autofocus]")?.focus(); }, [step]);
  const status = step === 1 ? "" : step === 2 ? t(meta.connected ? "onboarding.status_done" : "onboarding.status_required") : meta.queue?.kind === "walk" ? t("onboarding.status_app", { name: meta.queue.name, index: meta.queue.index, total: meta.queue.total }) : meta.queue?.kind === "summary" ? t("onboarding.status_done") : t("onboarding.status_optional");
  const skipLabel = step === 1 ? t("onboarding.skip") : step === 2 ? t("onboarding.not_now") : t("onboarding.skip");
  const skip = () => step === 3 ? props.onFinish(props.bot?.id) : props.onSkip();
  return (
    // Radix Dialog (ui/src/overlays/ModalDialog.tsx) keeps Tab inside and hides the workspace from screen readers. The
    // sheet ends only through its own buttons: neither Escape nor a press on the scrim closes it.
    <ModalDialog onClose={noop} closeOnEscape={false} closeOnOutside={false} overlayClassName="onb-scrim" overlayProps={{ "data-onboarding": "true", "data-step": step }}>
      <section ref={sheet} className="onb-sheet" role="dialog" aria-labelledby={titleId}>
        <header className="onb-head">
          <div className="onb-progress" role="progressbar" aria-valuemin={1} aria-valuemax={ONBOARDING_STEPS} aria-valuenow={step} aria-valuetext={t("onboarding.step_of", { step, total: ONBOARDING_STEPS })}>
            {[1, 2, 3].map(index => <i key={index} data-state={index < step ? "done" : index === step ? "current" : "todo"} />)}
          </div>
          <span className="onb-count">{t("onboarding.step_of", { step, total: ONBOARDING_STEPS })}{status && <> · {status}</>}</span>
          <Button variant="ghost" size="sm" className="onb-skip" onClick={skip}>{skipLabel}</Button>
        </header>
        <div className="onb-step" data-dir={direction} key={step}>
          {step === 1 && <Welcome titleId={titleId} onStart={() => go(2)} onSkip={props.onSkip} />}
          {step === 2 && <ModelStep titleId={titleId} {...props} onBack={() => go(1)} onNext={() => go(3)} onConnected={onConnected} />}
          {step === 3 && <OnboardingServices titleId={titleId} botId={props.bot?.id} onQueue={onQueue} onFinish={() => props.onFinish(props.bot?.id)} />}
        </div>
      </section>
    </ModalDialog>
  );
}

const noop = () => {};

function Welcome({ titleId, onStart, onSkip }: { titleId: string; onStart: () => void; onSkip: () => void }) {
  const { t } = useTranslation("chat");
  return (
    <>
      <div className="onb-body onb-welcome">
        <Card variant="block" className="onb-art" aria-hidden="true"><img src="/brand/tofi-logo-calico-awake.svg?v=awake-calico-3" alt="" width="96" height="96" decoding="async" /></Card>
        <h2 id={titleId} className="onb-title">{t("onboarding.welcome_title")}</h2>
        <p className="onb-lede">{t("onboarding.welcome_body")}</p>
      </div>
      <footer className="onb-foot onb-foot-welcome">
        <Button variant="ghost" className="onb-quiet" onClick={onSkip}>{t("onboarding.skip_for_now")}</Button>
        <Button variant="primary" className="onb-primary" type="button" iconAfter="arrow-right" data-autofocus onClick={onStart}>{t("onboarding.welcome_start")}</Button>
      </footer>
    </>
  );
}

type KeyProvider = "openai" | "anthropic";
const keyHelp: Record<KeyProvider, { prefix: string; keys: string; billing: string }> = {
  openai: { prefix: "sk-", keys: "https://platform.openai.com/api-keys", billing: "https://platform.openai.com/settings/organization/billing/overview" },
  anthropic: { prefix: "sk-ant-", keys: "https://console.anthropic.com/settings/keys", billing: "https://console.anthropic.com/settings/billing" },
};
const keyLabels: Record<KeyProvider, string> = { openai: "OpenAI", anthropic: "Anthropic" };
type Phase = "idle" | "waiting" | "checking" | "error" | "connected";
type Via = "chatgpt" | "server" | KeyProvider;

function ModelStep({ titleId, modelConfigured, bot, onModelChanged, onBot, onBack, onNext, onConnected }: OnboardingProps & { titleId: string; onBack: () => void; onNext: () => void; onConnected: (connected: boolean) => void }) {
  const { t } = useTranslation(["chat", "settings"]);
  const [choice, setChoice] = useState<"chatgpt" | "key" | "server">("chatgpt");
  const [serverBusy, setServerBusy] = useState(false);
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
  const { catalog } = useModelCatalog();
  const [botNote, setBotNote] = useState(false);
  const [saveError, setSaveError] = useState(false);
  const run = useRef(0);
  const alive = useRef(true);
  const provided = usePolicy().provided("ai_models");
  const [checking, setChecking] = useState(false);
  const keyInput = useRef<HTMLInputElement>(null);
  useEffect(() => { alive.current = true; return () => { alive.current = false; run.current++; }; }, []);
  useEffect(() => { onConnected(phase === "connected"); }, [phase, onConnected]);
  useEffect(() => { if (phase === "waiting" || phase === "checking") { setSlow(false); const timer = window.setTimeout(() => setSlow(true), 8000); return () => window.clearTimeout(timer); } }, [phase]);

  const loadDefaults = useCallback(async () => {
    const [settings, models] = await Promise.all([
      request<{ model: string; reasoning_effort: string }>("/api/model-settings").catch(() => null),
      loadModelCatalog().catch(() => null),
    ]);
    if (!alive.current) return;
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

  // Models provided by the organization and available: make sure the first Bot exists, then go on; there is nothing to connect.
  useEffect(() => {
    if (!provided || !modelConfigured) return;
    void ensureFirstBot().then(() => { if (alive.current) onNext(); });
    /* eslint-disable-next-line react-hooks/exhaustive-deps */
  }, [provided, modelConfigured]);

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
      let failures = 0;
      while (Date.now() < result.expires_at) {
        await new Promise(resolve => window.setTimeout(resolve, delay));
        if (run.current !== token) return;
        // One dropped poll is not a cancelled sign-in: give up only after three in a row.
        let status: Awaited<ReturnType<typeof api.codexPoll>>;
        try { status = await api.codexPoll(result.session_id); failures = 0; }
        catch (cause) {
          if (++failures < 3) continue;
          throw cause;
        }
        if (run.current !== token) return;
        if (status.connected) { await finishConnect("chatgpt"); return; }
        if (!status.pending) break;
      }
      if (run.current === token) { setPhase("error"); setError({ text: t("onboarding.sign_in_expired") }); }
    } catch (cause) {
      popup?.close();
      // The server could not start or follow the sign-in with OpenAI: that is not the person cancelling.
      const unreachable = cause instanceof ApiError && (cause.code === "codex_connect" || cause.status >= 500) || cause instanceof TypeError;
      if (run.current === token) { setPhase("error"); setError({ text: unreachable ? t("onboarding.codex_unreachable") : t("onboarding.sign_in_expired") }); }
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
      const models = await loadModelCatalog().catch(() => null);
      if (run.current !== token) return;
      if (models && !models.models.length) { setPhase("error"); setError({ text: t("onboarding.key_no_models"), billing: true }); return; }
      setKey("");
      await finishConnect(provider);
    } catch (cause) {
      if (run.current !== token) return;
      const label = keyLabels[provider];
      setPhase("error");
      const code = cause instanceof ApiError ? cause.code : undefined;
      // The server names why a key cannot be used; each reason gets its own words, and the two billing reasons link to billing.
      setError(code === "no_models" ? { text: t("onboarding.key_no_models"), billing: true }
        : code === "no_quota" ? { text: t("onboarding.key_no_quota", { provider: label }), billing: true }
        : { text: code === "invalid_workspace" ? t("settings:providers.invalid_workspace") : code === "invalid_key" ? t("onboarding.key_invalid", { prefix: keyHelp[provider].prefix }) : cause instanceof ApiError && cause.status >= 500 || code === "provider_unreachable" ? t("onboarding.key_unreachable", { provider: label }) : t("onboarding.key_failed") });
      keyInput.current?.select();
    }
  }

  async function saveDefaults(model: string, reasoning_effort: string) {
    const previous = defaults;
    setDefaults({ model, reasoning_effort }); setSaveError(false);
    try { await request("/api/model-settings", { method: "PUT", body: JSON.stringify({ model, reasoning_effort }) }); await onModelChanged(); }
    catch { setDefaults(previous); setSaveError(true); }
  }

  const connected = phase === "connected";
  const busy = phase === "waiting" || phase === "checking" || serverBusy;
  const labels = effortLabels();
  const selectedModel = catalog?.models.find(item => item.id === defaults?.model);
  const efforts = modelEfforts(selectedModel);
  const providerName = via === "chatgpt" ? "ChatGPT" : via === "server" ? providerLabels.compatible : via ? keyLabels[via] : defaults?.model ? providerLabels[providerForModel(defaults.model)] : "";
  const continueLabel = choice === "chatgpt" ? t("onboarding.model_continue_chatgpt") : t("onboarding.key_connect");

  let body: ReactNode;
  if (provided) {
    body = modelConfigured
      ? <p className="onb-lede onb-lede-left" role="status">{t("onboarding.provided_body")}</p>
      : <Panel className="onb-waiting-admin" data-state="org_resource_missing" role="status"><p className="onb-waiting"><Icon name="lock" size={15} /> {t("onboarding.waiting_title")}</p><p className="onb-small">{t("onboarding.waiting_body")}</p></Panel>;
  } else if (connected) {
    body = (
      <>
        <div role="status"><Banner tone="done" className="onb-connected" title={providerName || t("onboarding.model_generic")}>{t("onboarding.connected_ready")}</Banner></div>
        {defaults?.model && <div className="onb-chips"><Tag>{t("onboarding.chip_default", { model: selectedModel?.name ?? defaults.model })}</Tag>{efforts.length > 0 && defaults.reasoning_effort && <Tag>{t("onboarding.chip_thinking", { effort: shortEffortLabel(defaults.reasoning_effort, labels) })}</Tag>}</div>}
        {botNote && <div role="status"><Banner tone="warning" className="onb-note">{t("onboarding.first_bot_failed")}</Banner></div>}
        <Card variant="quiet" pad={false} className="onb-advanced">
          <>
            <Disclosure title={t("onboarding.advanced")}>
              {defaults && catalog && (
                <div className="onb-adv-rows">
                  <SettingsRow label={t("onboarding.adv_model")} hint={t("onboarding.adv_model_hint")} htmlFor="model-default-select" fill>
                    <Select id="model-default-select" value={defaults.model} onValueChange={value => { const next = catalog.models.find(item => item.id === value); void saveDefaults(value, effortForModel(next, defaults.reasoning_effort)); }}>
                      {!selectedModel && <option value={defaults.model}>{defaults.model}</option>}
                      {groupModels(catalog.models).map(group => <optgroup key={group.provider} label={group.label}>{group.models.map(item => <option key={item.id} value={item.id}>{item.label}</option>)}</optgroup>)}
                    </Select>
                  </SettingsRow>
                  {efforts.length > 0 && (
                    <SettingsRow label={t("onboarding.adv_thinking")} hint={t("onboarding.adv_thinking_hint")} htmlFor={efforts.length <= 4 ? undefined : "model-effort-select"} fill={efforts.length > 4}>
                      {efforts.length <= 4
                        ? <Segmented className="onb-seg" label={t("onboarding.adv_thinking")} value={defaults.reasoning_effort} options={efforts.map(value => ({ value, label: shortEffortLabel(value, labels) }))} onChange={value => void saveDefaults(defaults.model, value)} />
                        : <Select id="model-effort-select" value={efforts.includes(defaults.reasoning_effort) ? defaults.reasoning_effort : ""} onValueChange={value => void saveDefaults(defaults.model, value)}>
                          {!efforts.includes(defaults.reasoning_effort) && <option value="">{t("settings:model.model_default")}</option>}
                          {efforts.map(value => <option key={value} value={value}>{labels[value] ?? value}</option>)}
                        </Select>}
                    </SettingsRow>
                  )}
                </div>
              )}
              {saveError && <Banner tone="error" className="onb-error">{t("onboarding.defaults_failed")}</Banner>}
            </Disclosure>
          </>
        </Card>
      </>
    );
  } else {
    body = (
      <>
        <p className="onb-lede onb-lede-left">{t("onboarding.model_body")}</p>
        <Segmented className="onb-choices" label={t("onboarding.model_title")} value={choice} disabled={busy}
          options={[{ value: "chatgpt", label: t("onboarding.chatgpt_title")}, { value: "key", label: t("onboarding.key_title")}, { value: "server", label: t("onboarding.server_title")}]}
          onChange={value => { setChoice(value); setError(null); }} />
        <p className="onb-small onb-choice-body">{choice === "chatgpt" && <><Badge tone="info" icon={null}>{t("onboarding.easiest")}</Badge>{" "}</>}{choice === "chatgpt" ? t("onboarding.chatgpt_body") : choice === "server" ? t("onboarding.server_body") : t("onboarding.key_body")}</p>
        <p className="onb-small onb-cost" data-cost={choice === "chatgpt" ? "plan" : choice === "server" ? "endpoint" : "api"}>{choice === "chatgpt" ? t("settings:providers.cost.plan") : choice === "server" ? t("settings:providers.cost.endpoint") : t("settings:providers.cost.api_setup")}</p>
        {choice === "chatgpt" && phase === "waiting" && (
          <Panel aria-live="polite">
            <Waiting>{t("onboarding.wait_title")}</Waiting>
            <p className="onb-small">{t("onboarding.wait_body")}</p>
            {session && <>
              <span className="onb-code-label">{t("onboarding.code_label")}</span>
              <code className="onb-code">{session.code}</code>
              <div className="onb-code-actions">
                <Button variant="secondary" icon="copy" onClick={() => void copyCode()}>{copied ? t("onboarding.copied") : t("onboarding.copy_code")}</Button>
                <a className="onb-link" href={session.url} target="_blank" rel="noopener noreferrer">{t("onboarding.open_sign_in")}</a>
              </div>
            </>}
            {slow && <p className="onb-small">{t("onboarding.taking_longer")}</p>}
          </Panel>
        )}
        {choice === "chatgpt" && phase === "error" && !key && error && <Banner tone="error" className="onb-error">{error.text}</Banner>}
        {choice === "server" && <Suspense fallback={<Panel aria-busy="true"><span /></Panel>}><OnboardingServer formId="onb-server-form" onBusy={setServerBusy} onConnected={() => finishConnect("server")} /></Suspense>}
        {choice === "key" && (
          <Panel className="onb-keyform">
            <Segmented className="onb-seg" label={t("onboarding.key_provider")} value={provider} disabled={busy} options={[{ value: "openai", label: "OpenAI" }, { value: "anthropic", label: "Anthropic" }]} onChange={value => { setProvider(value); setError(null); setPhase("idle"); }} />
            <form id="onb-key-form" onSubmit={event => { event.preventDefault(); void checkKey(); }}>
              <Field id="onb-key" label={t("onboarding.key_field", { provider: keyLabels[provider] })}
                error={phase === "error" && error ? <span role="alert">{error.text}{error.billing && <> <a className="onb-link" href={keyHelp[provider].billing} target="_blank" rel="noopener noreferrer">{t("onboarding.open_billing")}</a></>}</span> : undefined}>
                <TextInput mono ref={keyInput} type="password" name={`${provider}-api-key`} autoComplete="off" spellCheck={false} autoCapitalize="off" autoCorrect="off" placeholder={provider === "openai" ? "sk-…" : "sk-ant-…"} value={key} disabled={busy} onChange={event => { setKey(event.target.value); if (phase === "error") { setPhase("idle"); setError(null); } }} data-autofocus />
              </Field>
            </form>
            <a className="onb-link" href={keyHelp[provider].keys} target="_blank" rel="noopener noreferrer">{t("onboarding.key_where")}</a>
            {provider === "anthropic" && <Field id="onb-workspace" label={t("settings:providers.workspace_label")} hint={t("settings:providers.workspace_note")}><TextInput mono form="onb-key-form" type="text" autoComplete="off" spellCheck={false} placeholder="wrkspc_…" value={workspace} disabled={busy} onChange={event => { setWorkspace(event.target.value); setError(null); }} /></Field>}
            {phase === "checking" && <Waiting role="status">{slow ? t("onboarding.taking_longer") : t("onboarding.key_checking")}</Waiting>}
          </Panel>
        )}
      </>
    );
  }

  let foot: ReactNode;
  if (provided) foot = <><Button variant="ghost" className="onb-quiet" onClick={onBack}>{t("onboarding.back")}</Button>{!modelConfigured && <Button variant="primary" className="onb-primary" type="button" data-autofocus disabled={checking} onClick={() => { setChecking(true); void onModelChanged().finally(() => { if (alive.current) setChecking(false); }); }}>{checking ? t("onboarding.key_checking_short") : t("onboarding.waiting_check")}</Button>}</>;
  else if (connected) foot = <><span /><Button variant="primary" className="onb-primary" type="button" iconAfter="arrow-right" data-autofocus onClick={onNext}>{t("onboarding.next")}</Button></>;
  else if (phase === "waiting" && choice === "chatgpt") foot = <><Button variant="ghost" className="onb-quiet" onClick={cancelChatGPT}>{t("onboarding.cancel")}</Button><Button variant="primary" className="onb-primary" type="button" disabled>{t("onboarding.waiting")}</Button></>;
  else if (choice === "chatgpt") foot = <><span className="onb-lock"><Icon name="lock" size={14} /> {t("onboarding.private_note")}</span><Button variant="primary" className="onb-primary" type="button" data-autofocus onClick={() => void startChatGPT()}>{phase === "error" ? t("onboarding.sign_in_again") : continueLabel}</Button></>;
  else if (choice === "server") foot = <><Button variant="ghost" className="onb-quiet" disabled={busy} onClick={onBack}>{t("onboarding.back")}</Button><Button variant="primary" className="onb-primary" type="submit" form="onb-server-form" disabled={serverBusy}>{serverBusy ? t("onboarding.key_checking_short") : t("onboarding.server_connect")}</Button></>;
  else if (choice === "key") foot = <><Button variant="ghost" className="onb-quiet" disabled={busy} onClick={onBack}>{t("onboarding.back")}</Button><Button variant="primary" className="onb-primary" type="submit" form="onb-key-form" disabled={busy || !key.trim() || provider === "anthropic" && !workspace.trim()}>{phase === "checking" ? t("onboarding.key_checking_short") : phase === "error" ? t("onboarding.key_try_again") : t("onboarding.key_connect")}</Button></>;
  else foot = <><Button variant="ghost" className="onb-quiet" disabled={busy} onClick={onBack}>{t("onboarding.back")}</Button><Button variant="primary" className="onb-primary" type="button" disabled>{t("onboarding.next")}</Button></>;

  return (
    <>
      <div className="onb-body">
        <h2 id={titleId} className="onb-title">{provided ? t(modelConfigured ? "onboarding.provided_title" : "onboarding.waiting_heading") : connected ? t("onboarding.connected_title") : t("onboarding.model_title")}</h2>
        {body}
      </div>
      <footer className="onb-foot">{foot}</footer>
    </>
  );
}
