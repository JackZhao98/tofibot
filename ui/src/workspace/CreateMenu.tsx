import type { TFunction } from "i18next";
import type { RefObject } from "react";
import { TofiIcon as Icon } from "../icons";
import { OverlayMenu } from "../overlays/OverlayHost";
import type { CoverId } from "../surfaces";
import { Note } from "../ds/Notice/Notice";

/** The New menu of the sidebar header: a Bot, a group, or an imported Bot. A lazy chunk, preloaded in idle time (the entry bundle has no room for it). */
export default function CreateMenu({ open, onOpenChange, t, triggerRef, anchorRef, placement, busy, importBusy, error, importRef, createNewBot, openCoverPanel }: {
  open: boolean; onOpenChange: (open: boolean) => void; t: TFunction<readonly ["chat", "common"], undefined>;
  triggerRef: RefObject<HTMLButtonElement | null>; anchorRef: RefObject<HTMLDivElement | null>;
  placement: { side?: "right"; align?: "end"; sideOffset: number };
  busy: boolean; importBusy: boolean; error: string; importRef: RefObject<HTMLInputElement | null>;
  createNewBot: () => Promise<void>; openCoverPanel: (id: CoverId) => void;
}) {
  return <OverlayMenu open={open} onOpenChange={onOpenChange} id="create-menu" className="create-menu" label={t("create.menu")} trigger={triggerRef} anchor={anchorRef} {...placement}>
    <button disabled={busy} onClick={() => { triggerRef.current?.focus(); void createNewBot(); }}><Icon name="bot-add" size={18} /><span>{busy ? t("create.creating") : t("create.new_bot")}</span></button>
    <button onClick={() => { triggerRef.current?.focus(); onOpenChange(false); openCoverPanel("new-group"); }}><Icon name="group-add" size={18} /><span>{t("create.new_group")}</span></button>
    <button disabled={importBusy} onClick={() => { onOpenChange(false); importRef.current?.click(); }}><Icon name="upload" size={18} /><span>{importBusy ? t("create.importing") : t("create.import_bot")}</span></button>
    {error && <Note tone="error" role="alert">{error}</Note>}
  </OverlayMenu>;
}
