import { useEffect, useId, useRef, useState } from "react";
import { IntegrationBrowser } from "../IntegrationBrowser";
import { ModalDialog } from "../overlays/ModalDialog";
import { TofiIcon } from "../icons";
import { ServiceMark } from "../ServiceMark";
import { Banner } from "../settings/components";
import type { IntegrationPreset } from "../integrationCatalog";
import { mcpOAuthRoute, type OAuthOptions, type OAuthRoute } from "../mcpOAuthRoute";
import { isDesktop } from "../desktop";
import { useTranslation } from "../i18n";
import type { LocalState } from "./localPlugins";
import { GogSetup } from "./GogSetup";
import { PackageForm } from "./PackageForm";
import { RemoteForm } from "./RemoteForm";
import { message, type MCP } from "./shared";
import { GoogleWorkspaceAdvanced } from "./GoogleWorkspace";
import { isOneClick, openSignInTab, presetDefaults } from "./oneClick";
import type { GoogleOverview } from "./google";

export type DialogRequest =
  | { view: "directory" }
  | { view: "preset"; preset: IntegrationPreset }
  | { view: "custom" }
  | { view: "edit"; server: MCP; preset?: IntegrationPreset }
  | { view: "gog" }
  | { view: "google" }
  | { view: "connecting"; preset: IntegrationPreset; popup?: Window | null }
  | { view: "package" };

/**
 * Everything that sets a connection up lives here, never inline on the page: the directory, then one focused step
 * (a catalog service, personal Gmail, a custom URL, a local package). Opened by "Add connection", by a row's
 * Authorize action and by "Edit settings".
 */
export function ConnectDialog({ request, servers, local, google, options, route, onSave, onOneClick, onLocalChanged, onClose }: {
  request: DialogRequest; servers: MCP[]; local: LocalState; google: GoogleOverview | null; options: OAuthOptions | null; route: OAuthRoute;
  onSave: (value: MCP, editing: boolean) => Promise<void>;
  /** Saves the connection and starts its sign-in (the page closes this dialog once it has); `popup` is the tab opened inside the click. */
  onOneClick: (value: MCP, popup: Window | null) => Promise<void>;
  onLocalChanged: () => Promise<void>; onClose: () => void;
}) {
  const { t } = useTranslation("extensions");
  const [view, setView] = useState<DialogRequest>(request);
  const [query, setQuery] = useState("");
  const [saving, setSaving] = useState(false);
  const [connectError, setConnectError] = useState("");
  // Reached through the Google step: Back returns there, not to the directory.
  const [viaGoogle, setViaGoogle] = useState(false);
  const headingId = useId();
  const fromDirectory = request.view === "directory";
  const heading = view.view === "directory" ? t("connections.dialog.title")
    : view.view === "preset" ? view.preset.name
    : view.view === "edit" ? view.preset?.name ?? view.server.name
    : view.view === "gog" ? t("connections.local.gog.name")
    : view.view === "google" ? t("connections.google.tile_name")
    : view.view === "connecting" ? view.preset.name
    : view.view === "package" ? t("browser.custom_package") : t("browser.custom_url");
  const hint = view.view === "directory" ? t("browser.intro")
    : view.view === "preset" ? view.preset.description
    : view.view === "custom" ? t("form.custom_description")
    : view.view === "google" ? t("connections.google.tile_description") : "";
  const mark = view.view === "preset" || view.view === "connecting" ? view.preset.name : view.view === "edit" ? view.preset?.name ?? view.server.name : view.view === "gog" ? "Gmail" : view.view === "google" ? "Google" : "";
  const editing = view.view === "edit" ? view.server : null;
  const back = () => { setConnectError(""); setView(viaGoogle ? { view: "google" } : { view: "directory" }); setViaGoogle(false); };
  const canGoBack = fromDirectory || viaGoogle;
  // The route this form's connection will take: a loopback-only provider (catalog property or saved setting) never gets the https popup.
  const loopbackOnly = (view.view === "edit" ? view.server.oauth?.redirect_policy : view.view === "preset" ? view.preset.oauthRedirect : undefined) === "loopback";
  const formRoute = loopbackOnly ? mcpOAuthRoute(window.location.protocol, isDesktop, Boolean(window.tofiDesktop?.authorizeMCP), options, true) : route;
  // The route a preset's sign-in would take, to decide whether it is one click.
  const routeFor = (preset: IntegrationPreset) => preset.oauthRedirect === "loopback" ? mcpOAuthRoute(window.location.protocol, isDesktop, Boolean(window.tofiDesktop?.authorizeMCP), options, true) : route;
  async function connect(preset: IntegrationPreset, popup: Window | null) {
    setSaving(true); setConnectError("");
    try { await onOneClick(presetDefaults(preset), popup); }
    catch (cause) { popup?.close(); setConnectError(message(cause)); }
    finally { setSaving(false); }
  }
  /** A directory or Google-step choice: one click for a self-registering OAuth service, the form for the rest. */
  function choose(preset: IntegrationPreset) {
    if (isOneClick(preset, servers, routeFor(preset))) {
      const popup = openSignInTab(preset.id, routeFor(preset));
      setView({ view: "connecting", preset, popup });
      void connect(preset, popup);
      return;
    }
    setViaGoogle(preset.category === "google" && view.view === "google");
    setView({ view: "preset", preset });
  }
  // Opened already connecting (an empty-state tile did the click): run it once.
  const started = useRef(false);
  useEffect(() => {
    if (request.view !== "connecting" || started.current) return;
    started.current = true;
    void connect(request.preset, request.popup ?? null);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  async function save(value: MCP) {
    setSaving(true);
    try { await onSave(value, Boolean(editing)); } finally { setSaving(false); }
  }
  return <ModalDialog onClose={onClose} closeOnOutside={!saving} locked={saving} overlayClassName="sheet-overlay">
    <div className="sheet connect-dialog" role="dialog" aria-labelledby={headingId} tabIndex={-1}>
      <header className="connect-head">
        {view.view !== "directory" && canGoBack && <button type="button" className="close-button" aria-label={t("mcp.back")} disabled={saving} onClick={back}><TofiIcon name="arrow-left" size={18} /></button>}
        {mark && <ServiceMark name={mark} />}
        <div className="connect-title"><h3 id={headingId}>{heading}</h3>{hint && <p>{hint}</p>}</div>
        <button type="button" className="close-button" aria-label={t("action.close")} disabled={saving} onClick={onClose}><TofiIcon name="close" size={18} /></button>
      </header>
      {view.view === "directory" && <IntegrationBrowser query={query} servers={servers} onQueryChange={setQuery} googleAdded={servers.some(server => server.name === "local_gog")} localAvailable={local.available}
        onSelect={choose} onGoogle={() => setView({ view: "google" })} onCustom={() => setView({ view: "custom" })} onPackage={local.available ? () => setView({ view: "package" }) : undefined} />}
      {(view.view === "preset" || view.view === "custom" || view.view === "edit") && <RemoteForm google={google} key={editing?.name ?? (view.view === "preset" ? view.preset.id : "custom")} initial={editing} preset={view.view === "preset" || view.view === "edit" ? view.preset : undefined} options={options} route={formRoute} busy={saving} onSave={save} onCancel={canGoBack && view.view !== "edit" ? back : onClose} />}
      {view.view === "gog" && <GogSetup local={local} google={google} callbackOrigin={options?.web_callback_origin} onChanged={onLocalChanged} onDone={onClose} onCancel={fromDirectory ? back : onClose} />}
      {view.view === "google" && (local.available
        ? <GogSetup local={local} google={google} callbackOrigin={options?.web_callback_origin} onChanged={onLocalChanged} onDone={onClose} onCancel={fromDirectory ? back : onClose}
            extra={<GoogleWorkspaceAdvanced servers={servers} onSelect={choose} />} />
        : <div className="connect-body"><GoogleWorkspaceAdvanced servers={servers} onSelect={choose} defaultOpen /></div>)}
      {view.view === "connecting" && <div className="connect-body" role="status" aria-live="polite">
        <p className="connect-lead">{t("connections.connecting.lead", { name: view.preset.name })}</p>
        {connectError && <Banner tone="error" title={connectError} />}
        <div className="sheet-actions">
          <button type="button" className="secondary-button" disabled={saving} onClick={() => { setConnectError(""); setView({ view: "preset", preset: view.preset }); }}>{t("connections.connecting.advanced")}</button>
          <button type="button" className="secondary-button" disabled={saving} onClick={onClose}>{t("action.cancel")}</button>
        </div>
      </div>}
      {view.view === "package" && <PackageForm onInstalled={async () => { await onLocalChanged(); onClose(); }} onCancel={fromDirectory ? back : onClose} />}
    </div>
  </ModalDialog>;
}
