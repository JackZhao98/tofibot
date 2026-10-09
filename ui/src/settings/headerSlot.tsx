import {createContext, useContext, type ReactNode} from "react";
import {createPortal} from "react-dom";
import {useSettingsActive} from "../settingsActivity";

/** The shell's page-header action slot: at most one primary button, next to the close button. */
export const SettingsHeaderSlotContext = createContext<HTMLElement | null>(null);

/** Renders its children into the header slot, only while its page is the visible one. */
export function SettingsHeaderAction({children}: {children: ReactNode}) {
  const slot = useContext(SettingsHeaderSlotContext);
  const active = useSettingsActive();
  return slot && active ? createPortal(children, slot) : null;
}
