import { BotAvatar } from "./BotAvatar";
import { Checkbox } from "./ds/Checkbox/Checkbox";
import { PrimaryBadge } from "./PrimaryAssistant";
import type { Bot } from "./types";
import "./group-settings.css";

/** The member list group settings and New group share: one kit Checkbox row per Bot, at most `max` picked. */
export function MemberPicker({ bots, selected, disabled, max = 8, label, onToggle }: {
  bots: readonly Bot[]; selected: readonly string[]; disabled?: boolean; max?: number; label: string; onToggle: (id: string) => void;
}) {
  return <div className="gs-members" role="group" aria-label={label}>
    {bots.map(bot => {
      const on = selected.includes(bot.id);
      return <Checkbox key={bot.id} className="gs-member" checked={on} disabled={disabled || (!on && selected.length >= max)} onChange={() => onToggle(bot.id)}>
        <span className="gs-member-avatar tb-cat-only" aria-hidden="true"><BotAvatar id={bot.id} mini /></span>
        <span className="gs-member-name">{bot.name}</span>
        {bot.is_primary && <PrimaryBadge />}
      </Checkbox>;
    })}
  </div>;
}
