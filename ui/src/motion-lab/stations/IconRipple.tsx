import { useEffect, useMemo, useRef, useState, type MouseEvent } from "react";
import catalog from "../../icons/catalog.json";
import { TofiIcon, createTofiMotion, tofiIconCategories, tofiIconNames, tofiIcons, type TofiIconName } from "../../icons";
import type { MotionCatalog, MotionController } from "../../icons/motion.mjs";
import { rippleDelays } from "../lib/choreo";
import { flipList } from "../lib/flip";
import { SPRINGS, springEasing } from "../lib/spring";
import { prefersReducedMotion, useSeenOnce } from "../lib/hooks";
import "./icon-ripple.css";

const WAVE_SPEED = 0.55; // px per ms
const SETTLE = springEasing(SPRINGS.morph);
const ALL = "all";
const SHOWN = "[data-flip]:not([hidden])";

/** Every icon in the set with its own semantic motion; a click sends a wave through them all. */
export function IconRipple() {
  const [rootRef, seen] = useSeenOnce<HTMLDivElement>(0.2);
  const gridRef = useRef<HTMLDivElement>(null);
  const ringRef = useRef<HTMLSpanElement>(null);
  const motion = useRef<MotionController | null>(null);
  const timers = useRef<number[]>([]);
  const [category, setCategory] = useState<string>(ALL);
  const [focus, setFocus] = useState<TofiIconName | null>(null);
  const visible = useMemo(
    () => tofiIconNames.filter((name) => category === ALL || tofiIcons[name].category === category),
    [category],
  );

  // One controller for the whole wall; each button plays its icon on hover and focus.
  useEffect(() => {
    const controller = createTofiMotion(catalog as unknown as MotionCatalog);
    motion.current = controller;
    const grid = gridRef.current;
    const detach = Array.from(grid?.querySelectorAll<HTMLButtonElement>("[data-flip]") ?? []).map((button) => {
      const svg = button.querySelector("svg");
      return svg ? controller.attach(svg, button.dataset.flip as TofiIconName, button) : () => undefined;
    });
    return () => {
      detach.forEach((stop) => stop());
      timers.current.forEach((timer) => window.clearTimeout(timer));
      controller.destroy();
      motion.current = null;
    };
  }, []);

  const wave = (originX: number, originY: number) => {
    const grid = gridRef.current;
    const controller = motion.current;
    if (!grid || !controller) return;
    timers.current.forEach((timer) => window.clearTimeout(timer));
    const base = grid.getBoundingClientRect();
    const buttons = Array.from(grid.querySelectorAll<HTMLButtonElement>(SHOWN));
    const tokens = getComputedStyle(grid);
    const rest = tokens.getPropertyValue("--ink-soft").trim();
    const lit = tokens.getPropertyValue("--clay-text").trim();
    const centres = buttons.map((button) => {
      const rect = button.getBoundingClientRect();
      return { x: rect.left - base.left + rect.width / 2, y: rect.top - base.top + rect.height / 2 };
    });
    const delays = rippleDelays({ x: originX, y: originY }, centres, WAVE_SPEED);
    const reduced = prefersReducedMotion();
    const ring = ringRef.current;
    if (ring && !reduced) {
      ring.style.left = `${originX}px`;
      ring.style.top = `${originY}px`;
      const reach = Math.max(...delays) * WAVE_SPEED * 2;
      ring.animate({ width: ["0px", `${reach}px`], height: ["0px", `${reach}px`], opacity: [0.9, 0] }, { duration: Math.max(...delays) + 200, easing: "linear" });
    }
    timers.current = buttons.map((button, index) => window.setTimeout(() => {
      const svg = button.querySelector("svg");
      if (svg) controller.play(svg, button.dataset.flip);
      if (!reduced) {
        button.animate(
          { transform: ["none", "translateY(-7px) scale(1.18)", "none"], color: [rest, lit, rest] },
          { duration: 520, easing: "cubic-bezier(.34,1.56,.64,1)" },
        );
      }
    }, delays[index]));
  };

  useEffect(() => {
    if (!seen || prefersReducedMotion()) return;
    const grid = gridRef.current;
    if (grid) wave(grid.clientWidth / 2, 0);
    // First look only.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [seen]);

  const onClick = (event: MouseEvent<HTMLButtonElement>) => {
    const base = gridRef.current!.getBoundingClientRect();
    const rect = event.currentTarget.getBoundingClientRect();
    wave(rect.left - base.left + rect.width / 2, rect.top - base.top + rect.height / 2);
  };

  const choose = (next: string) => {
    if (next === category) return;
    const grid = gridRef.current;
    const before = new Set(Array.from(grid?.querySelectorAll<HTMLElement>(SHOWN) ?? []).map((element) => element.dataset.flip));
    flipList(grid, () => setCategory(next), { duration: SETTLE.durationMs, easing: SETTLE.easing, selector: SHOWN });
    if (prefersReducedMotion()) return;
    grid?.querySelectorAll<HTMLElement>(SHOWN).forEach((element, index) => {
      if (before.has(element.dataset.flip)) return;
      element.animate({ opacity: [0, 1], transform: ["scale(.3)", "none"] }, { duration: SETTLE.durationMs, easing: SETTLE.easing, delay: Math.min(index * 8, 240), fill: "backwards" });
    });
  };

  const focused = focus ? tofiIcons[focus] : null;
  return (
    <div className="icon-ripple" ref={rootRef}>
      <div className="icon-filters" role="group" aria-label="按分类筛选">
        {[{ id: ALL, label: "全部" }, ...tofiIconCategories].map((item) => (
          <button key={item.id} type="button" className="icon-filter" aria-pressed={category === item.id} onClick={() => choose(item.id)}>
            {item.label}
          </button>
        ))}
      </div>
      <div className="stage dotted icon-stage">
        <p className="icon-caption" aria-live="polite">
          {focused && focus ? <><span className="icon-caption-name">{focus}</span>{focused.label}</> : `${visible.length} 个图标 · 悬停看单个动效，点一下起波浪`}
        </p>
        <div className="icon-grid" ref={gridRef}>
          <span className="icon-ring" ref={ringRef} aria-hidden="true" />
          {tofiIconNames.map((name) => (
            <button
              key={name}
              type="button"
              data-flip={name}
              className="icon-cell"
              hidden={!visible.includes(name)}
              aria-label={tofiIcons[name].label}
              onClick={onClick}
              onPointerEnter={() => setFocus(name)}
              onFocus={() => setFocus(name)}
              onPointerLeave={() => setFocus(null)}
              onBlur={() => setFocus(null)}
            >
              <TofiIcon name={name} size={24} />
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}
