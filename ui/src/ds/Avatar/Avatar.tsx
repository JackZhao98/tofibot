import { useTranslation } from "../../i18n";
import { cx } from "../cx";
import "./Avatar.css";

export type AvatarTone = "mustard" | "teal" | "grape" | "cobalt" | "lime" | "tomato";
export type Presence = "online" | "away" | "offline";

export type AvatarProps = {
  /** Initials come from it, and it is the accessible name. */
  name: string;
  /** A photo, in place of initials. */
  src?: string;
  size?: number;
  tone?: AvatarTone;
  status?: Presence;
  className?: string;
};

function initials(name: string): string {
  const text = String(name || "?").trim();
  const parts = text.split(/\s+/);
  return (parts.length > 1 ? parts[0][0] + parts[1][0] : text.slice(0, 2)).toUpperCase();
}

/** A person: a disc with initials (or a photo) and an optional presence dot. Cats are Bots; use BotAvatar. */
export function Avatar({ name, src, size = 34, tone = "mustard", status, className }: AvatarProps) {
  const { t } = useTranslation();
  return <span className={cx("tb-avatar", `tb-avatar--${tone}`, className)} style={{ width: size, height: size, fontSize: Math.round(size * 0.38) }} role="img" aria-label={status ? `${name}, ${t(`ds.presence.${status}`)}` : name}>
    {src ? <img src={src} alt="" /> : initials(name)}
    {status && <span className={cx("tb-presence", `is-${status}`)} aria-hidden="true" />}
  </span>;
}
