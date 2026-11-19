// The header's two menus, in a chunk of their own: they are drawn only when open, and Workspace loads this after first paint
// (the way the sidebar menus load), so the entry carries none of their markup.
import { OverlayMenu } from "../overlays/OverlayHost";
import type { TFunction } from "i18next";
import type { RefObject } from "react";
import type { Conversation, Run, ToolActivity } from "../types";
import type { BotAvatarState } from "../ds/BotAvatar/BotAvatar";

export interface ComputerEntry {
  key: string;
  label: string;
  icon: "monitor" | "globe";
  pressed: boolean;
  on?: boolean;
  driven?: boolean;
  /** data-* hooks of this source's toggle (scripts and the computer state read them). */
  attrs: Record<string, string>;
  toggle: () => void;
}

export interface MoreMenuProps {
  t: TFunction<readonly ["chat", "common"], undefined>;
  active: Conversation;
  trigger: RefObject<HTMLButtonElement | null>;
  setOpen: (open: boolean) => void;
  columnMode: boolean;
  hasComputer: boolean;
  tasks: () => void;
  members: () => void;
  terminal: () => void;
  memory: () => void;
  usage: () => void;
  approvals: () => void;
  copyLink: () => void;
  settings: () => void;
  remove: () => void;
}

export function MoreMenu({ t, active, trigger, setOpen, hasComputer, tasks, members, terminal, memory, usage, approvals, copyLink, settings, remove }: MoreMenuProps) {
  const pick = (action: () => void) => () => { trigger.current?.focus(); setOpen(false); action(); };
  const group = active.kind === "group";
  return <OverlayMenu open onOpenChange={setOpen} id="conversation-more-menu" className="more-menu" label={t("header.more")} trigger={trigger} align="end" sideOffset={6}>
    <button onClick={pick(tasks)}>{t("header.tasks")}</button>
    {group && <button onClick={pick(members)}>{t("header.open_members")}</button>}
    {hasComputer && <button onClick={pick(terminal)}>{t("header.terminal")}</button>}
    <button onClick={pick(memory)}>{group ? t("header.group_memory") : t("header.bot_memory")}</button>
    <button onClick={pick(usage)}>{t("header.usage")}</button>
    <button onClick={pick(approvals)}>{t("header.remembered_approvals")}</button>
    <button onClick={pick(copyLink)}>{t("header.copy_link")}</button>
    <button onClick={() => { trigger.current?.focus(); settings(); setOpen(false); }}>{group ? t("header.group_settings") : t("header.bot_settings")}</button>
    <button className="delete-menu-action" onClick={pick(remove)}>{group ? t("header.delete_group") : t("header.delete_bot")}</button>
  </OverlayMenu>;
}

export function SourceMenu({ entries, label, trigger, setOpen }: { entries: ComputerEntry[]; label: string; trigger: RefObject<HTMLButtonElement | null>; setOpen: (open: boolean) => void }) {
  return <OverlayMenu open onOpenChange={setOpen} id="computer-source-menu" className="more-menu source-menu" label={label} trigger={trigger} align="end" sideOffset={6}>
    {entries.map(entry => <button key={entry.key} {...entry.attrs} onClick={() => { trigger.current?.focus(); setOpen(false); entry.toggle(); }}>{entry.label}</button>)}
  </OverlayMenu>;
}

/** What the header cat shows for one Bot in one conversation. A pending question for the person beats work, work beats a failed last run,
 *  and a Bot that has been quiet for a while is asleep. Nothing: awake and idle. */
export function botCatState(botId: string, conversationId: string, runs: Run[], activities: ToolActivity[], asleep: boolean, asked: ReadonlySet<string>): BotAvatarState | undefined {
  const mine = (run: Run) => run.bot_id === botId && run.conversation_id === conversationId;
  // `waiting` is also a run that parks on a delegated child: only a question pending for the person (`asked`: run ids) is 待确认.
  const parked = runs.filter(run => mine(run) && run.status === "waiting");
  if (parked.some(run => asked.has(run.id))) return "waiting";
  if (parked.length || runs.some(run => mine(run) && (run.status === "running" || run.status === "queued"))) return activities.some(activity => activity.bot_id === botId && activity.conversation_id === conversationId && (activity.status === "running" || activity.status === "queued")) ? "working" : "thinking";
  const last = runs.filter(run => mine(run) && ["done", "failed", "cancelled", "interrupted"].includes(run.status))
    .reduce<Run | undefined>((latest, run) => !latest || run.updated_at >= latest.updated_at ? run : latest, undefined);
  if (last?.status === "failed") return "error";
  return asleep ? "asleep" : undefined;
}
