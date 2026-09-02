# AI Interaction Sentry Implementation

本文是 [AI Interaction Sentry](./ai-interaction-sentry.md) 的实现索引，说明通用 Trace、JSON-RPC、Web adapter 和 WASM HTTP 之间的边界。

## 代码分布

### 通用 AI 层

| 文件 | 作用 |
| --- | --- |
| `tools/ai/trace.go` | 定义 `Tracer`、`Span`、`SpanInfo`、`SpanEnd`、`ErrorInfo` 和 Noop 实现 |
| `tools/ai/trace_operation.go` | 保存当前 game session ID；创建并结束 Think、Archive 和 Command span；最终失败只记录 `category/reason` |
| `tools/ai/trace_transport.go` | 每次真实 Transport 调用创建一个 `http.client`；失败只设置 span 状态 |
| `tools/ai/transport_context.go` | 在 context 中复制保存不透明 extra headers |
| `tools/ai/ai.go` / `classify.go` | Think/Archive 重试、取消、Tracer 快照和终态分类 |
| `tools/ai/command.go` | Command 执行及参数错误、handler panic、普通 handler error 的分流 |
| `tools/ai/wasmtrans/wasmtrans.go` | 合并 extra headers、执行原生 `window.fetch`、处理取消并解析响应 |

`tools/ai` 中的通用 Trace 代码不依赖 `js.Value`、JSON-RPC、Sentry header 名称或 Web bridge；浏览器 API 只存在于 `wasmtrans` 子包。

### iSPX RPC 层

| 文件 | 作用 |
| --- | --- |
| `tools/ispx/internal/rpc/client.go` | 通用 `Call` / `Notify` / `HandleMessage` / `Close`、pending 匹配和取消 |
| `tools/ispx/rpc_wasm.go` | 暴露通用 message replier 和 response 入口，按 session 安装或关闭 Tracer |
| `tools/ispx/trace.go` | 用 `trace/span.start`、`trace/span.error`、`trace/span.end` 实现 `ai.Tracer` |
| `tools/ispx/ai.go` | 安装 `NewTraceTransport(wasmtrans.New(...))`，并注入 endpoint、token provider 和 game session ID |

### Web 层

| 文件 | 作用 |
| --- | --- |
| `spx-gui/src/ispx/rpc.ts` | 通用 JSON-RPC session、取消、关闭和迟到消息隔离 |
| `spx-gui/src/ispx/sentry-trace-adapter.ts` | 消费通用 trace RPC，创建 Sentry span、返回 propagation headers、记录固定 exception |
| `spx-gui/src/components/project/runner/ProjectRunner.vue` | 创建、注入和关闭当前运行的 RPC session |
| `spx-gui/src/setup/sentry.ts` | 排除 AI URL 的自动 fetch tracing，并使用 `ispxSampleRate` 采样 AI root |

### Builder backend

配套的 [builder-backend#351](https://github.com/goplus/builder-backend/pull/351) 不增加新的业务 span，而是扩展现有 `http.server` transaction：

| 文件 | 作用 |
| --- | --- |
| `cmd/xbuilder-backend/middleware.go` | 续接浏览器 trace；仅为两个 AI API 捕获最多 15 KB 的 request/response body；写入请求期间积累的 metadata |
| `internal/tracer/metadata.go` | 提供 context 内通用、并发安全的 tag/extra 记录器，并在读取时返回快照 |
| `internal/aiinteraction/aiinteraction.go` | 记录模型 request ID、首个有效响应耗时，以及最终失败的 `category/reason` |
| `internal/aiinteraction/failure.go` | 分类 AI interaction 内部错误；诊断字段不进入公共 `code/msg` 响应 |

后端保留已有模型 `http.client` span，不创建 `ai.upstream`、attempt span 或额外 exception。

## 一次 Think 请求

```text
Player.Think
  -> start ai.think through ai.Tracer
  -> TraceTransport.Interact
  -> RPCTracer.StartSpan(http.client)
  -> trace/span.start over generic JSON-RPC
  -> Web Sentry adapter creates child span
  <- { spanId, headers: { Sentry-Trace, Baggage } }
  -> ai.WithExtraHeaders(request context)
  -> wasmtrans.Interact
  -> window.fetch with merged headers
  -> backend http.server and existing model http.client
  -> TraceTransport ends client span
  -> ai.go handles retry / command / finish
  -> root records at most one final error and ends
```

Archive 使用相同链路，但以独立的 `ai.archive` root 开始。

## 关键约定

- `TraceTransport` 不创建 attempt span；一次底层 `Interact` / `Archive` 调用对应一个 `http.client`。
- `rateGate.Wait` 发生在 Transport 调用之前，因此等待失败不会创建 client span。
- RPC 的短超时只用于 `RPCTracer.StartSpan`。成功的 span context 从原始请求 context 派生，不从短超时 context 派生。
- root 创建失败后使用 Noop Tracer 继续当前操作，避免后续每次请求重复等待 RPC 超时。
- 同一次 Think 快照一个 Tracer；异步 Archive 在新的 owner context 中只继承这份 Tracer。
- RPC session 关闭后拒绝新请求、完成 pending call、忽略迟到 response；Web 同时结束残留 span。
- Web 只返回 propagation headers，不接收 AI HTTP body，也不执行 fetch。
- `wasmtrans` 仍拥有 endpoint、token、JSON、HTTP status、`Retry-After`、原生 `Response` 和 `AbortController`。
- extra headers 写入和读取都复制 map；受保护 header 名称按大小写不敏感比较，不能被覆盖。
- client 失败只结束为 `StatusError`。最终 Think/Archive failure 才 `RecordError` 一次。
- `RecordError` 只携带 `category/reason`；Web 生成固定 exception，不接收原始错误、prompt、command args、token 或 body。

## 验证命令

```sh
cd tools/ai
go test ./...
GOOS=js GOARCH=wasm go build ./...

cd ../ispx
go test ./...
GOOS=js GOARCH=wasm go build ./...

cd ../../spx-gui
pnpm type-check
pnpm exec vitest run src/ispx/rpc.test.ts src/ispx/sentry-trace-adapter.test.ts
pnpm build

# 在 builder-backend checkout 中
go test ./...
go vet ./internal/aiinteraction ./internal/tracer ./cmd/xbuilder-backend

git diff --check
```

浏览器验收 success、retry、final failure、quota、429、cancel、Archive、rerun 和并发 Player，并确认请求携带 `Sentry-Trace/Baggage`。

## 不在本次实现中

- 生产环境采样率下调。
- `traceparent` / `tracestate`。
- Copilot tracing 或公共 HTTP client 改造。
- 前端上传 prompt、command args、token 或 AI body，以及后端对完整 OpenAI messages 或原始 SSE 的额外采集。
