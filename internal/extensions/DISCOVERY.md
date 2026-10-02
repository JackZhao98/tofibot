# Run-scoped extension discovery

`PrepareDiscoverableForBot` retains `PrepareForBot` authorization and client
lifecycle, but exposes bounded discovery tools instead of every MCP schema:

Remote MCP clients use `github.com/modelcontextprotocol/go-sdk` v1.8.0 and
require protocol `2026-07-28`. They send `server/discover`; a sending middleware
blocks the SDK's legacy `initialize` fallback before any older request reaches
the server. Discovery must advertise the required revision, and the connected
session is checked again. Saved protocol/session headers cannot override SDK
negotiation. Existing `sse`/`legacy_sse` configuration remains editable, but
connections are refused before network activity because HTTP+SSE does not
support this revision. Settings inspection and tool search return safe,
actionable `unsupported_protocol` or `unsupported_transport` diagnostics.

OAuth uses the official `auth.OAuthHandler` transport interface, SDK `oauthex`
dynamic registration, and `oauth2` PKCE/code/refresh operations. Private token
files keep their existing JSON format; state binding, resource parameters,
credential generation guards, refresh serialization, and disconnect remain in
Manager. The SDK handles per-session `ttlMs`/`cacheScope` cache hints. The Tofi
search snapshot and persistent metadata index retain the policies documented
below; protocol upgrades change their configuration fingerprints so old
protocol metadata is rebuilt.


- `list_mcp_servers({query?, offset?})`: paginated server names and transports from local
  configuration; this does not connect to remote servers.
- `search_mcp_catalog({query, offset?, limit?})`: ranks tool names and short descriptions
  across configured servers, returning up to 50 metadata rows and no input
  schemas. The first successful connection saves a private, bounded per-server
  name/description index beside the MCP configuration; later searches and App
  restarts reuse it without connecting. The ten-minute memory cache is only a
  hot layer, not a metadata expiry policy. A first search for uncatalogued
  servers has a ten-second wall budget. `indexed_servers`, `total_servers`,
  and `complete` distinguish a global result from a partial index. Config
  changes, tool-list-change notifications received on an active connection,
  and tool-call errors invalidate entries; `refresh: true` explicitly re-fetches
  them. A server that changes silently can remain stale until explicit refresh
  or another invalidation. Index hits do not authorize or connect tools.
  Select a candidate's server and use `search_mcp_tools` to inspect its full
  schema before calling it. Ranking is lexical first, with the bounded Luna
  query expansion only on a miss.
- `search_mcp_tools({query, server?, server_offset?, tool_offset?, limit?})`: BM25 lexical ranking on name and
  description; extra unmatched query words no longer discard relevant tools. In an App run with a configured Codex connection, a lexical miss can invoke GPT-6 Luna query expansion (up to four English phrases, three-second deadline) over the same authorized server page. This is AI-assisted query expansion, not a full embedding index. Supplying `server` limits connection and discovery to that server.
  Without a server, scans at most eight services; follow next_server_offset. Failed
  services return diagnostics and can be retried. Successful tool metadata is
  cached across runs for up to two minutes in a process-local, 32 MiB LRU;
  each search still opens and discovers a fresh MCP connection. Saving or
  deleting a server clears its catalog entry, and tool-call errors clear it
  for the next discovery. Remote tool changes may remain stale until TTL
  expiry; this cache is not an authorization decision.
  If a plausible service has unclear terminology or a keyword miss, use `query: "*"`
  with a specific `server` to browse schemas in deterministic name order. Targeted
  keyword searches and browsing return `next_tool_offset` when more matching
  entries exist; pass it as `tool_offset` with the same server and query. Wildcard
  browsing across all servers is rejected. A directory listing never authorizes
  calls, and omitted schemas are never callable. `omitted_schema_count` reports
  schemas too large to fit even alone; pagination advances past them rather than looping. A schema that fits alone but exceeds the remaining page budget is deferred to the next page, not omitted.
  Default 5, maximum 10 entries per page, at most 32 KiB of JSON. Each result
  contains the callable name, description, and input schema. Oversized schemas
  are omitted; `truncated` signals omitted matches. Narrow the query when needed.
  `search_method` reports `lexical`, `ai_expanded`, or `browse`; AI failure falls
  back to the original lexical result. Pagination reuses first-page ranking.
- `call_mcp_tool({name, arguments})`: names returned by a search in this
  prepared run may be invoked. A bounded schema reference from the same Bot
  conversation may also be accepted when its opaque per-process configuration
  version, current server configuration, and allow/deny policy still match.
  The direct call establishes a fresh connection and never treats the cached
  schema as proof of OAuth authorization or current availability. The cache is
  limited to three schemas discovered within 24 hours, is dropped on process
  restart or any configuration change, and is rejected with rediscovery
  guidance when it no longer matches. A search in another Bot or conversation
  confers no access.
- `list_skills({offset?})`: short metadata, up to 50 entries and 32 KiB per page;
  `next_offset` indicates another page.
- `read_skill({name})`: loads the selected skill body from disk at call time.
- `read_skill_file({name, path})`: reads supporting files within that skill root.

Initial instructions include a local capability directory capped at 4 KiB: configured MCP source names and Skill names/purposes (descriptions capped at 240 bytes). Each category has a reserved budget; omitted entries remain discoverable through pagination. Metadata is JSON-escaped, treated as untrusted, and does not include connection credentials, URLs, Skill bodies, or remote schemas. Configured does not imply authenticated or reachable.
Global MCP allow/deny policies are applied before search. Installed MCP servers
and skills are workspace-wide and available to every Bot; legacy per-Bot grant
and skill activation files are ignored. The discovery facade does not put every
  remote schema in the initial prompt: each run connects to configured servers
only when `search_mcp_tools` is called, or when it directly invokes an accepted
bounded recent schema reference. Search results remain cached for that run.

This optimization reduces model context and avoids startup network discovery.
Call `Prepared.Close` at run end, as before. Both the run and individual call
cancellation contexts apply.

External-data/action routing first checks relevant installed capabilities unless
an explicit user source/method takes precedence. Names-only listing is not tool
inspection. The model can select a known server directly without repeating the
index call, inspect matching Skills, and stop once it has sufficient capabilities.
Stable explanations and self-contained writing/reasoning do not require tool
calls. Relevance remains a model decision based on user intent and metadata;
there is no domain keyword router, forced unrelated MCP invocation, separate
triage model, or guarantee that every model will follow the policy. The bounded
browse fallback removes lexical-search dead ends without injecting all schemas.

## System prompt budget and acceptance evidence

The run assembler reserves fixed policies before allocating optional context.
Core conversation rules, evidence requirements, durable-work rules, computer
rules, capability scope and discovery rules are retained in full. The 16,000-rune
cap replaces an 8,000-rune tail trim that could remove discovery from a normal
computer-enabled run. Bot instructions retain their original text within the
remaining budget, with an explicit truncation notice when necessary. Directory
metadata receives a bounded share and preserves whole JSON rows; omitted names
remain discoverable through the index tools. Short configurations are not padded.
Fixed policies exceeding the cap fail the run explicitly rather than silently
losing required instructions. History and memory retain their separate budgets.

`TestRunPromptBudgetPreservesDMAndGroupPoliciesWithComputer` exercises the real
conversation context builder and computer instructions with a synthetic local VM
metadata endpoint. The measured fixed policies are approximately 9,953 runes for
a DM and 12,401 for a natural group; short Bot instructions yield approximately
10,132 and 12,580 runes respectively. Long Bot configuration remains bounded by
16,000. These counts can change as policy wording evolves; tests assert preserved
policies, original Bot text and the bound rather than exact counts.

`TestLiveInstalledCapabilityDiscovery` is opt-in (`TOFI_LIVE_DISCOVERY=1` and an
access-only credential snapshot path). Its two synthetic domains use local MCP
schemas/results, randomized verification codes and a nonfunctional browser
fallback; a self-contained writing case checks that tools are not mandatory.
The initial live run passed both discovery cases and the writing control using
a short isolated policy. That result does not establish production prompt
assembly behavior. The updated test uses the production context builder and
assembler, including computer rules and group context. The updated live run
passed on 2026-09-19 with codex-gpt-5.6-luna: the DM/computer prompt used 10,196
runes and the group/computer prompt with long Bot instructions used 16,000. Both
external-data cases selected `search_mcp_tools` then `call_mcp_tool`, returned the
random verification code, and made no browser call; the writing control made no
tool calls. The temporary access-only snapshot was deleted and the source
credential hash remained unchanged. Synthetic logs are retained on the acceptance
host at `/tmp/tofi-desktop-mvp-acceptance/live-discovery-production.log`.
Its tool registry remains synthetic and reduced; this run does not cover the
production queue, real MCP services or all model decisions. Default test runs
skip model calls.
