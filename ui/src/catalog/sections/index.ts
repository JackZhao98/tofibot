// Every section of docs/design/components.md, in its order. The catalog is that document made visible: a component
// added there gets a section here, and states the catalog cannot show yet are listed as gaps on the page.
import { foundations } from "./foundations";
import { primitives } from "./primitives";
import { surfaces } from "./surfaces";
import type { CatalogSection } from "../types";
import { actions, bubbles, composer, edges, receipts } from "./chat";
import { dialogs, hints, listboxes, menus, popovers } from "./overlays";
import { approvals, handover, questions, standing } from "./cards";
import { settings } from "./settings";
import { boot, computer, notices, other, pins, push, rows } from "./workspace";
import { v3 } from "./v3";

export const SECTIONS: CatalogSection[] = [
  foundations, primitives, surfaces,
  bubbles, actions, receipts, composer, rows, pins, dialogs, menus, hints, popovers, listboxes,
  questions, approvals, handover, standing, settings, notices, boot, computer, push, edges, other,
  v3,
];
