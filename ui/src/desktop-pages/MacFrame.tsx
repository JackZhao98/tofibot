import type { ReactNode } from "react";
import { SettingsShell } from "../ds/SettingsShell/SettingsShell";

/** The frame of every Mac page (V3 PR 8.2b): the settings page's dot ground and one SettingsShell without a rail, so the head row
 *  (title, a quiet tag, the way back) is the same as Settings'. Kept free of fetch for the settings isolation guard. */
export function MacFrame({ title, tag, back, children }: { title: string; tag?: ReactNode; back?: ReactNode; children: ReactNode }) {
  return <div className="workspace mac-settings desktop-page settings-shell">
    <div className="mac-settings-titlebar" aria-hidden="true" />
    <SettingsShell className="settings-frame" groups={[]} active="" onNavigate={() => {}} navLabel={title} title={title} titleAs="h1" tag={tag} actions={back}>
      <main className="settings-page-body"><div className="mac-settings-inner">{children}</div></main>
    </SettingsShell>
  </div>;
}
