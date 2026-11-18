// Layout helpers for the V3 kit section. They draw no kit style: every kit look comes from src/ds/.
import type { ReactNode } from "react";

/** One specimen with a small caption above it. */
export function Specimen({ label, children }: { label: string; children: ReactNode }) {
  return <span className="catalog-specimen"><small>{label}</small>{children}</span>;
}

/** A wrapping row of specimens. */
export function Row({ children }: { children: ReactNode }) {
  return <div className="catalog-specimens">{children}</div>;
}

/** The notebook ground (dot grid) the kit sits on in the product; glass and empty states are shown on it. */
export function Dots({ children }: { children: ReactNode }) {
  return <div className="catalog-dots">{children}</div>;
}

/** A forced interaction state names its specimen with data-specimen. */
export const INTERACTION = [
  { label: "Rest", key: "rest" },
  { label: "Hover", key: "hover" },
  { label: "Focus", key: "focus" },
  { label: "Pressed", key: "active" },
  { label: "Disabled", key: "disabled" },
] as const;
export const forced = (selector = "[data-specimen]") => [
  { selector: `${selector}[data-specimen="hover"]`, state: "hover" as const },
  { selector: `${selector}[data-specimen="focus"]`, state: "focus" as const },
  { selector: `${selector}[data-specimen="active"]`, state: "active" as const },
];
