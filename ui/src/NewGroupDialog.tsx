import { Button } from "./ds/Button/Button";
import { useRef, useState } from "react";
import { BotAvatar } from "./BotAvatar";
import { useCover } from "./coverGuard";
import { errorText } from "./errorText";
import { useTranslation } from "./i18n";
import { Dialog, DIALOG_SCRIM_DOCK } from "./ds/Dialog/Dialog";
import { ModalDialog } from "./overlays/ModalDialog";
import { PrimaryBadge } from "./PrimaryAssistant";
import type { Bot } from "./types";
import "./group-settings.css";
import "./new-group.css";

/**
 * New group (docs/design/widgets-and-sidebar.md section 5.1, last row): a name and 2 to 8 members, in a dialog of its own. It used to be
 * the other half of the Bot panel. It is the cover `new-group` of the surface state, so the sidebar's + closes it the same way (enter
 * and leave through CoverShell's state), but it draws a dialog, not the cover window. Lazy chunk `newGroup`.
 */
export function NewGroupDialog({ bots, onCreate }: { bots: Bot[]; onCreate: (name: string, botIds: string[]) => Promise<void> }) {
  const { t } = useTranslation("chat");
  const { leaving, close, returnFocus } = useCover();
  const [name, setName] = useState("");
  const [members, setMembers] = useState<string[]>([]);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");
  const nameInput = useRef<HTMLInputElement>(null);
  const valid = name.trim().length > 0 && members.length >= 2 && members.length <= 8;
  const toggle = (id: string) => setMembers(current => current.includes(id) ? current.filter(item => item !== id) : current.length >= 8 ? current : [...current, id]);
  async function submit(event: React.FormEvent) {
    event.preventDefault();
    if (!valid || saving) return;
    setSaving(true); setError("");
    try { await onCreate(name.trim(), members); }
    catch (cause) { setError(errorText(cause)); }
    finally { setSaving(false); }
  }
  return <ModalDialog onClose={close} locked={leaving || saving} initialFocus={nameInput} returnFocus={returnFocus} overlayClassName={`${DIALOG_SCRIM_DOCK} new-group-scrim${leaving ? " is-leaving" : ""}`} overlayProps={{ "data-cover": "new-group" }}>
    <Dialog dock className={`new-group-dialog${leaving ? " is-leaving" : ""}`} title={t("create.new_group")} aria-busy={saving}
      actions={<>
        <Button className="new-group-cancel" disabled={saving} onClick={close}>{t("deleteDialog.cancel")}</Button>
        <Button type="submit" form="new-group-form" variant="primary" className="new-group-create" disabled={!valid || saving}>{saving ? t("action.saving") : t("groupCreate.create")}</Button>
      </>}>
      <form id="new-group-form" onSubmit={event => void submit(event)}>
        <label className="gs-field"><span>{t("members.name")}</span>
          <input ref={nameInput} value={name} maxLength={200} disabled={saving} placeholder={t("members.name")} onChange={event => setName(event.target.value)} />
        </label>
        <div className="gs-block">
          <div className="gs-block-head"><h3>{t("members.members")}</h3><span className="gs-count">{members.length}/8</span></div>
          <div className="gs-members" role="group" aria-label={t("members.pick")}>
            {bots.map(bot => <label key={bot.id} className="gs-member" data-selected={members.includes(bot.id) || undefined}>
              <input type="checkbox" checked={members.includes(bot.id)} disabled={saving || (!members.includes(bot.id) && members.length >= 8)} onChange={() => toggle(bot.id)} />
              <span className="gs-member-avatar tb-cat-only" aria-hidden="true"><BotAvatar id={bot.id} mini /></span>
              <span className="gs-member-name">{bot.name}</span>
              {bot.is_primary && <PrimaryBadge />}
            </label>)}
          </div>
          <p className="gs-hint">{t("members.pick_hint")}</p>
        </div>
        {error && <p className="new-group-error" role="alert">{error}</p>}
      </form>
    </Dialog>
  </ModalDialog>;
}
