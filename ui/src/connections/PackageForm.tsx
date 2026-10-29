import { useState } from "react";
import { request } from "../api";
import { Banner } from "../settings/components";
import { useTranslation } from "../i18n";
import { message } from "./shared";
import { Select } from "../Select";

type PackageKind = "npm" | "pypi";
type EnvRow = { key: string; value: string };
const integrityPattern = /^sha512-[A-Za-z0-9+/]{86}==$/;

/** Install a pinned npm or PyPI MCP server on this server. The install command and the plugin run with the local plugin service's permissions. */
export function PackageForm({ onInstalled, onCancel }: { onInstalled: () => Promise<void>; onCancel: () => void }) {
  const { t } = useTranslation("extensions");
  const [kind, setKind] = useState<PackageKind>("npm");
  const [id, setID] = useState("");
  const [packageName, setPackageName] = useState("");
  const [version, setVersion] = useState("");
  const [binary, setBinary] = useState("");
  const [args, setArgs] = useState("");
  const [integrity, setIntegrity] = useState("");
  const [envRows, setEnvRows] = useState<EnvRow[]>([{ key: "", value: "" }]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const preview = kind === "npm"
    ? `npm install --prefix <${id || t("local.custom.plugin_id")}> --ignore-scripts --no-audit --no-fund -- ${packageName || t("local.custom.package")}@${version || t("local.custom.version")}`
    : `python3 -m venv <${id || t("local.custom.plugin_id")}> && pip install ${packageName || t("local.custom.package")}==${version || t("local.custom.version")}`;
  const entries = envRows.map(row => ({ key: row.key.trim(), value: row.value })).filter(row => row.key !== "" || row.value !== "");
  const valid = entries.every(row => /^[A-Za-z_][A-Za-z0-9_]*$/.test(row.key)) && (kind !== "npm" || integrity.trim() === "" || integrityPattern.test(integrity.trim()));
  const updateRow = (index: number, patch: Partial<EnvRow>) => setEnvRows(rows => rows.map((row, at) => at === index ? { ...row, ...patch } : row));
  async function install() {
    setBusy(true); setError("");
    try {
      await request("/api/extensions/local-mcp/install", { method: "POST", body: JSON.stringify({ id, kind, package: packageName, version, binary, args: args.split("\n").map(item => item.trim()).filter(Boolean), env: Object.fromEntries(entries.map(row => [row.key, row.value])), ...(kind === "npm" && integrity.trim() ? { integrity: integrity.trim() } : {}) }) });
      setEnvRows([{ key: "", value: "" }]); setIntegrity("");
      await onInstalled();
    } catch (caught) { setError(message(caught)); setBusy(false); }
  }
  return <div className="connect-body">
    <p className="connect-lead">{t("local.custom.intro")}</p>
    <fieldset className="form-fields connect-form" disabled={busy}>
      <label>{t("local.custom.runtime")}<Select value={kind} onValueChange={value => setKind(value as PackageKind)}><option value="npm">Node / npm</option><option value="pypi">Python / PyPI</option></Select></label>
      <label>{t("local.custom.plugin_id")}<input value={id} onChange={event => setID(event.target.value)} placeholder="my-plugin" /></label>
      <label>{t("local.custom.package")}<input value={packageName} onChange={event => setPackageName(event.target.value)} /></label>
      <label>{t("local.custom.version")}<input value={version} onChange={event => setVersion(event.target.value)} placeholder="1.2.3" /></label>
      <label>{t("local.custom.binary")}<input value={binary} onChange={event => setBinary(event.target.value)} /></label>
      <label>{t("local.custom.args")}<textarea rows={2} value={args} onChange={event => setArgs(event.target.value)} /></label>
      {kind === "npm" && <label>{t("local.custom.integrity")}<input value={integrity} onChange={event => setIntegrity(event.target.value)} placeholder="sha512-…" spellCheck={false} autoComplete="off" /></label>}
      <fieldset className="connect-env"><legend>{t("local.custom.env")}</legend>
        {envRows.map((row, index) => <div key={index} className="connect-env-row"><input aria-label={t("local.custom.env_name")} value={row.key} onChange={event => updateRow(index, { key: event.target.value })} placeholder="API_TOKEN" spellCheck={false} autoComplete="off" /><input aria-label={t("local.custom.env_value")} type="password" value={row.value} onChange={event => updateRow(index, { value: event.target.value })} autoComplete="new-password" spellCheck={false} /></div>)}
        <button type="button" className="secondary-button" onClick={() => setEnvRows(rows => [...rows, { key: "", value: "" }])}>{t("local.custom.env_add")}</button>
      </fieldset>
      <code className="connect-code">{preview}</code>
    </fieldset>
    {error && <Banner tone="error" title={error} />}
    <div className="sheet-actions"><button type="button" className="secondary-button" disabled={busy} onClick={onCancel}>{t("action.cancel")}</button><button type="button" className="primary-button" disabled={busy || !id || !packageName || !version || !binary || !valid} onClick={() => void install()}>{busy ? t("local.custom.installing") : t("local.custom.install")}</button></div>
  </div>;
}
