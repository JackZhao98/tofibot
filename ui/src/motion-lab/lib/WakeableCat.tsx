import { useRef, useState, type CSSProperties } from "react";
import type { AvatarConfig } from "../../lib/tofi-avatar/index.js";
import { CatStage, type CatHandle } from "./CatStage";
import "./primitives.css";

type WakeableCatProps = { config: Partial<AvatarConfig>; name: string; size?: number };

/** Empty-state companion: a cat asleep on the dot grid; a tap wakes it, another says hi. */
export function WakeableCat({ config, name, size = 96 }: WakeableCatProps) {
  const cat = useRef<CatHandle>(null);
  const [awake, setAwake] = useState(false);
  const poke = async () => {
    if (!awake) {
      setAwake(true);
      if (await cat.current?.play("wake")) cat.current?.setAutoplay(true);
      return;
    }
    void cat.current?.play("happy");
  };
  return (
    <button
      type="button"
      className={`wakeable-cat${awake ? " is-awake" : ""}`}
      style={{ "--cat": `${size}px` } as CSSProperties}
      aria-label={awake ? `${name} 醒着，再点一下打招呼` : `叫醒 ${name}`}
      onClick={() => void poke()}
    >
      <CatStage config={config} initialState="asleep" ref={cat} />
      {!awake && <span className="wakeable-zzz" aria-hidden="true"><i>z</i><i>z</i></span>}
    </button>
  );
}
