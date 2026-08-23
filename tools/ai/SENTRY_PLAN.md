# AI Interaction Sentry 方案

把 `Player.think`、传输、命令执行、归档和后端调模型接到 Sentry。

看稳定性、慢在哪：靠 span 耗时，后端再加 `first_meaningful_delta_ms`。
看质量差在哪：靠 `category` / `reason`；前端留命令现场，后端留模型 input/output。

短字段进 tag，原始报错进 message，复现现场进 `ai.detail`。

合同：[builder#3407](https://github.com/goplus/builder/pull/3407)、[builder-backend#326](https://github.com/goplus/builder-backend/pull/326)。实现对照（改了什么、去哪核对）：[SENTRY_IMPLEMENTATION.md](./SENTRY_IMPLEMENTATION.md)。

已有基础：后端 HTTP 中间件会对请求 `ContinueTrace`；`callOpenAI` 已是流式拼阻塞，有意义 delta 的耗时已经能算；`llmsentry` 的上报模式可复用。切片 1–3（WASM 桥、Think / Archive 独立 root、终态 / exception）已落地。切片 4 对齐后端合同：细 reason、`empty_archive`、quota 边界。

---

## 链路

```text
Player.think → 多个 turn → 每 turn 多次 attempt
  attempt = fetch = 后端 HTTP = 一次调模型
Archive 独立进行，结果不改变 Think 的 outcome
```

每次 Think 自己开一条 root trace，和页面 pageload 分开。attempt 的 fetch 带上这条 Think 的 `Sentry-Trace` / `Baggage`，后端续在同一棵树上。Think 按 `ai.*` 自己采样，寿命跟这次交互走。

```text
ai.think                         # 独立 root，父页面 JS 创建
  ├─ ai.transport.attempt        # 一次 Interact
  │    └─ http.server            # 后端中间件（已有）
  │         └─ ai.upstream
  └─ ai.command.execute          # 填参数 → 调 handler → 返回

ai.archive                       # 独立 root，不复用 Think 的 ctx header
  └─ ai.archive.transport.attempt
       └─ http.server
            └─ ai.archive.upstream
```

SDK 在父页面。WASM 经桥通知 span 起止（带上 WASM 侧取好的时间戳）。`startThink` / `startArchive` 必须同步返回 `Sentry-Trace` / `Baggage`；其余桥方法 `setTimeout(0)`，避免同步调 Sentry 破坏 Go WASM 的 panic 栈。多 Player 并发时，trace 头放在对应 `context` 里（`TraceHeadersFromContext`），不要用全局变量。`game_session_id` 由 runner 开局注入，用来把同一局里多次 Think / Archive 收拢到一起。

`reason` 在对应代码分支写入（`errors.As` / `errors.Is`），排查动作相同就共用一个。`model_response_invalid` 按下面优先级落到细 reason，不再用 `other` 当兜底。`upstream_failure` 仍只有 `timeout` / `provider_error`。

---

## 什么时候打 exception

span 记录每一步做了什么、花了多久；失败的步骤标 `status=error`。
exception 只在**这条流程停掉**时打一条，由停掉的那一侧上报，message 用原始 `err.Error()`。

前端停 Think 时上报：timeout / network 重试用尽、缺初始 command、`turn_limit`、`handler_panic`、`invalid_arguments`。429 用尽和额度、取消不打 exception。
前端停 Archive 时上报：非取消且 retry 用尽（`retries_exhausted`）。空归档是后端这一跳 HTTP 失败，前端仍按普通 Archive retry；用尽后还是这一条前端 exception。

后端失败只把 span 标 error，并把 `{category, reason, request_id}` 放进 HTTP 错误体。后端 **不** `CaptureException`（含 `empty_archive`），避免前端 retry 打出多条 Issue。

额度用完、用户取消、429 用尽，是预期结束，记在 `ai.think` 的 `outcome` 上。handler 返回普通 error（还能下一 turn）只体现在 command span 上。

exception 带 `game_session_id`、`category`、`reason`。exception 全量保留；trace 按 `ai.*` / `/api/ai-interaction/*` 单独采样。

后端把失败分类放进错误响应 `{category, reason, request_id}`，前端 transport span 打成 `backend_*` tag，同一条 Think 里就能看到失败落在哪一层。

---

## Span

### `ai.think`

tags：`game_session_id`、`outcome`、`turn_count`、`attempt_count`；`outcome=failure` 时加 `category`。

| `outcome` | 含义 |
|---|---|
| `success` | 模型不再给 command，或 handler 返回 `Break` |
| `cancelled` | 停游戏 / 中断（`ctx` 取消） |
| `quota_exhausted` | 额度用完：HTTP `403` 且 JSON `code=40301` |
| `rate_limited` | 429 重试用尽（attempt 标 error，不打 exception） |
| `failure` | 失败终点，伴随一条 exception |

`outcome` 只描述整次 Think 的结束方式。中途 command 被拒绝、后面仍正常结束的，仍是 `success`。`quota_exhausted` 发生在进入 `AIInteraction` 之前，没有 `ai.upstream`；attempt 上 `backend_reason` 为空。

### `ai.transport.attempt`

每次 `Transport.Interact`。tags：`turn_index`、`attempt`。失败标 span error，并把后端带回的 `backend_*` / `request_id` 打上。

### `ai.command.execute`

已注册的 command 进入 `callCommandHandler` 时创建，覆盖填参数到 handler 返回。tags：`command_name`、`turn_index`。

handler 返回 `Break`：span 正常结束。
handler 返回其它 error：span 标 error，`ErrorMessage` 进 detail；Think 继续。游戏里业务拒绝和模型胡来目前是同一种返回值，这一层只记执行结果。
未注册的 command 没有 handler，结果写进 history，随下一轮 `ai.detail` 一起看。

### `ai.upstream`

每次调模型。tags：`request_id`。

measurement：`first_meaningful_delta_ms`，从发出请求到第一个带 content 或 tool_call 的 delta。成功或失败，只要测到了都写；没测到这条 span 上就没有这个值。失败流若已经出过 meaningful delta，保留 TTFT 方便看卡在首包之后还是之前。

`ai.detail`（context）成功失败都记：

```text
input:  user_msg, user_context, role, role_context,
        knowledge_base, archived_history, history, command_specs
output: finish_reason, content
        解析成功：command_name, command_args
        解析失败：tool_calls
```

`request_id` 放在 tag 上筛。`role` / `role_context` 放在 input 顶层，history 里只留各轮自己的消息和执行结果。`history` 末项的 `ExecutedCommandResult` 是上一 turn 的执行结果。

### Archive

独立 root `ai.archive`，包住整段 retry；每次 `Transport.Archive` 是 `ai.archive.transport.attempt`。`scheduleHistoryManagement` 开的是新 `spx.Go` ctx，`manageHistory` 里 `startArchive`，不从 Think 继承 header。

前端 span 记耗时。没有 `ai.archive.outcome`。取消 / 额度结束 span、不打 exception。非取消 retry 用尽打一条 `archive_failure` / `retries_exhausted`。

后端 `ai.archive.upstream` 同样有 `request_id` 和 `first_meaningful_delta_ms`。Archive 没有 tool call，正常结束必须是 `finish_reason=stop`。`stop` 但内容为空 / 空白归一化后为空：`empty_archive`。缺失、未知、或 `tool_calls`：`unexpected_finish_reason`，即使 content 非空也不当成功。detail：

```text
input:  existing_archive, turns
output: archive_content, finish_reason, content
```

---

## 分类

前端 exception（`outcome=failure` 才打）：

```text
transport_failure        timeout | network
model_quality_failure    missing_initial_command | invalid_arguments
                         | turn_limit
runtime_failure          handler_panic
archive_failure          retries_exhausted
```

| reason | 判定 | 载体 | 额外 tags | detail |
|---|---|---|---|---|
| `timeout` | 超时包装错误且重试用尽 | exception | `turn_index`, `attempt` | `user_msg` |
| `network` | 其余 transport 错误且重试用尽 | exception | `turn_index`, `attempt` | `user_msg` |
| `missing_initial_command` | 空 command 且本 Think 从未执行过 command | exception | | `user_msg` |
| `invalid_arguments` | 参数填充失败，Think 结束 | exception | `turn_index` | `command_name`, `command_args`, `user_msg` |
| `turn_limit` | 跑满 maxTurns | exception | | `user_msg` |
| `handler_panic` | handler panic（非 AbortThread） | exception | `turn_index` | `command_name`, `command_args` |
| `retries_exhausted` | 归档重试用尽（含后端 `empty_archive` 被前端消化完） | exception | | `attempt_count`, `error` |

不打 exception 的 Think 终点：`cancelled`、`quota_exhausted`（`403` 且 `code=40301`）、`rate_limited`（429 用尽）。后两者的 attempt 仍标 error。`rate_limited` 不是 Think 的 exception category。

`unknown_command` 写入 history，随后续 turn 的 detail 排查。
`invalid_arguments` 和 `handler_panic` 已在 command 出口分流。

后端 span 带 `request_id` 和 `ai.detail`。失败走 HTTP 错误体 `{category, reason, request_id}`，**不** `CaptureException`。attempt 上的 `backend_*` 是后端原样，不当 Think 的 category。

```text
upstream_failure         timeout | provider_error
model_response_invalid   见下表（HTTP 成功但 completion 按合同不能用）
archive_failure          empty_archive
```

`upstream_failure` 只用于真正的传输 / API 错。`model_response_invalid` 按优先级落到细 reason，不再用 `other` 兜底。

| 优先级 | reason | 判定 | 排查看 |
|---|---|---|---|
| 1 | `blocked` | Refusal 或 `finish_reason=content_filter` | 提示词 / 安全策略 |
| 2 | `truncated` | `finish_reason=length` | maxTokens / prompt 过长 |
| 3 | `no_choices` | choices 为空 | provider 空包 |
| 4 | `missing_required_tool_call` | 本跳必须有 function call，但没有 | tool_choice / 提示 |
| 5 | `invalid_tool_call` | 多 call、空 args、`null`、坏 JSON、尾随数据（第二次 Decode 必须 `io.EOF`，不能只看 `More()`） | `output.tool_calls` |
| 6 | `unexpected_finish_reason` | finish_reason 未知 / 缺失 / 与 tool_calls 不匹配；流提前结束也归这里 | `output.finish_reason` |

必须有 tool call 的跳：`ContinuationTurn == 0`（首轮），以及 history 末项 command 执行失败的续轮。成功续轮可以没有 tool call（模型收工，前端当 Think `success`）。现码 `callOpenAI(..., requiredToolCall)` 只在首轮为 true，切片 4 要补失败续轮。

`empty_archive` 不属于 `model_response_invalid`，见切片 4。

---

## 截断

整份 `ai.detail` ≤ 32KB。超出时先收 `knowledge_base`、`command_specs`、更早的 `history` / `turns`、`archived_history`，最后才动 `output` 和当前 `user_msg`。被收掉的字段写入 `_dropped`（带原始字节数）。

| 字段 | 上限 |
|---|---|
| `user_msg` | 全文 |
| `output` 单字段 | 16KB，解析失败时优先留 `tool_calls` |
| `history` / `turns` | 16KB，从最近往前 |
| `knowledge_base` / `role_context` / `user_context` / `archived_history` / `existing_archive` / `command_specs` | 各 4KB |
| 前端 command + args | 合计 4KB |
| `error` | 1KB |

---

## 切片

1. **桥和 Think trace**（已落地）：桥函数、`game_session_id`、从 Think 取 Trace 头；`ai.think` / `ai.transport.attempt` / `ai.command.execute`。验收：Think 能连到后端 `http.server`，和页面 trace 分开。
2. **后端 upstream**（已落地）：`ai.upstream` / `ai.archive.upstream`、`request_id`、`first_meaningful_delta_ms`、`ai.detail`；错误响应带粗分类。验收：同一次调模型的 input/output 能在这条 trace 上对上。
3. **终态**（已落地）：按上表接 exception 和 `outcome`；`invalid_arguments` / `handler_panic` 分流。验收：`failure` 才打一条 exception；429 用尽 / 额度 / 取消不进 Issues。`outcome` 是自定义 span attribute，Explore 默认不索引，搜 `span.op:ai.think` 再点根 span 看 Attributes。SDK 自动打的 `category: ai` 不是切片 3 的 `transport_failure` 等。
4. **合同对齐**（builder-backend#326，已落地）：细 reason、`empty_archive` 闭环、quota 边界。前端 `backend_*` 已能抄 attempt，切片 4 **不改前端**。切片 5 的采样、精灵名、还原 `sentry.ts` 早退仍不做。
5. **采样**：上线后按量定 `ai.*` 采样率。

---

## 切片 4

范围在 `builder-backend/internal/aiinteraction`。HTTP 层 `replyWithInnerError` 已经会把 `Failure.{Category,Reason,RequestID}` 抄进错误体，前端 attempt 已经打 `backend_*`。判定已按下列合同落地。

### 现状缺口

- `failure.go`：`no_choices` / 流提前结束落到 `model_response_invalid` + `other`；解析失败一律 `other`。没有 `blocked` / `truncated` / `invalid_tool_call` 等常量。
- `responseFromChoice`：多 tool call 只取第一个；空 args 当成功；`json.Unmarshal` 不拒尾随数据，`"null"` 能解成 nil map。
- `Interact`：`requiredToolCall` 仅 `ContinuationTurn == 0`。失败续轮模型可以只回文本，后端当成功。
- `Archive`：空字符串 / 空白归一化后为空仍当成功返回。
- quota 已在 `ensureQuotaRemaining`（进 `AIInteraction` 之前）回 `HTTP 403` + JSON `code=40301`，body 只有 `code` / `msg`。其它 403 不是额度。这是现合同，前端已按此识别。

### 1. `empty_archive` 闭环

`finish_reason=stop` 且内容为空字符串、或 `strings.TrimSpace` 后为空 → 不当成功。缺失 / 未知 / `tool_calls` 先走 `unexpected_finish_reason`，不落到 `empty_archive`。

- HTTP 失败；`category=archive_failure`，`reason=empty_archive`。
- `ai.archive.upstream` 标 error；`ai.detail` 仍写（能看到空 `content` / `finish_reason`）。
- 错误体带 `{category, reason, request_id}`。
- **不改** Think 的 `outcome`。Archive 本来就是独立 root。
- 后端 **不** `CaptureException`（与切片 3「停掉才打、attempt 不打」一致；否则前端 retry 3 次会多条 Issue）。提交只保留这套约定，不要再写「后端上报 empty_archive exception」。
- 前端仍按普通 Archive 失败 retry；用尽后还是一条 `archive_failure` / `retries_exhausted`。

`no turns to archive` 仍是入参错误，不是 `empty_archive`。

### 2. 细 reason：先匹配先生效

在 `classifyCallError` / `responseFromChoice`（或并列的 completion 校验）里按分类表优先级判定，命中即停。不要再落到 `other`。

`upstream_failure` 仍只有：

- `timeout`：`context.DeadlineExceeded`、`net.Error.Timeout()`、上游 408 / 504。
- `provider_error`：其余传输 / API 错（含上游 4xx/5xx、chunk id 不一致）。

`model_response_invalid` 补全：

- `blocked`：message Refusal，或 `finish_reason=content_filter`。
- `truncated`：`finish_reason=length`（即使同时带着半截 tool call，也优先 `truncated`）。
- `no_choices`：accumulator 无 choices。
- `missing_required_tool_call`：本跳要求 function call 但 `ToolCalls` 为空。要求条件：首轮，或 history 末项 `ExecutedCommandResult != nil && !Success`。给这些跳设 `tool_choice=required`。
- `invalid_tool_call`：多于一个 call；args 为空字符串；JSON `null`；无法解析；第一次 Decode 成功后第二次必须 `io.EOF`（`{"x":1}}` 这类 `More()` 看不出来的尾巴也算）。
- `unexpected_finish_reason`：`finish_reason` 为空（流提前结束，现 `errStreamEndedEarly`）、未识别值、或与是否有 tool_calls 不匹配（例如 `stop` 却带 call、`tool_calls` 却没有 call）。Archive 没有 tool call，正常结束只能是 `stop`。不要再用 `other`。

校验写在 span `finish(err)` 之前，这样 `ai.upstream` / `ai.archive.upstream` 的 status 和错误体一致。detail 成功失败都留。

### 3. Quota 边界（不改合同）

维持：`HTTP 403` + `code=40301`。短窗限流仍是 `429` + `42901`，前端走 `rate_limited`，不是额度。

切片 4 **不** 给 quota 加 `category` / `reason` / `request_id`，**不** 改前端识别，**不** 给 quota 开 `ai.upstream`（根本没进 `AIInteraction`）。attempt 上可能有失败 fetch，但 `backend_reason` 为空。其它 403（例如 `40300`）不是额度，前端不当 `quota_exhausted`。

只补测试和文档，把这条边界钉死。

### 验收

- 空归档：`stop` 且内容空 → 错误体 `archive_failure` / `empty_archive`，upstream span error，detail 在，无 Issue；Think 的 `outcome` 不变。
- Archive 缺失 / 未知 / `tool_calls` 的 finish_reason → `unexpected_finish_reason`，即使 content 非空。
- 各细 reason 有单测；错误体和 span tag 一致；不再出现 `reason=other`。
- 流提前结束 → `unexpected_finish_reason`，不是 `other`。
- 失败续轮无 tool call → `missing_required_tool_call`；成功续轮无 tool call → HTTP 成功。
- 多 call / 空 args / `null` / 坏 JSON / 尾随数据（含 `{"x":1}}`）→ `invalid_tool_call`。
- quota：`403` + `40301`，无 `category`；其它 403 不是额度。后端无 `CaptureException`。
- 搜 Sentry 用 `span.op:ai.upstream` / `span.op:ai.archive.upstream`，点 Attributes 看 `reason`；不要用 Explore 默认字段搜自定义 attribute。

---

## 附录：实现锚点

- 后端 Sentry：`cmd/xbuilder-backend/main.yap`；`ContinueTrace` 在 `cmd/xbuilder-backend/middleware.go`；上报模式见 `internal/tracer/llmsentry/sentry.go`。后端失败不 `CaptureException`。
- 调模型：`internal/aiinteraction/aiinteraction.go` 的 `callOpenAI`；`request_id` 用 chunk id；有意义 delta 见 `chunkHasMeaningfulDelta`（成功失败只要测到都写 TTFT）。切片 4：`responseFromChoice` 改为校验而非「取第一个 call」；`Interact` 的 `requiredToolCall` 覆盖失败续轮；`Archive` 正常结束必须 `stop`，空内容走 `empty_archive`。
- 分类：`internal/aiinteraction/failure.go`。删掉 `ReasonOther` 在 completion 校验上的兜底。quota 不经过这里，见 `cmd/xbuilder-backend/util.go` 的 `ensureQuotaRemaining`（`errorQuotaExceeded=40301`）。
- 错误体：`cmd/xbuilder-backend/util.go` 的 `replyWithInnerError` 已抄 `Failure`；切片 4 不用改 HTTP 层。
- 前端桥：`spx-gui/src/components/project/runner/ProjectRunner.vue`；`startThink` / `startArchive` 同步取 header，其余 `setTimeout(0)`（同文件 `handleRuntimePanic`）。Think / Archive 各自独立 root（可参考 LSP 按 operation 采样，页面 trace 继续自己结束）。
- Trace 头：`startThink` / `startArchive` 把 `Sentry.getTraceData()` 写进 Go `context`；fetch 用 `TraceHeadersFromContext(ctx)`。写法参考 `spx-gui/src/apis/common/client.ts`。
- 采样：前端 `spx-gui/src/setup/sentry.ts` 的 `tracesSampler`；后端按路由。切片 4 不改采样、不还原 development 早退。
- 前端写入：`tools/ai/ai.go`（取消 → `cancelled`；quota → `quota_exhausted`；429 用尽 → `rate_limited`；timeout / network 用尽才 exception；缺初始 command；未知命令写 history；`invalid_arguments`；command span；`turn_limit`）；`tools/ai/command.go`（`ai.command.execute`、`handler_panic`）；`manageHistory` 重试用尽。切片 4 不改这些。
