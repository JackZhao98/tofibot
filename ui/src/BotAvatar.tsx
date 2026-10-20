import { useEffect, useRef, useState } from "react";
import { mountCat, type AvatarConfig, type CatAvatar } from "./lib/tofi-avatar/index.js";
import { getBotAvatarConfig, subscribeBotAvatar } from "./avatarStore";
import { directAvatarMotion, type AvatarMotion } from "./avatarMotionDirector";

export type { AvatarMotion } from "./avatarMotionDirector";

/** One observer for every small avatar: a cat is built only once its slot is near the viewport, so opening a long group mounts a screenful, not the history. */
let nearObserver: IntersectionObserver | undefined;
const nearCallbacks = new WeakMap<Element, () => void>();
function whenNearViewport(element: Element, callback: () => void) {
  if (typeof IntersectionObserver === "undefined") { callback(); return () => {}; }
  nearObserver ??= new IntersectionObserver(entries => {
    for (const entry of entries) {
      if (!entry.isIntersecting) continue;
      nearObserver?.unobserve(entry.target);
      const run = nearCallbacks.get(entry.target);
      nearCallbacks.delete(entry.target);
      run?.();
    }
  }, { rootMargin: "240px 0px" });
  nearCallbacks.set(element, callback);
  nearObserver.observe(element);
  return () => { nearCallbacks.delete(element); nearObserver?.unobserve(element); };
}

export function BotAvatar({ id, mini = false, animated = false, motion, config }: { id: string; mini?: boolean; animated?: boolean; motion?: AvatarMotion; config?: AvatarConfig }) {
  const host = useRef<HTMLSpanElement | null>(null);
  const controller = useRef<CatAvatar | null>(null);
  const previousMotion = useRef<AvatarMotion>("sleeping");
  const mountedAppearance = useRef<AvatarConfig | null>(null);
  const [near, setNear] = useState(!mini);
  const [storedConfig, setStoredConfig] = useState<AvatarConfig>(() => getBotAvatarConfig(id));
  const appearance = config ?? storedConfig;
  const effectiveMotion = motion ?? (animated ? "working" : "sleeping");
  useEffect(() => {
    setStoredConfig(getBotAvatarConfig(id));
    return subscribeBotAvatar(id, setStoredConfig);
  }, [id]);
  useEffect(() => {
    if (!mini || near || !host.current) return;
    return whenNearViewport(host.current, () => setNear(true));
  }, [mini, near]);
  useEffect(() => {
    if (!host.current || !near) return;
    const cat = mountCat(host.current, { ...appearance, initialState: "asleep" });
    mountedAppearance.current = appearance;
    controller.current = cat;
    return () => { cat.destroy(); controller.current = null; };
    // A config update is applied by the effect below so the SVG keeps its current pose.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, near]);
  useEffect(() => {
    // The cat was just built from this very config; re-applying it rebuilds the SVG for nothing.
    if (mountedAppearance.current === appearance) return;
    mountedAppearance.current = appearance;
    controller.current?.setAppearance(appearance);
  }, [appearance]);
  useEffect(() => {
    const cat = controller.current;
    if (!cat) return;
    let cancelled = false;
    let timer: number | undefined;
    let finishPause: (() => void) | undefined;
    const pause = (ms: number) => new Promise<void>((resolve) => {
      if (cancelled) { resolve(); return; }
      finishPause = resolve;
      timer = window.setTimeout(() => { timer = undefined; finishPause = undefined; resolve(); }, ms);
    });
    const from = previousMotion.current;
    previousMotion.current = effectiveMotion;
    // Tiny message/mention avatars keep breathing but do not each start their
    // own random gesture director. The sidebar, work area and preview opt in.
    const allowAutoplay = motion !== undefined && !mini;
    void directAvatarMotion(cat, from, effectiveMotion, () => !cancelled,
      pause, Math.random, allowAutoplay);
    return () => {
      cancelled = true;
      window.clearTimeout(timer);
      finishPause?.();
      if (controller.current === cat) cat.setAutoplay(false);
    };
  }, [id, appearance.shape, effectiveMotion, motion, mini, near]);
  return <span className={`avatar ${mini ? "mini" : ""}`} aria-hidden="true"><span className="tofi-avatar" ref={host} /></span>;
}
