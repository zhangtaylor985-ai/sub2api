# Sub2API Claude -> GPT Web Search 兼容发现记录

## 已确认事实

- 本地 Sub2API 源码路径：`/Users/taylor/sdk/sub2api`。
- 当前分支：`main`。
- 初始工作区：干净。
- 本次任务用户选择：只做“方案一”，即参考当前 CLIProxyAPI 的 Claude -> GPT Web search 兼容方式，不默认采用 Brave/Tavily 模拟，也不先押 OpenAI 搜索引用完整转换。
- 线上 Sub2API SSH 入口：`root@204.168.245.138`。
- 线上主机名：`PG-01`。
- 线上 Sub2API 目录：`/root/cliapp/sub2api`。
- 线上运行方式：Docker Compose，容器包括 `sub2api`、`sub2api-postgres`、`sub2api-redis`。
- 线上服务镜像：`weishaw/sub2api:latest`，`sub2api` 容器将 `8080/tcp` 暴露到宿主机 `0.0.0.0:8080`。
- 线上 `/root/cliapp/sub2api` 当前未确认是 Git 工作区，初步检查未输出 Git 状态。
- 线上 Caddy active，`cc.claudepool.com` 反代到 `127.0.0.1:8080`；Nginx inactive。

## 已确认发布流程

- 线上生产机可用 `root` 用户的 GitHub SSH key 拉取 `git@github.com:zhangtaylor985-ai/sub2api.git`。
- 生产源码目录：`/root/cliapp/sub2api-src`。
- 本次生产镜像在生产机使用完整根目录 Dockerfile 构建：`zhangtaylor985/sub2api:main-decdc6d0`。
- 生产 Compose 只替换 app 容器 `sub2api`，Postgres/Redis 容器不动。
- 本次 Compose 备份：`/root/cliapp/sub2api/docker-compose.yml.bak.20260527T105427Z`。
- 回滚优先切回上一版 app 镜像 `zhangtaylor985/sub2api:v0.1.131-claude-websearch.2` 并执行 `docker compose up -d sub2api`。

## 黑盒验证记录

- 2026-05-27：本轮未打 tag，使用本地 `HEAD` 打包上传到生产机并启动临时 canary 容器验证。
- canary 只监听远端 `127.0.0.1:18080`，本机通过 SSH 隧道访问 `127.0.0.1:18080`；正式线上 `sub2api:8080` 未被替换。
- Claude CLI `-p` smoke、`stream-json` WebSearch、真实 TTY 多轮均能经 `/v1/messages` 完成。
- WebSearch 覆盖到客户端 `WebSearch` 工具调用、工具结果回传、继续回答和来源链接输出。
- 固定字符串 TTY 测试会让 Claude Code 的会话标题解析在 debug log 中记录非致命 JSON parse 噪音；自然语言 TTY prompt 未复现。
- 用户要求后续黑盒优先本地启动 Sub2API 并本地授权 Codex auth file；远端 canary 仅用于需要生产同配置验证的场景。

## Claude -> GPT 稳定性迁移评估

- 2026-05-27：已完成第一轮只读评估，详见 `docs/claude_gpt_stability_migration_matrix_20260527_CN.md`。
- 迁移原则：不整包迁移 CLIProxyAPI 架构，只迁移稳定性经验、测试矩阵和小范围兼容边界。
- Sub2API 已有能力：
  - `/v1/messages` 到 OpenAI Responses 的模型映射和账号调度。
  - `prompt_cache_key`、digest 复用、`previous_response_id` 绑定与失效重试。
  - 缺失 terminal event 的基本错误识别。
  - tool-call arguments done 兜底、Read `pages:""` 清理和 tool_use stop_reason 保持。
  - 原生 `web_search` 映射与本地方案一客户端分流实现。
- 主要缺口候选：
  - HTTP 200 SSE 内嵌 `{"error":...}` 错误帧未明确分类。
  - `response.failed` 当前可能被转换成普通 `end_turn` 成功结束。
  - streaming `response.output_item.done` 中直接携带完整 message content 时，缺少 text fallback。
  - Claude `tool_result.content[]` 中未知 block 目前可能退化为 `(empty)`，有上下文丢失风险。
  - 已有 terminal/EOF 检测需要补“已写出部分 text/tool_use 后断流”的端到端测试。
- 已补测试并完成业务修复：
  - partial text 后 EOF -> `missing terminal event`。
  - open tool_use 后 EOF -> `missing terminal event`。
  - `output_item.done` message-only 现在会补 text fallback。
  - unknown `tool_result` block 现在保留为压缩 JSON 文本。
  - 200/SSE error frame 现在返回 upstream stream error，不再退化为 terminal missing。
  - `response.failed` before output 现在返回 stream error，不再伪装为成功流。
- 验证通过：`go test ./internal/pkg/apicompat`、`go test ./internal/service`、`go test ./internal/handler -run 'OpenAIGateway|Messages|Gateway'`、`go test ./...`。

## 宿主机化迁移判断

- 结论：长期建议把 Sub2API 应用、Postgres、Redis 都迁到宿主机 systemd 管理，但不建议和本次 Web search 兼容修复混成同一次上线。
- 原因：本次改动是应用协议兼容，回滚可以做到切回旧镜像；Postgres/Redis 宿主机化涉及数据目录、备份恢复、端口监听、认证、连接配置、systemd、健康检查与回滚窗口，风险级别明显更高。
- 性能判断：宿主机 Postgres/Redis 会减少 Docker 网络/volume 的一点开销，排查与监控也更直观；但对当前 API 网关类请求，主要延迟通常来自上游模型与流式链路，DB/Redis 宿主机化不应作为当前 Web search 兼容修复的阻塞项。
- 建议路径：当前发布先保持 Postgres/Redis Docker，不碰数据层；下一阶段单独做“数据层宿主机化迁移”，包含全量备份、恢复演练、只读校验、短暂停写切换、健康检查和明确 rollback。

## 模型映射关系

- 分组层入口：`backend/internal/handler/openai_gateway_handler.go` 的 `OpenAIGatewayHandler.Messages`。
  - 只有 API Key 所属 group 是 OpenAI 且 `allow_messages_dispatch=true` 时，才允许 Claude `/v1/messages` 调度。
  - 请求模型 `reqModel` 先经过 `resolveOpenAIMessagesDispatchMappedModel(apiKey, reqModel)`，实际调用 `apiKey.Group.ResolveMessagesDispatchModel(reqModel)`。
- 分组层字段：`groups.messages_dispatch_model_config`，Go 类型为 `domain.OpenAIMessagesDispatchModelConfig`。
  - `exact_model_mappings` 精确映射优先。
  - 然后按 Claude family 分流：`opus_mapped_model`、`sonnet_mapped_model`、`haiku_mapped_model`。
  - 未配置时使用代码默认值：Opus -> `gpt-5.4`，Sonnet -> `gpt-5.3-codex`，Haiku -> `gpt-5.4-mini`。
- 账号层入口：`backend/internal/service/openai_gateway_messages.go` 的 `ForwardAsAnthropic`。
  - `billingModel := resolveOpenAIForwardModel(account, normalizedModel, defaultMappedModel)`。
  - `defaultMappedModel` 来自分组层映射，只服务 `/v1/messages` 的 Claude 系列显式调度。
  - `resolveOpenAIForwardModel` 会先查账号 `credentials.model_mapping`；若账号映射未命中且请求是 Claude family，才使用分组层 `defaultMappedModel`。
  - 随后 `normalizeOpenAIModelForUpstream(account, billingModel)` 得到真正上游请求模型。
- 账号层字段：`accounts.credentials.model_mapping`。
  - 支持精确和 `*` 通配符，最长匹配优先。
  - 既是账号可服务模型白名单，也是账号级模型改写规则。
- 线上当前配置：
  - 所有 OpenAI 分组 `allow_messages_dispatch=true`。
  - OpenAI 分组的 family 映射当前为 Opus -> `gpt-5.4`，Sonnet -> `gpt-5.3-codex`，Haiku -> `gpt-5.4-mini`。
  - `Codex Base` 与 `CP Legacy ungrouped` 当前没有 active linked account；其余 OpenAI 分组有 active accounts。
  - OpenAI OAuth accounts 的账号级 mapping 中同时存在 `claude-* -> gpt-5.3-codex`、`claude-opus-4-6/4-7 -> gpt-5.5`、`claude-sonnet-4-6 -> gpt-5.3-codex`，以及 GPT 目标模型的 passthrough mapping。

## Web Search 兼容关系

- Claude `/v1/messages` -> OpenAI Responses 转换在 `backend/internal/pkg/apicompat/anthropic_to_responses.go`：Anthropic `web_search_20250305` 会映射为 OpenAI Responses `{"type":"web_search"}`。
- 2026-05-27 线上截图排查补充：Claude Code/VSCode 常见的搜索工具不是 server tool，而是 `name:"WebSearch"` 的客户端 function tool。当前线上 `convertAnthropicToolsToResponses` 只识别 `type` 前缀为 `web_search` 的工具，因此这类请求会把 `WebSearch` 作为普通 function 交给 GPT；GPT 调用后由 Claude Code 客户端执行原生 Web Search，界面会显示 `Web Search("...")` 和 `Found 0 results`，没有进入 OpenAI 原生 `web_search_call` 兼容层。
- 修复方向：在 Claude -> GPT 入口处将 Claude Code `WebSearch` 工具也映射为 OpenAI Responses `{"type":"web_search"}`，并避免同时保留普通 `WebSearch` function；后续 OpenAI 返回的 `web_search_call` 继续走既有 `server_tool_use` / `web_search_tool_result` / CLI/VSCode 进度兼容。
- OpenAI Responses -> Anthropic 转换在 `backend/internal/pkg/apicompat/responses_to_anthropic.go`：
  - 非流式 `web_search_call` 当前生成 `server_tool_use` 和空的 `web_search_tool_result`。
  - 流式 `response.output_item.done` 且 item 为 `web_search_call` 时，也生成 `server_tool_use` 和空的 `web_search_tool_result`。
  - 当前没有按 Claude CLI / VSCode 客户端定制搜索进度展示。
- 参考项目 CLIProxyAPI 的方案一核心：
  - 根据 `User-Agent` / `Originator` 区分 Claude CLI、Claude VSCode、Codex VSCode。
  - Claude CLI：遇到真实 OpenAI `web_search_call` 时补合成文本块，形如 `Searching the web.` / `Searched: <query>` 加 `<tool_call>` 标记。
  - VSCode/Codex VSCode：遇到真实 `web_search_call` 时补简短 `thinking` 进度，例如 `Searching the web for: <query>`。
  - 对这些客户端 suppress post-hoc reasoning summary，避免搜索完成后才把总结伪装成实时 thinking。
