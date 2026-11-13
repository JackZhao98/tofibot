import { WorkspaceToast } from "../../WorkspaceToast";
import { FilePicker } from "../../settings/components";
import { useId, useState } from "react";
import { Select } from "../../Select";
import { SettingsSwitch, Segmented } from "../../settings/components";
import { Disclosure } from "../../InteractionPrimitives";
import { TofiIcon } from "../../icons";
import { computer } from "./workspace";
import type { CatalogSection, CatalogState } from "../types";
import "../../bot-settings.css";
import "../../group-settings.css";
import "../../admin-accounts.css";
import "../../extensions-system.css";
import "../../desktop-presence.css";

const buttons = [
  "primary-button", "secondary-button", "ghost-button", "text-button", "st-button", "st-button is-danger",
  "close-button", "icon-button", "top-icon-button", "computer-button", "computer-button is-ready",
   "admin-danger-button", "purge-button", "mcp-button", "mcp-button mcp-danger",
  "bs-button", "bs-button is-danger", "gs-button", "gs-button is-danger", "settings-ghost-button", "danger-link", "work-icon-button", "work-icon-button is-danger", "back-button", "send-button",
];
function Controls({ disabled = false }: { disabled?: boolean }) {
  const radioName = useId();
  const [choice, setChoice] = useState("one");
  const [checked, setChecked] = useState(false);
  return <div className="catalog-control-grid">
    <label>Text input<input className="field" placeholder="Room name" disabled={disabled}/></label>
    <label>Textarea<textarea className="field" defaultValue="Synthetic meeting notes" disabled={disabled}/></label>
    <label>Select<Select value={choice} onValueChange={setChoice} disabled={disabled} options={[{ value: "one", label: "One" }, { value: "two", label: "Two" }, { value: "three", label: "Unavailable", disabled: true }]}/></label>
    <label>Small Select<Select size="sm" value={choice} onValueChange={setChoice} disabled={disabled} options={[{ value: "one", label: "One" }, { value: "two", label: "Two" }]}/></label>
    <div><SettingsSwitch label="Switch off" checked={checked} onChange={setChecked} disabled={disabled}/><SettingsSwitch label="Switch on" checked={!checked} onChange={next => setChecked(!next)} disabled={disabled}/></div>
    <div>{[false, true].map(on => <label key={String(on)}>Checkbox<input type="checkbox" defaultChecked={on} disabled={disabled}/></label>)}</div>
    <div>{["one", "two"].map(item => <label key={item}>{item}<input type="radio" name={radioName} value={item} checked={choice === item} onChange={() => setChoice(item)} disabled={disabled}/></label>)}</div>
    <Segmented label="Sample choice" value={choice} options={[{ value: "one", label: "One" }, { value: "two", label: "Two" }]} onChange={setChoice} disabled={disabled}/>
  </div>;
}
const buttonStates: CatalogState[] = buttons.map(className => ({
  id: className.replaceAll(" ", "-"), kind: "inline", label: `.${className.replaceAll(" ", ".")}`,
  // These are native buttons with product classes, not copied component markup.
  render: () => <div className={className.startsWith("computer-button") ? "header-actions" : "mcp-settings"}><div className="catalog-specimens">{["Rest", "Hover", "Focus", "Pressed", "Disabled"].map((state, index) => <span key={state}>
    <small>{state} </small><button type="button" aria-label={`${className} ${state}`} disabled={index === 4} data-specimen={index} className={className}>
      {/close-button|icon-button|computer-button|desktop-control-button/.test(className) ? <TofiIcon name={className.includes("close") ? "close" : "monitor"} size={18}/> : "Example"}
    </button></span>)}</div></div>,
  force: [{ selector: '[data-specimen="1"]', state: "hover" }, { selector: '[data-specimen="2"]', state: "focus" }, { selector: '[data-specimen="3"]', state: "active" }],
}));
export const primitives: CatalogSection = {
  id: "primitives", number: "P", title: "Primitives",
  states: [
    ...buttonStates,
    { id: "workspace-toast", kind: "inline", label: "Radix toast · Retry, F8, swipe to dismiss", render: () => <WorkspaceToast onRetry={() => {}} /> },
    { id: "file-drop", kind: "inline", label: "Dropzone · settings file picker", render: () => <FilePicker label="Choose file" onFile={() => {}} /> },
    ...(["rest", "hover", "focus"] as const).map(state => ({ ...computer.states[0], id: `desktop-control-button-${state}`, label: `.desktop-control-button · ${state}`, force: state === "rest" ? [] : [{ selector: ".desktop-control-button", state }] })),
    { id: "controls", kind: "inline", label: "Form controls · rest", render: () => <Controls/> },
    { id: "controls-hover", kind: "inline", label: "Form controls · hover", render: () => <Controls/>, force: [{ selector: "input,textarea,button", state: "hover" }] },
    { id: "controls-focus", kind: "inline", label: "Form controls · focus ring", render: () => <Controls/>, force: [{ selector: "input,textarea,button", state: "focus" }] },
    { id: "controls-disabled", kind: "inline", label: "Form controls · disabled", render: () => <Controls disabled/> },
    { id: "disclosure", kind: "inline", label: "Disclosure · closed and open", render: () => <><Disclosure title="Details">Synthetic details</Disclosure><Disclosure title="Details" defaultOpen>Synthetic details</Disclosure></> },
    { id: "tabs", kind: "app", label: "Radix tabs · artifact scope and type", note: "the widget column, so desktop width only", app: { path: "/b/bot_mochi" }, widths: ["desktop"], drive: async step => { await step.click("button[aria-label='Open artifacts']"); await step.click(".artifacts-widget-all"); await step.find(".artifacts-kinds"); } },
    { id: "message-toolbar", kind: "app", label: "Radix message toolbar · hover", app: { path: "/b/bot_mochi" }, drive: async step => { await step.hover(".message-block:last-child .message-hover-host"); await step.find(".message-hover-actions"); } },
  ],
};
