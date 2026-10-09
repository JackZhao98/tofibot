# TOFI Tools / Connections: Audit and Redesign Contract

Repository root: `/Users/jackzhao/Developer/sentiosurge/tofibot-dicebear` (all `file:line` citations below are relative to it; `main` as of 2026-10-09, head `5be5851`). Read-only audit; no files modified, no hosts contacted, no credentials read.

Status legend for findings: **[V]** verified from code, **[H]** hypothesis to test in the eval harness.

---

## 0. Verdict in one page

The current system is engineered for *safety against prompt injection and replay* (fenced config fingerprints, one-shot approval claims, untrusted-metadata everywhere) and it does that well. It is not engineered for *the owner's four fronts*:

1. **Connecting**: three OAuth routes (`web` / `desktop` / `vm`) selected by protocol + env var (`ui/src/mcpOAuthRoute.ts:6-16`), with the web route hard-requiring `TOFI_PUBLIC_ORIGIN` or terminated TLS at the Go process (`internal/app/extension_oauth.go:16-26`) and no reverse-proxy header support. Google presets require the user to create their own OAuth client across two Google consoles (`ui/src/integrationCatalog.ts:43-48`). Local MCP is a second, visually separate subsystem that silently wakes the account computer when the Tools page opens (`internal/app/local_mcp.go:94` → `internal/computer/runner.go:38-41`).
2. **Bots using tools**: every MCP call is two hops (`search_mcp_tools` → `call_mcp_tool`) because no schema is ever in context (`internal/extensions/discovery_instructions.go:14-24` sends *names only*); search is BM25 over name+description (`internal/extensions/tool_ranking.go:19-82`); and **any** failed or `isError` call on a non-"trusted read-only" tool is reported to the model as *"may already have happened, do not retry"* (`internal/extensions/mcp.go:713-727, 738-747`) — and the trusted list has **no UI or API setter** (only `internal/extensions/mcp.go:45-47`, `management.go:222,251`, `discovery.go:925-932`), so in practice it is empty and zero MCP calls get retries.
3. **Approvals**: with AutoReview `off` (the default, `internal/app/auto_review_settings.go:19,70`), **every** MCP call that is not in the (empty) trusted list opens a human card titled "Allow this external tool call?" with impact text "The external server may change data or contact others" (`internal/app/mcp_call_approval.go:63-90,129-138`) — including a docs search on Microsoft Learn. The card has no "always allow this tool". AutoReview `auto` adds a 30 s, no-retry reviewer round trip per call (`internal/app/mcp_auto_review.go:23,268-288`) and a `context_gap` answer permanently closes the proposal with "replan" (`internal/app/mcp_review_contract.go:108-138`).
4. **UI**: Settings has `Tools` (= remote MCP list + Local MCP panel + catalog + custom form), `Skills`, `Keys and environment`, and AutoReview hidden inside **Models and reasoning** (`ui/src/App.tsx:1846`, `ui/src/SettingsShell.tsx:14`). Five nouns (Tools, MCP, Local MCP, Service, Plugin, Skill, Extension) for three user concepts.

The redesign below collapses this into **Connections** (one list, one add flow, per-connection tools with trust and Bot access), **Skills** (unchanged concept, cleaned copy), and **Approvals** (trust defaults + optional Reviewer). On the model side it moves to **hybrid direct exposure** (direct tool schemas when the usable tool set is small; the search facade only for large servers), classifies failures correctly (server-reported error ≠ uncertain dispatch), and retries non-dispatched failures. The owner's principle is preserved: risk is judged by what the exact action does (model self-labeled effect + reviewer + owner-reviewed per-tool defaults), no keyword rules.

---

## 1. Audit

### 1.1 Concept inventory (as the product exposes them today)

| Concept (EN / zh-CN copy today) | Where it lives | What it actually is | Source |
|---|---|---|---|
| **Tools** / 工具 (settings tab `mcp`) | Settings › Workspace | Remote MCP server list + Local MCP panel + catalog + custom form | `ui/src/SettingsShell.tsx:14`; `ui/src/locales/en/settings.json:60-63`; `ui/src/MCPSettings.tsx:88-129` |
| **Service** / 服务 ("Add service", "Added") | Tools page toolbar | A saved `MCPServerConfig` row | `ui/src/MCPSettings.tsx:122`; `en/extensions.json:277-279` |
| **Integration preset** / 连接预设 | "Add a service" browser | Catalog tile that pre-fills the MCP form (12 presets, 4 "held") | `ui/src/IntegrationBrowser.tsx:10-28`; `ui/src/integrationCatalog.ts:55-263, 281-303` |
| **Custom MCP** / 自定义 MCP | Catalog bottom button | Same form with name+URL exposed | `ui/src/MCPSettings.tsx:292-293`; `IntegrationBrowser.tsx:27` |
| **Local MCP** / 本地 MCP ("plugin", "Runner") | Top of Tools page (web only; hidden on desktop) | npm/PyPI stdio servers installed in the account computer's Runner, attached as `local_<id>` remote entries | `ui/src/MCPSettings.tsx:121`; `ui/src/LocalMCPPanel.tsx:14-136`; `internal/app/local_mcp.go:296-358` |
| **Personal Gmail · gogcli** | Card inside Local MCP | Built-in gog plugin, Gmail only, needs user's Google OAuth client JSON + HTTPS origin | `LocalMCPPanel.tsx:107-126`; `internal/mcprunner/install.go:152`; `internal/app/local_mcp.go:194-223` |
| **Skills** / Skills (tab `skills`) | Settings › Workspace | Folder of SKILL.md, workspace-wide, read on demand by the model | `ui/src/ExtensionPanel.tsx:23-63`; `internal/extensions/skills.go`; `ui/src/skillCatalog.ts` |
| **Tofi work Skills** (starters) | Skills page | 3 bundled SKILL.md bodies | `ui/src/skillCatalog.ts:7-29` |
| **Keys and environment** / 密钥与环境 | Settings › Workspace | Env vars + SSH keys for the computer (not MCP credentials) | `ui/src/ComputerCredentials.tsx`; `en/settings.json:56-59` |
| **AutoReview** | Settings › **Models and reasoning** (bottom) | off/shadow/auto reviewer mode for all external tools | `ui/src/App.tsx:1846`; `ui/src/AutoReviewSettings.tsx:32-35` |
| **Extension** | Code/API only (`/api/extensions/*`, `extension_status`, `manage_extensions`) | Umbrella for MCP+Skills | `internal/app/extension_management.go:16`; `internal/app/app.go:3137`; `internal/app/extension_tools.go:50` |
| **Allowed tools / Blocked tools** | Advanced disclosure of MCP form | Global per-server allow/deny lists by remote tool name | `MCPSettings.tsx:307`; `internal/extensions/mcp.go:43-44,668-670` |
| **Trusted read-only tools** | **Not exposed anywhere** | The only path to skip the approval card | `internal/extensions/mcp.go:45-47`; `discovery.go:925-932` |
| **Bot allowlists** | Config field, ignored | Legacy per-Bot grant; `PrepareForBot` passes nil | `mcp.go:48`; `management.go:39 (json:"-")`; `discovery.go:293,417`; `DISCOVERY.md` ("legacy per-Bot grant … ignored") |

**[V]** There is no per-Bot enablement of any connection today; everything installed is visible to every Bot (`internal/extensions/DISCOVERY.md`, "Installed MCP servers and skills are workspace-wide").

### 1.2 Settings pages and entry points

- `SettingsShell` groups: Preferences (account, models, dictate), Workspace (usage, computers, credentials, **mcp**, **skills**), Advanced (connection, debug) — `ui/src/SettingsShell.tsx:12-16`.
- `App.tsx:1846` renders `ExtensionPanel kind={page}` for `mcp`/`skills`, `ComputerCredentials` for `credentials`, and `<ModelDefaults/><AutoReviewSettings/>` for `models`.
- Chat → "open tools" deep-link sets tab `mcp` (`App.tsx:1127,1625`); task issue cards with `action:"open_tools"` dispatch the same (`ui/src/QuestionCard.tsx:127`, `ui/src/TaskRunBlock.tsx:168`).
- Tools page composition (`MCPSettings.tsx:119-128`): `[LocalMCPPanel (web only)] → "Added N" + "Add service" → list of MCPRow`. Add → full-screen `IntegrationBrowser` → `MCPForm`.
- Row actions (`MCPSettings.tsx:226-240`): primary button is labeled **"Connect"** both for "run a test" (idle/error) and for "start OAuth" (needs auth) — `MCPSettings.tsx:228`. Status strings: checking / N tools / authorizing / waiting / failed / needs auth / **"Not verified yet"** (`MCPSettings.tsx:225`; `en/extensions.json:364-374`).
- Menu: Edit settings, Refresh tools, Disconnect (OAuth only), Remove service (`MCPSettings.tsx:230`).

### 1.3 Server code paths (map)

**Configuration and management** — `internal/extensions/management.go`
- `ListMCP` masks header values and reports `oauth.connected` from a token file (`:133-170`).
- `SaveMCP` validates name/URL, refuses credential carry-over on endpoint change, preserves `TrustedReadOnlyTools` from the old record and clears it when the credential target changes (`:172-265`).
- `InspectMCP` = connect + `tools/list`, returns `tool_count`, `auth_required`, diagnostics; **no tool is ever called** (`:299-343`).
- OAuth: `OAuthStart` (DCR when `client_id` empty, PKCE, 10-min session) `:847-954`; `OAuthCallbackForRedirect` commits DCR client only after a successful code exchange `:961-1043`; `OAuthDisconnect` `:1057`.
- HTTP routes — `internal/app/extension_management.go:16-195` (`/api/extensions/mcp[...]`, `/oauth/{pending,start,callback,disconnect}`, `/test`, `/skills`), `routeLocalMCP` (`internal/app/local_mcp.go:107-282`), `routeVMOAuth` (`internal/app/vm_oauth.go:286`), `routeDesktopOAuth` (`internal/app/extension_oauth.go:28-68`).

**Run-time tool exposure** — `internal/extensions/mcp.go`, `discovery.go`
- Run start calls `PrepareDiscoverableForBotWithCallGate(ctx, botID, cachedMCPTools, gate)` (`internal/app/app.go:3123-3133`). Discoverable mode never connects at run start (`mcp.go:331-334`), registers **4 MCP facade tools + 3 skill tools** (`discovery.go:907`, `952-955`, `mcp.go:361-363`) and a names-only directory in the system prompt (`discovery_instructions.go:13-53`, 4 KiB cap).
- The facade is dropped entirely when no usable server exists (`app.go:3866-3888`).
- `search_mcp_catalog` (`discovery.go:354-522`): name/description index, no schemas, persistent disk index at `<mcp.json dir>/mcp-tool-index` (`mcp.go:195-198`), 10 s wall budget per first build (`:386`).
- `search_mcp_tools` (`discovery.go:523-691`): default 5 / max 10 results, 32 KiB page, ≤ 8 servers per page, BM25 then optional AI expansion on a miss (`:614-631`), `*` browse per server. Each first search per server connects + `tools/list` (`ensure`, `:216-319`), 10 s discovery timeout.
- `call_mcp_tool` (`discovery.go:733-844`): accepts a name seen in this run, a validated cached schema from the conversation (≤ 3 schemas, 24 h, `internal/app/context.go:47-60`), or a known exact name resolved by server prefix (`resolveKnown`, `:695-732`). Validates args against the JSON schema and returns the schema on failure (`:791-792`; `mcp_outcome.go:15-42`; `internal/tooloutcome/validation.go:8-28`, repair limit 3). Then readiness recheck, approval gate, execute under the config fence (`:814-843`).
- Limits: tool timeout 30 s, discovery 10 s, result 50 000 runes, description 1 024 runes, 1 000 tools max (`mcp.go:26-33`, `852-877`, `804-813`).
- Retries/outcomes (`mcp.go:685-750`): read-only (trusted) → up to 3 attempts on transient errors; **anything else → immediate `Uncertain/mcp_result_unknown/verify_effect`** (`:716-717`); `isError` results are also `Uncertain` unless trusted (`:738-747`).
- Session reuse per run via `mcpSessionSlot` with 30 s readiness TTL (`mcp_session.go:18, 194-274`).
- Caches: catalog (schemas) 2-min LRU, metadata 10-min, disk metadata index (`mcp.go:194-198`; `DISCOVERY.md`).
- `manage_extensions` lets the model create/update/delete MCP servers and skills (`internal/app/extension_tools.go:34-53`).
- `workspace_capabilities` lists all registered tools and 5 schemas on demand (`internal/app/capability_tools.go:30-82`).

**Approvals**
- Gate: `approveMCPCall` → `approveMCPCallOnce` (`internal/app/mcp_call_approval.go:30-256`). Flow: if mode ≠ auto and the tool is trusted read-only and no prior card → allow (`:67-90`); else create question `Allow this external tool call?` with Action `Call <tool>`, Target `MCP server <server>`, Impact "The external server may change data or contact others…" and the raw argument payload (`:129-138`), wait (`:205-217`), then atomically claim one execution (`:236-251`). Claimed approvals cannot be reused (`:177-178`). Arguments containing a key like `token/secret/password/apikey` are refused outright (`:296-324`) **[V]** — a GitHub tool argument named `token_id` would be unreviewable.
- AutoReview v5 (`internal/app/mcp_auto_review.go`): prompt `:15-23`; reviewer = Codex review model, 30 s, no retry, ≤ 100 KiB packet (`:268-288`); disposition `:191-211` (allow+low/medium → approved; high/needs_human → human; context_gap/unknown → `context_required`, which closes the proposal with "Do not retry this MCP action" `mcp_review_contract.go:108-138`); 2-min validity (`:24`). Shadow mode records advice in background (`mcp_shadow_review.go`). Settings singleton default `off` (`auto_review_settings.go:19,70`). Changing mode invalidates unclaimed decisions (`:80-163`).
- Computer actions: model self-labels `effect` on click/type/key (`internal/app/microvm.go:369,424-454`); consequential effects go to `guardAction` → `reviewAction` (Codex review model, 30 s) → allow or tell the model to `request_approval` (`internal/app/browser_read.go:288-385`); human approvals in the run are matched by element label (`:389-405`). Workflow guide instructs the labeling (`internal/app/workflow_guides.go:18`).
- Human card UI: `ui/src/ApprovalCard.tsx` (Approve / Not now, payload in `<details>`), `ui/src/QuestionCard.tsx:98-139` shows review states.

**Local MCP** — `internal/mcprunner/*`, `internal/computer/guest/runner.go`, `internal/computer/runner.go`
- In isolated workspaces the Runner lives inside the account microVM (`internal/app/local_mcp.go:22-25`; `internal/computer/guest/runner.go:14-51`), reached via `http://account-computer/v1/runner` (`internal/computer/runner.go:11`); **every request to it admits/wakes the VM** (`runner.go:38-44`, `Prepare` 120 s, `:22-31`).
- Plugins start on demand (45 s startup timeout), idle 10 min (`internal/mcprunner/runner.go:20-24`; guest sets 10 min `guest/runner.go:31`). Install = pinned `npm install --ignore-scripts` / `pip` in a venv, 3-min timeout (`install.go:131-137`). The account computer is sized at **1024 MiB** (`deploy/self-host/tofi_host.py:815`), shared by desktop/Chrome and all plugins **[V]**.
- gog: installed as `gog mcp --allow-tool gmail` (`install.go:152`); OAuth via gogcli two-step with the web callback (`gog.go:156,218`); hidden from runs until a Google account is connected, re-checked at most every 30 s via a 2 s Runner call (`local_mcp.go:362-393`).

### 1.4 End-to-end flows (steps counted from an open Settings panel)

**A. Add remote MCP by URL (custom, no auth)** — [V]
Settings › Tools (1) → Add service (2) → scroll past catalog, "Custom MCP" (3) → Service ID (4) → URL (5) → Add (6) → auto test runs (`MCPSettings.tsx:115,170`) → row shows "N tools" or error. 6 steps; the "Service ID" is a user-typed identifier that becomes the model-facing prefix `mcp_<id>__…` (`mcp.go:568`).

**B. Add remote MCP with OAuth (Notion via DCR)** — [V]
Steps 1-3 as above but tile "Notion" (3) → Add (4) → row shows "Authorization needed" → Connect (5) → route:
- `web` (page is `https:` **and** server returned `web_callback_origin`): popup `/oauth/pending` → provider → `/oauth/callback` (zh-CN completion page, `internal/app/oauth_completion.go:44-54,86-121`) → UI polls popup title every 2 s (`MCPSettings.tsx:172-186`) → test. 5 steps if HTTPS origin is configured.
- `desktop`: native loopback `http://127.0.0.1:43821/oauth/callback` (`extension_oauth.go:14`).
- `vm`: only on plain HTTP with a computer: needs an active Bot (`MCPSettings.tsx:202`), takes the computer lease, opens the provider in the guest Chrome, streams the desktop to the user (`vm_oauth.go:106-144`; `ui/src/VMOAuthDialog.tsx`). DCR must accept the guest's redirect URI (`management.go:873-876`).
- `blocked`: http without computer, or https without `TOFI_PUBLIC_ORIGIN` → "Set TOFI_PUBLIC_ORIGIN…" (`en/extensions.json:316`).

**C. Add Google Gmail/Drive/… (own OAuth client)** — [V]
Google Cloud: enable product API + MCP API, consent screen, test users, create *Web application* client, register the exact redirect URL (`integrationCatalog.ts:43-48`; `en/extensions.json:77-82,186`) → Tofi: tile → Client ID + Secret → Add → Connect → provider → back. **≥ 12 steps across two consoles**, and the Google MCP endpoints are "Developer Preview" (`integrationCatalog.ts:69`).

**D. Add token service (GitHub read-only)** — [V]
Tile (3) → paste PAT (4) → Add (5) → auto test. Good. Catalog copy itself says "Source and settings are checked but not yet tested with a real account" (`en/extensions.json:54,102`).

**E. Local stdio MCP** — [V]
Tools page (web only) → Local MCP panel loads (`GET /api/extensions/local-mcp` → wakes VM) → "Install another local MCP" disclosure (1) → runtime / Plugin ID / package / pinned version / executable / args / env (7 fields) → Install (2, up to 3 min, inside the 1 GiB VM) → auto-attach as `local_<id>` (`local_mcp.go:145-149`) → first model call cold-starts the process (≤ 45 s). No catalog of local packages; no memory estimate; no "test" button for local plugins.

**F. Gmail (gog)** — [V]
Install gogcli (1) → Google Cloud web client + redirect registration (2-5) → email + scope + upload client JSON (6-8) → Connect Google (HTTPS origin required, `LocalMCPPanel.tsx:17,118-119`) (9) → Google → callback → Verify (10) → Connect to Tofi/attach (11).

**G. Install a Skill** — [V]
Skills tab → Install Skill → choose folder (webkitdirectory) or manual SKILL.md → Install (3-4 steps). Starters are one click. Skills are **not** in the system prompt in discoverable mode (`mcp.go:350-363`: `loadSkillsMode(..., metadataOnly=true)`, `discoveryInstructions` = name+description ≤ 240 B); the model must call `read_skill`.

**H. Model discovers and calls a tool** — [V]
System prompt: policy sentence + `Configured MCP sources (names only)` + skills (`discovery_instructions.go:14-52`). The model must either (a) `search_mcp_tools{query, server?}` → gets ≤ 5 schemas → `call_mcp_tool{name, arguments}`, or (b) guess `mcp_<server>__<tool>` and call directly (`resolveKnown`), or (c) reuse ≤ 3 schemas cached from the last 24 h of the conversation. Each search on a cold server performs connect + `tools/list` (10 s budget). Then: schema validation → readiness → approval gate → execute.

**I. What the model sees on failure** — [V]
- Connection down: `mcpReadinessOutcome(...)` (`mcp_session.go:247-258`; `mcp_method_readiness.go:24`).
- Transport/timeout/JSON-RPC error on a non-trusted tool: `mcp_result_unknown` "The action may already have happened. Verify the target state before proposing any retry" (`mcp.go:716-717`, `mcp_outcome.go:50-54`).
- Server `isError:true` (e.g. "repo not found"): same Uncertain outcome plus the text (`mcp.go:738-747`).
- Invalid args: `invalid_arguments` + current schema (≤ 4 KiB) and repair limit 3.
- Tool not returned by search: `schema_required` (`discovery.go:783`).
- Approval denied/expired/claimed: Denied / Expired / Uncertain codes (`mcp_call_approval.go:177,223,268`).

**J. Persistence** — [V]
`mcp.json` (`serverFile{mcpServers}`, 0600) with headers/OAuth client in clear (`management.go:257,345`); OAuth tokens `tokenPath(name)` JSON; `mcp-tool-index/` metadata; SQLite `questions`, `mcp_call_approvals`, `mcp_auto_reviews`, `mcp_call_execution_claims`, `auto_review_settings`, `tool_activities`; Runner `manifest.json` + `plugins/<id>` (+ secret env files 0600) in the VM's shared `.tofi/runner`.

**K. Where errors surface** — [V]
Settings row (`MCPSettings.tsx:235`), form banner (`:309`), Local MCP panel (`LocalMCPPanel.tsx:134`), OAuth completion page (zh-CN only), chat `extension_status` tool + system note (`app.go:3134-3138`), task issue cards (`QuestionCard.tsx:127`). The UI maps English server strings to i18n keys by exact match (`MCPSettings.tsx:69-80`); OAuth-start and callback errors are emitted in Chinese by the server (`extension_management.go:52,72,75,77`), so the mapping cannot apply and non-Chinese UIs show Chinese text.

### 1.5 Concrete failure points and friction

Connecting
- **C1 [V]** Web OAuth requires `TOFI_PUBLIC_ORIGIN` or TLS terminated by the Go process; no `X-Forwarded-Proto/Host` support, so a reverse proxy (Caddy/nginx/Tailscale Serve) yields route `blocked` with "Set TOFI_PUBLIC_ORIGIN" (`extension_oauth.go:16-26`; `mcpOAuthRoute.ts:10-12`). The `Origin` header must also equal the configured origin exactly (`extension_management.go:51`), so an install reachable by two hostnames fails on one of them.
- **C2 [V]** Self-signed HTTPS on an IP is accepted by `ValidateRedirectURI` (`oauth_transport.go:204-217`) but Google-type providers reject non-public-TLD redirect URIs; DCR providers (Notion) accept it. Copy does not tell the user which case they are in.
- **C3 [V]** Plain-HTTP LAN installs are routed to the VM flow, which needs a Bot, an awake computer, the desktop stream, and a provider that accepts the guest's dynamic redirect (`mcpOAuthRoute.ts:13-15`; `vm_oauth.go:106-135`). Copy: "The service must support a local redirect" (`en/extensions.json:317`).
- **C4 [V]** Google presets: user must create and register their own OAuth client (≥ 12 steps); endpoints are Developer Preview.
- **C5 [V]** OAuth completion/pending pages and several server errors are Chinese-only (`oauth_completion.go:44-54,87`; `extension_management.go:45,52,72-77`; `local_mcp.go:227,235`), while the UI is in 7 languages.
- **C6 [V]** Transport select offers "SSE" (`MCPSettings.tsx:304`) which the server refuses before any network call (`mcp.go:593-595`): a guaranteed dead end.
- **C7 [V]** "Service ID" is a required technical field for custom servers and becomes the model's tool prefix; a typo like `My Server` is rejected by regex (`MCPSettings.tsx:284`) with a format error.
- **C8 [V]** The row's primary button says "Connect" for both "test connection" and "start OAuth" (`MCPSettings.tsx:228`); status after save is "Not verified yet" until the test completes.
- **C9 [V]** "Test" only lists tools; it never proves a call works (`management.go:307-327`). A GitHub PAT with wrong scopes passes the test.
- **C10 [V]** Local MCP panel is hidden on desktop (`MCPSettings.tsx:121`) and simply disappears if the Runner is unavailable, with reason text from the Go error (`LocalMCPPanel.tsx:48`).
- **C11 [V]** Opening Settings › Tools (web) issues `GET /api/extensions/local-mcp`, which goes through `RunnerTransport.RoundTrip` → `ensure` and wakes a hibernated computer (`local_mcp.go:50-62,94`; `computer/runner.go:38-44`). **[H]** Users perceive this as "Settings is slow / computer starts by itself".
- **C12 [V]** Local install form has 7 free-text fields incl. executable name and pinned version; no package catalog, no "verify package exists" step, 3-min blocking install.
- **C13 [V]** All local plugins share the 1 GiB guest with Chrome (`tofi_host.py:815`). **[H]** Two Node MCP servers + Chrome exceed it; first call hits the 45 s startup timeout.
- **C14 [V]** gog requires an HTTPS origin **and** a user-created Google client JSON; it only exposes Gmail; and until connected, `mcpServerUsable` probes the Runner (and thus admission) on each run start when the 30 s cache is stale (`local_mcp.go:372-393`).
- **C15 [V]** `manage_extensions` lets a Bot add arbitrary MCP endpoints (`extension_tools.go:40-50`) — surface the owner probably does not want on by default.
- **C16 [V]** The "held" catalog section (Discord, Slack, Yahoo Finance, Notion token) is rendered in the add flow as non-clickable research notes (`IntegrationBrowser.tsx:25`), adding noise to a first-run screen.

Bots using tools
- **T1 [V]** Two-hop calling is mandatory on first use; no schema in context (`discovery_instructions.go:14`). Each cold `search_mcp_tools` costs connect + `tools/list` (10 s budget) before the model can even see argument names.
- **T2 [V]** Search is lexical BM25 over name+description; AI expansion only on a *miss* and only with a Codex-managed provider (`tool_search_ai.go:29`). **[H]** Queries like "find my open PRs" miss `list_pull_requests` whose description says "List pull requests in a repository" (no "open", no "my") — rank still works via "pull"; but Chinese queries miss entirely unless Codex is configured.
- **T3 [V]** `trusted_read_only_tools` cannot be set from UI/API → read-only retries never apply; **every** error on **every** tool is "may already have happened, verify, do not retry" (`mcp.go:716-717`). A transient 502 on a docs search becomes a dead end and a "verify effect" instruction.
- **T4 [V]** `isError:true` (server-side validation such as "unknown repository") is reported as Uncertain dispatch rather than a definite, retry-after-fix failure (`mcp.go:738-747`).
- **T5 [V]** Result cap 50 000 runes with `[truncated]` and no continuation (`mcp.go:872-876`).
- **T6 [V]** Tool description cap 1 024 runes (`mcp.go:804-813`) but no normalization (no server display name, no annotations like `readOnlyHint` surfaced); descriptions over 512 B are cut in the catalog index (`discovery.go:246`).
- **T7 [V]** Three overlapping discovery tools (`list_mcp_servers`, `search_mcp_catalog`, `search_mcp_tools`) with long guidance strings (`discovery.go:354,523,845`) compete for the model's attention.
- **T8 [V]** Skills are never injected; the model must choose to `read_skill` based on a 240-byte description. **[H]** Starter skills rarely trigger.
- **T9 [V]** Approval-interrupted calls require the model to "call call_mcp_tool again now with the identical name and arguments" (`internal/app/input_continuation.go:339`) when the run was suspended; one more hop.
- **T10 [V]** No per-call telemetry view for the owner (only `tool_activities` rows and debug previews).

Approvals
- **A1 [V]** Default: human card for every non-trusted MCP call, including pure reads (`mcp_call_approval.go:63-90`), with generic impact text (`:135`).
- **A2 [V]** No "always allow this tool / this server / this Bot"; approvals are one-shot claims (`:177-178, 236-251`).
- **A3 [V]** AutoReview `auto`: +30 s worst-case per call, no retry; reviewer unavailability → "unavailable" closes the proposal (`mcp_auto_review.go:381-386`; `mcp_review_contract.go:140-142`). `context_gap` → permanent "do not retry, replan" (`:116-129`). Review packet rebuilds conversation evidence every call (`mcp_review_evidence.go`).
- **A4 [V]** AutoReview lives under "Models and reasoning"; copy is dense ("Observe · Record advice, keep the existing execution path", `en/settings.json:303-309`).
- **A5 [V]** Argument keys that *contain* `token|secret|password|apikey|authorization|credential|privatekey` make the call unreviewable (`mcp_call_approval.go:296-324`): e.g. a GitHub `token_name` or Linear `secret_gist` field.
- **A6 [V]** Computer click review: separate reviewer prompt (`browser_read.go:328-331`), separate "approved by label" memory (`:389-405`), reviewer failure → always ask (`:358`).
- **A7 [V]** 2-minute AutoReview validity (`mcp_auto_review.go:24`) and the human card's `expires_at` cause "approval expired" when the user is slow; expiry parks the run (`mcp_call_approval.go:258-269`).

UI
- **U1 [V]** Seven nouns for three concepts (table 1.1).
- **U2 [V]** Local MCP panel sits *above* the remote list on the Tools page (`MCPSettings.tsx:121-122`), so the first thing a new user sees is "Personal Gmail · gogcli" and a code sample `gog mcp --allow-tool gmail` (`LocalMCPPanel.tsx:110`).
- **U3 [V]** No connection detail page: tools of a connection are never listed; allow/deny lists are comma-separated text (`MCPSettings.tsx:307`).
- **U4 [V]** No health/status except the last manual test; no "last used", no error history.
- **U5 [V]** Back button in the browser is labeled "Added" (`en/extensions.json:14`, `"back": "Added"`).
- **U6 [V]** Approval settings separated from the tools they govern.

### 1.6 What is already good (keep)
- Config fence + fingerprint binding of approvals (`mcp.go:158-167`; `mcp_call_approval.go:271-280`).
- DCR + PKCE OAuth with isolated exchange store and commit-after-success (`management.go:877-883, 1003-1043`).
- Credential-carry-over protection on endpoint edits (`management.go:198-219`).
- Per-run session slot reuse and readiness memo (`mcp_session.go`).
- Schema validation with schema hint and bounded repair (`mcp_outcome.go`, `tooloutcome/validation.go`).
- Persistent metadata index (`metadata_index.go`) — basis for "direct exposure without startup connections".
- Effect self-labeling for computer actions (`microvm.go:369`) — the owner's principle already in code.
- Acceptance test style with synthetic MCP fixtures (`internal/app/extensions_e2e_acceptance_test.go`, `internal/extensions/mcp_fixture_test.go`) and `scripts/acceptance.py` HTTP harness.

---

## 2. Benchmark against current practice

Confidence: **(C)** confident, **(U)** uncertain/verify before quoting.

Connection UX
- Claude.ai / Claude Desktop "Connectors" and Claude Code `claude mcp add --transport http <url>` + `/mcp` login: one-click OAuth with **dynamic client registration** when the server supports it; the user never creates an OAuth client; connectors appear as a flat list with per-connector tool toggles. (C for DCR/one-click; U for exact toggle granularity.)
- ChatGPT connectors and Apps (built on MCP): OAuth handled by the platform; custom MCP via "developer mode"; write-capable tools ask for confirmation. (C high level; U for specifics.)
- Cursor: JSON config (`mcp.json`) supporting stdio/HTTP, OAuth login button per server, per-server enable toggle and per-tool toggles; historically warned when > ~40 tools were enabled because tool-list bloat degraded model performance. (C on config + toggles; U on the exact 40-tool figure.)
- Zapier MCP / Composio: managed OAuth apps (the vendor's registered client), per-action enable, "test action" with real sample data before exposing to the agent. (C.)
- MCP spec: Authorization uses OAuth 2.1 with Protected Resource Metadata (RFC 9728), Authorization Server Metadata (RFC 8414), DCR (RFC 7591), PKCE; loopback redirects per RFC 8252 for native clients. Tool annotations `readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint` exist as untrusted hints. (C.)

Tool exposure
- Claude Code / Claude API: tools are normally listed directly; Anthropic's **tool search / deferred tool loading** (as used in this very session: "deferred tools are available via ToolSearch") loads only a name list and fetches schemas on demand when the tool set is large. Two-stage search is used *as an optimization above a threshold*, not as the only path. (C that the pattern exists; U on thresholds.)
- OpenAI Responses API hosted MCP tool: the platform fetches the server's tool list, optionally filtered by `allowed_tools`, and passes schemas directly; approvals via `require_approval: "always" | "never" | {never:{tool_names:[…]}}`. (C.)
- Practitioner guidance (Anthropic/OpenAI docs): keep enabled tools to a few dozen; write descriptions as "when to use" + "what it returns"; namespace by server. (C.)
- Cursor/Claude Code show the user a tool count per server and let them disable noisy tools. (C.)

Approval UX
- Claude Code: per-tool permission prompt with "Yes / Yes, don't ask again for this tool (session or project) / No"; persistent allow/deny rules by tool pattern (`mcp__server__tool`); read-only built-ins are pre-allowed. (C on allow-once/always + rules; U on exact wording.)
- OpenAI Agents SDK hosted MCP: `require_approval` per tool name lists; approval requests surfaced as items the app resolves. (C.)
- ChatGPT: read actions run freely; write actions show a confirmation with the parameters. (C high level.)
- Cursor: "Run" / "Auto-run" per MCP tool, with a global "yolo"/auto-run mode. (C.)
- Common ground: classification by **what the tool does** (read vs write) at *registration* time, remembered per tool; confirmation shows concrete parameters; "always allow" is one click.

Observability
- Claude Code `/mcp` shows server status (connected / needs auth / failed) and tool count; Cursor shows a red/green dot and tool list; Zapier/Composio show an action log. (C.)

Gaps in TOFI relative to this: no "always allow", no per-tool trust at registration, no direct exposure, no per-connection tool list/health, OAuth not one-click behind proxies, no managed Google client, Local MCP as a separate top-level concept.

---

## 3. Redesign contract

### 3.1 Principles (binding)
1. One noun per user concept: **Connection** (a service a Bot can use), **Tool** (one capability of a connection), **Skill** (a way of working). "MCP", "plugin", "extension", "preset", "Runner" become technical labels, never headings.
2. Add flow is catalog-first and ends with a real **Test** that calls one tool.
3. Trust is a property of a tool (auto / ask / blocked) with sane defaults proposed by a reviewer model at registration and editable by the owner; per-call human cards only for "ask" tools; "always allow" is one click on the card.
4. The model sees schemas directly whenever the usable tool set is small; search is an optimization for large servers.
5. Failures are classified honestly: not dispatched → retry; dispatched but server replied error → definite failure with the message; dispatched and no reply → uncertain.
6. Every user-visible string goes through the i18n catalogs; no hardcoded zh-CN or en in Go HTML/handlers.
7. Keep all existing security invariants (config fence, one-shot claims, DCR commit-after-exchange, credential carry-over refusal).

### 3.2 Information architecture and vocabulary

Settings navigation (replace `mcp`, `skills`, and the AutoReview section):

| Tab id | EN | zh-CN | Contents |
|---|---|---|---|
| `connections` | **Connections** | **连接** | List of all connections (hosted, on-computer, built-in); Add; per-connection detail |
| `skills` | **Skills** | **技能 (Skills)** | unchanged scope, cleaned copy |
| `approvals` | **Approvals** | **审批** | Trust defaults, Reviewer toggle, "always allowed" list, recent decisions |
| `credentials` | Keys and environment | 密钥与环境 | unchanged (computer env/SSH); link from connections that need an env var |

Vocabulary (EN / zh-CN), to be the only terms in `ui/src/locales/*/connections.json`:
- Connection / 连接; Hosted connection (remote MCP) / 在线服务; On-computer connection (local stdio) / 在电脑上运行; Built-in / 内置
- Tool / 工具; Trust: Auto / 自动执行, Ask / 每次确认, Blocked / 已禁用
- Status: Connected / 已连接; Needs sign-in / 需要登录; Not working / 无法连接; Starting on your computer / 正在电脑上启动; Asleep / 休眠
- Bot access: All Bots / 所有 Bot; Only selected Bots / 仅指定 Bot
- Add a connection / 添加连接; Catalog / 目录; Custom / 自定义; Test connection / 测试连接
- Sign in with <service> / 使用 <service> 登录; Paste the address you landed on / 粘贴跳转后的地址 (manual OAuth fallback)
- Reviewer / 审查器 (replaces "AutoReview")

### 3.3 Screens (wireframe-level)

**S1 Connections list** (`ui/src/connections/ConnectionsPage.tsx`, replaces `MCPSettings.tsx` list + `LocalMCPPanel.tsx`)
```
Connections                                   [ + Add a connection ]
──────────────────────────────────────────────────────────────────
● GitHub (read-only)        Connected · 23 tools · 3 auto / 20 ask   ›
● Notion                    Needs sign-in                 [Sign in]  ›
◐ Filesystem (on computer)  Asleep · starts when used · 11 tools    ›
✕ Context7                  Not working: 401 — check API key [Fix]  ›
──────────────────────────────────────────────────────────────────
Empty state: "Connect the services your Bots should use." + 4 featured tiles.
```
Row = `ServiceMark`, display name (catalog name or user name), status line, Bot-access chip when not "All Bots", chevron to detail. Health is refreshed on page open from `GET /api/connections` (cached last test result + last call outcome; **no** Runner/VM probe on page load — see 3.4).

**S2 Add a connection** (modal/full page, `AddConnection.tsx`)
Tabs: **Catalog** (default) · **Custom URL** · **On my computer** · **Paste config** (accepts a `mcpServers` JSON snippet as used by Claude Desktop/Cursor; maps `url` → hosted, `command/args` → on-computer).
Catalog tile shows: name, one-line purpose, auth badge (Sign in / API key / No key), "Runs on your computer" badge, provider/community badge. Held/research items are **not** shown (moved to `docs/integrations`).
Selecting a tile → **S3 Connect sheet**.

**S3 Connect sheet** (one screen, 3 stages, progress at top: `Connect → Test → Done`)
- Stage Connect: shows only what the auth type needs. Sign-in: one button "Sign in with Notion" (route chosen server-side; see 3.4); if blocked, inline reason + the single fix (e.g. "Open Tofi at https://… to sign in" with copy button, or "Use manual sign-in" link). API key: one password field with "Where to get a key" link. No key: nothing.
- Stage Test: runs `POST /api/connections/{id}/test` which does `tools/list` **and** one probe call (3.4). Shows "Found 23 tools · probe `search_repositories` ok (0.8 s)". On failure: actionable error from the error catalog (3.4) with a "Fix" button that returns to Connect.
- Stage Done: "Review tools" summary: N auto, M ask (reviewer-proposed), link to detail; Bot access selector (All Bots default). Buttons: Done / Open details.
- Advanced disclosure (collapsed): connection id (auto-derived from name, editable), headers JSON, scopes, metadata URL, tool allow/deny.

**S4 Connection detail** (`ConnectionDetail.tsx`)
```
← Connections        GitHub (read-only)                      [Test] [⋯]
Status: Connected · last call 2 min ago · 0 errors today
Sign-in: PAT saved ••••  [Replace]                    Bot access: All Bots [Change]
On-computer only: Memory ~180 MB · Idle stop after 10 min · Last start 3.2 s

Tools (23)                                   Search tools…   [Trust all reads: Auto]
name                  what it does (first sentence)          trust       enabled
search_repositories   Search for GitHub repositories         Auto ▾      [x]
create_issue          Create a new issue in a repository     Ask ▾       [x]
…
Recent calls (last 20): time · Bot · tool · outcome · latency · [view args]
Danger: Sign out · Remove connection
```
Trust column values: Auto / Ask / Blocked. "Reviewer proposed" dot next to defaults not yet confirmed by the owner. `[⋯]`: Edit advanced, Refresh tools, Export config.

**S5 Approvals page** (`ApprovalsPage.tsx`, replaces `AutoReviewSettings.tsx`)
- Default trust for new tools: "Reads run automatically, everything else asks" (recommended, default) / "Everything asks" / "Everything runs automatically (not recommended)".
- Reviewer: toggle "Let the Reviewer approve low-risk 'Ask' actions" (off by default) with latency note; model/provider shown read-only.
- "Always allowed" list: entries created from cards ("<tool> for <Bot>") with remove buttons.
- Recent decisions: 30 most recent cards with outcome.
- Computer actions: read-only explainer that clicks/typing with an effect are reviewed the same way.

**S6 Approval card** (`ApprovalCard.tsx` extended)
Title: "<Bot> wants to use <Connection> › <tool>". Facts: what it does (description first sentence), key arguments rendered as label/value (full JSON in details), effect label (from reviewer proposal or tool trust), Bot. Buttons: **Allow once** · **Always allow this tool for <Bot>** · **Don't allow**. Secondary: "Block this tool". After the decision, the card shows the result line (existing behavior).

**S7 Chat surfaces**: connection status chip in the composer tool picker (existing "open tools" deep link → `connections` tab, detail of the failing connection when `issue.connection_id` is set).

### 3.4 Connection mechanics

**API (new, replaces `/api/extensions/mcp*` and `/api/extensions/local-mcp*`; old paths kept as aliases for one release)**
- `GET /api/connections` → `[{id, kind: hosted|local|builtin, display_name, status, status_detail, tool_count, trust_summary, bot_access, last_test_at, last_call_at, last_error}]`
- `POST /api/connections` `{kind, id?, display_name, url?|package?, auth: {type: none|token|oauth, token?, header?, client_id?, scopes?}, catalog_id?}`
- `POST /api/connections/{id}/test` → `{ok, tool_count, probe: {tool, ok, latency_ms}, error?: {code, message_key, params, fix: {kind, url?}}}`
- `GET /api/connections/{id}/tools` → `[{name, description, schema, trust, enabled, proposed_trust, annotations}]`; `PUT /api/connections/{id}/tools/{name}` `{trust?, enabled?}`
- `PUT /api/connections/{id}/bots` `{mode: all|selected, bot_ids}`
- `POST /api/connections/{id}/signin` → `{route: web|desktop|manual|vm, authorization_url, session_id}`; `POST /api/connections/{id}/signin/complete` `{session_id, redirected_url}` (manual fallback)
- `GET /api/connections/{id}/calls?limit=20`
- `GET /api/connections/catalog` (server-served catalog JSON so copy/i18n and curation can change without a UI release; UI keeps a bundled fallback)

**Config file (`mcp.json`, extended; same loader)**
```json
{"mcpServers": {"github": {
  "url": "…", "transport": "streamable_http", "headers": {…}, "oauth": {…},
  "display_name": "GitHub (read-only)", "kind": "hosted", "catalog_id": "github-readonly",
  "tools": {"search_repositories": {"trust": "auto", "proposed": "auto", "proposed_by": "reviewer-v1"},
            "create_issue": {"trust": "ask"}},
  "disabled_tools": [],
  "bot_access": {"mode": "all"},
  "trusted_read_only_tools": []   // migrated into tools.*.trust=auto then removed
}}}
```
Owner: `internal/extensions/mcp.go` (struct), `management.go` (save/validate), new `internal/extensions/trust.go`.

**OAuth redirect strategy (replace `mcpOAuthRoute.ts` client-side guessing with server decision)**
1. Server computes `publicOrigin` from, in order: persisted setting (`settings.public_origin`, editable in Settings › Connection with a "Use this address" button that proposes `window.location.origin`), `TOFI_PUBLIC_ORIGIN`, `X-Forwarded-Proto/Host` **only when** `TOFI_TRUSTED_PROXY=1` (or the request comes from a loopback/RFC1918 proxy address), else `r.TLS`. File: `internal/app/extension_oauth.go:16-26` → new `internal/app/public_origin.go`.
2. Route selection (server): `desktop` if the client reports native support; else `web` if `publicOrigin` is https (self-signed allowed; note shown that some providers reject non-public hostnames); else `manual`.
3. **Manual route** (new, universal fallback): start with redirect URI `http://127.0.0.1:43821/oauth/callback` (the desktop loopback constant); the user's browser lands on an unreachable loopback page; UI shows "Paste the address you landed on" and posts it to `/signin/complete` which parses `code`/`state` and calls `OAuthCallbackForRedirect` (same primitive gogcli already uses: `internal/mcprunner/gog.go:218`). Works behind any proxy, self-signed IP, or plain HTTP; DCR providers accept loopback redirects (RFC 8252); user-supplied clients register the loopback URI once.
4. **VM route** stays available only behind Advanced ("Sign in on the shared computer") until the eval (section 3.8) shows the manual route covers the cases; then delete `vm_oauth.go`, `VMOAuthDialog.tsx`, guest `oauth.go`.
5. All OAuth HTML pages (`oauth_completion.go`) become locale-aware: accept `Accept-Language`, strings from a Go i18n table mirrored in `ui/src/locales/*/oauth.json`; `lang` attribute set accordingly.

**Token / API key**: unchanged storage; the catalog entry defines header/prefix (existing `tokenHeader/tokenPrefix`). Add "Replace key" in detail; never show the value.

**Test connection** (`InspectMCP` → `TestConnection`): connect + `tools/list` + **probe call**: pick the catalog's `probe` tool (e.g. GitHub `get_me`, Linear `list_teams`, Context7 `resolve-library-id {libraryName:"react"}`, Microsoft Learn `microsoft_docs_search {query:"azure"}`), or for custom servers the first tool whose schema has no required properties and whose `readOnlyHint` is true; otherwise skip the probe and say so. Probe is never counted as a Bot action and never needs approval. Error catalog (server returns `code`, UI translates): `auth_required`, `auth_invalid` (401/403 after sign-in), `dns`, `tls_untrusted`, `timeout`, `unsupported_protocol`, `unsupported_transport`, `redirect_origin_missing`, `redirect_rejected_by_provider`, `runner_unavailable`, `computer_asleep`, `plugin_start_failed`, `plugin_memory`, each with a `fix` hint.

**On-computer connections (local stdio) lifecycle**
- Keep Runner-in-VM; make the UX honest: status `Asleep · starts when used (~Ns)` using the last measured `WakeInfo.Seconds` + plugin start time; show "Memory ~X MB" from a per-plugin RSS sample taken after start (`internal/mcprunner/runner.go` after `start`).
- **No VM probe on page open**: `GET /api/connections` returns cached Runner status; `GET /api/connections/{id}/tools` for a local connection uses the persisted metadata/catalog index first; only "Test" and "Refresh tools" may wake the computer (explicit copy: "This wakes your computer").
- Memory budget: before install, require `runner_memory_mib_free ≥ 256` (guest reports free memory via `/v1/plugins` status); cap concurrently running plugins at 2 (configurable `TOFI_RUNNER_MAX_ACTIVE=2`); idle stop 5 min (from 10) when ≥ 2 plugins installed.
- Install UX: catalog of on-computer packages (v0.1: `@modelcontextprotocol/server-filesystem`, `@modelcontextprotocol/server-memory`, `mcp-server-time` (uvx), `@modelcontextprotocol/server-sequential-thinking`) with pinned versions in the server catalog; custom package form keeps the pinned-version requirement but derives `binary` from the package's `bin` field after install (read `package.json` in the staging dir) instead of asking the user.
- Model-side: a local connection whose plugin is asleep is still exposed directly (schemas from index); the first call shows the model `starting_on_computer` readiness (not an error) and the call proceeds with a 60 s budget.
- `mcpServerUsable` for gog: replace the run-start Runner probe with the persisted "connected" flag written by the OAuth finish handler (`local_mcp.go:224-242`) and cleared by disconnect; no VM traffic at run start.

**Catalog curation**
- v0.1 (ship): Microsoft Learn (no key), Context7 (API key), GitHub read-only + GitHub (PAT), Linear read-only + Linear (API key), Notion (OAuth/DCR), Gmail (personal, on computer, gog), on-computer starter packages above. Google Workspace hosted presets move to **Advanced › "Bring your own Google OAuth client"** (hidden by default).
- v0.2: DeepWiki (`https://mcp.deepwiki.com/mcp`, no auth — **verify before shipping**), Cloudflare docs MCP (no auth — verify), Sentry/Atlassian (OAuth/DCR — verify), Slack when a registered app can be shipped. Held research list leaves the UI.
- Each catalog entry gains: `probe`, `tool_defaults` (optional pre-reviewed trust map for vendor read-only endpoints), `display_name_i18n_key`, `wakes_computer: bool`.

### 3.5 Model-side tool use

**Exposure mode (hybrid)**
- At run start, for each usable connection (enabled, Bot-access allows this Bot, not blocked), load tool metadata **from the persistent index** (extend `PersistentMCPMetadataIndex` to store bounded schemas ≤ 64 KiB/server; file `internal/extensions/metadata_index.go`). No network.
- If `Σ enabled tools ≤ 24` **and** `Σ schema bytes ≤ 24 KiB`: register each tool **directly** as `<connection>__<tool>` (drop the `mcp_` prefix; keep the sanitized form) with description `"[<Display name>] <first ≤ 300 chars of description>"` and the original schema. Execution goes through the existing session slot with lazy connect (`mcp_session.go:247`), the same validation, readiness, gate and fence (`discovery.go:791-843` factored into `executeMCPCall(source, args)`).
- Otherwise: expose directly the tools of connections with ≤ 12 tools, and for large connections expose **one** facade: `find_tools{query, connection?}` (merges `search_mcp_catalog` + `search_mcp_tools`; returns ≤ 8 schemas, 24 KiB) and `call_tool{name, arguments}` (current `call_mcp_tool`, with `resolveKnown` kept). Delete `list_mcp_servers`.
- System prompt directory (`discovery_instructions.go`) becomes: per connection one line `"<display name>: <tool1>, <tool2>, … (N more via find_tools)"` capped at 2 KiB; the policy sentence shortened to: "Use connections when the task needs their data or actions; call tools directly when listed; use find_tools for others."
- Skills: inject the full body of skills marked `always` (new front-matter `trigger: always|on_demand`, default on_demand) up to 8 KiB; others remain `read_skill`.
- Thresholds are owner decision D1 (3.10).

**Description/schema normalization** (`internal/extensions/normalize.go`, new)
- Collapse whitespace; take sentences up to 300 chars; append `" Returns: …"` if the description has a "Returns"/"Response" sentence; append `" (read-only per server)"` when `readOnlyHint=true` (untrusted hint, never policy); prefix with display name.
- Schema: drop `$schema`, `title`, `examples` > 200 B; keep `enum`, `required`, `description`; if schema is not `type: object`, wrap as `{input: schema}` and unwrap on call.

**Argument validation and repair**
- Keep schema validation + schema hint. Add pre-validation coercion: string→integer/number/boolean when the schema type demands it and the parse is exact; `"null"`→omit for optional fields. Log coercions as telemetry.
- Repair limit stays 3; on the 3rd failure the error adds one worked example built from `required` + defaults.

**Timeouts / retries / outcomes** (`mcp.go:685-750` rewrite)
- Timeouts: default 30 s; per-tool override from catalog; writes 45 s; on-computer first call 60 s (includes start).
- Classification: (a) error before the request was written (connect, auth refresh, readiness) → `Transient/not_executed` → automatic retry ×2 with backoff for all tools; (b) HTTP 401 → refresh token once, then `auth_required` outcome and a connection status flip to Needs sign-in; (c) JSON-RPC response with `isError:true` → `Permanent/mcp_reported_error`, **certainty `not_executed_per_server`**, message included, repair allowed; (d) timeout or EOF after the request was written → `Uncertain/verify_effect` **only for tools whose trust ≠ auto**; for `auto` (read) tools retry ×2.
- Result size: 32 000 runes inline; if larger, write the full text to `<bot workspace>/.tofi/tool-results/<call id>.txt` and append `"[truncated: full result saved at … ; call read_file with offset to continue]"`.
- Caching: keep catalog/metadata caches; add a 60 s per-run memo of identical `(tool, args)` read calls to absorb model repeats.
- Telemetry: new table `connection_calls(id, run_id, bot_id, connection, tool, started_at, latency_ms, outcome_code, approval_path (auto|ask_human|ask_reviewer|blocked), bytes_in, bytes_out, retries)`; written in `executeMCPCall`; surfaced in S4 and the eval harness. Owner: `internal/app/connection_calls.go`.

**Prompt/tool-schema changes (concrete)**
- Delete the three long guidance strings in `discovery.go:354,523,845`; `find_tools` description: "Find tools on connected services by what you need (e.g. 'list open pull requests'). Returns exact tool names and input schemas; call them with call_tool."
- `call_tool` description: "Call a connected service tool by exact name with arguments matching its schema. Read-style tools run immediately; other tools may wait for the user's approval."
- Direct tools carry `x-tofi-trust: auto|ask` in their description tail ("Runs immediately" / "May ask the user") so the model can plan around waits.

### 3.6 Approval policy

**Trust model (default)**
- Each tool has `trust ∈ {auto, ask, blocked}`. On first registration (test or first discovery), a **classification pass** asks the Reviewer model once per tool: given name, description, schema, annotations → `effect ∈ {read, write, send, purchase, delete, publish, account, unknown}` and `proposed_trust` (`read`→auto; others→ask; `unknown`→ask). This is a model judgment of what the action does — no keyword rules. Stored as `proposed`; the owner confirms or edits in S4; catalog `tool_defaults` may pre-fill for vendor read-only endpoints (`/mcp/readonly`), still editable.
- Per call: `auto` → execute, log; `ask` → card (S6) unless an "always allow (tool, Bot)" rule exists; `blocked` → `Denied/tool_blocked`.
- "Always allow this tool for <Bot>" writes `tools.<name>.always_allow_bots += bot_id`; "for all Bots" available in S4 only.
- Per-server trust: S4 "Trust all reads: Auto" bulk action; "Everything asks" bulk action.
- Argument secret-field refusal (`containsMCPSecretField`) becomes a *card warning* ("this argument looks like a secret") instead of a hard block; hard block only if the value matches a saved credential value.

**Where the Reviewer (AutoReview v5) still adds value**
- Only for `ask` tools, when the owner enables "Let the Reviewer approve low-risk Ask actions". Keep v5 prompt and packet, but: timeout 10 s (D3), one retry on transport error, packet ≤ 32 KiB (drop historical tool records beyond the current run), and **context_gap no longer closes the proposal** — it falls through to the human card with the reviewer's reason as the card note. Shadow mode removed (replaced by the `connection_calls.approval_path` telemetry and the S5 recent decisions list).
- Computer actions keep self-labeled effect + `guardAction`; unify the human-approval memory with the same "always allow" rule store keyed by `(site host, effect)` for the run (no cross-run persistence for web actions).
- Latency budget per call: auto ≤ 50 ms overhead; ask+reviewer ≤ 10 s before a card appears; human card expiry 15 min (was `ExpiresInSeconds` per question; set a uniform default in `questions.go` creation for approval type) and reviewer validity 10 min (from 2).

**Principle check**: risk is judged by what the exact action does (model-declared effect for computer actions; reviewer-proposed effect per tool at registration; reviewer per call for ask tools when enabled; human decides for the rest). No hardcoded keyword routing anywhere; the only string matching that remains is UI mapping of server error codes.

### 3.7 Migration and deletions

Migration (`internal/app/migrate_connections.go`, run once at startup):
- `mcp.json`: for each server add `display_name` (catalog name if `url` matches a catalog entry, else the id), `kind` (`local` if id starts with `local_` or url host is `account-computer`, `builtin` for `local_gog`, else `hosted`), `tools` map from `trusted_read_only_tools` → `trust: auto`; `tool_allowlist` → `enabled` flags; `tool_denylist` → `trust: blocked`; delete `bot_allowlists`, `trusted_read_only_tools`, `transport` values other than `streamable_http` (log).
- `auto_review_settings.mode`: `auto` → `reviewer_enabled=1`; `shadow`/`off` → `0`. Keep tables `mcp_auto_reviews`, `mcp_call_approvals`, `mcp_call_execution_claims` (history), stop writing shadow rows.
- Pending questions of type approval remain valid; `MCPReviewDisplay.status` values `shadow_*` rendered as "observation" in history only.
- Runner manifest unchanged.

Delete (after one release of aliases):
- UI: `ui/src/MCPSettings.tsx`, `LocalMCPPanel.tsx`, `IntegrationBrowser.tsx`, `AutoReviewSettings.tsx`, `mcpOAuthRoute.ts`, `VMOAuthDialog.tsx` (D4), `ExtensionPanel.tsx` (Skills page becomes `SkillsPage.tsx`), locale namespace `extensions.json` → `connections.json` + `skills.json` + `oauth.json`; the SSE option; the held-integrations section; the animated `MCPTestMotion/OAuthLinkMotion` (optional keep in motion-lab).
- Server: `list_mcp_servers` and `search_mcp_catalog` tools; `shadow` review path (`mcp_shadow_review.go`); Chinese literals in `oauth_completion.go`, `extension_management.go`, `local_mcp.go`, `vm_oauth.go:103`; `manage_extensions` write actions behind an owner toggle (default off; list/test stay).
- Config: `BotAllowlists`, `TrustedReadOnlyTools` fields after migration.

### 3.8 Evaluation harness

Location: `scripts/tools_eval.py` (same style as `scripts/acceptance.py`: disposable instance, `/health` identity check, HTTP only) + task file `docs/agent-plan/tools-eval/tasks.json` + synthetic fixture server `internal/extensions/evalfixture` (built from `mcp_fixture_test.go` helpers, run as a standalone binary `cmd/tofi-eval-mcp`) exposing mixed read/write tools with deterministic results.

Connections under test (no paid accounts):
1. `fixture` (synthetic, local HTTP): `lookup_order{id}` (read), `create_ticket{title}` (write), `send_message{to,body}` (send), `delete_record{id}` (delete), `flaky_read{}` (fails first call with -32603 then succeeds), `big_read{}` (returns 120 k chars), `schema_strict{count:int}` (rejects strings).
2. Microsoft Learn `https://learn.microsoft.com/api/mcp` (no auth).
3. Context7 `https://mcp.context7.com/mcp` (free API key provided via env `EVAL_CONTEXT7_KEY`; skip if unset).
4. GitHub read-only `https://api.githubcopilot.com/mcp/readonly` (PAT via `EVAL_GITHUB_PAT`; skip if unset).
5. On-computer: `@modelcontextprotocol/server-filesystem` pinned, rooted at the Bot workspace; `mcp-server-time` via uvx (skip if no computer).
6. Notion (OAuth) connect-only task, manual (not CI).

Task set (each with expected tool and a deterministic pass check):
- E1 "What is my order 1042's status?" → `fixture.lookup_order{id:"1042"}`; no approval expected.
- E2 "Open a ticket titled 'Printer down'" → `create_ticket`; exactly one approval card expected.
- E3 "Send 'hi' to bob" → `send_message`; one card; decline → model must not retry.
- E4 "Order 7 status" with `flaky_read` wired as the only lookup → expect automatic retry and success, no "verify effect" text in the final answer.
- E5 "Summarize order history" → `big_read`; expect truncation marker and no crash; answer mentions saved file.
- E6 "Count 5 things" → `schema_strict{count:"5"}` coerced or repaired within ≤ 2 attempts.
- E7 "How do I create an Azure Function in Python?" → Microsoft Learn `microsoft_docs_search`; no card.
- E8 "Latest React useEffect docs" → Context7 `resolve-library-id` then `get-library-docs`.
- E9 "List my 5 most recently updated repos" → GitHub `search_repositories`/`list_*`; no card (read-only endpoint pre-trusted).
- E10 "Save a note file named todo.md with 'buy milk' in my workspace" → filesystem `write_file`; one card; verify file.
- E11 "What time is it in Tokyo?" → time server; expect computer wake + plugin start within 90 s.
- E12 Connect-flow tasks (scripted UI via the browse skill or Playwright): add Microsoft Learn (count clicks, time), add GitHub with PAT, add Notion via manual route; expected ≤ 4 clicks for no-auth, ≤ 5 for key, ≤ 7 for OAuth; test stage shows probe result.
- E13 Negative: wrong PAT → test error code `auth_invalid` with fix hint; SSE config import → `unsupported_transport`.
- E14 Chinese prompts for E1, E7, E9 (search quality without Codex expansion).

Metrics (written to `docs/agent-plan/tools-eval/results/<date>.json` by the script): connect time and click count per connection; task success (answer check); correct-tool rate (first tool call = expected); wrong-arg rate (count of `invalid_arguments` outcomes / calls); approvals asked per task; end-to-end latency and per-call latency p50/p95 (from `connection_calls`); retries; tokens (from `usage`). Baseline is run on current `main` first (with the fixture pointed at the existing `/api/extensions` paths) so the redesign has a before/after.

How to run: `go build ./cmd/tofi-eval-mcp && python3 scripts/tools_eval.py --model <id> --tasks docs/agent-plan/tools-eval/tasks.json [--github-pat-env EVAL_GITHUB_PAT] [--with-computer]`; the script creates a Bot, connections via API, posts each task as a message, polls `/events`, answers approval cards per the task's `approval_policy` (`accept|decline|none`), and asserts. Live runs are opt-in (`TOFI_LIVE_EVAL=1`) like `TestLiveInstalledCapabilityDiscovery`.

### 3.9 Workstreams

Sequencing: WS0 → (WS1 ∥ WS5) → WS2 → WS3 → WS6 → WS4 → WS7 runs baseline first (before WS1) and again after each WS. WS8 last.

**WS0 Baseline eval (1-2 days)** — owner: harness author. Files: `scripts/tools_eval.py`, `docs/agent-plan/tools-eval/tasks.json`, `cmd/tofi-eval-mcp/main.go`, `internal/extensions/evalfixture/`. Acceptance: baseline numbers for E1-E14 on current main recorded. Risk: live services flaky → every live task has a skip flag.

**WS1 Connection core (server)** — files: `internal/extensions/mcp.go`, `management.go`, new `trust.go`, `normalize.go`, `metadata_index.go` (schemas), `internal/app/connections_api.go` (new routes), `internal/app/migrate_connections.go`, `internal/app/connection_calls.go`. Acceptance: new API serves list/detail/tools/test(with probe)/bots; migration converts a fixture `mcp.json` with `trusted_read_only_tools` and `local_*` entries; old `/api/extensions/mcp*` aliases pass existing tests (`management_test.go`, `token_catalog_test.go`, `extensions_e2e_acceptance_test.go`). Risk: config fence semantics — all writes must still take `mcpConfigFence`.

**WS5 Model-side exposure and outcomes** — files: `internal/extensions/discovery.go` (factor `executeMCPCall`, add direct registration, `find_tools`/`call_tool`), `discovery_instructions.go`, `mcp_outcome.go`, `mcp.go:685-750`, `internal/app/app.go:3123-3143`, `internal/app/context.go` (cached schemas become unnecessary for direct tools), `internal/app/skills` injection. Acceptance: E1/E7/E9 complete in one tool call when direct; E4 retries; E5 truncation; E6 coercion; `TestRunPromptBudget*` still within 16 000 runes; no behavior change for servers > threshold except tool names. Risk: prompt budget — directory capped at 2 KiB; large schemas excluded by the 24 KiB rule.

**WS2 Connections UI** — files: `ui/src/connections/{ConnectionsPage,AddConnection,ConnectSheet,ConnectionDetail,ToolTrustTable,ConnectionStatus}.tsx`, `ui/src/catalog/` (moved from `integrationCatalog.ts`), `ui/src/SettingsShell.tsx` (tabs), `ui/src/App.tsx:1846`, locales `connections.json` ×7, delete list in 3.7. Acceptance: `npm --prefix ui run test:i18n` clean; E12 click counts met; empty state and error states covered by component tests (`ui/scripts/test-integration-catalog.mjs` replaced by `test-connections.mjs`); no Runner/VM request on page open (assert via server log in a test).

**WS3 OAuth routes** — files: `internal/app/public_origin.go` (new), `extension_oauth.go`, `extension_management.go` (signin/complete), `oauth_completion.go` (i18n), `ui/src/connections/SignIn.tsx`, `ui/src/locales/*/oauth.json`. Acceptance: Notion connect succeeds via web (HTTPS), via manual paste on plain HTTP, and behind a proxy with `TOFI_TRUSTED_PROXY=1` (test with httptest + forwarded headers); completion page renders in the request language; VM route hidden under Advanced. Risk: forwarded-header spoofing — only honored when the peer is loopback/RFC1918 or the env flag is set.

**WS6 Approvals and trust** — files: `internal/app/mcp_call_approval.go` (trust lookup, always-allow rules, secret-field warning), `questions.go` (card fields: connection, tool summary, effect; `always_allow` answer), `mcp_auto_review.go` (10 s, retry, context_gap→human), `mcp_review_contract.go`, delete `mcp_shadow_review.go`, new `internal/app/tool_classification.go` (reviewer classification pass), `ui/src/ApprovalCard.tsx`, `QuestionCard.tsx`, `ui/src/approvals/ApprovalsPage.tsx`, `ui/src/locales/*/tasks.json`. Acceptance: E1/E7/E9 zero cards; E2/E3/E10 exactly one card; "Always allow" removes the card on a second identical task; reviewer-off path adds < 50 ms; reviewer-on path shows a card within 10 s on reviewer timeout; existing claim/fence tests pass (`mcp_call_approval_test.go`, `mcp_auto_review_v5_test.go`, `approval_*`). Risk: policy version bump invalidates unclaimed auto decisions (already handled by `migrateAutoReview`).

**WS4 On-computer connections** — files: `internal/app/local_mcp.go` (status caching, no probe at run start, memory check), `internal/mcprunner/runner.go` (RSS sample, max active, idle), `install.go` (derive bin), `internal/computer/guest/runner.go` (free memory in status), `ui/src/connections/AddOnComputer.tsx`. Acceptance: E10/E11 pass; opening Connections with a hibernated computer does not wake it (assert `WakeInfo` unchanged); install refused with `plugin_memory` when free < 256 MiB; status shows measured start seconds. Risk: 1 GiB guest — if two plugins cannot coexist with Chrome in practice, raise `COMPUTER_MEMORY_MIB` guidance in `tofi_host.py` (owner decision outside this doc).

**WS7 Eval after each workstream** — rerun `scripts/tools_eval.py`; acceptance gate for the whole program: task success ≥ 90 % on E1-E11, correct-tool rate ≥ 90 %, wrong-arg rate ≤ 10 %, approvals per read task = 0, connect clicks per E12 target, p95 per-call latency (auto path) ≤ 3 s on fixture.

**WS8 Cleanup and docs** — delete aliases and files listed in 3.7; update `internal/extensions/DISCOVERY.md`, `README.md`, `docs/integrations/`; move held research to `docs/integrations/held.md`.

### 3.10 Owner decisions needed (max 5)

| # | Decision | Recommended default |
|---|---|---|
| D1 | Direct-exposure threshold | Direct when ≤ 24 enabled tools and ≤ 24 KiB schemas; per-connection direct when ≤ 12 tools; `find_tools` facade only above that. Revisit after WS7 numbers. |
| D2 | Default trust for newly connected tools | "Reads run automatically, everything else asks", with reviewer-proposed per-tool defaults shown for confirmation in the Done stage (not blocking). Vendor read-only endpoints pre-trusted. |
| D3 | Reviewer (AutoReview) scope | Keep as opt-in for `ask` tools only; 10 s timeout with human fallback; drop shadow mode and the Models-page placement. |
| D4 | OAuth fallback on non-HTTPS installs | Add the manual "paste the address" route now; keep the VM route under Advanced for one release, delete it if WS7 shows zero need. |
| D5 | Catalog v0.1 scope | Hide the six Google hosted presets behind Advanced ("bring your own OAuth client"); keep Gmail (gog) as the Google path; ship Microsoft Learn, Context7, GitHub ×2, Linear ×2, Notion, and four on-computer starter packages. |

---

### Critical Files for Implementation
- `/Users/jackzhao/Developer/sentiosurge/tofibot-dicebear/internal/extensions/discovery.go` — facade tools, call path, gate; becomes `executeMCPCall` + direct registration + `find_tools`/`call_tool`
- `/Users/jackzhao/Developer/sentiosurge/tofibot-dicebear/internal/extensions/mcp.go` — `MCPServerConfig` (trust/bot access), limits, `callMCPToolObserved` outcome classification and retries
- `/Users/jackzhao/Developer/sentiosurge/tofibot-dicebear/internal/app/mcp_call_approval.go` — approval gate; trust lookup, always-allow rules, card content
- `/Users/jackzhao/Developer/sentiosurge/tofibot-dicebear/internal/app/extension_oauth.go` (+ `extension_management.go`, `oauth_completion.go`) — public origin resolution, sign-in routes, manual completion, i18n of OAuth pages
- `/Users/jackzhao/Developer/sentiosurge/tofibot-dicebear/ui/src/MCPSettings.tsx` (+ `LocalMCPPanel.tsx`, `SettingsShell.tsx`, `App.tsx:1846`) — replaced by the Connections / Approvals pages
