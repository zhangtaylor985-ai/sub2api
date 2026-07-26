# Sub2API Admin API Key 策略管理发现记录

## 2026-05-28 当前任务事实

- 业务目标：管理员托管 API Key 策略，普通用户只是隔离容器。
- 推荐模型：一 API Key 一用户；`users.concurrency` / `users.rpm_limit` 做用户级隔离，`api_keys.expires_at` / `quota` / `rate_limit_*` 做 key 级策略。
- 线上检查已确认：legacy API Key 的 `api_keys.expires_at` 没有丢；`users` 表本身没有 `expires_at`。
- 后端已有用户侧 `PUT /api/v1/api-keys/:id` 能更新 `quota`、`expires_at`、`rate_limit_5h/1d/7d`、重置用量等字段。
- 现有 admin 侧 `PUT /api/v1/admin/api-keys/:id` 只支持改分组和重置限速用量，缺少过期时间/额度/状态等策略字段。
- 现有 admin UI `UserApiKeysModal.vue` 只能在用户 API Key 弹窗里查看 key 和改分组，没有策略编辑入口。
- 本次实现后，admin 侧同一个接口支持 `status`、`quota`、`expires_at`、`reset_quota`、`rate_limit_5h/1d/7d`、`reset_rate_limit_usage`。
- 为避免部分更新，handler 会先解析/校验策略字段，再执行分组更新。
- 2026-06-01：用户确认回切 Sub2API；线上 Sub2API app 容器在 `8080`，但 `cc.claudepool.com` 当前 Caddy 反代到 `127.0.0.1:8317`，即 CLIProxyAPI。
- 2026-06-01：线上 Sub2API `api_keys` 当前无 `concurrency` 字段；已有 `quota`、`quota_used`、`expires_at`、`rate_limit_5h/1d/7d`、`usage_5h/1d/7d` 与窗口字段。
- 2026-06-01：当前并发限流使用 `AuthSubject.UserID` 和 `AuthSubject.Concurrency`，middleware 从 `apiKey.User.Concurrency` 填充；如果多个 key 共用一个 carrier user，会互相共享同一个用户并发池。
- 2026-06-01：key 级并发必须同时改变限流作用域：API Key 显式 `concurrency > 0` 时使用 `api_key_id` 作用域；未设置时继续使用 user 作用域，兼容用户侧现有行为。
- 2026-06-01：线上 Sub2API 只读计数：有效 API key 81 个，usage log 906303 条。最终迁移前需重新对账并做 DB/Redis 备份。
- 2026-06-01：CLIProxyAPI 生产组字段包含 `concurrency_limit`；当前四类车组为独享车 3、双人车 3、三人车 2、四人车 1。
- 2026-06-01：CLIProxyAPI API Key 显式并发从 `policy_json->>'concurrency-limit'` 解析；Sub2API 迁移脚本会写入 `api_keys.concurrency`，未设置的 key 继承组级并发。
- 2026-06-01：Sub2API auth cache 版本已提升到 v11，并携带 API key / group concurrency；数据迁移后还需清理 Redis `apikey:auth:*` 与 `apikey:rate:*`，避免旧 snapshot 或旧限速窗口影响切换。
- 2026-06-01：认证热路径必须显式 select `group.concurrency`，否则组级继承会在 auth cache 路径退化为用户并发；已在代码与 SQLite 回归中修复。

---

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
- 2026-05-27 线上用户反馈补充：`Searched:` 后出现 `This session is being continued from a previous conversation...`，说明 OpenAI `web_search_call` 没有 `action.query` 时，Sub2API 可能把 Claude Code resume/compact 的 continuation summary 当成 fallback search query 展示；如果上游 `action.query` 自身也带这类文本，同样需要屏蔽。修复边界：action/fallback query 进入 state、CLI synthetic text、VSCode thinking 和 `server_tool_use.input.query` 时都必须经过同一套 search-query 清洗；命中 continuation summary 时改为 generic `Searching the web.` / `Searched the web.`。如果上游把 `web_search` `<tool_call>` 伪装成普通 assistant text 且携带 continuation summary，则直接抑制该文本块，不让它进入 Claude Code UI。
- 后续设计方向：WebSearch 兼容不应继续堆零散字符串判断。需要把“真实 OpenAI web_search_call 进度”和“模型文本伪装 tool_call”分成两个明确通道；前者走状态机生成 Claude CLI/VSCode 可读进度，后者默认作为普通文本处理，只对已证明会污染 Claude Code UI 的 continuation summary tool-call 做出口安全门。
- OpenAI Responses -> Anthropic 转换在 `backend/internal/pkg/apicompat/responses_to_anthropic.go`：
  - 非流式 `web_search_call` 当前生成 `server_tool_use` 和空的 `web_search_tool_result`。
  - 流式 `response.output_item.done` 且 item 为 `web_search_call` 时，也生成 `server_tool_use` 和空的 `web_search_tool_result`。
  - 当前没有按 Claude CLI / VSCode 客户端定制搜索进度展示。
- 参考项目 CLIProxyAPI 的方案一核心：
  - 根据 `User-Agent` / `Originator` 区分 Claude CLI、Claude VSCode、Codex VSCode。
  - Claude CLI：遇到真实 OpenAI `web_search_call` 时补合成文本块，形如 `Searching the web.` / `Searched: <query>` 加 `<tool_call>` 标记。
  - VSCode/Codex VSCode：遇到真实 `web_search_call` 时补简短 `thinking` 进度，例如 `Searching the web for: <query>`。
  - 对这些客户端 suppress post-hoc reasoning summary，避免搜索完成后才把总结伪装成实时 thinking。

## WebSearch 来源/链接可见性

- 2026-05-29 用户截图对比显示：Sub2API 只显示 `Searched: <查询>`，而当前 CLIProxyAPI 能在搜索结果/最终回答中显示来源信息。
- 根因一：Sub2API 的 `ResponsesRequest.Include` 只请求 `reasoning.encrypted_content`，没有请求 OpenAI Responses 支持的 `web_search_call.action.sources`，因此上游即使有 sources 默认也不一定返回。
- 根因二：Sub2API 类型里 `WebSearchAction` 只有 `type/query`，没有 `queries/url/sources`；`ResponsesContentPart` 没有 `annotations`，所以 OpenAI `url_citation` 的 `url/title` 会在 JSON unmarshal 后丢失。
- 根因三：Sub2API 把 `web_search_tool_result.content` 固定为空数组；这和 Anthropic 原生 WebSearch 示例里的 `web_search_result {url,title,page_age,encrypted_content}` 不一致，Claude Code UI 没有可展示的搜索来源。
- 官方依据：OpenAI Responses WebSearch 会在 `web_search_call.action` 返回搜索动作，并在 `message.content[].annotations` 提供 `url_citation`；`include` 支持 `web_search_call.action.sources`。Anthropic WebSearch 的 `web_search_tool_result.content` 可以包含 `web_search_result`，text block 可以包含 `web_search_result_location` citations。
- 本地修复边界：只保留上游真实返回的 `sources/url/annotations`，不伪造搜索结果；如果上游没有返回来源，仍保持空结果，避免把用户查询误显示成来源。

## 生产账号分组绑定快照

- 2026-05-29 11:42 CST：线上 `sub2api` 容器运行镜像 `zhangtaylor985/sub2api:main-2e01e876`，Postgres/Redis 健康，`/health` 返回 ok。
- 表关系确认：账号到分组由 `account_groups(account_id, group_id, priority, created_at)` 表维护，主键为 `(account_id, group_id)`；`priority` 默认 50，`created_at` 默认 `now()`。
- 当前数量：未删除账号 11 个，未删除分组 8 个，现有 `account_groups` 12 行；若把所有未删除账号补齐到所有未删除分组，需要新增 76 行。
- 当前快照已记录到 `docs/prod_account_group_snapshot_20260529.md`；未读取或记录任何 credentials/API key/token。
- 默认变更边界：只对 `accounts.deleted_at IS NULL` 和 `groups.deleted_at IS NULL` 的组合补齐绑定；已删除账号 `id=1` 不纳入补齐。

## `/key-usage` 模型黑盒展示

- 用户侧 `/key-usage` 调用 `GET /v1/usage`，模型表来自响应中的 `model_stats`；该表属于用户可见口径，不应暴露内部上游调度模型。
- 如果客户端请求 Claude 模型但后端通过 OpenAI `allow_messages_dispatch` 调度到 GPT，上游 GPT 只能作为内部路由/管理员排查口径，不应在用户侧模型统计里显示。
- 如果客户端确实请求 GPT，例如 OpenAI `/v1/responses` 或 `/v1/chat/completions`，用户侧可以显示 GPT。
- 当前修复口径：用户侧模型统计优先使用 `model_mapping_chain` 第一段，其次 `requested_model`，最后回退 legacy `model`；`upstream_model` 和完整映射链只作为管理员排查字段。
- 线上只读抽样确认：`/v1/messages` 且 `model_mapping_chain` 类似 `claude-opus-4-7→gpt-5.5` 的记录，用新表达式会聚合到 `claude-opus-4-7`；OpenAI 端点直接请求 `gpt-*` 的记录仍聚合为 `gpt-*`。
- 历史 `cliproxy_legacy` 行如果没有 `requested_model` 和 `model_mapping_chain`，无法可靠恢复原始客户端模型；不要凭猜测批量改库。

## `claude-opus-4-8` 线上映射

- OpenAI 分组链路有两层：分组 `messages_dispatch_model_config` 先把 Claude family 映射到 GPT，账号 `credentials.model_mapping` 再作为白名单和最终上游改写；两层都要覆盖 4.8。
- Anthropic 分组不走 OpenAI dispatch；账号级 `credentials.model_mapping` 同样会作为模型白名单。若 4.7 可用但 4.8 不可用，需要先确认该 key 绑定的是 OpenAI 分组还是 Anthropic 分组。
- 2026-05-29 用户反馈的 key 绑定 `CPA-Double` Anthropic 分组，实际成功链路选中 `CPA Worker` Anthropic 账号。该账号只有 `claude-opus-4-7 -> claude-opus-4-7`，缺少 4.8，补充 `claude-opus-4-8 -> claude-opus-4-7` 后生产黑盒验证通过。
- 排查同类问题时不要只看日限额或 OpenAI 账号映射；应按 key -> group -> linked accounts -> selected account -> account model_mapping 的顺序确认真实链路。

## Claude -> GPT 兼容库边界

- 2026-05-29 复核：当前 Claude->GPT 兼容入口只在 OpenAI `/v1/messages` dispatch 路径，即 `OpenAIGatewayHandler.Messages` -> `OpenAIGatewayService.ForwardAsAnthropic`；原生 Claude 账号路径仍是 `GatewayHandler.Messages` 选择 Anthropic/Gemini/Antigravity 账号后直接转发。
- 新增库：`backend/internal/pkg/claudegptcompat`。
- 库职责：只放 Claude 客户端使用 GPT/OpenAI Responses 时需要的兼容策略，包括客户端识别、WebSearch query 清洗、Claude CLI synthetic 搜索进度、VSCode thinking 搜索进度、continuation summary 防泄漏、WebSearch sources/url/citation 辅助。
- `backend/internal/pkg/apicompat` 当前保留协议结构体、Anthropic <-> Responses 转换器和薄 wrapper；这样后续维护时可以先看 `claudegptcompat` 判断是否属于 Claude->GPT 专用逻辑。
- 迁移矩阵复核：P1 协议稳定性项已基本落地；P2/P3 诊断和观测类能力仍按后续任务处理，不算“全部完成”。
- 可维护性边界进一步细化：`claudegptcompat` 不保留一个大杂烩文件，而按职责拆分为 client/query/safety/websearch。后续新增 Claude->GPT 行为时，先判断属于哪类策略；如果需要新增跨类别状态机，应先设计子包或独立文件，不要把策略继续塞回 `apicompat`。

## 本地黑盒沙盒

- 本地 Sub2API 已可作为 Claude->GPT 黑盒沙盒：`http://127.0.0.1:8080`，容器 `sub2api-dev` / `sub2api-postgres-dev` / `sub2api-redis-dev`。
- 本地分组 `Local Codex GPT` 作为测试分组，开启 OpenAI `/v1/messages` dispatch，Opus family 映射到 `gpt-5.5`。
- 本地 API Key 名称 `Local Claude GPT Blackbox` 用于测试；raw key 不写入文档。
- 验证证据链：
  - 直接 API smoke 返回 Claude 形态响应，用户侧 `model` 保持 `claude-opus-4-7`。
  - usage log 内部证据显示 `model_mapping_chain=claude-opus-4-7→gpt-5.5`、`upstream_model=gpt-5.5`。
  - Claude CLI `-p` 黑盒 debug-file 显示命中 `ANTHROPIC_BASE_URL=http://127.0.0.1:8080` 和 `/v1/messages`，输出 `LOCAL_CC1_SUB2API_OK`。
  - WebSearch stream-json 黑盒显示搜索过程和来源链接都能从 OpenAI `web_search_call` 转回 Claude 事件：`server_tool_use`、`web_search_tool_result`、URL 列表、最终正文和 `message_stop` 都出现。
- 重要坑：Claude CLI settings 中的 `env.ANTHROPIC_AUTH_TOKEN` 会影响实际发送的 key；本地黑盒不能只在 shell 中临时设置 token，必须检查并必要时备份修改 `/Users/taylor/.claude_local/settings.json`。

## 2026-06-01 上线门禁结论

- 本地 dev compose 缺失时，已验证可使用 fallback 沙盒：`sub2api-postgres-local` 暴露 `127.0.0.1:5433`，`sub2api-redis-local` 暴露 `127.0.0.1:6380`，当前源码构建的 `backend/bin/server` 在 tmux session `sub2api-local` 监听 `127.0.0.1:8080`。
- 真实 Codex auth file 黑盒通过：直接 `/v1/messages`、Claude CLI 非交互 `-p`、WebSearch `stream-json --include-partial-messages`、真实 TTY 同一会话连续两轮均成功。
- WebSearch 黑盒看到 `server_tool_use name=web_search`、`web_search_tool_result.content[]` URL 列表、`Searched:`、最终正文和 `message_stop`；usage log 证实 `claude-opus-4-6→gpt-5.5`。
- 自动化测试通过：`git diff --check`、`go test ./internal/pkg/claudegptcompat ./internal/pkg/apicompat`、`go test ./internal/service -run 'TestForwardAsAnthropic|TestNormalizeOpenAIMessagesDispatchModelConfig|TestResolveOpenAIForwardModel|TestOpenAI'`、`go test -tags=unit ./internal/repository`、`go test ./...`、`pnpm 9 lint:check/typecheck/build`。
- 本地噪音：本地后台 `AccountExpiry` 曾记录一次 Postgres `Cannot allocate memory`，判定为本机 Docker/OrbStack 资源噪音；不影响请求链路通过，但生产发布后仍要观察容器日志和健康状态。
- 生产发布判断：当前变更达到本地上线门禁，下一步应先把本地分支安全合入最新 `origin/main`，再走 GitHub 主线和 Docker app 容器 canary/替换流程；数据层保持不动。

## 2026-06-01 生产发布结论

- 已上线镜像：`zhangtaylor985/sub2api:main-19663655`。
- 上一版镜像：`zhangtaylor985/sub2api:main-853b8019`。
- Compose 备份：`/root/cliapp/sub2api/docker-compose.yml.bak.20260601T065530Z`。
- 发布方式：生产 `/root/cliapp/sub2api-src` fast-forward 到 `19663655`，在生产机本地构建镜像，先起 `sub2api-canary-19663655` 绑定远端 `127.0.0.1:18080`，canary smoke 通过后替换正式 `sub2api` app 容器；Postgres/Redis 未重启。
- 验证结果：canary `/health` 通过，canary 直接 `/v1/messages` 返回 `SUB2API_CANARY_19663655_OK` / `SUB2API_CANARY_OPUS47_OK`；正式容器 Docker health 为 healthy，宿主机和公开 `https://cc.claudepool.com/health` 均返回 ok，正式 `/v1/messages` 返回 `SUB2API_PROD_19663655_OK`。
- canary 的非流式强制 `WebSearch` 样本只返回最终文本，没有暴露中间 `server_tool_use`；因此本次 WebSearch 展示验收仍以本地真实 Claude CLI `stream-json` 黑盒为主证据，不把该非流式样本当作失败。
- 生产配置发现：测试 key `id=313` 当前所在分组/账号链路把 `claude-opus-4-6` 和 `claude-opus-4-7` 都映射到 `gpt-5.4`；这是生产模型映射配置，不是本次代码发布导致。后续若要“所有 Opus -> GPT-5.5”，需要单独做生产分组和账号映射整理。
- 观察到真实用户大上下文请求仍可能触发上游 `context window` 502；该日志与本次 smoke 无关，后续应归入长上下文/模型窗口治理。

## 2026-06-01 生产 Opus -> GPT-5.5 映射收敛

- 线上实际运行镜像在本阶段开始时为 `zhangtaylor985/sub2api:main-378405f6`，容器 healthy，公开 `/health` 正常；该镜像晚于此前 `main-19663655`，本阶段以线上实际状态为准，不回滚已有更新。
- 只读快照显示 6 个未删除 OpenAI 分组均已开启 `allow_messages_dispatch=true`，但 `opus_mapped_model` 仍为 `gpt-5.4`；`claude-opus-4-8` 已有精确映射到 `gpt-5.5`，`claude-opus-4-6` 和 `claude-opus-4-7` 精确映射缺失。
- 只读快照显示 8 个 active+schedulable 的 OpenAI 账号没有 `claude-opus-4-6/4-7/4-8` 的冲突账号级映射；部分账号已有 4-6/4-7/4-8 -> `gpt-5.5`，多数账号没有账号级 `model_mapping`，会使用分组层 defaultMappedModel。
- 一个非 schedulable OpenAI API key 类型账号存在 `claude-opus-4-6/4-7/4-8` passthrough 到 Claude 名称的映射；因当前不可调度，本阶段不把它作为生产流量阻塞项。
- 决策：只收敛 OpenAI 分组层 `messages_dispatch_model_config`，把 Opus family 和 4-6/4-7/4-8 精确映射统一到 `gpt-5.5`；不为原本无 mapping 的账号新增 mapping，避免意外改变账号白名单语义。
- 执行结果：6 个 OpenAI dispatch 分组均更新完成；Redis `apikey:auth:*` 快照清理到 0，`sub2api` 应用容器重启后 Docker health healthy，公开 `/health` ok。
- 生产 direct smoke：`claude-opus-4-6`、`claude-opus-4-7`、`claude-opus-4-8` 均 HTTP 200，usage log 分别确认 `claude-opus-4-6→gpt-5.5`、`claude-opus-4-7→gpt-5.5`、`claude-opus-4-8→gpt-5.5`。
- 发布后日志观察到的独立问题：API key 并发槽等待超时、上游 HTTP/2 `INTERNAL_ERROR`、`/v1/chat/completions` 直接请求 Claude Opus 被 Codex 上游拒绝、大上下文 context-window 错误；这些不是本次分组映射收敛导致，后续应单独治理。

## OpenAI dispatch 多轮 session 粘性

- `/v1/messages` OpenAI dispatch 入口此前先调用 `GenerateSessionHash(c, body)`，该方法在没有显式 `session_id`/`conversation_id`/`prompt_cache_key` 时会从 body 的 model、tools、system 和第一条 user 消息生成 content-based seed。
- 因为 content fallback 通常非空，后续 `resolveOpenAIMessagesMetadataSession` 很少有机会使用 Claude `metadata.user_id`；这与原生 `GatewayService.GenerateSessionHash(parsed)` 的“metadata.user_id session_id 最高优先级”口径不一致。
- 风险：普通多轮 replay 若第一条 user/system/tools 稳定，content seed 能粘住；但 compact/resume 或 body 被客户端改写，第一条 user/system/tools 变化时，同一个 Claude session 可能生成不同 session hash，从而换 OpenAI/Codex account。
- 2026-06-01 复核：Sub2API 原本已有 Redis `sticky_session:{groupID}:openai:{hash} -> account_id` 一小时缓存；本次 `d1d5efb2` 没有新增第二套缓存，只调整 OpenAI `/v1/messages` dispatch 的 session hash 来源优先级。该调整不是照搬旧 CLIProxyAPI，而是让 OpenAI dispatch 路径对齐 Sub2API 原生 Claude/Gateway 路径的 `metadata.user_id` 优先语义。
- 与 `ForwardAsAnthropic` 内部 prompt cache / `previous_response_id` 复用不同，OpenAI dispatch session hash 只决定“本轮选哪个 OpenAI/Codex account”；上游缓存键仍由 `ForwardAsAnthropic` 根据 `metadata.user_id`、cache_control 或完整消息 digest 自行派生。
- 本地修复：新增 `resolveOpenAIMessagesSessionSignals`，优先级调整为显式 session header / `prompt_cache_key` > Claude `metadata.user_id` > content-based fallback；`metadata.user_id` 仍只影响账号粘性，不在 handler 层生成 `prompt_cache_key`。
- 回归测试覆盖：metadata 在 body 改写时保持相同 session hash；无 metadata 时仍按 content fallback 区分不同首轮内容；显式 `session_id` 优先于 metadata。

## API Key 模型族限制迁移状态

- CLIProxyAPI 旧项目的 API Key 模型族限制来自 `policy_json.excluded-models`，管理端把它展示为“允许 Claude 系列 / 允许 GPT 系列”。默认 GPT 隐藏模式包括 `gpt-*`、`chatgpt-*`、`o1*`、`o3*`、`o4*`，Claude 隐藏模式包括 `claude-*`。
- 旧项目中限制判定发生在用户请求命名空间：middleware 明确写着 access controls evaluated against the client-requested model namespace，downstream routing/fallback targets remain unaffected by excluded-models。因此 Claude-only key 的含义是“允许用户请求 Claude 模型，不允许直接请求 GPT 模型”；它仍然可以在内部黑盒地走 Claude -> GPT 路由。
- 线上旧 CLIProxyAPI 配置快照中，API Key 策略大致分布为：Claude-only 293、GPT-only 6、both 80。这说明这不是少量边缘配置，而是生产策略的一部分。
- 线上 Sub2API 已迁移一部分 key，并在 `cliproxy_legacy_api_key_migration.source_policy_json` 中保留旧 policy JSON。已迁移有效 key 的旧策略快照为：Claude-only 57、GPT-only 2、both 23。
- 但 Sub2API 当前 `api_keys` 运行时字段没有保存 `allow_claude_family` / `allow_gpt_family` / `excluded_models` 等策略；迁移 SQL 只把旧策略保存到 audit 表，没有写入可执行字段。
- 2026-06-01 线上 Sub2API 实际状态复核：app 镜像为 `zhangtaylor985/sub2api:main-3f0dad5d`；有效 API Key 82 个、有效分组 8 个、`channels` 0、`channel_groups` 0、`channel_model_pricing` 0。
- Sub2API 有 `channels.restrict_models` 与 `channel_model_pricing` 机制，但线上 `channels` / `channel_groups` / `channel_model_pricing` 为空，且该机制是渠道/分组维度，不适合表达同一个 CP Legacy 分组内混合存在的 Claude-only、GPT-only、both API Key。
- 当前线上 CP Legacy key 基本都挂在 OpenAI 分组，分组 `allow_messages_dispatch=true`；路由根据 key 所属 group platform 进入 OpenAI gateway。由于缺少 key 级模型族限制，旧项目中的 Claude-only/GPT-only 语义目前没有在 Sub2API 运行时生效。
- 黑盒边界：实现时不能按“内部上游是否 GPT”阻断 Claude-only。正确语义应是按用户请求的 endpoint/model 判断：Claude-only 允许 `/v1/messages` 请求 `claude-*` 并内部 Claude -> GPT；但应阻断直接 OpenAI endpoint 或 GPT family 模型请求。GPT-only 则应阻断用户请求 `claude-*`，允许用户请求 GPT family。
- 下一步建议新增 Sub2API key 级模型族策略，而不是复用 channel 限制：在 API key 运行时模型中增加可迁移、可缓存、可管理的 allow family 字段或独立策略表；从 `cliproxy_legacy_api_key_migration.source_policy_json` 回填现有 82 个迁移 key；在 gateway handler 前统一做用户侧模型族校验，并返回协议兼容、无内部 GPT/Codex 细节的泛化 403。

## API Key 模型族策略实现

- 2026-06-02 本地实现采用 `api_keys.allow_claude_family` / `api_keys.allow_gpt_family` 两个运行时字段，而不是 channel 限制或独立策略表。原因：旧策略是 per API key，当前线上 channel 表为空，且同一分组内可能同时存在 Claude-only、GPT-only、both key。
- 策略只看用户侧请求命名空间和入口形态，不看内部上游模型。Claude-only key 可以通过 `/v1/messages` 请求 `claude-*`，内部仍可黑盒调度到 GPT；但不能使用 OpenAI 形态入口 `/v1/responses`、`/v1/chat/completions`、Images。
- GPT-only key 阻断用户请求 `claude-*`；both key 允许两类模型族。未设置策略的旧内存构造对象默认视为 both-allowed，避免测试或旧路径因 bool 零值误判全禁。
- 迁移 `144_add_api_key_model_family_policy.sql` 在本地 PG18 生产恢复库试跑通过，并且幂等复跑通过。回填后的有效 key 分布为 `both=23`、`claude_only=57`、`gpt_only=2`，与此前 audit 表统计一致。
- 管理端 API Key 列表和创建/编辑弹窗新增“模型族权限”字段，便于以后直接维护 `allow_claude_family` / `allow_gpt_family`。

## Claude -> GPT 错误黑盒

- 2026-06-02 修复点在 OpenAI `/v1/messages` dispatch 的 Anthropic 兼容错误出口：`handleAnthropicErrorResponse` 现在调用 `handleCompatErrorResponse` 的 black-box 模式。
- black-box 模式会跳过 error passthrough 规则，并把非 failover 上游 HTTP 错误写成 Anthropic 形态的 `502 api_error "Upstream request failed"`；上游状态、request id、消息和可选 body 仍保留在 ops/log 错误上下文中。
- 该模式不影响 OpenAI 原生 chat/responses passthrough，也不改变原生 Claude 账号路径。目标是避免 Claude Code 客户端在 Claude->GPT 路径看到 `gpt-*`、`Codex`、`ChatGPT account`、auth file 或内部路由细节。
- 本地黑盒复现 `claude-sonnet-4-6 -> gpt-5.3-codex` 上游不支持错误时，客户端只收到 `502 api_error "Upstream request failed"`，响应体不含 `gpt-5.3-codex`、`Codex`、`ChatGPT account`；服务端日志仍保留真实上游错误，便于管理员排查。
- 本地正向黑盒确认：Claude-only key 通过 `/v1/messages` 请求 `claude-opus-4-7` 时不会被内部 `gpt-5.5` 映射误伤，客户端得到 Claude 形态 `200 OK`，返回 `model` 仍是 `claude-opus-4-7`。
- 2026-06-02 生产上线后复核：客户端黑盒与运维日志是两个边界。生产日志允许保留 `gpt-5.3-codex` / Codex / ChatGPT account 等上游细节；用户侧 `/v1/messages` 响应必须保持 `api_error "Upstream request failed"` 这类泛化信息。

## API Key 级 Claude -> GPT 目标模型覆盖

- 当前 Sub2API 的 Claude -> GPT 目标模型映射有两层：分组级 `groups.messages_dispatch_model_config` 生成 OpenAI `/v1/messages` dispatch 的 `defaultMappedModel`，账号级 `accounts.credentials.model_mapping` 再做最终上游模型映射和账号支持模型约束。
- 现有 API Key 运行时模型只有 `allow_claude_family` / `allow_gpt_family` 这类“是否允许请求模型族”的策略，没有“该 key 的 Claude 请求应默认转到 `gpt-5.5` 还是 `gpt-5.4`”的目标模型覆盖。
- 旧 CLIProxyAPI 有 per-key `claude-gpt-target-family`，用于覆盖全局 Claude -> GPT target family。Sub2API 如果要支持同一分组内不同 key 使用不同 GPT 目标模型，需要新增 key 级配置，否则只能通过拆分分组或账号映射绕行，维护性较差。
- 新增能力的优先级建议为：账号级 `credentials.model_mapping` 最终改写/白名单 > API key 级 dispatch 映射覆盖 > 分组级 dispatch 映射 > 代码默认值。空 API key 配置必须表示不覆盖。
- 2026-06-02 实现后的实际优先级：API key 级覆盖先决定 OpenAI `/v1/messages` dispatch 的 `defaultMappedModel`；未命中才回退分组级配置。账号级 `credentials.model_mapping` 仍在后续 OpenAI account 解析中作为最终改写和白名单，不被 key 级覆盖绕过。
- 生产 `api_keys.id=125` 所在分组仍保持 Opus -> `gpt-5.5`、Sonnet -> `gpt-5.3-codex`、Haiku -> `gpt-5.4-mini`；本次只给该 key 设置 key 级覆盖到 `gpt-5.4`，不影响同组其他 key。

## 2026-06-02 生产数据本地恢复准备

- 只读探测确认线上容器：`sub2api` 当前镜像 `zhangtaylor985/sub2api:main-6dc024d4`，`sub2api-postgres` 为 `postgres:18-alpine`，`sub2api-redis` 为 `redis:8-alpine`，容器 healthy。
- 线上当前数据库名为 `sub2api`，数据库体量约 `1663 MB`。
- 本地当前可用 Sub2API 沙盒依赖容器为 `sub2api-postgres-local` 和 `sub2api-redis-local`，Postgres 版本 `17.6`，本地库约 `15 MB`。
- 版本风险：线上 PG18 逻辑备份恢复到本地 PG17 属于跨大版本向下恢复，不应作为默认路径。更安全的路径是在本地单独启动 PG18 恢复容器或升级本地恢复目标，再导入生产 dump。
- 建议默认不覆盖现有本地沙盒库；先创建独立本地恢复库并保留本地旧库备份，确认可查询后再决定是否切换本地应用使用该库。
- 执行结果：已创建独立本地恢复容器 `sub2api-postgres-restore-pg18`，镜像 `postgres:18-alpine`，监听 `127.0.0.1:5434`，数据目录 `deploy/postgres_data_prod_restore_pg18/`。
- 本地 PG17 沙盒恢复前备份：`deploy/db_backups/local_pg17_sub2api_before_prod_restore_20260602T011651Z.dump`。
- 生产 dump：`deploy/db_backups/prod_sub2api_pg18_20260602T011802Z.dump`，大小约 `62M`，SHA256 `4190943e33860b2e89ea0f767685fde1196f659be04c721b9895c13117e1e7f5`；dump 和 restore 日志同目录保存。
- 恢复校验：本地 PG18 恢复库 `public` 表数 77；关键表行数 `users=83`、`api_keys=82`、`groups=8`、`accounts=12`、`account_groups=88`、`cliproxy_legacy_api_key_migration=82`，与线上对照一致。
- `usage_logs` 本地恢复后为 1,041,547；线上恢复后即时对照为 1,041,565。差异 18 行来自生产在 dump 之后继续写入，符合在线只读逻辑备份预期。

## 2026-06-02 生产错误可观测性与 Request ID

- 用户截图中的 `Claude's response exceeded the 64000 output token maximum... CLAUDE_CODE_MAX_OUTPUT_TOKENS` 是 Claude Code 客户端本地输出上限报错口径；线上 Sub2API 最近 2 小时日志未检索到该原文或 `CLAUDE_CODE_MAX_OUTPUT_TOKENS`，因此不能按“Sub2API 上游错误原样返回”处理。
- 线上 Sub2API 当前日志体系并不为空：全局 `RequestLogger` 会生成/保留 `X-Request-ID`，access log、内容审核日志、ops error log、ops system log 均能按该 ID 关联；公网与容器本地响应头都已验证返回 `X-Request-ID`。
- 线上日志落点有三层：Docker stdout/stderr（`docker logs sub2api`）、容器文件 `/app/data/logs/sub2api.log` 及轮转压缩文件、Postgres `ops_system_logs` / `ops_error_logs`。当前日志级别按代码默认和实际输出判断为 `info`，日志同时输出 stdout 与文件，轮转默认 100MB/7 天/压缩。
- 当前缺口：Claude Code UI 通常不展示响应头；用户只给报错截图时，未必能拿到 `X-Request-ID`。因此需要在错误 JSON/SSE 体中也带同一个网关 `request_id`，但不能把 GPT/Codex/auth file 等内部路由细节写进用户侧错误 message。
- `usage_logs.request_id` 常见值如 `generated:...`，属于用量/上游请求记录口径，不等同于 HTTP 网关 `X-Request-ID`。排查一次用户 HTTP 请求优先用网关 `request_id` 查 `ops_error_logs`、`ops_system_logs` 和文件日志；用量表作为补充证据。
- 生产最近可观测到的真实 502 样本包含 `request_id`、`client_request_id`、`api_key_id`、`account_id`、`model`、`body_bytes` 和泛化错误；文件日志保留更具体的服务端错误，例如上游 context window 超限。客户端仍应只看到黑盒错误。

## 2026-06-02 全 API Key Claude -> GPT 映射收敛

- 生产未删除 API Key 共 82 个，全部绑定 OpenAI 分组；本次全量写入 API Key 级 `messages_dispatch_model_config`，统一 `opus_mapped_model=gpt-5.4`、`sonnet_mapped_model=gpt-5.3-codex`。
- 写库前有效备份为 `/root/cliapp/sub2api/ops_backups/api_key_messages_dispatch_config_before_opus54_sonnet53codex_20260602T100209Z.tsv`，共 82 行；首次 0 行备份文件无效，不作为回滚依据。
- Redis `apikey:auth:*` auth snapshot 已清到 0；生产复核 82 个有效 API Key 均已有 `opus_mapped_model=gpt-5.4` 和 `sonnet_mapped_model=gpt-5.3-codex`。
- 分组层仍保留 Opus exact mapping 到 `gpt-5.5`，但代码优先级是 API Key family override 先于分组 exact mapping；因此全 key 级 `opus_mapped_model=gpt-5.4` 会实际覆盖分组层 Opus `gpt-5.5`。
- 账号级 `credentials.model_mapping` 当前未设置相关白名单/改写，不会把 `gpt-5.3-codex` 或 `gpt-5.4` 再改成其他模型。
- Opus 黑盒按 5 个 OpenAI 分组代表 key 验证均通过；`usage_logs` 确认 `claude-opus-4-7→gpt-5.4`。
- Sonnet 黑盒按 5 个 OpenAI 分组代表 key 验证均失败，HTTP 502，客户端错误保持黑盒并带 request_id；服务端日志真实根因为：`The 'gpt-5.3-codex' model is not supported when using Codex with a ChatGPT account.` 当前生产 OpenAI OAuth 账号形态不支持 `gpt-5.3-codex`，不是 Sub2API 映射未生效。

## 2026-06-04 `/v1/models` GPT 黑盒展示

- 用户反馈 Claude-only API Key 调用 `GET /v1/models` 仍返回 `gpt-5.5`、`gpt-5.4`、`gpt-5.4-mini`；管理端截图显示该 key 只允许 Claude，不允许 GPT/OpenAI。
- 生产只读确认：近期 Claude-only key 均为 `allow_claude_family=true`、`allow_gpt_family=false`，绑定 `platform=openai` 且 `allow_messages_dispatch=true` 的 OpenAI dispatch 分组；配置本身符合“Claude -> GPT 内部黑盒”的预期。
- 根因在 `GatewayHandler.Models`：该路径从 `GatewayService.GetAvailableModels` 聚合分组内可调度账号的 `credentials.model_mapping` key，并直接作为用户可见模型返回；请求路径已有模型族策略拦截，但列表路径没有套同一层策略。
- 修复口径：`/v1/models` 返回前必须按 API Key 模型族策略过滤所有来源的模型列表。Claude-only + OpenAI dispatch 分组如果过滤掉内部 GPT mapping 后无可见模型，应 fallback 到 Claude 默认模型列表，而不是 OpenAI 默认模型列表。
- 本地回归已覆盖：Claude-only OpenAI dispatch 不暴露 GPT mapping，且 GPT-only OpenAI key 仍能看到 OpenAI 模型。

## 2026-06-05 新服务器迁移快照

- 线上只读确认：`sub2api` 当前镜像 `zhangtaylor985/sub2api:main-d271fbbf`，app/Postgres/Redis 容器 healthy；生产数据库体量约 `1922 MB`。
- 本地迁移快照目录：`deploy/migration_snapshots/sub2api_migration_20260605T020823Z`，权限 700，已通过 `deploy/.gitignore` 忽略。
- 最新生产 dump：`db/prod_sub2api_pg18_20260605T020823Z.dump`，约 `84M`，SHA256 `b838c73204d4b33fdc4e28bd33e7a5f6d50915eb2d93025581049ebe8ea89269`。
- 已覆盖恢复到本地独立 PG18 容器 `sub2api-postgres-restore-pg18`，监听 `127.0.0.1:5434`；本地 PG17 沙盒未覆盖。
- 恢复校验：本地 PG18 恢复库 `public` 表数 79；关键表行数 `users=83`、`api_keys=84`、`groups=8`、`accounts=12`、`account_groups=88`、`cliproxy_legacy_api_key_migration=82`，与线上对照一致。
- `usage_logs` 本地恢复后为 1,127,960；线上恢复后即时对照为 1,128,102。差异 142 行来自生产在 dump 之后继续写入，符合在线只读逻辑备份预期。
- 已打包 Sub2API runtime/data 与 compose：`sub2api_runtime/sub2api_runtime_data_and_compose.tar.gz`；实时日志 `data/logs/*` 已排除。
- 已打包生产 `.env`、`docker-compose.yml` 和 Caddyfile：`sub2api_runtime/deployment_config_env_compose_caddy.tar.gz`。
- 已收集并打包 50 个 Codex auth JSON：`auth_files/codex_auth_files.tar.gz`，并解压到 `auth_files/extracted/`，文件权限 600、目录权限 700。
- 迁移说明已归档：`docs/sub2api_migration_snapshot_20260605_CN.md`。Redis 未作为权威数据迁移；建议新服务器 Redis 重新初始化，避免旧缓存污染。

## 2026-06-08 新服务器迁移快照

- 线上只读确认：`sub2api` 当前镜像 `zhangtaylor985/sub2api:main-d271fbbf`，app/Postgres/Redis 容器 healthy；生产数据库体量约 `2131 MB`。
- 本地迁移快照目录：`deploy/migration_snapshots/sub2api_migration_20260608T025111Z`，权限 700，已通过 `deploy/.gitignore` 忽略。
- 最新生产 dump：`db/prod_sub2api_pg18_20260608T025111Z.dump`，约 `102M`，SHA256 `5bfc570c9367930ba8795f061428d795f3e728bf7aa9eef686d9ece95cb8d401`。
- 已覆盖恢复到本地独立 PG18 容器 `sub2api-postgres-restore-pg18`，监听 `127.0.0.1:5434`；本地 PG17 沙盒未覆盖。
- 恢复校验：本地 PG18 恢复库 `public` 表数 79；关键表行数 `users=83`、`api_keys=85`、`groups=8`、`accounts=12`、`account_groups=88`、`cliproxy_legacy_api_key_migration=82`，与线上对照一致。
- `usage_logs` 本地恢复后为 1,195,662；线上恢复后即时对照为 1,195,788。差异 126 行来自生产在 dump 之后继续写入，符合在线只读逻辑备份预期。
- 已打包 Sub2API runtime/data 与 compose：`sub2api_runtime/sub2api_runtime_data_and_compose.tar.gz`；实时日志 `data/logs/*` 已排除。
- 已打包生产 `.env`、`docker-compose.yml` 和 Caddyfile：`sub2api_runtime/deployment_config_env_compose_caddy.tar.gz`。
- 已收集并打包 50 个 Codex auth JSON：`auth_files/codex_auth_files.tar.gz`，并解压到 `auth_files/extracted/`，文件权限 600、目录权限 700。
- 迁移说明已归档：`docs/sub2api_migration_snapshot_20260608_CN.md`。Redis 未作为权威数据迁移；建议新服务器 Redis 重新初始化，避免旧缓存污染。

## 2026-06-09 新服务器迁移快照

- 线上只读确认：`sub2api` 当前镜像 `zhangtaylor985/sub2api:main-d271fbbf`，app/Postgres/Redis 容器 healthy；生产数据库体量约 `2207 MB`。
- 本地迁移快照目录：`deploy/migration_snapshots/sub2api_migration_20260609T022630Z`，权限 700，已通过 `deploy/.gitignore` 忽略。
- 最新生产 dump：`db/prod_sub2api_pg18_20260609T022630Z.dump`，约 `108M`，SHA256 `d8762d370260962bfcf91c4058026b4164668d3b44a9a602fa3f67b46f0f3bac`。
- 已覆盖恢复到本地独立 PG18 容器 `sub2api-postgres-restore-pg18`，监听 `127.0.0.1:5434`；本地 PG17 沙盒未覆盖。
- 恢复校验：本地 PG18 恢复库 `public` 表数 79；关键表行数 `users=83`、`api_keys=85`、`groups=8`、`accounts=12`、`account_groups=88`、`cliproxy_legacy_api_key_migration=82`，与线上对照一致。
- `usage_logs` 本地恢复后为 1,218,825；线上恢复后即时对照为 1,218,919。差异 94 行来自生产在 dump 之后继续写入，符合在线只读逻辑备份预期。
- 已打包 Sub2API runtime/data 与 compose：`sub2api_runtime/sub2api_runtime_data_and_compose.tar.gz`；实时日志 `data/logs/*` 已排除。
- 已打包生产 `.env`、`docker-compose.yml` 和 Caddyfile：`sub2api_runtime/deployment_config_env_compose_caddy.tar.gz`。
- 已收集并打包 50 个 Codex auth JSON：`auth_files/codex_auth_files.tar.gz`，并解压到 `auth_files/extracted/`，文件权限 600、目录权限 700。
- 迁移说明已归档：`docs/sub2api_migration_snapshot_20260609_CN.md`。Redis 未作为权威数据迁移；建议新服务器 Redis 重新初始化，避免旧缓存污染。

## 2026-06-10 新服务器迁移快照

- 线上只读确认：`sub2api` 当前镜像 `zhangtaylor985/sub2api:main-d271fbbf`，app/Postgres/Redis 容器 healthy；生产数据库体量约 `2325 MB`。
- 本地迁移快照目录：`deploy/migration_snapshots/sub2api_migration_20260610T150259Z`，权限 700，已通过 `deploy/.gitignore` 忽略。
- 最新生产 dump：`db/prod_sub2api_pg18_20260610T150259Z.dump`，约 `118M`，SHA256 `296b29b2ede83a16d06c9d8422ec95913bc1f23ab7b36001618f6690891a8d0a`。
- 已覆盖恢复到本地独立 PG18 容器 `sub2api-postgres-restore-pg18`，监听 `127.0.0.1:5434`；本地 PG17 沙盒未覆盖。
- 恢复校验：本地 PG18 恢复库 `public` 表数 79；关键表行数 `users=83`、`api_keys=87`、`groups=8`、`accounts=12`、`account_groups=88`、`cliproxy_legacy_api_key_migration=82`，与线上对照一致。
- `usage_logs` 本地恢复后为 1,255,213；线上恢复后即时对照为 1,255,259。差异 46 行来自生产在 dump 之后继续写入，符合在线只读逻辑备份预期。
- 已打包 Sub2API runtime/data 与 compose：`sub2api_runtime/sub2api_runtime_data_and_compose.tar.gz`；实时日志 `data/logs/*` 已排除。
- 已打包生产 `.env`、`docker-compose.yml` 和 Caddyfile：`sub2api_runtime/deployment_config_env_compose_caddy.tar.gz`。
- 已收集并打包 50 个 Codex auth JSON：`auth_files/codex_auth_files.tar.gz`，并解压到 `auth_files/extracted/`，文件权限 600、目录权限 700。
- 迁移说明已归档：`docs/sub2api_migration_snapshot_20260610_CN.md`。Redis 未作为权威数据迁移；建议新服务器 Redis 重新初始化，避免旧缓存污染。

## 2026-06-10 生产 502 代理认证失败排查

- 用户截图中的错误为 Claude/Codex 客户端显示的 `API Error: 502 Upstream request failed`，来自 `cc.claudepool.com` 网关的黑盒上游错误包装。
- 线上只读确认：`sub2api` 当前镜像 `zhangtaylor985/sub2api:main-d271fbbf`，app/Postgres/Redis 容器 healthy，宿主机 `/health` 返回 ok；不是整个 Sub2API 或 Caddy 入口故障。
- 最近 6 小时 `ops_error_logs` 显示 502 集中在 OpenAI/Codex 请求，尤其是 `account_id=8` 的 `gpt-5.5`、`account_id=6` 的 `claude-opus-4-8/gpt-5.5`、`account_id=9` 的 `claude-opus-4-7` 等。
- 文件日志反查样本 request id 后，真实错误均为访问 `https://chatgpt.com/backend-api/codex/responses` 时 SOCKS5 代理认证失败：`username/password authentication failed`。
- 管理端里唯一 `status=error` 的账号是 `account_id=11`，原因是 OAuth token revoked；但本次截图对应的 502 不是该账号导致，而是多个仍为 `active/schedulable` 的账号在运行时被坏代理击中。
- 代理只读快照：`proxy_id=4/5/6/7` 均为 SOCKS5 且状态仍是 `active`，分别绑定活跃账号 `4,5`、`6,7`、`8,9`、`10`；其中 4/5/6/7 对应账号最近都有 502 样本。
- 结论：当前“账号状态正常但请求 502”的直接原因是代理层认证失败未体现在账号 `status=error`；短期处置应更新/替换这些代理凭据，或临时下线坏代理绑定的账号/代理，避免调度继续打到坏链路。

## 2026-06-10 生产 SOCKS5 代理续费后更新验证

- 用户提供 4 条续费后的代理文本；本次只将这些代理的 `protocol` 更新为 `socks5` 并刷新对应用户名/密码，不在文档记录密钥明文。
- 更新对象：`proxy_id=2` (`69.3.236.211:443`)、`proxy_id=6` (`207.97.135.24:443`)、`proxy_id=7` (`206.135.155.102:443`)、`proxy_id=8` (`206.135.155.208:443`)。
- 更新前备份保存在生产机 `/root/cliapp/sub2api/ops_backups/proxies_before_socks_refresh_20260610T144540Z.psv`。
- 验证方式：在生产机使用 `curl --socks5-hostname` 分别连 `https://chatgpt.com/backend-api/codex/responses` 和 `https://api.ipify.org`。4 条代理均通过 SOCKS5 认证；ChatGPT endpoint 返回 HTTP 405，说明已成功连到目标但探测方法不匹配 endpoint；`api.ipify.org` 返回 HTTP 200 且出口 IP 与代理 IP 一致。
- 更新后日志复核：未发现这 4 条新文本代理继续出现 `username/password authentication failed`。
- 额外发现：`proxy_id=4` (`38.125.1.28:443`) 和 `proxy_id=5` (`38.75.200.116:443`) 不在用户本次文本中，但仍绑定活跃账号 `4,5` 和 `6,7`，并在更新后继续产生 SOCKS5 认证失败日志。若用户仍看到 502，应优先处理这两个旧代理或其绑定账号。

## 2026-06-10 生产代理收敛与账号均摊

- 用户明确要求把当前账号平均分摊到 4 个续费后的 IP，并删除此前没有用的旧代理，只保留这 4 个。
- 执行前备份：账号代理分布备份 `/root/cliapp/sub2api/ops_backups/accounts_proxy_distribution_before_20260610T144825Z.psv`；代理表收敛备份 `/root/cliapp/sub2api/ops_backups/proxies_before_keep_four_20260610T145017Z.psv`。
- 账号均摊结果：`proxy_id=2` (`69.3.236.211`) 绑定 active+schedulable 账号 `2,3,4`；`proxy_id=6` (`207.97.135.24`) 绑定 `5,6`；`proxy_id=7` (`206.135.155.102`) 绑定 `7,8`；`proxy_id=8` (`206.135.155.208`) 绑定 `9,10`。`account_id=11` 仍为 `status=error/schedulable=false`，不计入可调度均摊。
- 旧代理收敛结果：未删除代理表当前只剩 `proxy_id=2/6/7/8` 四条 active SOCKS5 代理；旧坏代理 `proxy_id=4/5` 与无账号 HTTP 代理 `proxy_id=9` 已软删除并设为 inactive；更早的 `proxy_id=1/3` 原本已是软删除状态。
- 因更新账号 proxy_id 后运行日志仍短暂命中旧代理，已重启 Docker app 容器 `sub2api` 刷新运行态缓存；Postgres/Redis 未重启。
- 重启验证：宿主机和公网 `/health` 均返回 ok，`sub2api` 容器 healthy；重启后用户请求样本出现 `status_code=200`，且重启完成后的时间窗未再检索到旧代理 IP 或 `username/password authentication failed`。

## 2026-06-13 生产迁移到新机器

- 新机器入口：`ssh -p 41012 root@172.247.109.38`，主机名 `C20260613138680`，系统为 Ubuntu 24.04.1 LTS，支持 systemd。
- 旧生产入口：`ssh root@204.168.245.138`，主机名 `PG-01`，当前仍由 Docker Compose 托管 `sub2api`、`sub2api-postgres`、`sub2api-redis`。
- 迁移策略：新机器不用 Docker，采用宿主机 PostgreSQL 18、Redis、Caddy 和 systemd 托管 Sub2API 二进制；正式切换前重新做最终 PostgreSQL dump，并迁移 `/root/cliapp/sub2api/data` 与 Codex auth JSON。
- `cc.claudepool.com` 的 Cloudflare DNS A 记录已从旧机 `204.168.245.138` 直切到新机 `172.247.109.38`；本机解析会被本地代理返回 fake IP，不作为切换依据。
- 回滚边界：DNS 切换后保留旧机 Docker Compose，不清理旧库和旧容器；若新机 smoke 或真实流量异常，可把 DNS A 记录切回 `204.168.245.138`。
- 最终迁移 dump：旧机停 app 后生成 `/root/cliapp/sub2api/final_migration/prod_sub2api_final_20260613T054258Z.dump`，恢复到新机 `/root/cliapp/sub2api-migration/prod_sub2api_final_20260613T054258Z.dump`。
- 最终恢复校验：新机 `users=83`、`api_keys=88`、`groups=8`、`accounts=13`、`account_groups=89`、`usage_logs=1309290`，与停写后旧机 dump 前读数一致。
- Codex auth JSON 已从旧机迁到新机，共 51 个候选文件，权限已收紧到目录 700、文件 600。
- 新机本地 `/health` 和 `/v1/models` 认证 smoke 通过；公开 `https://cc.claudepool.com/health` 和公开 `/v1/models` 认证 smoke 也通过。
- 通过 Cloudflare API MCP 已将 `cc.claudepool.com` A 记录改到 `172.247.109.38`，DNS-only，TTL 自动；新机 Caddy 已成功签发正式证书。旧机 Caddy 仍保留到新机的桥接配置，但 DNS 已不再指向旧机；旧 app 容器保持停止，避免旧库写入分叉。

## 2026-06-13 坏 SOCKS 代理下线与 Codex auth 重新均摊

- `.codex_capi` 使用 `base_url=https://cc.claudepool.com`、`wire_api=responses`，生产 502 对应 `api_key_id=120`、`group_id=14`、`account_id=3`。
- 直接原因是 `proxy_id=2` (`69.3.236.211:443`) SOCKS5 认证失败；生产机使用数据库内代理凭据探测时返回 `User was rejected by the SOCKS5 server`。其余 `proxy_id=6/7/8` 能完成 SOCKS5 连接。
- 已将 `proxy_id=2` 设为 `inactive` 并软删除；备份 tag 为 `codex_proxy_rebalance_20260613T0655Z`，存于生产库 `ops_proxy_rebalance_backup`。
- 7 个 active+schedulable OpenAI OAuth 账号已重新均摊到 3 个健康代理：`proxy_id=6` (`207.97.135.24`) 绑定账号 `3,6,9`；`proxy_id=7` (`206.135.155.102`) 绑定账号 `4,7`；`proxy_id=8` (`206.135.155.208`) 绑定账号 `5,8`。`account_id=2/10/11` 仍为 error 或不可调度，不计入均摊；`account_id=12` 是 apikey worker，不计入 Codex OAuth。
- 已重启新机 `sub2api.service` 刷新运行态缓存。`.codex_capi` `/responses` 连续 5 次 smoke 均返回 completed；切换后 `api_key_id=120` 未再出现新 502，且 `account_id=3` 已在新代理 `207.97.135.24` 上成功请求。

## 2026-06-15 API Key 纯流量包模式与分组

- 根因：`BillingCacheService.evaluateRateLimits` 已支持 token-package-only API Key 的前置检查，但 `buildUsageBillingCommand` 只在 `apiKey.HasRateLimits()` 为真时写入 `APIKeyRateLimitCost`。因此“无 5h/1d/7d 限额但有流量包”的 key 可以通过前置检查，却不会进入 `usage_billing_repo` 的流量包分配逻辑。
- 本地修复：`postUsageBillingParams.shouldUpdateRateLimits(ctx)` 在无 rate limit 时，通过 `APIKeyService.GetTokenPackageState` 判断是否存在 token package；存在时将 `ActualCost` 写入 `APIKeyRateLimitCost`，触发 `api_key_token_packages.used_usd` 与 `api_key_token_package_usage` 后扣。
- 本地测试：新增 unit 覆盖 token-package-only 会写入 `APIKeyRateLimitCost`，无 rate limit 且无流量包的普通 key 不写入；`go test ./...` 已通过。
- 生产分组现状：线上已有多个 `standard/openai` 分组，包括 `Codex Base`、`CP Legacy dedicated/double/triple/quad/ungrouped`，均开启 `allow_messages_dispatch`，每个绑定 10 个未删除 OpenAI 账号，其中 6 个当前 active+schedulable。
- 新增生产分组：已创建 `Codex Token Package Pool` (`group_id=19`)，`platform=openai`、`subscription_type=standard`、日/周/月限额均为 0、`allow_messages_dispatch=true`，沿用 `Codex Base` 的 Claude family 映射，并绑定 10 个未删除 OpenAI 账号。未自动迁移任何 API Key。

## 2026-06-17 API Key 生图权限与 Image 2.0 排查

- 生产只读确认：用户提供的 key 对应 `api_key_id=493`、名称 `沙栎`，绑定 `group_id=16` (`CP Legacy quad`)；该分组为 `platform=openai`、`allow_messages_dispatch=true`，但 `allow_image_generation=false`。
- 因此当前线上 Image 2.0 / `gpt-image-2` 不支持的直接原因是分组生图开关未启用，不是 API Key 已有 GPT 族权限缺失。该 key 当前 `allow_gpt_family=true`，`allow_claude_family=false`。
- `group_id=16` 绑定的 OpenAI OAuth 账号中，active+schedulable 账号存在；账号级 `credentials.model_mapping` 未显式列出 `gpt-image-2`，但本次已在分组级开关处被挡住，尚未进入上游账号能力验证阶段。
- 本地实现新增 `api_keys.allow_image_generation`，默认 true；管理端 API Key 创建/编辑弹窗可维护该字段。运行时图片生成需要 API Key 级和分组级同时允许。

## 2026-06-18 CP Legacy 分组生图开关

- 生产只读确认：用户提供的第二把 key 对应 `api_key_id=495`、名称 `Aiden 邓先生`，绑定 `group_id=14` (`CP Legacy double`)；该分组已经是 `allow_image_generation=true`，所以这把 key 当前在已部署代码下不再被分组生图开关挡住。
- 生产当前尚未部署本地新增的 `api_keys.allow_image_generation` 字段，线上 `api_keys` 表没有该列；本次立即生效的变更是分组级 `groups.allow_image_generation`。
- 已将所有未删除 `CP Legacy %` 分组开启生图：`CP Legacy dedicated`、`CP Legacy double`、`CP Legacy triple`、`CP Legacy quad`、`CP Legacy ungrouped` 均为 `allow_image_generation=true`。
- 写库前已备份旧值到 `ops_group_image_generation_backup`，`backup_tag=cp_legacy_image_generation_20260618T0929Z`；更新后对这些分组下 90 个有效 API Key 执行 Redis auth snapshot 删除和 `auth:cache:invalidate` 广播。

## 2026-06-18 生产 Claude -> GPT 默认映射调整

- 用户截图对应 API Key 级 Claude -> GPT 映射覆盖表单，目标为 Opus `gpt-5.4`、Sonnet `gpt-5.3-codex`；若 API Key 留空，会继承分组级 `groups.messages_dispatch_model_config`。
- 生产只读确认：8 个 active OpenAI 分组的 `sonnet_mapped_model` 已为 `gpt-5.3-codex`，但 `opus_mapped_model` 以及 `claude-opus-4-6/4-7/4-8` exact mapping 仍为 `gpt-5.5`。由于 exact mapping 优先级高于 family default，必须同时调整 exact Opus 项。
- 已更新 8 个 active OpenAI 分组：`Codex Base`、`CP Legacy dedicated/double/triple/quad/ungrouped`、`Codex Token Package Pool`、`CP Dedicated - nguyenvanlinh0208`。更新后 `opus_mapped_model=gpt-5.4`，`sonnet_mapped_model=gpt-5.3-codex`，三个 Opus exact mapping 均为 `gpt-5.4`。
- 变更前已生成生产完整 dump `/root/cliapp/sub2api/ops_backups/prod_sub2api_before_group_opus54_20260618T105611Z.dump`，并备份分组映射行 `/root/cliapp/sub2api/ops_backups/groups_messages_dispatch_before_opus54_20260618T105611Z.psv`。
- 更新后已清理 Redis `apikey:auth:*` 快照，数量从 7 降为 0，并发布 `auth:cache:invalidate`；新机本地 `/health` 返回 ok。

## 2026-06-26 生产 API Key 日限额时区排查

- 线上新机宿主机与 PostgreSQL session 默认时区为 `Etc/UTC`，但 `sub2api.service` 进程环境和 `/etc/sub2api/sub2api.env` 均设置 `TZ=Asia/Shanghai`。因此应用层北京时间是有效的，数据库层 `NOW()` 截日仍默认按 UTC。
- 用户截图对应的 API Key 为 `api_key_id=110`，绑定 `group_id=14` (`CP Legacy double`)。当前 `api_keys.usage_1d=13.77345170`，`window_1d_start=2026-06-26 00:00:00+00`，说明环形“日限额”使用的是 UTC 0 点窗口。
- 同一 key 北京时间 `2026-06-26 00:00:00` 到 `2026-06-27 00:00:00` 的 `usage_logs` 汇总为 `3614` 次请求、`actual_cost=150.67959165`，与截图明细表 `2026-06-26 $150.68` 一致；这不是单纯展示误差。
- 北京时间当天用量集中在 `00:00` 到 `08:35`，最后一条样本为 `2026-06-26 08:35:10`，均为 `/v1/messages`。模型分布：`claude-opus-4-8 -> gpt-5.4` 约 `$114.11`，`claude-4.5-haiku -> gpt-5.4-mini` 约 `$36.57`。
- 代码根因在 `backend/internal/repository/api_key_repo.go`：`IncrementRateLimitUsage` / `ResetRateLimitWindows` 使用 `date_trunc('day', NOW())` 写 `window_1d_start` 和 `window_7d_start`。生产 DB session 为 UTC 时，该 SQL 不会跟随 Go 应用的 `timezone.Init("Asia/Shanghai")`。
- 前端另有潜在边界问题：`frontend/src/views/KeyUsageView.vue` 的 `getDateParams()` 用 `new Date().toISOString().split('T')[0]` 生成默认日期，在 UTC+8 的 `00:00-07:59` 会把“今天”算成 UTC 前一天；本次截图已进入 `2026-06-26` UTC 日，不是这次 $150.68 的主因，但应一并修。

## 2026-06-26 新机 SOCKS5 入口

- 新生产机 `C20260613138680` 已存在 Xray 26.3.27，systemd 服务为 `xray.service`，原配置仅提供 `127.0.0.1:10085` VLESS/WS 入站。
- 本次新增 `public-socks5-in` 入站：监听 `0.0.0.0:26812`，协议 `socks`，认证方式 `password`，账号复用本机 `/Users/taylor/.codex_td/.env` 既有代理凭据；不记录明文。
- Xray 配置备份位于 `/usr/local/etc/xray/config.json.bak.socks5_20260626T035626Z`。如需回滚，可恢复该文件并重启 `xray.service`。
- 验证结果：带认证经新 SOCKS5 访问 `https://api.ipify.org` 出口为 `172.247.109.38`；无认证访问同端口被拒绝，未变成开放代理。
- 本机 `/Users/taylor/.codex_td/.env` 已切换到 `172.247.109.38:26812`；备份为 `/Users/taylor/.codex_td/.env.bak.sub2api_socks5_20260626T035813Z`。

## 2026-07-02 Claude Fable 5 支持

- 官方 Claude 文档确认 `claude-fable-5` 的 Claude API 价格为 input `$10/MTok`、output `$50/MTok`、5m cache write `$12.50/MTok`、1h cache write `$20/MTok`、cache read `$1/MTok`。
- 本地价格目录和生产 `/opt/sub2api/data/model_pricing.json`、`/opt/sub2api/resources/model-pricing/model_prices_and_context_window.json` 均已有 `claude-fable-5`，值分别为 `0.00001/0.00005/0.0000125/0.00002/0.000001` per token。
- 生产当前 8 个 active OpenAI dispatch 分组均已为 Opus `gpt-5.4`，但 `exact_model_mappings.claude-fable-5` 为空；98 个 active API Key 也没有 key 级 Fable exact mapping。
- 由于当前线上已部署代码还不会把 `fable` 识别为 Opus family，立即上线支持可通过给 active OpenAI dispatch 分组增加 `claude-fable-5 -> gpt-5.4` exact mapping，并清理相关 API Key auth snapshot 实现，不需要立即重启 app。
- 本地代码已补长期修复：`fable` 归入 Opus dispatch family、默认模型列表新增 `claude-fable-5`、硬编码 fallback price 使用 Fable 官方价，避免动态 pricing 不可用时误回 Sonnet 价。
- 生产热配置结论：仅给 8 个 active OpenAI dispatch 分组和 96 个 active API Key 增加 `exact_model_mappings.claude-fable-5=gpt-5.4` 后，旧二进制仍会把 `claude-fable-5` 原样发给 Codex 上游，smoke 返回 400/502；因此 Fable 支持必须包含代码发布。
- 上线结果：当前线上二进制 sha256 为 `eda55842f08cdcbd10c935a3b0baf5bc9e3cc35b42f4a36b9740d5ab81570f7e`；备份包括 `/opt/sub2api/sub2api.bak.20260702T0620Z-before-fable5` 和 `/opt/sub2api/sub2api.bak.20260702T0654Z-before-fable5-billing-fix`。
- 生产 DB 备份：分组映射备份 `/root/cliapp/sub2api/ops_backups/groups_messages_dispatch_before_fable5_20260702T040807Z.psv`；API Key 映射备份 `/root/cliapp/sub2api/ops_backups/api_keys_messages_dispatch_before_fable5_20260702T041113Z.psv`。
- 最终 smoke：`claude-fable-5` `/v1/messages` 返回 HTTP 200 和 `FABLE_SMOKE_OK`；usage 记录 `requested_model=model=claude-fable-5`、`upstream_model=gpt-5.4`、`model_mapping_chain=claude-fable-5→gpt-5.4`。
- 最终计费证据：smoke 行 input_tokens=120、output_tokens=19，`input_cost=0.0012000000`、`output_cost=0.0009500000`，即按 Fable `$10/MTok` 和 `$50/MTok` 计算，不再按 `gpt-5.4` 的 `$2.5/$15 MTok` 成本价计算。

## 2026-07-06 关闭 Claude Fable 5 支持

- 当前线上在关闭前已运行 7 月 4 日 token package 版本，binary sha256 为 `4270663a398e8dc04bf868872095d69821d24ba718ba0ea7f07711be3360e37e`，不能直接回滚 7 月 2 日 Fable 备份，否则会丢后续功能。
- 本地关闭点：移除 `DefaultModels` 中的 `claude-fable-5`、移除 `fable -> opus` dispatch family、移除 Fable 专用 fallback price、移除 OpenAI `/v1/messages` Fable requested-model 计费 override；保留历史 usage/pricing 识别，避免旧账单统计退化。
- 已把 Fable 开启/关闭流程补入 `.codex/skills/sub2api-local-binary-deploy/SKILL.md`，明确 code/binary 与 production DB exact mapping 两层都要处理。
- 本地关闭版 binary sha256 为 `f9d4f750210ce12cbf960d70ae4b119e2bd2bad790dfb27eec8b4326260db259`；基于当前线上 `427066...` 生成 patch `sub2api-linux-amd64-20260706T042353Z-disable-fable-from-4270663.zst`，sha256 为 `f8772dc41958daacb4cbe40ab904968b1e0f6afd28aefd64e7293c4b28decee0`。
- 生产 binary 备份：`/opt/sub2api/sub2api.bak.20260706T0425Z-before-disable-fable`；当前 `/opt/sub2api/sub2api` sha256 为 `f9d4f750210ce12cbf960d70ae4b119e2bd2bad790dfb27eec8b4326260db259`。
- 生产 DB 映射备份：`/root/cliapp/sub2api/ops_backups/groups_messages_dispatch_before_disable_fable_20260706T042900Z.psv` 和 `/root/cliapp/sub2api/ops_backups/api_keys_messages_dispatch_before_disable_fable_20260706T042900Z.psv`。
- 已移除 8 个 active OpenAI dispatch 分组和 96 个 active dispatch API Key 的 `exact_model_mappings.claude-fable-5`；更新语句覆盖 8 个分组和 103 个 active dispatch key，复核 Fable exact mapping 均为 0。
- 已清理并广播 103 个 active dispatch key 的 auth cache，Redis `apikey:auth:*` 剩余 0。
- 验证结果：`/v1/models` HTTP 200 且不含 `claude-fable-5`；Fable smoke 返回 502 泛化上游错误且 `2026-07-06 04:29:00+00` 后 Fable 成功 usage 为 0；`claude-opus-4-8` smoke HTTP 200 并返回 `OPUS_OK`；本机和公网 `/health` 均正常。

## 2026-07-06 Claude Fable 5 显式开关本地实现

- 新策略：Fable 5 默认关闭，只通过 `exact_model_mappings.claude-fable-5=gpt-5.4` 显式开启；不新增 DB schema，不把 Fable 恢复为全局默认模型，也不把 `fable` 归入 Opus/Sonnet/Haiku family 默认路由。
- 单 API Key 开关：Admin API Key 创建/编辑弹窗新增 `允许 Claude Fable 5`，开启时写入该 key 的 `messages_dispatch_model_config.exact_model_mappings`，关闭时删除该 key 的 Fable exact mapping；列表“模型与能力”会显示 `Fable 5`。
- 分组/全局开关：Admin Groups 的 OpenAI Messages dispatch 配置中新增 `分组开启 Claude Fable 5`，开启时写入 OpenAI 分组 exact mapping；该分组下未单独覆盖的 API Key 会继承分组配置。
- 后端模型列表：OpenAI dispatch 的 Claude-only `/v1/models` 仅在当前 API Key 解析 `claude-fable-5` 有目标模型时追加 `Claude Fable 5`；默认状态仍不展示 Fable。
- 计费：保留 `PricingService` 对 Fable dynamic pricing 的识别，移除硬编码 fallback 的状态不变。OpenAI usage 计费候选会优先尝试 requested model `claude-fable-5`；动态价格存在时按 Fable 价，缺失时可继续落到上游 `gpt-5.4` 候选，避免 0 成本或阻断。
- Skill 已更新：`.codex/skills/sub2api-local-binary-deploy/SKILL.md` 改为说明当前 UI 优先、DB 备用的 Fable 开关方案。
- 本地验证通过：targeted Go 测试、`go test ./internal/pkg/apicompat ./internal/service ./internal/handler -run ...`、`go test ./...`、`./node_modules/.bin/vue-tsc --noEmit`、targeted eslint、`./node_modules/.bin/vite build`、Skill validator、`git diff --check`。
- 本次未部署生产、未修改生产 DB、未记录 raw API key。

## 2026-07-06 Claude Fable 5 单 Key 生产开启

- 目标 key 对应 `api_key_id=110`、`group_id=14` (`CP Legacy double`)；本次只给该 key 写入 `api_keys.messages_dispatch_model_config.exact_model_mappings.claude-fable-5=gpt-5.4`，未给任何 group 开启 Fable。
- 生产映射备份：`/root/cliapp/sub2api/ops_backups/api_key_messages_dispatch_before_enable_fable_single_20260706T0706Z.psv`。
- 当前线上 binary sha256 为 `c6f16bc9d0a0e67e833fb81c99d0991bf5aeb471d97ecd5d60e8aa01fcdcbcc7`；主要回滚点包括 `/opt/sub2api/sub2api.bak.20260706T0705Z-before-fable-explicit-switch`、`/opt/sub2api/sub2api.bak.20260706T0712Z-before-fable-body-map`、`/opt/sub2api/sub2api.bak.20260706T0719Z-before-fable-billing`。
- 上线中发现并修复两个生产级问题：第一，dispatch 映射只用于选账号，未改写 `/v1/messages` 转发 body，导致上游拒绝 `claude-fable-5`；第二，body 改写后 usage 的第一计费候选变成 `gpt-5.4`，需对 `requested_model=claude-fable-5` 优先按 Fable 动态价计费。
- 最终权限范围：`groups_with_fable=0`、`active_keys_with_fable=1`、`target_key_with_fable=1`、`non_target_keys_with_fable=0`。
- 最终 smoke：目标 key `/v1/models` 包含 `claude-fable-5`；`claude-fable-5` `/v1/messages` HTTP 200，响应模型 `gpt-5.4`，usage 记录 `requested_model=claude-fable-5`、`model=gpt-5.4`、`model_mapping_chain=claude-fable-5→gpt-5.4`。
- 最终计费：最新 smoke 行 `input_tokens=117`、`output_tokens=5`，费用 `0.0011700000 + 0.0002500000 = 0.0014200000`，符合 Fable `$10/$50 MTok` 动态价格。

## 2026-07-07 Claude -> GPT 用户侧模型黑盒

- 用户侧报错 `Session model gpt-5.4 could not be restored` 的直接原因是 Anthropic `/v1/messages` 响应中的 `model` 被回传成内部上游模型 `gpt-5.4`；Claude Code 不认识该模型作为 Claude session model，因此恢复会话时回退默认模型。
- 代码根因是 7 月 6 日 body-map 修复把 handler 中的转发 body 预先改写成 `gpt-5.4`，但服务层 `ForwardAsAnthropic` 只从传入 body 读取 `originalModel`，于是失去原始 Claude 请求模型。
- 修复策略：上游请求体继续使用内部映射模型，保证 OpenAI/Codex 账号可处理；客户端 display model 单独传递，Anthropic 响应体和 SSE `message_start.message.model` 始终使用原始 Claude 请求模型。
- 后台可观测性保留：`OpenAIForwardResult.UpstreamModel` 仍记录 `gpt-5.4`，usage/channel mapping 仍可保留 `model_mapping_chain`，但用户客户端不再看到 GPT/Codex 模型名。
- 本地回归覆盖非流式和流式两种响应，均验证上游请求 body 为 `gpt-5.4`、客户端响应不含 `"model":"gpt-5.4"`。
- 第一版生产 hotfix `4271f787...` 修复了用户侧模型泄漏，但暴露 Fable 计费 helper 依赖 `result.Model != requested` 的旧假设；第二版 `b0239cc6...` 改为在 requested 为 `claude-fable-5` 且 `upstream_model` 为内部 GPT 时按 Fable requested model 计费。
- 当前线上 binary sha256 为 `b0239cc6eb366fbc7257fe978bf0763777eef3ab900f80aa36af669c7ea83a69`；回滚点包括 `/opt/sub2api/sub2api.bak.20260707T0832Z-before-display-model-blackbox` 和 `/opt/sub2api/sub2api.bak.20260707T0840Z-before-display-model-billing`。
- 最终验证：`api_key_id=111` 的 `claude-fable-5` / `claude-opus-4-8` 非流式 smoke 都返回 HTTP 200，响应模型保持 Claude 且不含 `gpt-`；流式 Opus smoke 的 `message_start.model=claude-opus-4-8`，SSE 不含 `gpt-`。
- 最新 Fable usage `2589430`：`model=claude-fable-5`、`requested_model=claude-fable-5`、`upstream_model=gpt-5.4`、`model_mapping_chain=claude-fable-5→gpt-5.4`，`input_cost=0.0012000000`、`output_cost=0.0008500000`，已恢复 Fable 动态价。

## 2026-07-07 `claude -r` 实时复线与 Fable 默认映射

- `cc.claudepool.com` 当前 display-model hotfix 在新会话与恢复会话上有效：临时配置目录中的 Claude CLI 非交互和真实 TTY 测试均未写入 `gpt-5.4`，session JSONL 的 assistant `message.model` 保持 Claude 模型。
- 用户“第一次遇到”仍可能来自今天早些时候的污染窗口：一旦某个 session 在修复前收到过 `model=gpt-5.4`，用户第一次执行 `claude -r` 时才会看到恢复 warning；服务端修复无法远程改写用户本机已有 session JSONL。
- 本次发现第二个生产问题：Fable 关闭/显式开关策略使部分 OpenAI dispatch 分组没有 `claude-fable-5` mapping；当 Claude Code 恢复失败后回退 `claude-fable-5`，这些 key 会把 Fable 原样发给 ChatGPT/Codex 上游并失败。
- 已对现有 8 个 OpenAI dispatch 分组补 `exact_model_mappings.claude-fable-5=gpt-5.4`，并上线代码默认映射 Fable 到 `gpt-5.4`；因此新 session 和恢复 fallback 都能保持用户侧 Claude 黑盒，内部 GPT 只保留在 usage 的 `upstream_model` / `model_mapping_chain`。
- 生产 post-deploy 证据：`api_key_id=494` 的 `claude-fable-5` 客户端响应为 `model=claude-fable-5` 且不含 GPT/ChatGPT/Codex；Claude CLI 新建 + resume 成功，精确搜索无 `Session model`、`could not be restored`、`gpt-5.4`。

## 2026-07-10 GPT/Codex 5.6 支持

- 原版在提交 `6cea1c35bb0e4a86ab6b00370e9cede9540da8de` 支持三个精确 ID：`gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-5.6-luna`；不支持裸 `gpt-5.6`。
- 当前 fork 未识别 5.6 时会落入通用 GPT-5 兼容兜底并被改写为 `gpt-5.4`，因此仅增加静态模型列表不足以支持真实请求。
- 原版提交中的 1.05M context 与三款统一价格已经落后；当前生产价格源 `Wei-Shaw/model-price-repo` main `e23618b20e6cba83e2528e365e5c7349ae8a03dc` 为 400K input、128K output，且 Sol/Terra/Luna 三档价格不同。
- 原版后续提交 `13e773ef5e7908b0af0f2938295775b38a26eaaa` 增加 Codex manifest 透传；当前 fork 必须额外遵守 API Key 模型族策略，Claude-only key 即使带 `client_version` 也不能看到 GPT manifest。
- 当前生产 7 个 active+schedulable OpenAI 账号中，5 个未配置 model mapping，可直接参与三款 5.6 调度；2 个受限账号尚无 5.6 mapping。大部分 OpenAI 分组有 5 个可用账号，专用分组 `group_id=30` 当前没有 5.6 eligible account，需在 live manifest 确认权限后再补精确 mapping。
- 当前线上 binary sha256 `4ede9ae80a924d11f6396f71d922a0837af948f3d96d1b9368f624c55ef75bf4` 来自当前 dirty 源码快照；从干净 `HEAD` 构建会回退已上线功能，本次必须基于现有工作区增量构建。
- 最终支持范围是三个精确 ID；裸 `gpt-5.6` 和 near-miss 不会落回 `gpt-5.4`。兼容别名只归一到对应 Sol/Terra/Luna 精确档位。
- 定价采用当前生产源：Sol `$5/$30`、Terra `$2.5/$15`、Luna `$1/$6` 每 MTok（input/output）；priority 为 2 倍，超过 272K input 时 input/cache 2 倍、output 1.5 倍。context 为 400K input / 128K output。
- 账号 `6/8` 的 free entitlement 实测不支持 5.6，已配置 `model_exclusions=["gpt-5.6-*"]`；账号 `14/15` 配置三个 identity mapping，账号 `3/4/9` 继续使用空 mapping。scheduler metadata 会保留 exclusions/mapping 且不带 token。
- OAuth 重新授权现在只在授权入口合并并保留 `model_mapping`、`model_exclusions`、compact mapping 等策略；普通账号编辑仍保持原覆盖语义。
- Codex 在线 manifest 透传受 API Key family 权限保护；最终上游 manifest 返回 8 个模型，其中三个为 5.6，Claude-only key 仍为 404。
- GPT-5.6 上游在发布期间把最低 Codex 身份从旧探测值提高到 `0.144.x`；官方 npm stable 为 `@openai/codex 0.144.1`，直接 HTTP/WS 上游均验证成功。实现对精确 5.6 HTTP 和所有 OpenAI OAuth WS 施加 `0.144.1` floor，并保留更高的严格三段版本；API Key、非 5.6 HTTP 与 Claude `/v1/messages` 不受该 floor 影响。
- 所有生产 OAuth 账号当前显式 `openai_oauth_responses_websockets_v2_mode=off`，因此不为 canary 临时改共享配置；候选的 WS 连接复用和会话切模由单测/race 覆盖，真实账号凭据直连上游 WS 的 Luna 请求返回 completed。
- 最终线上 binary 为 `/opt/sub2api/sub2api`，sha256 `11125e55a14aaa0a8423011c23170cc4f6034c9a2a978ada44a377bd60ace9db`；回滚备份 `/opt/sub2api/sub2api.bak.20260710T050538Z-before-gpt56-stable-01441`，上一稳定 SHA 为 `43009b024401688561698c1426c6fbb5efc3220923f9e595352f0abb5fab4b2a`。
- 发布后公网三款 5.6 与 Luna SSE 均成功；真实 `claude2 2.1.174` 返回 `CLAUDE2_PROD_01441_OK`，debug-file 证实命中公网 `/v1/messages` 并收到 stream first chunk。Opus/Fable 用户侧模型保持 Claude，未出现 GPT/OpenAI/Codex 泄漏。
- 生产 usage `2634987/2634988/2634989/2634998/2634999` 的 Sol/Terra/Luna input/output 费用逐条符合对应档位，费用 mismatch 为 0；发布后 5.6 未命中账号 `6/8`。Claude usage 继续保留既有 `claude-opus-4-7/claude-fable-5→gpt-5.4` 内部映射。

## 2026-07-08 API Key 纯流量包 `quota_exhausted`

- 用户反馈的 key 对应生产 `api_key_id=147`，名称 `Mr 椰子`，绑定 `Codex Token Package Pool`。
- 只读确认该 key `token_package_required=true`，流量包总额 `2000.00000000`，已用 `437.93789465`，剩余 `1562.06210535`；传统 quota 为 `1400.00000000`，`quota_used=1404.29324415`，状态为 `quota_exhausted`。
- 代码根因：认证中间件在 token package eligibility 前先按 `status=quota_exhausted` 和 `IsQuotaExhausted()` 返回 `API_KEY_QUOTA_EXHAUSTED`；后扣命令在 `Quota>0` 时仍写 `APIKeyQuotaCost`，导致纯流量包请求继续推进传统 quota 状态。
- 修复口径：`token_package_required=true` 时跳过传统 quota 状态/数值拦截和钱包余额 fallback；后扣只写 token package/rate-limit ledger，不再写传统 API key quota。

## 2026-07-14 Claude Sonnet 5 现状

- 当前 `HEAD` 和 dirty worktree 的 `backend/internal/pkg/claude/constants.go` 已包含 `claude-sonnet-5`，`GatewayHandler.Models` 的 Claude-only OpenAI dispatch 回归也已断言展示该模型。
- `backend/internal/service/openai_messages_dispatch.go` 的 Sonnet family 代码默认已是 `gpt-5.4`，但 `frontend/src/views/admin/groupsMessagesDispatch.ts` 的新建/缺省 UI 默认仍是 `gpt-5.3-codex`，会把旧值持久化为显式覆盖。
- 前端 `useModelWhitelist.ts` 尚未包含 Sonnet 5 的 Anthropic whitelist/preset 和 Bedrock preset；后端 `DefaultBedrockModelMapping` 也缺 `claude-sonnet-5 -> us.anthropic.claude-sonnet-5-v1`。
- 上游参考提交为 `db0414233ce324903adc72e858374086da158b4b` (`feat: 适配 sonnet5`)；除模型列表和 Bedrock 映射外，还将 `context-1m-2025-08-07` 改为仅 Sonnet 5 及其合法变体放行、其他模型 fallback filter。
- 生产 2026-07-14 只读快照：8 个 active OpenAI dispatch 分组的 `sonnet_mapped_model` 均为 `gpt-5.4`；107 个 active dispatch key 中 24 个继承分组，83 个显式保留旧默认 `gpt-5.3-codex`。
- 生产近 30 天已有 397 条 `claude-sonnet-5` usage，全部为 `claude-sonnet-5→gpt-5.4`；最新样本的用户侧 `model/requested_model` 保持 `claude-sonnet-5`，`upstream_model=gpt-5.4`，计费按 GPT-5.4 实际上游价格记录。
- 生产 `settings` 表当前没有持久化 `beta_policy_settings`，因此发布后新的代码默认 beta policy 会直接生效，不需要额外改该 setting。
- 生产基线正常：`sub2api.service=active`，内网/公网 health 均 ok，当前 binary sha256=`11125e55a14aaa0a8423011c23170cc4f6034c9a2a978ada44a377bd60ace9db`。

## 2026-07-14 Claude Sonnet 5 发布结论

- 代码与产品能力已补齐：前端 Anthropic/Bedrock whitelist 与 preset、Bedrock 默认模型映射、Sonnet 5 专属 `context-1m-2025-08-07` 默认策略，以及 OpenAI messages dispatch UI 的 Sonnet→`gpt-5.4` 默认值。
- 本地门禁全部通过：协议组合测试、`go test ./...`、`go test -tags=unit ./...`、相关 `-race`、frontend lint/typecheck/build、embed 测试和 `git diff --check`。本地无有效 OpenAI OAuth 账号时，失败响应保持通用 503，未泄漏内部模型。
- 隔离 canary 使用候选 binary sha256=`27efc96830124c393a3caf839270bf613cc2b79b3b31e0eac328561fcf34eeab`，仅监听远端 `127.0.0.1:18080`。Sonnet 5 非流式、SSE、Claude Code 非交互和真实 TTY 同会话双轮均成功，客户端只显示 `claude-sonnet-5`。
- canary 产生 7 条 Sonnet 5 usage，全部记录 `requested_model/model=claude-sonnet-5`、`upstream_model=gpt-5.4`、`model_mapping_chain=claude-sonnet-5→gpt-5.4`；总成本与实际成本一致。
- 发布前完整 PG dump 为 `/opt/sub2api/backups/sub2api-before-sonnet5-gpt54-20260714T095120Z.dump`，大小 264358622 字节，sha256=`7d4094d6b3cce73752de1cf82bcd4ef5327a74f9e9417d0e5c3de525ad44bc06`，并通过 `pg_restore -l` 校验；分组和 key 配置另有 CSV 行备份。
- 生产配置迁移仅命中 84 个未删除且 `sonnet_mapped_model` 恰为旧默认 `gpt-5.3-codex` 的 key，删除该单字段并失效 84 个 auth cache；迁移后 108 个未删除 key 均继承组配置，8/8 个 active OpenAI dispatch 组为 Sonnet→`gpt-5.4`，未改其他自定义字段。
- 正式 binary 已切换到 sha256=`27efc96830124c393a3caf839270bf613cc2b79b3b31e0eac328561fcf34eeab`；旧 binary 备份为 `/opt/sub2api/sub2api.bak.20260714T095346Z-before-sonnet5-gpt54`，sha256=`11125e55a14aaa0a8423011c23170cc4f6034c9a2a978ada44a377bd60ace9db`。
- 发布后公网模型清单、非流式、SSE、1M context beta 和全新配置的正式 `claude2 2.1.174` 均通过；4 条正式 usage 全部正确映射并按 GPT-5.4 实际成本计费。`sub2api.service` 为 `active/running`、`NRestarts=0`，内网/公网 health 正常，硬错误计数为 0。
- 启动时远程价格仓库发生一次 DNS timeout，但服务使用本地价格数据正常启动，实际 usage 成本逐条一致；该网络噪音不影响本次发布。远端 canary、18080 监听、本机隧道、本地服务及本次启动的本地依赖容器均已停止。

## 2026-07-21 生产 Codex `/responses` 断连初始事实

- 用户截图显示 Codex CLI `0.144.6`，模型为 `gpt-5.6-sol high`，首次发送“你好”后进入 `Reconnecting... 1/5`。
- 客户端错误为 `Stream disconnected before completion: error sending request for url (https://cc.claudepool.com/responses)`；它说明流式 HTTP 请求在完成前断开，但尚不能区分 Caddy/Cloudflare、Sub2API 进程、上游 OpenAI/Codex 或客户端网络。
- 用户配置入口为 `base_url = "https://cc.claudepool.com"`，实际请求路径是 `/responses`，不是 `/v1/responses`。
- 当前任务先执行生产只读检查；任何服务重启、配置或数据修改需另行确认。
- 2026-07-21 09:41 CST 基线：公网 `/health` HTTP 200，未认证 `/responses` HTTP 401；新机 `sub2api/caddy/postgresql/redis-server` 均 active，Sub2API 自 2026-07-14 09:53 UTC 启动以来 `NRestarts=0`，内网 health 正常，正式 binary sha256=`27efc96830124c393a3caf839270bf613cc2b79b3b31e0eac328561fcf34eeab`。
- 同期应用日志大量出现 `socks connect tcp 54.176.138.113:10808->chatgpt.com:443: dial tcp ... connect: connection timed out`；受影响 `/responses` 请求在约 138–143 秒后返回 502，账号样本包括 `account_id=4/9`。
- 同期 `account_id=3` 的 `gpt-5.6-sol` `/v1/responses` 请求有连续 HTTP 200，说明公网/Caddy/Sub2API 与模型能力不是整体失效，疑似特定上游代理或绑定该代理的账号故障。
- 用户截图对应的新请求已命中 `/responses`：HTTP request id `58da64ff-3d5d-4db1-96cd-e31bbaf048e0`、`api_key_id=495`、`group_id=14`、`model=gpt-5.6-sol`、stream=true；需继续等待/查询终态。
- 截图请求终态：调度到 `account_id=4`，133903 ms 后记录 502；根错误为从新生产机连接 `54.176.138.113:10808` 超时。`ops_error_logs` 已落同一 request id。
- 账号拓扑：`account_id=4/9` 均绑定 `proxy_id=11` (`54.176.138.113:10808`) 且仍为 active+schedulable；`account_id=3/6/8` 为 DIRECT，`account_id=15` 绑定 `proxy_id=10` (`54.241.144.215:10808`)。目标 group 14 同时绑定这 6 个账号。
- 从新生产机无凭据 TCP 探测：`54.176.138.113:10808` timeout，`54.241.144.215:10808` reachable；从本机两者均 reachable，说明坏节点并非全网端口关闭，更像是对新生产机路径/源 IP 的网络策略或路由问题。
- 近 60 分钟账号 4/9 分别产生 60/52 条 5xx；账号 3 同期有大量成功 usage，包括 11 条 `gpt-5.6-sol`，最新成功到 01:40 UTC。成功/失败分布与代理绑定完全一致。
- `api_key_id=495` 近 30 分钟仅见截图对应这一条 502，没有成功 usage；未执行任何生产变更。

## 2026-07-21 坏代理下线与恢复验证

- 用户明确确认：所有绑定坏代理 `proxy_id=11` (`54.176.138.113:10808`) 的账号都改为不使用代理。
- 变更前再次确认该代理仅绑定两个未删除账号：`account_id=4` (`hoangnga21091996@gmail.com`) 与 `account_id=9` (`anhduc250391@gmail.com`)。
- 原子变更于 2026-07-21 01:49:50 UTC 完成：两账号 `proxy_id` 均从 11 改为 NULL，状态继续 active+schedulable；未删除 proxy 11 记录，未重启服务。
- 最小回滚快照保存在 `ops_proxy_rebalance_backup`，backup tag=`proxy11_to_direct_20260721T014950Z`，仅记录 id、旧 proxy_id 和旧 updated_at，不复制 credentials。
- 写入 scheduler outbox 事件 id=272174、payload=`account_ids:[4,9]`；Redis outbox watermark 后续为 272187，证明已消费并刷新账号/分组快照。
- 变更后四次服务器内 `/responses` SSE smoke 全部 HTTP 200 + `response.completed`，均命中账号 4，耗时 1.1–2.2 秒；与变更前该账号约 134 秒代理超时形成直接对照。
- 真实生产流量确认账号 4/9 都已直连成功：截至 02:00 UTC，变更后分别有 35/42 条成功 usage。最后残留代理超时都来自 01:49:50 UTC 之前已开始的在途请求；01:52 UTC 后坏代理日志计数为 0。
- 用户提供的测试 key 通过公网 `https://cc.claudepool.com/responses` smoke：HTTP 200、完整 `response.completed`、无 `response.failed`；该请求命中账号 3。公网 health 正常，service active/running，`NRestarts=0`。
- 本机 `.codex_capi` 真实 Codex CLI 在无显式代理时请求未到服务器；显式使用本机 Clash `127.0.0.1:7890` 的 HTTP/SOCKS 代理后请求到达服务器，账号 4/9 均返回 HTTP 200，但客户端仍报告 SSE stream disconnected。该现象与生产账号代理无关，属于本机 Clash/Codex 传输 caveat；没有继续扩大线上变更。

## 2026-07-26 Claude → GPT-5.6 初始事实

- 当前生产 binary sha256 为 `27efc96830124c393a3caf839270bf613cc2b79b3b31e0eac328561fcf34eeab`，服务 active、`NRestarts=0`、内网 health 正常。
- 当前 dirty 源码已支持三个直接 GPT-5.6 精确模型；本任务的 Claude dispatch 目标选择最高档 `gpt-5.6-sol`，不使用裸 `gpt-5.6`。
- 生产 8 个 active OpenAI dispatch 分组的 Opus/Sonnet family 与 Opus 4-6/4-7/4-8 exact mapping 均为 `gpt-5.4`。
- 111 个 active API Key 中，84 个 key 级 `opus_mapped_model=gpt-5.4`，27 个 Opus 继承；Sonnet 全部继承。全局默认要真实生效，必须把冗余 5.4 覆盖安全迁移为继承。
- 生产 active+schedulable OpenAI OAuth 账号中，账号 3/4/9 为 unrestricted，账号 15/16 显式允许三款 5.6；账号 6/8 已明确排除 `gpt-5.6-*` 且不可调度。
- 近 7 天已有 26703 条直接 GPT-5.6 usage，同时存在 429/403/502 错误；这些错误混合了账号 entitlement、限流和历史代理问题，不能代替同账号本地 A/B。
- Claude → Responses 当前转换将 `output_config.effort` 的 `low/medium/high` 原样传递、`max` 转为 `xhigh`；未传时当前默认 `medium`。
- `cc2` 实际为 `CLAUDE_CONFIG_DIR=~/.claude_cc claude2 --dangerously-skip-permissions`，Claude Code 版本 `2.1.174`；CLI 支持 `--effort`、`--debug-file`、`--resume` 和真实交互会话。
- 本地 8080 当前无服务；既有 `sub2api-postgres-local` / `sub2api-redis-local` 容器已停止，可在确认后恢复独立沙盒。
- 生产 OpenAI OAuth 凭据实际存储于 PostgreSQL `accounts.credentials`，不是独立 auth file。安全本地验证应只导出短时 access token，去除 refresh token，并关闭本地 token refresh。

## 2026-07-26 Claude → GPT-5.6 A/B 结论

- 同一账号直连 A/B 共 16/16 通过：`gpt-5.4` / `gpt-5.6-sol` × Opus 4.8 / Sonnet 5 × `low/medium/high/max`。全部 HTTP 200、常识答案精确、客户端模型保持 Claude；usage 的 `max` 正确转为上游 `xhigh`。
- function tool 与 SSE 协议矩阵 4/4 通过：两目标都返回精确 `lookup_weather(city=Paris)`；SSE 重建文本正确、具备 `message_start/message_stop`，没有 GPT/OpenAI 内部模型泄漏。
- 真实 Claude Code effort 矩阵显示 5.6 四档都正确，但均先调用 `Bash` 发送 macOS 完成通知，再给最终答案，因而每例产生两次 `/v1/messages`；5.4 的精确回复样本只需一次。这是工具选择/效率差异，不是 effort 或协议错误。
- 修正 auth cache 测试夹具后，自然任务两目标答案均正确。5.6 在知识、只读代码检查、逻辑排序三例均发送完成通知；5.4 在只读代码检查中也会发送完成通知，证明该行为不是 5.6 独有，但 5.6 触发更积极。
- WebSearch A/B 均成功返回 OpenAI 官方 `Introducing GPT-5` 标题、`August 7, 2025` 和正确 URL，原生 `server_tool_use/web_search_tool_result` 事件完整。5.6 因完成通知多一次 API 轮次，但没有搜索协议错误。
- 真实 PTY 已完成：5.4 同一会话正确保留 `ORBIT-731` 并完成年龄排序；5.6 同一会话正确保留 `X=17`，两轮分别返回 `37` 与 `285`。5.6 usage 全部为同一账号、`gpt-5.6-sol`、effort high，debug 无 API/网关错误。
- A/B 门禁结论：GPT-5.6 Sol 的正确性、Claude 黑盒、effort、stream/tool/WebSearch 和多轮稳定性达到切换要求；把“更积极调用本机完成通知，增加一次工具和 API 轮次”记录为非阻断效率差异。

## 2026-07-26 全局设置实现与新二进制本地验收

- 新增全局 setting `openai_messages_dispatch_default_target`，仅允许 `gpt-5.4` / `gpt-5.6-sol`；迁移和缺失配置的产品默认均为 `gpt-5.6-sol`，数据库读取失败或存储值非法时安全回退 `gpt-5.4`。
- 最终解析优先级为 API Key 显式映射 > 分组显式映射 > 全局目标 > 安全回退；Haiku 继续默认 `gpt-5.4-mini`，Fable 保持既有显式开关语义。
- 管理端 settings API 和 UI 已增加全局下拉框；分组新建/重置的 Opus/Sonnet 默认改为空，表示继承全局。API Key 与分组提示文案均明确支持 5.4/5.6 和继承优先级。
- 后端 `go test ./...`、`go test -tags=unit ./...`、相关 `-race`、embed 测试全部通过。前端 lint、typecheck、Vite production build 通过，本功能 37 项专项测试通过。
- 前端完整 Vitest 为 649/661；12 个失败全部来自本任务未修改的旧测试/实现基线（AccountUsageCell mock 参数、EmailVerify affiliate mock、两个图表 fixture、page-size 隔离），不属于本功能回归。
- 新 macOS 本地 binary sha256=`fda349677c4532e473eb04f6189538ecb2de82b45aa4a01d439e56e33d36dad1`。迁移 154 自动执行，settings GET/PUT 与非法值 400 校验均通过。
- 在 API Key/分组 Opus/Sonnet 均为空时，全局 5.4 的 Opus/Sonnet usage 为 99/100；API 不重启切至 5.6 后 usage 101/102 分别保留 `max→xhigh` 与 `low→low`。分组覆盖与 Key 覆盖 usage 103/104 证明优先级正确。
- 新代码真实 `cc2` PTY 会话 `09846024-f07a-4a6c-b379-6abb38255deb` 两轮分别返回 `TTY-GLOBAL56-ONE` 和 `42`；usage 105–107 全部为 `claude-opus-4-8→gpt-5.6-sol`、effort high、同一账号，debug/session 无 API Error。
