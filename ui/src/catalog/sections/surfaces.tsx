import { Banner } from "../../settings/components";
import { EmptyState, CATS } from "../../EmptyCat";
import { popovers } from "./overlays";
import { boot, rows } from "./workspace";
import type { CatalogSection } from "../types";

// Reuse the actual workspace states rather than approximating its widgets and shells in catalog markup.
export const surfaces: CatalogSection = {
  id: "surfaces", number: "S", title: "Surfaces and states",
  states: [
    ...popovers.states.filter(state => /^(widget-|sidebar-|cover-bot-settings$)/.test(state.id)),
    ...rows.states.filter(state => state.id.startsWith("search-")),
    { id: "empty", kind: "inline", label: "EmptyState / EmptyCat · all poses", render: () => <div className="catalog-control-grid">{(["asleep", "awake", "curious"] as const).map(pose => <EmptyState key={pose} name={`catalog-${pose}`} look={CATS.mochi} pose={pose} title="Nothing here yet"><p>Synthetic empty state · {pose}</p></EmptyState>)}</div> },
    { id: "banners", kind: "inline", label: "Error, warning and information banners", render: () => <div className="settings-stack">{(["error", "warn", "info"] as const).map(tone => <Banner key={tone} tone={tone} title="Synthetic service status" details="Synthetic diagnostic details" action={{ label: "Retry", onClick: () => {} }}>Try again when the connection is back.</Banner>)}</div> },
    ...boot.states,
  ],
};
