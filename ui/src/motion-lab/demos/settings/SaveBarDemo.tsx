import { useLayoutEffect, useRef, useState } from "react";
import { TofiIcon } from "../../../icons";
import { LoadingCat } from "../../lib/LoadingCat";
import "./settings.css";

type Settings = Readonly<{ model: string; language: string; notify: boolean }>;
type Status = "idle" | "saving" | "saved";

const SAVED: Settings = { model: "luna", language: "zh", notify: true };

function Segmented({ label, value, options, onChange }: { label: string; value: string; options: readonly (readonly [string, string])[]; onChange: (value: string) => void }) {
  const listRef = useRef<HTMLDivElement>(null);
  const pillRef = useRef<HTMLSpanElement>(null);
  // One pill slides between segments instead of each segment switching its own background.
  useLayoutEffect(() => {
    const active = listRef.current?.querySelector<HTMLElement>('[aria-checked="true"]');
    const pill = pillRef.current;
    if (!active || !pill) return;
    pill.style.width = `${active.offsetWidth}px`;
    pill.style.transform = `translateX(${active.offsetLeft - 3}px)`;
  }, [value]);
  return (
    <div className="save-field">
      <span>{label}</span>
      <div className="segmented" role="radiogroup" aria-label={label} ref={listRef}>
        <span className="segmented-pill" ref={pillRef} aria-hidden="true" />
        {options.map(([id, text]) => (
          <button key={id} type="button" role="radio" aria-checked={value === id} onClick={() => onChange(id)}>{text}</button>
        ))}
      </div>
    </div>
  );
}

/** Settings save bar: rises when something changed, shows saving then saved, and sinks away. */
export function SaveBarDemo() {
  const [saved, setSaved] = useState<Settings>(SAVED);
  const [draft, setDraft] = useState<Settings>(SAVED);
  const [status, setStatus] = useState<Status>("idle");

  const changes = (Object.keys(draft) as (keyof Settings)[]).filter((key) => draft[key] !== saved[key]).length;
  const open = changes > 0 || status !== "idle";
  const update = (patch: Partial<Settings>) => setDraft((current) => ({ ...current, ...patch }));

  const save = () => {
    setStatus("saving");
    window.setTimeout(() => {
      setSaved(draft);
      setStatus("saved");
      window.setTimeout(() => setStatus("idle"), 1100);
    }, 900);
  };

  return (
    <div className="save-demo">
      <div className="save-form">
        <Segmented label="默认模型" value={draft.model} options={[["luna", "Luna"], ["terra", "Terra"]]} onChange={(model) => update({ model })} />
        <Segmented label="回复语言" value={draft.language} options={[["zh", "中文"], ["en", "English"]]} onChange={(language) => update({ language })} />
        <div className="save-field">
          <span>完成后提醒我</span>
          <button type="button" role="switch" aria-checked={draft.notify} aria-label="完成后提醒我" className="save-switch" onClick={() => update({ notify: !draft.notify })}>
            <span className="save-knob" />
          </button>
        </div>
      </div>
      <div className={`save-bar${open ? " is-open" : ""} is-${status}`} role="status" aria-hidden={!open}>
        {status === "idle" && <>
          <span className="save-text">{changes} 处修改还没保存</span>
          <button type="button" className="lab-btn quiet" tabIndex={open ? 0 : -1} onClick={() => setDraft(saved)}>放弃</button>
          <button type="button" className="lab-btn primary" tabIndex={open ? 0 : -1} onClick={save}>保存</button>
        </>}
        {status === "saving" && <span className="save-text"><LoadingCat size={24} />保存中…</span>}
        {status === "saved" && <span className="save-text is-saved"><TofiIcon name="check-circle" size={18} variant="filled" />已保存</span>}
      </div>
    </div>
  );
}
