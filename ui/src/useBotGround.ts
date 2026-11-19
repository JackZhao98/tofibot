import { useEffect, useState } from "react";
import { getBotGround, subscribeBotGround, type Ground } from "./avatarStore";

/** The Bot's ground colour from the avatar store (its own, or the default derived from its id), kept current. */
export function useBotGround(id: string | undefined): Ground | undefined {
  const [ground, setGround] = useState<Ground | undefined>(() => id ? getBotGround(id) : undefined);
  useEffect(() => {
    if (!id) { setGround(undefined); return; }
    setGround(getBotGround(id));
    return subscribeBotGround(id, setGround);
  }, [id]);
  return ground;
}
