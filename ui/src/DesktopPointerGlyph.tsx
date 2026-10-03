import { useEffect, useState, type CSSProperties } from "react";
import { getBotAvatarConfig, subscribeBotAvatar } from "./avatarStore";
import { paletteOptions } from "./lib/tofi-avatar/index.js";
import { TofiIcon } from "./icons";

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
    <TofiIcon className="desktop-pointer-arrow" name="cursor" variant="filled" size={24} />
  </span>;
}
