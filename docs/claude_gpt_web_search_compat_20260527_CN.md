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
  - 覆盖 OpenAI `web_search_call` 缺失 `action.query` 时使用 request fallback query。
  - 覆盖中文 `请使用 web search 查询 ...，并...` 查询提取。
  - 覆盖 VSCode thinking 搜索进度。
  - 覆盖 reasoning summary 抑制。
  - 覆盖非流式 CLI 搜索文本补齐。

## 2026-05-27 线上 WebSearch 路径纠偏

线上截图显示 Claude Code/VSCode 仍在展示 `Web Search("...")` 与 `Found 0 results`，并且请求耗时约 4 分钟。复查后确认这是另一条入口：Claude Code 提供的是 `name:"WebSearch"` 的客户端 function tool，不是 Anthropic server tool `web_search_20250305`。当它被当成普通 function 透传给 GPT 时，GPT 会调用客户端 `WebSearch`，于是实际执行的是 Claude Code 原生搜索，而不是 OpenAI Responses 原生 `web_search`。

追加修复：

- `convertAnthropicToolsToResponses` 同时识别 `web_search_20250305` 和 Claude Code `WebSearch`，统一映射为 OpenAI Responses `{"type":"web_search"}`。
- 同一请求里如果同时存在 server web_search 与客户端 WebSearch，只保留一个 OpenAI `web_search`，避免重复工具。
- `tool_choice: {"type":"tool","name":"WebSearch"}` 会映射为 `{"type":"web_search"}`，避免强制调用不存在的普通 function。

本地黑盒验证：

- 本地启动 Sub2API 于 `127.0.0.1:8080`，使用本机生产库副本和本地 Docker Postgres/Redis，验证后已停止。
- `cc1`/Claude Code `stream-json --include-partial-messages` 样本显示 `Searching the web.`、`server_tool_use name=web_search`、`web_search_tool_result`、`Searched:` 与最终中文回答；未出现客户端 `tool_use name=WebSearch`。
- 真实 TTY 样本显示 `Searching the web.` / `Searched: ...` 并返回 OpenAI 官网标题；没有复现 `Web Search("...") Found 0 results`。

上线记录：

- 修复提交：`77dfaf2b fix(apicompat): route Claude Code WebSearch to native web search`。
- 生产镜像：`zhangtaylor985/sub2api:main-77dfaf2b`。
- 线上 Compose 备份：`/root/cliapp/sub2api/docker-compose.yml.bak.20260527T134952Z`。
- 上一版应用镜像：`zhangtaylor985/sub2api:main-decdc6d0`。
- 生产公开入口 direct `/v1/messages` 使用 `tool_choice: WebSearch` 验证通过：返回 `server_tool_use name=web_search`、`web_search_tool_result`、正文与 `message_stop`。

## 验证

在 `backend/` 目录执行：

```bash
go test ./internal/pkg/apicompat
go test ./internal/service -run 'TestForwardAsAnthropic|TestNormalizeOpenAIMessagesDispatchModelConfig|TestResolveOpenAIForwardModel|TestOpenAI'
go test ./internal/handler -run 'OpenAIGateway|Messages|Gateway'
go test ./...
```

结果：

- `github.com/Wei-Shaw/sub2api/internal/pkg/apicompat` 通过。
- `github.com/Wei-Shaw/sub2api/internal/service` 定向测试通过。
- `github.com/Wei-Shaw/sub2api/internal/handler` 定向测试通过。
- `go test ./...` 通过。

## 上线记录

- 发布提交：
  - `ee377355`：方案一主体实现与文档。
  - `3e8f76bd`：修复 OpenAI `web_search_call` 没有 `action.query` 时 query 为空的问题。
- 发布 tag：`v0.1.131-claude-websearch.2`
- 生产镜像：`zhangtaylor985/sub2api:v0.1.131-claude-websearch.2`
- 线上 Compose 备份：`/root/cliapp/sub2api/docker-compose.yml.bak.20260527T063700Z`
- 正式容器：`sub2api` 已切到新镜像，`/health` 返回 `{"status":"ok"}`。
- 未迁移数据层：`sub2api-postgres` 与 `sub2api-redis` 仍由 Docker Compose 管理。

上线前后黑盒验证：

- canary 容器 `sub2api-canary-websearch` 曾在宿主机 `127.0.0.1:18080` 验证，验证后已删除。
- `cc1`/Claude Code 非交互 smoke 命中 canary，返回 `SUB2API_CANARY_OK`。
- canary direct `/v1/messages` WebSearch 验证通过：`Searching the web`、`server_tool_use.input.query`、`Searched` 三处均保留 `OpenAI official website homepage title`。
- `cc1`/Claude Code WebSearch 验证通过，禁用 Bash 后无额外通知工具干扰，最终 result 为 success。
- `cc1`/Claude Code 真实 TTY 连续两轮验证通过：同一 PTY 内返回 `TTY_ONE`、`TTY_TWO`。
- 生产域名 `https://cc.claudepool.com/v1/messages` direct WebSearch 验证通过，最终正文正常返回。
- 生产域名 `cc1` smoke 验证通过，返回 `SUB2API_PROD_OK`。

发布观察：

- 新容器启动和健康检查通过，没有发现 panic、terminal-missing 或 WebSearch 相关错误。
- 观察到若干既有业务侧 `glm-4.6 no available accounts` 和 `/v1/messages/count_tokens` 404，不属于本次 WebSearch 兼容改动；后续可单独排查路由能力和 count_tokens 兼容。

## 剩余事项

- 将线上 GitHub clone/pull 链路修好：本次生产机 HTTPS clone 曾长时间卡住并 early EOF，最终使用本地 `git archive` 传固定 tag 源码构建。
- 将 Postgres/Redis 宿主机化作为独立迁移项目处理，不并入本次应用协议修复。
- 若未来仍希望看到真实搜索结果列表，可单独评估 Brave/Tavily emulation fallback，但不应作为本次默认路径。
