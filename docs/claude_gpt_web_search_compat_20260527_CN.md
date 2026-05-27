# Sub2API Claude -> GPT Web Search 兼容记录（2026-05-27）

## 背景

Claude 客户端通过 Sub2API 的 OpenAI 分组访问 GPT/Codex 模型时，`web_search_20250305` 已经能映射到 OpenAI Responses 的原生 `web_search` 工具，但 Claude CLI / VSCode 对 OpenAI `web_search_call` 的展示不够友好：流式过程中能看到 `server_tool_use` / `web_search_tool_result`，但搜索结果块为空，客户端容易表现成“搜了但没有清晰进度”。

用户明确选择只做方案一：参考当前 CLIProxyAPI 的 Claude -> GPT Web search 兼容方式，不默认改用 Brave/Tavily 模拟，也不先依赖 OpenAI 返回里的引用信息做完整转换。

## 线上接管信息

- SSH：`ssh root@204.168.245.138`
- 主机名：`PG-01`
- 部署目录：`/root/cliapp/sub2api`
- 运行方式：Docker Compose
- Compose：`/root/cliapp/sub2api/docker-compose.yml`
- 容器：
  - `sub2api`：`weishaw/sub2api:latest`，`0.0.0.0:8080 -> 8080/tcp`
  - `sub2api-postgres`：`postgres:18-alpine`
  - `sub2api-redis`：`redis:8-alpine`
- 反代：
  - Caddy active
  - `cc.claudepool.com` -> `127.0.0.1:8080`
  - `/management.html` -> `https://admin.claudepool.com/`
  - Nginx inactive
- 健康检查：`curl -fsS http://127.0.0.1:8080/health`

注意：线上应用代码来自 Docker 镜像，宿主机 `/root/cliapp/sub2api` 当前不是源码 Git 工作区。后续发布应明确镜像构建、推送和回滚流程，不建议直接修改容器内文件。

## Claude -> GPT 模型映射关系

Sub2API 当前有两层映射。

第一层是 OpenAI 分组的 `/v1/messages` 调度映射：

- 表字段：`groups.allow_messages_dispatch`
- 表字段：`groups.messages_dispatch_model_config`
- 代码入口：`Group.ResolveMessagesDispatchModel`
- Handler 入口：`OpenAIGatewayHandler.Messages`
- 优先级：
  - `exact_model_mappings`
  - Claude family 映射：Opus / Sonnet / Haiku
  - 代码默认值
- 代码默认值：
  - Opus -> `gpt-5.4`
  - Sonnet -> `gpt-5.3-codex`
  - Haiku -> `gpt-5.4-mini`

第二层是 OpenAI/Codex 账号侧模型映射：

- 表字段：`accounts.credentials.model_mapping`
- 代码入口：`Account.GetMappedModel` / `Account.ResolveMappedModel`
- 支持精确映射和 `*` 通配符，最长匹配优先。
- 账号级 mapping 既是支持模型白名单，也是最终上游模型改写规则。

实际链路：

1. Claude 请求进入 `/v1/messages`。
2. Handler 校验 API Key 所属 OpenAI group 是否 `allow_messages_dispatch=true`。
3. group 根据请求模型得到 `defaultMappedModel`。
4. `ForwardAsAnthropic` 调用 `resolveOpenAIForwardModel(account, normalizedModel, defaultMappedModel)`。
5. 如果账号级 `model_mapping` 命中，优先使用账号映射结果。
6. 如果账号映射未命中，且请求模型是 Claude family，则使用 group 给出的 `defaultMappedModel`。
7. 最后 `normalizeOpenAIModelForUpstream` 得到真正请求上游的模型。

线上当前观察：

- 所有 OpenAI 分组已开启 `allow_messages_dispatch=true`。
- OpenAI 分组 family 映射为 Opus -> `gpt-5.4`，Sonnet -> `gpt-5.3-codex`，Haiku -> `gpt-5.4-mini`。
- `Codex Base` 与 `CP Legacy ungrouped` 当前没有 active linked account；其他 OpenAI 分组有 active accounts。
- OpenAI OAuth accounts 账号侧 mapping 包含 Claude 通配和 GPT passthrough 规则，例如 `claude-* -> gpt-5.3-codex`、Opus 精确映射到 `gpt-5.5`、Sonnet 精确映射到 `gpt-5.3-codex`。

## 本次方案一改动

改动目标：保留 OpenAI 原生 `web_search`，只改善 Claude 客户端兼容展示。

主要代码：

- `backend/internal/pkg/apicompat/anthropic_client_compat.go`
  - 新增 Claude/Codex 客户端识别。
  - 新增搜索 query 推断。
  - 新增 Claude CLI 合成 `<tool_call>` 文本构造。
  - 新增 VSCode thinking 搜索进度构造。
- `backend/internal/pkg/apicompat/responses_to_anthropic.go`
  - 新增 `ResponsesToAnthropicWithOptions`。
  - 流式 state 支持客户端兼容选项。
  - Claude CLI：`web_search_call` added/done 时补 `Searching the web.` / `Searched: <query>` 文本块。
  - Claude VSCode / Codex VSCode：`web_search_call` added 时补简短 thinking 进度。
  - 对 Claude CLI / VSCode / Codex VSCode 抑制 post-hoc reasoning summary thinking。
  - 保留原有 `server_tool_use` + 空 `web_search_tool_result`，不改变 OpenAI 原生搜索链路。
- `backend/internal/service/openai_gateway_messages.go`
  - 从请求 headers 检测客户端类型。
  - 从原始 Anthropic body 推断 web search fallback query。
  - 将兼容选项传入流式和非流式 Responses -> Anthropic 转换。
- `backend/internal/pkg/apicompat/anthropic_responses_test.go`
  - 覆盖 Claude CLI 搜索进度文本。
  - 覆盖 VSCode thinking 搜索进度。
  - 覆盖 reasoning summary 抑制。
  - 覆盖非流式 CLI 搜索文本补齐。

## 验证

在 `backend/` 目录执行：

```bash
go test ./internal/pkg/apicompat
go test ./internal/service -run 'TestForwardAsAnthropic|TestNormalizeOpenAIMessagesDispatchModelConfig|TestResolveOpenAIForwardModel|TestOpenAI'
go test ./internal/handler -run 'OpenAIGateway|Messages|Gateway'
```

结果：

- `github.com/Wei-Shaw/sub2api/internal/pkg/apicompat` 通过。
- `github.com/Wei-Shaw/sub2api/internal/service` 定向测试通过。
- `github.com/Wei-Shaw/sub2api/internal/handler` 定向测试通过。

## 剩余事项

- 明确 Sub2API 镜像发布流程：本地源码如何构建镜像、推送到哪个 registry、线上如何拉取和回滚。
- 如需上线本次代码，建议先确定镜像 tag，不要继续使用不可追溯的 `latest` 作为唯一发布标识。
- 若未来仍希望看到真实搜索结果列表，可单独评估 Brave/Tavily emulation fallback，但不应作为本次默认路径。
