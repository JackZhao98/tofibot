# 0036 聊天里上传的文件放进 Bot 的电脑

> 编号 D36，索引见 [README](README.md)。

## 决定

用户在聊天里上传的文件，是 Bot 电脑工作区里的一份文件（和 ChatGPT 的 `/mnt/data`、Claude 容器里的上传一样的做法）。

| 项 | 内容 |
| --- | --- |
| 位置 | `/workspace/home/uploads/<对话 id>/<原文件名>`。在共享的 guest 主目录（`HOME=/workspace/home`）下，不在任何 Bot 的个人目录里 |
| 复制哪些 | 本对话里**用户发出的消息**带的附件：触发这次 run 的那条消息的先复制，再从新到旧复制更早的，最多 40 个、约 80 MB（触发消息的附件不受字节上限限制）。Bot 自己发布的文件不回传（它们本来就在电脑里） |
| 可见性 | 与 `read_attachment` 一致：只复制本对话（私聊、群、隐藏的 Bot 对 Bot 对话）消息里的附件。群里的附件到群成员的 run 里，别的对话的不会出现 |
| 重名 | 同一对话里按（上传时间，id）排序，最早的保留原名，之后同名（不分大小写）的在扩展名前加 `-<id 前 8 位>`。路径只由附件和它前面的附件决定，所以同一个附件永远是同一个路径。文件名里的 `/`、`\`、控制字符换成 `_`，`.`、`..`、空名换成 `attachment`，以 `.part` 结尾的名字补 `_`，超长截到 200 字节 |
| 不做 | 不解压压缩包；不超过现有 20 MB 附件上限（与 blob 上限一致） |
| 传输 | 复用 `publish_file` 的 guest 文件通道，反方向：新增 guest 动作 `files.import_chunk`（`export_chunk` 的逆），每块 512 KB，先写 `<路径>.part`，最后一块 fsync 后改名，读者不会看到半个文件 |
| 幂等 | guest 在第一块发现目标路径已有普通文件就回 `exists` 并停止：同一附件不会再传，Bot 改过的副本也不会被盖掉 |
| 何时复制 | 懒复制：每个 run 的 computer 工具集在第一次调用任何 `computer_*` 工具前同步一次（失败重试一次，仍失败只记日志，不让工具调用失败）。不碰电脑的 run 不付任何代价；建上下文只算路径、不传数据 |
| 告诉模型 | 提示词里列附件的那一行后面加 ` On the computer at <路径>.`；电脑段落加一句原则：附件在电脑里列出的路径上。不写例句 |

## 日期

2026-10-21（PDT）

## 谁拍板

协调者按评测发现派工并写明要求；owner 的原话不在仓库里，待确认。

## 背景

评测发现：用户在聊天里发 zip、PDF、DOCX、XLSX，Bot 只拿到附件 id。`read_attachment` 只读文本、上限 128 KB；图片只通过视觉输入、最多 3 张；`computer_files` 只写文本；`publish_file` 只能从电脑发给用户。所以“这是我们的发票（zip）”这类请求无法完成。

## 理由

附件变成电脑里的文件后，Bot 可以用 shell、办公工具和脚本直接处理任意二进制，不需要为每种格式各做一个读取工具。换位置的代价（按需复制、一个固定目录）比逐格式提取小。

## 影响

- 实施状态：已实现（分支 `feat/computer-files-and-office`）。
- guest 二进制要重新构建并随镜像发布；旧 guest 不认识 `files.import_chunk` 时，复制失败只记日志，Bot 照旧只有附件 id。
- 路径里的对话 id 让不同对话的同名文件互不覆盖；共享主目录意味着同一台电脑上的其他 Bot 也读得到这些文件（电脑本来就共享，与现有隔离边界一致）。
- 提示词预算：电脑段落多约 183 个字符（与办公工具那一句合计），`prompt_budget_test.go` 的三个上限按实测各加 183。

## 相关代码与文档

- `internal/app/guest_inputs.go`（路径映射、同步、`guestInputSync`）、`internal/app/microvm.go`（`microVMTools` 里的钩子、电脑段落提示）、`internal/app/attachment_context.go`（附件行加路径）
- `internal/computer/guest/import.go`（`files.import_chunk`）
- 测试：`internal/app/guest_inputs_test.go`、`internal/computer/guest/import_test.go`
- 对照：`internal/app/artifact_publish.go`（`publish_file`，`files.export_chunk`）
