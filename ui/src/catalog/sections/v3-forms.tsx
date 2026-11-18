// V3 kit, group 3: Card, Field, TextInput, TextArea, Checkbox.
import { useState } from "react";
import { Button } from "../../ds/Button/Button";
import { Card } from "../../ds/Card/Card";
import { Checkbox } from "../../ds/Checkbox/Checkbox";
import { Field } from "../../ds/Field/Field";
import { TextArea } from "../../ds/TextArea/TextArea";
import { TextInput } from "../../ds/TextInput/TextInput";
import type { CatalogState } from "../types";
import { Row, Specimen } from "./v3-parts";

const VARIANTS = ["solid", "raised", "glass", "quiet", "block"] as const;

function Checks({ disabled = false }: { disabled?: boolean }) {
  const [on, setOn] = useState(true);
  return <Row>
    <Specimen label="unchecked"><Checkbox disabled={disabled}>Read</Checkbox></Specimen>
    <Specimen label="checked"><Checkbox defaultChecked disabled={disabled}>Write</Checkbox></Specimen>
    <Specimen label="controlled"><Checkbox checked={on} onChange={setOn} disabled={disabled}>Admin</Checkbox></Specimen>
    <Specimen label="no label"><Checkbox aria-label="Select row" disabled={disabled} /></Specimen>
  </Row>;
}

export const forms: CatalogState[] = [
  { id: "card", kind: "inline", label: "Card · every variant",
    render: () => <div className="catalog-stack">{VARIANTS.map(variant => <Card key={variant} variant={variant} eyebrow={variant} title="Weekly report"
      actions={<Button size="sm" variant="ghost">Edit</Button>} footer={<Button size="sm">Open</Button>}>Mochi sends it on Fridays.</Card>)}</div> },
  { id: "card-parts", kind: "inline", label: "Card · body only, flush body, title only",
    render: () => <div className="catalog-stack">
      <Card>Body only.</Card>
      <Card pad={false} title="Flush"><div style={{ padding: 12 }}>Content sets its own padding.</div></Card>
      <Card title="Title only" />
    </div> },
  { id: "textinput", kind: "inline", label: "TextInput · plain, icon, suffix, mono, disabled, invalid",
    render: () => <div className="catalog-stack">
      <TextInput placeholder="Room name" aria-label="Room name" />
      <TextInput icon="search" placeholder="Search" aria-label="Search" />
      <TextInput suffix="MB" defaultValue="512" aria-label="Limit" />
      <TextInput mono defaultValue="sk-tofi-…9Fq2" aria-label="Key" />
      <TextInput defaultValue="Locked" disabled aria-label="Locked" />
      <TextInput type="password" defaultValue="secret" aria-label="Password" />
    </div> },
  { id: "textinput-focus", kind: "inline", label: "TextInput · focus (rises to the surface)",
    render: () => <div className="catalog-stack"><TextInput icon="search" defaultValue="Focused" aria-label="Focused" /></div>,
    drive: async step => { await step.focus("input"); } },
  { id: "textarea", kind: "inline", label: "TextArea · rest, filled, disabled",
    render: () => <div className="catalog-stack">
      <TextArea placeholder="Instructions" aria-label="Instructions" />
      <TextArea defaultValue="Be brief. Name the result." aria-label="Notes" />
      <TextArea defaultValue="Locked" disabled aria-label="Locked" />
    </div> },
  { id: "textarea-focus", kind: "inline", label: "TextArea · focus",
    render: () => <div className="catalog-stack"><TextArea defaultValue="Focused" aria-label="Focused" /></div>,
    drive: async step => { await step.focus("textarea"); } },
  { id: "field", kind: "inline", label: "Field · plain, optional, hint, error; around TextInput and TextArea",
    render: () => <div className="catalog-stack">
      <Field label="Name"><TextInput placeholder="Production bot" /></Field>
      <Field label="Webhook URL" optional="Optional"><TextInput mono placeholder="https://" /></Field>
      <Field label="Key name" hint="Bots that use this key stop at their next call if you revoke it."><TextInput defaultValue="production-bot" /></Field>
      <Field label="Port" error="Use a number from 1 to 65535."><TextInput defaultValue="99999" /></Field>
      <Field label="Instructions" hint="One sentence is enough."><TextArea /></Field>
      <Field label="Role" error="A role is required."><TextArea /></Field>
    </div> },
  { id: "checkbox", kind: "inline", label: "Checkbox · unchecked, checked, controlled, no label",
    render: () => <Checks /> },
  { id: "checkbox-disabled", kind: "inline", label: "Checkbox · disabled", render: () => <Checks disabled /> },
  { id: "checkbox-focus", kind: "inline", label: "Checkbox · keyboard focus",
    render: () => <Row><Checkbox defaultChecked>Write</Checkbox></Row>,
    force: [{ selector: "input", state: "focus" }] },
];
