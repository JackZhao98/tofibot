import { useEffect, useRef, useState } from "react";
import { mountCat, type AvatarConfig, type CatAvatar } from "./lib/tofi-avatar/index.js";
import { getBotAvatarConfig, subscribeBotAvatar } from "./avatarStore";
import { useBotGround } from "./useBotGround";
import "./ds/bot-context.css";
import "./ds/BotAvatar/BotAvatar.css";

/** A cat is drawn once, awake or asleep. Nothing here moves: the header cat moves with CSS (ds/ChatHeader). */
export type AvatarMotion = "sleeping" | "awake" | "working";

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

export function BotAvatar({ id, mini = false, motion, config, still = false, bare = false, plain = false }: { id: string; /** The cat in its V2 box with no tile: the working cats by the composer, until PR 4.1 removes them. */ plain?: boolean; /** Only the cat, with no tile: the caller (the kit BotAvatar) draws the ground tile. */ bare?: boolean; mini?: boolean; motion?: AvatarMotion; config?: AvatarConfig; /** Awake, drawn once: no life layer, no motion director (group avatars). */ still?: boolean }) {
  const host = useRef<HTMLSpanElement | null>(null);
  const controller = useRef<CatAvatar | null>(null);
    const mountedAppearance = useRef<AvatarConfig | null>(null);
  const [near, setNear] = useState(!mini);
  const [storedConfig, setStoredConfig] = useState<AvatarConfig>(() => getBotAvatarConfig(id));
  const appearance = config ?? storedConfig;
  const ground = useBotGround(id) ?? "tomato";
  const effectiveMotion = motion ?? "sleeping";
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
    const cat = mountCat(host.current, { ...appearance, initialState: effectiveMotion === "sleeping" && !still ? "asleep" : "awake", life: false });
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
    // A Bot that falls asleep or wakes changes its resting pose in place.
    if (still) return;
    controller.current?.pose(effectiveMotion === "sleeping" ? { o: 0, k: 1 } : { o: 1, k: 0 });
  }, [effectiveMotion, still, near]);
  if (bare) return <span className="tofi-avatar" ref={host} aria-hidden="true" />;
  return <span className={`avatar ${plain ? "" : `tb-botavatar tb-bot--${ground} `}${mini ? "mini" : ""}`} aria-hidden="true"><span className="tofi-avatar" ref={host} /></span>;
}
