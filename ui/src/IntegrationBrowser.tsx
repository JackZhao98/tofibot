import { TofiIcon, type TofiIconName } from "./icons";
import { heldIntegrations, integrationAuthLabel, integrationCatalog, integrationOriginLabel, matchesIntegration, type IntegrationPreset } from "./integrationCatalog";

export function ServiceMark({name}:{name:string}) {
 const icon:TofiIconName = /Calendar/.test(name)?"calendar":/Drive/.test(name)?"folder":/Gmail/.test(name)?"inbox":/Docs|Sheets|Slides/.test(name)?"file-text":/GitHub/.test(name)?"code":/Notion|Context7/.test(name)?"book-open":"mcp";
 return <span className={`service-mark${/Google|Gmail/.test(name)?" service-mark-google":""}`} aria-hidden="true"><TofiIcon name={icon} size={22}/></span>;
}

export function IntegrationBrowser({query,servers,onQueryChange,onBack,onSelect,onCustom}:{query:string;servers:{url:string}[];onQueryChange:(query:string)=>void;onBack:()=>void;onSelect:(preset:IntegrationPreset)=>void;onCustom:()=>void}) {
 const held = heldIntegrations.filter(item=>matchesIntegration(item,query));
 const matching = integrationCatalog.filter(item=>matchesIntegration(item,query));
 return <>
  <div className="mcp-toolbar"><button className="extension-back" onClick={onBack}><TofiIcon name="arrow-left" size={16}/>已添加</button><h3>添加服务</h3></div>
  <p className="integration-catalog-note">选择连接预设，添加后确认授权和工具连接。</p>
  <label className="integration-search"><TofiIcon name="search" size={20}/><input aria-label="搜索服务" placeholder="搜索服务" value={query} onChange={e=>onQueryChange(e.target.value)}/></label>
  {(["google","service"] as const).map(category=>{
   const items=matching.filter(item=>item.category===category);
   return items.length>0&&<section className="integration-section" key={category}><h3>{category==="google"?"Google Workspace":"服务连接"}</h3><div className="integration-grid">{items.map(item=>{
    const installed=servers.some(server=>server.url===item.url);
    return <button className="integration-tile" key={item.id} data-integration-id={item.id} disabled={installed} onClick={()=>onSelect(item)}><ServiceMark name={item.name}/><span><strong>{item.name}</strong><small>{item.description}</small><small className="integration-details">{integrationAuthLabel(item.auth)} · {integrationOriginLabel(item.upstream)}{item.readOnly?" · 只读工具":""}</small></span>{installed?<span className="integration-added">已添加</span>:<TofiIcon name="plus" size={16}/>}</button>;
   })}</div></section>;
  })}
  {held.length>0&&<section className="integration-section integration-held" aria-label="暂不可添加的服务"><h3>暂不可添加</h3><ul>{held.map(item=><li key={item.id} data-held-integration-id={item.id}><ServiceMark name={item.name}/><div><strong>{item.name}</strong><small>{integrationAuthLabel(item.auth)} · {item.maintenance==="retired"?"历史提供方项目 · 已停止维护":integrationOriginLabel(item.upstream)}</small><p>{item.reason}</p><a href={item.docsURL} target="_blank" rel="noopener noreferrer">{item.maintenance==="retired"?"历史项目文档":item.upstream==="community"?"社区项目文档":"接入文档"}<TofiIcon name="external-link" size={14}/></a></div></li>)}</ul></section>}
  {matching.length===0&&held.length===0&&<p className="mcp-placeholder" role="status">没有找到匹配的服务。可以添加自定义 MCP。</p>}
  <button className="integration-custom" onClick={onCustom}><TofiIcon name="plus" size={20}/><span>自定义 MCP</span><TofiIcon name="chevron-right" size={16}/></button>
 </>;
}
