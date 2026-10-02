import { flushSync } from "react-dom";

type FlipOptions = { duration: number; easing: string; selector?: string };

/**
 * FLIP for a keyed list: record each child's position, apply a React update
 * synchronously, then play every moved child from its old spot to the new one.
 * Children are matched by their `data-flip` attribute.
 */
export function flipList(container: HTMLElement | null, update: () => void, { duration, easing, selector = "[data-flip]" }: FlipOptions): void {
  if (!container) { flushSync(update); return; }
  const before = new Map<string, DOMRect>();
  container.querySelectorAll<HTMLElement>(selector).forEach((element) => {
    before.set(element.dataset.flip ?? "", element.getBoundingClientRect());
  });
  flushSync(update);
  container.querySelectorAll<HTMLElement>(selector).forEach((element) => {
    const first = before.get(element.dataset.flip ?? "");
    if (!first) return;
    const last = element.getBoundingClientRect();
    const dx = first.left - last.left;
    const dy = first.top - last.top;
    if (Math.abs(dx) < 0.5 && Math.abs(dy) < 0.5) return;
    element.animate({ transform: [`translate(${dx}px, ${dy}px)`, "none"] }, { duration, easing });
  });
}
