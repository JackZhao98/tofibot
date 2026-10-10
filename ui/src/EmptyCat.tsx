import { useEffect, useRef, useState, type CSSProperties, type ReactNode, type Ref } from "react";
import { CatStage, type CatHandle } from "./CatStage";
import type { AvatarConfig } from "./lib/tofi-avatar/index.js";
import "./empty-states.css";

/** Cats on screen at once. Beyond this the extra empty states keep their space and draw no cat. */
export const MAX_LIVE_CATS = 6;

const live = new Set<object>();
const waiting = new Set<() => void>();
const wake = () => waiting.forEach(fn => fn());

/**
 * A cat is mounted only while its box is (nearly) on screen and a slot is free; scrolling away or
 * unmounting destroys it, so a screen never runs more than MAX_LIVE_CATS animation loops.
 */
function useLiveSlot(box: { current: HTMLElement | null }) {
  const [on, setOn] = useState(false);
  useEffect(() => {
    const el = box.current;
    if (!el) return;
    const token = {};
    let visible = false;
    const sync = () => {
      if (visible) {
        if (!live.has(token) && live.size < MAX_LIVE_CATS) { live.add(token); setOn(true); }
      } else if (live.delete(token)) { setOn(false); wake(); }
    };
    waiting.add(sync);
    const observer = typeof IntersectionObserver === "function"
      ? new IntersectionObserver(entries => { visible = entries[entries.length - 1].isIntersecting; sync(); }, { rootMargin: "80px" })
      : null;
    if (observer) observer.observe(el); else { visible = true; sync(); }
    return () => { observer?.disconnect(); waiting.delete(sync); live.delete(token); wake(); };
  }, [box]);
  return on;
}

export type CatLook = Pick<AvatarConfig, "shape" | "pattern" | "palette">;
/** asleep: nothing to do. awake: idle and breathing. curious: waiting for input, looks around once on arrival. */
export type CatPose = "asleep" | "awake" | "curious";

/** Named looks, so every empty state in a given place wears the same cat. */
export const CATS = {
  mochi: { shape: "loaf", pattern: "solid", palette: "ivory" },
  miso: { shape: "curl", pattern: "calico", palette: "calico" },
  nori: { shape: "tall", pattern: "split", palette: "slate" },
  yuzu: { shape: "stand", pattern: "stripes", palette: "honey" },
  azuki: { shape: "bean", pattern: "tortie", palette: "caramel" },
  taro: { shape: "rice", pattern: "cloud", palette: "iris" },
  kiwi: { shape: "puddle", pattern: "solid", palette: "moss" },
} as const satisfies Record<string, CatLook>;

type EmptyCatProps = {
  look: CatLook;
  pose?: CatPose;
  /** Square edge in px. */
  size?: number;
  className?: string;
  /** Identifies this cat for tests and styling. */
  name?: string;
  ref?: Ref<CatHandle>;
};

/** One small live cat for an empty or first-use state. Decorative: the state's text carries the meaning. */
export function EmptyCat({ look, pose = "asleep", size, className, name, ref }: EmptyCatProps) {
  const box = useRef<HTMLSpanElement>(null);
  const handle = useRef<CatHandle>(null);
  const on = useLiveSlot(box);
  useEffect(() => {
    if (!on || pose !== "curious") return;
    const timer = window.setTimeout(() => void handle.current?.play("curious"), 600);
    return () => window.clearTimeout(timer);
  }, [on, pose]);
  const setRefs = (value: CatHandle | null) => {
    handle.current = value;
    if (typeof ref === "function") ref(value); else if (ref) (ref as { current: CatHandle | null }).current = value;
  };
  return (
    <span className={`empty-cat${className ? ` ${className}` : ""}`} style={size ? { "--cat": `${size}px` } as CSSProperties : undefined} ref={box}
      data-empty-cat={name ?? ""} data-pose={pose} data-live={on ? "true" : "false"} aria-hidden="true">
      {on && <CatStage config={look} initialState={pose === "asleep" ? "asleep" : "awake"} autoplay={pose !== "asleep"} ref={setRefs} />}
      {pose === "asleep" && <span className="empty-cat-zzz"><i>z</i><i>z</i></span>}
    </span>
  );
}

type EmptyStateProps = {
  look: CatLook;
  pose?: CatPose;
  name: string;
  title?: ReactNode;
  children?: ReactNode;
  className?: string;
  role?: "status" | "alert";
  size?: number;
};

/** The shared shape of an empty state: a small cat, a heading, optional supporting text and actions. */
export function EmptyState({ look, pose = "asleep", name, title, children, className, role, size }: EmptyStateProps) {
  return (
    <div className={`empty-state${className ? ` ${className}` : ""}`} role={role} data-empty-state={name}>
      <EmptyCat look={look} pose={pose} size={size} name={name} />
      {title && <strong className="empty-state-title">{title}</strong>}
      {children}
    </div>
  );
}
