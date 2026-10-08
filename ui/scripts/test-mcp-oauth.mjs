import assert from "node:assert/strict";
import { openUiModules } from "./ui-modules.mjs";
const ui = await openUiModules({ language: "zh-CN" });
try {
  const { mcpOAuthRoute } = await ui.load("/src/mcpOAuthRoute.ts");
  const options = { vm_available: true, web_callback_origin: "https://tofi.example", desktop_redirect_uri: "http://127.0.0.1:43821/oauth/callback" };
  assert.equal(mcpOAuthRoute("https:", false, false, options).mode, "web", "HTTPS never prefers an available VM");
  assert.equal(mcpOAuthRoute("http:", true, true, options).mode, "desktop", "native loopback is not an insecure Web session");
  assert.equal(mcpOAuthRoute("https:", true, true, options).mode, "desktop");
  assert.equal(mcpOAuthRoute("http:", true, false, options).mode, "blocked", "old client must update, not silently use VM");
  assert.equal(mcpOAuthRoute("http:", false, false, options).mode, "vm");
  assert.equal(mcpOAuthRoute("http:", false, false, { ...options, vm_available: false }).mode, "blocked");
  assert.equal(mcpOAuthRoute("https:", false, false, { ...options, web_callback_origin: "" }).mode, "blocked");
  assert.equal(mcpOAuthRoute("https:", false, false, null).mode, "blocked");
  assert.equal(mcpOAuthRoute("https:", false, false, options).note, "在当前设备的浏览器登录，回调 Tofi 的 HTTPS 服务器。不使用共享电脑。");
  assert.equal(mcpOAuthRoute("http:", true, false, options).note, "此客户端版本尚不支持本地授权，请升级客户端。不会自动改用共享电脑。");
  assert.equal(mcpOAuthRoute("https:", false, false, null).note, "正在读取授权配置…");
  await ui.setLanguage("en");
  assert.equal(mcpOAuthRoute("http:", false, false, { ...options, vm_available: false }).note, "A plain HTTP connection can't receive the redirect safely, and no shared computer is set up. Use HTTPS or the Tofi app.");
  console.log("MCP OAuth entry routing: PASS");
} finally { await ui.close(); }
