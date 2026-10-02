import { useEffect, useRef, useState } from "react";
import { mountCat, type AvatarConfig, type CatAvatar } from "./lib/tofi-avatar/index.js";
import { getBotAvatarConfig, subscribeBotAvatar } from "./avatarStore";
import { directAvatarMotion, type AvatarMotion } from "./avatarMotionDirector";

export type { AvatarMotion } from "./avatarMotionDirector";

export function GazeAvatar({ id, mini = false, animated = false, motion, config }: { id: string; mini?: boolean; animated?: boolean; motion?: AvatarMotion; config?: AvatarConfig }) {
  const host = useRef<HTMLSpanElement | null>(null);
  const controller = useRef<CatAvatar | null>(null);
  const previousMotion = useRef<AvatarMotion>("sleeping");
  const [storedConfig, setStoredConfig] = useState<AvatarConfig>(() => getBotAvatarConfig(id));
  const appearance = config ?? storedConfig;
  const effectiveMotion = motion ?? (animated ? "working" : "sleeping");
  useEffect(() => {
    setStoredConfig(getBotAvatarConfig(id));
    return subscribeBotAvatar(id, setStoredConfig);
  }, [id]);
  useEffect(() => {
    if (!host.current) return;
    const cat = mountCat(host.current, { ...appearance, initialState: "asleep" });
    controller.current = cat;
    return () => { cat.destroy(); controller.current = null; };
    // A config update is applied by the effect below so the SVG keeps its current pose.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id]);
  useEffect(() => { controller.current?.setAppearance(appearance); }, [appearance]);
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
  }, [id, appearance.shape, effectiveMotion, motion, mini]);
  return <span className={`avatar ${mini ? "mini" : ""}`} aria-hidden="true"><span className="tofi-avatar" ref={host} /></span>;
}
