# 0047 TOFI 只精选官方连接；社区连接由使用者自己安装，且工具默认不信它自己的“只读”标注

> 编号 D47，索引见 [README](README.md)。范围由 owner 在 2026-10-22 对 Discord 连接任务中途改变：原任务是“把社区 Discord 服务做成精选连接”，改成“Discord 是用户自己安装社区连接这条路的试验案例”。

## 决定

1. **政策。** TOFI 精选的连接只收**官方**的（提供方自己维护或托管的，例如 Notion、Robinhood、Linear）。社区维护的服务不进精选目录，也不钉进 Runner 镜像或代码；使用者自己安装、自己承担信任。Discord 仍是目录里的“held”，只是它的说明改为指向自己添加的步骤。
2. **自己安装的路。** 连接页“添加连接 › 自定义 › 本地软件包”（`ui/src/connections/PackageForm.tsx`）（npm 或 PyPI，固定版本），加通用的这几点改进，不针对某个服务：
   - **包摘要（可选）。** 安装请求可带 npm 的 `sha512-…` 摘要；`npm install` 之后读 `node_modules/.package-lock.json`，顶层包的版本或摘要对不上就拒装并清掉目录。只覆盖顶层包；依赖用的是 caret 范围时，传递依赖版本仍会漂移。
   - **密钥只进私密文件。** 环境变量值一律写成 0600 私密文件，清单、状态、响应、日志里都没有；表单里值是密码框，安装成功后清空。
   - **出处。** Runner 状态带 `kind`、`package`、`version`；服务端在接入时（`attachLocalMCP`）把它写成连接的 `provenance`（`origin: community`）。出处与重做后连接页的“官方 / 社区 / 自定义”来源行是同一个模型：本地包的来源行写“社区 · 包名 版本”，“查看详情”再写“其工具在标为只读之前每次都要确认”。来源只由服务端按 Runner 状态设置，请求体、设置页和 Bot 都不能写；它进入配置指纹，所以换了包或版本，旧的“始终允许”和待批准的卡片都失效。自带的 gogcli 和运营方配置的适配器档案不算社区包。
3. **安全性质：社区包的工具默认按“写”处理。** 工具分级（`tool_tiers.go`）原来以提供方的 `readOnlyHint` 为准。MCP 规范说来自未经审核服务的注解不可信，所以对 `provenance.origin == community` 的连接，提示只展示、不生效：每个工具每次调用都走批准，直到 owner 在工具面板里把确实只读的几个标为只读（owner 的覆盖照旧生效，工具定义改了就失效）。官方（精选）连接和远程服务保持原契约。
4. **Skill 是另一条路，已补齐。** 使用者安装的带脚本的 Skill，`read_skill` 现在也把 `SKILL.md` 之外的文件复制到 Bot 的电脑（和内置 Skill 同一机制、同一个 `.skills/<name>/<digest>/` 目录，有数量和大小上限，只读普通文件）；只有 `SKILL.md` 的 Skill 什么都不传。取舍：Skill 的脚本在 Bot 的电脑上跑，需要的凭据必须交到那台电脑，Bot 的每条命令都读得到；MCP 服务在 Runner 里，token 在 Bot 的电脑够不到的私密文件里。**带凭据的东西优先用 MCP；Skill 留给没有凭据的流程和辅助脚本。**
5. **Discord 的试验案例。** 候选评估、选定的包和固定值写在 [资格记录](../integrations/discord-package-qualification.md)：`@discord-mcp/cli` 0.32.1（Apache-2.0，Node，原生协商 2026-07-28，无需适配器，Runner 镜像不用改）；SaseQ（Java、Docker）、PaSympa（SDK 停在 2025-11-25，默认请求特权 intent，破坏性工具过滤不掉）、GustyCube（停更）不过。推荐只开 10 个工具（8 读 2 写），邀请权限整数 **66560**（只读）/ **68672**（读写、反应），范围只用 `bot`，只开 Message Content 特权 intent。owner 的步骤见 [自己添加 Discord](../integrations/discord-add-it-yourself.md)。

## 日期

2026-10-22（太平洋时间）。

## 谁拍板

owner。范围变更原话没有逐字存档；协调者转述为：“TOFI 只精选官方连接（Notion、Robinhood 等）；社区连接由使用者自己添加和安装；Discord 是这条路的试验案例，不做精选连接。”

## 背景

目录里 Discord 一直是“held”（[token-catalog-review.md](../integrations/token-catalog-review.md)）：没有官方 MCP 服务，社区方案要么是 Java/Docker（Runner 装不了），要么协议版本不到 2026-07-28。2026-10-22 查到的 `@discord-mcp/cli` 用 MCP TypeScript SDK 2.x，TOFI 的最新协议客户端能直接协商，于是有了一个能走通用安装路径的候选。随后 owner 把范围改为“不精选”。

## 理由

- 精选意味着 TOFI 替使用者背书；单人维护、三个月大的包做不到。让使用者自己装，信任是明说的，不是继承的。
- 社区包的注解不可信是这条路的主要风险：注解标错的写工具会绕过批准。默认当写处理，代价只是读工具要先标一次只读，owner 的“写必须经人批准”不被削弱。
- 摘要、私密文件和出处都是通用改进，对之后任何社区包同样有效；没有为 Discord 硬编码任何东西（固定值在文档和 `docs/integrations/community/discord.json`，代码里没有）。

## 影响

实施状态：已在分支 `feat/discord-connector` 实现，未合并。

- 不变：精选目录、Runner 镜像、`Spec.Adapter`（Notion 档案）、其他 MCP 的分级。
- 远程服务（使用者手填 URL）仍按提供方注解分级；是否也对它们降级未决，**待确认**。
- 已知局限：传递依赖不被摘要覆盖；Discord 的真实行为（邀请权限被接受、开 intent 后返回正文、批准后发送）没有真实 token 可验。

## 相关代码与文档

`internal/mcprunner/install.go`（`Integrity`、`verifyInstalledIntegrity`）、`runner.go`（`Spec.Package/Version`、`Status`）；`internal/extensions/mcp.go`（`MCPProvenance`、`communityServer`）、`tool_tiers.go`（`resolveMCPToolTier`）、`management.go`（`SaveMCP` 保留来源）、`builtin.go`（`InstalledSkillFiles`）；`internal/app/local_mcp.go`（`attachLocalMCP`）、`builtin_skills.go`；`ui/src/connections/PackageForm.tsx`、`ui/src/MCPSettings.tsx`、`locales/*/extensions.json`。测试：`community_install_test.go`、`community_discord_pin_test.go`（mcprunner）、`community_provenance_test.go`（extensions）、`community_connector_test.go`（app）、`ui/scripts/test-community-install.mjs`（`npm run test:community-install`）。文档：[资格记录](../integrations/discord-package-qualification.md)、[添加步骤](../integrations/discord-add-it-yourself.md)、`docs/integrations/community/`。
