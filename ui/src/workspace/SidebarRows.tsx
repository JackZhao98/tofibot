import type { ComponentProps } from "react";
import { Avatar, type AvatarTone } from "../ds/Avatar/Avatar";
import type { BadgeTone } from "../ds/Badge/Badge";
import { BotAvatar } from "../ds/BotAvatar/BotAvatar";
import type { SidebarRow } from "../ds/SidebarRow/SidebarRow";
import type { RowIndicatorKind } from "../rowIndicator";

/** The five Bot hues; blue is the person's colour (S4), so a group never takes it. A group keeps one tone for as long as it lives: the tone is read from its id. */
const GROUP_TONES: AvatarTone[] = ["mustard", "teal", "grape", "lime", "tomato"];
const STAMP: Partial<Record<RowIndicatorKind, BadgeTone>> = { attention: "needs", failed: "error" };

/** The lead of a conversation row: the Bot's squircle (a dot while it works), or the group's initials. */
export function RowLead({ name, botId, group, working, size = 36 }: { name: string; botId: string; group: boolean; working: boolean; size?: number }) {
  return group
    ? <Avatar name={name} tone={GROUP_TONES[botId.charCodeAt(botId.length - 1) % 5]} size={size} working={working} />
    : <BotAvatar id={botId} name={name} size={size} state={working ? "working" : undefined} />;
}

/** The row's one status slot (priority rule in rowIndicator.ts). An unread count is a count; waiting and failed are a stamp;
 *  working is the dot on the avatar, not a second mark on the row. */
export function rowStatus(indicator: RowIndicatorKind | null, unread: number): Pick<ComponentProps<typeof SidebarRow>, "count" | "status"> {
  return indicator === "unread" ? { count: Math.min(unread, 99) } : { status: indicator ? STAMP[indicator] : undefined };
}
