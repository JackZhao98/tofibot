import { TofiIcon, type TofiIconName } from "./icons";
import { CATS, EmptyState } from "./EmptyCat";
import { useTranslation } from "./i18n";
import { heldIntegrations, integrationAuthLabel, integrationCatalog, integrationOriginLabel, matchesIntegration, type IntegrationPreset } from "./integrationCatalog";

export function ServiceMark({name}:{name:string}) {
 const icon:TofiIconName = /Calendar/.test(name)?"calendar":/Drive/.test(name)?"folder":/Gmail/.test(name)?"inbox":/Docs|Sheets|Slides/.test(name)?"file-text":/GitHub/.test(name)?"code":/Notion|Context7/.test(name)?"book-open":"mcp";
 return <span className={`service-mark${/Google|Gmail/.test(name)?" service-mark-google":""}`} aria-hidden="true"><TofiIcon name={icon} size={22}/></span>;
}

export function IntegrationBrowser({query,servers,onQueryChange,onBack,onSelect,onCustom}:{query:string;servers:{url:string}[];onQueryChange:(query:string)=>void;onBack:()=>void;onSelect:(preset:IntegrationPreset)=>void;onCustom:()=>void}) {
 const { t } = useTranslation("extensions");
 const held = heldIntegrations.filter(item=>matchesIntegration(item,query));
 const matching = integrationCatalog.filter(item=>matchesIntegration(item,query));
 return <>
  <div className="mcp-toolbar"><button className="extension-back" onClick={onBack}><TofiIcon name="arrow-left" size={16}/>{t("browser.back")}</button><h3>{t("browser.title")}</h3></div>
  <p className="integration-catalog-note">{t("browser.intro")}</p>
  <label className="integration-search"><TofiIcon name="search" size={20}/><input aria-label={t("browser.search")} placeholder={t("browser.search")} value={query} onChange={e=>onQueryChange(e.target.value)}/></label>
  {(["google","service"] as const).map(category=>{
   const items=matching.filter(item=>item.category===category);
   return items.length>0&&<section className="integration-section" key={category}><h3>{category==="google"?"Google Workspace":t("browser.services")}</h3><div className="integration-grid">{items.map(item=>{
    const installed=servers.some(server=>server.url===item.url);
    return <button className="integration-tile" key={item.id} data-integration-id={item.id} disabled={installed} onClick={()=>onSelect(item)}><ServiceMark name={item.name}/><span><strong>{item.name}</strong><small>{item.description}</small><small className="integration-details">{integrationAuthLabel(item.auth)} · {integrationOriginLabel(item.upstream)}{item.readOnly?` · ${t("browser.read_only_tools")}`:""}</small></span>{installed?<span className="integration-added">{t("browser.added")}</span>:<TofiIcon name="plus" size={16}/>}</button>;
   })}</div></section>;
  })}
  {held.length>0&&<section className="integration-section integration-held" aria-label={t("browser.held_label")}><h3>{t("browser.held_title")}</h3><ul>{held.map(item=><li key={item.id} data-held-integration-id={item.id}><ServiceMark name={item.name}/><div><strong>{item.name}</strong><small>{integrationAuthLabel(item.auth)} · {item.maintenance==="retired"?`${t("browser.retired_origin")} · ${t("browser.retired")}`:integrationOriginLabel(item.upstream)}</small><p>{item.reason}</p><a href={item.docsURL} target="_blank" rel="noopener noreferrer">{item.maintenance==="retired"?t("docs.retired"):item.upstream==="community"?t("docs.community"):t("docs.vendor")}<TofiIcon name="external-link" size={14}/></a></div></li>)}</ul></section>}
  {matching.length===0&&held.length===0&&<EmptyState name="integrations-no-match" className="mcp-placeholder is-compact" role="status" look={CATS.yuzu} pose="curious"><p>{t("browser.empty")}</p></EmptyState>}
  <button className="integration-custom" onClick={onCustom}><TofiIcon name="plus" size={20}/><span>{t("browser.custom")}</span><TofiIcon name="chevron-right" size={16}/></button>
 </>;
}
