import type { ReactNode } from "react";
import type { Synthetic } from "./sandbox";
import type { AppOptions } from "./world";
import type { ForcedState } from "./pseudo";
import type { Drive } from "./drive";

export type Width = "desktop" | "phone";
export const WIDTHS: Record<Width, { width: number; height: number; label: string }> = {
  desktop: { width: 1280, height: 800, label: "1280 px" },
  phone: { width: 390, height: 760, label: "390 px" },
};

/**
 * One state of one component. Three kinds:
 * - inline: the production component rendered in place with synthetic props. A section's inline states share one frame.
 * - overlay: a production component that covers the page (dialog, sheet, popover). Each gets a frame of its own.
 * - app: the real App against the synthetic server (world.ts), then `drive` clicks or types its way to the state. Used
 *   for everything that only exists inside the workspace (sidebar rows, menus, the composer, the phone push).
 */
export type CatalogState = {
  id: string;
  label: string;
  /** One line under the label: what to look at, or how to reach the state by hand in the frame. */
  note?: string;
  kind: "inline" | "overlay" | "app";
  render?: () => ReactNode;
  app?: AppOptions;
  /** The synthetic server for an overlay state (inline states share their section's `server`). */
  server?: Synthetic;
  /** Class names of the ancestors the component has in the app, outermost first, inside `.workspace` (default: the
   * section's). Only the product's own selectors use them; the catalog adds no style to these wrappers. */
  context?: string[];
  /** Steps run after the first render, inside the frame, with real DOM events. */
  drive?: (step: Drive) => Promise<void>;
  /** Forced :hover / :active / :focus-visible (see pseudo.ts), applied after `drive`. */
  force?: { selector: string; state: ForcedState }[];
  /** The overlay this state opens. scripts/test-catalog.mjs checks that the topmost element at its centre is inside it. */
  probe?: string;
  /** Keyboard check: with focus inside the overlay, `close` removes the probe; `reopen` then brings it back. */
  keyboard?: { close: string; reopen?: { focus: string; key: string; shift?: boolean } };
  /** Widths this state means something at (default both). */
  widths?: Width[];
  /** Frame height for overlay and app states (default the width's own height). */
  height?: number;
};

export type CatalogSection = {
  id: string;
  /** The section number in docs/design/components.md. */
  number: string;
  title: string;
  states: CatalogState[];
  /** The synthetic server behind the section's inline frame (default: world.ts `isolated()`). */
  server?: Synthetic;
  /** Ancestor classes for every inline and overlay state of the section (see CatalogState.context). */
  context?: string[];
  /** States components.md lists that the catalog cannot show yet, and why. Shown on the page, never hidden. */
  gaps?: string[];
};
