// V3 kit, overlays (PR 2.3): Menu, Dialog, Toast on a workspace edge, Hint. Dialog and Menu here are the static kit components;
// the product's own menus and dialogs are shown in sections 8 to 11, drawn with the same classes.
import { Button } from "../../ds/Button/Button";
import { Dialog } from "../../ds/Dialog/Dialog";
import { Hint } from "../../ds/Hint/Hint";
import { Kbd } from "../../ds/Kbd/Kbd";
import { Menu, type MenuItem } from "../../ds/Menu/Menu";
import { Toast } from "../../ds/Toast/Toast";
import type { CatalogState } from "../types";
import { Dots, Row, Specimen } from "./v3-parts";

const noop = () => {};
const ROW_MENU: MenuItem[] = [
  { label: "Rename", icon: "edit" },
  { label: "Pin to the top", icon: "pin", hint: "P" },
  { label: "Mute", icon: "bell-off", checked: true },
  { label: "Export", icon: "download", disabled: true },
  "-",
  { label: "Delete Bot", icon: "trash", danger: true },
];
const LONG_MENU: MenuItem[] = [
  { heading: "Switch language" },
  { label: "Deutsch (Deutschland, Bundesrepublik)", icon: "globe" },
  { label: "Traditional Chinese (Taiwan) with a long name that must wrap", icon: "globe", checked: true },
  { label: "Delete every conversation and file that belongs to this Bot", icon: "trash", danger: true },
];

export const overlays: CatalogState[] = [
  { id: "menu", kind: "inline", label: "Menu · icon, hint, tick, disabled, danger last", note: "the row under the pointer is solid ink; destructive items go last, after a separator",
    render: () => <Dots><Row>
      <Specimen label="rest"><Menu label="Row menu" items={ROW_MENU} /></Specimen>
      <Specimen label="hover on the second row"><Menu label="Row menu, hover" items={ROW_MENU} className="catalog-menu-hover" /></Specimen>
      <Specimen label="hover on the danger row"><Menu label="Row menu, danger" items={ROW_MENU} className="catalog-menu-danger" /></Specimen>
    </Row></Dots>,
    force: [{ selector: ".catalog-menu-hover .tb-menu-item:nth-of-type(2)", state: "hover" }, { selector: ".catalog-menu-danger .tb-menu-item.is-danger", state: "hover" }] },
  { id: "menu-heading", kind: "inline", label: "Menu · group heading, rows that wrap", note: "long labels wrap inside the menu and never leave it",
    render: () => <Dots><div style={{ maxWidth: 330 }}><Menu label="Long labels" items={LONG_MENU} /></div></Dots> },
  { id: "dialog", kind: "inline", label: "Dialog · neutral, with a close button", note: "name the question; the confirm button is last",
    render: () => <Dialog scrim title="Rename this group" onClose={noop} actions={<><Button variant="secondary">Cancel</Button><Button variant="primary">Rename</Button></>}>
      Members see the new name at once. The old name stays in the history.
    </Dialog> },
  { id: "dialog-danger", kind: "inline", label: "Dialog · danger mark, names what is lost",
    render: () => <Dialog scrim tone="danger" title="Delete Miso?" actions={<><Button variant="secondary">Cancel</Button><Button variant="danger">Delete Miso</Button></>}>
      <p>Its conversations, files and schedules are deleted from this server. This cannot be undone.</p>
      <p className="tb-dialog-note">The computer it uses stays as it is.</p>
    </Dialog> },
  { id: "dialog-narrow", kind: "inline", label: "Dialog · long title and long buttons on a narrow screen", note: "at 390 px the buttons wrap and nothing leaves the panel",
    render: () => <Dialog scrim tone="danger" title="Delete the group called Quarterly planning, hiring and the offsite for the whole company?" onClose={noop}
      actions={<><Button variant="secondary">Keep the group</Button><Button variant="danger">Delete the group and all of its files</Button></>}>
      <p>Every member loses access. The files are removed from the file store in a few minutes.</p>
    </Dialog> },
  { id: "toast-edge", kind: "inline", label: "Toast · a workspace notice with Retry, and a site check", note: "role alert for a failure that stays; one action at most",
    render: () => <Dots><div className="catalog-stack">
      <Toast tone="error" action="Retry" onAction={noop}>Couldn&rsquo;t refresh the workspace.</Toast>
      <Toast tone="error" role="status" action="Verify again" onAction={noop}>The site protection wants a fresh check before it can go on.</Toast>
    </div></Dots> },
  { id: "hint", kind: "inline", label: "Hint · the tooltip tokens, plain and quiet", note: "ActionHints positions it from data-hint; here it is placed in the flow",
    render: () => <Dots><Row>
      <Specimen label="icon name"><Hint style={{ position: "static" }}>Copy link</Hint></Specimen>
      <Specimen label="quiet, read-by names"><Hint tone="quiet" style={{ position: "static" }}>Read by Mia, Noah and 4 more</Hint></Specimen>
      <Specimen label="with a key"><Hint style={{ position: "static", display: "inline-flex", gap: 6, alignItems: "center" }}>Search <Kbd>K</Kbd></Hint></Specimen>
    </Row></Dots> },
];
