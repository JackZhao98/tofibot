import { useEffect, useRef, useState } from "react";
import { request } from "../api";
import { useTranslation } from "../i18n";
import { message, normalizeTiers, post, TIERS, toolsEndpoint, type ToolTierList, type ToolTierView } from "./shared";

/** Tools grouped by tier, a per-tool selector, and Refresh. Copy stays in the extensions namespace. */
export function ToolTierPanel({ server, tiers, onChange, busy }: { server: string; tiers: ToolTierList; onChange: (value: ToolTierList) => void; busy: boolean }) {
  const { t } = useTranslation("extensions");
  const [refreshing, setRefreshing] = useState(false);
  const [saving, setSaving] = useState("");
  const [error, setError] = useState("");
  const alive = useRef(true);
  useEffect(() => { alive.current = true; return () => { alive.current = false; }; }, []);
  async function refresh() {
    setRefreshing(true); setError("");
    try {
      const value = normalizeTiers(await request<ToolTierList>(toolsEndpoint(server) + "/refresh", post({})));
      if (!alive.current) return;
      onChange(value);
      if (value.error_class) setError(value.diagnostics?.map(item => message(item.message)).join(t("diagnostics_separator")) || t("tools.error.refresh_failed"));
    } catch (e) { if (alive.current) setError(message(e)); }
    finally { if (alive.current) setRefreshing(false); }
  }
  async function setTier(tool: ToolTierView, value: string) {
    setSaving(tool.name); setError("");
    try {
      const result = await request<{ tool: ToolTierView; summary: ToolTierList }>(`${toolsEndpoint(server)}/${encodeURIComponent(tool.name)}`, { method: "PUT", body: JSON.stringify({ tier: value === "auto" ? "" : value }) });
      if (!alive.current) return;
      onChange(normalizeTiers(result.summary));
    } catch (e) { if (alive.current) setError(message(e)); }
    finally { if (alive.current) setSaving(""); }
  }
  const groups = TIERS.map(tier => ({ tier, tools: tiers.tools.filter(tool => tool.tier === tier) }));
  return <div className="mcp-tools" aria-busy={refreshing}>
    <div className="mcp-tools-head"><p>{t("tools.explain")}</p><button type="button" className="mcp-button" disabled={busy || refreshing} onClick={() => void refresh()}>{refreshing ? t("tools.refreshing") : t("tools.refresh")}</button></div>
    {error && <p className="mcp-service-error mcp-tools-error" role="alert">{error}</p>}
    {tiers.tools.length === 0 && <p className="mcp-tools-note">{t("tools.empty")}</p>}
    {groups.map(group => group.tools.length > 0 && <section key={group.tier} className="mcp-tool-group" data-tier={group.tier} aria-label={t(`tools.group.${group.tier}`)}>
      <h5>{t(`tools.group.${group.tier}`)}<span className="mcp-tool-count">{group.tools.length}</span></h5>
      <ul>{group.tools.map(tool => {
        const marker = tool.source === "owner" ? t("tools.source.owner") : tool.source === "provider" ? t("tools.source.provider") : "";
        return <li key={tool.name} className="mcp-tool" data-source={tool.source} data-changed={tool.changed ? "true" : undefined}>
          <div className="mcp-tool-copy"><code>{tool.name}</code><span className="mcp-tool-marks">{marker && <small className="mcp-tier-mark">{marker}</small>}{tool.duplicated && <small className="mcp-tier-mark">{t("tools.duplicated")}</small>}{tool.changed && <small className="mcp-tier-mark is-changed">{t("tools.changed")}</small>}</span>{tool.description && <p>{tool.description}</p>}</div>
          <select className="mcp-tier-select" aria-label={t("tools.select_label", { name: tool.name })} value={tool.override ?? "auto"} disabled={busy || refreshing || saving === tool.name} onChange={e => void setTier(tool, e.target.value)}>
            <option value="auto">{t("tools.tier.auto")}</option>
            {TIERS.map(tier => <option key={tier} value={tier}>{t(`tools.tier.${tier}`)}</option>)}
          </select>
        </li>;
      })}</ul>
    </section>)}
  </div>;
}
