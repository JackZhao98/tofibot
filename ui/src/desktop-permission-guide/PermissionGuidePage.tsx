import { useTranslation } from "../i18n";

// Native theme updates can arrive while the catalogs are loading, before React mounts.
window.setPermissionGuideTheme = dark => {
  document.documentElement.dataset.theme = dark ? "dark" : "light";
  document.documentElement.style.colorScheme = dark ? "dark" : "light";
};
document.documentElement.dataset.permissionGuide = "true";
const params = new URLSearchParams(location.search);
window.setPermissionGuideTheme(params.get("theme") === "dark");

export function PermissionGuidePage() {
  const {t} = useTranslation("settings");
  const name = params.get("packaged") === "1" ? "Tofi.app" : t("desktop_pages.guide.development");
  return <main className="permission-guide">
    <div className="permission-tile" draggable aria-label={t("desktop_pages.guide.drag_label", {name})} onDragStart={event => { event.preventDefault(); window.tofiDesktop?.startAccessibilityDrag(); }}>
      <img src="/__desktop/app-icon.png" draggable={false} alt=""/>
      <div className="permission-copy"><strong>{t("desktop_pages.guide.title", {name})}</strong><p>{t("desktop_pages.guide.hint")}</p></div>
    </div>
    <button className="st-link permission-close" aria-label={t("desktop_pages.guide.close")} onClick={() => window.close()}>×</button>
  </main>;
}
