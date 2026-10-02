import {createContext, useContext} from "react";

export const SettingsActivityContext = createContext(true);

export function useSettingsActive() {
  return useContext(SettingsActivityContext);
}
