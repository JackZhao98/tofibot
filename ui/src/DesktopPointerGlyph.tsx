import { useEffect, useState, type CSSProperties } from "react";
import { getBotAvatarConfig, subscribeBotAvatar } from "./avatarStore";
import { paletteOptions } from "./lib/tofi-avatar/index.js";

/** Neutral pointer silhouette; only its small glow carries the Bot's coat color. */
export function DesktopPointerGlyph({ botId }: { botId: string }) {
  const [coat, setCoat] = useState(() => getBotAvatarConfig(botId));
  useEffect(() => {
    setCoat(getBotAvatarConfig(botId));
    return subscribeBotAvatar(botId, setCoat);
  }, [botId]);
  const palette = paletteOptions(coat.pattern).find(option => option.id === coat.palette)!;
  const color = palette.white ? palette.accent : palette.body;
  return <span className="desktop-pointer-glyph" style={{ "--pointer-coat":color } as CSSProperties}>
    <svg className="desktop-pointer-arrow" viewBox="0 0 28 28"><path d="M4 3 24 11.5 15 14.5 11.5 24Z" /></svg>
  </span>;
}
