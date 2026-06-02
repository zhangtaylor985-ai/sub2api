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
