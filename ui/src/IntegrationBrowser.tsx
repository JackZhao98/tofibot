import { TofiIcon } from "./icons";
import { ServiceMark } from "./ServiceMark";
import { CATS, EmptyState } from "./EmptyCat";
import { Disclosure } from "./InteractionSystem";
import { useTranslation } from "./i18n";
import { heldIntegrations, integrationAuthLabel, integrationCatalog, integrationOriginLabel, matchesIntegration, type IntegrationPreset } from "./integrationCatalog";

/** Connectors that run beside Tofi instead of at a vendor URL. Only offered when the server has a local plugin service. */
export type LocalConnector = { id: "local-gog"; added: boolean };

/**
 * The directory in the "Add connection" dialog: Google services, other services and local connectors in one list,
 * then the advanced "Custom" paths. Choosing one hands off to its setup step; nothing is configured here.
 */
export function IntegrationBrowser({ query, servers, localConnectors, onQueryChange, onSelect, onLocal, onCustom, onPackage }: {
  query: string; servers: { url: string }[]; localConnectors: LocalConnector[];
  onQueryChange: (query: string) => void; onSelect: (preset: IntegrationPreset) => void; onLocal: (id: LocalConnector["id"]) => void; onCustom: () => void; onPackage?: () => void;
}) {
  const { t } = useTranslation("extensions");
  // The directory curates official connectors; a community one is added through the custom path below.
  const held = heldIntegrations.filter(item => item.upstream === "vendor" && matchesIntegration(item, query));
  const matching = integrationCatalog.filter(item => matchesIntegration(item, query));
  const needle = query.trim().toLowerCase();
  const locals = localConnectors.map(item => ({ ...item, name: t("connections.local.gog.name"), description: t("connections.local.gog.description") }))
    .filter(item => !needle || `${item.name} ${item.description} gmail google`.toLowerCase().includes(needle));
  const services = matching.filter(item => item.category === "service");
  const google = matching.filter(item => item.category === "google");
  const tile = (item: IntegrationPreset) => {
    const installed = servers.some(server => server.url === item.url);
    return <button className="integration-tile" key={item.id} data-integration-id={item.id} disabled={installed} onClick={() => onSelect(item)}><ServiceMark name={item.name} /><span><strong>{item.name}</strong><small>{item.description}</small><small className="integration-details">{integrationAuthLabel(item.auth)} · {integrationOriginLabel(item.upstream)}{item.readOnly ? ` · ${t("browser.read_only_tools")}` : ""}</small></span>{installed ? <span className="integration-added">{t("browser.added")}</span> : <TofiIcon name="plus" size={16} />}</button>;
  };
  const nothing = matching.length === 0 && held.length === 0 && locals.length === 0;
  return <div className="connect-directory">
    <label className="integration-search"><TofiIcon name="search" size={20} /><input aria-label={t("browser.search")} placeholder={t("browser.search")} value={query} onChange={e => onQueryChange(e.target.value)} /></label>
    {(locals.length > 0 || services.length > 0) && <section className="integration-section"><h3>{t("browser.services")}</h3><div className="integration-grid">
      {locals.map(item => <button className="integration-tile" key={item.id} data-integration-id={item.id} disabled={item.added} onClick={() => onLocal(item.id)}><ServiceMark name="Gmail" /><span><strong>{item.name}</strong><small>{item.description}</small><small className="integration-details">{t("browser.local_details")}</small></span>{item.added ? <span className="integration-added">{t("browser.added")}</span> : <TofiIcon name="plus" size={16} />}</button>)}
      {services.map(item => tile(item))}
    </div></section>}
    {google.length > 0 && <section className="integration-section integration-preview">
      <h3>{t("browser.preview_title")}</h3>
      <p className="integration-catalog-note">{t("connections.preview_note")}</p>
      <Disclosure key={needle ? "match" : "all"} title={t("browser.preview_toggle")} defaultOpen={Boolean(needle)}>
        <div className="integration-grid">{google.map(item => tile(item))}</div>
      </Disclosure>
    </section>}
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
