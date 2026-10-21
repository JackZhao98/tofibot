# 0026 依赖修复：vite 升到 8.3.3（Dependabot #15–17）

> 编号 D26，索引见 [README](README.md)。

## 决定

把 `ui/package.json` 里的 `vite` 从 `^8.3.0` 升到 `^8.3.3`，同时更新 `ui/package-lock.json`，修掉 Dependabot 的三条告警。

## 日期

上下文记为 2026-10-21；提交时间是 2026-10-20 17:42 PDT（即 10-21 UTC）

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里，仓库内没有原始对话记录。

## 背景

三条告警都出在 vite 依赖的 `launch-editor` 上（来源：`gh api repos/{owner}/{repo}/dependabot/alerts`）：

| 告警 | 内容 |
| --- | --- |
| #15 | `escapeCmdArgs()` 在 Windows 上没有转义 `%` |
| #16 | `fs.existsSync()` 的存在性检查在 Windows 上可以被绕过 |
| #17 | 编辑器的命令行参数被误解析，部分编辑器下可以执行任意命令 |

## 理由

- 这三条都只影响开发服务器，`launch-editor` 只在 `vite` 开发模式下用到，不进入生产构建。
- 补丁版本升级风险低，所以直接合入。

## 影响

- 实施状态：已实施：`a067264`。三条告警的 `fixed_at` 都是 2026-10-21T00:42:42Z
- 首个仍存在的含此改动的标签：v0.1.4-rc.29（更早的 rc 标签已按 [D25](0025-github-release-cleanup.md) 清理）
- 只改了 `ui/package.json:91` 和 `ui/package-lock.json`，生产代码没有变。
- 提交说明只写了 #15，#16 和 #17 由同一次升级一起关闭（三条的 `fixed_at` 相同）。

## 相关代码与文档

- `ui/package.json`、`ui/package-lock.json`
