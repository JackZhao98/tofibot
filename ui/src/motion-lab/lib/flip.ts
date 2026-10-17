import { flushSync } from "react-dom";

type FlipOptions = { duration: number; easing: string; selector?: string; /** Only the last N matches are measured and played; a long list measures just the rows that can be on screen. */ last?: number };

/**
 * FLIP for a keyed list: record each child's position, apply a React update
 * synchronously, then play every moved child from its old spot to the new one.
 * Children are matched by their `data-flip` attribute.
 */
export function flipList(container: HTMLElement | null, update: () => void, { duration, easing, selector = "[data-flip]", last }: FlipOptions): void {
  if (!container) { flushSync(update); return; }
  const before = new Map<string, DOMRect>();
  const pick = () => { const all = Array.from(container.querySelectorAll<HTMLElement>(selector)); return last === undefined ? all : all.slice(-last); };
  pick().forEach((element) => {
    before.set(element.dataset.flip ?? "", element.getBoundingClientRect());
  });
  flushSync(update);
  pick().forEach((element) => {
    const first = before.get(element.dataset.flip ?? "");
    if (!first) return;
    const after = element.getBoundingClientRect();
    const dx = first.left - after.left;
    const dy = first.top - after.top;
    if (Math.abs(dx) < 0.5 && Math.abs(dy) < 0.5) return;
    element.animate({ transform: [`translate(${dx}px, ${dy}px)`, "none"] }, { duration, easing });
  });
}
