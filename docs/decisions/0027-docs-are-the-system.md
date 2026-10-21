# 0027 文档就是系统：决定、现状、流程、设计规范都写进仓库的 docs/

> 编号 D27，索引见 [README](README.md)。

## 决定

- 项目的决定记录、产品现状、工作流程和设计系统规范都放在仓库的 `docs/` 下。
- 每次合并、发版或 owner 拍板一个决定，都要在同一个提交里更新对应的文档。
- `AGENTS.md` 是入口，最上面有“从这里开始”，指向这些文档。

## 日期

上下文记为 2026-10-21；按 PDT 计实际日期待确认

## 谁拍板

owner（jackzhao）。原话由协调者整理在本次文档工作的上下文文件里，仓库内没有原始对话记录。

## 背景

owner 原话：“建立真正的可以 Follow 的文档或者 Spec，让后来的对话都有迹可查。建立真正的系统。我都不知道之前到底在干什么。”

在此之前：

- 决定只散落在对话里和提交说明中；
- `docs/` 下主要是零散的专项计划和验收文档（`docs/agent-plan/`、`docs/acceptance/` 等），没有总索引，也看不出哪些已经过时。

## 理由

后来的人或 AI 会话打开仓库，应该能直接查到：

- TOFI 是什么、现在做到哪一步；
- 每个决定为什么这样定；
- 工作按什么流程做。

## 影响

- 实施状态：实施中：本文档体系在分支 `docs/system-opus` 上编写，合进 main 的提交待确认

本次建立的文档结构如下，各文件由同一批提交创建，以合入 main 后的实际文件为准：

| 路径 | 内容 |
| --- | --- |
| `docs/README.md` | 总索引，并标出旧文档哪些还有效、哪些已过时 |
| `docs/product/status.md` | 产品现状、owner 的打分标准、评测结果 |
| `docs/product/roadmap.md` | 已完成、进行中、下一步、暂缓 |
| `docs/decisions/` | 每条决定一个文件，索引见 [README](README.md) |
| `docs/releases.md` | 每个版本改了什么 |
| `docs/process.md` | 评测、预览、发版、安全审查和选模型的流程 |
| `docs/design/` | 设计系统规范和迁移方案，见 D28 |

- 每条新决定都按 [README](README.md) 中“怎么新增一条决定”的步骤，在作出决定的那个提交里加上。
- `AGENTS.md` 现有的模块段落（motion、sidebar、questions 等）仍然有效；改实现时要同步修改这些段落。

## 相关代码与文档

- `AGENTS.md`、`docs/README.md`、`docs/decisions/README.md`
