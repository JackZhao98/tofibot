# 0039 `sandbox:` 链接在服务端输出时改成附件地址

> 编号 D39，索引见 [README](README.md)。

## 决定

Codex 系模型会写 `[下载](sandbox:/api/attachments/<id>)` 这种 ChatGPT 沙箱写法。服务端在输出助手消息时（`Message.MarshalJSON`，列表响应和流事件都经过它）把两种写法改成普通的 `/api/attachments/<id>`：

- `sandbox:/api/attachments/<id>`：去掉 `sandbox:`。
- `sandbox:` 加绝对路径：文件名与该消息自带的附件中唯一一个同名时，改成该附件的地址。找不到或有重名则不动。

其他协议一律不碰。数据库里存的原文不改，模型原话可审计；只改输出。用户自己的消息不改。

## 日期

2026-10-21（PDT）

## 谁拍板

owner（jackzhao），经协调者转达：生产评测发现的三个缺陷之一，要求在服务端一处处理，所有客户端受益，并说明存储原文的取舍。原话不在仓库里，待确认。

## 背景

界面的 Markdown 渲染（`ui/src/MessageMarkdown.tsx`）用 react-markdown 的 `defaultUrlTransform`，只放行 http、https、mailto 等；`sandbox:` 不在其中，链接被清成空地址，点了没有任何效果，是死链。

## 理由

存储点有二十多处 `INSERT INTO messages`，没有单一入口；所有客户端拿到消息都经过 JSON 序列化，所以放在 `Message.MarshalJSON` 是唯一的一处。输出时改写而不是落库时改写，原文原样留在库里，也不影响喂回模型的上下文。路径写法只对本条消息自带的附件解析，因为最终消息才绑定附件（失败时见 [D38](0038-failed-runs-keep-published-files.md)）；更早的消息里只有 `/api/attachments/` 写法能被改。

## 影响

- 实施状态：已在分支 `fix/run-failure-deliverables` 实现并测试，未发版。
- 流式草稿（还没成为消息）里的文字仍是原样，落成消息后才被改。
- 导出等不经 `Message` 序列化的路径拿到的是库里原文。
- 同一提交把回复标记 `[message_id=<uuid>]` 的解析抽成 `ui/src/replyMarker.ts`，并容忍标记前的空白；独占一行（后接换行）的写法原本就能解析，现在有测试守着。

## 相关代码与文档

- `internal/app/sandbox_links.go`；测试 `internal/app/sandbox_links_test.go`
- `ui/src/replyMarker.ts`、`ui/scripts/test-reply-quote.mjs`
