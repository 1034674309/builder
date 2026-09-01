# AI Interaction Sentry

本文定义 AI Interaction 的 trace 结构和各层职责。目标是定位一次 Think 或 Archive 慢在哪里、最终为什么失败，同时保持 AI HTTP 仍由 WASM Transport 执行。

## Trace 结构

Think 和 Archive 各自是一条独立 trace，不挂在页面 pageload 上。浏览器生成的 `Sentry-Trace` 和 `Baggage` 会随 WASM 发出的请求传到后端。

```text
ai.think
├── http.client               WASM 发往 Builder backend
│   └── http.server           backend 的 AI 接口
│       └── http.client       backend 发往模型
└── ai.command.execute        游戏 command 的执行

ai.archive
└── http.client               WASM 发出的归档请求
```

不创建 `ai.transport.attempt`、`ai.upstream` 或 `ai.archive.upstream`。每次真正调用 `Transport.Interact` 或 `Transport.Archive` 时只创建一个 `http.client`。

## 各层职责

| 层 | 职责 |
| --- | --- |
| `tools/ai` | 定义通用 `Tracer` / `Span`；创建 Think、Archive、Command 和 `http.client` span；收口终态分类 |
| `tools/ispx` | 用通用 JSON-RPC client 实现 `ai.Tracer`，管理 span 请求和 session 生命周期 |
| Web Sentry adapter | 创建真实 Sentry span、记录最终 exception，并返回 propagation headers |
| `wasmtrans` | 序列化请求、合并不透明 extra headers、执行原生 `fetch`、处理取消和解析响应 |
| backend `http.server` | 记录请求/响应 body、模型 request ID、首个有效响应耗时和后端失败分类 |
| backend `http.client` | 保留已有模型请求 span，不改变生命周期，也不采集原始 SSE |

JSON-RPC 不承载 AI request/response body，也不代理 HTTP。

## 请求链路

```text
Player.Think / Archive
  -> tools/ai 创建 root span
  -> TraceTransport 创建 http.client
  -> iSPX 通过 trace/span.start 请求 Web
  -> Web 创建 Sentry span 并返回 Sentry-Trace / Baggage
  -> headers 写入当前 Transport context
  -> wasmtrans 合并 headers 并调用 window.fetch
  -> backend 续接同一条 trace
```

`wasmtrans` 将 extra headers 当作不透明数据，同时禁止它们覆盖 `Authorization`、`Content-Type`、`Host`、`Cookie` 等凭据、载荷和浏览器控制字段。当前只发送 `Sentry-Trace` 和 `Baggage`，不发送 `traceparent` / `tracestate`。

Tracer 启动失败时直接降级为普通 AI 调用。失败不能阻断请求，也不能把 HTTP 改走 RPC。

## 结束状态和 exception

`ai.think` 写入 `outcome`、`turn_count` 和 `attempt_count`；`ai.archive` 写入 `outcome` 和 `attempt_count`。

| 状态 | 含义 | exception |
| --- | --- | --- |
| `success` | 正常结束，或 command 返回 `Break` | 否 |
| `cancelled` | 游戏停止或 context 取消 | 否 |
| `quota_exhausted` | HTTP `403` 且 body 中 `code=40301` | 否 |
| `rate_limited` | HTTP `429` 重试用尽 | 否 |
| `failure` | 超时、网络错误、参数错误、handler panic、缺少初始 command、轮数上限或归档重试用尽 | 是，一次 root 最多一条 |

请求级 `http.client` 失败只设置 `StatusError`，不调用 `RecordError`。最终 Think/Archive 失败才在 root 上调用一次 `RecordError`。

Web 使用固定的 `ISPXOperationError("iSPX operation failed")` 创建 exception，只附带 `category` 和 `reason`。前端 trace 不上传原始错误文本、prompt、command args、token 或 AI request/response body。Command span 只记录 command 名称、轮次和成功状态。

具体错误和请求现场从同一 trace 下的 backend `http.server` 查看。后端会按现有策略保存 `/ai-interaction/turns` 和 `/archives` 的原始 request/response body，每份最多 15 KB；超出部分截断并记录原始字节数。这一轮不修改后端采集策略。

## Session 生命周期

`ProjectRunner` 在每次运行时创建一个 JSON-RPC session。stop、rerun、unmount、iframe reload、game error 或 exit 都会关闭旧 session：

- 拒绝 pending call。
- 结束残留 span。
- 忽略旧 session 的迟到消息。

一次 Think 在开始时快照当前 session Tracer，并通过派生 context 传给请求和 Command。异步 Archive 只继承这份 Tracer，不复用已经结束的调用 context，因此旧运行不会读到新运行的全局 Tracer。

## 采样

当前 `ispxSampleRate` 保持为 `1`，采样行为不变。是否下调由产品和观测成本单独决定，不属于本次架构调整。

## 验收

在 Sentry 中确认：

1. 成功 Think 有一个 `ai.think` root、对应的 `http.client` 和 Command 子 span。
2. 一次失败后重试成功时，每次真实请求各有一个 client span，root 不产生 exception。
3. 三次请求失败时有三个 client span，root 最多一条 exception，且只带 `category/reason`。
4. quota、429 和 cancel 不产生 exception。
5. Archive success/failure 的 span 状态和请求次数正确。
6. 浏览器请求携带 `Sentry-Trace/Baggage`，后端续接同一 trace。
7. stop/rerun 后旧消息不会进入新 session，并发 Player 的 headers 不串。
