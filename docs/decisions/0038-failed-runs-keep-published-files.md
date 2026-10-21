# 0038 瞬时错误先重试，失败的运行不丢已发布的文件

> 编号 D38，索引见 [README](README.md)。

## 决定

1. 提供方报告的瞬时错误（过载、限流、5xx、流中断）在运行失败之前按有界退避重试：带抖动，总时间上限约 2 分钟，服从 `Retry-After`。判定看错误码和状态码，不看消息文字。
2. 运行在 `publish_file` 之后失败时，已发布的文件不再被清理：失败时它们绑定到一条只带附件的助手消息上，用户看到的失败里带着文件，链接继续能打开。取消和重启中断的运行仍然清理（用户主动停止，或运行会被恢复）。

## 日期

2026-10-21（PDT）

## 谁拍板

owner（jackzhao），经协调者转达：生产评测发现的三个产品缺陷之一，要求两半都修。原话不在仓库里，待确认。

## 背景

评测里一个 Codex 模型（`codex-gpt-5.6-terra`）在 Bot 电脑上生成了 XLSX 并调用 `publish_file`，返回了 `/api/attachments/<id>`。下一次模型调用收到流内事件 `response.failed`，错误码 `server_is_overloaded`（HTTP 状态是 200）。运行以 `failed` 结束，用户看到一条带该链接的文字，打开却是 404。

两个原因：

- 重试层其实存在（`RetryProvider` 和 `agent` 循环里的流中重试），但 `IsRetryable` 对流内失败只认 `server_error` 和 `rate_limit_exceeded` 两个码，`server_is_overloaded` 不在其中，所以第一次就失败。重试层也不读 `Retry-After`，默认 5 次、最长约 31 秒，达不到 2 分钟。
- `publish_file` 只把文件登记在 `run_attachments`，要等最终消息落库时才绑定到消息。运行以 `failed` 结束时 `SetRunStatus` 会调用 `cleanupRunAttachments`，把未绑定的附件连同行一起删掉。模型已经把链接写进了更早发布的文字里，于是链接成了死链。

## 理由

流内错误没有 HTTP 状态，错误码是唯一的结构化信息，所以按码归类（`retryableResponseFailure`），额度耗尽（`usage_limit_reached` 等）和无效输入仍然不重试。限制总时间而不只是次数，是为了让一次提供方抖动不会把运行卡很久。文件是用户要的成果，运行失败不该让它消失；绑定到一条空文字的消息是不引入新文案、不用新界面的最小办法。

## 影响

- 实施状态：已在分支 `fix/run-failure-deliverables` 实现并测试，未发版。
- `provider_retries` 设置的默认值由 5 改为 8（范围仍是 0–10）；`RetryConfig` 新增 `TotalBudget`（默认 2 分钟），服务器要求的等待超过 60 秒时不再重试。
- 流内顶层 `error` 事件现在和 `response.failed` 一样变成带码的 `ResponseFailedError`。
- `agent` 的流中重试退避由 3 档（2/5/10 秒）加到 6 档（到 45 秒），同样服从 `Retry-After`。
- 失败消息没有文字，只带附件；运行的失败状态仍由界面的失败提示表达。
- 没有改的：被取消或因重启被中断的运行仍清理它们暂存的文件。

## 相关代码与文档

- `internal/provider/errors.go`（`retryableResponseFailure`、`NewAPIErrorFor`、`parseRetryAfter`）、`internal/provider/retry.go`（`retryDelay`、`TotalBudget`）、`internal/provider/openai.go`（`error` 事件）、`internal/agent/agent.go`（`transientProviderBackoff`）
- `internal/app/artifact_publish.go`（`keepFailedRunFilesTx`）、`internal/app/app.go`（`SetRunStatus`）
- 测试：`internal/provider/retry_overload_test.go`、`internal/runtime/overloaded_stream_test.go`、`internal/agent/transient_provider_test.go`、`internal/app/artifact_publish_test.go`（`TestFailedRunKeepsPublishedFileReachable`）
