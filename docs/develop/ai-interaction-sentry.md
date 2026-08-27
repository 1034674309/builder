# AI Interaction Sentry

本文定义 AI Interaction 在 Sentry 中记录什么，以及各层的职责。目标不是把所有请求内容都发到 Sentry，而是能回答三个问题：一次交互慢在哪里、为什么失败、失败时能否还原现场。

## Trace 结构

Think 和 Archive 各自是一条独立 trace，不挂在页面 pageload 上。浏览器和后端使用同一组 trace headers，所以一次 AI 请求可以从浏览器一直看到后端。

```text
ai.think
├── http.client              浏览器发往 Builder backend
│   └── http.server           backend 的 AI 接口
│       └── http.client       backend 发往模型的请求
└── ai.command.execute        游戏 command 的执行

ai.archive
└── http.client              同样经过 backend 的归档请求
```

`ai.transport.attempt`、`ai.upstream` 和 `ai.archive.upstream` 不再创建。代码中仍保留 `attempt_id`，但它只是 Go/WASM 与 JavaScript 之间的内部关联 ID，用来找到对应请求和处理取消，不会出现在 Sentry。

## 各层负责什么

| 层 | 记录内容 |
| --- | --- |
| `ai.think` | 一次 Think 的 `outcome`、轮数、请求次数，以及 command 子 span |
| `ai.archive` | 一次归档的耗时、请求次数和最终状态 |
| 前端 `http.client` | 浏览器实际发出的 AI HTTP 请求、状态码和耗时 |
| 后端 `http.server` | 原始请求/响应 body、模型 request ID、首个有效响应耗时和后端失败分类 |
| 后端 `http.client` | 已有的模型 HTTP 请求；不改它的生命周期，也不采集 SSE |
| `ai.command.execute` | command 名称、轮次、执行结果和普通错误文本 |

Sentry SDK 在父页面运行。WASM 只通知开始、结束和请求信息；真正创建前端 `http.client` 并调用 `fetch` 的代码在父页面的 `fetchAI` 中。

## 前端 Think 和请求

`startThink` / `startArchive` 同步登记 root，并把内部 ID 写入对应的 Go context。其它桥调用异步执行，避免在 WASM 的同步调用栈上操作 Sentry。

每次 `Transport.Interact` 或 `Transport.Archive` 都会生成一个新的 `attempt_id`。`fetchAI` 下一拍创建 `http.client`，从这个 client 生成 `Sentry-Trace` 和 `Baggage`，再调用浏览器 `fetch`。请求完成后才结束 client span。

浏览器响应 body 只读取一次，转换成普通对象后交给 WASM：

```text
{ ok, status, statusText, retryAfter, body }
```

WASM 继续负责解析成功响应和错误 JSON。取消请求时，Go context 和 JavaScript 的 `AbortController` 都可以中止同一个 fetch。

## Think 的结束状态

`ai.think` 在结束时写入 `outcome`、`turn_count` 和 `attempt_count`。只有真正失败才上报一条 exception。

| 状态 | 含义 | exception |
| --- | --- | --- |
| `success` | 模型正常结束，或 command 返回 `Break` | 否 |
| `cancelled` | 用户停止游戏或 context 被取消 | 否 |
| `quota_exhausted` | HTTP `403` 且 body 中 `code=40301` | 否 |
| `rate_limited` | HTTP `429` 重试用尽 | 否 |
| `failure` | 超时、网络错误、参数错误、handler panic、缺少初始 command 或达到轮数上限 | 是 |

普通 handler error 只标记 `ai.command.execute` 并写入 `error_message`；如果交互还能继续，不把它升级成 Think exception。未知 command 写进 history，交给后续请求现场排查。

Archive 规则类似：取消和额度结束只结束 span；非取消的重试用尽上报一条 `retries_exhausted` exception。Archive 的结果不改变 Think 的 outcome。

## 后端 AI 接口

后端复用已有的 `http.server` transaction，不再创建专用 upstream span。它记录：

- `request_body`：前端发往 `/ai-interaction/turns` 或 `/archives` 的原始 JSON。
- `response_body`：后端返回给前端的原始 JSON。
- `request_id`：模型响应中的请求 ID。
- `first_meaningful_delta_ms`：模型返回第一个有效内容或 tool call 的耗时。
- `category` / `reason`：后端对失败的分类标签。

后端失败响应使用统一格式：

```json
{"category":"model_response_invalid","reason":"no_choices","request_id":"..."}
```

后端只把 transaction 标为 error 并返回分类，不直接 `CaptureException`。这样后端的重试不会为同一个问题产生多条 Issue，最终是否上报 exception 由前端流程决定。

### 失败分类

传输或 provider 错误属于 `upstream_failure`，原因是 `timeout` 或 `provider_error`。模型响应不符合合同则属于 `model_response_invalid`，原因包括：

```text
blocked
truncated
no_choices
missing_required_tool_call
invalid_tool_call
unexpected_finish_reason
```

归档结果为空属于 `archive_failure / empty_archive`。额度边界保持为 HTTP `403` + `code=40301`；普通 `403` 不当作额度错误。

## Body 大小

`request_body` 和 `response_body` 都是原始 body，不做字段级解析，也不采集完整 OpenAI messages 或原始 SSE。每个 body 最多记录 15 KB；超出时截断，并记录原始字节数：

```text
request_body_dropped
response_body_dropped
```

body 放在 data/context 中，不放在 tag 中。短字段使用 tag，便于筛选；大字段只在点开对应 transaction 时查看。

## 验收方式

在 Sentry 中：

1. 搜 `span.op:ai.think`，确认 Think 是独立 root，并能看到浏览器 `http.client`。
2. 搜 `transaction:"POST /ai-interaction/turns"` 或 `transaction:"POST /ai-interaction/archives"`，查看 `request_body`、`response_body`、`request_id` 和失败标签。
3. 在 Issues 中只应看到真正的 Think/Archive failure；取消、额度耗尽和 429 不产生 exception。

自定义 attribute 不一定会出现在 Explore 的默认字段列表中，应点开 span 查看。SDK 的 `span.category=http` 与业务 tag `category` 不是同一个字段。

## 当前范围

已完成 Think/Archive trace、浏览器 client、后端 server 现场、终态和失败合同。生产采样率调整、精灵名 tag、Copilot 采集、完整模型 messages 和 SSE 内容不在本文范围内。
