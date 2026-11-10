// Shared harness for the Administration suites (test:admin-groups, test:org-resources): a Vite fixture that mounts the real
// SettingsShell + SettingsPages as an administrator of a multi-account install, and a stateful stub of the Administration API
// (groups, membership, per-account policy, organization resources, organization web tools) with the server's rules: all-or-nothing
// batch moves, group_in_use, built-in groups read-only, keys at least 16 characters, write-only keys (only `key_hint` from 16
// characters and `key_prefix` from 32 ever come back). Synthetic data only.
import {mkdtemp, mkdir, readFile, writeFile} from "node:fs/promises";
import {basename, join} from "node:path";
import {pathToFileURL} from "node:url";

export async function startFixture(ui, prefix) {
  const {createServer} = await import("vite");
  const {default: react} = await import("@vitejs/plugin-react");
  const fixture = await mkdtemp(join(ui, `.${prefix}-`));
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
import {SettingsShell,type SettingsEntry,type SettingsTab} from '../src/SettingsShell';
import {SettingsPages} from '../src/settings/SettingsPages';
import {setComputerCapability} from '../src/computerCapability';
import {setCapabilityPolicy} from '../src/capabilityPolicy';
const query=new URLSearchParams(location.search);
const host=query.get('host')==='none'?'none':'kvm';
setComputerCapability({backend:host,available:host==='kvm',host_backend:host,capabilities:['shell','files'],personal_computers:true} as any);
if(query.get('policy'))setCapabilityPolicy(JSON.parse(query.get('policy')!));
const bots=[{id:'bot_research',name:'Research Bot',is_primary:true,archived:false,model:'default',reasoning_effort:'default'}] as any;
function Settings(){
 const appearance=useAppearance();
 const [tab,setTab]=useState<SettingsTab>((query.get('tab') as SettingsTab)||'general');
 const [entry,setEntry]=useState<SettingsEntry>({seq:0,view:(query.get('view') as any)||'page'});
 const openSettings=(next:SettingsTab,view:'home'|'page'='page')=>{setTab(next);setEntry(current=>({seq:current.seq+1,view}))};
 return <div className="workspace"><div className="workspace-grid"><aside className="detail-pane visible" role="dialog" aria-modal="true"><SettingsShell tab={tab} onTab={setTab} onClose={()=>{}} entry={entry} renderPage={page=><SettingsPages page={page} bots={bots} timezone="America/Los_Angeles" usageBotId="" portabilityBotID="" onPortabilityFileConsumed={()=>{}} appearance={{preference:'light',choose:()=>{}}} extensionRefresh={0} slots={{codexRefresh:0,onCodexConfigured:()=>{},providersRefresh:0,onProvidersConfigured:()=>{},legacyArchive:null}} openTab={openSettings}/>}/></aside></div></div>;
}
void i18nReady.then(()=>setLanguage((query.get('lang') as any)||'en')).then(()=>createRoot(document.getElementById('root')!).render(<TimezoneProvider><OwnerSessionGate><Settings/></OwnerSessionGate></TimezoneProvider>));
`);
  const server = await createServer({configFile: false, root: ui, plugins: [react()], server: {host: "127.0.0.1", port: 0, hmr: false}, logLevel: "error"});
  await server.listen();
  return {server, fixture, origin: `http://127.0.0.1:${server.httpServer.address().port}`, path: `/${basename(fixture)}/index.html`};
}

const CAPS = ["ai_models", "computer", "web_search", "web_fetch"];
const SELF_SERVE = {ai_models: {mode: "own", params: {}}, computer: {mode: "host", params: {}}, web_search: {mode: "own", params: {}}, web_fetch: {mode: "own", params: {}}};
export const PROVIDED = {ai_models: {mode: "provided", params: {}}, computer: {mode: "provided", source: "vm", params: {}}, web_search: {mode: "provided", params: {}}, web_fetch: {mode: "provided", params: {}}};
const NEEDS = {
  ai_models: () => ["provider:anthropic", "provider:openai", "provider:compatible"],
  web_search: () => ["webtool:tavily", "webtool:brave"],
  web_fetch: () => ["webtool:firecrawl", "webtool:brightdata"],
  computer: policy => policy.source === "e2b" ? ["computer:e2b"] : [],
};
const WEB = [
  {id: "tavily", chain: "search", label: "Tavily", kind: "api", paid: true},
  {id: "brave", chain: "search", label: "Brave Search", kind: "api", paid: true},
  {id: "searxng", chain: "search", label: "SearXNG", kind: "self_hosted", paid: false},
  {id: "firecrawl", chain: "fetch", label: "Firecrawl", kind: "api", paid: true},
  {id: "brightdata", chain: "fetch", label: "Bright Data", kind: "api", paid: true},
];

/** One administrator, three members, the two built-in groups; the stub answers the way the gateway does. */
export function createOrgStub() {
  const state = {};
  const writes = [];
  const reads = []; // GETs of a per-account policy: the accounts list carries each account's group and must not need them
  const secrets = new Set(); // every key ever sent, to prove none comes back
  const reset = (options = {}) => {
    Object.assign(state, {
      kvm: true,
      accounts: [
        {id: "acct_sample", username: "Ada Sample", email: "ada@example.test", role: "admin", disabled: false, must_change_password: false, deleting: false},
        {id: "acct_two", username: "Sam Example", email: "sam@example.test", role: "user", disabled: false, must_change_password: false, deleting: false},
        {id: "acct_three", username: "Lee Example", email: "lee@example.test", role: "user", disabled: false, must_change_password: false, deleting: false},
        {id: "acct_four", username: "Max Example", email: "max@example.test", role: "user", disabled: false, must_change_password: false, deleting: false},
      ],
      groups: [
        {id: "self_serve", name: "Self-serve", builtin: true, position: 0, policies: structuredClone(SELF_SERVE)},
        {id: "provided", name: "Provided", builtin: true, position: 1, policies: structuredClone(PROVIDED)},
      ],
      members: {}, // account id -> group id (self_serve is no row)
      overrides: {}, // account id -> {capability: policy}
      resources: {}, // id -> view
      web: {search_order: ["tavily", "brave", "searxng", "computer"], fetch_order: ["firecrawl", "brightdata", "computer"], disabled: [], searxng_url: "", daily_search: null, daily_fetch: null},
      providers: [],
      // The person's own ChatGPT (Codex) sign-in: the member-side "Your own" section reads and drives it.
      codex: {connected: false, needs_reconnect: false},
      // The organization's computer image (computer:e2b): the server's build for this TOFI version.
      build: {phase: "ready", version: "0.9.1", error_code: undefined, updated_at: "2026-11-08T19:00:00Z", min_guest_version: undefined},
      rejectKeys: new Set(), failNext: null, nextId: 1, now: 0,
      ...options,
    });
    writes.length = 0;
    reads.length = 0;
  };
  reset();
  const groupOf = id => state.members[id] ?? "self_serve";
  const count = gid => state.accounts.filter(account => groupOf(account.id) === gid).length;
  const groupView = group => ({id: group.id, name: group.name, builtin: group.builtin, position: group.position, member_count: count(group.id), policies: group.policies});
  const effective = id => {
    const group = state.groups.find(item => item.id === groupOf(id));
    return Object.fromEntries(CAPS.map(cap => { const v = state.overrides[id]?.[cap] ?? group.policies[cap] ?? SELF_SERVE[cap]; return [cap, {mode: v.mode, ...(v.source ? {source: v.source} : {})}]; }));
  };
  const policyView = id => { const group = state.groups.find(item => item.id === groupOf(id)); return {group: {id: group.id, name: group.name}, overrides: state.overrides[id] ?? {}, effective: effective(id)}; };
  /** The view the gateway returns: computer:e2b carries `build` (custom when the admin supplied a template, else this version's build). */
  const viewOf = row => {
    if (!row || row.id !== "computer:e2b") return row;
    const custom = Boolean(row.config.template_id);
    const build = custom ? {phase: "ready", custom: true, version: state.build.version} : {phase: state.build.phase, custom: false, version: state.build.version, error_code: state.build.error_code, updated_at: state.build.updated_at};
    if (state.build.min_guest_version) build.min_guest_version = state.build.min_guest_version;
    return {...row, build};
  };
  const have = id => Boolean(state.resources[id]) || (id === "webtool:searxng" && Boolean(state.web.searxng_url));
  const missing = () => {
    const out = [];
    for (const cap of CAPS) {
      const groupIds = state.groups.filter(group => { const v = group.policies[cap] ?? SELF_SERVE[cap]; if (v.mode !== "provided" && !(cap === "computer" && v.mode === "provided")) return false; if (cap === "computer" && v.source === "vm" && !state.kvm) return true; const needs = NEEDS[cap](v); return needs.length > 0 && !needs.some(have); }).map(group => group.id);
      const accountIds = Object.entries(state.overrides).filter(([, rows]) => { const v = rows[cap]; if (!v || v.mode !== "provided") return false; const needs = NEEDS[cap](v); return needs.length > 0 && !needs.some(have); }).map(([id]) => id);
      if (groupIds.length || accountIds.length) out.push({capability: cap, group_ids: groupIds, account_ids: accountIds});
    }
    return out;
  };
  const validPolicy = (cap, v) => {
    if (!CAPS.includes(cap) || !v) return false;
    const ok = {ai_models: ["own", "provided"], computer: ["host", "none", "own", "provided"], web_search: ["off", "own", "provided"], web_fetch: ["builtin", "own", "provided"]}[cap];
    if (!ok.includes(v.mode)) return false;
    if (cap === "computer" && v.mode === "provided") return (v.source === "vm" && state.kvm) || v.source === "e2b";
    return !v.source;
  };
  const fail = (status, code, message = code) => [status, {error: {code, message}}];
  const hint = key => key.length >= 16 ? "…" + key.slice(-4) : "";
  const prefix = key => key.length >= 32 ? key.slice(0, 4) : "";
  const specs = {
    "provider:anthropic": {kind: "model_provider"}, "provider:openai": {kind: "model_provider"}, "provider:compatible": {kind: "model_provider"}, "computer:e2b": {kind: "computer_provider"},
    "webtool:tavily": {kind: "web_provider"}, "webtool:brave": {kind: "web_provider"}, "webtool:firecrawl": {kind: "web_provider"}, "webtool:brightdata": {kind: "web_provider", zone: true},
  };
  const webView = () => ({
    search_order: state.web.search_order, fetch_order: state.web.fetch_order, disabled: state.web.disabled, searxng_url: state.web.searxng_url, daily_search: state.web.daily_search, daily_fetch: state.web.daily_fetch,
    providers: WEB.map(p => {
      const row = state.resources[`webtool:${p.id}`];
      const out = {id: p.id, chain: p.chain, label: p.label, kind: p.kind, paid: p.paid, enabled: !state.web.disabled.includes(`${p.chain}/${p.id}`)};
      if (p.kind === "self_hosted") { out.base_url = state.web.searxng_url; out.status = state.web.searxng_url ? "ready" : "needs_address"; return out; }
      out.status = !row ? "needs_key" : row.status === "rejected" ? "error" : "ready";
      if (row) { Object.assign(out, {key_prefix: row.key_prefix || undefined, key_hint: row.key_hint || undefined, zone: row.config.zone, error_code: row.error_code || undefined, tested_at: row.verified_at || undefined}); }
      return out;
    }),
  });

  const handle = (url, method, body) => {
    const path = decodeURIComponent(url.pathname);
    let m;
    if (method !== "GET") writes.push(`${method} ${path}${body && Object.keys(body).length ? " " + JSON.stringify(body) : ""}`);
    if (state.failNext && method !== "GET" && state.failNext.test(path)) { const code = state.failNext.code; state.failNext = null; return fail(409, code); }
    if (path === "/api/providers" && method === "GET") return [200, {providers: state.providers}];
    if (path === "/api/model-settings" && method === "GET") return [200, {model: state.defaultModel ?? "", reasoning_effort: "medium"}];
    if (path === "/api/auth/codex" && method === "GET") return [200, {connected: state.codex.connected, expires_at: Date.parse("2026-12-01T00:00:00Z"), needs_reconnect: state.codex.needs_reconnect}];
    if (path === "/api/auth/codex/verify") return [200, {connected: state.codex.connected, expires_at: Date.parse("2026-12-01T00:00:00Z"), needs_reconnect: state.codex.needs_reconnect, check: "ok"}];
    if (path === "/api/auth/codex" && method === "DELETE") { state.codex = {connected: false, needs_reconnect: false}; return [200, {}]; }
    if (path === "/api/auth/codex/connect" && method === "POST") return [200, {session_id: "cx1", verification_url: "https://auth.example.test/device", user_code: "ABCD-EFGH", expires_at: Date.now() + 600000, interval: 1}];
    if (/^\/api\/auth\/codex\/connect\/[^/]+\/poll$/.test(path)) { state.codex = {connected: true, needs_reconnect: false}; return [200, {connected: true, pending: false, expires_at: Date.parse("2026-12-01T00:00:00Z")}]; }
    if (path === "/api/admin/accounts" && method === "GET") return [200, state.accounts.map(account => { const group = state.groups.find(item => item.id === groupOf(account.id)); return {...account, group: {id: group.id, name: group.name}}; })];
    if (path === "/api/admin/capacity") return [200, {total_bytes: 500 * 2 ** 30, available_bytes: 300 * 2 ** 30, allocated_bytes: 100 * 2 ** 30, promised_bytes: 100 * 2 ** 30, admission_remaining_bytes: 120 * 2 ** 30, warning: false, accounts: []}];
    if (path === "/api/admin/storage") return [200, {version: 1, host: {total_bytes: 500 * 2 ** 30, available_bytes: 300 * 2 ** 30}, accounts: state.accounts.map(a => ({account_id: a.id, server_bytes: 2 ** 30, limit_bytes: null, over_limit: false, source: "measured"}))}];
    if (path === "/api/admin/spend") return [200, {version: 1, accounts: state.accounts.map(a => ({account_id: a.id, spend_usd: 0, limit_usd: 0, state: "off", available: true}))}];
    if (path === "/api/admin/deleted-exports") return [200, []];
    if (path === "/api/admin/groups" && method === "GET") return [200, state.groups.map(groupView)];
    if (path === "/api/admin/groups" && method === "POST") {
      const name = String(body.name ?? "").trim();
      if (!name) return fail(400, "invalid_request");
      if (state.groups.some(g => g.name === name)) return fail(409, "group_exists");
      const policies = structuredClone(SELF_SERVE);
      for (const [cap, v] of Object.entries(body.policies ?? {})) { if (!validPolicy(cap, v)) return fail(400, "invalid_policy"); policies[cap] = v; }
      const group = {id: `grp_${state.nextId++}`, name, builtin: false, position: state.groups.length, policies};
      state.groups.push(group);
      return [201, groupView(group)];
    }
    if ((m = path.match(/^\/api\/admin\/groups\/([^/]+)$/))) {
      const group = state.groups.find(g => g.id === m[1]);
      if (!group) return fail(404, "not_found");
      if (method === "PATCH") {
        if (body.policies && group.id === "self_serve") return fail(409, "group_builtin_readonly");
        if (body.name !== undefined && group.builtin && body.name !== group.name) return fail(409, "group_builtin_readonly");
        if (body.policies) {
          const next = structuredClone(SELF_SERVE);
          for (const [cap, v] of Object.entries(body.policies)) { if (!validPolicy(cap, v)) return fail(400, "invalid_policy"); next[cap] = v; }
          group.policies = next;
        }
        if (body.name !== undefined && !group.builtin) group.name = body.name.trim();
        return [200, groupView(group)];
      }
      if (method === "DELETE") {
        if (group.builtin) return fail(409, "group_builtin_readonly");
        if (count(group.id) > 0) return fail(409, "group_in_use");
        state.groups = state.groups.filter(g => g !== group);
        return [200, {deleted: true, id: group.id}];
      }
    }
    if ((m = path.match(/^\/api\/admin\/groups\/([^/]+)\/members$/)) && method === "PUT") {
      const group = state.groups.find(g => g.id === m[1]);
      if (!group) return fail(404, "not_found");
      const ids = body.account_ids ?? [];
      if (!ids.length || ids.some(id => !state.accounts.some(a => a.id === id))) return fail(404, "not_found");
      for (const id of ids) { if (group.id === "self_serve") delete state.members[id]; else state.members[id] = group.id; }
      return [200, {group_id: group.id, moved: ids.length}];
    }
    if ((m = path.match(/^\/api\/admin\/accounts\/([^/]+)\/policy$/))) {
      if (method === "GET") reads.push(`GET ${path}`);
      if (!state.accounts.some(a => a.id === m[1])) return fail(404, "not_found");
      if (method === "PUT") {
        if (body.group_id !== undefined) { if (!state.groups.some(g => g.id === body.group_id)) return fail(404, "not_found"); if (body.group_id === "self_serve") delete state.members[m[1]]; else state.members[m[1]] = body.group_id; }
        if (body.overrides !== undefined) {
          for (const [cap, v] of Object.entries(body.overrides)) if (!validPolicy(cap, v)) return fail(400, "invalid_policy");
          state.overrides[m[1]] = body.overrides;
        }
      }
      return [200, policyView(m[1])];
    }
    if (path === "/api/admin/org/resources" && method === "GET") return [200, {resources: Object.values(state.resources).map(viewOf), missing: missing()}];
    if (path === "/api/admin/org/resources/computer:e2b/build" && method === "POST") {
      const row = state.resources["computer:e2b"];
      if (!row) return fail(404, "not_found");
      if (row.config.template_id) return fail(409, "custom_template");
      if (state.build.phase !== "preparing") Object.assign(state.build, {phase: "preparing", error_code: undefined, updated_at: "2026-11-08T20:05:00Z"});
      return [202, viewOf(row)];
    }
    if ((m = path.match(/^\/api\/admin\/org\/resources\/([^/]+?)(\/test)?$/))) {
      const id = decodeURIComponent(m[1]);
      if (!specs[id]) return fail(404, "not_found");
      if (m[2]) {
        if (method !== "POST") return fail(405, "method_not_allowed");
        const row = state.resources[id];
        if (!row) return fail(404, "not_found");
        if (state.rejectKeys.has(id)) { Object.assign(row, {status: "rejected", error_code: "invalid_key", verified_at: undefined}); return fail(400, "invalid_key"); }
        Object.assign(row, {status: "verified", error_code: undefined, verified_at: "2026-11-08T20:00:00Z", updated_at: `t${++state.now}`});
        return [200, viewOf(row)];
      }
      if (method === "GET") return state.resources[id] ? [200, viewOf(state.resources[id])] : fail(404, "not_found");
      if (method === "DELETE") { if (!state.resources[id]) return fail(404, "not_found"); delete state.resources[id]; return [200, {deleted: true, id, missing: missing()}]; }
      if (method === "PUT") {
        const key = String(body.key ?? "").trim();
        if (key) secrets.add(key);
        if (id !== "provider:compatible" && (key.length < 16 || /\s/.test(key))) return fail(400, "invalid_key");
        if (id === "provider:anthropic" && !/^wrkspc_[A-Za-z0-9_-]{4,}$/.test(body.workspace_id ?? "")) return fail(400, "invalid_workspace");
        if (state.rejectKeys.has(id)) return fail(400, "invalid_key");
        const config = {};
        let status = "verified";
        if (id === "provider:compatible") {
          if (!body.base_url || !/^https?:\/\//.test(body.base_url)) return fail(400, "invalid_base_url");
          config.base_url = body.base_url; config.models = body.models ?? []; status = "stored";
        }
        if (id === "computer:e2b" && body.template_id) config.template_id = body.template_id;
        if (specs[id].zone) { if (!body.zone) return fail(400, "invalid_zone"); config.zone = body.zone; }
        if (specs[id].kind === "web_provider") status = "stored";
        const keptKey = id === "provider:compatible" && !key ? state.resources[id]?.key_hint : "";
        state.resources[id] = {id, kind: specs[id].kind, status, key_prefix: prefix(key), key_hint: key ? hint(key) : keptKey, verified_at: status === "verified" ? "2026-11-08T20:00:00Z" : undefined, updated_at: `t${++state.now}`, config};
        return [200, viewOf(state.resources[id])];
      }
    }
    if (path === "/api/admin/org/web-tools") {
      if (method === "PUT") {
        for (const key of ["search_order", "fetch_order", "disabled", "searxng_url", "daily_search", "daily_fetch"]) if (body[key] !== undefined) state.web[key] = body[key];
      }
      return [200, webView()];
    }
    return undefined;
  };
  return {state, writes, reads, secrets, reset, handle, groupOf, effective, missing};
}

/** A page on the fixture with every /api/* call answered by `stub.handle` (and the few the shell itself makes). */
export async function openSettings({browser, fixture, stub, errors, params = {}, width = 1440, height = 900, theme = "light", language = "en", locale = "en-US", touch = false, role = "admin"}) {
  const context = await browser.newContext({viewport: {width, height}, colorScheme: theme, locale, hasTouch: touch, isMobile: touch});
  await context.addInitScript(value => { try { localStorage.setItem("tofi:appearance", value); } catch {} }, theme);
  const page = await context.newPage();
  page.on("pageerror", error => errors.push(`pageerror: ${error.message}`));
  const json = (route, status, body) => route.fulfill({status, contentType: "application/json", body: JSON.stringify(body)});
  await page.route("**/*", async route => {
    const request = route.request(), url = new URL(request.url()), method = request.method(), path = url.pathname;
    if (url.origin !== fixture.origin) { await route.abort(); return; }
    if (!path.startsWith("/api/")) { await route.continue(); return; }
    let body = {};
    try { body = JSON.parse(request.postData() || "{}"); } catch { /* a GET */ }
    if (path === "/api/auth/session") return json(route, 200, {enabled: true, setup_required: false, authenticated: true, password_transport_allowed: true, multi_account: true, owner: {id: "acct_sample", username: "Ada Sample", email: "ada@example.test", role}});
    if (path === "/api/server-info") return json(route, 200, {service: "tofi", protocol_version: 1, instance_id: "0b1c2d3e-4f50-4a6b-8c7d-9e0f1a2b3c4d", auth: {mode: "owner"}});
    if (path === "/api/bots") return json(route, 200, {bots: [{id: "bot_research", name: "Research Bot", archived: false}]});
    if (path === "/api/preferences") return json(route, 200, {timezone: "America/Los_Angeles", timezone_configured: true});
    if (path === "/api/models") return json(route, 200, {models: stub.state.models ?? []});
    const answer = stub.handle(url, method, body);
    if (answer === undefined) { if (method !== "GET") errors.push(`unstubbed ${method} ${path}`); return json(route, 200, {}); }
    return json(route, answer[0], answer[1]);
  });
  const query = new URLSearchParams({lang: language, host: stub.state.kvm ? "kvm" : "none", ...params});
  await page.goto(`${fixture.origin}${fixture.path}?${query}`);
  await page.locator(".settings-shell").first().waitFor({timeout: 20000});
  return {page, context, close: () => context.close()};
}

export {pathToFileURL, mkdir};
