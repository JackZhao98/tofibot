import { useId, useState } from "react";
import { IntegrationBrowser } from "../IntegrationBrowser";
import { ModalDialog } from "../overlays/ModalDialog";
import { TofiIcon } from "../icons";
import { ServiceMark } from "../ServiceMark";
import type { IntegrationPreset } from "../integrationCatalog";
import type { OAuthOptions, OAuthRoute } from "../mcpOAuthRoute";
import { useTranslation } from "../i18n";
import type { LocalState } from "./localPlugins";
import { GogSetup } from "./GogSetup";
import { PackageForm } from "./PackageForm";
import { RemoteForm } from "./RemoteForm";
import type { MCP } from "./shared";
import type { GoogleOverview } from "./google";

export type DialogRequest =
  | { view: "directory" }
  | { view: "preset"; preset: IntegrationPreset }
  | { view: "custom" }
  | { view: "edit"; server: MCP; preset?: IntegrationPreset }
  | { view: "gog" }
  | { view: "package" };

/**
 * Everything that sets a connection up lives here, never inline on the page: the directory, then one focused step
 * (a catalog service, personal Gmail, a custom URL, a local package). Opened by "Add connection", by a row's
 * Authorize action and by "Edit settings".
 */
export function ConnectDialog({ request, servers, local, google, options, route, onSave, onLocalChanged, onClose }: {
  request: DialogRequest; servers: MCP[]; local: LocalState; google: GoogleOverview | null; options: OAuthOptions | null; route: OAuthRoute;
  onSave: (value: MCP, editing: boolean) => Promise<void>; onLocalChanged: () => Promise<void>; onClose: () => void;
}) {
  const { t } = useTranslation("extensions");
  const [view, setView] = useState<DialogRequest>(request);
  const [query, setQuery] = useState("");
  const [saving, setSaving] = useState(false);
  const headingId = useId();
  const fromDirectory = request.view === "directory";
  const heading = view.view === "directory" ? t("connections.dialog.title")
    : view.view === "preset" ? view.preset.name
    : view.view === "edit" ? view.preset?.name ?? view.server.name
    : view.view === "gog" ? t("connections.local.gog.name")
    : view.view === "package" ? t("browser.custom_package") : t("browser.custom_url");
  const hint = view.view === "directory" ? t("browser.intro")
    : view.view === "preset" ? view.preset.description
    : view.view === "gog" ? t("connections.local.gog.description")
    : view.view === "custom" ? t("form.custom_description") : "";
  const mark = view.view === "preset" ? view.preset.name : view.view === "edit" ? view.preset?.name ?? view.server.name : view.view === "gog" ? "Gmail" : "";
  const editing = view.view === "edit" ? view.server : null;
  const back = () => setView({ view: "directory" });
  async function save(value: MCP) {
    setSaving(true);
    try { await onSave(value, Boolean(editing)); } finally { setSaving(false); }
  }
  return <ModalDialog onClose={onClose} closeOnOutside={!saving} locked={saving} overlayClassName="sheet-overlay">
    <div className="sheet connect-dialog" role="dialog" aria-labelledby={headingId} tabIndex={-1}>
      <header className="connect-head">
        {view.view !== "directory" && fromDirectory && <button type="button" className="close-button" aria-label={t("mcp.back")} disabled={saving} onClick={back}><TofiIcon name="arrow-left" size={18} /></button>}
        {mark && <ServiceMark name={mark} />}
        <div className="connect-title"><h3 id={headingId}>{heading}</h3>{hint && <p>{hint}</p>}</div>
        <button type="button" className="close-button" aria-label={t("action.close")} disabled={saving} onClick={onClose}><TofiIcon name="close" size={18} /></button>
      </header>
      {view.view === "directory" && <IntegrationBrowser query={query} servers={servers} onQueryChange={setQuery}
        localConnectors={local.available ? [{ id: "local-gog", added: servers.some(server => server.name === "local_gog") }] : []}
        onSelect={preset => setView({ view: "preset", preset })} onLocal={() => setView({ view: "gog" })} onCustom={() => setView({ view: "custom" })} onPackage={local.available ? () => setView({ view: "package" }) : undefined} />}
      {(view.view === "preset" || view.view === "custom" || view.view === "edit") && <RemoteForm google={google} key={editing?.name ?? (view.view === "preset" ? view.preset.id : "custom")} initial={editing} preset={view.view === "preset" || view.view === "edit" ? view.preset : undefined} options={options} route={route} busy={saving} onSave={save} onCancel={fromDirectory && view.view !== "edit" ? back : onClose} />}
      {view.view === "gog" && <GogSetup local={local} google={google} callbackOrigin={options?.web_callback_origin} onChanged={onLocalChanged} onDone={onClose} onCancel={fromDirectory ? back : onClose} />}
      {view.view === "package" && <PackageForm onInstalled={async () => { await onLocalChanged(); onClose(); }} onCancel={fromDirectory ? back : onClose} />}
    </div>
  </ModalDialog>;
}
