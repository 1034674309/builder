# AI Interaction Sentry Implementation

本文是 [AI Interaction Sentry](./ai-interaction-sentry.md) 的实现索引。它说明代码在哪里、请求如何经过各层，以及提交前如何验证。

## 代码分布

### Builder 前端

| 文件 | 作用 |
| --- | --- |
| `spx-gui/src/components/project/runner/ProjectRunner.vue` | 在父页面创建 `ai.think`、`ai.archive`、`http.client` 和 command span；负责 fetch、trace headers、响应转换和取消 |
| `spx-gui/src/setup/sentry.ts` | 排除 AI URL 的自动 fetch tracing，并为 `ai.*` 使用独立采样率 |
| `spx-gui/src/utils/tracing.ts` | 提供 `isAIOperation`，供采样器识别 AI root |
| `tools/ai/ai.go` | Think/Archive 的循环、重试、取消、command 执行和终态收口 |
| `tools/ai/sentry_transport.go` | 包装底层 Transport，统一登记每次 attempt，并修正超时等通用传输错误 |
| `tools/ai/sentry_wasm.go` / `sentry_stub.go` | WASM 桥和非 WASM 测试用空实现 |
| `tools/ai/sentry_context.go` | 把 Think/Archive ID 和 `attempt_id` 放入 context，隔离并发请求 |
| `tools/ai/wasmtrans/wasmtrans.go` | 序列化请求、调用 `FetchAI`、只读一次响应 body，并解析错误/成功 JSON |
| `tools/ai/classify.go` / `exception.go` / `httperr.go` | 终态、异常和 HTTP 错误的分类规则 |
| `tools/ai/command.go` | command span，以及参数错误、handler panic 和普通 handler error 的分流 |
| `tools/ispx/ai.go` | 把 game session ID 和 Sentry bridge 注入 WASM |

### Builder backend

| 文件 | 作用 |
| --- | --- |
| `internal/aiinteraction/aiinteraction.go` | 流式调用模型，计算首个有效响应耗时，并把模型 metadata 写到 HTTP transaction |
| `internal/aiinteraction/failure.go` | 校验 completion/Archive 结果并生成统一的 `category` 和 `reason` |
| `cmd/xbuilder-backend/middleware.go` | 创建 `http.server` transaction，并按路由捕获原始 request/response body、限制 Sentry 副本大小和记录截断信息 |

## 一次 Think 请求怎么走

```text
Player.Think
  -> tools/ai/ai.go
  -> startThink(context)
  -> sentryTransport.Interact
  -> register attempt in context
  -> wasmtrans.Interact
  -> wasmtrans.fetchAndParse
  -> ai.FetchAI
  -> ProjectRunner.fetchAI
  -> create http.client + window.fetch
  -> backend http.server
  -> backend existing http.client (model)
  -> response body returned to WASM
  -> ai.go handles retry/command/finish
  -> endThink
```

Archive 走同一条请求路径，但从 `startArchive` 开始，使用自己的 root 和 context。

### 请求关联

`startThink` / `startArchive` 创建的 interaction ID 和每次请求的 `attempt_id` 只存在于 Go context 与父页面 Map 中：

```text
think_id / archive_id -> root span
attempt_id             -> 当前 fetch 和 client span
```

它们不作为 Sentry 字段发送。真正发给后端的 `Sentry-Trace` 和 `Baggage` 由前端 `http.client` 生成。

## 重要实现约定

- Think/Archive root 必须同步登记，因为 WASM 需要立即拿到 context 关联信息。
- 默认 WASM Transport 在 `SetDefaultTransport` 前由 `NewSentryTransport` 包装；wrapper 不保存单次请求状态，attempt ID 只放在各自的 context 中。
- 其它桥操作使用 `setTimeout(0)`，避免同步调用 Sentry 影响 WASM 栈展开。
- `fetchAI` 在下一拍创建 client，并在完整读取 response body 后结束 client。
- AI URL 已从浏览器自动 fetch tracing 中排除，否则同一个请求会生成重复 client。
- body 只在前端读取一次；WASM 同时兼容 bridge 返回的普通对象和无 bridge 时的原生 `Response`。
- Sentry 操作失败时回退到普通 fetch，不影响 AI 功能。
- 后端不创建 `ai.upstream`，也不修改共用 `http.client` 的完成时机。
- 后端失败只标记 `http.server` transaction 并记录根因标签；这些标签不通过公共错误响应传给前端。前端使用自己的终态分类，在所有重试结束后至多上报一次最终 exception，并通过同一条 trace 关联后端根因。

## 验证

Builder AI Go/WASM：

```sh
cd tools/ai
go test ./...
GOOS=js GOARCH=wasm go build ./...
```

Builder backend：

```sh
go test ./internal/aiinteraction ./cmd/xbuilder-backend
```

前端：

```sh
cd spx-gui
pnpm build
```

改动 `tools/ai` 或 `tools/ispx` 后，需要重新编译 WASM 并更新 `spx-gui` 使用的 `ispx.wasm`。

真机验收时建议检查：

- 两个角色同时 Think 时，每个 Think 都有自己的 root 和 client。
- 成功 Think 的 `outcome` 为 `success`。
- 后端 transaction 上有原始 `request_body` / `response_body`。
- 后端失败 transaction 上有对应的 `category` / `reason`，有模型响应时还应有 `request_id`。
- 取消、额度耗尽、429 重试用尽不会产生新的 exception。

## 不在本次实现中的内容

- 生产环境的 AI 采样率调整。
- 精灵名和 `backend_*` 前端 tag。
- Copilot 的 tracing 或公共 HTTP client 改造。
- 完整 OpenAI messages、原始 SSE 内容和模型 prompt 的额外采集。
- 独立的 `ai.transport.attempt`、`ai.upstream`、`ai.archive.upstream` span。
