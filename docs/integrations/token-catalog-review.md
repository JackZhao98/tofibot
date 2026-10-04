# Initial token-capable catalog review

## Current-main composition

This private composition extracts Astra's eleven-file catalog and credential-safety
slice from cleared `68c66fb387d7b7eec36ec9b42690915c3a8636d2` onto verified main
`33a1f83e9f6b0df575f0d2071943ce2bf391a35e`. The historical review below records its
source evidence and limits; it is not new provider or package qualification.
The original icon candidates and stale acceptance.json are intentionally omitted.
No Notion adapter, protocol change, tool-approval exemption, activation or release
is included. Existing expiry/Stop and unrelated UI remain main's source.
Exact resulting-source bindings and focused composition checks are recorded in
the private handoff packet; this new composition requires independent review.

Candidate based on verified remote main `2d304632558b4497d98ec7759be6a181d7362a9c`, in isolated branch `codex/token-catalog-20261003`. Reviewed 2026-10-03. No publish, merge, provider installation, production mutation, live credential read or live MCP request was performed. Repository AGENTS.md was read; no .agents/skills directory or further AGENTS.md exists in this main snapshot. Other workers' mail, metadata, release, portability and installer scopes are untouched.

## Existing capability

`ui/src/integrationCatalog.ts` already offered GitHub/Linear Bearer presets, Context7 custom API-key header, Microsoft Learn no-auth, Notion hosted OAuth and six Google presets. Google remains deferred and unchanged. `MCPSettings.tsx` has an existing password input and submits headers privately to the settings API. `internal/extensions/management.go` masks all saved header values and writes configuration atomically at mode 0600. Nil header map preserves all values; masked/empty values preserve the corresponding existing key; an explicitly supplied map without a key removes that key. These permissions protect files at rest; they are not encryption or an account authorization proof.

Local MCP Runner supports owner-selected, pinned npm/PyPI packages and stdio-to-private-HTTP bridging. `install.go` keeps secret environment values in mode-0600 files and omits those values from the manifest. Its generic installation form does not establish vendor provenance or protocol compatibility. No new package installer or general PAT/OAuth system is introduced.

Skills already support workspace-wide folder import, manual SKILL.md entry, three original TOFI starter workflows, `list_skills`, `read_skill`, and bounded reference-file reads. Install validates matching YAML name/description, safe paths, at most 256 files and 1 MiB aggregate content. Read access is bounded and rejects directory escapes/symlinks. Imported scripts are not automatically executed. Instructions/reference files are supported; binary assets are not faithfully imported by the UI's text-based folder reader. A Skill is not a provider credential grant.

## Provider decisions

| Service | Proven source and auth contract | Candidate decision / remaining gap |
| --- | --- | --- |
| GitHub | Vendor repository; hosted Streamable HTTP at `https://api.githubcopilot.com/mcp/`; PAT in `Authorization: Bearer …`. Vendor remote docs list `/mcp/readonly`. Pinned upstream uses Go SDK 1.8.0, the same SDK as TOFI. | Add GitHub read-only PAT preset. Source/auth vetted; compatibility with the hosted deployment is an inference from its documented use of this library, not live availability proof. Runtime discovery and account/org policy still apply. |
| Notion hosted | Vendor `https://mcp.notion.com/mcp`, user OAuth rather than integration PAT. | Preserve existing OAuth preset. No fake Bearer-token mode for this hosted endpoint. |
| Notion token | Historical vendor-origin source `@notionhq/notion-mcp-server`, version 2.5.2, default stdio, `NOTION_TOKEN`. Its own HTTP auth token is distinct from the Notion integration token. | Historical reference only: the pinned vendor README retires active maintenance/support for this local server. It is outside first-wave support. Declared SDK 1.29.0 supports only through 2025-11-25, and package ownership/integrity/runtime remain unverified. Any TOFI maintenance commitment or legacy adapter activation is a separate decision. |
| Discord | Community `SaseQ/discord-mcp` has active source history and documented Discord bot `DISCORD_TOKEN`, optional guild ID, Java/Docker HTTP singleton. Community `v-3/discordmcp` has two commits and no package bin field. `hanweg/mcp-discord` is another Python candidate. None is a Discord-owned upstream. | Hold installable preset: Java/Docker deployment is outside current npm/PyPI installer contract; reviewed alternatives lack proven current protocol/package compatibility. Self-hosted HTTP auth and bot channel permissions require explicit validation. Do not invent a Discord-hosted MCP endpoint or accept a personal user token. |
| Slack | Slack-owned Streamable HTTP endpoint `https://mcp.slack.com/mcp`; confidential user OAuth, no DCR. Registered fixed app identity is required; only published or internal apps may use it. Scopes depend on tools. | Hold preset: a generic bot token is not this official auth contract; TOFI's generic client identity is not a registered Slack app identity. No OAuth redesign or unregistered-app workaround. A community bot-token bridge must separately prove maintenance, package provenance, current protocol and its scopes before TOFI can support it. |
| Yahoo Finance | Community `Alex2Yang97/yahoo-finance-mcp` uses yfinance/FastMCP, no token. Main declares Python >=3.14.6, while current Runner Debian Python is older. PyPI's same-named 0.1.2 project lists maintainer ycjcl868, and published metadata differs from GitHub main. | Hold: no verified Yahoo-owned MCP or token contract; published package provenance and runtime compatibility are unresolved. Do not silently install a similarly named package or describe community yfinance as Yahoo upstream support. |

TOFI-supported describes TOFI curation, not vendor ownership or endorsement. The UI type now distinguishes vendor/community provenance and uses neutral “connection documentation” instead of calling every linked project official. The review records held candidates; it does not place unproven providers in the clickable installation catalog.

## Supported catalog follow-up

The separate `codex/supported-catalog-20261003` branch preserves reviewed catalog head `c0f8f0fc8ca54bd4c176a97626b4dce208a4c5a7`, descended from main `2d304632558b4497d98ec7759be6a181d7362a9c` verified again by remote reference lookup on 2026-10-03. Other workers may advance main; the parent owns fresh integration checks and publication. This branch does not include the Notion adapter source or opt any provider into it.

The addable service catalog contains GitHub PAT (read-only and standard), Notion hosted OAuth, Linear API key (read-only and standard), Context7 API key and Microsoft Learn without credentials. Each service has explicit vendor provenance. Existing Google entries remain unchanged. New `linear-readonly` uses the same documented vendor transport and Bearer contract as the existing Linear preset, with `/mcp/readonly` and a restricted Read API key. Provider read-only modes never create a TOFI approval exemption. These are source/auth-vetted connection presets, not live-account or deployed-protocol acceptance claims: strict discovery still decides runtime compatibility when a user connects.

The browser shows auth method and provider/community origin, filters by a trimmed case-insensitive service search, and marks already-added endpoints. Held research is represented by a separate type without endpoint, install command, package or credential fields. The unavailable section explicitly labels Notion token as retired historical vendor-origin source, excludes it from first-wave support and links historical project documentation. Discord, Slack and Yahoo Finance remain held research; Discord and Yahoo candidates are community-maintained. None can populate the connection form. Actual component tests enforce retirement disclosure and the absence of setup buttons and credential inputs for held records.

Notion adapter source `247a737967b1edb9f5d325b4ca460659d348e895` was independently cleared as a source candidate, including its replacement-race fix. That review does not establish ongoing vendor maintenance or TOFI support for retired source. Actual package ownership/integrity, installed bytes, runtime compatibility and provider behavior remain held. The registry metadata lookup was unavailable and the npm version page returned 403; no alternate artifact access was attempted. The [pinned vendor README](https://github.com/makenotion/notion-mcp-server/blob/730ae781ba28beeaf0865025a3f2ed4c25ea2387/README.md) identifies hosted Notion MCP as the actively supported alternative. Hosted OAuth remains separately addable; the historical token reference remains absent from `getIntegrationPreset` and the installable catalog.

The retirement correction is checked with `node ui/scripts/test-integration-catalog.mjs --retired-notion`, which renders the actual Notion rows and skips unrelated catalog/header assertions. It requires explicit historical origin/retirement/first-wave exclusion, historical documentation, no token setup or credential input, and a separate hosted OAuth preset. Earlier cleared provider and credential checks are reused rather than rerun for this presentation correction.

Source/auth contracts were rechecked against [GitHub's pinned remote documentation](https://github.com/github/github-mcp-server/blob/71ef8266e48110974b13aef50b4df6ff9914ff68/docs/remote-server.md), [Notion's hosted MCP overview](https://developers.notion.com/guides/mcp/overview), [Linear's MCP documentation](https://linear.app/docs/mcp), [Context7 client documentation](https://context7.com/docs/resources/all-clients), [Microsoft Learn MCP overview](https://learn.microsoft.com/en-us/training/support/mcp) and [Slack's authentication contract](https://docs.slack.dev/ai/slack-mcp-server/). Community blockers remain supported by their own [Discord deployment documentation](https://github.com/SaseQ/discord-mcp) and [Yahoo runtime declaration](https://github.com/Alex2Yang97/yahoo-finance-mcp/blob/main/pyproject.toml). No provider package was downloaded or executed, and no live credential, account call or production change was used.

[Context7's current vendor implementation](https://github.com/upstash/context7/blob/master/packages/mcp/src/index.ts), inspected on 2026-10-03, explicitly accepts `Context7-API-Key` alongside Bearer authentication and declares native modern `2026-07-28` HTTP serving. The existing custom-header preset is retained; hosted deployment compatibility still requires runtime discovery.

## Credential safety change

Saving a new endpoint cannot implicitly carry saved headers or an OAuth client using omitted, empty or masked retention markers. Such edits fail before config writes, leaving the old connection intact. Explicitly re-entering credentials or removing header keys supports intentional edits. The pending UI token draft is cleared when the address changes. Existing same-endpoint preserve/replace/delete semantics remain intact. No approvals are exempted and no read-only annotation is treated as authorization.

## Verification and retained source evidence

`ui/scripts/test-integration-catalog.mjs` exercises catalog URLs/provenance, GitHub read-only PAT, no-auth empty headers, custom keys/prefixes and preserve/replace/delete behavior. `internal/extensions/token_catalog_test.go` covers private saved files, masks, refused destination moves, unchanged configs after rejection and disposable no-auth/Bearer/custom-header discovery. Fixture connection tests only use `server/discover` and `tools/list`; they never execute a tool. Existing legacy-protocol rejection and Skill import tests are included in focused validation. See accompanying acceptance evidence for actual completed checks and environment limits; neither synthetic tests nor a UI build prove live providers work.

Named SVG candidates, preview and rights notes remain separate evidence in the
original reviewed source branch and are not shipped by this minimal composition.
No provider mark was redrawn or recolored here. Notion/Slack/Yahoo variants remain
held where exact asset rights are unverified. The original TOFI icon library is
unchanged.

## Primary sources and immutable revisions

- GitHub remote/PAT and endpoint docs: https://github.com/github/github-mcp-server/blob/71ef8266e48110974b13aef50b4df6ff9914ff68/README.md and https://github.com/github/github-mcp-server/blob/71ef8266e48110974b13aef50b4df6ff9914ff68/docs/remote-server.md
- GitHub SDK dependency: https://github.com/github/github-mcp-server/blob/71ef8266e48110974b13aef50b4df6ff9914ff68/go.mod
- Notion package/auth/transport: https://github.com/makenotion/notion-mcp-server/blob/730ae781ba28beeaf0865025a3f2ed4c25ea2387/package.json and https://github.com/makenotion/notion-mcp-server/blob/730ae781ba28beeaf0865025a3f2ed4c25ea2387/README.md
- Protocol declaration: https://github.com/modelcontextprotocol/typescript-sdk/blob/e12cbd7078db388152f6e839abdbe09ba01f3f32/src/types.ts
- Slack vendor contract: https://docs.slack.dev/ai/slack-mcp-server/
- Discord community sources: https://github.com/SaseQ/discord-mcp ; https://github.com/v-3/discordmcp ; https://github.com/hanweg/mcp-discord
- Yahoo community source/published registry: https://github.com/Alex2Yang97/yahoo-finance-mcp/blob/main/pyproject.toml ; https://pypi.org/project/yahoo-finance-mcp/
- Brand source and terms: https://brand.github.com/foundations/logo ; https://discord.com/branding ; https://slack.com/media-kit ; https://github.com/simple-icons/simple-icons/blob/develop/DISCLAIMER.md
