// Settings polish audit: opens every Settings tab and sub-state (stubbed API, synthetic data), at 1440x900 and
// 390x844, light and dark, scrolls the whole page, and fails on UI that looks like raw browser fallback or is misaligned.
//
//   PLAYWRIGHT_MODULE=/path/to/playwright/index.mjs npm run test:settings-polish
//   POLISH_SHOTS=<dir>   also saves a full-page screenshot of every scenario (and violations.json) there
//   POLISH_ONLY=<regex>  only scenarios whose id matches;  POLISH_VIEWPORTS=1440,390;  POLISH_THEMES=light,dark
//
// Rules (a visible element is one with a layout box, visibility visible, and not visually hidden):
//  native-checkbox / native-radio / native-select / native-input-<type>
//        computed `appearance` must be none (checkbox, radio, select, range, number, date-like inputs)
//  native-file   an <input type=file> must be visually hidden (<= 2px, clipped, or opacity 0) behind a styled button
//  native-button a <button> must not render with the UA button look (outset border or the UA background)
//  button-class  a <button> needs a design-system class (ALLOWED_BUTTON_CLASSES) or must live in an allowed container
//  font-family   text and controls must compute to one of the --body / --display / --mono stacks
//  card-padding  text or a control whose left edge is < 12px from the inner edge of its card (SettingsCard or any
//                bordered, rounded, >= 200x56 box); a full-width button row is judged by its own left padding
//  clipped-scroll a scrollable box inside a card whose content is cut off (render the full list instead)
//  multi-details / floating-details  at most one "Details" disclosure per card, never outside a card
//  overflow      390px only: the document, or any visible element, is wider than the viewport or its clipping parent
//  font-size     text under 12px, except the explicit exception: mono, >= 10px, and either uppercase with letter
//                spacing (section/danger labels) or the nav count pill (.settings-count)
import assert from "node:assert/strict";
import {mkdtemp, mkdir, readFile, rm, writeFile} from "node:fs/promises";
import {basename, dirname, join} from "node:path";
import {fileURLToPath, pathToFileURL} from "node:url";

const ui = dirname(dirname(fileURLToPath(import.meta.url)));
if (!process.env.PLAYWRIGHT_MODULE) { console.log("SKIP: PLAYWRIGHT_MODULE is not set"); process.exit(0); }
const {chromium} = await import(pathToFileURL(process.env.PLAYWRIGHT_MODULE));
const {createServer} = await import("vite");
const {default: react} = await import("@vitejs/plugin-react");

// The single allowed set of design-system button classes (all defined in the shared CSS), plus containers whose
// buttons are styled as a group (segmented control, tab strips, the settings rail).
const ALLOWED_BUTTON_CLASSES = ["primary-button", "secondary-button", "text-button", "settings-ghost-button", "admin-danger-button", "purge-button", "close-button", "settings-banner-more", "mcp-button", "settings-mrow", "settings-back", "admin-row", "disclosure-toggle", "integration-tile", "integration-custom", "extension-back", "settings-dismiss", "danger-link", "onb-choice", "onb-quiet", "onb-skip", "onb-later", "onb-tile", "onb-service-main", "onb-primary", "update-banner-dismiss"];
const ALLOWED_BUTTON_CONTAINERS = [".segmented", ".settings-tabs", ".settings-segment", ".credential-tabs", ".appearance-options", ".onb-seg", ".emoji-mart", ".usage-agents", ".usage-ranges"];

const VIEWPORTS = (process.env.POLISH_VIEWPORTS ?? "1440,390").split(",").map(width => width === "1440" ? ["1440", 1440, 900] : ["390", 390, 844]);
const THEMES = (process.env.POLISH_THEMES ?? "light,dark").split(",");
const only = process.env.POLISH_ONLY ? new RegExp(process.env.POLISH_ONLY) : null;
const shots = process.env.POLISH_SHOTS;
if (shots) await mkdir(shots, {recursive: true});

/** Runs inside the page. Returns violations for everything visible under the settings shell, sheets and dialogs. */
function auditPage(opts) {
  const {mobile, allowedClasses, allowedContainers} = opts;
  const found = [];
  const roots = [...document.querySelectorAll(".settings-shell, .sheet-overlay, dialog[open], [data-onboarding]")].filter(root => !root.parentElement?.closest(".settings-shell, .sheet-overlay, dialog[open], [data-onboarding]"));
  const inRoots = el => roots.some(root => root.contains(el));
  const name = el => {
    const part = node => node.tagName.toLowerCase() + (node.id ? "#" + node.id : "") + [...node.classList].slice(0, 2).map(c => "." + c).join("");
    const chain = [];
    for (let node = el, depth = 0; node && node.nodeType === 1 && depth < 3; node = node.parentElement, depth++) { chain.unshift(part(node)); if (node.id) break; }
    return chain.join(" > ");
  };
  const add = (kind, el, detail) => found.push({kind, selector: name(el), detail});
  const stack = family => { const probe = document.createElement("span"); probe.style.fontFamily = family; document.body.append(probe); const value = getComputedStyle(probe).fontFamily; probe.remove(); return value; };
  const stacks = {body: stack("var(--body)"), display: stack("var(--display)"), mono: stack("var(--mono)")};
  const stackNames = new Set(Object.values(stacks));
  const style = el => getComputedStyle(el);
  const hiddenByTricks = (el, s) => {
    const r = el.getBoundingClientRect();
    return r.width <= 2 || r.height <= 2 || parseFloat(s.opacity) < 0.05 || (s.clip && s.clip !== "auto" && /rect\(\s*0/.test(s.clip)) || s.clipPath === "inset(50%)";
  };
  const visible = el => {
    if (!el.getClientRects().length) return false;
    const s = style(el);
    if (s.visibility === "hidden" || s.display === "none") return false;
    for (let node = el; node && node !== document.body; node = node.parentElement) { if (node.hidden || parseFloat(style(node).opacity) < 0.02) return false; }
    return true;
  };
  // Native "UA look" reference values, from an element reverted to user-agent styles.
  const reference = tag => { const probe = document.createElement(tag); probe.style.all = "revert"; probe.style.position = "absolute"; probe.style.left = "-9999px"; document.body.append(probe); const s = style(probe); const out = {background: s.backgroundColor, border: s.borderTopStyle}; probe.remove(); return out; };
  const nativeButton = reference("button");
  const CARD_BORDERED = el => {
    const s = style(el);
    if (el.matches("button, input, select, textarea, li, a, dd, dt, .segmented, .status-badge, .settings-count")) return false;
    if (parseFloat(s.borderLeftWidth) < 1 || s.borderLeftStyle === "none") return false;
    if (parseFloat(s.borderTopLeftRadius) < 8) return false;
    const r = el.getBoundingClientRect();
    return r.width >= 200 && r.height >= 56;
  };
  const cardOf = el => { for (let node = el.parentElement; node && node !== document.body; node = node.parentElement) { if (node.matches(".settings-card, .danger-zone, .sheet, .settings-banner, .owner-account, .extension-card") || CARD_BORDERED(node)) return node; } return null; };
  const innerLeft = card => card.getBoundingClientRect().left + parseFloat(style(card).borderLeftWidth);
  const innerRight = card => card.getBoundingClientRect().right - parseFloat(style(card).borderRightWidth);

  // ---- controls
  for (const el of document.querySelectorAll("button, input, select, textarea")) {
    if (!inRoots(el) || !el.getClientRects().length) continue;
    const s = style(el), tag = el.tagName.toLowerCase(), type = (el.getAttribute("type") || "text").toLowerCase();
    const hidden = hiddenByTricks(el, s) || s.visibility === "hidden" || s.display === "none" || el.hidden;
    if (tag === "input" && type === "file") { if (!hidden && visible(el)) add("native-file", el, "file input is visible; hide it behind a styled button"); continue; }
    if (hidden || !visible(el)) continue;
    const family = s.fontFamily;
    if (!stackNames.has(family)) add("font-family", el, family.slice(0, 60));
    if (tag === "input" && (type === "checkbox" || type === "radio") && s.appearance !== "none") add("native-" + type, el, `appearance: ${s.appearance}`);
    else if (tag === "select" && s.appearance !== "none") add("native-select", el, `appearance: ${s.appearance}`);
    else if (tag === "input" && ["range", "number", "date", "time", "datetime-local", "month", "week", "color"].includes(type) && s.appearance !== "none") add("native-input-" + type, el, `appearance: ${s.appearance}`);
    if (tag === "button") {
      if (/^(outset|inset|ridge|groove)$/.test(s.borderTopStyle) || (s.backgroundColor === nativeButton.background && s.borderTopStyle === nativeButton.border && s.borderTopStyle !== "none")) add("native-button", el, `bg ${s.backgroundColor}, border ${s.borderTopStyle}`);
      const classed = allowedClasses.some(c => el.classList.contains(c)) || allowedContainers.some(sel => el.closest(sel));
      if (!classed) add("button-class", el, `classes: ${el.className || "(none)"}`);
    }
    // left padding against the card (a full-width row button is judged by its own padding instead)
    if (!(tag === "input" && (type === "checkbox" || type === "radio") && el.closest("label") && false)) {
      const card = cardOf(el);
      if (card) {
        const r = el.getBoundingClientRect(), gap = r.left - innerLeft(card);
        if (gap < 12 - 0.5) {
          const fullRow = tag === "button" && innerRight(card) - r.right < 12;
          const own = parseFloat(s.paddingLeft) + parseFloat(s.borderLeftWidth);
          if (!fullRow || gap + own < 12 - 0.5) add("card-padding", el, `${gap.toFixed(1)}px from ${name(card)}`);
        }
      }
    }
  }

  // ---- text
  const walker = document.createTreeWalker(document.body, NodeFilter.SHOW_TEXT);
  const seenText = new Set();
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const text = node.nodeValue.trim();
    const parent = node.parentElement;
    if (!text || !parent || !inRoots(parent) || parent.closest("script, style, option, svg, noscript")) continue;
    if (!visible(parent)) continue;
    const range = document.createRange(); range.selectNodeContents(node);
    const rect = range.getBoundingClientRect();
    if (rect.width < 1 || rect.height < 1) continue;
    const s = style(parent);
    if (hiddenByTricks(parent, s) && parent.getBoundingClientRect().width <= 2) continue;
    const size = parseFloat(s.fontSize);
    const mono = s.fontFamily === stacks.mono;
    const uppercaseTracked = s.textTransform === "uppercase" && parseFloat(s.letterSpacing) > 0;
    const exception = mono && size >= 10 && (uppercaseTracked || parent.closest(".settings-count"));
    if (size < 12 && !exception) add("font-size", parent, `${size}px "${text.slice(0, 30)}"`);
    if (!stackNames.has(s.fontFamily)) add("font-family", parent, `${s.fontFamily.slice(0, 60)} "${text.slice(0, 24)}"`);
    if (parent.closest("button, select, textarea, option, label:has(input[type=checkbox]), label:has(input[type=radio])")) continue;
    const card = cardOf(parent);
    if (card) {
      const gap = rect.left - innerLeft(card);
      if (gap < 12 - 0.5 && !seenText.has(parent)) { seenText.add(parent); add("card-padding", parent, `text "${text.slice(0, 30)}" ${gap.toFixed(1)}px from ${name(card)}`); }
    }
  }
  // labels that wrap a checkbox/radio: judge the label box
  for (const label of document.querySelectorAll("label")) {
    if (!inRoots(label) || !visible(label) || !label.querySelector("input[type=checkbox], input[type=radio]")) continue;
    const card = cardOf(label); if (!card) continue;
    const box = label.getBoundingClientRect(), gap = box.left - innerLeft(card);
    const fullRow = innerRight(card) - box.right < 12;
    const own = fullRow ? parseFloat(style(label).paddingLeft) : 0; // a full-width row is judged by its own padding
    if (gap + own < 12 - 0.5) add("card-padding", label, `${gap.toFixed(1)}px from ${name(card)}`);
  }

  // ---- clipped inner scroll boxes: a scrollable element inside a card must show all of its content
  for (const el of document.querySelectorAll("*")) {
    if (!inRoots(el) || el.matches("textarea, input, select, svg, pre, .settings-page-body, .settings-tabs, .settings-mhome, .sheet, .onb-sheet, .onb-body, .onb-step") || !visible(el)) continue;
    if (!/auto|scroll/.test(style(el).overflowY) || !cardOf(el)) continue;
    if (el.scrollHeight > el.clientHeight + 2) add("clipped-scroll", el, `scrollHeight ${el.scrollHeight}px > clientHeight ${el.clientHeight}px`);
  }
  // ---- "Details" disclosures: at most one per card, always inside a card
  const perCard = new Map();
  for (const toggle of document.querySelectorAll(".disclosure-toggle")) {
    if (!inRoots(toggle) || !visible(toggle) || toggle.textContent.trim() !== "Details") continue;
    const card = cardOf(toggle);
    if (!card) { add("floating-details", toggle, "a Details disclosure outside any card"); continue; }
    perCard.set(card, (perCard.get(card) ?? 0) + 1);
  }
  for (const [card, count] of perCard) if (count > 1) add("multi-details", card, `${count} Details disclosures in one card`);

  // ---- overflow (phones)
  if (mobile) {
    const doc = document.documentElement;
    if (doc.scrollWidth > window.innerWidth + 1) add("overflow", doc, `document ${doc.scrollWidth}px > viewport ${window.innerWidth}px`);
    for (const el of document.querySelectorAll("*")) {
      if (!inRoots(el) || el.closest("svg") || !visible(el)) continue;
      const r = el.getBoundingClientRect();
      if (r.width === 0 || r.height === 0) continue;
      let limit = window.innerWidth, clipper = null;
      for (let node = el.parentElement; node && node !== document.body; node = node.parentElement) {
        const o = style(node).overflowX;
        if (o === "hidden" || o === "clip" || o === "auto" || o === "scroll") { clipper = node; limit = Math.min(limit, node.getBoundingClientRect().right); break; }
      }
      if (clipper && /auto|scroll/.test(style(clipper).overflowX) && !clipper.matches(".settings-page-body, .sheet, .settings-content, .onb-sheet, [data-onboarding]")) continue; // an intentional horizontal scroller
      if (r.right > limit + 1 && r.left < window.innerWidth) add("overflow", el, `right edge ${Math.round(r.right)}px > ${Math.round(limit)}px`);
    }
  }
  return found;
}

// ---------------------------------------------------------------------------------------------------------------
const fixture = await mkdtemp(join(ui, ".settings-polish-"));
let server, browser;
const violations = [];
const warnings = [];
try {
  const foundations = await readFile(join(ui, "src/v2-foundations.css"), "utf8");
  await writeFile(join(fixture, "foundations.css"), foundations.replace(/^@import url\("https:\/\/fonts\.googleapis\.com\/[^"\n]+"\);\r?\n/m, ""));
  const main = await readFile(join(ui, "src/main.tsx"), "utf8");
  const styles = [...main.matchAll(/^import "\.\/([\w/-]+\.css)";$/gm)].map(match => match[1] === "v2-foundations.css" ? "import './foundations.css';" : `import '../src/${match[1]}';`).join("");
  await writeFile(join(fixture, "index.html"), '<!doctype html><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><script type="module" src="./main.tsx"></script>');
  await writeFile(join(fixture, "main.tsx"), `${styles}
import React,{useEffect,useState} from 'react';import {createRoot} from 'react-dom/client';
import {i18nReady,setLanguage} from '../src/i18n';
import {TimezoneProvider} from '../src/UserTimezone';
import {OwnerSessionGate} from '../src/OwnerSession';
import {useAppearance} from '../src/InteractionSystem';
import {SettingsShell,type SettingsEntry,type SettingsTab,type SettingsView} from '../src/SettingsShell';
import {SettingsPages} from '../src/settings/SettingsPages';
import {subscribeSettingsDeepLinks} from '../src/settings/deepLinks';
import {CodexPanel,NotificationSetting} from '../src/App';
import {UpdateBanner} from '../src/UpdateNotice';
const query=new URLSearchParams(location.search);
const bots=[{id:'bot_research',name:'Research Bot',archived:false,model:'default',reasoning_effort:'default'},{id:'bot_inbox',name:'Inbox Bot',archived:false,model:'gpt-6-luna',reasoning_effort:'medium'},{id:'bot_notes',name:'Notes Bot',archived:false,model:'default',reasoning_effort:'default'},{id:'bot_old',name:'Retired Bot',archived:true,model:'default',reasoning_effort:'default'}] as any;
function Fixture(){
 const appearance=useAppearance();
 const [tab,setTab]=useState<SettingsTab>((query.get('tab') as SettingsTab)||'general');
 const [entry,setEntry]=useState<SettingsEntry>({seq:0,view:(query.get('view') as SettingsView)||'home'});
 const [closed,setClosed]=useState(false);
 const [file,setFile]=useState<File|undefined>(undefined);
 const openSettings=(next:SettingsTab,view:SettingsView='page')=>{setTab(next);setEntry(current=>({seq:current.seq+1,view}))};
 useEffect(()=>subscribeSettingsDeepLinks(next=>openSettings(next)),[]);
 return <div className="workspace"><UpdateBanner/><div className="workspace-grid"><button type="button" className="detail-backdrop" aria-label="Close"/><aside className="detail-pane visible" role="dialog" aria-modal="true">{!closed&&<SettingsShell tab={tab} onTab={setTab} onClose={()=>setClosed(true)} entry={entry} refreshToken={0} renderPage={page=><SettingsPages page={page} bots={bots} timezone="America/Los_Angeles" usageBotId="" portabilityBotID="" portabilityFile={file} onPortabilityFileConsumed={()=>setFile(undefined)} appearance={appearance} extensionRefresh={0} openTab={next=>openSettings(next)} slots={{codex:<CodexPanel refreshToken={0} onConfigured={()=>{}}/>,notifications:<NotificationSetting/>,providersRefresh:0,onProvidersConfigured:()=>{},legacyArchive:null}}/>}/>}</aside></div></div>;
}
void i18nReady.then(()=>setLanguage((query.get('lang') as any)||'en')).then(()=>createRoot(document.getElementById('root')!).render(<TimezoneProvider><OwnerSessionGate><Fixture/></OwnerSessionGate></TimezoneProvider>));
`);
  server = await createServer({configFile: false, root: ui, plugins: [react()], server: {host: "127.0.0.1", port: 0, hmr: false}, logLevel: "error"});
  await server.listen();
  const origin = `http://127.0.0.1:${server.httpServer.address().port}`;
  browser = await chromium.launch({executablePath: process.env.TOFI_TEST_CHROME || undefined});

  // ---- Stubbed API: neutral sample data only.
  const INSTANCE = "0b1c2d3e-4f50-4a6b-8c7d-9e0f1a2b3c4d";
  const day = n => new Date(Date.UTC(2026, 9, 9 - n)).toISOString();
  const providers = [
    {id: "codex", label: "Codex", kind: "oauth", configured: true, status: "connected"},
    {id: "openai", label: "OpenAI", kind: "api_key", configured: true, key_hint: "…9fa2", verified_at: "2026-10-08T16:05:00Z", error: ""},
    {id: "anthropic", label: "Claude", kind: "api_key", configured: false, key_hint: "", verified_at: "", error: ""},
  ];
  const models = [
    {id: "codex-gpt-6-sol", name: "Codex · GPT-6 Sol", provider: "codex", reasoning_efforts: ["low", "medium", "high"], default_reasoning: "medium"},
    {id: "gpt-6-luna", name: "GPT-6 Luna", provider: "openai", reasoning_efforts: ["low", "medium", "high", "xhigh", "max"], default_reasoning: "medium"},
    {id: "claude-opus-5-5", name: "Claude Opus 5.5", provider: "anthropic", reasoning_efforts: ["low", "medium", "high", "xhigh"], default_reasoning: "high"},
  ];
  const GIB = 2 ** 30;
  const jsonAnswer = (status, body) => ({status, body});
  function createAdmin() {
  const adminState = {};
  function resetAdmin() {
    adminState.accounts = [
      {id: "acct_sample", username: "Ada Sample", email: "ada@example.test", role: "admin", disabled: false, must_change_password: false, deleting: false},
      {id: "acct_two", username: "Sam Example", email: "sam@example.test", role: "user", disabled: false, must_change_password: false, deleting: false},
      {id: "acct_three", username: "Lee Example", email: "lee@example.test", role: "user", disabled: true, must_change_password: false, deleting: false},
      {id: "acct_four", username: "Max Example", email: "acct_four@account.invalid", role: "user", disabled: true, must_change_password: false, deleting: true, delete_step: "computer", delete_error: "computer_unavailable"},
    ];
    adminState.disks = {acct_sample: [16, "ready"], acct_two: [8, "ready"], acct_three: [8, "disabled"], acct_four: [8, "disabled"]};
    adminState.exports = [
      {id: "exp_old", username: "Kim Example", email: "kim@example.test", created_at: Math.floor(Date.parse("2026-09-30T10:00:00Z") / 1000), expires_at: Math.floor(Date.parse("2026-10-30T10:00:00Z") / 1000), size: 3_400_000, link_path: "/exports/" + "A".repeat(43), passphrase_pending: false},
      {id: "exp_new", username: "Joy Example", email: "", created_at: Math.floor(Date.parse("2026-10-08T10:00:00Z") / 1000), expires_at: Math.floor(Date.parse("2026-11-07T10:00:00Z") / 1000), size: 820_000, link_path: "/exports/" + "B".repeat(43), passphrase_pending: true},
    ];
    adminState.failDelete = 0;
  }
  resetAdmin();
  function adminStub(url, method, postData) {
    const path = url.pathname, bodyOf = () => { try { return JSON.parse(postData || "{}"); } catch { return {}; } };
    if (path === "/api/admin/accounts" && method === "GET") return jsonAnswer(200, adminState.accounts);
    if (path === "/api/admin/capacity") {
      const accounts = adminState.accounts.filter(a => adminState.disks[a.id]).map(a => ({account_id: a.id, quota_bytes: adminState.disks[a.id][0] * GIB, logical_bytes: adminState.disks[a.id][0] * GIB, state: adminState.disks[a.id][1], pending_quota: false}));
      return jsonAnswer(200, {total_bytes: 500 * GIB, available_bytes: 300 * GIB, allocated_bytes: 100 * GIB, promised_bytes: accounts.reduce((sum, a) => sum + a.quota_bytes, 0), admission_remaining_bytes: 120 * GIB, warning: false, accounts});
    }
    if (path === "/api/admin/deleted-exports" && method === "GET") return jsonAnswer(200, adminState.exports.map(({passphrase, ...rest}) => rest));
    const match = /^\/api\/admin\/accounts\/([^/]+)$/.exec(path);
    if (match && method === "DELETE") {
      const account = adminState.accounts.find(a => a.id === match[1]);
      if (!account) return jsonAnswer(404, {error: {code: "not_found", message: "account not found"}});
      if (adminState.failDelete > 0) { adminState.failDelete--; account.deleting = true; account.delete_step = "computer"; account.delete_error = "computer_unavailable"; return jsonAnswer(503, {error: {code: "account_delete_failed", message: "stopped"}}); }
      adminState.accounts = adminState.accounts.filter(a => a !== account);
      const created = Math.floor(Date.parse("2026-10-09T12:00:00Z") / 1000);
      return jsonAnswer(200, {id: account.id, deleted: true, export: {id: "exp_x", username: account.username, email: account.email, created_at: created, expires_at: created + 30 * 86400, size: 1_250_000, link_path: "/exports/" + "C".repeat(43), passphrase: "ABCD-EFGH-IJKL-MNOP-QRST-UVWX-YZ23-4567", passphrase_pending: true}});
    }
    if (match && method === "PATCH") return jsonAnswer(200, adminState.accounts.find(a => a.id === match[1]));
    if (/\/passphrase$/.test(path)) return jsonAnswer(200, {passphrase: "ABCD-EFGH-IJKL-MNOP-QRST-UVWX-YZ23-4567"});
    return jsonAnswer(404, {error: {code: "not_found", message: "not stubbed"}});
  }
  return {adminState, adminStub};
  }
  const portableBots = [{id: "bot_research", name: "Research Bot", archived: false}, {id: "bot_inbox", name: "Inbox Bot", archived: false}, {id: "bot_notes", name: "Notes Bot", archived: false}, {id: "bot_old", name: "Retired Bot", archived: true}];
  const stub = (url, method, query) => {
    const path = url.pathname;
    if (path === "/api/server-info") return {service: "tofi", protocol_version: 1, instance_id: INSTANCE, auth: {mode: "owner"}};
    if (path === "/api/system/update") return {current: "v0.1.0", latest: "v0.1.1", update_available: true, notes_url: "https://github.com/example/tofibot/releases/tag/v0.1.1", checked_at: "2026-10-09T09:30:00Z", auto_update: "off"};
    if (path === "/api/auth/session") return {enabled: true, setup_required: false, authenticated: true, password_transport_allowed: true, multi_account: true, owner: {id: "acct_sample", username: "Ada Sample", email: "ada@example.test", role: "admin"}};
    if (path === "/api/bots") return {bots: portableBots};
    if (path === "/api/preferences") return {timezone: "America/Los_Angeles", timezone_configured: true};
    if (path === "/api/extensions/mcp" && method === "GET") return {servers: [
      {name: "acme_docs", url: "https://mcp.example.test/docs", oauth: {client_id: "sample", connected: false}},
      {name: "acme_tracker", url: "https://mcp.example.test/tracker", oauth: {client_id: "sample", connected: true}},
      {name: "notes_search", url: "https://mcp.example.test/notes", headers: {}},
    ]};
    if (/^\/api\/extensions\/mcp\/[^/]+\/test$/.test(path) && method === "POST") return {ok: true, tool_count: 8};
    if (path === "/api/extensions/oauth-options") return {vm_available: false, web_callback_origin: "https://tofi.example.test", desktop_redirect_uri: ""};
    if (path === "/api/extensions/local-mcp") return {available: false, reason: "sample"};
    if (path === "/api/extensions/skills") return {skills: [{name: "research-assistant", description: "Research and verify sources", access: {mode: "all"}}, {name: "weekly-report", description: "Draft the weekly status report", access: {mode: "selected", bot_ids: ["bot_research"]}}]};
    if (path === "/api/auto-review-settings") return {mode: "shadow", revision: 1, review_scope: "all_external_tools"};
    if (path === "/api/providers") return {providers};
    if (path === "/api/auth/codex") return {connected: true, expires_at: Date.parse("2026-11-01T00:00:00Z")};
    if (path === "/api/auth/codex/verify") return {connected: true, expires_at: Date.parse("2026-11-01T00:00:00Z"), check: "ok"};
    if (path === "/api/models") return {models, source: "live", warning: ""};
    if (path === "/api/model-settings") return {model: "codex-gpt-6-sol", reasoning_effort: "medium"};
    if (path === "/api/dictation-settings") return {model: "gpt-4o-mini-transcribe", configured: true, auth_source: "api_key", models: [{id: "gpt-4o-mini-transcribe", name: "GPT-4o mini Transcribe"}, {id: "gpt-4o-transcribe", name: "GPT-4o Transcribe"}]};
    if (path === "/api/computers/firecracker/info") return {state: "ready", workspace_root: "/workspace", browser: "Chromium"};
    if (path === "/api/computers") return {computers: [{id: "dev_sample", name: "Sample laptop", kind: "mac", online: true}]};
    if (path === "/api/computer/resources") return {current: {vcpus: 2, memory_mib: 4096, disk_gib: 20}, desired: {vcpus: 2, memory_mib: 4096, disk_gib: 20}, pending: false, state: "ready", host: {cpus: 8, memory_total_mib: 16384, memory_available_mib: 9000, disk_available_gib: 120}, limits: {max_vcpus: 6, max_memory_mib: 12288, max_disk_gib: 100}};
    if (path === "/api/computer/credentials") return {credentials: [{id: "cred_1", name: "WEB_API_TOKEN", kind: "env", target: "WEB_API_TOKEN", created_at: day(3)}, {id: "cred_2", name: "DEPLOY_REGION", kind: "env", target: "DEPLOY_REGION", created_at: day(5)}]};
    if (path === "/api/computer/ssh-keys") return {keys: [{name: "id_ed25519_sample", has_private: true, fingerprint: "SHA256:sampleFingerprint0000000000000000000000000", public_key: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIsample sample@example.test", public_key_verified: true}]};
    if (path === "/api/portability/environment") return {records: [{id: "env_1", name: "WEB_API_TOKEN", target: "WEB_API_TOKEN", source_category: "active_vault"}, {id: "env_2", name: "DEPLOY_REGION", target: "DEPLOY_REGION", source_category: "inactive_recovery", bytes: 12}]};
    if (path === "/api/portability/preview") return {preview_id: "prev_1", counts: {bot_config: 2, chats: 14, memories: 6}, bots: [{id: "bot_research", name: "Research Bot"}, {id: "bot_inbox", name: "Inbox Bot"}], conflicts: ["Timezone will change to Europe/Berlin"], warnings: ["Chat text may contain pasted secrets"], excluded: [], expires_at: "2026-10-09T10:00:00Z", estimated_bytes: 20480, attachment_bytes: 2048, can_apply: true, dependencies: ["Bot configurations needed by history references"]};
    if (path === "/api/usage") return {contexts: [{run_id: "run_1", conversation_id: "conv_1", conversation_name: "Weekly summary", bot_id: "bot_research", bot_name: "Research Bot", run_status: "completed", model: "codex-gpt-6-sol", window_tokens: 200000, window_known: true, compact_at: 160000, estimated_input: 52000, last_input: 48000, total_input: 310000, total_output: 42000, compact_count: 1, updated_at: day(0)}],
      agents: [{bot_id: "bot_research", bot_name: "Research Bot", input_tokens: 310000, output_tokens: 42000, runs: 14, compactions: 1}, {bot_id: "bot_inbox", bot_name: "Inbox Bot", input_tokens: 120000, output_tokens: 18000, runs: 6, compactions: 0}],
      periods: {"24h": [{bot_id: "bot_research", input_tokens: 90000, output_tokens: 12000, requests: 5, equivalent_usd: 0.42, unpriced_requests: 0}], "7d": [{bot_id: "bot_research", input_tokens: 310000, output_tokens: 42000, requests: 14, equivalent_usd: 1.9, unpriced_requests: 0}], "30d": [{bot_id: "bot_research", input_tokens: 310000, output_tokens: 42000, requests: 14, equivalent_usd: 1.9, unpriced_requests: 0}]},
      calls: [{id: 1, bot_id: "bot_research", bot_name: "Research Bot", conversation_name: "Weekly summary", model: "codex-gpt-6-sol", input_tokens: 18000, output_tokens: 2100, equivalent_usd: 0.09, price_known: true, trigger_content: "Summarize the week", occurred_at: day(0)}],
      price_source: "sample", price_as_of: "2026-10-01", note: ""};
    return undefined;
  };

  async function newPage(width, height, theme) {
    const context = await browser.newContext({viewport: {width, height}, reducedMotion: "no-preference", colorScheme: theme, locale: "en-US", acceptDownloads: true});
    await context.addInitScript(value => { try { localStorage.setItem("tofi:appearance", value); } catch {} }, theme);
    const page = await context.newPage();
    const admin = createAdmin();
    page.on("pageerror", error => warnings.push(`pageerror: ${error.message}`));
    await page.route("**/*", async route => {
      const request = route.request(), url = new URL(request.url()), method = request.method();
      if (url.origin !== origin) { await route.abort(); return; }
      if (!url.pathname.startsWith("/api/")) { await route.continue(); return; }
      if (url.pathname.startsWith("/api/admin/")) { const answer = admin.adminStub(url, method, request.postData()); await route.fulfill({status: answer.status, contentType: "application/json", body: JSON.stringify(answer.body)}); return; }
      const body = stub(url, method, new URLSearchParams(page.url().split("?")[1] ?? ""));
      if (body === undefined) { warnings.push(`unstubbed ${method} ${url.pathname}`); await route.fulfill({status: 404, contentType: "application/json", body: JSON.stringify({error: {message: "not stubbed", code: "not_found"}})}); return; }
      await route.fulfill({contentType: "application/json", body: JSON.stringify(body)});
    });
    return {page, context, admin};
  }
  async function openTab(page, tab, mobile, params = {}) {
    const query = new URLSearchParams({lang: "en", tab, view: "page", ...params});
    await page.goto(`${origin}/${basename(fixture)}/index.html?${query}`);
    await page.locator(".settings-shell").waitFor({timeout: 20000});
    await page.waitForFunction(id => (document.querySelector(`.settings-page-body[data-page="${id}"]`)?.innerText ?? "").trim().length > 20, tab, {timeout: 20000});
    await page.waitForTimeout(tab === "usage" ? 1800 : 700);
  }

  // ---- Scenarios. `container` is the scrolling element for the full-page shot (default: the tab's page body).
  const body = tab => page => page.locator(`.settings-page-body[data-page="${tab}"]`);
  const click = async (locator, label) => { try { await locator.first().click({timeout: 4000}); } catch { warnings.push(`step skipped (not found): ${label}`); return false; } return true; };
  const bundleFile = async (page, {encrypt}) => {
    const source = JSON.stringify({format: "tofi.bundle", version: 3, kind: "account", included: ["bot_config", "chats", "memories", "settings", "vault_environment"], bots: [{id: "bot_research", name: "Research Bot"}, {id: "bot_inbox", name: "Inbox Bot"}], counts: {bot_config: 2}, vault_environment: [{id: "env_1", name: "WEB_API_TOKEN", target: "WEB_API_TOKEN", source_category: "active_vault"}]});
    const text = encrypt ? await page.evaluate(async plain => (await import("/src/portabilityCrypto.ts")).encryptPortable(plain, "Synthetic-Test-Passphrase-Only"), source) : source;
    return {name: "sample-account.tofi.json", mimeType: "application/json", buffer: Buffer.from(text)};
  };
  const scenarios = [];
  const tabScenario = (id, tab) => scenarios.push({id, tab, run: async () => {}});
  for (const tab of ["general", "connections", "skills", "approvals", "models", "computer", "keys", "usage", "advanced", "admin"]) tabScenario(tab, tab);
  scenarios.push(
    {id: "connections-add", tab: "connections", run: async ({page}) => { await click(page.getByRole("button", {name: /^Add/}), "Connections: Add"); await page.waitForTimeout(500); }},
    {id: "skills-install", tab: "skills", run: async ({page}) => { await click(page.getByRole("button", {name: /Add skill|Install/}), "Skills: install"); await page.waitForTimeout(500); }},
    {id: "skills-access-sheet", tab: "skills", container: ".sheet", run: async ({page}) => { await click(page.getByRole("button", {name: "Change"}).first(), "Skills: Change"); await page.locator(".sheet").waitFor({timeout: 4000}).catch(() => {}); await page.getByRole("radio", {name: /^Only selected Bots/}).check().catch(() => {}); await page.waitForTimeout(400); }},
    {id: "approvals-modes", tab: "approvals", run: async ({page}) => { await page.locator("#auto-review-mode").selectOption({index: 1}).catch(() => warnings.push("step skipped: approvals mode select")); await page.waitForTimeout(300); }},
    {id: "models-key-form", tab: "models", run: async ({page}) => { await page.locator(".provider-key-row input").first().fill("sk-proj-synthetic-0000000000").catch(() => warnings.push("step skipped: provider key input")); await page.waitForTimeout(400); }},
    {id: "keys-add-env", tab: "keys", run: async ({page}) => { await click(page.getByRole("button", {name: /Add environment variable|Add variable/}), "Keys: add env"); await page.waitForTimeout(500); }},
    {id: "keys-ssh", tab: "keys", run: async ({page}) => { await click(page.getByRole("tab").nth(1), "Keys: SSH tab"); await page.waitForTimeout(400); await click(page.getByRole("button", {name: /Show public|public key/i}), "Keys: show public key"); await click(page.getByRole("button", {name: /Add (SSH )?key/}), "Keys: add key"); await page.waitForTimeout(500); }},
    {id: "computer-expanded", tab: "computer", run: async ({page}) => { await page.waitForTimeout(600); }},
    {id: "usage-calls", tab: "usage", run: async ({page}) => { await click(page.locator(".usage-ranges button").nth(1), "Usage: second range"); await page.waitForTimeout(600); }},
    {id: "advanced-export-env", tab: "advanced", run: async ({page}) => {
      await click(page.getByRole("button", {name: /Show transferable environment credentials/}), "Advanced: load env");
      await page.getByRole("checkbox", {name: /Vault environment credentials/}).check({timeout: 3000}).catch(() => warnings.push("step skipped: vault category checkbox"));
      await page.waitForTimeout(400);
    }},
    {id: "advanced-export-bot", tab: "advanced", run: async ({page}) => { await page.locator(".portability-section select").first().selectOption("bot").catch(() => warnings.push("step skipped: scope select")); await page.waitForTimeout(400); }},
    {id: "advanced-import-chosen", tab: "advanced", run: async ({page}) => { await page.locator('input[type="file"]').first().setInputFiles(await bundleFile(page, {encrypt: true})); await page.waitForTimeout(800); }},
    {id: "advanced-import-passphrase", tab: "advanced", run: async ({page}) => { await page.locator('input[type="file"]').first().setInputFiles(await bundleFile(page, {encrypt: true})); await page.getByLabel("Decryption passphrase").waitFor({timeout: 4000}).catch(() => warnings.push("step skipped: passphrase field")); await page.getByLabel("Decryption passphrase").fill("Synthetic-Test-Passphrase-Only").catch(() => {}); await page.waitForTimeout(400); }},
    {id: "advanced-import-enter", tab: "advanced", run: async ({page}) => {
      // Enter in the passphrase field is "Decrypt and view".
      await page.locator('input[type="file"]').first().setInputFiles(await bundleFile(page, {encrypt: true}));
      await page.getByLabel("Decryption passphrase").fill("Synthetic-Test-Passphrase-Only");
      await page.getByLabel("Decryption passphrase").press("Enter");
      await page.getByRole("button", {name: /Preview import/}).waitFor({timeout: 5000});
      await page.waitForTimeout(300);
    }},
    {id: "advanced-import-selection", tab: "advanced", run: async ({page}) => {
      await page.locator('input[type="file"]').first().setInputFiles(await bundleFile(page, {encrypt: true}));
      await page.getByLabel("Decryption passphrase").fill("Synthetic-Test-Passphrase-Only").catch(() => warnings.push("step skipped: passphrase field"));
      await click(page.getByRole("button", {name: /Decrypt and view/}), "Advanced: Decrypt");
      await page.waitForTimeout(900);
    }},
    {id: "advanced-import-preview", tab: "advanced", run: async ({page}) => {
      await page.locator('input[type="file"]').first().setInputFiles(await bundleFile(page, {encrypt: true}));
      await page.getByLabel("Decryption passphrase").fill("Synthetic-Test-Passphrase-Only").catch(() => warnings.push("step skipped: passphrase field"));
      await click(page.getByRole("button", {name: /Decrypt and view/}), "Advanced: Decrypt");
      await page.waitForTimeout(700);
      await page.locator("label.check-row", {hasText: "Vault environment credentials"}).first().click({timeout: 3000}).catch(() => warnings.push("step skipped: import env category"));
      await page.locator("label.check-row", {hasText: "WEB_API_TOKEN"}).first().click({timeout: 3000}).catch(() => warnings.push("step skipped: import env checkbox"));
      await click(page.getByRole("button", {name: /Preview import/}), "Advanced: Preview import");
      await page.waitForTimeout(900);
    }},
    {id: "advanced-danger", tab: "advanced", run: async ({page}) => { await page.locator(".danger-zone input").first().fill("PURGE").catch(() => warnings.push("step skipped: purge input")); await page.waitForTimeout(300); }},
    {id: "admin-detail", tab: "admin", run: async ({page}) => { await click(page.locator('.admin-row[data-account="acct_two"]'), "Admin: row"); await page.locator('[data-testid="admin-detail"]').waitFor({timeout: 4000}).catch(() => {}); await page.waitForTimeout(500); }},
    {id: "admin-add-sheet", tab: "admin", container: ".sheet", run: async ({page}) => { await click(page.getByRole("button", {name: "Add account"}), "Admin: Add account"); await page.locator(".sheet").waitFor({timeout: 4000}).catch(() => {}); await page.waitForTimeout(500); }},
    {id: "admin-delete-sheet", tab: "admin", container: ".sheet", run: async ({page}) => {
      await click(page.locator('.admin-row[data-account="acct_three"]'), "Admin: deactivated row");
      await page.locator('[data-testid="admin-detail"]').waitFor({timeout: 4000}).catch(() => {});
      await click(page.getByRole("button", {name: "Delete account…"}), "Admin: Delete account");
      await page.locator(".admin-delete-sheet").waitFor({timeout: 4000}).catch(() => {});
      await page.locator(".admin-delete-sheet").getByLabel(/^Type/).fill("Lee Example").catch(() => {});
      await page.waitForTimeout(400);
    }},
    {id: "admin-delete-failed", tab: "admin", container: ".sheet", run: async ({page, admin}) => {
      admin.adminState.failDelete = 1;
      await click(page.locator('.admin-row[data-account="acct_three"]'), "Admin: deactivated row");
      await page.locator('[data-testid="admin-detail"]').waitFor({timeout: 4000}).catch(() => {});
      await click(page.getByRole("button", {name: "Delete account…"}), "Admin: Delete account");
      const sheet = page.locator(".admin-delete-sheet");
      await sheet.getByLabel(/^Type/).fill("Lee Example").catch(() => {});
      await click(sheet.getByRole("button", {name: "Delete account", exact: true}), "Admin: confirm delete");
      await sheet.getByText("Deletion stopped at", {exact: false}).waitFor({timeout: 4000}).catch(() => warnings.push("step skipped: deletion stopped message"));
      await page.waitForTimeout(400);
    }},
    {id: "admin-result-sheet", tab: "admin", container: ".sheet", run: async ({page}) => {
      await click(page.locator('.admin-row[data-account="acct_three"]'), "Admin: deactivated row");
      await page.locator('[data-testid="admin-detail"]').waitFor({timeout: 4000}).catch(() => {});
      await click(page.getByRole("button", {name: "Delete account…"}), "Admin: Delete account");
      const sheet = page.locator(".admin-delete-sheet");
      await sheet.getByLabel(/^Type/).fill("Lee Example").catch(() => {});
      await click(sheet.getByRole("button", {name: "Delete account", exact: true}), "Admin: confirm delete");
      await page.locator(".admin-result-sheet").waitFor({timeout: 5000}).catch(() => warnings.push("step skipped: result sheet"));
      await page.waitForTimeout(400);
    }},
  );

  // ---- Onboarding sheet (full app mount, tiny stateful stub).
  const at = "2026-10-01T00:00:00Z";
  async function onboardingPage(width, height, theme) {
    const context = await browser.newContext({viewport: {width, height}, colorScheme: theme, locale: "en-US"});
    await context.addInitScript(value => { try { localStorage.setItem("tofi:appearance", value); } catch {} }, theme);
    const page = await context.newPage();
    page.on("pageerror", error => warnings.push(`pageerror: ${error.message}`));
    const state = {model: false, onboarding: {step: 0, completed: false, skipped: false}, bots: [], keyTries: 0, mcp: []};
    const makeBot = () => ({id: "bot_1", name: "New Bot", instructions: "", model: "default", reasoning_effort: "default", dm_conversation_id: "conv_1", created_at: at, archived: false});
    await context.route(u => new URL(u).origin !== origin && !String(u).startsWith("data"), route => route.fulfill({status: 200, contentType: "text/css", body: ""}));
    await page.route("**/api/**", async route => {
      const request = route.request(), path = new URL(request.url()).pathname, method = request.method();
      const bodyOf = () => { try { return JSON.parse(request.postData() || "{}"); } catch { return {}; } };
      const json = (value, status = 200) => route.fulfill({status, json: value});
      if (path === "/api/server-info") return json({service: "tofi", protocol_version: 1, instance_id: INSTANCE, auth: {mode: "none"}});
      if (path === "/api/preferences") return json({timezone: "UTC", timezone_configured: true});
      if (path === "/api/config") return json({model_configured: state.model, default_model: "", provider: "synthetic"});
      if (path === "/api/bots" && method === "GET") return json({bots: state.bots});
      if (path === "/api/conversations") return json({conversations: state.bots.map(bot => ({id: bot.dm_conversation_id, kind: "dm", name: bot.name, bot_id: bot.id, bot_ids: [bot.id], updated_at: at, read_seq: 0, user_visible: true}))});
      if (path === "/api/onboarding" && method === "GET") return json(state.onboarding);
      if (path === "/api/onboarding" && method === "PUT") { const update = bodyOf(); state.onboarding = {...state.onboarding, ...(typeof update.step === "number" ? {step: Math.max(state.onboarding.step, update.step)} : {})}; return json(state.onboarding); }
      if (path === "/api/onboarding/first-bot") { state.bots = [makeBot()]; return json({bot: state.bots[0], created: true}, 201); }
      if (path === "/api/auth/codex/connect") return json({session_id: "sess_1", verification_url: "https://auth.example.test/device", user_code: "WQ7K-3MPD", expires_at: Date.now() + 600000, interval: 1});
      if (path === "/api/auth/codex/connect/sess_1/poll") return json({connected: false, pending: true});
      if (path === "/api/auth/codex") return json({connected: false});
      if (path === "/api/providers" && method === "GET") return json({providers: []});
      if (path.startsWith("/api/providers/") && path.endsWith("/key")) { state.keyTries++; if (state.keyTries <= 1) return json({error: {code: "invalid_key", message: ""}}, 400); state.model = true; return json({id: "openai", label: "OpenAI", kind: "api_key", configured: true, key_hint: "…0000"}); }
      if (path === "/api/models") return json({models: state.model ? models : [], source: "live"});
      if (path === "/api/model-settings") return json({model: "codex-gpt-6-sol", reasoning_effort: "medium"});
      if (path === "/api/extensions/oauth-options") return json({vm_available: false, web_callback_origin: "", desktop_redirect_uri: ""});
      if (path === "/api/extensions/mcp" && method === "GET") return json({servers: state.mcp});
      if (path.endsWith("/messages")) return json({messages: [], drafts: [], has_more: false, event_cursor: 0});
      if (path.endsWith("/runs")) return json({runs: []});
      if (path.endsWith("/tools")) return json({activities: [], summaries: [], tool_count: 0, has_more: false});
      if (path.startsWith("/api/computer")) return json({owner: null, waiting: []});
      if (path.endsWith("/memories")) return json({memories: []});
      if (path.endsWith("/schedules")) return json({schedules: []});
      if (path === "/api/questions") return json({questions: []});
      if (path === "/api/mail-drafts") return json({drafts: []});
      if (path === "/api/secret-inputs") return json({requests: []});
      return json({});
    });
    await page.goto(origin + "/");
    return {page, context, admin: null};
  }
  const onb = (id, run) => scenarios.push({id: `onboarding-${id}`, onboarding: true, container: ".onb-sheet", run});
  onb("welcome", async ({page}) => { await page.locator(".onb-welcome h2").waitFor(); await page.waitForTimeout(1200); });
  onb("model", async ({page}) => { await page.locator(".onb-welcome h2").waitFor(); await page.getByRole("button", {name: /Let.s set up your crew/}).click(); await page.getByRole("radio").first().waitFor(); await page.waitForTimeout(700); });
  onb("chatgpt-waiting", async ({page}) => { await page.getByRole("button", {name: /Let.s set up your crew/}).click(); await page.getByRole("radio").first().waitFor(); await page.locator(".onb-primary").click(); await page.locator(".onb-code").waitFor(); await page.waitForTimeout(700); });
  onb("key-error", async ({page}) => { await page.getByRole("button", {name: /Let.s set up your crew/}).click(); await page.getByRole("radio").first().waitFor(); await page.locator(".onb-choice").nth(1).click(); await page.locator("#onb-key").fill("sk-proj-synthetic-0000000000"); await page.locator(".onb-keyrow button").click(); await page.locator("#onb-key-error").waitFor(); await page.waitForTimeout(500); });
  onb("connected", async ({page}) => { await page.getByRole("button", {name: /Let.s set up your crew/}).click(); await page.getByRole("radio").first().waitFor(); await page.locator(".onb-choice").nth(1).click(); await page.locator("#onb-key").fill("sk-proj-synthetic-0000000000"); await page.locator(".onb-keyrow button").click(); await page.locator("#onb-key-error").waitFor(); await page.locator(".onb-keyrow button").click(); await page.locator(".onb-connected").waitFor(); await page.locator(".onb-advanced .disclosure-toggle").click().catch(() => {}); await page.waitForTimeout(700); });
  onb("services", async ({page}) => { await page.getByRole("button", {name: /Let.s set up your crew/}).click(); await page.getByRole("radio").first().waitFor(); await page.locator(".onb-choice").nth(1).click(); await page.locator("#onb-key").fill("sk-proj-synthetic-0000000000"); await page.locator(".onb-keyrow button").click(); await page.locator("#onb-key-error").waitFor(); await page.locator(".onb-keyrow button").click(); await page.locator(".onb-connected").waitFor(); await page.locator(".onb-foot .onb-primary").click(); await page.locator(".onb-tiles").waitFor(); await page.locator("[data-integration-id='github']").click(); await page.locator("[data-integration-id='linear']").click(); await page.waitForTimeout(700); });
  onb("services-walk", async ({page}) => { await page.getByRole("button", {name: /Let.s set up your crew/}).click(); await page.getByRole("radio").first().waitFor(); await page.locator(".onb-choice").nth(1).click(); await page.locator("#onb-key").fill("sk-proj-synthetic-0000000000"); await page.locator(".onb-keyrow button").click(); await page.locator("#onb-key-error").waitFor(); await page.locator(".onb-keyrow button").click(); await page.locator(".onb-connected").waitFor(); await page.locator(".onb-foot .onb-primary").click(); await page.locator(".onb-tiles").waitFor(); await page.locator("[data-integration-id='github']").click(); await page.locator("[data-integration-id='linear']").click(); await page.locator(".onb-foot .onb-primary").click(); await page.locator(".onb-queue").waitFor(); await page.locator(".onb-service-form input[type=password]").fill("synthetic-token").catch(() => {}); await page.waitForTimeout(700); });

  // ---- Walk everything.
  const scrollThrough = async (page, selector) => {
    const handle = await page.locator(selector).first().elementHandle().catch(() => null);
    if (!handle) return null;
    await handle.evaluate(async element => {
      const step = Math.max(200, element.clientHeight * 0.8);
      for (let y = 0; y < element.scrollHeight; y += step) { element.scrollTop = y; await new Promise(r => setTimeout(r, 60)); }
      element.scrollTop = element.scrollHeight; await new Promise(r => setTimeout(r, 80));
      element.scrollTop = 0;
    });
    return handle;
  };
  const fullShot = async (page, selector, file, base) => {
    const handle = await page.locator(selector).first().elementHandle().catch(() => null);
    const extra = handle ? await handle.evaluate(el => Math.max(0, Math.min(8000, el.scrollHeight - el.clientHeight))) : 0;
    if (extra > 0) {
      await page.addStyleTag({content: "@media(min-width:701px){.workspace .detail-pane:has(.settings-tabs){height:calc(100dvh - 48px)!important;max-height:none!important}}.sheet,.onb-sheet{max-height:none!important}"});
      await page.setViewportSize({width: base.width, height: base.height + extra + 60});
      await page.waitForTimeout(300);
    }
    await page.screenshot({path: file});
  };

  let scenarioRuns = 0;
  const combos = [];
  for (const [vpName, width, height] of VIEWPORTS) for (const theme of THEMES) combos.push({vpName, width, height, theme});
  async function runCombo({vpName, width, height, theme}) {
    const mobile = width < 700;
    for (const scenario of scenarios) {
      if (only && !only.test(scenario.id)) continue;
      let context, page;
      try {
        let admin;
        ({page, context, admin} = scenario.onboarding ? await onboardingPage(width, height, theme) : await newPage(width, height, theme));
        if (!scenario.onboarding) await openTab(page, scenario.tab, mobile);
        await scenario.run({page, mobile, admin});
        const container = scenario.container ?? `.settings-page-body[data-page="${scenario.tab}"]`;
        await scrollThrough(page, container);
        await page.waitForTimeout(200);
        const found = await page.evaluate(auditPage, {mobile, allowedClasses: ALLOWED_BUTTON_CLASSES, allowedContainers: ALLOWED_BUTTON_CONTAINERS});
        for (const item of found) violations.push({...item, scenario: scenario.id, viewport: vpName, theme});
        if (shots) await fullShot(page, container, join(shots, `${vpName}-${theme}-${scenario.id}.png`), {width, height});
        scenarioRuns++;
      } catch (error) {
        warnings.push(`scenario ${scenario.id} (${vpName}, ${theme}) failed: ${error.message.split("\n")[0]}`);
        violations.push({kind: "scenario-error", selector: scenario.id, detail: error.message.split("\n")[0], scenario: scenario.id, viewport: vpName, theme});
      } finally { await context?.close(); }
    }
  }
  await Promise.all(combos.map(runCombo));

  // ---- Report
  const unique = new Map();
  for (const v of violations) {
    const key = `${v.kind}|${v.scenario}|${v.viewport}|${v.selector}|${v.detail}`;
    const entry = unique.get(key) ?? {...v, themes: new Set()};
    entry.themes.add(v.theme); unique.set(key, entry);
  }
  const list = [...unique.values()];
  if (shots) await writeFile(join(shots, "violations.json"), JSON.stringify(list.map(v => ({...v, themes: [...v.themes]})), null, 1));
  const byKind = {};
  for (const v of list) byKind[v.kind] = (byKind[v.kind] ?? 0) + 1;
  for (const v of list) console.log(`VIOLATION ${v.kind.padEnd(14)} ${v.scenario} @${v.viewport} [${[...v.themes].join("+")}] ${v.selector}  ${v.detail}`);
  for (const w of [...new Set(warnings)]) console.log(`note: ${w}`);
  console.log(`audited ${scenarioRuns} scenario runs; ${list.length} distinct violations`, JSON.stringify(byKind));
  if (list.length) { console.error(`FAIL settings polish: ${list.length} violation(s)`); process.exitCode = 1; }
  else console.log("PASS settings polish: 0 violations");
} finally {
  await browser?.close();
  await server?.close();
  await rm(fixture, {recursive: true, force: true});
}
