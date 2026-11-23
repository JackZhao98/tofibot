import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";
import { layerRoot } from "../layers";
import { onTunables, setTunable, tunables, tunableValue } from "../tunables";
import "./tuning-panel.css";

/**
 * Dev-only Tune panel: a small button at the bottom right opens sliders for every registered motion parameter (`src/tunables.ts`), each
 * starting at its preset. Values are kept in localStorage and apply to the next motion. main.tsx imports this only when import.meta.env.DEV.
 */
function TuningPanel() {
  const [open, setOpen] = useState(false);
  const [, render] = useState(0);
  useEffect(() => onTunables(() => render(n => n + 1)), []);
  const groups = new Map<string, ReturnType<typeof tunables>>();
  for (const def of tunables()) groups.set(def.group, [...(groups.get(def.group) ?? []), def]);
  return <div className="dev-tune" data-open={open || undefined}>
    {open && <div className="dev-tune-panel" role="dialog" aria-label="Tune">
      <div className="dev-tune-head"><strong>Tune</strong><button type="button" onClick={() => tunables().forEach(def => setTunable(def.key, null))}>Reset all</button></div>
      {[...groups].map(([group, defs]) => <fieldset key={group}><legend>{group}</legend>
        {defs.map(def => {
          const value = tunableValue(def.key);
          const changed = value !== def.value;
          return <label key={def.key} className="dev-tune-row">
            <span>{def.label}</span>
            <input type="range" min={def.min} max={def.max} step={def.step} value={value} onChange={event => setTunable(def.key, Number(event.target.value))}/>
            <output data-changed={changed || undefined}>{value}</output>
            <button type="button" disabled={!changed} title={`Preset ${def.value}`} onClick={() => setTunable(def.key, null)}>↺</button>
          </label>;
        })}
      </fieldset>)}
      <p className="dev-tune-note">Applies to the next motion. Dev build only.</p>
    </div>}
    <button type="button" className="dev-tune-toggle" aria-expanded={open} onClick={() => setOpen(value => !value)}>Tune</button>
  </div>;
}

export function mountTuningPanel() {
  if (document.getElementById("dev-tune-root")) return;
  const host = document.createElement("div");
  host.id = "dev-tune-root";
  layerRoot().append(host);
  createRoot(host).render(<TuningPanel/>);
}
