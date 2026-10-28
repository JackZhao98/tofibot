import { i18n } from "../i18n";
import { integrationCatalog, type IntegrationPreset } from "../integrationCatalog";
import { localPluginId, type MCP } from "./shared";

/** The catalog entry a saved connection came from, by address. */
export const presetFor = (server: MCP): IntegrationPreset | undefined => integrationCatalog.find(item => item.url === server.url);

/** The name a person sees: a catalog name, "Gmail" for the personal Gmail plugin, else what they called it. Never `local_gog`. */
export function displayName(server: MCP, preset: IntegrationPreset | undefined = presetFor(server)) {
  if (preset) return preset.name;
  const plugin = localPluginId(server.name);
  return plugin === "gog" ? i18n.t("extensions:connections.local.gog.name") : plugin || server.name;
}

/** The mark name for a connection: the vendor's brand for Gmail, else the display name. */
export const markName = (server: MCP, display: string) => localPluginId(server.name) === "gog" ? "Gmail" : display;
