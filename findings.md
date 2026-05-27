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

## 待确认问题

- 线上镜像更新/构建/回滚流程。

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
- OpenAI Responses -> Anthropic 转换在 `backend/internal/pkg/apicompat/responses_to_anthropic.go`：
  - 非流式 `web_search_call` 当前生成 `server_tool_use` 和空的 `web_search_tool_result`。
  - 流式 `response.output_item.done` 且 item 为 `web_search_call` 时，也生成 `server_tool_use` 和空的 `web_search_tool_result`。
  - 当前没有按 Claude CLI / VSCode 客户端定制搜索进度展示。
- 参考项目 CLIProxyAPI 的方案一核心：
  - 根据 `User-Agent` / `Originator` 区分 Claude CLI、Claude VSCode、Codex VSCode。
  - Claude CLI：遇到真实 OpenAI `web_search_call` 时补合成文本块，形如 `Searching the web.` / `Searched: <query>` 加 `<tool_call>` 标记。
  - VSCode/Codex VSCode：遇到真实 `web_search_call` 时补简短 `thinking` 进度，例如 `Searching the web for: <query>`。
  - 对这些客户端 suppress post-hoc reasoning summary，避免搜索完成后才把总结伪装成实时 thinking。
