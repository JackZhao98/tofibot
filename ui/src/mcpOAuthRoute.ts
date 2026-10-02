export type OAuthOptions = { vm_available: boolean; web_callback_origin: string; desktop_redirect_uri: string };
export type OAuthRoute = { mode: "desktop" | "web" | "vm" | "blocked"; note: string };

export function mcpOAuthRoute(protocol: string, desktop: boolean, nativeOAuth: boolean, options: OAuthOptions | null): OAuthRoute {
  if (desktop) return nativeOAuth
    ? { mode: "desktop", note: "在系统浏览器登录，完成后自动返回此客户端。不使用共享电脑。" }
    : { mode: "blocked", note: "此客户端版本尚不支持本地授权，请升级客户端。不会自动改用共享电脑。" };
  if (protocol === "https:") return options?.web_callback_origin
    ? { mode: "web", note: "在当前设备的浏览器登录，回调 Tofi 的 HTTPS 服务器。不使用共享电脑。" }
    : { mode: "blocked", note: options === null ? "正在读取授权配置…" : "请为服务器配置正确的 TOFI_PUBLIC_ORIGIN（公开 HTTPS 地址）后重试。" };
  return options?.vm_available
    ? { mode: "vm", note: "当前通过 HTTP 直连，仅此入口使用共享电脑授权。服务需支持本地回调；推荐改用 HTTPS 或 Tofi 客户端。" }
    : { mode: "blocked", note: options === null ? "正在读取授权配置…" : "HTTP 直连无法安全回调，且未配置共享电脑。请改用 HTTPS 或 Tofi 客户端。" };
}
