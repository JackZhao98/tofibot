import { Button } from "./ds/Button/Button";
import { useRef, useState } from "react";
import { Field } from "./ds/Field/Field";
import { TextInput } from "./ds/TextInput/TextInput";
import { Banner } from "./ds/Banner/Banner";
import { MemberPicker } from "./MemberPicker";
import { useCover } from "./coverGuard";
import { errorText } from "./errorText";
import { useTranslation } from "./i18n";
import { Dialog, DIALOG_SCRIM_DOCK } from "./ds/Dialog/Dialog";
import { ModalDialog } from "./overlays/ModalDialog";
import type { Bot } from "./types";
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
        <Field label={t("members.name")}>
          <TextInput ref={nameInput} className="gs-name" value={name} maxLength={200} disabled={saving} placeholder={t("members.name")} onChange={event => setName(event.target.value)} />
        </Field>
        <div className="gs-block">
          <div className="gs-block-head"><span>{t("members.members")}</span><span className="gs-count">{members.length}/8</span></div>
          <MemberPicker bots={bots} selected={members} disabled={saving} label={t("members.pick")} onToggle={toggle} />
          <p className="gs-hint">{t("members.pick_hint")}</p>
        </div>
        {error && <Banner tone="error" className="new-group-error">{error}</Banner>}
      </form>
    </Dialog>
  </ModalDialog>;
}
