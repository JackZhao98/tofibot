import tokens from "../../design-tokens.css?raw";
import { MOTION, EASE, SPRING } from "../../motion";
import { TofiIcon, tofiIconNames } from "../../icons";
import type { CatalogSection } from "../types";

// Read the vocabulary from the real stylesheet, including aliases. Values resolve in the frame's actual theme.
const names = [...new Set([...tokens.matchAll(/(--[\w-]+)\s*:/g)].map(match => match[1]))];
const value = (name: string) => getComputedStyle(document.documentElement).getPropertyValue(name).trim();
function Tokens({ kind }: { kind: "colour" | "type" | "radius" | "shadow" }) {
  const selected = names.filter(name => kind === "colour" ? CSS.supports("color", value(name))
    : kind === "type" ? name.startsWith("--type-")
    : kind === "radius" ? /^--(radius-|bubble-(radius|tail|joined)$|r$|r-lg$)/.test(name)
    : /shadow|elevation|lift|^--hard$|^--hard-sm$|^--sheen$/.test(name));
  return <div className="catalog-token-grid">{selected.map(name => <figure key={name}>
    {kind === "colour" ? <div className="catalog-token-sample" style={{ background: `var(${name})` }}/>
      : kind === "type" ? <p style={{ font: `var(${name})` }}>The quick brown cat · 0123456789</p>
      : <div className="catalog-token-sample" style={{ background: "var(--surface)", ...(kind === "radius" ? { borderRadius: `var(${name})` } : { boxShadow: `var(${name})` }) }}/>}
    <figcaption><code>{name}</code><small>{value(name)}</small></figcaption>
  </figure>)}</div>;
}
export const foundations: CatalogSection = {
  id: "foundations", number: "F", title: "Foundations",
  states: [
    ...(["colour", "type", "radius", "shadow"] as const).map(kind => ({ id: kind, kind: "inline" as const, label: kind, render: () => <Tokens kind={kind}/> })),
    { id: "motion", kind: "inline", label: "Motion durations and easing", note: "CSS tokens with their JS mirrors; hover a sample to see the curve, reduced motion stays still.",
      render: () => <div className="catalog-token-grid">{names.filter(name => name.startsWith("--m-") || name.startsWith("--ease-")).map(name => <figure key={name}>
        <div className="catalog-motion-track" style={{ transitionDuration: CSS.supports("transition-duration", value(name)) ? `var(${name})` : "var(--m-base)", transitionTimingFunction: CSS.supports("transition-timing-function", value(name)) ? `var(${name})` : "var(--ease-out)" }}><TofiIcon name="arrow-right"/></div>
        <figcaption><code>{name}</code><small>{value(name)}</small></figcaption>
      </figure>)}<pre>{JSON.stringify({ MOTION, EASE, SPRING }, null, 2)}</pre></div> },
    { id: "icons", kind: "inline", label: "Every icon · outline and filled", render: () => <div className="catalog-icon-grid">{tofiIconNames.map(name => <figure key={name}><TofiIcon name={name} variant="outline"/><TofiIcon name={name} variant="filled"/><figcaption>{name}</figcaption></figure>)}</div> },
  ],
};
