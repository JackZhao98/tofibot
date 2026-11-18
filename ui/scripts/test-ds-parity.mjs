// Static-kit parity with the frozen V3.0 spec (V3 PLAN 0.6, 7.7). LOCAL: it needs the spec folder, which is not in the repo.
//   V3_SPEC=/path/to/docs/design/ds-v3-frozen-2026-11-17 PLAYWRIGHT_MODULE=/abs/playwright/index.mjs npm run test:ds-parity
// Each case is rendered twice to static HTML: by the spec's bundle (project/components/bundle.js, React 19 standing in for its
// React 18 global) and by the kit (src/ds, through Vite SSR). Both go in a Chromium page with their own CSS (the spec's
// tokens.css + bundle.css; the V3 skin + the kit's sheets), in light and dark. The two trees are walked in parallel; the tag
// names and the computed layout and paint properties of every element must be equal, except the departures listed below.
import { readFileSync, readdirSync, statSync, existsSync } from "node:fs";
import { dirname, join, relative } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createRequire } from "node:module";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
const spec = process.env.V3_SPEC;
if (!spec || !existsSync(join(spec, "project", "components", "bundle.js"))) { console.log("ds-parity: set V3_SPEC to the frozen spec folder (docs/design/ds-v3-frozen-2026-11-17)"); process.exit(2); }
if (!process.env.PLAYWRIGHT_MODULE) { console.log("ds-parity: PLAYWRIGHT_MODULE is not set"); process.exit(2); }
const require = createRequire(join(ui, "package.json"));
const React = require("react");
const { renderToStaticMarkup } = require("react-dom/server");
globalThis.window = globalThis;
globalThis.React = React;
(0, eval)(readFileSync(join(spec, "project", "components", "bundle.js"), "utf8"));
const T = globalThis.TofiV3;

const D = (c, p = {}, k) => ({ c, p, k });
const input = D("TextInput", { placeholder: "Name", "aria-label": "Name" });
// Cases that exist in both: [name, description]. Callbacks are omitted; buttons that show only with a handler get a no-op by name.
const noop = "fn";
const CASES = [
  ...["primary", "secondary", "ghost", "danger"].flatMap(v => ["md", "sm"].map(s => [`Button ${v} ${s}`, D("Button", { variant: v, size: s }, "Save and restart")])),
  ["Button icons", D("Button", { variant: "primary", icon: "plus", iconAfter: "arrow-right" }, "Create key")],
  ["Button busy", D("Button", { busy: true }, "Saving")],
  ["Button disabled", D("Button", { variant: "primary", disabled: true }, "Save")],
  ...["ghost", "solid"].flatMap(v => ["md", "sm"].map(s => [`IconButton ${v} ${s}`, D("IconButton", { icon: "copy", label: "Copy", variant: v, size: s })])),
  ["IconButton filled", D("IconButton", { icon: "star", label: "Pin", filled: true })],
  ["Icon", D("Icon", { name: "key", size: 20 })],
  ["Icon filled", D("Icon", { name: "key", size: 24, variant: "filled" })],
  ...["neutral", "working", "needs", "done", "error", "unread", "info", "paused"].map(t => [`Badge ${t}`, D("Badge", { tone: t }, "Label")]),
  ["Tag", D("Tag", {}, "read")], ["Tag icon removable", D("Tag", { icon: "file", onRemove: noop, removeLabel: "Remove" }, "PDF")],
  ["Kbd", D("Kbd", {}, "Esc")],
  ...["info", "warning", "error", "done"].map(t => [`Banner ${t}`, D("Banner", { tone: t, title: "Update ready", action: D("Button", { size: "sm" }, "Restart") }, "Restart to use it.")]),
  ["Notice", D("Notice", { icon: "info", action: D("Button", { variant: "ghost", size: "sm" }, "Review") }, "Spend is at 80%.")],
  ...["neutral", "done", "error"].map(t => [`Toast ${t}`, D("Toast", { tone: t, action: "Retry", onAction: noop, onClose: noop, closeLabel: "Dismiss" }, "Saved")]),
  ["Progress", D("Progress", { value: 42, label: "Upload", showValue: true })],
  ["Progress warning", D("Progress", { value: 82, label: "Storage", tone: "warning" })],
  ["Progress danger", D("Progress", { value: 96, tone: "danger" })],
  ["Skeleton", D("Skeleton", { lines: 4, label: "Loading" })],
  ["EmptyState", D("EmptyState", { title: "No keys", icon: "key", action: D("Button", { variant: "primary" }, "Create key") }, "Keys let a service call Tofi.")],
  ...["solid", "raised", "glass", "quiet", "block"].map(v => [`Card ${v}`, D("Card", { variant: v, eyebrow: "Eyebrow", title: "Weekly report", actions: D("Button", { size: "sm", variant: "ghost" }, "Edit"), footer: D("Button", { size: "sm" }, "Open") }, "Body text.")]),
  ["Card flush", D("Card", { pad: false, title: "Flush" }, "Body.")],
  ["TextInput", D("TextInput", { icon: "search", suffix: "MB", defaultValue: "512", "aria-label": "Limit" })],
  ["TextInput mono", D("TextInput", { mono: true, defaultValue: "sk-tofi-9Fq2", "aria-label": "Key" })],
  ["TextArea", D("TextArea", { defaultValue: "Notes", "aria-label": "Notes" })],
  ["Field", D("Field", { label: "Name", optional: "Optional", hint: "One line." }, input)],
  ["Field error", D("Field", { label: "Port", error: "Use 1 to 65535." }, input)],
  ["Checkbox", D("Checkbox", { defaultChecked: false }, "Read")],
  ["Checkbox checked", D("Checkbox", { defaultChecked: true }, "Write")],
  ["Checkbox disabled", D("Checkbox", { disabled: true }, "Admin")],
  ...["mustard", "teal", "grape", "cobalt", "lime", "tomato"].map(t => [`Avatar ${t}`, D("Avatar", { name: "Mia Chen", tone: t })]),
  ["Avatar presence", D("Avatar", { name: "Mia Chen", status: "online", size: 48 })],
  ...["tomato", "mustard", "teal", "grape", "lime", "paper"].map(c => [`BotAvatar ${c}`, D("BotAvatar", { name: "Mochi", coat: c, src: "/brand/tofi-logo-calico.svg" })]),
  ...["thinking", "working", "waiting", "error", "asleep"].map(s => [`BotAvatar ${s}`, D("BotAvatar", { name: "Mochi", coat: "teal", state: s, src: "/brand/tofi-logo-calico.svg" })]),
  ["BotAvatar glyph", D("BotAvatar", { name: "Mochi", coat: "grape" })],
  ["BrandMark", D("BrandMark", { src: "/brand/services/notion.svg", name: "Notion" })],
  ["BrandMark dark", D("BrandMark", { src: "/brand/services/github-inverse.svg", tile: "dark", size: 48 })],
  ["MemberChip", D("MemberChip", { name: "Mia Chen", role: "Owner", onRemove: noop, removeLabel: "Remove Mia", avatar: D("Avatar", { name: "Mia Chen", size: 28, tone: "cobalt" }) })],
];

// Known departures, as [case name pattern, property]. Each is explained in the PR 0.6 report.
const ALLOWED = [
  [/^TextArea/, "gap"], [/^TextArea/, "align-items"],  // flex properties the spec's shared rule leaves on a block element: no effect
  [/^Skeleton/, "opacity"],           // a running animation: sampled at different times
  [/^Checkbox/, "position"],           // the kit anchors the hidden input to its label
  [/^Checkbox/, "margin-top"], [/^Checkbox/, "margin-bottom"],  // the kit zeroes the hidden input's margin (V2 base gives inputs one)
];

// ---- render both sides
const { createServer } = await import("vite");
const { default: react } = await import("@vitejs/plugin-react");
const server = await createServer({ configFile: false, mode: "development", root: ui, plugins: [react()], server: { host: "127.0.0.1", port: 0 }, appType: "custom", logLevel: "error" });
await server.listen();
const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
const { registry } = await server.ssrLoadModule("/scripts/ds-parity-registry.tsx");
const fn = () => {};
const make = (components, desc) => {
  if (typeof desc === "string" || desc == null) return desc;
  const props = Object.fromEntries(Object.entries(desc.p).map(([key, value]) => [key, value === noop ? fn : value && typeof value === "object" && value.c ? make(components, value) : value]));
  const kids = Array.isArray(desc.k) ? desc.k.map(k => make(components, k)) : [make(components, desc.k)];
  return React.createElement(components[desc.c], props, ...kids.filter(k => k != null));
};
const specHtml = [], kitHtml = [];
for (const [name, desc] of CASES) {
  specHtml.push(renderToStaticMarkup(make(T, desc)));
  kitHtml.push(renderToStaticMarkup(make(registry, desc)));
}

// ---- the kit's sheets: every ds/*.css, in a stable order
const walk = dir => readdirSync(dir).flatMap(name => { const path = join(dir, name); return statSync(path).isDirectory() ? walk(path) : [path]; });
const kitSheets = walk(join(ui, "src", "ds")).filter(file => file.endsWith(".css")).sort().map(file => "/" + relative(ui, file));
const specCss = ["tokens.css", "components/bundle.css"].map(file => readFileSync(join(spec, "project", file), "utf8").replace(/@import url\([^)]*\);?/g, "")).join("\n");

const PROPS = ["display", "position", "color", "background-color", "background-image", "border-top-color", "border-right-color", "border-bottom-color", "border-left-color",
  "border-top-width", "border-right-width", "border-bottom-width", "border-left-width", "border-top-style", "border-top-left-radius", "border-bottom-right-radius", "box-shadow", "opacity",
  "font-family", "font-size", "font-weight", "line-height", "letter-spacing", "text-transform", "text-align", "padding-top", "padding-right", "padding-bottom", "padding-left",
  "margin-top", "margin-bottom", "gap", "width", "height", "flex-grow", "flex-shrink", "align-items", "justify-content", "transform", "filter", "animation-name", "outline-style"];

const { chromium } = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE).href);
const browser = await chromium.launch({ headless: true });
const failures = [];
try {
  for (const theme of ["light", "dark"]) {
    const page = await browser.newPage({ viewport: { width: 900, height: 900 } });
    await page.route("**/*", route => route.request().url().startsWith(origin) ? route.continue() : route.abort());
    const html = side => `<!doctype html><html data-theme="${theme}" ${side === "kit" ? 'data-skin="v3"' : ""} style="color-scheme:${theme}"><head><meta charset="utf-8">${side === "spec" ? `<style>${specCss}</style>` : ["/src/skin/structure.css", "/src/skin/v3.css", ...kitSheets].map(href => `<link rel="stylesheet" href="${href}">`).join("")}</head>
      ${side === "kit" ? "<style>*,*::before,*::after{box-sizing:border-box}body{font:400 15px/1.6 var(--font-body);color:var(--ink)}</style>" : ""}
      <body style="margin:0;background:var(--bg)">${(side === "spec" ? specHtml : kitHtml).map((markup, i) => `<div class="case" data-i="${i}" style="padding:10px;width:520px">${markup}</div>`).join("")}</body></html>`;
    const measure = async side => {
      await page.route(`${origin}/__${side}`, route => route.fulfill({ contentType: "text/html", body: html(side) }));
      await page.goto(`${origin}/__${side}`);
      await page.waitForLoadState("networkidle");
      // The product's TofiIcon wraps the drawing in <g> groups (motion hooks); they draw nothing, so they are skipped.
      return page.evaluate(props => [...document.querySelectorAll(".case")].map(box => [...box.querySelectorAll("*:not(g)")].map(el => {
        const style = getComputedStyle(el);
        const rect = el.getBoundingClientRect();
        return { tag: el.tagName.toLowerCase(), values: { ...Object.fromEntries(props.map(p => [p, style.getPropertyValue(p)])), width: `${Math.round(rect.width)}`, height: `${Math.round(rect.height)}` } };
      })), PROPS);
    };
    const [a, b] = [await measure("spec"), await measure("kit")];
    CASES.forEach(([name], i) => {
      const left = a[i], right = b[i];
      if (left.length !== right.length || left.some((el, j) => el.tag !== right[j].tag)) { failures.push(`${theme} ${name}: element trees differ (${left.map(e => e.tag).join(" ")} vs ${right.map(e => e.tag).join(" ")})`); return; }
      left.forEach((el, j) => {
        for (const prop of PROPS) {
          if (el.values[prop] === right[j].values[prop]) continue;
          if (ALLOWED.some(([pattern, property]) => pattern.test(name) && property === prop)) continue;
          failures.push(`${theme} ${name}: <${el.tag}> #${j} ${prop}: spec ${el.values[prop]} vs kit ${right[j].values[prop]}`);
        }
      });
    });
    await page.close();
  }
} finally { await browser.close(); await server.close(); }
for (const failure of failures.slice(0, 80)) console.log(`FAIL ${failure}`);
if (failures.length) { console.log(`ds-parity: ${failures.length} difference(s) over ${CASES.length} cases`); process.exit(1); }
console.log(`ds-parity: PASS (${CASES.length} cases, light and dark)`);
