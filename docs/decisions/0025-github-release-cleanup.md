# 0025 GitHub 发布清理：保留 4 个正式版和最近的几个 rc

> 编号 D25，索引见 [README](README.md)。

## 决定

- 保留 4 个正式版（v0.1.0–v0.1.3）和最近的几个 rc。
- 删除旧 rc 的 GitHub Release、git tag，以及 ghcr 上对应的容器镜像版本。

## 日期

上下文记为 2026-10-21；按 PDT 计实际日期待确认

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里，仓库内没有原始对话记录。

## 背景

每发一次 rc，`.github/workflows/release.yml` 都会生成一个 Release 和一组镜像（`tofi`、`tofi-worker`、`tofi-mcp-runner`，`release.yml` 第 55–143 行）。它们越积越多，真正可用的版本反而不好找。

## 理由

只留下有意义的版本，方便安装和回滚时选版本。owner 原话里的其他理由：待确认。

## 影响

- 实施状态：已执行。这是 GitHub 上的操作，仓库里没有对应提交

2026-10-20 用 `gh` 只读核对的结果：

| 对象 | 现存 |
| --- | --- |
| GitHub Release（`gh release list`） | v0.1.0、v0.1.1、v0.1.2、v0.1.3（Latest，2026-10-15T17:12Z），v0.1.4-rc.26–rc.29（Pre-release） |
| 远端 tag（`gh api repos/{owner}/{repo}/tags`） | 上面 8 个，另有 `live-20261005-b35ead1`、`live-20261006-93db5f8` |
| ghcr 镜像 `tofi`、`tofi-worker`、`tofi-mcp-runner` 带标签的版本（`gh api /users/JackZhao98/packages/container/<名字>/versions`） | 三个镜像都只剩上面 8 个标签 |

- 两个 `live-*` tag 也保留着，它们是否属于这次清理的保留范围：待确认。
- 以后的发版仍然由 `.github/workflows/release.yml` 处理：rc 标签发布为 prerelease，并带 `--latest=false`（见该文件第 45–48 行、第 269 行）。
- 清理的范围、怎么做：待确认。以后每次清理前要先问 owner（D30 规定对外动作要先确认）。

## 相关代码与文档

- `.github/workflows/release.yml`、`deploy/self-host/README.md` 中的 “Publishing a release”
- 版本记录：`docs/releases.md`
- 相关决定：[D30](0030-standing-operating-rules.md)
