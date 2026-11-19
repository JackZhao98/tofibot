import { useTranslation } from "../../i18n";
import { BotAvatar as BotCat } from "../../BotAvatar";
import type { Ground } from "../../avatarStore";
import { useBotGround } from "../../useBotGround";
import { Icon } from "../Icon/Icon";
import { cx } from "../cx";
import "../bot-context.css";
import "./BotAvatar.css";

export type BotCoat = Ground | "paper";
export type BotAvatarState = "thinking" | "working" | "waiting" | "error" | "asleep";

export type BotAvatarProps = {
  /** The Bot's id. The ground comes from the avatar store (the Bot's `ground`, or the default for its id) and the cat is
   *  drawn from its stored look. */
  id?: string;
  /** The accessible name. */
  name: string;
  /** A fixed ground, instead of the Bot's own. `paper` is the plain surface. */
  coat?: BotCoat;
  size?: number;
  /** thinking: teal ring. working: teal dot. waiting: grape dot (needs you). error: red dot. asleep: dimmed, no dot. */
  state?: BotAvatarState;
  /** An image of the cat, in place of the procedural one. */
  src?: string;
  className?: string;
};

/** A Bot: a squircle in its colour holding the cat, with a small status dot. Still everywhere except the chat header
 *  (V3 PLAN 8.2): the cat here is drawn once, with no life layer. */
export function BotAvatar({ id, name, coat, size = 40, state, src, className }: BotAvatarProps) {
  const { t } = useTranslation();
  const stored = useBotGround(id);
  const ground = coat ?? stored ?? "tomato";
  const showDot = state && state !== "asleep";
  return <span className={cx("tb-botavatar", ground !== "paper" && `tb-bot--${ground}`, ground === "paper" && "tb-botavatar--paper", state && `is-${state}`, className)}
    style={{ width: size, height: size }} role="img" aria-label={state ? `${name}, ${t(`ds.botState.${state}`)}` : name}>
    {src ? <img src={src} alt="" />
      : id ? <span className="tb-botavatar-cat"><BotCat id={id} mini still /></span>
      : <Icon name="bot" size={Math.round(size * 0.55)} />}
    {showDot && <span className="tb-botavatar-dot" aria-hidden="true" />}
  </span>;
}
