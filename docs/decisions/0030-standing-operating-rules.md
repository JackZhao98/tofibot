# 0030 长期有效的操作规则

> 编号 D30，索引见 [README](README.md)。

## 决定

| # | 规则 | 怎么检查 |
| --- | --- | --- |
| 1 | 打正式版 `v*` 标签（不含 `-rc.N`）之前必须先问 owner 并得到确认。部署授权不包括打正式版标签 | 推 `vX.Y.Z` 标签会触发 `.github/workflows/release.yml`，生成正式 Release（`prerelease=false`，见第 45–48 行）。问 owner 时要列出哪些已验证、哪些未验证 |
| 2 | 每次部署前，查所有账户有没有正在跑的 run，有就不切镜像 | `tofi update` 自带运行检查（`deploy/self-host/tofi_host.py:134` `ACTIVE_RUN_STATES`、`:1376` `wait_for_idle`）；步骤见 [`docs/process.md`](../process.md) 第 7.4 节 |
| 3 | 生产环境的操作只由协调会话做。子代理和并行写作者不碰生产 | 子任务说明里明确写“不 push、不 ssh、不打 tag” |
| 4 | 不读取、不打印任何密钥 | `AGENTS.md` 的 “Never read or export credentials for a source-tree review” |
| 5 | 说测试全绿，必须以命令的退出码为准，不能数输出里有几行 ok | 汇报时附上命令和 `$?` |
| 6 | 每个修复都配一个测试，把修复回退后这个测试会失败 | 评审时把修复回退，测试应变红 |
| 7 | 放宽安全限制的改动，合并前要做一次对抗性审查 | 例：D24 的 `6f45df4` 经过审查，问题在 `5592eb3` 中修完，然后才合并为 `748c774` |

## 日期

长期有效。owner 在多个时间点分别定下，第一次提出的日期各自待确认

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里，仓库内没有原始对话记录。

## 背景

这些规则来自具体的事故和教训：

| 规则 | 来源 |
| --- | --- |
| 部署前查正在跑的 run | 2026-10-06 部署时打断过 owner 正在进行的提问 |
| 以退出码为准 | 曾按输出里的 ok 行数报告“全绿”，结果报错了 |

上面两条来源记录在 owner 的本机记忆里，不在仓库中：

- `~/.claude/projects/-Users-jackzhao-Developer-sentiosurge/memory/tofi-deploy-check-active-runs.md`
- `~/.claude/projects/-Users-jackzhao-Developer-sentiosurge/memory/prove-green-by-exit-code.md`

其余几条的来源：待确认。

## 理由

- 正式版和部署都是对外动作，影响真实用户。
- 一个测试如果在修复回退后仍然通过，就证明不了修复有效。
- 放宽安全限制的改动最容易藏住绕过路径，需要专门找漏洞的人来看。

## 影响

- 实施状态：生效中
- 这些规则在 `docs/process.md` 里展开写成具体步骤。
- 这份决定和 `docs/process.md` 中任何一份修改了规则，另一份要在同一个提交里同步改。
- 子代理接到的任务说明都要带上第 3、4 条。

## 相关代码与文档

- `.github/workflows/release.yml`、`AGENTS.md`、`docs/process.md`
- 相关决定：[D24](0024-standing-tool-allowance.md)、[D25](0025-github-release-cleanup.md)
