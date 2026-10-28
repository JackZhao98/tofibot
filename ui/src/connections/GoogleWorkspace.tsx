import { Disclosure } from "../InteractionSystem";
import { IntegrationTile } from "../IntegrationBrowser";
import { integrationCatalog, type IntegrationPreset } from "../integrationCatalog";
import { useTranslation } from "../i18n";
import { Banner } from "../settings/components";
import { useRepoStars } from "./repoStars";

/** D46: from this day Workspace MCP rejects OAuth clients that are not marked as AI-agent clients (the day after, an existing client is fixed). */
const AGENTIC_CUTOFF = Date.UTC(2026, 10, 11);

/**
 * "Advanced: Google Workspace MCP (Developer Preview)": the six first-party Workspace servers, folded inside the Google step instead
 * of sitting in the directory. A dated warning says what Google now requires of the OAuth client; after the date it is stated as fact.
 */
export function GoogleWorkspaceAdvanced({ servers, onSelect, defaultOpen = false, now = Date.now() }: { servers: { url: string }[]; onSelect: (preset: IntegrationPreset) => void; defaultOpen?: boolean; now?: number }) {
  const { t } = useTranslation("extensions");
  const presets = integrationCatalog.filter(item => item.category === "google");
  const stars = useRepoStars(presets.map(item => item.repo ?? ""));
  return <div className="connect-workspace" data-section="workspace">
    <Disclosure title={t("google.workspace.title")} defaultOpen={defaultOpen}>
      <Banner tone="warn" title={now >= AGENTIC_CUTOFF ? t("google.workspace.deadline_past") : t("google.workspace.deadline")} />
      <p className="integration-catalog-note">{t("connections.preview_note")}</p>
      <div className="integration-grid">{presets.map(item => <IntegrationTile key={item.id} item={item} installed={servers.some(server => server.url === item.url)} stars={stars} onSelect={onSelect} />)}</div>
    </Disclosure>
  </div>;
}
