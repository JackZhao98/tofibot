// V3 kit, group 1: Icon, Button, IconButton. Every variant and state, in the frame's theme.
import { Button, type ButtonProps } from "../../ds/Button/Button";
import { Icon, type IconName } from "../../ds/Icon/Icon";
import { IconButton } from "../../ds/IconButton/IconButton";
import type { CatalogState } from "../types";
import { INTERACTION, Row, Specimen, forced } from "./v3-parts";

const SAMPLE: IconName[] = ["key", "bot", "search", "settings", "check", "close", "bell", "info", "alert", "error", "inbox", "mention"];
const VARIANTS: NonNullable<ButtonProps["variant"]>[] = ["primary", "secondary", "ghost", "danger"];

const buttonRow = (variant: NonNullable<ButtonProps["variant"]>, size: "md" | "sm"): CatalogState => ({
  id: `button-${variant}-${size}`, kind: "inline", label: `Button · ${variant} · ${size}`,
  render: () => <Row>{INTERACTION.map(({ label, key }) => <Specimen key={key} label={label}>
    <Button variant={variant} size={size} disabled={key === "disabled"} data-specimen={key}>Save and restart</Button></Specimen>)}</Row>,
  force: forced(),
});
const iconButtonRow = (variant: "ghost" | "solid" | "danger", size: "md" | "sm"): CatalogState => ({
  id: `iconbutton-${variant}-${size}`, kind: "inline", label: `IconButton · ${variant} · ${size}`,
  render: () => <Row>{INTERACTION.map(({ label, key }) => <Specimen key={key} label={label}>
    <IconButton icon="copy" label="Copy key" variant={variant} size={size} disabled={key === "disabled"} data-specimen={key} /></Specimen>)}</Row>,
  force: forced(),
});

export const actions: CatalogState[] = [
  { id: "read-me", kind: "inline", label: "How to read this section",
    render: () => <p className="catalog-note">The V3 kit lives in src/ds/. The kit reads V3 roles from the skin.</p> },
  { id: "icon-sizes", kind: "inline", label: "Icon · sizes 16, 20, 24, 32 · outline and filled",
    render: () => <div className="catalog-stack">{(["outline", "filled"] as const).map(variant => <Row key={variant}>
      {[16, 20, 24, 32].map(size => <Specimen key={size} label={`${variant} ${size}`}><span style={{ display: "inline-flex", gap: 8 }}>{SAMPLE.slice(0, 4).map(name => <Icon key={name} name={name} size={size} variant={variant} />)}</span></Specimen>)}
    </Row>)}</div> },
  { id: "icon-set", kind: "inline", label: "Icon · a sample of the 144, outline over filled",
    render: () => <Row>{SAMPLE.map(name => <Specimen key={name} label={name}><span style={{ display: "inline-flex", gap: 8 }}><Icon name={name} /><Icon name={name} variant="filled" /></span></Specimen>)}</Row> },
  { id: "icon-title", kind: "inline", label: "Icon · with a title (stands alone) and without (hidden from screen readers)",
    render: () => <Row><Specimen label="title"><Icon name="info" title="Information" /></Specimen><Specimen label="no title"><Icon name="info" /></Specimen></Row> },
  ...VARIANTS.flatMap(variant => [buttonRow(variant, "md"), buttonRow(variant, "sm")]),
  { id: "button-icons", kind: "inline", label: "Button · icon before, icon after, busy",
    render: () => <Row>
      <Specimen label="icon"><Button variant="primary" icon="plus">Create key</Button></Specimen>
      <Specimen label="iconAfter"><Button iconAfter="arrow-right">Continue</Button></Specimen>
      <Specimen label="busy"><Button variant="primary" busy>Saving</Button></Specimen>
      <Specimen label="sm icon"><Button size="sm" icon="copy">Copy</Button></Specimen>
      <Specimen label="icon only content"><Button variant="ghost" icon="more" aria-label="More" /></Specimen>
    </Row> },
  iconButtonRow("ghost", "md"), iconButtonRow("ghost", "sm"), iconButtonRow("solid", "md"), iconButtonRow("solid", "sm"), iconButtonRow("danger", "sm"),
  { id: "iconbutton-filled", kind: "inline", label: "IconButton · filled icon",
    render: () => <Row><Specimen label="outline"><IconButton icon="star" label="Pin" /></Specimen><Specimen label="filled"><IconButton icon="star" label="Unpin" filled /></Specimen></Row> },
];
