import type { ComponentProps } from "react";
import { Avatar, type AvatarTone } from "../ds/Avatar/Avatar";
import { BotAvatar } from "../ds/BotAvatar/BotAvatar";
import { SidebarRow } from "../ds/SidebarRow/SidebarRow";
import type { RowIndicatorKind } from "../rowIndicator";

/** The five Bot hues; blue is the person's colour (S4), so a group never takes it. */
const GROUP_TONES: AvatarTone[] = ["mustard", "teal", "grape", "lime", "tomato"];

/** A group's initials disc keeps one tone for as long as the group lives: the tone is read from its id. */
export function groupTone(id: string): AvatarTone {
  let hash = 0;
  for (const char of id) hash = (hash * 31 + char.charCodeAt(0)) >>> 0;
  return GROUP_TONES[hash % GROUP_TONES.length];
}

/** The lead of a conversation row: the Bot's squircle (a dot while it works), or the group's initials. */
export function RowLead({ name, botId, group, working, size = 36 }: { name: string; botId: string; group: boolean; working: boolean; size?: number }) {
  return group
    ? <Avatar name={name} tone={groupTone(botId)} size={size} />
    : <BotAvatar id={botId} name={name} size={size} state={working ? "working" : undefined} />;
}

/** The row's one status slot (priority rule in rowIndicator.ts). An unread count is a count; waiting and failed are a stamp;
 *  working is the dot on the avatar, not a second mark on the row. */
export function rowStatus(indicator: RowIndicatorKind | null, unread: number): Pick<ComponentProps<typeof SidebarRow>, "count" | "status"> {
  if (indicator === "unread") return { count: Math.min(unread, 99) };
  if (indicator === "attention") return { status: "needs" };
  if (indicator === "failed") return { status: "error" };
  return {};
}
