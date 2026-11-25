// V3 kit, group 5 (PR 2.2): Switch, Segmented, Tabs, the Select field and its list, and the Collapsible motion.
import { useState } from "react";
import { Button } from "../../ds/Button/Button";
import { CollapsibleContent } from "../../ds/Collapsible/Collapsible";
import { Field } from "../../ds/Field/Field";
import { Segmented } from "../../ds/Segmented/Segmented";
import { SettingsRow } from "../../ds/SettingsRow/SettingsRow";
import { Switch } from "../../ds/Switch/Switch";
import { SwatchPicker } from "../../ds/SwatchPicker/SwatchPicker";
import { Tabs } from "../../ds/Tabs/Tabs";
import { Select } from "../../Select";
import * as Collapsible from "@radix-ui/react-collapsible";
import type { CatalogState } from "../types";
import { Row, Specimen } from "./v3-parts";

const MODELS = [
  { value: "claude", label: "Claude Sonnet", description: "Fast, for most chats" },
  { value: "deep", label: "Claude Opus", description: "Slower, for hard problems", tag: "New" },
  { value: "none", label: "Not available", disabled: true },
];

function SwitchRow({ disabled = false }: { disabled?: boolean }) {
  const [on, setOn] = useState(true);
  return <Row>
    <Specimen label="off"><Switch label="Off" disabled={disabled} /></Specimen>
    <Specimen label="on"><Switch label="On" defaultChecked disabled={disabled} /></Specimen>
    <Specimen label="controlled"><Switch label="Controlled" checked={on} onChange={setOn} disabled={disabled} /></Specimen>
    <Specimen label="busy"><Switch label="Busy" busy defaultChecked disabled={disabled} /></Specimen>
  </Row>;
}

function Choices({ disabled = false }: { disabled?: boolean }) {
  const [theme, setTheme] = useState("system");
  return <div className="catalog-stack">
    <Segmented label="Theme" value={theme} onChange={setTheme} disabled={disabled} options={["system", "light", "dark"]} />
    <Segmented label="Range" defaultValue="30d" disabled={disabled} options={[{ value: "7d", label: "7 days" }, { value: "30d", label: "30 days" }]} />
    <Segmented label="Mode" defaultValue="auto" disabled={disabled} options={[{ value: "off", label: "Off", icon: "close" }, { value: "auto", label: "Auto", icon: "check" }]} />
  </div>;
}

function SelectField({ size, disabled = false, placeholder, id, "aria-invalid": invalid, "aria-describedby": describedBy }: { size?: "md" | "sm"; disabled?: boolean; placeholder?: string; id?: string; "aria-invalid"?: boolean; "aria-describedby"?: string }) {
  const [value, setValue] = useState(placeholder ? "unset" : "claude");
  return <Select value={value} onValueChange={setValue} options={MODELS} size={size} disabled={disabled} placeholder={placeholder} aria-label="Model" id={id} aria-invalid={invalid} aria-describedby={describedBy} />;
}

function Drawer({ initial }: { initial: boolean }) {
  const [open, setOpen] = useState(initial);
  return <Collapsible.Root open={open} onOpenChange={setOpen}>
    <Collapsible.Trigger asChild><Button variant="secondary" size="sm" iconAfter="chevron-down">{open ? "Hide details" : "Show details"}</Button></Collapsible.Trigger>
    <CollapsibleContent><div style={{ padding: "10px 0" }}>The details sit in one child element; the row grows from 0 to its height.</div></CollapsibleContent>
  </Collapsible.Root>;
}

export const choices: CatalogState[] = [
  { id: "switch", kind: "inline", label: "Switch · off, on, controlled, busy", render: () => <SwitchRow /> },
  { id: "switch-disabled", kind: "inline", label: "Switch · disabled", render: () => <SwitchRow disabled /> },
  { id: "switch-focus", kind: "inline", label: "Switch · keyboard focus", render: () => <Row><Switch label="Focus" defaultChecked /></Row>, force: [{ selector: ".tb-switch", state: "focus" }] },
  { id: "segmented", kind: "inline", label: "Segmented · plain, two options, with icons", render: () => <Choices /> },
  { id: "segmented-disabled", kind: "inline", label: "Segmented · disabled", render: () => <Choices disabled /> },
  { id: "segmented-focus", kind: "inline", label: "Segmented · keyboard focus and hover", render: () => <Row><Segmented label="Theme" defaultValue="system" options={["system", "light", "dark"]} /></Row>,
    force: [{ selector: ".tb-seg-opt:nth-child(1)", state: "focus" }, { selector: ".tb-seg-opt:nth-child(3)", state: "hover" }] },
  { id: "swatch-picker", kind: "inline", label: "SwatchPicker · five Bot hues, one chosen, and disabled",
    render: () => <Row><Specimen label="grape"><SwatchPicker label="Avatar colour" defaultValue="grape" /></Specimen><Specimen label="disabled"><SwatchPicker label="Avatar colour" defaultValue="teal" disabled /></Specimen></Row> },
  { id: "swatch-picker-focus", kind: "inline", label: "SwatchPicker · keyboard focus", render: () => <Row><SwatchPicker label="Avatar colour" defaultValue="grape" /></Row>, force: [{ selector: ".tb-swatch--teal", state: "focus" }] },
  { id: "tabs", kind: "inline", label: "Tabs · with counts and a panel (a function of the value)",
    render: () => <Tabs label="Key views" tabs={[{ value: "keys", label: "Keys", count: 4 }, { value: "usage", label: "Usage" }, { value: "members", label: "Members", count: 12 }]}>
      {value => <p style={{ margin: 0 }}>The {value} view.</p>}
    </Tabs> },
  { id: "tabs-focus", kind: "inline", label: "Tabs · keyboard focus",
    render: () => <Tabs label="Views" tabs={[{ value: "a", label: "Keys" }, { value: "b", label: "Usage" }]} />, force: [{ selector: ".tb-tab:nth-child(2)", state: "focus" }] },
  { id: "select", kind: "inline", label: "Select · md, sm, placeholder, disabled, invalid (the field of a text input)",
    render: () => <div className="catalog-stack">
      <SelectField />
      <SelectField size="sm" />
      <SelectField placeholder="Choose a model" />
      <SelectField disabled />
      <Field label="Model" error="Choose a model to continue."><SelectField /></Field>
    </div> },
  { id: "select-open", kind: "overlay", probe: ".tb-select-list", keyboard: { close: "Escape" }, label: "Select · open list (Menu material; the chosen row is bold with a tick, the row under the pointer is ink)",
    render: () => <div className="catalog-stack"><SelectField /></div>,
    // [data-state] is the Radix trigger: until its lazy chunk arrives the field is a plain button that opens nothing.
    drive: async step => { await step.click(".tb-select[data-state]"); await step.find(".tb-select-list"); await step.settled(); } },
  { id: "select-phone", kind: "inline", widths: ["phone"], label: "Switch, Segmented and Select in settings rows on a phone (390 px, 700 or narrower)",
    note: "Switches and segmented controls stay beside the text; the Select takes the full row under it.",
    render: () => <div className="catalog-stack">
      <SettingsRow label="Notify me" hint="When a Bot needs you."><Switch label="Notify me" defaultChecked /></SettingsRow>
      <SettingsRow label="Theme"><Segmented label="Theme" defaultValue="system" options={["system", "light", "dark"]} /></SettingsRow>
      <Field label="Model"><SelectField /></Field>
    </div> },
  { id: "collapsible", kind: "inline", label: "Collapsible · closed, open (the drawer motion lives in the kit)",
    render: () => <div className="catalog-stack"><Drawer initial={false} /><Drawer initial /></div> },
];
