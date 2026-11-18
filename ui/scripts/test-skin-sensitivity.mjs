// Skin sensitivity guard (V3 PLAN 7.7). The two probe skins (probe-a, probe-b) give every role a different value. Each catalog
// frame is rendered under both, in the same theme and width, and the computed paint of every element is compared. A colour,
// background, border, outline, shadow, fill or stroke that is the same in both runs does not come from a skin role: it is
// hard-coded, and a second skin could not change it.
//
// Skipped (never reported): a value that is transparent or none in both runs; a colour inherited from the parent that is
// equal there (the origin is reported, not every child); a border, outline or shadow that is the element's own text colour
// (currentColor); elements at opacity 0 (hidden native inputs), images, video, canvas, iframes, and cat artwork (svg with data-art); the catalog's own chrome (class catalog-*) and the Mac sidebar's transparent material (EXPECTED).
// Which frames: the catalog sections whose id starts with "v3" (the V3 kit), read from the catalog at run time, so Kits III
// and IV need no edit here. Legacy sections are not expected to respond to the probes. SENSITIVITY_SECTIONS=id1,id2 (or
// "all") picks others.
// Ratchet: findings already present when the guard landed are in scripts/skin-sensitivity-baseline.json (a list of
// "section state | element | property"); a new finding fails, a baseline entry that no longer fires must be removed (--update).
// Usage: PLAYWRIGHT_MODULE=/abs/playwright/index.mjs node scripts/test-skin-sensitivity.mjs [--update | --init] [--verbose]
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const baselineFile = join(ui, "scripts", "skin-sensitivity-baseline.json");
const flag = name => process.argv.includes(name);
if (!process.env.PLAYWRIGHT_MODULE) { console.log("skin-sensitivity: PLAYWRIGHT_MODULE is not set"); process.exit(2); }
const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE).href);
const { createServer } = await import("vite");
const { default: react } = await import("@vitejs/plugin-react");

const LOAD_MS = 120_000;
const WIDTHS = { desktop: { width: 1280, height: 800 }, phone: { width: 390, height: 760 } };
// Elements that are expected to keep one value under every skin: [css selector, properties]. The Mac sidebar is transparent
// over the native material (PLAN 7.4); it only exists with data-desktop="darwin".
const EXPECTED = [[":root[data-desktop='darwin'] [class*='tb-sidebar']", ["background"]]];

/** Runs in the page: one record per painted element (and ::before / ::after with content), in document order. */
const SNAPSHOT = `(expected) => {
  const SKIP_TAG = new Set(["SCRIPT", "STYLE", "LINK", "META", "TITLE", "HEAD", "IMG", "VIDEO", "CANVAS", "PICTURE", "IFRAME", "OBJECT", "SOURCE", "TRACK", "NOSCRIPT"]);
  const SHAPES = new Set(["path", "circle", "rect", "line", "polyline", "polygon", "ellipse", "text", "tspan", "use"]);
  const transparent = value => !value || value === "none" || /^rgba?\\(\\s*\\d+,\\s*\\d+,\\s*\\d+,\\s*0\\s*\\)$/.test(value) || /^transparent$/.test(value);
  const clean = value => value.replace(/rgba\\(\\s*\\d+,\\s*\\d+,\\s*\\d+,\\s*0\\s*\\)/g, "transparent");
  const hasColour = value => /rgb|color\\(|oklch|oklab|lab\\(|lch\\(|hsl|#/.test(clean(value));
  const isChrome = element => element.closest(".catalog-caption, .catalog-note, .catalog-specimen > small, .catalog-error") || [...element.classList].some(name => name.startsWith("catalog-"));
  const label = element => element.tagName.toLowerCase() + ([...element.classList].sort().map(name => "." + name).join("") || "");
  const records = [];
  const walk = (element, parent) => {
    if (SKIP_TAG.has(element.tagName)) return;
    const style = getComputedStyle(element);
    const painted = element.getClientRects().length > 0 && style.visibility !== "hidden" && style.opacity !== "0";
    const inSvg = element.closest("svg") !== null;
    if (painted && !isChrome(element) && !(inSvg && element.closest("svg[data-art]"))) {
      const record = { label: label(element), tag: element.tagName.toLowerCase(), props: {}, inherited: {}, expected: expected.filter(([selector]) => element.matches(selector)).flatMap(([, props]) => props) };
      const parentStyle = parent ? getComputedStyle(parent) : null;
      const colour = style.color;
      // Text colour is judged on any element that shows text (its own or a descendant's): the origin of a hard-coded
      // colour is then found even when a child span carries the words; children that only inherit it are skipped below.
      const ownText = Boolean(element.textContent.trim()) || /^(INPUT|TEXTAREA|SELECT)$/.test(element.tagName);
      if (ownText && !inSvg) { record.props.color = colour; record.inherited.color = parentStyle && parentStyle.color === colour; }
      const background = style.backgroundColor;
      if (!transparent(background)) record.props["background-color"] = background;
      const image = style.backgroundImage;
      if (image !== "none" && /gradient/.test(image)) record.props["background-image"] = image.replace(/url\\([^)]*\\)/g, "url()");
      for (const side of ["top", "right", "bottom", "left"]) {
        const width = parseFloat(style["border-" + side + "-width"]);
        const value = style["border-" + side + "-color"];
        if (width > 0 && style["border-" + side + "-style"] !== "none" && !transparent(value) && value !== colour) record.props["border-" + side + "-color"] = value;
      }
      if (style.outlineStyle !== "none" && parseFloat(style.outlineWidth) > 0 && !transparent(style.outlineColor) && style.outlineColor !== colour) record.props["outline-color"] = style.outlineColor;
      if (style.boxShadow !== "none" && hasColour(style.boxShadow)) record.props["box-shadow"] = clean(style.boxShadow);
      if (style.textShadow !== "none" && hasColour(style.textShadow)) record.props["text-shadow"] = clean(style.textShadow);
      if (inSvg && SHAPES.has(element.tagName.toLowerCase())) {
        if (style.fill !== "none" && !transparent(style.fill) && !/^url/.test(style.fill)) { record.props.fill = style.fill; record.inherited.fill = parentStyle && parentStyle.fill === style.fill; }
        if (style.stroke !== "none" && parseFloat(style.strokeWidth) > 0 && !transparent(style.stroke) && !/^url/.test(style.stroke)) { record.props.stroke = style.stroke; record.inherited.stroke = parentStyle && parentStyle.stroke === style.stroke; }
      }
      records.push(record);
      for (const pseudo of ["::before", "::after"]) {
        const pseudoStyle = getComputedStyle(element, pseudo);
        if (pseudoStyle.content === "none" || pseudoStyle.content === "normal" || pseudoStyle.display === "none") { records.push({ label: label(element) + pseudo, tag: "", props: {}, inherited: {}, expected: [], gap: true }); continue; }
        const rec = { label: label(element) + pseudo, tag: "", props: {}, inherited: {}, expected: [] };
        if (!transparent(pseudoStyle.backgroundColor)) rec.props["background-color"] = pseudoStyle.backgroundColor;
        if (pseudoStyle.backgroundImage !== "none" && /gradient/.test(pseudoStyle.backgroundImage)) rec.props["background-image"] = pseudoStyle.backgroundImage;
        for (const side of ["top", "right", "bottom", "left"]) {
          const width = parseFloat(pseudoStyle["border-" + side + "-width"]);
          const value = pseudoStyle["border-" + side + "-color"];
          if (width > 0 && pseudoStyle["border-" + side + "-style"] !== "none" && !transparent(value) && value !== pseudoStyle.color) rec.props["border-" + side + "-color"] = value;
        }
        if (pseudoStyle.boxShadow !== "none" && hasColour(pseudoStyle.boxShadow)) rec.props["box-shadow"] = clean(pseudoStyle.boxShadow);
        records.push(rec);
      }
    } else records.push({ label: label(element), tag: "", props: {}, inherited: {}, expected: [], gap: true });
    for (const child of element.children) walk(child, element);
  };
  walk(document.body, document.documentElement);
  return records;
}`;

const baseline = existsSync(baselineFile) ? new Set(JSON.parse(readFileSync(baselineFile, "utf8"))) : new Set();
const server = await createServer({ configFile: false, mode: "development", root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0 }, logLevel: "error" });
await server.listen();
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
const browser = await chromium.launch({ headless: true, ...(process.env.CHROME_EXECUTABLE ? { executablePath: process.env.CHROME_EXECUTABLE } : {}) });
const findings = new Map(); // key -> { frame, example }
const problems = [];
let frames = 0, elements = 0, compared = 0;

try {
  const context = await browser.newContext({ viewport: WIDTHS.desktop, locale: "en-US", reducedMotion: "reduce" });
  await context.route("**/*", route => { const url = new URL(route.request().url()); return url.pathname.startsWith("/api/") || url.pathname === "/health" ? route.abort() : route.continue(); });
  const shell = await context.newPage();
  await shell.goto(`${origin}/catalog.html`);
  await shell.waitForFunction(() => Array.isArray(window.__catalog), null, { timeout: LOAD_MS });
  const index = await shell.evaluate(() => window.__catalog);
  await shell.close();

  const wanted = (process.env.SENSITIVITY_SECTIONS ?? "").split(",").filter(Boolean);
  const sections = index.filter(section => wanted.includes("all") || (wanted.length ? wanted.includes(section.id) : /^v3/.test(section.id)));
  if (!sections.length) { console.log(`skin-sensitivity: FAIL no catalog section to probe (looked for ids starting with v3 among ${index.map(section => section.id).join(", ")})`); process.exit(1); }
  const jobs = [];
  for (const section of sections) for (const [width, theme] of [["desktop", "light"], ["desktop", "dark"], ["phone", "light"]]) {
    if (section.states.some(state => state.kind === "inline")) jobs.push({ section, state: null, width, theme });
    for (const state of section.states.filter(state => state.kind !== "inline" && state.widths.includes(width))) jobs.push({ section, state, width, theme });
  }

  async function snapshot(job, skin) {
    const page = await context.newPage();
    const errors = [];
    page.on("pageerror", error => errors.push(error.message));
    await page.setViewportSize(WIDTHS[job.width]);
    const query = new URLSearchParams({ frame: job.section.id, theme: job.theme, lng: "en", skin });
    if (job.state) query.set("state", job.state.id);
    try {
      await page.goto(`${origin}/catalog.html?${query}`);
      await page.waitForFunction(() => document.documentElement.dataset.catalogReady === "1", null, { timeout: LOAD_MS });
      const error = await page.evaluate(() => document.documentElement.dataset.catalogError ?? "");
      if (error) problems.push(`${job.section.id}/${job.state?.id ?? "inline"} ${skin}: ${error}`);
      if (await page.evaluate(() => document.documentElement.dataset.skin) !== skin) problems.push(`${job.section.id}/${job.state?.id ?? "inline"}: the frame did not take skin ${skin}`);
      await page.evaluate(() => document.fonts.ready);
      await page.waitForTimeout(350);
      const records = await page.evaluate(`(${SNAPSHOT})(${JSON.stringify(EXPECTED)})`);
      if (errors.length) problems.push(`${job.section.id}/${job.state?.id ?? "inline"} ${skin}: page error: ${errors.join("; ")}`);
      return records;
    } finally { await page.close(); }
  }

  const queue = [...jobs];
  await Promise.all(Array.from({ length: Number(process.env.SENSITIVITY_PARALLEL ?? 4) }, async () => {
    while (queue.length) {
      const job = queue.shift();
      const name = `${job.section.id}/${job.state?.id ?? "inline"} ${job.theme} ${WIDTHS[job.width].width}`;
      try {
        const [a, b] = await Promise.all([snapshot(job, "probe-a"), snapshot(job, "probe-b")]);
        frames += 1;
        if (a.length !== b.length || a.some((record, i) => record.label !== b[i].label)) { problems.push(`${name}: the DOM differs between the two runs (${a.length} vs ${b.length} nodes); the comparison is not valid`); continue; }
        a.forEach((left, i) => {
          const right = b[i];
          if (left.gap) return;
          elements += 1;
          for (const [property, value] of Object.entries(left.props)) {
            if (right.props[property] !== value) continue;
            if (left.expected.some(name => property.startsWith(name))) continue;
            if (left.inherited[property] && right.inherited[property]) continue;
            compared += 1;
            const key = `${job.section.id}/${job.state?.id ?? "inline"} | ${left.label} | ${property}`;
            if (!findings.has(key)) findings.set(key, { name, value });
          }
        });
      } catch (cause) { problems.push(`${name}: ${cause instanceof Error ? cause.message.split("\n")[0] : cause}`); }
    }
  }));
  await context.close();
} finally {
  await browser.close();
  await server.close();
}

const keys = [...findings.keys()].sort();
if (flag("--init") || flag("--update")) {
  if (problems.length) { console.log(`skin-sensitivity: FAIL\n  ${problems.join("\n  ")}`); process.exit(1); }
  if (flag("--update")) { const fresh = keys.filter(key => !baseline.has(key)); if (fresh.length) { console.log(`skin-sensitivity: FAIL --update refuses new findings:\n  ${fresh.join("\n  ")}`); process.exit(1); } }
  writeFileSync(baselineFile, `${JSON.stringify(keys, null, 2)}\n`);
  console.log(`skin-sensitivity: baseline written, ${keys.length} finding(s)`);
  process.exit(0);
}
const fresh = keys.filter(key => !baseline.has(key)), gone = [...baseline].filter(key => !findings.has(key));
if (flag("--verbose")) for (const key of keys) console.log(`  ${baseline.has(key) ? "known" : "NEW  "} ${key} = ${findings.get(key).value.slice(0, 90)}`);
if (problems.length || fresh.length) {
  console.log("skin-sensitivity: FAIL");
  for (const problem of problems) console.log(`  ${problem}`);
  if (fresh.length) console.log("  hard-coded paint (identical under probe-a and probe-b); use a skin role (var(--...)), not a literal:");
  for (const key of fresh) console.log(`  ${key} = ${findings.get(key).value.slice(0, 120)} (${findings.get(key).name})`);
  process.exit(1);
}
if (gone.length) { console.log(`skin-sensitivity: FAIL ${gone.length} baseline finding(s) no longer fire; lock the gain with --update:\n  ${gone.join("\n  ")}`); process.exit(1); }
console.log(`skin-sensitivity: ${frames} frame(s) under both probes, ${elements} painted element(s) compared; ${keys.length} known finding(s) in the baseline, none new PASS`);
