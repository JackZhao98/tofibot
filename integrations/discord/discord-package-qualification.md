# Discord community MCP server: qualification

Discord publishes no MCP server (checked 2026-10-22). This record qualifies the community servers so a person who installs one themselves starts from a known-good pin. It is the reference for the owner's [add-it-yourself guide](discord-add-it-yourself.md); policy is [decision 0047](../decisions/0047-curated-official-community-user-installed.md): TOFI curates official connectors only, and a community server is something a user adds, so **nothing here is bundled in the Runner image, pinned in code or listed in the curated catalog**. Discord stays "held" there.

Method: registry and GitHub metadata read on 2026-10-22; the chosen package was installed in a scratch directory with `--ignore-scripts`, its registry signatures and attestations verified with `npm audit signatures`, its tool list read through TOFI's own Runner client, and a fake Discord REST API (undici `MockAgent`, no sockets) answered its calls. The other candidates were read, not installed. No Discord bot token was used or obtained.

## Result

**Pass, as a user-installed community server, with the restrictions below: `@discord-mcp/cli` 0.32.1.** SaseQ, PaSympa and GustyCube do not pass (reasons per candidate). Nothing was added to the curated catalog, and no one at TOFI vets this code: it is a single-maintainer, pre-1.0 project that is three months old.

| Pin | Value |
| --- | --- |
| Package | `@discord-mcp/cli` 0.32.1 (npm, executable `discord-mcp`, argument `serve`) |
| Integrity (npm SRI, the tarball) | `sha512-rIdvu+/Eia+YVtEy4OBAzrVpshFn9Q2BTVlIqDHdP5ptgR9U0AsM3sVfuxPnoz/kgNnlA+p0zp/6F/5Y8+c5mQ==` |
| Source | `https://github.com/cappyeo/discord-mcp`, tag `v0.32.1`, commit `964f37457aa7a9d90aa33bd50650b2db43737f48` |
| Licence | Apache-2.0 |

The same values are machine-readable in [`community/discord.json`](community/discord.json), the full tool list of the pinned version in [`community/discord-mcp-0.32.1.tools.tsv`](community/discord-mcp-0.32.1.tools.tsv). `internal/mcprunner/community_discord_pin_test.go` keeps the file, this record, the guide and the snapshot consistent.

## Candidates

| | `@discord-mcp/cli` (cappyeo/discord-mcp) | SaseQ/discord-mcp | PaSympa/discord-mcp | GustyCube/discord-mcp |
| --- | --- | --- | --- | --- |
| Stack | Node/TypeScript, MCP TypeScript SDK 2.3.1 | Java, Spring Boot 4.0.2 + JDA, Spring AI `2.0.0-M4` (a milestone) | Node/TypeScript, discord.js, MCP SDK `^1.29` | Node/TypeScript, MCP SDK `^1.17` |
| Activity | repo created 2026-04-28; 48 npm versions 2026-08-01 to 2026-10-19; last push 2026-10-19; 163 stars; one maintainer | created 2025-03-14; one release (v1.0.0, 2026-03-16); last push 2026-10-18; 556 stars | created 2026-03-16; v2.2.0 2026-09-03; last push 2026-10-11; 29 stars | created 2025-07-27; last commit 2026-01-12; 11 stars; package.json says 0.1.0, tag says v1.0.0 |
| Licence | Apache-2.0 | MIT | MIT | MIT |
| Transport | stdio (default) and bearer-protected Streamable HTTP | HTTP singleton on :8085 (recommended) or a container per client over stdio | stdio (npx or Docker) | stdio, run from source |
| MCP protocol | Negotiated **2026-07-28** with TOFI's latest-only Runner client, no adapter | Not verified; milestone Spring AI stack | `^1.29` SDK tops out at 2025-11-25, so it would need a private adapter like the Notion one | Older SDK; would need an adapter |
| Auth | `DISCORD_TOKEN` env (bare or `Bot ...`) | `DISCORD_TOKEN` env | `DISCORD_TOKEN` env | env |
| Tools | 222; 105 read-only, 117 write, 31 destructive (provider annotations) | 65 | 95+ | 120+ |
| Narrowing | `MCP_CATEGORIES`, `ALLOWED_GUILDS`, `MCP_WRITE_MODE=preview`, dry-run plus `__confirm` for destructive tools | none documented | toolsets only; a toolset ships its destructive tools (messages: bulk delete; members: kick and ban) | none documented |
| Intents | REST only by default; no Gateway, no privileged intents requested | Gateway (JDA) | Gateway on, requests Server Members and Message Content by default | Gateway |
| Install weight in the Runner | 119 npm packages, 99 MB; Node 22.12+ is already in the image; **no image change** | JVM and Docker; the Runner image has neither and installs npm or PyPI only | npm, fine | source build only |
| Verdict | pass | no (Java does not run in the Runner; unproven protocol) | no (needs an adapter; Gateway and privileged intents by default; destructive tools unfiltered) | no (stale; no installable package verified) |

## `@discord-mcp/cli` 0.32.1 in detail

**Protocol.** Its dependency `@modelcontextprotocol/server` 2.3.1 lists 2026-07-28 among its versions. Started by TOFI's Runner (`mcprunner.start`, which refuses any older handshake), it listed 222 tools and answered calls with the Runner's latest-only client. This is stronger than a metadata claim: the Runner's own negotiation succeeded.

**Supply chain.** `npm audit signatures` over the full install: 119 packages with verified registry signatures, 58 with verified attestations. The SLSA provenance of `@discord-mcp/cli` 0.32.1 names the workflow `.github/workflows/release.yml` of `cappyeo/discord-mcp` at commit `964f37457aa7a9d90aa33bd50650b2db43737f48` (the tag's commit) and a subject digest equal to the integrity above. `npm audit`: 0 known vulnerabilities. Dependencies use caret ranges and the package ships no lockfile, so **transitive versions float at install time**; the integrity pin covers the top-level tarball only. The Runner installs with `--ignore-scripts`.

**Network and secrets.** The code in the published `dist` contacts `discord.com/api/v10` (all tools), `registry.npmjs.org` (only the `update` sub-command, never sent the token), and `emoji.gg` (only the tool `inspiration_emoji_gg_search`, which is not in the recommended surface because it sends the query to a third party). OpenTelemetry export is off unless `OTEL_ENABLED` is set. It does not use `child_process`. With the token set to a synthetic value and `LOG_LEVEL=trace`, a 401 from the fake API left the token out of stdout and stderr; the tool result carried only a structured code (`DISCORD_AUTH_INVALID`) with no upstream text. The Runner additionally discards a plugin's stderr and never puts a secret in a response, status, manifest or log (tests: `community_install_test.go`).

**Tools and tiers.** Provider annotations are mostly right (105 read-only), but TOFI does not rely on them for community code: see the decision. Notable: `mcp_pipeline` and `workflow_*` chain other tools behind one call; `webhooks_get_with_token` and `inspiration_emoji_gg_search` are marked read-only although they handle a token or go to a third party. None of them is in the recommended surface.

**Recommended surface (10 tools).** Read: `users_list_current_user_guilds`, `channels_list`, `channels_get`, `channels_list_active_threads_guild`, `messages_read`, `messages_get`, `messages_search_recent`, `messages_context`. Write: `messages_send`, `reactions_create`. Set them as the connection's tool allowlist; set `MCP_CATEGORIES=messages,channels,users,reactions` and `MCP_WRITE_MODE=allow` as a second fence (the person's approval, not the package's preview mode, is the write gate). `messages_edit` is left out: the package's own access catalogue declares it as needing Manage Messages, a moderation permission. Excluded entirely: bans, kicks, timeouts, deletes, bulk deletes, role and permission changes, channel and guild management, webhooks, invites, DMs, automod, the blueprint and workflow tools.

**Permissions and intents.** Invite scope `bot` only (no `applications.commands`). Least-privilege permissions, from the package's access catalogue (`access/requirements.ts`) and the bit values of Discord's permission flags:

| Use | Permission bits | Integer |
| --- | --- | --- |
| Read only | View Channels (1<<10) + Read Message History (1<<16) | **66560** |
| Read and send, react | the two above + Send Messages (1<<11) + Add Reactions (1<<6) | **68672** |

The privileged **Message Content** intent must be switched on in the Developer Portal (Bot, Privileged Gateway Intents). Discord's documentation says message content, embeds, attachments, components and poll fields are empty without it across the Gateway and the REST API, except in DMs, messages that mention the app, and the app's own messages ([Gateway docs](https://docs.discord.com/developers/events/gateway)). Server Members and Presence are not needed and the Gateway stays off. The bot token is the only credential; a personal user token must never be used.

## What is proven and what is not

Proven here: the pin, signatures, attestation and digest; protocol negotiation with the Runner; the 222-tool list and annotations; read calls and their output against a fake REST API (guild list, channel list, message read), including that the fake saw the bot token only as the `Authorization` header; a write held by `MCP_WRITE_MODE=preview` made no request; no token in stdout, stderr or results on an authentication failure; the generic install path (fake `npm`) with secret handling, digest pin and provenance; the community tier rule.

**Not proven without a real token:** that Discord accepts the invite permissions and returns message content once the intent is on; real rate-limit behaviour; that `messages_send` and `reactions_create` execute against Discord after a TOFI approval (only the approval path and the fake were exercised); behaviour on a server with many channels. The first real use is the test.

## Skills as the other community path

A person can also install a community Skill (`manage_extensions` with an approved payload, or Settings). Reading it with `read_skill` now copies any files beyond `SKILL.md` (scripts) to the Bot's computer under `/workspace/home/uploads/.skills/<name>/<digest>/`, as built-in skills already did; the Bot then runs them with its shell. The trade-off: a Skill's script runs on the Bot's computer, so any credential it needs must reach that computer (an env credential or a one-password-field secret question), where every command the Bot runs can read it. An MCP server runs in the Runner and keeps the token in a private file the Bot's computer cannot reach. **Prefer an MCP server for anything with credentials**; use a Skill for credential-free procedures and helper scripts.

## Sources

- Registry metadata: `https://registry.npmjs.org/@discord-mcp%2fcli`; tarball `https://registry.npmjs.org/@discord-mcp/cli/-/cli-0.32.1.tgz`; attestations `https://registry.npmjs.org/-/npm/v1/attestations/@discord-mcp%2fcli@0.32.1`
- Source at the pinned commit: `https://github.com/cappyeo/discord-mcp/tree/964f37457aa7a9d90aa33bd50650b2db43737f48` (configuration reference under `site/src/content/docs/reference/config/`)
- Candidates: `https://github.com/SaseQ/discord-mcp`, `https://github.com/PaSympa/discord-mcp`, `https://github.com/GustyCube/discord-mcp`
- Discord: `https://docs.discord.com/developers/events/gateway` (Message Content intent)
