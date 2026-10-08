import {useDebugMode} from "./debugMode";
import { useEffect, useState } from "react";
import "./ConnectionInfo.css";
import { useTranslation } from "./i18n";

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
  const { t } = useTranslation("settings");
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
    mode = t("connection.mode_browser", { protocol: window.location.protocol === "https:" ? "HTTPS" : "HTTP" });
  } else if (connection?.connectionMode === "ssh") {
    endpoint = t("connection.ssh_endpoint", { alias: connection.sshAlias, port: connection.remotePort });
    mode = t("connection.mode_ssh");
  } else if (connection?.connectionMode === "direct") {
    endpoint = connection.serverURL;
    mode = t("connection.mode_desktop_direct", { protocol: new URL(connection.serverURL).protocol === "https:" ? "HTTPS" : "HTTP" });
  } else {
    endpoint = state.loading ? t("connection.loading") : t("connection.unavailable");
    mode = t("connection.mode_desktop");
  }
  return <section className="connection-info" aria-label={t("connection.label")}>
    <div className="connection-info-heading"><h3>{t("connection.title")}</h3><span>{mode}</span></div>
    <dl>
      <div><dt>{connection?.connectionMode === "ssh" ? t("connection.server") : isDesktop ? t("connection.server_address") : t("connection.access_address")}</dt><dd>{endpoint}</dd></div>
      {debug && <div><dt>{t("connection.instance_id")}</dt><dd className="connection-instance-id">{state.instanceID || (state.loading ? t("connection.confirming") : t("connection.unconfirmed"))}</dd></div>}
    </dl>
    {memorySession && <p role="status">{t("connection.memory_session")}</p>}
    {isDesktop && <a className="text-button" href="/__desktop/setup">{t("connection.switch_server")}</a>}
    {isDesktop && !state.loading && !connection && <p>{t("connection.local_entry", { origin: window.location.origin })}</p>}
  </section>;
}
