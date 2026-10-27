import { BotAvatar } from "./BotAvatar";
import { useTranslation } from "./i18n";
import { TofiIcon as Icon } from "./icons";
import { PrimaryBadge } from "./PrimaryAssistant";
import { MemberTask } from "./TaskCard";
import type { TaskCardModel } from "./taskCards";
import type { Bot, Conversation } from "./types";
import "./members-widget.css";

/**
 * The Members widget (docs/design/widgets-and-sidebar.md section 2.1): who is in this group and what each one is working on. Read-only;
 * a row opens that Bot's direct chat, the gear opens the Group settings cover. Lazy chunk `membersWidget`.
 */
export function MembersWidget({ conversation, bots, tasksByBot, onOpen, onSettings }: {
  conversation: Conversation;
  bots: Bot[];
  /** The open tasks each member holds (decision 0060); a claimed task shows as claimed. */
  tasksByBot?: ReadonlyMap<string, TaskCardModel[]>;
  onOpen: (conversationId: string) => void;
  onSettings: () => void;
}) {
  const { t } = useTranslation("chat");
  const byId = new Map(bots.map(bot => [bot.id, bot]));
  const members = conversation.bot_ids.map(id => byId.get(id)).filter((bot): bot is Bot => Boolean(bot));
  return <div className="members-widget">
    <div className="mw-head">
      <span>{t("header.member_count", { count: members.length })}</span>
      <button type="button" className="mw-gear" aria-label={t("header.group_settings")} data-hint={t("header.group_settings")} onClick={onSettings}><Icon name="settings" size={16} /></button>
    </div>
    <ul className="mw-list">
      {members.map(bot => {
        const tasks = tasksByBot?.get(bot.id)?.slice(0, 3) ?? [];
        return <li key={bot.id} className="mw-member">
          <button type="button" className="mw-open" aria-label={t("members.open_dm", { name: bot.name })} data-hint={t("members.open_dm", { name: bot.name })} onClick={() => onOpen(bot.dm_conversation_id)}>
            <span className="mw-avatar" aria-hidden="true"><BotAvatar id={bot.id} mini /></span>
            <span className="mw-name">{bot.name}</span>
            {bot.is_primary && <PrimaryBadge />}
          </button>
          {tasks.length
            ? <ul className="member-tasks mw-tasks" aria-label={t("members.tasks_of", { name: bot.name })}>{tasks.map(card => <MemberTask key={card.id} card={card} />)}</ul>
            : <p className="mw-idle">{t("membersWidget.idle")}</p>}
        </li>;
      })}
    </ul>
  </div>;
}
