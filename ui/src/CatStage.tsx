import { useEffect, useImperativeHandle, useRef, type Ref } from "react";
import { mountCat, normalizeConfig, type Action, type AvatarConfig, type CatAvatar, type Idle } from "./lib/tofi-avatar/index.js";

export type CatHandle = {
  play(action: Action): Promise<boolean>;
  setAutoplay(on: boolean): void;
  get(): CatAvatar | null;
};

type Props = { config: Partial<AvatarConfig>; initialState?: Idle; autoplay?: boolean; className?: string; ref?: Ref<CatHandle> };

/** Shared production cat stage; Lab uses this same mounted avatar. */
export function CatStage({ config, initialState = "awake", autoplay = false, className, ref }: Props) {
  const host = useRef<HTMLSpanElement>(null);
  const cat = useRef<CatAvatar | null>(null);
  const appearance = normalizeConfig(config);
  useEffect(() => {
    if (!host.current) return;
    const mounted = mountCat(host.current, { ...appearance, initialState, autoplay });
    cat.current = mounted;
    return () => { mounted.destroy(); cat.current = null; };
    // Appearance and autoplay update in place below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  useEffect(() => { cat.current?.setAppearance(appearance); }, [appearance.shape, appearance.pattern, appearance.palette]);
  useEffect(() => { cat.current?.setAutoplay(autoplay); }, [autoplay]);
  useImperativeHandle(ref, () => ({
    async play(action) {
      const current = cat.current;
      if (!current || !current.clips().includes(action)) return false;
      try { const result = await current.play(action); return !result.cancelled; } catch { return false; }
    },
    setAutoplay(on) { cat.current?.setAutoplay(on); },
    get: () => cat.current,
  }), []);
  return <span className={className ? `lab-cat ${className}` : "lab-cat"} ref={host} aria-hidden="true" />;
}
