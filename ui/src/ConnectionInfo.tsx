import {useDebugMode} from "./debugMode";
import { useEffect, useState } from "react";
import "./ConnectionInfo.css";

type DesktopConnection =
  | { connectionMode: "direct"; serverURL: string }
  | { connectionMode: "ssh"; sshAlias: string; remotePort: number };
type ConnectionState = { instanceID: string | null; desktop: DesktopConnection | null; loading: boolean };
const instancePattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

function desktopConnection(value: unknown): DesktopConnection | null {
  if (!value || typeof value !== "object") return null;
  const input = value as Record<string, unknown>;
  if (input.connectionMode === "ssh" && typeof input.sshAlias === "string" && /^[A-Za-z0-9][A-Za-z0-9_.@-]{0,127}$/.test(input.sshAlias) && typeof input.remotePort === "number" && Number.isInteger(input.remotePort) && input.remotePort > 0 && input.remotePort <= 65535) {
    return { connectionMode: "ssh", sshAlias: input.sshAlias, remotePort: input.remotePort };
  }
  if (input.connectionMode === "direct" && typeof input.serverURL === "string") {
    try {
      const url = new URL(input.serverURL);
      if (!["http:", "https:"].includes(url.protocol) || url.username || url.password || url.search || url.hash) return null;
      return { connectionMode: "direct", serverURL: url.href };
    } catch { return null; }
  }
  return null;
}

// Read-only connection identity for both the browser and desktop shell. The
// desktop settings bridge is intentionally not used: it exposes unrelated Mac
// device configuration and is restricted to its own local settings page.
export function ConnectionInfo() {
  const debug = useDebugMode();
  const isDesktop = Boolean((window as Window & { tofiDesktop?: unknown }).tofiDesktop);
  const [state, setState] = useState<ConnectionState>({ instanceID: null, desktop: null, loading: true });
  const [memorySession, setMemorySession] = useState(false);
  useEffect(() => {
    let disposed = false;
    let pending: AbortController | null = null;
    async function refresh() {
      pending?.abort();
      const controller = new AbortController();
      pending = controller;
      const timer = window.setTimeout(() => controller.abort(), 6000);
      const read = async (path: string, headers?: HeadersInit): Promise<unknown> => {
        const response = await fetch(path, { signal: controller.signal, credentials: "same-origin", cache: "no-store", headers });
        if (!response.ok || !response.headers.get("content-type")?.includes("application/json")) throw new Error("Connection metadata unavailable");
        return response.json();
      };
      const [server, desktop] = await Promise.allSettled([
        read("/api/server-info"),
        isDesktop ? read("/__desktop/connection", { "X-Tofi-Connection-Info": "1" }) : Promise.resolve(null),
      ]);
      window.clearTimeout(timer);
      if (disposed || pending !== controller) return;
      const info = server.status === "fulfilled" && server.value && typeof server.value === "object" ? server.value as Record<string, unknown> : null;
      const instanceID = info?.service === "tofi" && info.protocol_version === 1 && typeof info.instance_id === "string" && instancePattern.test(info.instance_id) ? info.instance_id : null;
      setState({ instanceID, desktop: desktop.status === "fulfilled" ? desktopConnection(desktop.value) : null, loading: false });
      setMemorySession(desktop.status === "fulfilled" && Boolean(desktop.value && typeof desktop.value === "object" && "sessionPersistence" in desktop.value && desktop.value.sessionPersistence === "memory"));
    }
    const focused = () => { if (!document.hidden) void refresh(); };
    void refresh();
    window.addEventListener("focus", focused);
    return () => { disposed = true; pending?.abort(); window.removeEventListener("focus", focused); };
  }, [isDesktop]);

  const connection = state.desktop;
  let endpoint: string;
  let mode: string;
  if (!isDesktop) {
    endpoint = window.location.origin;
    mode = `${window.location.protocol === "https:" ? "HTTPS" : "HTTP"} · 浏览器直连`;
  } else if (connection?.connectionMode === "ssh") {
    endpoint = `${connection.sshAlias} · 远端端口 ${connection.remotePort}`;
    mode = "SSH 隧道 · 桌面客户端";
  } else if (connection?.connectionMode === "direct") {
    endpoint = connection.serverURL;
    mode = `${new URL(connection.serverURL).protocol === "https:" ? "HTTPS" : "HTTP"} · 桌面客户端直连`;
  } else {
    endpoint = state.loading ? "正在读取连接信息…" : "客户端暂未提供远端连接信息";
    mode = "桌面客户端";
  }
  return <section className="connection-info" aria-label="当前服务器连接">
    <div className="connection-info-heading"><h3>当前连接</h3><span>{mode}</span></div>
    <dl>
      <div><dt>{connection?.connectionMode === "ssh" ? "服务器" : isDesktop ? "服务器地址" : "访问地址"}</dt><dd>{endpoint}</dd></div>
      {debug && <div><dt>实例 ID</dt><dd className="connection-instance-id">{state.instanceID || (state.loading ? "正在确认…" : "暂时无法确认")}</dd></div>}
    </dl>
    {memorySession && <p role="status">钥匙串暂不可用。本次登录仅在 App 打开期间有效，重启后需要重新登录。</p>}
    {isDesktop && <a className="text-button" href="/__desktop/setup">切换服务器</a>}
    {isDesktop && !state.loading && !connection && <p>本地客户端入口：{window.location.origin}。远端地址和连接方式请在客户端的服务器配置中查看。</p>}
  </section>;
}
