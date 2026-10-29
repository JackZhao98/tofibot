// The catalog page around the frames: section list, theme / width / language switches, one iframe per state, theme
// and width. The shell itself renders no product component; everything product-made is inside a frame.
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import { LANGUAGES, LANGUAGE_ENDONYMS, type Language } from "../i18n/languages";
import { SECTIONS } from "./sections/index";
import { WIDTHS, type CatalogSection, type CatalogState, type Width } from "./types";
import type { Theme } from "./sandbox";
import { Select } from "../Select";

type View = { section: string; themes: Theme[]; widths: Width[]; language: Language };
const THEME_CHOICES: Record<string, Theme[]> = { light: ["light"], dark: ["dark"], both: ["light", "dark"] };
const WIDTH_CHOICES: Record<string, Width[]> = { desktop: ["desktop"], phone: ["phone"], both: ["desktop", "phone"] };

function readView(): View {
  const hash = new URLSearchParams(location.hash.slice(1));
  const language = hash.get("l") as Language;
  return {
    section: SECTIONS.some(item => item.id === hash.get("s")) ? hash.get("s")! : SECTIONS[0].id,
    themes: THEME_CHOICES[hash.get("t") ?? "light"] ?? THEME_CHOICES.light,
    widths: WIDTH_CHOICES[hash.get("w") ?? "both"] ?? WIDTH_CHOICES.both,
    language: LANGUAGES.includes(language) ? language : "en",
  };
}
function writeView(view: View) {
  const key = <T,>(choices: Record<string, T[]>, value: T[]) => Object.entries(choices).find(([, list]) => list.join() === value.join())![0];
  location.hash = new URLSearchParams({ s: view.section, t: key(THEME_CHOICES, view.themes), w: key(WIDTH_CHOICES, view.widths), l: view.language }).toString();
}

export function frameSrc(section: string, state: string, theme: Theme, language: Language) {
  const query = new URLSearchParams({ frame: section, theme, lng: language });
  if (state) query.set("state", state);
  return `/catalog.html?${query}`;
}

function Frame({ section, state, theme, width, language, fixedHeight }: { section: string; state: string; theme: Theme; width: Width; language: Language; fixedHeight?: number }) {
  const box = useRef<HTMLDivElement>(null);
  const frame = useRef<HTMLIFrameElement>(null);
  const size = WIDTHS[width];
  const [scale, setScale] = useState(1);
  const [height, setHeight] = useState(fixedHeight ?? 160);
  const [error, setError] = useState("");
  useLayoutEffect(() => {
    const element = box.current!;
    const measure = () => setScale(Math.min(1, element.clientWidth / size.width));
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(element);
    return () => observer.disconnect();
  }, [size.width]);
  useEffect(() => {
    const listen = (event: MessageEvent) => {
      if (event.origin !== location.origin || event.source !== frame.current?.contentWindow || event.data?.catalog !== "height") return;
      if (!fixedHeight) setHeight(Math.max(80, Number(event.data.height) || 0));
      setError(String(event.data.error ?? ""));
    };
    window.addEventListener("message", listen);
    return () => window.removeEventListener("message", listen);
  }, [fixedHeight]);
  const src = frameSrc(section, state, theme, language);
  return <figure className="catalog-frame-box" ref={box} data-theme-label={theme}>
    <figcaption>{theme === "dark" ? "Dark" : "Light"} · {size.label}{scale < 1 ? ` · shown at ${Math.round(scale * 100)}%` : ""} · <a href={src} target="_blank" rel="noreferrer">open alone</a></figcaption>
    {error && <pre className="catalog-frame-error" role="alert">{error}</pre>}
    <div className="catalog-frame-viewport" style={{ height: height * scale, width: size.width * scale }}>
      <iframe ref={frame} title={`${section} ${state || "inline"} ${theme} ${width}`} src={src} loading="lazy" data-catalog-frame={`${section}/${state}/${theme}/${width}`}
        style={{ width: size.width, height, transform: scale < 1 ? `scale(${scale})` : undefined }} />
    </div>
  </figure>;
}

function Frames({ section, state, view }: { section: CatalogSection; state?: CatalogState; view: View }) {
  const widths = view.widths.filter(width => !state?.widths || state.widths.includes(width));
  if (!widths.length) return <p className="catalog-muted">Only at {state!.widths!.map(width => WIDTHS[width].label).join(" and ")}.</p>;
  return <div className="catalog-frames">{view.themes.flatMap(theme => widths.map(width =>
    <Frame key={`${theme}-${width}`} section={section.id} state={state?.id ?? ""} theme={theme} width={width} language={view.language}
      fixedHeight={state ? state.height ?? WIDTHS[width].height : undefined} />))}</div>;
}

function Segmented<T extends string>({ label, value, options, onChange }: { label: string; value: T; options: [T, string][]; onChange: (value: T) => void }) {
  return <div className="catalog-switch" role="radiogroup" aria-label={label}><span>{label}</span>{options.map(([key, text]) =>
    <button key={key} role="radio" aria-checked={value === key} onClick={() => onChange(key)}>{text}</button>)}</div>;
}

export function Shell() {
  const [view, setView] = useState(readView);
  useEffect(() => { const sync = () => setView(readView()); window.addEventListener("hashchange", sync); return () => window.removeEventListener("hashchange", sync); }, []);
  const update = (next: Partial<View>) => writeView({ ...view, ...next });
  const section = SECTIONS.find(item => item.id === view.section)!;
  const inline = section.states.filter(state => state.kind === "inline");
  const solo = section.states.filter(state => state.kind !== "inline");
  const themeKey = view.themes.length === 2 ? "both" : view.themes[0];
  const widthKey = view.widths.length === 2 ? "both" : view.widths[0];
  const count = useMemo(() => SECTIONS.reduce((sum, item) => sum + item.states.length, 0), []);
  useEffect(() => { document.title = `${section.number}. ${section.title} · Tofi component catalog`; }, [section]);
  return <div className="catalog-shell">
    <nav className="catalog-nav" aria-label="Sections">
      <header><strong>Tofi component catalog</strong><small>Dev only · synthetic data · {count} states</small></header>
      <ol>{SECTIONS.map(item => <li key={item.id}><a href={`#${new URLSearchParams({ s: item.id, t: themeKey, w: widthKey, l: view.language })}`} aria-current={item.id === section.id ? "page" : undefined}>
        <span>{item.number}</span>{item.title}</a></li>)}</ol>
    </nav>
    <main className="catalog-main">
      <div className="catalog-toolbar">
        <Segmented label="Theme" value={themeKey} options={[["light", "Light"], ["dark", "Dark"], ["both", "Both"]]} onChange={key => update({ themes: THEME_CHOICES[key] })} />
        <Segmented label="Width" value={widthKey} options={[["desktop", "1280"], ["phone", "390"], ["both", "Both"]]} onChange={key => update({ widths: WIDTH_CHOICES[key] })} />
        <label className="catalog-switch"><span>Language</span><Select value={view.language} onValueChange={value => update({ language: value as Language })}>
          {LANGUAGES.map(language => <option key={language} value={language}>{LANGUAGE_ENDONYMS[language]}</option>)}</Select></label>
      </div>
      <p className="catalog-muted catalog-howto">Every frame is the production component with its own CSS, on synthetic data; nothing here talks to a server. Frames are live: hover, click and type in them.
        Reduced motion: DevTools › Rendering › Emulate CSS prefers-reduced-motion. Touch-only states: DevTools device mode. Source of truth: docs/design/components.md §{section.number}.</p>
      <h1>{section.number}. {section.title}</h1>
      {section.gaps?.length ? <div className="catalog-gaps"><strong>Not shown here yet</strong><ul>{section.gaps.map(gap => <li key={gap}>{gap}</li>)}</ul></div> : null}
      {inline.length > 0 && <section className="catalog-group" data-catalog-group="inline">
        <h2>{inline.map(state => state.label).join(" · ")}</h2>
        <Frames section={section} view={view} />
      </section>}
      {solo.map(state => <section key={state.id} className="catalog-group" data-catalog-group={state.id}>
        <h2>{state.label}</h2>{state.note && <p className="catalog-muted">{state.note}</p>}
        <Frames section={section} state={state} view={view} />
      </section>)}
    </main>
  </div>;
}

/** What scripts/test-catalog.mjs walks: every section and state, without the render functions. */
export function catalogIndex() {
  return SECTIONS.map(section => ({
    id: section.id, number: section.number, title: section.title, gaps: section.gaps ?? [],
    states: section.states.map(({ id, label, kind, probe, keyboard, widths, height }) => ({ id, label, kind, probe, keyboard, widths: widths ?? ["desktop", "phone"], height })),
  }));
}
