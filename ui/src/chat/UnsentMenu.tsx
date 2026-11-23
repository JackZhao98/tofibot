import type { TFunction } from "i18next";
import { TofiIcon as Icon } from "../icons";
import { OverlayMenu } from "../overlays/OverlayHost";

/** The right-click menu of a message that did not go out: Edit and Delete leave its failure line for here (V3 PR 3.3). A lazy chunk: it is drawn only when open. */
export default function UnsentMenu({ x, y, t, onEdit, onDelete, close }: { x: number; y: number; t: TFunction<readonly ["chat", "common"], undefined>; onEdit: () => void; onDelete: () => void; close: () => void }) {
  return <OverlayMenu open onOpenChange={open => { if (!open) close(); }} className="unsent-context-menu" label={t("receipt.failed")} point={{ x, y }} key={`${x}:${y}`}>
    <button onClick={() => { close(); onEdit(); }}><Icon name="edit" size={17} />{t("receipt.edit")}</button>
    <div className="tb-menu-sep" role="separator" />
    <button className="is-danger" onClick={() => { close(); onDelete(); }}><Icon name="trash" size={17} />{t("receipt.delete")}</button>
  </OverlayMenu>;
}
