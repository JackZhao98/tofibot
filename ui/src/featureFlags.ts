/**
 * Build-time switches for surfaces that are written but not shown.
 *
 * ARCHIVE_ENABLED: the archived-conversations view (ArchivePanel, `?side=archive`, the Settings shortcut to it). It is
 * unverified and its request 403s behind Cloudflare, so nothing links to it. The backend is untouched; flip this to
 * show it again. Every way in goes through `sidebarAvailable`, so a disabled view cannot close the panel that was open.
 */
export const ARCHIVE_ENABLED = false;

export const sidebarAvailable = (id: string | null | undefined): boolean => id !== "archive" || ARCHIVE_ENABLED;
