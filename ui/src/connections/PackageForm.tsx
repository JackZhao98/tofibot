import { useState } from "react";
import { request } from "../api";
import { Banner } from "../settings/components";
import { useTranslation } from "../i18n";
import { message } from "./shared";

type PackageKind = "npm" | "pypi";

/** Install a pinned npm or PyPI MCP server on this server. The install command and the plugin run with the local plugin service's permissions. */
export function PackageForm({ onInstalled, onCancel }: { onInstalled: () => Promise<void>; onCancel: () => void }) {
  const { t } = useTranslation("extensions");
  const [kind, setKind] = useState<PackageKind>("npm");
  const [id, setID] = useState("");
  const [packageName, setPackageName] = useState("");
  const [version, setVersion] = useState("");
  const [binary, setBinary] = useState("");
  const [args, setArgs] = useState("");
  const [environment, setEnvironment] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const preview = kind === "npm"
    ? `npm install --prefix <${id || t("local.custom.plugin_id")}> --ignore-scripts --no-audit --no-fund -- ${packageName || t("local.custom.package")}@${version || t("local.custom.version")}`
    : `python3 -m venv <${id || t("local.custom.plugin_id")}> && pip install ${packageName || t("local.custom.package")}==${version || t("local.custom.version")}`;
  const entries = environment.split("\n").map(line => line.trim()).filter(Boolean);
  const valid = entries.every(line => /^[A-Za-z_][A-Za-z0-9_]*=/.test(line));
  async function install() {
    setBusy(true); setError("");
    try {
      await request("/api/extensions/local-mcp/install", { method: "POST", body: JSON.stringify({ id, kind, package: packageName, version, binary, args: args.split("\n").map(item => item.trim()).filter(Boolean), env: Object.fromEntries(entries.map(line => { const index = line.indexOf("="); return [line.slice(0, index), line.slice(index + 1)]; })) }) });
      await onInstalled();
    } catch (caught) { setError(message(caught)); setBusy(false); }
  }
  return <div className="connect-body">
    <p className="connect-lead">{t("local.custom.intro")}</p>
    <fieldset className="form-fields connect-form" disabled={busy}>
      <label>{t("local.custom.runtime")}<select value={kind} onChange={event => setKind(event.target.value as PackageKind)}><option value="npm">Node / npm</option><option value="pypi">Python / PyPI</option></select></label>
      <label>{t("local.custom.plugin_id")}<input value={id} onChange={event => setID(event.target.value)} placeholder="my-plugin" /></label>
      <label>{t("local.custom.package")}<input value={packageName} onChange={event => setPackageName(event.target.value)} /></label>
      <label>{t("local.custom.version")}<input value={version} onChange={event => setVersion(event.target.value)} placeholder="1.2.3" /></label>
      <label>{t("local.custom.binary")}<input value={binary} onChange={event => setBinary(event.target.value)} /></label>
      <label>{t("local.custom.args")}<textarea rows={2} value={args} onChange={event => setArgs(event.target.value)} /></label>
      <label>{t("local.custom.env")}<textarea rows={3} value={environment} onChange={event => setEnvironment(event.target.value)} /></label>
      <code className="connect-code">{preview}</code>
    </fieldset>
    {error && <Banner tone="error" title={error} />}
    <div className="sheet-actions"><button type="button" className="secondary-button" disabled={busy} onClick={onCancel}>{t("action.cancel")}</button><button type="button" className="primary-button" disabled={busy || !id || !packageName || !version || !binary || !valid} onClick={() => void install()}>{busy ? t("local.custom.installing") : t("local.custom.install")}</button></div>
  </div>;
}
