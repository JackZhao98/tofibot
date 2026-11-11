import { createContext, useContext } from "react";
import { TofiIcon } from "./icons";
import { useTranslation } from "./i18n";

/** Opens the Bot computer's mini window (or brings it forward). Null where there is no computer: the button then is not drawn. */
export const ComputerLookContext = createContext<(() => void) | null>(null);

/** The small monitor button at the end of a computer step row: the person's own way to open the window, whatever the automatic setting says. */
export function ComputerLookButton() {
  const { t } = useTranslation("computer");
  const look = useContext(ComputerLookContext);
  if (!look) return null;
  return <button type="button" className="computer-look" aria-label={t("desktop.look_aria")} data-hint={t("desktop.look")} onClick={event => { event.stopPropagation(); look(); }}><TofiIcon name="monitor" size={14} /></button>;
}
