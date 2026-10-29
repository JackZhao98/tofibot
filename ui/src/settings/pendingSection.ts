/** The section a link names waits here until the page that owns it is mounted, then `takeSettingsSection` hands it over once (the `takeConnectionTarget` pattern). Lazy chunks only. */
export type SectionScope = "settings" | "bot-settings" | "group-settings";
export type PendingSection = { scope: SectionScope; page: string; section: string };
let pendingSection: PendingSection | null = null;
export const setPendingSection = (target: PendingSection | null) => { pendingSection = target; };
/** Returns the pending section for this scope (and page, when given) once; a section meant for another page stays. */
export const takeSettingsSection = (scope: SectionScope, page?: string): string | null => {
  if (!pendingSection || pendingSection.scope !== scope || (page !== undefined && pendingSection.page !== page)) return null;
  const { section } = pendingSection;
  pendingSection = null;
  return section;
};
