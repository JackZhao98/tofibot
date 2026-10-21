// Every section of docs/design/components.md, in its order. The catalog is that document made visible: a component
// added there gets a section here, and states the catalog cannot show yet are listed as gaps on the page.
import type { CatalogSection } from "../types";
import { actions, bubbles, composer, edges, perch, receipts } from "./chat";
import { dialogs, hints, listboxes, menus, popovers } from "./overlays";
import { approvals, handover, questions, standing } from "./cards";
import { settings } from "./settings";
import { boot, computer, notices, other, pins, push, rows } from "./workspace";

export const SECTIONS: CatalogSection[] = [
  bubbles, actions, receipts, composer, perch, rows, pins, dialogs, menus, hints, popovers, listboxes,
  questions, approvals, handover, standing, settings, notices, boot, computer, push, edges, other,
];
