# AI Interaction Sentry：实现对照

对照方案 [`SENTRY_PLAN.md`](./SENTRY_PLAN.md)。方案是合同，这份是「改了什么、为什么、去哪核对」。

仓库：

- 前端 `builder`，分支 `feat/sentry-ai-interaction`
- 后端 `builder-backend`，分支 `feat/aiinteraction-sentry-server`

切片 1–4 已落地。切片 5（上线后调 `ai.*` 采样率、精灵名）未做。

---

## 怎么核对

1. 打开方案对应章节，再按下表进代码。
2. 先看终态和分类，再看 span 树。exception 规则和 span 树是两件事。
3. Sentry Explore 默认不索引自定义 attribute。搜 `span.op:ai.think` 看 Think；搜 `transaction:"POST /ai-interaction/turns"` 看后端这一跳（不要只搜 `span.op:http.server`）。点 transaction 看 Attributes。SDK 自动打的 `span.category: http` 与业务失败分类无关；失败分类是 tag `category`。

---

## 切片总览

| 方案切片 | 目标 | 达成 | 核对入口 |
|---|---|---|---|
| 1 桥和 Think trace | 独立 root、同步 root 登记、`game_session_id`、command span | 是 | `ProjectRunner.vue` 桥；`sentry_wasm.go`；`sentry_context.go` |
| 2 后端模型现场 | `http.server` 上的 TTFT / `request_body` / `response_body` / 分类；出站 `http.client`；错误体带分类 | 是 | `annotateAITransaction`；`detail_record.go` `SetRawAIDetail`；`util.go` `replyWithInnerError` |
| 3 终态 / exception | `failure` 才打一条；429 / 额度 / 取消只记 outcome | 是 | `classify.go`；`httperr.go`；`exception.go`；`ai.go` |
| 4 合同对齐 | 细 reason、`empty_archive`、quota 不改合同 | 是 | `failure.go`；`responseFromChoice`；`classifyArchiveChoice` |
| 5 采样 | 上线后按量调 `ai.*` | 否 | 挂钩已有，`aiSampleRate` 缺省仍是 `1` |

真机只稳定验过成功 Think。failure / 429 / 空归档主要靠单测。

---

## 对照方案正文

### 链路（方案「链路」）

**设计：** Think / Archive 各开独立 root，不挂在页面 pageload 上，避免页面采样把整次交互吃掉。SDK 留在父页面；WASM 只通知起止，并带上 WASM 侧时间戳。`startThink` / `startArchive` 同步登记 root 并返回成功状态；`startAttempt` 只登记内部 `attempt_id`，`fetchAI` 在下一拍创建 `http.client`（父节点为 think/archive），再由 client header 续到后端。其余涉及 Sentry 的桥操作 `setTimeout(0)`，避免同步调 Sentry 破坏 Go WASM 栈展开。多 Player 时 interaction ID 和 attempt ID 进 `context`，不用全局。

**做了什么：**

| 文件 | 工作 |
|---|---|
| `spx-gui/.../ProjectRunner.vue` | JS 桥：root 同步登记；统一的 `startAttempt` 登记 Think / Archive 的内部 ID；`fetchAI` 下一拍创建 `http.client` 并在 body 读取完成后收口；通过 SDK 当前版本所需的 `_parentSpanId` workaround 连接非 root 子 span；开局 `xbuilder_set_game_session_id` |
| `tools/ispx/ai.go` | 注入 `setGameSessionID` / `setSentryBridge` |
| `tools/ai/sentry_wasm.go` | WASM 调桥；`startRoot` 把 interaction ID 写入 ctx |
| `tools/ai/sentry_stub.go` | 非 wasm 空实现，单测可编 |
| `tools/ai/sentry_context.go` | interaction ID / attempt ID 的 context 绑定 |
| `tools/ai/wasmtrans/wasmtrans.go` | 通过 `FetchAI` 发请求；响应 body 只读一次，trace header 由 client 生成 |
| `tools/ai/command.go` | `ai.command.execute` |
| `spx-gui/src/utils/tracing.ts` | `isAIOperation`（`ai.` 前缀） |
| `spx-gui/src/setup/sentry.ts` | `tracesSampler` 对 `ai.*` 走 `aiSampleRate`，不继承 pageload |
| `spx-gui/src/apps/*/env.ts` | 读 `VITE_SENTRY_AI_SAMPLE_RATE`，缺省 `1` |

**达成：** 是。成功 Think 真机已连到后端 `http.server`，和页面 trace 分开。Sentry 上不再有 `ai.transport.attempt`；AI URL 已排除自动 fetch tracing，避免重复 client。

**核对：** `startRoot` 是否同步登记并返回成功状态；Think / Archive 的 attempt ID 是否隔离；`fetchAI` 是否在下一拍创建 client 并在 settle 后结束；多个 ctx 的 root / attempt 是否互不串。

---

### 什么时候打 exception（方案「什么时候打 exception」）

**设计：** span 记每一步；exception 只在**这条流程停掉**时打一条，由停掉的那一侧上报。后端 retry 可消化的失败只标 span error，避免前端 retry 3 次打出 3 条 Issue。因此后端 **不** `CaptureException`（含 `empty_archive`）。429 用尽、额度、取消是预期结束，进 `outcome`，不进 Issues。

**做了什么：**

| 文件 | 工作 |
|---|---|
| `tools/ai/exception.go` | `shouldCaptureException`：Think 仅 `outcome=failure`；Archive 仅 `captureException` |
| `tools/ai/classify.go` | 429 → `rate_limited`；403+40301 → `quota_exhausted`；timeout / network 才 `failure` |
| `tools/ai/httperr.go` | 只把 `403 && code===40301` 当成额度；其它 403 不是 |
| `tools/ai/ai.go` | Think / Archive 各 `endThink` / `endArchive` **一次**收口 |
| `ProjectRunner.vue` | `AIThinkFailure` / `AIArchiveFailure`；`endThink` 同一 `setTimeout(0)` 里 capture → attributes → finish → `Map.delete` |
| 后端 `annotateAITransaction` | 失败只把 `http.server` 标 error + tag，不 CaptureException |

**达成：** 是。和更早「后端上报 empty_archive exception」的草案不同，以方案现口径为准。

**核对：** `exception_test.go`、`finish_once_test.go`、`think_finish_test.go`、`archive_finish_test.go`。429 / quota / cancel 不得带 exception payload。

---

### Span（方案「Span」）

**`ai.think`** — `endThink` 写入 `outcome` / `turn_count` / `attempt_count`；`failure` 时加 `category`。每个 `ai.command.execute` 是它的子 span。中途 command 被拒、后面仍结束的，outcome 仍是 `success`（`finish_once_test.go`）。

**`http.client`（前端）** — 每次 Interact / Archive。父节点是 `ai.think` 或 `ai.archive`。只记 HTTP 状态与耗时，不记第几轮 / 第几次，不抄后端分类。

**`ai.command.execute`** — `command.go` `callCommandHandler`。`Break` 正常结束；其它 error 标 span error，并把 `ErrorMessage` 写入该 span 的 `error_message` 属性；Think 继续。`invalid_arguments` / `handler_panic` 在出口分流后**停 Think**。

**`http.server`（AI Interaction）** — 不再开 `ai.upstream`。前端 `http.client` 通过 trace header 成为 server 的父节点；`request_id`、`request_body`、`response_body`、`first_meaningful_delta_ms`、失败 tag `category` / `reason` 写在 HTTP transaction 上（`TransactionFromContext`，避免出站 `http.client` 抢走当前 span）。`request_body` / `response_body` 是原始 HTTP body；专用 `aiinteraction_detail_middleware.go` 负责捕获，detail 层不解析字段。`first_meaningful_delta_ms`：**成功或失败，只要测到 meaningful delta 都写**。没测到就没有这个字段。TTFT 挂在 server：不改后端 `httpclient` RoundTrip 返回即 Finish，本轮不采 SSE。后端出站 span 仍是共用 `traceClient` 的 `http.client`。

**Archive** — 使用独立 root 和 context（`scheduleHistoryManagement` 新 ctx），不与 Think 共用链路。没有 `ai.archive.outcome`。取消 / 额度结束 span、不打 exception。非取消 retry 用尽 → 前端 `archive_failure` / `retries_exhausted`。后端 Archive 同样写在 `http.server`，没有 `ai.archive.upstream`。

**达成：** 是。TTFT 和模型现场现在写在 `http.server`；没有 dedicated upstream span。

**核对：** 后端 `TestInteractRecordsHTTPTransaction` / `TestArchiveRecordsHTTPTransaction`；带 `httpclient.NewClient` 的子测确认 `http.client` 在、body 不在 client 上；前端 `think_finish_test.go`。

---

### 分类（方案「分类」）

两套 category，不要混：

- Think exception：`transport_failure` / `model_quality_failure` / `runtime_failure` / `archive_failure`
- 后端错误体：`upstream_failure` / `model_response_invalid` / `archive_failure`（`empty_archive`）

前端 `rate_limited` **不是** exception category，只是 Think `outcome`。

后端细 reason 先匹配先生效，没有 `other` 兜底：

1. `blocked`（Refusal / `content_filter`）
2. `truncated`（`length`，即使带着半截 tool call）
3. `no_choices`
4. `missing_required_tool_call`（首轮，或 history 末项 command 失败的续轮）
5. `invalid_tool_call`（多 call、空 args、`null`、坏 JSON、尾随数据）
6. `unexpected_finish_reason`（空 / 未知 / 与 tool_calls 不匹配；流提前结束）

尾随 JSON：第一次 Decode 成功后必须再 Decode 一次得到 `io.EOF`。只看 `Decoder.More()` 会把 `{"x":1}}` 当合法。见 `decodeToolCallArgs`。

Archive 没有 tool call，正常结束必须 `stop`。`stop` 且内容空 → `empty_archive`。缺失 / 未知 / `tool_calls` → `unexpected_finish_reason`（content 非空也不当成功）。

**达成：** 是。

**核对：** `failure.go` 的 `classifyChoice` / `classifyArchiveChoice` / `decodeToolCallArgs`；`failure_test.go`。

---

### 截断（方案「截断」）

**设计：** `request_body` / `response_body` 各先限制在 15KB，两个原始 body 合计最多 30KB，限制按原始字节计算（Sentry 最终 JSON 序列化会有少量转义开销）；超出时截断，并用 `request_body_dropped` / `response_body_dropped` 记录原始字节数。记录原始 HTTP body，不做 Turn / Archive 的字段级解析；不采完整 OpenAI `messages` 或原始 SSE。

**做了什么：** AI endpoint 用专用 middleware 捕获原始 request / response body；公共 detail 层只负责写入 `http.server` 和大小控制。

| 文件 | 职责 |
|---|---|
| `aiinteraction_detail_middleware.go` | 捕获 `/turns`、`/archives` 的原始 request / response body |
| `detail_record.go` | 写入 `http.server`、每个 body 15KB 上限、截断和 dropped 字节数 |

**达成：** 是。核对 `detail_test.go`、`aiinteraction_detail_middleware_test.go`，以及 `TestInteractRecordsHTTPTransaction` / `TestArchiveRecordsHTTPTransaction` 的模型状态测试。

---

## 切片 4 对照（方案「切片 4」）

方案里「现状缺口」是改之前的旧描述，下面是改完后的状态。

### 1. `empty_archive`

| 方案 | 代码 |
|---|---|
| `stop` + 空 / 空白 → HTTP 失败 | `classifyArchiveChoice` |
| `archive_failure` / `empty_archive` | 同函数；错误体经 `Failure` → `replyWithInnerError` |
| `http.server` 标 error，原始 body 仍在 | middleware 写入 `request_body` / `response_body`，`annotateAITransaction` 写分类 |
| 不改 Think outcome | Archive 独立 root |
| 后端不 CaptureException | `annotateAITransaction` |
| 前端照常 retry，用尽 `retries_exhausted` | 切片 3 已有，切片 4 不改前端 |

`no turns to archive` 仍是入参错误，不是 `empty_archive`。

### 2. 细 reason

`Interact`：`requiresToolCall(request) && len(tools) > 0` 传给 `callOpenAI` 和 `responseFromChoice`。失败续轮设 `tool_choice=required`。成功续轮可以只回文本。

`callOpenAI` 不再把空 `finish_reason` 当传输错误；交给 `classifyChoice` 落 `unexpected_finish_reason`。

### 3. Quota

**未改合同。** 仍是 `ensureQuotaRemaining` → `HTTP 403` + `code=40301`，body 无 `category`。短窗仍是 `429` + `42901`。不给额度写模型现场。其它 403（`40300`）前端不当额度。补了 `TestEnsureQuotaRemainingAIInteractionContract` / `TestQuotaErrorCodes`。

---

## 文件清单（只含方案相关）

前端 `builder`：

- 改：`ProjectRunner.vue`、`tracing.ts`、`sentry.ts`、`env.ts`、`ai.go`、`command.go`、`transport.go`、`wasmtrans.go`、`ispx/ai.go`
- 新：`SENTRY_PLAN.md`、本文件、`sentry_wasm.go`、`sentry_stub.go`、`sentry_context.go`、`classify.go`、`httperr.go`、`exception.go` 及对应 `*_test.go`

后端 `builder-backend`：

- 改：`aiinteraction.go`、`util.go`（错误体抄 Failure）及测试
- 新：`failure.go`、`detail_record.go`、`aiinteraction_detail_middleware.go` 及对应测试

---

## 未做 / 不要当成缺口

- 切片 5：生产采样率仍缺省 `1`；后端仍是全局 `SENTRY_SAMPLE_RATE`，未按 `/api/ai-interaction/*` 拆。
- 精灵名未进 tag。
- `sentry.ts` 的 development 早退保持原样（本地测过又还原了）。不要再注释掉提交。
- 不抽跨模型公共 detail 包；不采完整 OpenAI `messages` / 原始 SSE；不改 `httpclient` 生命周期。AI detail 直接记录 endpoint 的原始 request / response body。

---

## 建议怎么跑

```text
# 前端 WASM 侧
cd builder/tools/ai && go test ./...

# 后端
cd builder-backend
go test ./internal/aiinteraction/ ./cmd/xbuilder-backend/

# 真机
Sentry environment 选实际上报环境；搜 span.op:ai.think
后端这一跳搜 transaction:"POST /ai-interaction/turns"
点 transaction 看 request_id / request_body / response_body / tag category、reason
Issues 搜 AIThinkFailure / AIArchiveFailure
```

改了 `tools/ai` 或 `tools/ispx` 后需要重编 wasm 再拷进 `spx-gui` 的 wasm 资源，否则父页面桥和 WASM 对不上。
