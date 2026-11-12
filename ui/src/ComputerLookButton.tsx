import { createContext, useContext } from "react";
import { TofiIcon } from "./icons";
import { sourceKey, type ComputerSource } from "./botBrowserSource";
import type { ToolActivity } from "./types";
import { useTranslation } from "./i18n";

/** Opens the explicitly selected computer source's mini window (or brings it forward). Null where there is no computer: the button then is not drawn. */
export const ComputerLookContext = createContext<{open: (source: ComputerSource) => void; sources: ComputerSource[]; defaultSource: ComputerSource | null} | null>(null);

/** The small monitor button at the end of a computer step row: the person's own way to open the window, whatever the automatic setting says. */
export function ComputerLookButton({activity}: {activity?: ToolActivity} = {}) {
  const { t } = useTranslation("computer");
  const look = useContext(ComputerLookContext);
  let source: ComputerSource | undefined;
  if (activity) {
    try {
      const args = JSON.parse(activity.arguments || '{}');
      if (typeof args.computer_id === 'string' && args.computer_id !== 'firecracker') {
        if (activity.name !== 'computer_browser' && !(activity.name === 'computer_action' && typeof args.action === 'string' && args.action.startsWith('browser.'))) return null;
        source = {kind: 'mac_browser', computer_id: args.computer_id};
      } else source = {kind: 'bot_computer', bot_id: activity.bot_id};
    } catch { return null; }
  }
  source ??= look?.defaultSource ?? undefined;
  if (!look || !source || !look.sources.some(item => sourceKey(item) === sourceKey(source!))) return null;
  return <button type="button" className="computer-look" aria-label={source?.kind === "mac_browser" ? t("browser.look", {id: source.computer_id}) : t("desktop.look_aria")} data-hint={t("desktop.look")} onClick={event => { event.stopPropagation(); look.open(source!); }}><TofiIcon name="monitor" size={14} /></button>;
}
