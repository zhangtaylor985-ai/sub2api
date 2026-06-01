# Claude -> GPT 稳定性迁移矩阵（2026-05-27）

## 本轮边界

- 第一步：迁移价值矩阵与风险边界梳理。
- 第二步：只补测试，不改业务逻辑。
- 不部署、不重启线上 Sub2API。

## 参考来源

- 旧项目 Bug 台账：`/Users/taylor/code/tools/CLIProxyAPI-ori/docs/claude-client-compat/bug-registry_CN.md`
- 旧项目 Web search 兼容记录：`/Users/taylor/code/tools/CLIProxyAPI-ori/tasks/2026-03-30-claude-gpt-compat-progress.md`
- 旧项目 GPT-5.5 Claude SSE 策略：`/Users/taylor/code/tools/CLIProxyAPI-ori/tasks/gpt55-claude-parity-sse/strategy_CN.md`
- 旧项目 worker SSE 错误帧经验：`/Users/taylor/code/tools/CLIProxyAPI-ori/tasks/2026-05-14-worker-rolling-cache-regression_CN.md`
- Sub2API Claude -> OpenAI 转换：`backend/internal/pkg/apicompat/anthropic_to_responses.go`
- Sub2API OpenAI -> Claude 转换：`backend/internal/pkg/apicompat/responses_to_anthropic.go`
- Sub2API OpenAI `/v1/messages` 转发：`backend/internal/service/openai_gateway_messages.go`

## 总体判断

Sub2API 已经有较好的底座：模型映射、账号调度、`prompt_cache_key`、`previous_response_id`、终端事件检查、部分 tool-call 兜底、cached tokens 记账和 Web search 原生映射都已存在。

真正值得从旧 CLIProxyAPI 迁移的不是整套架构，而是这些稳定性经验：

- 不把缺失 terminal event 的流当成功。
- 不把 200/SSE 内的错误帧当普通内容。
- 不把半截 tool_use 写成成功 transcript。
- 不把后置 reasoning summary 冒充实时 thinking。
- 不丢弃 Claude `tool_result` 中未知但有信息量的 block。
- 对 Claude CLI / VSCode 做客户端差异化，而不是一刀切。

## 迁移矩阵

| 能力 | 旧项目价值 | Sub2API 当前状态 | 缺口判断 | 建议 |
| --- | --- | --- | --- | --- |
| Claude 错误体 schema | 避免 Claude CLI 把 OpenAI 错误 JSON 显示成 `Failed to parse JSON` | `/v1/messages` 路径已有 `writeAnthropicError`，部分错误路径是 Claude 风格 | 需要补齐首包前、鉴权失败、上游 request error、非 failover 4xx 的端到端测试 | P1，先补测试确认所有错误路径 |
| 缺失 terminal event | 防止 `stream closed before response.completed` 被误判成功 | Streaming 路径遇到 `[DONE]` 或 EOF 且无 terminal 会返回 `missing terminal event`；buffered 路径无 terminal 会返回 502 | 已有部分测试；还需要覆盖“已写出 text/tool_use 后 EOF”的 `/v1/messages` 真实链路 | P1，先补回归，确认不会记录为成功用量 |
| SSE 组帧 | 避免半截 JSON、拆开的 event/data 被错误转发 | Sub2API 有 `openAICompatSSEFrameParser` | 需要针对 Anthropic 转换路径补多行 `event:` + `data:`、跨行 payload、末尾残帧测试 | P1，测试优先 |
| 200/SSE 错误帧 | 旧项目发现上游可能 HTTP 200 后第一帧返回 `{"error":...}` | Sub2API 当前会尝试按 `ResponsesStreamEvent` 解析；`type` 为空时大概率被忽略，最后才变成 missing terminal | 还没有等价的“错误帧分类 + failover/冷却”能力 | P1，先用测试复现并定义目标行为 |
| first activity 判定 | 只有真实内容/tool delta 才算首个有效输出，协议壳不算 | Sub2API `firstTokenMs` 以第一帧 payload 计，不区分协议壳与真实内容 | 对指标有偏差；对 failover 窗口是否有影响还要看调度层 | P2，先测试记录，不急着改 |
| `response.failed` / `response.incomplete` | 旧项目强调非成功终止不能被当普通成功 | Sub2API 转换器会把 `response.failed` 转成 `message_delta/end_turn + message_stop`；`incomplete/max_output_tokens` 转 `max_tokens` | `response.failed` 当前语义偏乐观，可能让客户端误以为成功结束 | P1，先补测试明确现状，再决定是否改 |
| tool-call 状态机 | 避免空 input、半截 JSON、错误 stop_reason | Sub2API 已有 `function_call_arguments.done` 无 delta 兜底、Read `pages:""` 清理、tool_use stop_reason 保持 | 覆盖较好；还需增加“tool_use 已开但 done/terminal 缺失”的端到端测试 | P1，测试补齐即可 |
| `response.output_item.done` message fallback | 旧项目修过“完整正文只在 output_item.done.item.content 里”导致客户端无 text block | Sub2API buffered 路径有 accumulator supplement；streaming `resToAnthHandleOutputItemDone` 对 message item 当前没有 text fallback | 流式路径存在缺口 | P1，先补 converter 单测暴露 |
| unknown `tool_result.content[]` 保留 | 旧项目修过未知 block 被静默丢弃导致上下文失真 | Sub2API `convertToolResultOutput` 目前只保留 text/image，未知 block 会导致输出退化为 `(empty)` | 对 Claude Code 非标准工具结果有风险 | P1，先补请求转换测试，再决定降级为 JSON 文本 |
| Web search 客户端分流 | CLI 需要可见搜索进度，VSCode 不适合普通文本 fake thinking | 本地已实现方案一：CLI 合成搜索文本，VSCode 发 thinking progress，抑制 reasoning summary | 需要真实 `cc1` TTY 和 VSCode/Codex VSCode 回归 | P1，进入黑盒门禁后执行 |
| reasoning summary 暴露 | 旧项目最终关闭 fake thinking，避免污染展示和 transcript | 本地方案一已对 Claude CLI / VSCode / Codex VSCode 抑制 post-hoc reasoning summary | 需要确认未知客户端是否仍要保留原行为 | P2，先保持当前兼容选项策略 |
| thinking/signature 清洗 | 避免历史 thinking signature 导致 Anthropic 上游 400 | Sub2API Claude -> OpenAI 路径会忽略 thinking 输入；其他 Antigravity/Gemini 路径有独立签名处理 | 对 Claude -> GPT 主路径不急；如果后续重放 thinking，需要重新评估 | P3，观察后再迁 |
| `prompt_cache_key` / `previous_response_id` 粘性 | 让多轮会话固定 cache/session，降低跳转和缓存断裂 | Sub2API 已有 `prompt_cache_key` 注入、digest 复用、`previous_response_id` 绑定与失效重试 | 需要真实多轮黑盒验证“body 变化时仍粘住同账号/同 response chain” | P1，黑盒门禁项 |
| raw SSE 诊断 | 旧项目用 raw SSE footer 区分 EOF、terminal、incomplete、scanner error | Sub2API 有服务日志和 ops 错误，但没有等价轻量 raw SSE footer 诊断 | 不是先决条件，但线上疑难会很有用 | P2，后续加最小诊断索引 |
| worker/provider 冷却架构 | 旧项目 worker 池的 failover/cooldown 经验很重 | Sub2API 调度模型不同，不应直接迁移 | 直接搬会复杂化 | 暂不迁移 |

## 测试缺口清单

### P1：必须先覆盖

1. `ForwardAsAnthropic` 流式路径：上游只返回 `data: [DONE]`，没有 terminal event。
2. `ForwardAsAnthropic` 流式路径：`response.created + output_text.delta + EOF`，没有 terminal event。
3. `ForwardAsAnthropic` 流式路径：`response.output_item.added(function_call) + partial arguments + EOF`。
4. `ResponsesEventToAnthropicEvents`：`response.output_item.done` 的 item 是 `message` 且带完整 `content/output_text`，但没有任何 `output_text.delta`。
5. `ForwardAsAnthropic` / buffered：HTTP 200 SSE 第一帧是 `{"error":{"message":"empty_stream..."}}`。
6. `response.failed` before output。
7. `AnthropicToResponses`：`tool_result.content[]` 包含未知 block 类型。
8. 多轮 `prompt_cache_key` / `previous_response_id`：同一 Claude TTY session 的第二轮请求 body 变化。

### P2：建议覆盖

1. 多行 SSE 组帧：`event:` 与 `data:` 分行、多个 `data:` 拼接、末尾残帧。
2. `response.incomplete` 非 `max_output_tokens` reason。
3. `response.completed` 缺 usage 时 usage 默认值。
4. `web_search_call added` 有 action query、done 缺 query 时的 fallback query。
5. Claude VSCode / Codex VSCode 搜索进度不产生普通 text block。
6. 未知客户端保留 reasoning summary 的兼容边界。

### P3：观察后再覆盖

1. Claude 历史 thinking signature 清洗。
2. 搜索请求自动压 reasoning effort 到 medium。
3. raw SSE 诊断 footer / index。
4. worker/provider 级冷却策略。

## 当前结论

1. Sub2API 不需要迁移旧项目整体架构。
2. 最有价值的迁移目标是“错误边界 + 流式状态机 + 上下文不丢失 + 客户端分流”。
3. P1 测试已补齐，并已根据用户“继续”的指令修复其中 4 个业务缺口。
4. 本小节是 2026-05-27 阶段结论；后续发布状态见下方 2026-06-01 复核。

## 2026-05-29 复核结论

当前不能说“矩阵里的所有项目都已修完”。更准确的状态是：

- P1 协议稳定性项已经基本落地：缺 terminal、200/SSE error frame、`response.failed`、message-only `output_item.done`、unknown `tool_result`、WebSearch 客户端分流、continuation summary 防泄漏等已有修复或回归测试。
- WebSearch 来源/链接可见性已本地补齐：Claude -> Responses 请求追加 `web_search_call.action.sources`，响应侧保留 `sources/url/annotations` 并转成 Anthropic `web_search_result` / citations。
- P2/P3 仍是后续任务：first activity 精准指标、raw SSE 轻量诊断索引、更多 fake upstream 黑盒矩阵、worker/provider 冷却架构不直接迁移。
- 原生 Claude 路径未发现被当前 Claude->GPT 改动污染：原生 `/v1/messages` 仍走 `GatewayHandler.Messages` 的平台分流；OpenAI 分组启用 `/v1/messages dispatch` 后才进入 `OpenAIGatewayService.ForwardAsAnthropic`。
- 已新增 `backend/internal/pkg/claudegptcompat`，把 Claude->GPT 专用的客户端识别、WebSearch query 清洗、synthetic/thinking 搜索进度、sources/url/citation 辅助抽成库；`apicompat` 只保留协议转换编排。

## 2026-06-01 发布后复核

已上线生产镜像 `zhangtaylor985/sub2api:main-19663655`，本地真实 Codex auth file 黑盒、Go 全量测试、前端 lint/typecheck/build、生产 canary 与正式 `/v1/messages` smoke 均通过。当前状态可以认为 P1 协议稳定性和 WebSearch 来源保留已达到一次生产发布标准，但矩阵仍不是“全部完成”。

下一阶段优先级：

1. **真实多轮黑盒扩展**：继续用本地沙盒做“同一 TTY、多轮 body 变化、WebSearch 后继续追问、长上下文增长”的 account/response-chain 复核；生产只做低风险 canary。
2. **fake upstream 矩阵**：为 `web_search_call added/done`、sources/url/annotations、200/SSE error frame、split delta、message-only terminal、unknown tool_result 做确定性 fake upstream 测试，减少依赖真实模型是否刚好触发某个事件。
3. **观测与诊断**：补 raw SSE 轻量诊断索引和 first activity 精准指标，方便区分协议壳、真实 token、tool delta、上游错误和客户端取消。
4. **长上下文窗口治理**：发布观察中仍有真实用户请求触发上游 context-window 502；这不是本次 WebSearch 修复导致，但需要后续从模型窗口、预估 token、错误提示和路由策略四个角度治理。
5. **OpenAI endpoint 的 Claude 模型误用治理**：生产日志显示仍有 `/v1/chat/completions` 直接请求 `claude-opus-*` 并被 Codex 上游拒绝；这不是 Claude `/v1/messages` dispatch 路径，需要单独决定是否在 OpenAI endpoint 也做 Claude 模型映射或返回更清晰错误。

已完成项：

- **生产 Opus 映射一致性**：2026-06-01 已收敛 6 个 OpenAI dispatch 分组，Opus family、`claude-opus-4-6`、`claude-opus-4-7`、`claude-opus-4-8` 均映射到 `gpt-5.5`；生产 direct smoke 和 usage log 均确认 4-6/4-7/4-8 为 `→gpt-5.5`。记录见 `docs/prod_opus_gpt55_mapping_20260601_CN.md`。
- **多轮 session 粘性基础修复**：2026-06-01 已修复 OpenAI `/v1/messages` dispatch 的 session 信号优先级，从“content fallback 先于 metadata”改为“显式 session > Claude `metadata.user_id` > content fallback”；这能避免 compact/resume 改写首轮内容时同一 Claude session 生成不同账号粘性键。已补单测并通过后端全量 `go test ./...`。

## 本轮已补测试与修复

第一阶段按“不改业务逻辑”的边界补了两类测试：

- 已有保护回归：
  - `TestForwardAsAnthropic_StreamEOFAfterPartialTextReturnsMissingTerminalError`
  - `TestForwardAsAnthropic_StreamEOFAfterOpenToolUseReturnsMissingTerminalError`
- 缺口 characterization，随后已翻转为目标行为：
  - `TestStreamingMessageOutputItemDoneWithoutDeltaEmitsTextFallback`
  - `TestAnthropicToResponses_ToolResultUnknownBlockPreservedAsJSONText`
  - `TestForwardAsAnthropic_BufferedSSEErrorFrameReturnsStreamError`
  - `TestForwardAsAnthropic_ResponseFailedBeforeOutputReturnsStreamError`

第二阶段已修复：

- streaming `response.output_item.done` 携带完整 message content、但没有 text delta 时，补出 Anthropic text block。
- Claude `tool_result.content[]` 中未知 block 不再丢弃，改为压缩 JSON 文本写入 `function_call_output`。
- HTTP 200 SSE 内嵌 `{"error":...}` 现在识别为 upstream SSE error；buffered 路径返回 Claude 风格错误，streaming 路径发送 Anthropic SSE error event。
- `response.failed` 不再被转换为普通成功 `message_stop`；buffered 路径返回 Claude 风格错误，streaming 路径发送 Anthropic SSE error event。

验证命令：

```bash
cd backend
NO_PROXY=127.0.0.1,localhost,::1 no_proxy=127.0.0.1,localhost,::1 HTTP_PROXY= HTTPS_PROXY= http_proxy= https_proxy= go test ./internal/pkg/apicompat
NO_PROXY=127.0.0.1,localhost,::1 no_proxy=127.0.0.1,localhost,::1 HTTP_PROXY= HTTPS_PROXY= http_proxy= https_proxy= go test ./internal/service
NO_PROXY=127.0.0.1,localhost,::1 no_proxy=127.0.0.1,localhost,::1 HTTP_PROXY= HTTPS_PROXY= http_proxy= https_proxy= go test ./internal/handler -run 'OpenAIGateway|Messages|Gateway'
NO_PROXY=127.0.0.1,localhost,::1 no_proxy=127.0.0.1,localhost,::1 HTTP_PROXY= HTTPS_PROXY= http_proxy= https_proxy= go test ./...
```

结果：

- `github.com/Wei-Shaw/sub2api/internal/pkg/apicompat` 通过。
- `github.com/Wei-Shaw/sub2api/internal/service` 通过。
- `github.com/Wei-Shaw/sub2api/internal/handler` 定向回归通过。
- `backend` 全量 `go test ./...` 通过。
