import { useEffect, useState } from "react";
import { catFraming } from "./lib/tofi-avatar/framing.js";
import type { AvatarConfig } from "./lib/tofi-avatar/index.js";
import { getBotAvatarConfig, subscribeBotAvatar } from "./avatarStore";
import { BotAvatar } from "./BotAvatar";
import { TofiIcon } from "./icons";
import "./group-avatar.css";

/** Head centres and diameters (percent of the square) for 1 to 4 members. Heads overlap; later ones sit on top. */
const LAYOUT: Record<number, { x: number; y: number; d: number }[]> = {
  1: [{ x: 50, y: 50, d: 88 }],
  2: [{ x: 31, y: 50, d: 66 }, { x: 69, y: 50, d: 66 }],
  3: [{ x: 50, y: 30, d: 58 }, { x: 29, y: 70, d: 58 }, { x: 71, y: 70, d: 58 }],
  4: [{ x: 30, y: 30, d: 56 }, { x: 70, y: 30, d: 56 }, { x: 30, y: 70, d: 56 }, { x: 70, y: 70, d: 56 }],
};

function Head({ id, override, spot }: { id: string; override?: AvatarConfig; spot: { x: number; y: number; d: number } }) {
  const [stored, setStored] = useState<AvatarConfig>(() => getBotAvatarConfig(id));
  useEffect(() => { setStored(getBotAvatarConfig(id)); return subscribeBotAvatar(id, setStored); }, [id]);
  const config = override ?? stored;
  const { size, left, top } = catFraming(config.shape);
  return <span className="group-head" style={{ left: `${spot.x - spot.d / 2}%`, top: `${spot.y - spot.d / 2}%`, width: `${spot.d}%`, height: `${spot.d}%` }}>
    <span className="group-head-cat" style={{ width: `${size * 100}%`, height: `${size * 100}%`, left: `${left * 100}%`, top: `${top * 100}%` }}><BotAvatar id={id} mini still config={config} /></span>
  </span>;
}

/**
 * A group's avatar: the face of up to four member Bots gathered together, cropped from each cat's real drawing (catFraming),
 * drawn once and never animated. Members come in membership order. A group with no Bot member keeps the people icon.
 * Fills its square parent; `configs` overrides the stored look per member (catalog and previews).
 */
export default function GroupAvatar({ ids, configs, iconSize = 19 }: { ids: string[]; configs?: AvatarConfig[]; iconSize?: number }) {
  const members = ids.slice(0, 4);
  if (!members.length) return <TofiIcon name="group" size={iconSize} />;
  const layout = LAYOUT[members.length];
  return <span className="group-heads" data-count={members.length}>
    {members.map((id, index) => <Head key={id} id={id} override={configs?.[index]} spot={layout[index]} />)}
  </span>;
}
