// Shared synthetic fixture for the computer-window browser tests: a Vite dev server
// plus Playwright route stubs for every /api/* call the workspace makes. No live data.
import { dirname } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

export const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const NOW = "2026-10-09T12:00:00Z";
const BOT = { id: "bot-synthetic", name: "Mochi", instructions: "Synthetic bot", model: "default", dm_conversation_id: "conv-synthetic", created_at: NOW };
const CONVERSATION = { id: "conv-synthetic", kind: "dm", name: "Mochi", bot_id: BOT.id, bot_ids: [BOT.id], updated_at: NOW };

/** A synthetic 1280x800 screen, so the frame's natural aspect ratio is 16:10. */
export function screenImage() {
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="1280" height="800"><rect width="1280" height="800" fill="#cfe8f2"/><rect width="1280" height="36" fill="#27465c"/><rect x="120" y="140" width="560" height="360" rx="14" fill="#fff"/><text x="160" y="200" font-size="34" font-family="sans-serif" fill="#27465c">Synthetic screen</text></svg>`;
  return "data:image/svg+xml;base64," + Buffer.from(svg).toString("base64");
}

/** state: { info, capture: "image" | "pending" | "fail", ownership, requests } is mutable by the test. */
export async function installStubs(page, state) {
  await page.route("**/*", async route => {
    const url = new URL(route.request().url());
    if (!url.pathname.startsWith("/api/")) { await route.continue(); return; }
    const json = (body, status = 200) => route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
    const p = url.pathname;
    state.requests?.push(p);
    if (p === "/api/server-info") return json({ service: "tofi", protocol_version: 1, instance_id: "00000000-0000-4000-8000-000000000001", auth: { mode: "none" } });
    if (p === "/api/bots") return json({ bots: [BOT] });
    if (p === "/api/conversations") return json({ conversations: [CONVERSATION] });
    if (p === "/api/computers/firecracker/info") return json(state.info);
    if (p === "/api/computers/firecracker/retry") { state.retries = (state.retries ?? 0) + 1; return json({ accepted: true }); }
    if (p === "/api/computers/firecracker/wake") { state.wakes = (state.wakes ?? 0) + 1; state.onWake?.(); return json({ accepted: true, state: "resuming" }, 202); }
    if (p === "/api/computers/firecracker/actions") {
      if (state.capture === "pending") return; // never answers: the screen is not ready
      if (state.capture === "fail") return json({ error: { message: "Synthetic: desktop is not reachable", code: "unreachable" } }, 500);
      return json({ ok: true, result: { image_url: screenImage() } });
    }
    if (p === "/api/computer/desktop-ownership") return json(state.ownership ?? { owner: null, waiting: [] });
    if (p.endsWith("/messages")) return json({ messages: [], has_more: false, event_cursor: 0 });
    if (p.endsWith("/runs") || p.endsWith("/memories") || p.endsWith("/schedules") || p.endsWith("/work-items")) return json({ runs: [], memories: [], schedules: [], work_items: [] });
    if (p.endsWith("/tools")) return json({ activities: [], summaries: [] });
    if (p.endsWith("/events") || p === "/api/workspace/events") { await route.fulfill({ status: 200, contentType: "text/event-stream", body: ": synthetic\n\n" }); return; }
    if (p === "/api/preferences") return json({ timezone: "UTC", timezone_configured: true });
    return json({});
  });
}

export async function startUi() {
  const { createServer } = await import("vite");
  const { default: react } = await import("@vitejs/plugin-react");
  const server = await createServer({ configFile: false, root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0 }, logLevel: "error" });
  await server.listen();
  return { server, origin: `http://127.0.0.1:${server.httpServer.address().port}` };
}

export async function launch() {
  const module = process.env.PLAYWRIGHT_MODULE || "/Users/jackzhao/Developer/sentiosurge/tofibot-web/node_modules/playwright/index.mjs";
  const { chromium } = await import(pathToFileURL(module));
  return chromium.launch({ headless: true, ...(process.env.TOFI_TEST_CHROME ? { executablePath: process.env.TOFI_TEST_CHROME } : {}) });
}
