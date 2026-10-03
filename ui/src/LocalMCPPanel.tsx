import { useCallback, useEffect, useState } from "react";
import { request } from "./api";
import "./local-mcp.css";

type Plugin = { id: string; state: "sleeping" | "starting" | "ready"; tool_count: number };
type RunnerStatus = { available: boolean; reason?: string; plugins?: Plugin[] };
type GoogleStatus = { connected: boolean; email: string; scope?: "readonly" | "read-send" };
type PackageKind = "npm" | "pypi";

const errorText = (error: unknown) => error instanceof Error ? error.message : String(error);

export function LocalMCPPanel({ onChanged, attachedIDs, callbackOrigin }: { onChanged: () => Promise<void>; attachedIDs: string[]; callbackOrigin?: string }) {
 const callbackURL = `${callbackOrigin ?? window.location.origin}/api/extensions/local-mcp/gog/oauth/callback`;
 const oauthReady = callbackOrigin?.startsWith("https://") && callbackOrigin === window.location.origin;
 const [runner, setRunner] = useState<RunnerStatus | null>(null);
 const [google, setGoogle] = useState<GoogleStatus | null>(null);
 const [busy, setBusy] = useState("");
 const [error, setError] = useState("");
 const [notice, setNotice] = useState("");
 const [authURL, setAuthURL] = useState("");
 const [email, setEmail] = useState("");
 const [googleScope, setGoogleScope] = useState<"readonly" | "read-send">("readonly");
 const [credentials, setCredentials] = useState("");
 const [kind, setKind] = useState<PackageKind>("npm");
 const [id, setID] = useState("");
 const [packageName, setPackageName] = useState("");
 const [version, setVersion] = useState("");
 const [binary, setBinary] = useState("");
 const [args, setArgs] = useState("");
 const [environment, setEnvironment] = useState("");
 const [confirmRemove, setConfirmRemove] = useState("");
 const [confirmDisconnect, setConfirmDisconnect] = useState(false);
 const refresh = useCallback(async () => {
  try {
   const status = await request<RunnerStatus>("/api/extensions/local-mcp");
   setRunner(status);
   if (status.plugins?.some(plugin => plugin.id === "gog")) {
    try { const account = await request<GoogleStatus>("/api/extensions/local-mcp/gog/oauth/status"); setGoogle(account); }
    catch { setGoogle(null); }
   } else setGoogle(null);
  } catch (caught) { setRunner({ available: false, reason: errorText(caught) }); }
 }, []);
 useEffect(() => { void refresh(); const focus = () => { if (!document.hidden) void refresh(); }; window.addEventListener("focus", focus); return () => window.removeEventListener("focus", focus); }, [refresh]);
 if (!runner) return null;
 if (!runner.available) return <section className="local-mcp-panel" aria-label="本地 MCP"><h3>本地 MCP Runner 暂不可用</h3><p>请检查 Runner 容器是否已启动。</p>{runner.reason && <p className="field-note">{runner.reason}</p>}</section>;
 const gog = runner.plugins?.find(plugin => plugin.id === "gog");
 const installed = runner.plugins ?? [];
 async function install(payload: { id: string; kind: string; package?: string; version?: string; binary?: string; args?: string[]; env?: Record<string,string> }) {
  setBusy(`install:${payload.id}`); setError(""); setNotice("");
  try {
   await request("/api/extensions/local-mcp/install", { method: "POST", body: JSON.stringify(payload) });
   await Promise.all([refresh(), onChanged()]);
   setNotice(`${payload.id} 已安装；工具会在使用时启动。`);
  } catch (caught) { setError(errorText(caught)); }
  finally { setBusy(""); }
 }
 async function connectGoogle() {
  let client: unknown;
  try { client = JSON.parse(credentials); } catch { setError("请上传有效的 Google OAuth 客户端 JSON。"); return; }
  // Open during the click gesture so browsers do not block the Google popup.
  const popup = window.open("about:blank", "tofi-google-oauth");
  if (popup) popup.document.body.textContent = "正在准备 Google 授权…";
  setBusy("oauth"); setError(""); setNotice("正在生成 Google 授权链接…"); setAuthURL("");
  try {
   const result = await request<{ authorization_url: string }>("/api/extensions/local-mcp/gog/oauth/start", { method: "POST", body: JSON.stringify({ email, scope: googleScope, credentials_json: client }) });
   setCredentials("");
   setAuthURL(result.authorization_url);
   setNotice("请在 Google 页面完成授权；返回 Tofi 后会更新连接状态。");
   if (popup && !popup.closed) popup.location.replace(result.authorization_url);
  } catch (caught) { popup?.close(); setError(errorText(caught)); setNotice(""); }
  finally { setBusy(""); }
 }
 async function checkGoogle() {
  setBusy("check"); setError(""); setNotice("");
  try { await request("/api/extensions/local-mcp/gog/oauth/check", { method: "POST", body: "{}" }); setNotice("个人 Gmail 读取已验证。" ); }
  catch (caught) { setError(errorText(caught)); }
  finally { setBusy(""); }
 }
 async function disconnectGoogle() {
  setBusy("disconnect");setError("");
  try { await request("/api/extensions/local-mcp/gog/oauth/disconnect",{method:"POST",body:"{}"});await refresh();setConfirmDisconnect(false);setNotice("Google 授权已从 Runner 移除。"); }
  catch (caught) { setError(errorText(caught)); }
  finally { setBusy(""); }
 }
 async function attach(pluginID: string) {
  setBusy(`attach:${pluginID}`); setError("");
  try { await request(`/api/extensions/local-mcp/${encodeURIComponent(pluginID)}/attach`, { method: "POST", body: "{}" }); await onChanged(); setNotice(`${pluginID} 已接入 Tofi。`); }
  catch (caught) { setError(errorText(caught)); }
  finally { setBusy(""); }
 }
 async function remove(pluginID: string) {
  setBusy(`remove:${pluginID}`); setError("");
  try { await request(`/api/extensions/local-mcp/${encodeURIComponent(pluginID)}`, { method: "DELETE" }); await Promise.all([refresh(), onChanged()]); setNotice(`${pluginID} 已移除。`); setConfirmRemove(""); }
  catch (caught) { setError(errorText(caught)); }
  finally { setBusy(""); }
 }
 const packagePreview = kind === "npm"
  ? `npm install --prefix <${id || "插件 ID"}> --ignore-scripts --no-audit --no-fund -- ${packageName || "包名"}@${version || "固定版本"}`
  : `python3 -m venv <${id || "插件 ID"}> && pip install ${packageName || "包名"}==${version || "固定版本"}`;
 const environmentEntries = environment.split("\n").map(line => line.trim()).filter(Boolean);
 const environmentValid = environmentEntries.every(line => /^[A-Za-z_][A-Za-z0-9_]*=/.test(line));
 return <section className="local-mcp-panel" aria-label="本地 MCP">
  <div className="local-mcp-heading"><div><h3>本地 MCP</h3><p>插件安装在独立 Runner 容器，按需启动，空闲后自动休眠。</p></div><span className="local-mcp-count">{installed.length} 个插件</span></div>
  <div className="local-mcp-card">
   <div className="local-mcp-card-title"><strong>个人 Gmail · gogcli</strong><span>{gog ? ({ ready: "运行中", starting: "启动中", sleeping: "已休眠" }[gog.state]) : "未安装"}</span></div>
   <p>固定版本的 gogcli 已包含在 Runner 镜像。可选择只读，或授权读取及发送。发送邮件仍需逐封确认。</p>
   {!gog ? <><code>gog mcp --allow-tool gmail</code><button className="mcp-button" disabled={!!busy} onClick={() => void install({ id: "gog", kind: "builtin_gog" })}>{busy === "install:gog" ? "正在安装…" : "安装 gogcli"}</button></> : <>
    <div className="local-mcp-google-state">{google?.connected ? `已授权 ${google.email}` : "等待 Google 授权"}</div>
    {!google?.connected && <div className="local-mcp-auth-form">
     <p>在 Google Cloud 创建 <strong>Web 应用</strong> OAuth 客户端，把以下地址加入“已获授权的重定向 URI”，再上传下载的 JSON。<a href="https://console.cloud.google.com/auth/clients" target="_blank" rel="noopener noreferrer">打开 Google Cloud</a></p>
     <code>{callbackURL}</code>
     <label>Google 邮箱<input type="email" value={email} onChange={event => setEmail(event.target.value)} placeholder="you@gmail.com" /></label>
     <label>Gmail 权限<select value={googleScope} onChange={event => setGoogleScope(event.target.value as "readonly" | "read-send")}><option value="readonly">只读：搜索及查看邮件</option><option value="read-send">读取及发送：每封邮件都要我确认</option></select></label>
     <label>OAuth 客户端 JSON<input type="file" accept=".json,application/json" onChange={event => { const file = event.target.files?.[0]; if (file) void file.text().then(setCredentials).catch(caught => setError(errorText(caught))); }} /></label>
     <button className="mcp-button" disabled={!!busy || !email || !credentials || !oauthReady} onClick={() => void connectGoogle()}>{busy === "oauth" ? "正在准备授权…" : "连接 Google"}</button>
     {!oauthReady && <small>请通过 Tofi 配置的 HTTPS 公开地址打开此页面。</small>}
     {authURL && <a href={authURL} target="_blank" rel="noopener noreferrer">继续打开 Google 授权页</a>}
    </div>}
    {google?.connected && <><p>{google.scope === "read-send" ? "已授权读取及发送；发送前仍需逐封确认。" : "当前仅授权读取。若要发送，请断开后重新选择读取及发送。"}</p><button className="mcp-button" disabled={!!busy} onClick={() => void checkGoogle()}>{busy === "check" ? "正在验证…" : "验证 Gmail 读取"}</button></>}
    {google?.connected && (confirmDisconnect ? <div className="local-mcp-confirm"><span>从 Runner 移除保存的 Google 授权？</span><button className="mcp-button" onClick={() => setConfirmDisconnect(false)}>取消</button><button className="mcp-button mcp-danger" disabled={!!busy} onClick={() => void disconnectGoogle()}>断开授权</button></div> : <button className="mcp-button" disabled={!!busy} onClick={() => setConfirmDisconnect(true)}>断开 Google</button>)}
    {!attachedIDs.includes("gog") && <button className="mcp-button" disabled={!!busy} onClick={() => void attach("gog")}>接入 Tofi</button>}
   </>}
  </div>
  {installed.length>0 && <div className="local-mcp-installed">{installed.map(plugin => <div key={plugin.id} className="local-mcp-installed-row"><span><strong>{plugin.id}</strong> · {plugin.state === "ready" ? "运行中" : plugin.state === "starting" ? "启动中" : "已休眠"}</span>{!attachedIDs.includes(plugin.id) && <button className="mcp-button" disabled={!!busy} onClick={() => void attach(plugin.id)}>接入 Tofi</button>}{confirmRemove === plugin.id ? <><span>移除插件及保存的授权？</span><button className="mcp-button" onClick={() => setConfirmRemove("")}>取消</button><button className="mcp-button mcp-danger" disabled={!!busy} onClick={() => void remove(plugin.id)}>确定移除</button></> : <button className="mcp-button" disabled={!!busy} onClick={() => setConfirmRemove(plugin.id)}>移除</button>}</div>)}</div>}
  <details className="local-mcp-custom"><summary>安装其他本地 MCP</summary>
   <p>指定固定版本和可执行文件。安装命令会在 Runner 内执行；插件代码将以 Runner 的权限运行。</p>
   <div className="local-mcp-fields"><label>运行时<select value={kind} onChange={event => setKind(event.target.value as PackageKind)}><option value="npm">Node / npm</option><option value="pypi">Python / PyPI</option></select></label><label>插件 ID<input value={id} onChange={event => setID(event.target.value)} placeholder="my-plugin" /></label><label>包名<input value={packageName} onChange={event => setPackageName(event.target.value)} /></label><label>固定版本<input value={version} onChange={event => setVersion(event.target.value)} placeholder="1.2.3" /></label><label>可执行文件<input value={binary} onChange={event => setBinary(event.target.value)} /></label><label>启动参数（每行一个）<textarea rows={2} value={args} onChange={event => setArgs(event.target.value)} /></label><label>环境变量（每行 KEY=value，作为私密文件保存）<textarea rows={3} value={environment} onChange={event => setEnvironment(event.target.value)} /></label></div>
   <code>{packagePreview}</code>
   <button className="mcp-button" disabled={!!busy || !id || !packageName || !version || !binary || !environmentValid} onClick={() => void install({ id, kind, package: packageName, version, binary, args: args.split("\n").map(item => item.trim()).filter(Boolean), env: Object.fromEntries(environmentEntries.map(line => { const index=line.indexOf("="); return [line.slice(0,index),line.slice(index+1)]; })) })}>安装插件</button>
  </details>
  {error && <p className="local-mcp-error" role="alert">{error}</p>}{notice && <p className="local-mcp-notice" role="status">{notice}</p>}
 </section>;
}
