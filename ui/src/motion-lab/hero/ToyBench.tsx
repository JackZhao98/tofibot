import { useEffect, useRef, useState, type CSSProperties, type PointerEvent as ReactPointerEvent } from "react";
import { CatStage, type CatHandle } from "../lib/CatStage";
import { CREW } from "../lib/crew";
import { benchSlots, type BenchSlot } from "../lib/choreo";
import { prefersReducedMotion } from "../lib/hooks";
import { bodyTransform, droppingBody, isBodyAtRest, kickSquash, restingBody, stepBody, type Body } from "./benchPhysics";

type Grab = {
  pointerId: number;
  offsetX: number;
  offsetY: number;
  startX: number;
  startY: number;
  lastX: number;
  lastY: number;
  lastTime: number;
  vx: number;
  vy: number;
  moved: boolean;
};

type ToyBenchProps = {
  /** Impact in bench coordinates, used by the dot field for ripples. */
  onImpact?: (x: number, y: number, strength: number) => void;
};

const MAX_THROW = 4200;
const HOVER_COOLDOWN = 2600;

function blockSize(width: number): number {
  if (width >= 1040) return 132;
  if (width >= 700) return 112;
  return 88;
}

function benchHeight(slots: readonly BenchSlot[], size: number): number {
  return Math.max(...slots.map((slot) => slot.y)) + size + 28;
}

/** Seven cat blocks: they drop in, can be dragged and thrown, and spring home. */
export function ToyBench({ onImpact }: ToyBenchProps) {
  const benchRef = useRef<HTMLDivElement>(null);
  const blockRefs = useRef<(HTMLButtonElement | null)[]>([]);
  const catRefs = useRef<(CatHandle | null)[]>([]);
  const bodies = useRef<Body[]>([]);
  const grabs = useRef<(Grab | null)[]>([]);
  const awake = useRef<boolean[]>(CREW.map(() => false));
  const lastHover = useRef<number[]>(CREW.map(() => 0));
  const pendingReaction = useRef<boolean[]>(CREW.map(() => false));
  const frame = useRef(0);
  const lastTick = useRef(0);
  const sizeRef = useRef(112);
  const zTop = useRef(1);
  const [layout, setLayout] = useState<{ size: number; height: number } | null>(null);

  const wakeCat = (index: number, delay: number) => {
    if (awake.current[index]) return;
    awake.current[index] = true;
    window.setTimeout(() => {
      const cat = catRefs.current[index];
      void cat?.play("wake").then(() => cat.setAutoplay(true));
    }, delay);
  };

  const paint = () => {
    bodies.current.forEach((body, index) => {
      const element = blockRefs.current[index];
      if (!element) return;
      element.style.transform = bodyTransform(body);
      element.style.setProperty("--lift", body.lift.position.toFixed(3));
    });
  };

  const tick = (now: number) => {
    const dt = lastTick.current ? (now - lastTick.current) / 1000 : 1 / 60;
    lastTick.current = now;
    const size = sizeRef.current;
    let busy = false;
    bodies.current = bodies.current.map((body, index) => {
      const { body: next, impact } = stepBody(body, dt, now);
      if (impact > 260) {
        onImpact?.(next.x.position + size / 2, next.home.y + size, Math.min(1, impact / 2400));
        if (!awake.current[index]) wakeCat(index, 180);
        else if (pendingReaction.current[index] && impact > 700) {
          pendingReaction.current[index] = false;
          void catRefs.current[index]?.play("double-take");
        }
      }
      if (!isBodyAtRest(next, now)) busy = true;
      return impact > 260 ? kickSquash(next, impact) : next;
    });
    paint();
    frame.current = busy ? requestAnimationFrame(tick) : 0;
    if (!busy) lastTick.current = 0;
  };

  const run = () => {
    if (!frame.current) frame.current = requestAnimationFrame(tick);
  };

  // Lay out (and re-lay out on resize: blocks spring to their new homes).
  useEffect(() => {
    const bench = benchRef.current;
    if (!bench) return;
    let introPlayed = false;
    const place = () => {
      const width = bench.clientWidth;
      const size = blockSize(width);
      const slots = benchSlots(width, CREW.length, size);
      const height = benchHeight(slots, size);
      sizeRef.current = size;
      setLayout((current) => (current?.size === size && current.height === height ? current : { size, height }));
      if (!introPlayed) {
        introPlayed = true;
        const now = performance.now();
        const reduced = prefersReducedMotion();
        bodies.current = slots.map((slot, index) => (reduced
          ? restingBody(slot)
          : droppingBody(slot, height + 260 + index * 30, now + 260 + index * 120, (index % 2 ? 1 : -1) * (14 + index * 3))));
        if (reduced) CREW.forEach((_, index) => wakeCat(index, 0));
      } else {
        bodies.current = bodies.current.map((body, index) => ({ ...body, home: slots[index] }));
      }
      paint();
      run();
    };
    const observer = new ResizeObserver(place);
    observer.observe(bench);
    return () => {
      observer.disconnect();
      cancelAnimationFrame(frame.current);
      frame.current = 0;
    };
    // The frame loop reads the latest layout through refs; mount once.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const benchPoint = (event: ReactPointerEvent) => {
    const rect = benchRef.current!.getBoundingClientRect();
    return { x: event.clientX - rect.left, y: event.clientY - rect.top };
  };

  const onPointerDown = (index: number) => (event: ReactPointerEvent<HTMLButtonElement>) => {
    if (event.button !== 0 || !bodies.current[index]) return;
    const point = benchPoint(event);
    const body = bodies.current[index];
    grabs.current[index] = {
      pointerId: event.pointerId,
      offsetX: point.x - body.x.position,
      offsetY: point.y - body.y.position,
      startX: point.x,
      startY: point.y,
      lastX: point.x,
      lastY: point.y,
      lastTime: event.timeStamp,
      vx: 0,
      vy: 0,
      moved: false,
    };
    // The block in hand always rides above the others, and stays there once dropped.
    zTop.current += 1;
    event.currentTarget.style.zIndex = String(zTop.current);
    try {
      event.currentTarget.setPointerCapture(event.pointerId);
    } catch {
      // Capture is an enhancement; moves over the block still drive the drag.
    }
  };

  const onPointerMove = (index: number) => (event: ReactPointerEvent<HTMLButtonElement>) => {
    const grab = grabs.current[index];
    if (!grab || grab.pointerId !== event.pointerId) return;
    const point = benchPoint(event);
    const elapsed = Math.max(8, event.timeStamp - grab.lastTime) / 1000;
    const vx = grab.vx * 0.55 + ((point.x - grab.lastX) / elapsed) * 0.45;
    const vy = grab.vy * 0.55 + ((point.y - grab.lastY) / elapsed) * 0.45;
    const moved = grab.moved || Math.hypot(point.x - grab.startX, point.y - grab.startY) > 5;
    grabs.current[index] = { ...grab, lastX: point.x, lastY: point.y, lastTime: event.timeStamp, vx, vy, moved };
    if (!moved) return;
    const body = bodies.current[index];
    bodies.current[index] = {
      ...body,
      dragging: true,
      falling: false,
      waitingUntil: 0,
      dragTilt: Math.max(-16, Math.min(16, vx * 0.012)),
      x: { position: point.x - grab.offsetX, velocity: vx },
      y: { position: point.y - grab.offsetY, velocity: vy },
    };
    run();
  };

  const release = (index: number, cancelled: boolean) => (event: ReactPointerEvent<HTMLButtonElement>) => {
    const grab = grabs.current[index];
    if (!grab || grab.pointerId !== event.pointerId) return;
    grabs.current[index] = null;
    const body = bodies.current[index];
    if (!grab.moved) {
      if (!cancelled) poke(index);
      return;
    }
    const clamp = (value: number) => Math.max(-MAX_THROW, Math.min(MAX_THROW, value));
    bodies.current[index] = {
      ...body,
      dragging: false,
      x: { position: body.x.position, velocity: clamp(grab.vx) },
      y: { position: body.y.position, velocity: clamp(grab.vy) },
    };
    pendingReaction.current[index] = true;
    run();
  };

  const poke = (index: number) => {
    const body = bodies.current[index];
    if (!body) return;
    bodies.current[index] = kickSquash({ ...body, y: { position: body.y.position, velocity: prefersReducedMotion() ? 0 : -520 } }, 900);
    run();
    void catRefs.current[index]?.play("happy");
  };

  const onHover = (index: number) => (event: ReactPointerEvent) => {
    if (event.pointerType !== "mouse" || grabs.current[index]) return;
    const now = performance.now();
    if (now - lastHover.current[index] < HOVER_COOLDOWN || !awake.current[index]) return;
    lastHover.current[index] = now;
    void catRefs.current[index]?.play("curious");
  };

  return (
    <div
      className="bench"
      ref={benchRef}
      style={layout ? { height: layout.height, "--block": `${layout.size}px` } as CSSProperties : undefined}
    >
      {CREW.map((member, index) => (
        <button
          key={member.id}
          type="button"
          className={`toy-block tone-${member.tone}`}
          ref={(element) => { blockRefs.current[index] = element; }}
          aria-label={`${member.name}，${member.trait}。按一下它会开心`}
          onPointerDown={onPointerDown(index)}
          onPointerMove={onPointerMove(index)}
          onPointerUp={release(index, false)}
          onPointerCancel={release(index, true)}
          onPointerEnter={onHover(index)}
          onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); poke(index); } }}
        >
          <span className="toy-name">{member.name}</span>
          <CatStage config={member.cat} initialState="asleep" ref={(handle) => { catRefs.current[index] = handle; }} />
        </button>
      ))}
    </div>
  );
}
