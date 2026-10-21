// One catalog frame: an iframe the shell renders, at a real viewport width, so the product's media queries, fixed
// overlays and portals behave exactly as they do in the app. A frame renders either every inline state of a section,
// stacked, or one overlay / app state on its own.
import { Component, StrictMode, useEffect, useLayoutEffect, useRef, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import App from "../App";
import { OwnerSessionGate } from "../OwnerSession";
import { BootDissolve } from "../BootScreen";
import { TimezoneProvider } from "../UserTimezone";
import { useLanguage } from "../i18n";
import { installFocusModality } from "../focusModality";
import { setSynthetic, frameParams } from "./sandbox";
import { isolated, workspace } from "./world";
import { driver } from "./drive";
import { force, installForcedStates } from "./pseudo";
import type { CatalogSection, CatalogState } from "./types";

type Ready = (error?: string) => void;

class Boundary extends Component<{ children: ReactNode; onError: (message: string) => void }, { error: string }> {
  state = { error: "" };
  static getDerivedStateFromError(error: unknown) { return { error: error instanceof Error ? error.message : String(error) }; }
  componentDidCatch(error: unknown) { this.props.onError(error instanceof Error ? error.message : String(error)); }
  render() { return this.state.error ? <pre className="catalog-error" role="alert">{this.state.error}</pre> : this.props.children; }
}

/** Runs the state's drive steps and forced states once its component has rendered. */
function Staged({ state, children, onReady }: { state: CatalogState; children: ReactNode; onReady: Ready }) {
  const ref = useRef<HTMLDivElement>(null);
  const started = useRef(false);
  useEffect(() => {
    if (started.current) return;
    started.current = true;
    const root = ref.current!;
    void (async () => {
      try {
        await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
        if (state.drive) await state.drive(driver(root));
        for (const item of state.force ?? []) if (!force(state.kind === "inline" ? root : document.body, item.selector, item.state)) throw new Error(`catalog: ${state.id} has nothing to force at ${item.selector}`);
        onReady();
      } catch (cause) { onReady(cause instanceof Error ? cause.message : String(cause)); }
    })();
  }, [state, onReady]);
  return <div ref={ref} className="catalog-state" data-catalog-state={state.id}>{children}</div>;
}

/** The real entry tree from main.tsx, unchanged. */
function AppRoot() {
  useLanguage();
  return <><OwnerSessionGate><App /></OwnerSessionGate><BootDissolve /></>;
}

function AppDriver({ state, onReady }: { state: CatalogState; onReady: Ready }) {
  const started = useRef(false);
  useLayoutEffect(() => {
    if (started.current) return;
    started.current = true;
    void (async () => {
      try {
        const step = driver(document.body);
        if (!state.app?.boot) await step.find(".workspace-grid", 8000);
        await step.wait(400);
        if (state.drive) await state.drive(step);
        for (const item of state.force ?? []) if (!force(document.body, item.selector, item.state)) throw new Error(`catalog: ${state.id} has nothing to force at ${item.selector}`);
        onReady();
      } catch (cause) { onReady(cause instanceof Error ? cause.message : String(cause)); }
    })();
  }, [state, onReady]);
  return null;
}

export function mountFrame(section: CatalogSection) {
  installFocusModality();
  installForcedStates();
  const html = document.documentElement;
  html.classList.add("catalog-frame");
  const states = frameParams.state ? section.states.filter(item => item.id === frameParams.state) : section.states.filter(item => item.kind === "inline");
  if (!states.length) throw new Error(`catalog: ${section.id} has no state ${frameParams.state || "(inline)"}`);
  let pending = states.length;
  const errors: string[] = [];
  const ready: Ready = error => {
    if (error) errors.push(error);
    if (--pending > 0) return;
    if (errors.length) html.dataset.catalogError = errors.join("\n");
    html.dataset.catalogReady = "1";
    report();
  };
  const report = () => parent.postMessage({ catalog: "height", section: section.id, state: frameParams.state, height: document.documentElement.scrollHeight, error: html.dataset.catalogError ?? "" }, location.origin);
  new ResizeObserver(report).observe(document.body);

  const single = states.length === 1 ? states[0] : undefined;
  const root = createRoot(document.getElementById("root")!);
  if (single?.kind === "app") {
    setSynthetic(workspace(single.app!));
    // The App reads its route from the address bar. The query stays, so the frame still knows what it is.
    history.replaceState(null, "", single.app!.path + location.search);
    root.render(<StrictMode><Boundary onError={message => ready(message)}><AppRoot /><AppDriver state={single} onReady={ready} /></Boundary></StrictMode>);
    return;
  }
  setSynthetic(single?.server ?? section.server ?? isolated());
  html.classList.add(single ? "catalog-frame-overlay" : "catalog-frame-inline");
  root.render(<StrictMode><TimezoneProvider>
    <div className="workspace catalog-stage">
      {states.map(state => <Boundary key={state.id} onError={message => ready(`${state.id}: ${message}`)}>
        {!single && <p className="catalog-caption">{state.label}{state.note && <span>{state.note}</span>}</p>}
        <Staged state={state} onReady={ready}>{(state.context ?? section.context ?? []).reduceRight<ReactNode>((inner, className) => <div className={className}>{inner}</div>, state.render?.())}</Staged>
      </Boundary>)}
    </div>
  </TimezoneProvider></StrictMode>);
}
