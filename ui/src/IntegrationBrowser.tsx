import { TofiIcon } from "./icons";
import { ServiceMark } from "./ServiceMark";
import { CATS, EmptyState } from "./EmptyCat";
import { useTranslation } from "./i18n";
import { RepoLink, useRepoStars } from "./connections/repoStars";
import { heldIntegrations, LOCAL_PLUGIN_REPOS, integrationAuthLabel, integrationCatalog, integrationOriginLabel, matchesIntegration, type IntegrationPreset } from "./integrationCatalog";

/** One catalog service as a directory row (the directory and the Workspace servers inside the Google step share it). */
export function IntegrationTile({ item, installed, stars, onSelect }: { item: IntegrationPreset; installed: boolean; stars: Record<string, number>; onSelect: (preset: IntegrationPreset) => void }) {
  const { t } = useTranslation("extensions");
  return <div className="integration-item">
    <button className="integration-tile" data-integration-id={item.id} disabled={installed} onClick={() => onSelect(item)}><ServiceMark name={item.name} /><span><strong>{item.name}</strong><small>{item.description}</small><small className="integration-details">{integrationAuthLabel(item.auth)} · {integrationOriginLabel(item.upstream)}{item.readOnly ? ` · ${t("browser.read_only_tools")}` : ""}</small></span>{installed ? <span className="integration-added">{t("browser.added")}</span> : <TofiIcon name="plus" size={16} />}</button>
    {item.repo && <div className="integration-repo"><RepoLink repo={item.repo} stars={stars[item.repo]} /></div>}
  </div>;
}

/**
 * The directory in the "Add connection" dialog: Google as one tile (Gmail, Calendar and Drive; its Workspace MCP servers sit inside it),
 * the other services, then the advanced "Custom" paths. Choosing one hands off to its setup step; nothing is configured here.
 */
export function IntegrationBrowser({ query, servers, googleAdded, localAvailable, onQueryChange, onSelect, onGoogle, onCustom, onPackage }: {
  query: string; servers: { url: string }[]; googleAdded: boolean; localAvailable: boolean;
  onQueryChange: (query: string) => void; onSelect: (preset: IntegrationPreset) => void; onGoogle: () => void; onCustom: () => void; onPackage?: () => void;
}) {
  const { t } = useTranslation("extensions");
  // The directory curates official connectors; a community one is added through the custom path below.
  const held = heldIntegrations.filter(item => item.upstream === "vendor" && matchesIntegration(item, query));
  const matching = integrationCatalog.filter(item => matchesIntegration(item, query));
  const needle = query.trim().toLowerCase();
  const stars = useRepoStars([...integrationCatalog.map(item => item.repo ?? ""), ...(localAvailable ? [LOCAL_PLUGIN_REPOS.gog] : [])]);
  // One Google tile stands for Gmail, Calendar, Drive and the Workspace servers: any of those words finds it.
  const googleName = t("connections.google.tile_name"), googleText = t("connections.google.tile_description");
  const showGoogle = !needle || `${googleName} ${googleText} gmail google calendar drive docs sheets slides workspace mail`.toLowerCase().includes(needle);
  const services = matching.filter(item => item.category === "service");
  const tile = (item: IntegrationPreset) => <IntegrationTile key={item.id} item={item} installed={servers.some(server => server.url === item.url)} stars={stars} onSelect={onSelect} />;
  const nothing = services.length === 0 && held.length === 0 && !showGoogle;
  return <div className="connect-directory">
    <label className="integration-search"><TofiIcon name="search" size={20} /><input aria-label={t("browser.search")} placeholder={t("browser.search")} value={query} onChange={e => onQueryChange(e.target.value)} /></label>
    {(showGoogle || services.length > 0) && <section className="integration-section"><h3>{t("browser.services")}</h3><div className="integration-grid">
      {showGoogle && <div className="integration-item"><button className="integration-tile" data-integration-id="google" onClick={onGoogle}><ServiceMark name="Google" /><span><strong>{googleName}</strong><small>{googleText}</small><small className="integration-details">{localAvailable ? t("browser.local_details") : t("browser.google_workspace_details")}</small></span>{googleAdded ? <span className="integration-added">{t("browser.added")}</span> : <TofiIcon name="chevron-right" size={16} />}</button>{localAvailable && <div className="integration-repo"><RepoLink repo={LOCAL_PLUGIN_REPOS.gog} stars={stars[LOCAL_PLUGIN_REPOS.gog]} /></div>}</div>}
      {services.map(item => tile(item))}
    </div></section>}
    {held.length > 0 && <section className="integration-section integration-held" aria-label={t("browser.held_label")}><h3>{t("browser.held_title")}</h3><ul>{held.map(item => <li key={item.id} data-held-integration-id={item.id}><ServiceMark name={item.name} /><div><strong>{item.name}</strong><small>{integrationAuthLabel(item.auth)} · {item.maintenance === "retired" ? `${t("browser.retired_origin")} · ${t("browser.retired")}` : integrationOriginLabel(item.upstream)}</small><p>{item.reason}</p><a href={item.docsURL} target="_blank" rel="noopener noreferrer">{item.maintenance === "retired" ? t("docs.retired") : item.upstream === "community" ? t("docs.community") : t("docs.vendor")}<TofiIcon name="external-link" size={14} /></a></div></li>)}</ul></section>}
    {nothing && <EmptyState name="integrations-no-match" className="mcp-placeholder is-compact" role="status" look={CATS.yuzu} pose="curious"><p>{t("browser.empty")}</p></EmptyState>}
    <section className="integration-section" aria-label={t("browser.custom_section")}>
      <h3>{t("browser.custom_section")}</h3>
      <p className="integration-catalog-note">{t("browser.community_note")}</p>
      <div className="integration-grid">
        <button className="integration-tile" data-integration-id="custom-url" onClick={onCustom}><span className="service-mark"><TofiIcon name="mcp" size={22} /></span><span><strong>{t("browser.custom_url")}</strong><small>{t("browser.custom_url_hint")}</small></span><TofiIcon name="chevron-right" size={16} /></button>
        {onPackage && <button className="integration-tile" data-integration-id="custom-package" onClick={onPackage}><span className="service-mark"><TofiIcon name="terminal" size={22} /></span><span><strong>{t("browser.custom_package")}</strong><small>{t("browser.custom_package_hint")}</small></span><TofiIcon name="chevron-right" size={16} /></button>}
      </div>
    </section>
  </div>;
}
