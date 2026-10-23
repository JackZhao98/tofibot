# 0058 技能库：一个全局库，每个 Bot 一列启用

> 编号 D58，索引见 [README](README.md)。设计过程见 [collab-todo.md](../design/collab-todo.md) 的 C4 一节，实施方案见 [collab-implementation-plan.md](../design/collab-implementation-plan.md) 阶段 1。

## 决定

1. **一个全局技能库。** 不管谁装的（用户在设置里装，或聊天时让 Bot 装），都进同一个库。库里的文件仍是技能内容的唯一来源；表 `skill_library`、`skill_versions`、`skill_bundles` 只记安装者、版本和套装，启动时与文件对账（文件为准，写诊断）。
2. **每个 Bot 一列启用**（`skill_enablement(bot_id, skill_name, enabled, set_by, set_at)`）。装进库的技能没有行 = 关；官方（内置）技能没有行 = 开。没有“对所有 Bot 生效”的总开关。
3. **Bot 自己装的技能只对自己开；Bot 给自己开关库里的技能不需要批准**（无人值守的 run 里也行），并记录谁、何时设的。
4. **Q11：Bot 装新技能不需要批准**（仍只在有人看得见的 run 里；无人值守的 run 拒绝并让 Bot 在回复里说明）。**Bot 发新版本会更新到所有开了它的 Bot，所以需要人批准**，复用 `extension_gate` 的批准流程，卡片上的事实由服务端按文件算出：新版本号、增/删/改的文件、会跟随的 Bot 数。**只有装它的那个 Bot 能发它的版本**；用户装的技能，Bot 不能发。
5. **Q12：开了它的 Bot 都跟最新版本**，设置里标“已更新”，用户可以回退（也可以再前进）到任一归档版本（`<skills>/.versions/<name>/<n>/`，最多留 20 个）。
6. **Q9：迁移**（一次性，有标记 `skill_library_per_bot_v1`）：旧的“所有 Bot”技能（`skill_access` 没有行）给每个现存 Bot 打勾；有名单的只给名单上的 Bot；之后新建的 Bot 不带任何用户技能。Bot 导出只带已启用的技能名字，不带文件、版本或谁设的；导入不会据此开技能。
7. **Q10：官方技能默认对每个 Bot 开，可按 Bot 关。** 旧的全局 `builtin_skill_off` 在迁移里转成每个现存 Bot 的关闭行，全局开关（`PUT /api/extensions/skills/<name>/enabled`）删除。`ui/src/skillCatalog.ts` 的三个 `tofi-*` 起步模板不是官方技能，装上后是普通技能。
8. **Q13：套装用 Claude Code 插件目录格式**：`.claude-plugin/plugin.json` 加 `skills/<name>/SKILL.md`，只认两层（套装 → 技能）；目录里别的东西（commands、agents、hooks）不装。支持从文件夹安装。界面里一个套装一行，可展开逐个开关；Bot 工具里用套装名可一次开关整套。
9. **删除只有用户能做**（设置里）。Bot 工具没有 `skill_delete`；`restrictedSkillGuard` 删除，换成 `skillPublishGuard`（只检查安装者、技能是否存在、文件是否有变化）。套装成员不能单个删，只能删整个套装。
10. **提示词**：技能目录只列这个 Bot 已启用的技能（标题 `Enabled Skills (name and purpose):`，比原来短 2 个字符）；`read_skill`、`read_skill_file` 对未启用的技能和不存在的一样拒绝，因为工具本身就是按过滤后的列表建的。
11. **只加不删的迁移**：`CREATE TABLE IF NOT EXISTS`，旧表 `skill_access`、`builtin_skill_off` 保留，迁移之后不再读。

## 日期

2026-10-23（PDT）

## 谁拍板

owner（实施方案第 3 节 Q9–Q13 与 C4 设计 1–10）。

## 背景

C4：Bot 能装技能但只能装成全工作区，每次装/删都要批准，没有编辑，Bot 能删 owner 装的；`contracts/alpha-api.md` 写“默认不授权”而代码相反，`docs/design/settings-ia.md` 提议删掉按 Bot 授权。对标 Claude.ai 设置里的技能库加开关、Claude Code 插件（一个插件打包多个技能）和 Claude API 技能的版本号。

## 影响

- 替代 [D41](0041-builtin-deliverable-skills.md) 的“全局开关”部分（`builtin_skill_off`、设置 › Skills 里的内置开关）；官方技能本身、脚本复制、SKILL.md 写法规则不变。
- 替代 [settings-ia.md](../design/settings-ia.md) 第 2 步“技能一刀切”的提议；`contracts/alpha-api.md` 的技能路由按本决定重写。
- 安全闸（`AGENTS.md` “Security gates”）：技能不再是“装/删都要批准”，而是“发新版本要批准、装新技能和给自己开关不要”；MCP 的闸不变。

## 相关代码与文档

- `internal/app/skill_library.go`（表、迁移、对账、安装/发布/回退/删除、视图）、`skill_library_http.go`（设置与 Bot 设置的路由）、`skill_tools.go`（`manage_extensions` 的技能动作）、`extension_gate.go`（发布的批准与事实）
- `internal/extensions/skill_library.go`（替换文件、版本归档、套装解析）、`mcp.go`（`Config.SkillAccess`）
- UI：`ui/src/settings/SkillLibrary.tsx`、`ui/src/BotSkills.tsx`、`ui/src/skillLibrary.ts`；目录状态 `skills-*`、`bot-skills-*`、`approval-publish`
- 测试：`internal/app/skill_library_test.go`、`internal/extensions/skill_library_test.go`、`ui/scripts/test-bot-skills.mjs`、`test-settings-shell.mjs`（7b）
