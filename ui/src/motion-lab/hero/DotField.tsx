import { useEffect, useImperativeHandle, useRef, type Ref } from "react";
import { prefersReducedMotion } from "../lib/hooks";

export type DotFieldHandle = {
  /** Send a ring through the dots from (x, y) in field coordinates; strength 0..1. */
  ripple(x: number, y: number, strength: number): void;
};

type Ripple = Readonly<{ x: number; y: number; born: number; strength: number }>;

// Matches the 16px --dots texture so the canvas lines up with every other dotted surface.
const PITCH = 16;
const LENS_RADIUS = 150;
const RIPPLE_SPEED = 0.95; // px per ms
const RIPPLE_LIFE = 1500; // ms
const BASE_ALPHA = 0.1;

type Pointer = { x: number; y: number } | null;

function readInk(): string {
  return getComputedStyle(document.documentElement).getPropertyValue("--ink").trim() || "#241b14";
}

/** The hero's dotted ground: dots swell under the pointer and ring outward on impacts. */
export function DotField({ ref }: { ref?: Ref<DotFieldHandle> }) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const ripples = useRef<readonly Ripple[]>([]);
  const wake = useRef<() => void>(() => undefined);

  useImperativeHandle(ref, () => ({
    ripple(x, y, strength) {
      if (prefersReducedMotion() || strength <= 0) return;
      ripples.current = [...ripples.current.slice(-5), { x, y, born: performance.now(), strength: Math.min(1, strength) }];
      wake.current();
    },
  }), []);

  useEffect(() => {
    const canvas = canvasRef.current;
    const field = canvas?.parentElement;
    const context = canvas?.getContext("2d");
    if (!canvas || !field || !context) return;
    let width = 0;
    let height = 0;
    let ink = readInk();
    let pointer: Pointer = null;
    let frame = 0;

    const resize = () => {
      const rect = field.getBoundingClientRect();
      const ratio = Math.min(window.devicePixelRatio || 1, 2);
      width = rect.width;
      height = rect.height;
      canvas.width = Math.round(width * ratio);
      canvas.height = Math.round(height * ratio);
      context.setTransform(ratio, 0, 0, ratio, 0, 0);
      draw(performance.now());
    };

    const draw = (now: number) => {
      ripples.current = ripples.current.filter((ripple) => now - ripple.born < RIPPLE_LIFE);
      const live = ripples.current;
      context.clearRect(0, 0, width, height);
      context.fillStyle = ink;
      for (let y = PITCH / 2; y < height; y += PITCH) {
        for (let x = PITCH / 2; x < width; x += PITCH) {
          let radius = 1;
          let alpha = BASE_ALPHA;
          let dx = 0;
          let dy = 0;
          if (pointer) {
            const distance = Math.hypot(x - pointer.x, y - pointer.y);
            if (distance < LENS_RADIUS) {
              const force = (1 - distance / LENS_RADIUS) ** 2;
              const push = (force * 7) / (distance || 1);
              radius += force * 1.7;
              alpha += force * 0.34;
              dx += (x - pointer.x) * push;
              dy += (y - pointer.y) * push;
            }
          }
          for (const ripple of live) {
            const age = now - ripple.born;
            const distance = Math.hypot(x - ripple.x, y - ripple.y);
            const band = distance - age * RIPPLE_SPEED;
            if (Math.abs(band) > 48) continue;
            const force = ripple.strength * Math.exp(-((band / 20) ** 2)) * (1 - age / RIPPLE_LIFE);
            const push = (force * 7) / (distance || 1);
            radius += force * 2.1;
            alpha += force * 0.42;
            dx += (x - ripple.x) * push;
            dy += (y - ripple.y) * push;
          }
          context.globalAlpha = Math.min(alpha, 0.6);
          context.beginPath();
          context.arc(x + dx, y + dy, radius, 0, Math.PI * 2);
          context.fill();
        }
      }
      context.globalAlpha = 1;
    };

    const loop = (now: number) => {
      draw(now);
      frame = pointer || ripples.current.length ? requestAnimationFrame(loop) : 0;
    };
    wake.current = () => { if (!frame) frame = requestAnimationFrame(loop); };

    const onMove = (event: PointerEvent) => {
      if (prefersReducedMotion() || event.pointerType === "touch") return;
      const rect = field.getBoundingClientRect();
      pointer = { x: event.clientX - rect.left, y: event.clientY - rect.top };
      wake.current();
    };
    const onLeave = () => { pointer = null; wake.current(); };
    const themeObserver = new MutationObserver(() => { ink = readInk(); draw(performance.now()); });
    const sizeObserver = new ResizeObserver(resize);

    sizeObserver.observe(field);
    themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
    field.addEventListener("pointermove", onMove);
    field.addEventListener("pointerleave", onLeave);
    resize();
    return () => {
      cancelAnimationFrame(frame);
      wake.current = () => undefined;
      sizeObserver.disconnect();
      themeObserver.disconnect();
      field.removeEventListener("pointermove", onMove);
      field.removeEventListener("pointerleave", onLeave);
    };
  }, []);

  return <canvas className="dot-field" ref={canvasRef} aria-hidden="true" />;
}
