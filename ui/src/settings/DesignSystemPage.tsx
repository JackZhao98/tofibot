import { useOwnerSession } from "../OwnerSession";
import { useTranslation } from "../i18n";

export default function DesignSystemPage() {
  const session = useOwnerSession();
  const { t } = useTranslation("settings");
  if (!session || (session.multi_account && session.owner?.role !== "admin")) return null;
  const src = import.meta.env.DEV ? "/catalog.html" : "/design-system.html";
  const title = t("shell.page.admin-design.name");
  return <section className="st-wide design-system-page">
    <a href={src} target="_blank" rel="noreferrer">{t("design.open_tab")}</a>
    <iframe src={src} title={title}/>
  </section>;
}
