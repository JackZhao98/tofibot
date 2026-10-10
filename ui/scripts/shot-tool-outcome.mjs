// Screenshots of the tool-outcome states with stub data (1440 and 1024, light and dark)
// to artifacts/tool-outcome/<label>/ (gitignored). Run against any ui root that has
// scripts/tool-outcome-fixture.*: TOOL_OUTCOME_LABEL=before UI_ROOT=/path node scripts/shot-tool-outcome.mjs
import { mkdirSync } from "node:fs";
import { fileURLToPath, pathToFileURL } from "node:url";
import { dirname, join } from "node:path";
import { createServer } from "vite";
import react from "@vitejs/plugin-react";

if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE).href);
const here = dirname(dirname(fileURLToPath(import.meta.url)));
const ui = process.env.UI_ROOT ?? here;
const label = process.env.TOOL_OUTCOME_LABEL ?? "after";
const out = join(here, "..", "artifacts", "tool-outcome", label);
mkdirSync(out, { recursive: true });
const scenes = (process.env.SCENES ?? "incident,recovered,uncertain,live").split(",");
const server = await createServer({ configFile: false, root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0, hmr: false }, logLevel: "error" });
await server.listen();
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });
try {
  for (const width of [1440, 1024]) for (const theme of ["light", "dark"]) for (const scene of scenes) {
    const page = await browser.newPage({ viewport: { width, height: 700 }, colorScheme: theme });
    const status = scene === "live" && label === "before" ? "&status=" + encodeURIComponent("Thinking: I role for myself too—something lik…") : "";
    await page.goto(`${origin}/scripts/tool-outcome-fixture.html?scene=${scene}&theme=${theme}${status}`);
    await page.waitForSelector(".task-run-block");
    for (const summary of await page.$$(".task-activity:not(.is-live)>summary")) await summary.click();
    await page.waitForTimeout(500);
    await page.screenshot({ path: join(out, `${scene}-${width}-${theme}.png`) });
    await page.close();
  }
  console.log("screenshots:", out);
} finally { await browser.close(); await server.close(); }
