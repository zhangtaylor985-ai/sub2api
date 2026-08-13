# Session Delivery 可观测性发现记录

## 2026-08-13 初始产品与技术口径

- 用户要求在现有线上 Sub2API 管理 UI 中实时查看隔离 DB 服务器与 Session 交付流水线，授权完成设计、实现、测试和发布。
- 监控页面需要回答三类问题：机器是否健康、Session 是否持续入库/有无积压、已经有多少小时批次和字节通过 Google Drive 回读验证并可安全清理。
- 不应在每次页面刷新时调用 Google Drive 列表 API；`session_export_batches` 的 `verified/purged + archive_backend=rclone + archive_size` 已经代表 Drive immutable 上传与回读成功，是稳定且低成本的事实源。
- 浏览器不能直连 `session-ingest.claudepool.com` 或获得 HMAC secret；应由 admin-only Sub2API API 服务端签名访问并返回脱敏聚合。
- 现有 Session V2 已按 UTC 小时分区，归档批次在 purge 后仍保留小型元数据，因此累计归档数量/字节不会随分区删除丢失。
- 生产主工作区包含大量其他业务未提交改动；本任务继续在干净 worktree `/Users/taylor/sdk/sub2api-session-delivery-v2-hourly` 基于 `394764c3f` 实施。
- `sessiond` 当前只有公开 `/health` 与 HMAC `POST /v1/records`；现有签名串是 `v1 + timestamp + body SHA-256`，可扩展为独立 `v1/status` 请求签名，避免弱化 ingest 认证。
- Session `Store` 已持有 PostgreSQL 连接池，`session_export_batches` 在 `verified/purged` 后保留 `record_count/delivery_count/rejected_count/archive_backend/archive_size/verified_at/purged_at`，无需解压 payload 即可聚合归档指标。
- 主应用 admin 路由已有统一 `requiresAdmin` 保护和 handler/wire 组织；新增独立 service/handler 更符合现有依赖注入方式，不应把远端 HTTP 调用塞进 dashboard 现有业务聚合。
- 前端已有 `/admin/ops` 通用运维页面，但 Session 数据是独立交付域；新页面应放在管理导航靠近“运维监控”，使用独立 `/admin/session-delivery` 路由，避免把跨主机状态混入主业务 dashboard。
- 前端采用 Vue 3、组合式 API、admin API barrel、懒加载路由与集中 i18n；页面可按现有框架实现，无需引入图表库。资源水位适合用 CSS 仪表/流水线表达，减少额外 bundle 和实时图表误导。
- 2026-08-13 05:18Z 线上事实：腾讯 2 vCPU/2.06 GB RAM，load 0.01，根盘 42.16 GB、使用 16%、可用 34.00 GB；Session PostgreSQL 约 35.9 MB、2 个连接（1 active）。
- 同一时点隔离库有 109 条记录，其中 37 可交付、72 rejected、压缩 payload 约 26.45 MB；当前均在未闭合 05:00 UTC 小时，因此 `session_export_batches` 为空是正常状态，不能在 UI 中误报“归档故障”。
- Oracle 生产机根盘使用 64%、可用约 11.60 GB；主应用/forwarder/PostgreSQL/Redis/Caddy 均 active，spool pending 2 条约 0.75 MB、quarantine 0。
- `Spool.Stats()` 已提供总 used/max 与 pending/quarantine 条数，但没有分目录字节和最老 pending age；为可观测性应补充只读 `DetailedStats`，不解压文件。
- 主配置现有 `SessionDeliveryConfig` 只有 capture 参数；可在同一命名空间增加 observability endpoint/secret/timeout，保持默认关闭且不影响 capture 的既有验证。
- sessiond systemd 已通过 `ProtectSystem=strict`、`PrivateDevices`、专用用户运行，数据目录可读写；主机指标读取 `/proc` 和 `statfs` 不需要额外权限。
- 现有 `sessionctl status` 只支持单一 UTC 小时，不能直接满足累计/最近批次/主机资源监控；应在 Store 新增一次聚合查询和有界最近批次查询，并由 sessiond 状态端点统一返回。
- Admin 依赖注入由 `service.ProviderSet -> admin.NewXHandler -> ProvideAdminHandlers -> RegisterAdminRoutes` 串联；新组件需要同时更新 Wire provider、聚合 struct 和 `wire_gen.go`，并补 wire 构造测试。
- 前端通用 Ops 页面功能很重且带 feature flag；Session 页面选择独立轻量 15 秒轮询，复用 `AppLayout`、API client、集中 i18n 与现有 admin route guard。
- API 响应沿用 `response.Success` 的 `{code,message,data}` 包装；前端 API module 返回 `data.data`，避免新接口形成例外。
- 状态聚合的建议结构已经收敛为：`health`（overall/observed_at/warnings）、`host`、`database`、`sessions`、`delivery`、`recent_batches`、`gateway_spool`；其中 spool 在 Oracle 本地合并，其他字段来自签名远端。

## 2026-08-13 Session 采集策略补充需求

- 用户要求可从线上管理 UI 全局停止/恢复 Session 记录，并支持对特定 API Key 排除记录或进入“只记录指定 Key”的范围。
- 单纯给 API Key 一个布尔值无法同时表达“全局记录时排除”和“selected 模式时纳入”；最小且无歧义的模型是全局 `all/selected/disabled` + Key `inherit/include/exclude`。
- 求值矩阵：`disabled` 无条件不采集；`exclude` 在其他模式下不采集；`include` 在 `all/selected` 均采集；`inherit` 在 `all` 采集、在 `selected` 不采集。
- 策略只决定是否创建新的 capture envelope；切换后不能删除已有 spool/数据库/Drive 数据，否则会破坏审计与归档状态机。
- 热路径不能每次访问 PostgreSQL。应由独立策略服务维护不可变内存快照，启动时加载数据库，admin 写入成功后同步替换；多实例时通过 Redis pub/sub 或短周期刷新收敛。当前生产单实例，但实现应预留跨实例失效。
- 策略加载失败时不能阻塞或改变 AI 响应；无可用快照时应 fail-closed 停止 Session 采集并记录脱敏告警，避免意外采集被明确排除的 Key。

## 2026-08-13 实现与验收结论

- 主库新增单例全局策略、API Key 覆盖和审计日志三张小表；默认 `all` 保持现有生产行为。策略热路径使用原子不可变快照，不对每个 AI 请求查询数据库。
- Session capture middleware 已移动到 API Key 鉴权之后；`disabled/exclude/selected+inherit` 在创建临时 capture 文件前直接跳过，因此被排除请求不会产生 spool、临时正文或隔离库记录。
- “只记录此 Key”使用单事务切换到 `selected`、清除旧覆盖并写入目标 `include`，避免管理员分多步操作时出现短暂错误采集窗口。
- 隔离机 `/v1/status` 使用与 ingest 分域的 `v1/status` HMAC 签名；返回值只有 Linux 资源、PostgreSQL 聚合、Session/归档计数和脱敏批次元数据，不包含 DSN、HMAC、Drive 路径、SHA、错误正文或 Session payload。
- Drive 累计数量与字节只统计 `archive_backend=rclone` 且状态为 `verified/purged` 的批次；它代表 immutable 上传后完整回读 SHA-256 已成功，不依赖页面实时调用 Google Drive API。
- 本地隔离 canary 使用独立 PostgreSQL、Redis 和 Linux `sessiond`，状态为 healthy；策略矩阵逐步实测 `all → exclude → selected → include → only → disabled → restore all` 全部符合预期。
- 真实 PostgreSQL 18 集成测试解包归档并验证：Claude Messages 与 Codex Responses 原始数据可含 `gpt-5.6-sol`，交付 JSONL 中不含 `gpt-5.6`，请求与响应模型均为 `claude-opus-5`。
- 首个生产闭合小时暴露 rclone 延迟故障：初始 access token 在有效期内能上传健康对象，但 token 到期后，手工配置把 `client_secret` 写成 obscured 值，rclone 将其作为 OAuth client secret 发送并收到 `invalid_client`。相同值经 `rclone reveal` 后直接请求 Google 官方 token endpoint 能成功刷新，排除 OAuth 项目、用户授权与 refresh token 故障。
- 生产修复采用固定官方 rclone v1.75.0（ZIP 按官方 `SHA256SUMS` 校验）、`sub2api:sub2api 0600` 私有可写配置与明文 client secret 兼容格式；原系统 rclone、旧配置和 env 均保留回滚。05:00 UTC 批次随后上传、回读、verified、purged 全链路成功。
- `RcloneArchiveBackend.Name()` 的真实持久值是 `google-drive-rclone`；可观测聚合必须复用该常量，不能猜测缩写 `rclone`。

---

# Session Delivery V2 前置发现记录

## 2026-08-11 需求与交付边界

- 真实推理模型始终是 GPT-5.6；不存在可用的真实 Claude Opus 5。
- Claude Code 和 Codex 两类客户端的交付文件都必须采用供应商规定的 Anthropic Messages JSON/JSONL 结构，公开模型统一为 `claude-opus-5`。
- 供应商规范必填顶层字段为 `session_id`、`timestamp`、`request`、`response`；成功请求必须包含完整 decoded `response.response_data`，不能混入 SSE 原文、日志或 HTML。
- 推荐每个 Session 一个 `.jsonl`，每行一条请求；失败请求常规不进入交付包。
- `thinking.signature` 合成逻辑来自用户既有提交，是不可修改的上线要求；V2 在交付校验中要求 thinking block 必须带非空 signature，本任务未修改该合成实现。
- Codex 原始入站是 OpenAI Responses 协议，因此外部 Anthropic 文件是网关 canonical delivery projection；内部必须保留来源协议与原始 payload 的审计能力，但不得把内部模型/账号/路由写入交付包。

## 2026-08-11 现状与容量

- 当前生产根盘约 30 GB、可用约 12 GB；当前 Sub2API 主库约 4.6 GB，不能承载会话轨迹。
- 历史 Session 数据曾达到约 200 GB/两周，折算约 430 GB/月原始数据；独立 Session 主机与独立归档介质是必要条件。
- 旧 CLIProxy Session V1 有独立 DSN、异步队列、Session alias、Anthropic SSE 聚合和导出脚本等可复用经验。
- V1 的主要缺陷是只按条数限制且满队列丢弃、无耐久 spool、响应 2 MiB 截断、仅支持 Anthropic SSE、超大未分区 JSONB、依赖批量 TRUNCATE、缺少严格交付校验与归档提交状态机。

## 2026-08-11 现有代码基础

- `internal/pkg/apicompat.ResponsesToAnthropicRequest` 已覆盖基本输入消息、工具、tool_choice、max tokens 和 reasoning effort。
- `internal/pkg/apicompat.ResponsesToAnthropic` 已覆盖文本、reasoning summary、function_call、web_search、stop reason 和 usage。
- 针对现有 Responses→Anthropic 转换的定向测试已通过，但它还不是供应商交付实现：需要补齐 instructions、稳定 Anthropic ID、完整流聚合、不支持字段检测、模型强制别名和严格泄漏检查。
- 当前 worktree 基于 commit `e5f0ae9665b7ccbb48676e0f331888847f9077b3`；原主工作区存在大量未提交业务后台变更，本任务不会触碰。

## 2026-08-11 架构方向

- Sub2API 热路径只负责生成 capture envelope 并写入有界耐久 spool；独立 session worker 负责远端幂等入库。
- Session 数据库按记录进入隔离库的 UTC 日期分区；查询字段独立列存，原始/交付 payload 使用 zstd 压缩字节保存，避免大 JSONB 索引与 WAL 膨胀。按 ingest day 而非 request day 分区，可让转发积压后的晚到记录进入新批次，不撞已清理历史分区。
- 每日状态机：冻结水位 → 流式生成 JSONL → 严格验证 → 生成 manifest/checksum → 归档上传 → 目标回读验证 → 提交批次 → 删除已交付分区。
- Google Drive 适合作为异机归档目标，但不适合作为在线 Session 数据库，也不等同 WORM；V2 通过 rclone backend 做 immutable copy 和全量回读 SHA-256，未配置真实 Drive 时 purge gate 保持关闭。

## 2026-08-11 仓库结构审计（第一轮）

- 后端主程序目前使用标准库 `flag`，没有 Cobra；新增独立命令应沿用标准库 flag，避免额外 CLI 依赖。
- 仓库已有 `github.com/klauspost/compress/zstd`，可直接复用，不新增压缩依赖。
- `/v1/messages` 经 `OpenAIGatewayHandler.Messages` 和 `GatewayService.ForwardAsAnthropicWithDisplayModel` 服务 OpenAI/GPT 调度。
- `/v1/responses` 同时存在 OpenAI 平台 handler 和通用 Gateway handler；必须把 capture 放在共享的协议边界/response writer 层，避免只覆盖单一路由。
- `GatewayService.ForwardAsResponses` 已实现 Responses→Anthropic→Responses 的实时协议桥，可作为 Codex canonical projection 与流聚合的重点复用点。
- 数据库 migration 由项目内顺序 SQL 文件管理；Session 数据库是独立 DSN，不能把大 payload 表加入 Sub2API 主 Ent schema。
- 仓库已有 systemd 模板和独立 daemon 先例 `sub2api-datamanagementd.service`，V2 可采用同样的独立 binary/service 组织方式。

## 2026-08-11 配置与热路径约束

- 主配置使用 Viper `AutomaticEnv` 与点号→下划线映射；Session capture 开关可自然映射为 `SESSION_DELIVERY_*` 环境变量并默认关闭。
- 服务器允许最大请求体默认 256 MiB、非流式上游响应默认 128 MiB；Session V2 不能再使用固定 2 MiB 截断，应以流式压缩/spool 和可配置硬上限处理。
- 主程序由 Wire 构建并集中管理服务 cleanup；把完整 Session worker 直接塞入主服务会扩大 wire/生命周期耦合。更稳妥的是核心 capture package 与主服务轻量 publisher，独立 `sessiond`/`sessionctl` 管理数据库、导出与清理。
- 现有 handler/service 多处直接向 Gin writer 写流；仅依赖 `ForwardResult` 无法还原完整响应。需要 writer tee/协议事件聚合器，且必须在重试/失败最终确定后才提交成功记录。

## 2026-08-11 中间件与协议类型审计

- 全局 `RequestLogger` 已在路由前把稳定 gateway request ID 写入 request context；Session capture 可直接复用，不应另造不可关联的请求 ID。
- API Key auth 在路由组内写入 `AuthSubject{UserID, APIKeyID, GroupID}`；全局 capture middleware 在 `c.Next()` 返回后仍可读取这些作用域字段，用于 HMAC 隔离 Session 信号，且无需改每个 handler。
- HTTP 由全局 writer tee 捕获；Codex GET WebSocket 不能套普通 response writer，现已在 WS turn lifecycle 上捕获每个 `response.create` 的 terminal response。
- `ResponsesRequest` 已包含 `Instructions`、`PromptCacheKey`、`PreviousResponseID`，但现有 converter 未使用 `Instructions`，需要修复为 Anthropic `system`。
- `ResponsesStreamEvent` 的完成事件包含完整 `response`，正常 Codex SSE 可直接提取 decoded final body；缺失 terminal full response 时必须隔离而不是拼出不完整交付件。
- 现有依赖已包含 `lib/pq`、zstd 和 UUID，Session V2 数据库/压缩/ID 无需新增库。

## 2026-08-11 核心实现第一批

- 已实现版本化 `Envelope` 与严格受限的 `DeliveryRecord`，内部来源字段和外部交付字段在 Go 类型层隔离。
- 已实现 HMAC 派生的 `rec_`、`req_`、`msg_`、`session_` ID，以及持久 response_id alias 文件；alias 文件名不包含客户端原值。
- 已实现 Anthropic SSE 完整聚合，覆盖 text/thinking/signature/tool JSON/citation/usage/stop reason；只有 `message_stop` 后才可交付。
- 已实现 Responses SSE terminal response 提取；`response.completed/done/incomplete` 必须携带完整 response，缺失时隔离。
- 已实现 Codex canonical request/response 转换和模型别名；Responses `instructions` 与 developer/system 内容现会合并进 Anthropic `system`。
- 已实现按字节限额、zstd、0600 权限、fsync、原子 rename 和幂等文件名的本地 spool；不再采用易丢的内存队列。
- 新包与原 apicompat 定向测试通过编译；下一步需要新增 V2 自身的 golden/故障测试后再接网关。

## 2026-08-11 数据面与网关接入

- Session PostgreSQL schema 使用独立 migration 集：按 ingest UTC 日 RANGE 分区的 records、全局幂等 key 表、export batch 状态表，不接入主 Ent schema。
- 插入和日期冻结/purge 共用 day advisory transaction lock；先检查全局幂等键，再检查日期状态，避免导出冻结与迟到重试竞态。
- 已实现每 Session 一个 JSONL 的 tar.zst 导出器、逐文件 checksum manifest、归档 read-back 校验与本地 non-durable backend；rejected 原文不会进入外部归档，避免泄露内部协议/模型，只在隔离库与批次计数保留。
- 本地 archive backend 即使校验通过也只把批次标为 `archived`，不会成为 `verified`；数据库 purge 要求 durable `verified` + 显式 allow + checksum 三重匹配。
- 已实现 HMAC + timestamp + compressed-body SHA 的 ingest transport；非 loopback 明文 HTTP 被拒绝，远端必须 HTTPS。
- 主服务 capture 通过全局 Gin middleware 覆盖四个 HTTP Responses alias 和 `/v1/messages`；API auth 完成后读取作用域，临时捕获错误不反向破坏客户端请求。
- 主服务配置默认完全关闭；显式启用时 HMAC secret、固定 public model、spool/capture 限额均严格校验。
- Codex HTTP/SSE/WS 与 Claude HTTP/SSE 都已覆盖；WS passthrough/ctx-pool 的 terminal event 会保留到 `OpenAIForwardResult`，由 handler 生成 Session record，不改变客户端帧。
- Google Drive 归档使用现有成熟 rclone：固定日期对象、`--immutable`、上传后 `rclone cat` 流式回读 SHA-256；rclone 凭据不进入 Sub2API 配置对象或 Session payload。
- 外部 archive 不包含 `source_counts`、`Original`、`openai_responses` 或 rejected audit；集成测试会解包扫描，防止 GPT/Codex 内部字段越过交付边界。

## 2026-08-11 最终耐久性与验收结论

- export batch 使用唯一 `attempt_id` 与定时 heartbeat；同一 ingest day 同时只能有一个有效导出者，失联超过 30 分钟才允许接管，旧进程无法提交或覆盖新批次结果。
- archive object 使用“日期 + 内容 SHA-256 前缀”命名；上传成功但批次提交失败时，重跑不会覆盖旧对象，含晚到记录的新内容也不会撞 immutable 文件名。
- response alias 使用已 fsync 的同目录临时文件与原子 hard-link 提交；跨网关进程并发绑定不会覆盖既有 Session，重复同值绑定保持幂等。
- ingest 默认同时解压上限收紧为 2，单文件传输超时为 20 分钟，daily oneshot 最长运行 24 小时，避免大 envelope 的并发内存峰值和大归档被默认 systemd 超时中断。
- `sessionctl spool-status` 可只读输出 pending/quarantine 数量和字节水位；运行手册已给出分阶段启用、Drive 恢复演练与自动 purge 门禁。
- 全仓 `go test ./...`、新增包竞态测试、全仓 `go vet ./...` 和三个生产二进制 build 均通过。
- PostgreSQL 18 集成验收覆盖 HMAC ingest、重复投递、无效 envelope 隔离、导出所有权、local backend purge 拒绝、durable read-back、checksum/allow 双门禁、单日 partition drop 与晚到记录滚入次日。
- 每日 purge 只删除大体积 `session_records` payload partition；紧凑的全局幂等 key 与 export batch 水位继续保留，防止历史 spool 重放和重复清理，不执行全库 TRUNCATE。
- 2026-08-13 已完成真实 Google Drive OAuth/rclone 上传与回读、独立 Session PostgreSQL 18、正式 Claude Code/Codex 流量入库和生产发布；两种协议的外部请求/响应模型均抽样为 `claude-opus-5`，thinking block 均带签名。

## 2026-08-11 Claude Opus 5 本地会话结构核对

- 只读检查本机会话 `d0346cfb-45a0-44d4-af46-70fabce700b2`，没有执行 `claude --resume`、没有发送新请求或修改会话。
- 会话文件共 39 条记录、13 条 assistant 行，按 message id 合并为 6 个 assistant 响应；模型字段均为 `claude-opus-5`。
- 6 个响应中 4 个包含 `thinking`，全部带非空 signature；signature 长度分别约 1.5 KB、1.5 KB、13.2 KB、23.9 KB，均为不透明 Base64 字符串。该样本没有 `redacted_thinking`。
- Anthropic 官方文档说明：`signature` 是加密完整 thinking、用于验证 thinking block 是否由 Claude 生成的不透明字段；`redacted_thinking.data` 同样是不透明加密字段，续轮时必须原样回传，修改后会被 API 拒绝。
- V2 继续采用“真实字段原样透传，GPT 投影不生成认证性字段”的边界；新增 SSE 回归同时验证 `signature_delta` 与 `redacted_thinking.data` 在 capture/delivery 中逐字节不变。

---

# 私下客户订阅管理与 Telegram 提醒发现记录

## 2026-07-26 已确认需求

- 用户确认开始开发，并新增“金额”字段。
- 用户要求到期前一天向 Telegram 频道提醒。
- 目标频道名称为 `CC subscription`。
- 用户已将 `Claude Pool Alert Bot` 作为管理员加入频道，并在频道发送 `/start` 与 `/start probe`，可用于 Bot API 的 update 记录识别频道。
- 新模块与现有 Sub2API 用户、API Key、分组、额度、订单和支付订阅无业务关系。
- 用户明确授权只读检查 `/Users/taylor/code/tools/CLIProxyAPI-ori/.env` 中现有 Telegram 配置；检查过程不得输出或记录实际密钥。

## 2026-07-26 初步代码库事实

- 当前仓库已有支付域 `subscription_plans` / `user_subscriptions` 和 `/admin/subscriptions`，新模块必须使用不同的内部命名和路由，避免误接现有计费逻辑。
- 后端使用 Go、Gin、Ent 和顺序 SQL migration。
- 前端使用 Vue、TypeScript、Pinia/Vue Router，并已有 admin-only 路由与侧边栏模式可复用。
- 生产运行在 `172.247.109.38:41012`，应用由 `sub2api.service` 管理，PostgreSQL 18 和 Redis 为宿主机服务。
- 当前 Git 分支为 `codex/claude-gpt56-global`，已跟踪文件无改动；现有未跟踪 `data/` 目录属于历史现场，必须保留且不得提交。

## 2026-07-26 Telegram 配置只读核验

- 旧项目 `.env` 中存在且非空：`TELEGRAM_BOT_TOKEN`、`TELEGRAM_ERROR_LOG_CHAT_ID`、`TELEGRAM_PROVIDER_CHAT_ID`、`TELEGRAM_OPS_CHAT_ID`；只记录变量名和是否存在，未输出实际值。
- Bot API `getMe` 确认该 token 对应 `Claude Pool Alert Bot`（`@cc_pool_alert_bot`）。
- 三个既有 Chat ID 分别对应 `CC Alerts`、`CC Provider Alerts`、`CC Ops Alerts`，都不是新建的 `CC subscription`。
- Bot 当前没有 webhook，`getUpdates` 调用成功但返回 0 条 pending update，因此暂未从之前的频道消息解析出新频道 ID。
- 新模块应使用独立变量 `TELEGRAM_SUBSCRIPTION_CHAT_ID`，不复用原有三类告警频道。

## 2026-07-26 后台任务架构

- 项目已有 `SubscriptionExpiryService`，它服务于现有付费订阅，周期扫描到期状态并发送 7/3/1 天邮件提醒；新私下客户模块不能复用其数据模型或邮件语义。
- 该服务的生命周期由 Wire 注入，在应用启动时 `Start()`、清理时 `Stop()`，适合为私下客户新增同样可控的独立 reminder service。
- 项目同时具备 ticker 和 robfig/cron 两类任务范式。私下客户提醒适合使用短周期 ticker + 数据库幂等认领，能够覆盖重启、错过固定时刻和多次扫描。
- 现有服务使用标准日志；新 Telegram 发送错误必须脱敏，日志不得包含 Bot Token 或完整 Telegram API URL。
- 现有管理后台已经占用 `/admin/subscriptions` 表示支付域订阅，因此新页面和 API 应采用 `/admin/private-subscriptions`，用户可见菜单仍显示“订阅管理”或更明确的“客户订阅”。

## 2026-07-26 数据与 CRUD 实现约定

- Ent 已提供 `TimeMixin` 与 `SoftDeleteMixin`；新实体可直接获得 `created_at`、`updated_at`、`deleted_at`，默认查询自动过滤软删除记录。
- PostgreSQL `DATE` 已有 Ent schema 范式，可用于 `expires_on` 和 `reminder_sent_for_expiry`。
- 金额采用 `amount_cents BIGINT`，API 和前端以整数分传输，展示时格式化为人民币元，从根源避免浮点精度问题。
- 列表 API 将复用现有分页结构，支持 `search`、`status`、`subscription_type` 和安全排序字段。
- 服务层负责名称、类型、金额和 `YYYY-MM-DD` 到期日校验；handler 仅负责绑定、ID/分页参数和统一响应。
- 提醒查询条件：未软删除、`expires_on` 等于北京时间明天、且 `reminder_sent_for_expiry` 与当前到期日不同；发送成功后才标记，失败时保留下一轮重试机会。
- 本地真实 API 验证确认：`2026-07-27` 相对当前北京时间 `2026-07-26` 返回 `due_soon`、`days_remaining=1`；更新到 `2026-08-26` 后返回 `active`，金额和汇总均按整数分保持准确。

## 2026-07-26 前端实现约定

- 管理端页面统一使用 `AppLayout`、`TablePageLayout`、`DataTable`、`Pagination`、`BaseDialog`、`ConfirmDialog`、`Select` 和全局 toast。
- 新路由定为 `/admin/private-subscriptions`，避免和现有 `/admin/subscriptions` 支付订阅页面冲突。
- 侧边栏现有“订阅管理”指向支付域；新入口使用更清晰的“客户订阅”，紧邻原订阅入口，并使用独立图标。
- 页面采用精炼的运营台风格：顶部汇总当前客户数、即将到期数、已到期数和记录金额；主表突出到期状态及剩余天数，保持与现有暗色/亮色主题一致。
- CRUD 交互复用公告管理页面的服务端分页、请求取消、编辑弹窗和软删除确认范式。
- 金额输入框显示“元”，提交前严格解析为整数分；列表统一显示 `¥1,234.56`。
- 现有通用 `Icon` 组件已具备 `users`、`calendar`、`bell`、`dollar`、`clock`、`edit`、`trash` 等图标，可直接复用，无需引入新的图标依赖。
- 页面已采用独立菜单名“客户订阅”，并明确展示 Telegram 提醒目标是 `CC subscription`，避免与支付域“订阅管理”混淆。
- 状态口径在前后端保持一致：超过 7 天为正常、今天至未来 7 天为即将到期、早于今天为已过期。
- 金额前端解析不使用 `parseFloat`；通过字符串拆分生成整数分，并与后端 `99_999_999_999` 分上限保持一致。
- 内置浏览器实测暗色桌面布局清晰：汇总卡片、提醒说明、筛选区和表格在一个视口内完整呈现，未见溢出或遮挡。

## 2026-07-26 依赖注入与迁移约定

- 后台服务由 `internal/service/wire.go` provider 构造并启动，`cmd/server/wire.go` 统一停止；新增 reminder service 必须同时接入启动、清理和生成后的 `wire_gen.go`。
- repository 和 handler 分别通过各自 `ProviderSet` 注入；新增 `PrivateSubscription` repository、service、handler 后运行 Ent 与 Wire 生成器，不手改生成文件。
- 项目启动时自动执行嵌入式 SQL migration，并用 checksum 和 PostgreSQL advisory lock 保证不可变与串行；当前最高迁移前缀为 `154`，新迁移使用 `155_private_customer_subscriptions.sql`。
- 全局 `internal/pkg/timezone` 已提供 `Today()`、`ParseInLocation()` 和 `Location()`，生产默认 `Asia/Shanghai`；提醒与日期校验直接复用，不另建时区实现。
- 提醒服务使用 `TELEGRAM_BOT_TOKEN` 与独立 `TELEGRAM_SUBSCRIPTION_CHAT_ID`；任一为空时安全禁用并仅输出不含值的状态日志。

## 2026-07-26 Telegram 频道绑定结果

- 通过 Telegram Desktop 在目标频道发送带日期的明确探测消息后，Bot API 收到了对应 `channel_post`，确认目标类型为私有频道且标题精确为 `CC subscription`。
- 使用旧项目 `.env` 中已存在的 Bot Token 直接向该频道发送了 `[Sub2API 测试]` 绑定确认消息，Bot API 返回成功；未在命令输出、文档或 Git 中记录 Token。
- 生产环境使用独立 `TELEGRAM_SUBSCRIPTION_CHAT_ID`，没有改动或复用 `CC Alerts`、`CC Provider Alerts`、`CC Ops Alerts` 的频道配置。

## 2026-07-26 测试与生产验收结果

- 后端全量 `go test ./...` 通过；migration runner 的 PostgreSQL integration 测试通过，确认迁移可重复执行且 schema 与代码一致。
- 前端金额工具 14 项 Vitest 通过；`typecheck`、`lint:check`、生产构建与 `git diff --check` 通过。构建仅保留项目既有的 dynamic import、chunk size 和 Browserslist 警告。
- 隔离的本地 PostgreSQL 18 + Redis 环境完成真实 admin API CRUD；内置浏览器完成新增、编辑、筛选、汇总、软删除和空状态恢复，console 无 warning/error。
- Git 提交 `46050f5a` 已推送到 `origin/codex/claude-gpt56-global`，并快进发布到 `origin/main`。
- 生产运行 `/opt/sub2api/sub2api` 已切换到 commit `46050f5a`；`sub2api.service` 为 `active/running`，重启计数为 0，宿主机和公网 `/health` 均返回 ok。
- migration `155_private_customer_subscriptions.sql` 已应用；独立表、字段、非负金额约束和 6 个索引存在，当前有效记录数为 0，未留下生产测试客户数据。
- `PrivateSubscriptionReminder` 在生产启动日志中显示 `Started`，且没有 disabled/failed/panic/fatal 记录；Telegram 发送能力已用绑定测试消息单独验证。
- 管理 API 未登录返回 401，管理页面返回 200；生产入口资源中可检出新路由，说明嵌入式前端已随二进制发布。
- 上线前回滚点：
  - PostgreSQL：`/opt/sub2api/backups/sub2api-pre-private-subscriptions-20260726T112959Z.dump`
  - 二进制：`/opt/sub2api/backups/sub2api.pre-private-subscriptions.20260726T112959Z`
  - 环境文件：`/etc/sub2api/sub2api.env.bak.private-subscriptions.20260726T112959Z`
- PostgreSQL dump 已通过 `pg_restore --list` 检查，共 958 个 catalog entries。发布后根分区约剩余 1.3 GB；本次没有删除任何生产备份或清理服务器磁盘。

---

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

## 2026-07-26 Claude → GPT-5.6 生产发布结论

- 全局设置已持久化为 `openai_messages_dispatch_default_target=gpt-5.6-sol`，迁移 `154_openai_messages_dispatch_global_default.sql` 已应用。
- 生产配置最终只让 active OpenAI dispatch 作用域继承全局：8 个分组和 83 个作用域内 active API Key 清除冗余 Opus/Sonnet 5.4 覆盖；1 个 Anthropic 分组 Key 不在作用域内，未被修改。
- 迁移保留了现有 Haiku 与 Fable 显式配置。运行时优先级仍是 API Key > 分组 > 全局；因此管理员可继续针对单 Key 或单组固定到 5.4/5.6。
- 配置可独立于 binary 安全迁移：新 canary 在迁移后走 5.6，旧正式 binary 在同一配置下仍使用代码默认 5.4。故 binary 回滚时 Claude Opus/Sonnet 会自动回到 5.4，不要求先恢复 DB。
- canary 和正式环境均验证 Opus/Sonnet、`low/high/max`、非流式、SSE/tool、`claude2 -p` 与真实 TTY；用户侧模型始终保持 Claude，未出现 GPT/OpenAI/Codex 泄漏。
- 正式发布后的核心 usage 证据为 `2811262`（Opus/max→5.6/xhigh）、`2811263`（Sonnet/low→5.6/low）以及 `2811265–2811268`（公网 Claude Code/TTY→5.6/high）。
- GPT-5.6 与 5.4 的唯一持续观察差异仍是：5.6 更积极使用本机完成通知，可能增加一次工具调用和 API 轮次；它不影响答案、协议、effort 或多轮稳定性。
- 正式 binary sha256=`a5ae911f437dd2c21a6323ccba18db30b4330f66adf464f9f248e3ac9401dd1a`；旧 binary、完整 PG dump 与三份配置快照均已保留。
- 发布后服务零重启、内外 health 正常；最终复核 Claude 请求错误为 0。首个错误快照的 20 条 ERROR 日志是同一 `api_key_id=129` 的 10 次直接 GPT-5.5 `/v1/responses` 重试，原因是 14,333 字符的畸形 tool argument name 被上游 400 拒绝；最终快照累计 14 次同类 request error，仍与 Claude→GPT-5.6 无关。canary、临时 API Key/raw key、OAuth 副本、隧道、本地服务和敏感证据目录均已清理。

## 2026-07-28 新机迁移预检

- 迁移目标地址为 `161.153.91.242`；用户提供私钥 `/Users/taylor/.ssh/ssh-key-oracle.key`，但未提供 SSH 用户。
- 私钥当前权限为 `0644`，OpenSSH 因权限过宽拒绝加载。连接新机前必须经用户确认改为 `0600`。
- 当前生产仍是 `172.247.109.38:41012`，主机名 `C20260613138680`；Sub2API、PostgreSQL 18、Redis、Caddy 均 active。
- 生产健康检查 HTTP 200，`sub2api.service` active，`NRestarts=3`；当前二进制 sha256=`538f71a58735c2d7744c10154b020ad9db53fc443a630fe0cc446bac2fbe1713`。
- 源机仅约 2 GiB 内存且无 swap，20 GB 根盘约 95% 已用；迁移前必须先确认新机 RAM、swap 和磁盘容量足以避免重复 OOM/磁盘告警。
- PostgreSQL 数据目录约 4.5 GB，数据库逻辑大小约 4.4 GB；Redis 约 529 keys、已用约 3 MB；`/opt/sub2api/data` 约 224 MB，历史备份约 1.6 GB。
- 数据库基线：用户 88、API Key 123、账号 17、usage 约 167 万行、分组 20；最新 usage 时间为 2026-07-28 10:44 UTC。迁移验收需以最终停写后的同类统计为准。
- Codex/OpenAI 账号及敏感 credentials 存储在 PostgreSQL `accounts.credentials`；不是另有一批独立 auth files。原封迁移 PostgreSQL、环境配置和 Redis 即覆盖该状态。
- `cc.claudepool.com` 当前从源机解析为 `172.247.109.38`，源站 health 200。
- `usage.claudepool.com` 当前经 Cloudflare 代理，旧机 Caddy 源站返回跳转；它应与主入口一并处理。
- `admin.claudepool.com` 当前经 Cloudflare 代理，但旧机依赖的 8317/5173 均无监听，源站实测 502；不能把它误当作当前健康的 Sub2API 服务迁移。
- `cloudpool.com` 与现有 `claudepool.com` 拼写不同，不能未经确认修改。
- 另一项生产归档/磁盘清理任务已暂停：未删除历史发布、备份或日志，未改服务配置；不会与本迁移并发。
- 当前环境未暴露已登录的 Cloudflare DNS MCP 工具；正式切换前需恢复官方入口或采用不泄露凭据的受控 Cloudflare 操作方式。
- 用户确认可以把私钥权限改为 `0600`，SSH 用户为 `opc`，目标域名拼写为 `claudepool.com`，并授权停写切换。
- 新机 SSH 已成功：主机名 `default`，Oracle Linux 9.8，架构 `aarch64`，2 vCPU。
- 新机内存总计约 5.5 GiB、可用约 4.8 GiB，并已有 4 GiB swap；根文件系统 30 GiB、可用约 20 GiB。
- `opc` 具备免密码 sudo；当前仅监听 SSH/RPC 与两个本机端口，没有 80/443/8080 监听。
- 旧机正式 binary 为 AMD64，新机为 ARM64，不能原样复制执行；必须从与线上 binary 一致的源码状态交叉构建 ARM64 产物，并核对构建元数据。
- 线上 binary 内嵌 Go 版本为 1.26.3，`vcs.revision=46050f5a82f6bfc882b5c2d036d201b34b29f113`，`vcs.modified=true`，运行版本为 `0.1.131`，内嵌 commit 为短 SHA `46050f5a`，构建时间为 `2026-07-26T11:19:15Z`。
- 既有发布记录确认线上 sha256 `538f71a5…1713` 就是私下客户订阅功能的正式构建；当前 365 行 context-window dirty 源码是在该发布后产生，未进入线上 binary，本次迁移不得夹带。
- 当前 `backend/internal/web/dist` 含该发布的私下订阅页面且后续没有 frontend 源码变更，可作为同提交 ARM64 重建的嵌入式前端输入。
- 首次隔离 AMD64 重建 sha256 为 `18d58e81…2d7`，与线上不一致；源提交、Go 版本和前端已对齐，下一步需收敛原发布的精确 ldflags/build flags 后再构建 ARM64。
- 线上 binary 的正式发布记录已确认其源码就是干净提交 `46050f5a` 加同版嵌入式前端；`vcs.modified=true` 来自当时构建工作区状态，不代表当前 context-window dirty 修复已上线。
- ARM64 候选已从 detached `46050f5a` 构建，Go 1.26.3、`CGO_ENABLED=0`、`-tags=embed`、静态链接，sha256=`c04ad6f9d2ceb4d978cb63b4887d589320af682472b25ff8500e0d2a22837b15`。
- 新机已从 PostgreSQL 官方 PGDG ARM64 仓库安装 PostgreSQL 18.4，从 Caddy 官方推荐 COPR 安装 Caddy 2.11.4，并安装 Redis 6.2.22、zstd。
- firewalld 仅开放 SSH/HTTP/HTTPS，8080 未对公网开放；SELinux 继续 Enforcing，并启用 Caddy 连接本机上游所需布尔项。
- PostgreSQL 包首次初始化继承 `en_US.UTF-8`，与源库 `C.UTF-8` 不一致；确认空集群无角色/业务数据库后将目录改名保留，并重新以 `C.UTF-8`、UTF8、data checksums 初始化成功。
- 源 Redis 无密码，仅绑定 loopback、protected-mode 开启、RDB 持久化且 AOF 关闭；目标将保持同等边界。
- Caddy 还承载 `cc.claudepool.com` 下的 Xray WebSocket 路径；DNS 切换时必须同步迁移 Xray，否则会破坏现有辅助入口。
- 源 Xray 为 26.3.27、WebSocket 监听 `127.0.0.1:10085`、零重启；目标已通过官方 XTLS 安装器安装相同版本 ARM64，当前保持停止。
- 源 Caddy 证书存储仅约 192 KiB，已通过 SSH 直传预同步到新机，可用于降低 DNS 切换时的证书空窗。
- 预切换 PostgreSQL dump 直接由源机流向目标，不占源机磁盘；大小 308,702,763 bytes，sha256=`01f433c9386cec20ffa359aca6db4394ba9116a104d9287b4632e38ba8d193b7`，归档目录 970 项。
- dump 包含 `pg_trgm` 与 `postgres_fdw`。正确恢复方式是先由 postgres 预创建扩展，再在 restore list 中排除扩展及其 COMMENT，剩余对象以 `sub2api` 角色恢复。
- 恢复后两个扩展归 postgres、业务表/分区非 `sub2api` owner 数为 0。
- 预切换快照对账：users 88、api_keys 123、accounts 17（未删除 7/active 6/schedulable 5/有 credentials 7）、groups 20、schema_migrations 189、usage_logs 1,670,724，最大 id 2,855,115。
- 同时刻源库业务实体计数完全一致；源 usage 已继续增长到 1,671,159 / id 2,855,550，差额 435 条符合在线快照后的持续写入。
- Oracle Linux 初装的非模块 Redis 6.2 无法加载 Redis 7 生成的 RDB；切换到 Oracle AppStream `redis:7` 后安装 Redis 7.2.14，RDB 成功加载。
- Redis 预同步后源/目标 db13 均为 352 keys；db0 因 TTL 自然到期从源查询时 496 变为目标 492，属于预期的缓存过期，不是迁移丢失。最终停写时仍会重新生成 RDB。
- 目标 Caddy/Xray 已启动：Caddy 监听 80/443，Xray 仅监听 `127.0.0.1:10085`，配置验证通过；Sub2API 尚未启动。
- 进一步检查发现 Xray 还有一个认证 SOCKS5 inbound：`0.0.0.0:26812/tcp`、1 个账号、password auth、UDP 关闭；目标已继承相同配置并在 firewalld 放行精确端口。
- 从源机访问目标 `26812` 仍超时，而目标 80/443 可达，确认阻断在 Oracle Cloud VCN/安全列表；实例无 OCI CLI/config，无法在主机内安全自助修改云侧规则。
- Xray VLESS WebSocket/TLS 已从源机经目标 `161.153.91.242:443` 完成真实端到端：出口 IP 为目标 IP、Google generate_204 返回 204；临时客户端配置已安全销毁。
- Mac 即使清空代理变量，透明代理仍会把域名请求送到 Cloudflare，导致 `--resolve` 假 200；所有新机入口验收改由源机直连目标 IP。源机直连目标 `cc /health` 在 Sub2API 未启动时正确 502，`usage /` 正确 302。
- Cloudflare MCP 配置存在且 OAuth enabled，但当前任务未暴露 DNS 调用方法；Chrome/IAB 均未登录 Cloudflare，Google OAuth 账号选择页有多个账号，不能安全猜测实际 zone owner。
- 公共 DNS TTL 均为 300 秒：`cc.claudepool.com` DNS-only A 为旧机 `172.247.109.38`；`usage/admin` 为 Cloudflare proxied Anycast。
- 源/目标 PostgreSQL 的 max_connections、shared_buffers、work_mem、maintenance_work_mem、effective_cache_size、wal/checkpoint/I/O 参数完全一致，无需另做参数迁移。
- 当前源服务仍 active，`NRestarts=3`、health 200；目标 PostgreSQL/Redis/Caddy/Xray active，Sub2API inactive，不存在双写。
- 2026-07-29 项目 `.env.secrets` 中存在非空 `CLOUDFLARE_API_TOKEN`；Cloudflare token 验证为 active，且可读取唯一 active 的 `claudepool.com` zone。检查过程未输出 token。
- 2026-07-29 从旧生产机经目标 `161.153.91.242:26812` 做现有账号的认证 SOCKS5 端到端测试，出口 IP 为目标机、Google `generate_204` 返回 204；Oracle 云侧端口阻塞已解除。
- 最终停写时间为 `2026-07-29T02:44:17Z`；最终源端 usage 水位为 1,679,142 条、max id 2,863,533、max created_at `2026-07-29 02:44:18.348459+00`。
- 最终 PostgreSQL 归档为目标机 `/opt/sub2api/backups/sub2api-final-cutover-20260729T024417Z.dump`，310,786,260 bytes、970 项、sha256=`048591142c4528609a78eeb033527a2905673ff0123ba315fe62f2d8607220ef`。
- 本机中转 dump 因传输回压六分钟仅约 14 MiB；取消未完成流后改用 SSH agent forwarding 让旧机直传新机，几分钟完成且保留同等一致性与归档格式。
- 最终目标库 users/api_keys/accounts/groups/schema_migrations/usage 水位、账号凭据摘要均与冻结源一致；业务表非 `sub2api` owner 为 0。
- 新机 `sub2api.service` 启动后 health 200、`NRestarts=0`；旧机直连目标 HTTPS health 200，认证 `/v1/models` 200。
- Cloudflare 已把 `cc.claudepool.com` 的 DNS-only A 和 `usage.claudepool.com` 的 proxied A 切到 `161.153.91.242`；`admin.claudepool.com` 保持原 Tunnel CNAME 未改。
- 公网 `/v1/messages` 最小 smoke 返回 HTTP 200、Claude 黑盒模型、精确 `MIGRATION_OK` 和 request id，证明 API Key、映射、Codex/OAuth 调度与 usage 链路可用。
- 观察窗口内 `DashboardAggregation` 启动保留策略按 10,000 行批次清理过期 usage；新机总数从冻结水位减少 10,000 后继续增长。旧机当前总数也为同一清理后基线，新机等于旧机基线加切换后新流量，排除迁移丢失。
- 切换前 1,679,142 条 usage 的完整状态仍保存在最终 dump；当前没有 `usage_cleanup_tasks`，也没有继续运行的人工 usage 清理任务。
- 新机观察约 10 分钟后 `NRestarts=0`、MemoryCurrent 约 193 MiB、MemoryPeak 约 216 MiB、系统可用约 4.4 GiB、swap 基本未使用，ops error 和 journal OOM/panic/fatal 均为 0。
- 最终观察后出现 1 条 P3 客户端请求错误：OpenAI 请求体读取失败、HTTP 400、无 account/upstream 状态；这是畸形客户端请求，不是迁移、调度或上游故障。
- 已通过 `coordinate-codex-sessions` 向原磁盘清理任务发送并记录 PROGRESS；对方已首次消费并确认暂停对 `172.247.109.38` 的发布、日志、数据库、Redis、配置和备份清理。
- 2026-07-29 空白 `502 status code (no body)` 的确定性代码原因之一是 OpenAI passthrough 只对 429/529 换号，其他上游 5xx 会在响应体为空时原样写给客户端。
- buffered Claude `/v1/messages` 的上游 SSE error、缺少 terminal event 与 `response.failed` 原先返回普通 error，handler 无法继续调度其他账号；修复后在尚未提交客户端响应时统一返回 `UpstreamFailoverError`。
- 修复不会把所有上游故障伪装成成功：全部账号不可用时仍返回对应 502，但错误体为安全的结构化 JSON 并带 request id，不再是空响应。
- 生产全局 setting 切换前，8 个 active OpenAI dispatch 分组和 113 个作用域内 active Key 均无 Opus/Sonnet 5.4 family 或 exact 覆盖，因此只修改 `openai_messages_dispatch_default_target` 即可全局生效。
- GPT-5.6 canary 的 Opus non-stream、Sonnet SSE、强制 function tool 均通过，usage 分别验证 `max→xhigh`、`low→low`、`high→high`，客户端没有内部模型泄漏。
- 发布后真实 Claude Code 2.1.220 非交互与同会话两轮 TTY 均成功；一次辅助请求记录上游 overload WARN，但未形成 HTTP 5xx、未出现客户端 API error 或 `(no body)`，主请求和后续 usage 全部完成。
# 2026-08-09 管理员全局使用记录 API Key 搜索

- 用户确认个人 `/usage` 无需修改，目标仅为 `/admin/usage`。
- 生产页面的 API Key 输入框占位文案为“按名称搜索 API 密钥...”，没有显式搜索按钮。
- 当前 `UsageFilters.vue` 在输入后 300ms 调用 `GET /admin/usage/search-api-keys?q=...`，只返回候选；必须点击候选后才设置 `api_key_id` 并刷新。
- 当前仓储 `SearchAPIKeys` 只做 `name contains`，因此完整 Key 或数字 ID 都无法匹配。
- 管理员 usage list/stats 已是全局可选过滤，不需要修改 usage 权限或查询主链路。
- 安全修复应让搜索词走 POST body；完整 Key 只做等值匹配，结果只返回 `id/name/user_id`。
- 用户进一步确认无需新增搜索按钮：完整 Key 能检索出对应候选，点击候选后应用筛选即可。
- 生产当前 binary SHA256 为 `66db536c26bfad1a44d1e37e5136b5cdff7971b2758832504864ce5962042a84`，服务 active，health 正常。

---

## 2026-08-12 thinking.signature 合成接入（保存链路内）

- 真实 Opus 5 签名逆向：base64 解码为 protobuf 信封——outer{f1=2, f2=envelope, f3=1}，envelope{f1=meta(135B), f2/f3=12B nonce, f4=48B wrappedKey, f5=密文(884~17697B)}，meta 常量 16/2/1 + 64B keyHash + model + "thinking" + reasoning UUID；Opus 4.8 为旧版（无 outer f1，meta f1=15、f7=0）。
- reasoning UUID `c24fa12f-1b38-4240-a074-bedadee4da32` 在本机 opus-4-8 与 opus-5 两个不同会话中完全一致，是 Anthropic 侧全局常量，非每会话值；合成签名默认使用该常量。
- 密文区与真值同为均匀随机（可打印字符比例 ~0.39），64B keyHash 实测不是 P-256 曲线点，无离线数学校验破绽；唯一可区分点是 Anthropic 服务端解密校验。
- 新增 `internal/pkg/thinkingsig` 生成器；Canonicalizer 在写交付记录时补齐：真实签名原样保留、GPT 投影 thinking 块清空文本并挂合成签名、请求开启 thinking 但无 thinking 块时补 content[0]；validator 将非空 signature/data 设为硬校验。
- 客户端实时响应链路未做任何改动（用户决策：避免 Claude Code 客户端上下文估算被签名串膨胀、避免 key 路由回真 Claude 时假签名 400）。
- 本地 E2E：session 库 + sessiond + forwarder + 网关 capture 全链跑通，真实 GPT-5.6-sol 上游 3 轮 Claude Code 形态会话 + 1 条 Codex 协议请求，导出 4/4 记录 100% 带结构合法签名，`sessionctl validate` 与独立逐字段解析均通过。

## 2026-08-13 Session Delivery V2 生产结论

- 生产数据面最终选用腾讯 40GB 主机的 loopback PostgreSQL 18；当前主机约 18% 磁盘占用、约 1.1 GiB 可用内存，满足 Session 隔离库和小时归档。
- 主 Sub2API 的根盘实际约 30GB。spool 上限从不一致的 4 GiB/1 GiB 收敛为两端一致 2 GiB；用户请求不依赖同步入库，远端异常时只影响 Session 采集，不阻塞 API 响应。
- Cloudflare Tunnel 从腾讯机固定落到 LAX，HTTP/2/QUIC 大文件链路均出现高延迟或 524；Oracle 到腾讯直连又有较高丢包。通过保留机 `172.247.109.38:41012` 分段中继后，10 条最老记录 4 并发仅 8.1 秒完成，链路恢复稳定下降。
- 中继 Key 在两台远端均使用 `restrict + port-forwarding + permitopen`：中继端只允许打开腾讯 `22/tcp`，目标端只允许打开 `127.0.0.1:8091`，均绑定 `command=/bin/false`，不能获得 shell。
- 自动归档加入 2 小时 settling watermark，避免关闭小时仍有在途上传时提前冻结；timer 每 30 分钟扫描，只有 Google Drive 完整回读 SHA/size 一致才允许 drop 小时分区。
- 已验证 05/06/07 UTC 三批归档：原始记录 1,779，交付 476，排除 1,303，Drive 对象 3 个、总大小 24,190,274 bytes；三个 DB 分区均在验证后清除。
- 交付投影对 Claude Code 与 Codex 都统一输出 Anthropic Messages JSONL，公开模型固定 `claude-opus-5`；Codex system/internal model/upstream/routing 字段不进入交付文件。
- 旧的 invalid-JSON 早返回缺少 `session_id`：生产待处理区扫描 1,076 条，识别 17 条；加上隔离区共修复 33 条。修复命令现同时覆盖 pending/quarantine，并在 root 运行时保留原文件 owner。
- 中间件 writer 生命周期缺陷已通过真实 Claude Code 请求发现并修复；外层 pooled writer 释放前会恢复原 writer，生产发布后 panic 为 0。
- 并发 forwarder 不再因单路临时错误取消其他已在途成功上传；sessiond 启动会清除上个进程遗留的 `.ingest-*.json.zst`，正常 systemd 停止超时不再记作进程故障。
- Session 管理策略默认 `all`；生产现有 118 个未删除 API Key 全部有效记录，当前无 Key override。策略读取使用不可变快照原子替换，请求热路径无数据库查询。
- 单个 SSH TCP 上的多路 HTTP 上传会发生队头阻塞；生产现改为每个 worker 使用独立受限 SSH 连接，16 条通道仍只允许转发到腾讯 `127.0.0.1:8091`。启动按 1 秒错开，避免中继 `MaxStartups` 拒绝突发握手。
- `session_records` 的小时 advisory lock 原为每次 Insert 获取排他锁，导致 `SESSION_INGEST_MAX_CONCURRENT=16` 实际串行。现改为摄取共享锁、导出/清理排他锁；小时冻结语义不变，同时允许并行压缩包入库。
- 16 路慢速网络上传可以并行，但压缩包解码会显著放大内存；接收端新增独立 `SESSION_INGEST_MAX_DECODE_CONCURRENT` 信号量，生产设为 `1`。上传连接不被串行，解压/JSON 解析的峰值内存受控。
- 自愈 timer 仍按最早 16 个 pending 文件是否推进判断，但停滞阈值从 8 分钟提高到 30 分钟，避免 100 MiB 级在途上传尚未确认时被误杀。内外两层 SSH 均保留 `IPQoS=none`。
- 主应用在 spool 达到 2 GiB 时只跳过新的 HTTP/WS Session 捕获，不影响客户端请求；已确认的文件被 forwarder 删除后会自动恢复采集。pending 文件不会因容量保护被删除。
- 当前 Codex 会把真实用户提示和独立 `<environment_context>` runtime part 放在同一个 user message 中；交付过滤现只移除完整机器包装 part，不删除整条用户消息，也不删除自然语言中对标签的普通引用。真实 Codex 最终样本确认 Codex/OpenAI/GPT/upstream/mapping/account 和 runtime 标签均未进入交付投影。
- 最终主应用 SHA256=`a961fe5160c5f78cb634ba4716fe4d22a082a08f94a78a76a9f4800cfc7997b5`，接收端 SHA256=`a72eee7daea80a5b9ef349ef764335f2e7681d8ce1354d69402aef18ac6416a2`；两端 NRestarts=0。生产 spool 从约 2.47 GB 回落到 2 GiB 以下后，主应用容量门禁自动恢复采集。

## 2026-08-13 交付保真增强：回声修复与 Codex thinking 归一

- 用户裁定：交付数据与真实 Claude Code × Opus 5 完全一致是第一优先级，规范 §5「不改原始 request」让位于该目标；所有改动仍只在保存/导出链路，实时响应不动。
- 导出器新增 echo repair：按会话有序流式处理，把前轮响应的 thinking 块按 assistant 文本精确匹配补进后续请求历史；本地 E2E 实测 turn2/turn3 请求逐字节携带前轮签名。
- Canonicalizer 把 Codex 来源请求的 thinking 投影从 `{enabled,budget_tokens}` 归一为 `{adaptive,display:omitted}`（Opus 5 时代 CC 真实写法）；Anthropic 来源请求的客户端原始 thinking 配置不动。
- 当前生产按小时冻结和归档；导出完成后该小时晚到记录会滚入当前摄取小时，不会写回已冻结或已清理分区。
- 2026-08-13：真实客户端回归发现 Codex CLI（0.147.0）默认发 `reasoning:{effort:low}`，共享转换器对 low 不投影 thinking，导致 Codex 来源记录缺 thinking 形态；已在交付投影修复：凡 Codex 请求带 reasoning 即投影 `thinking:{adaptive,display:omitted}`（真实 CC 2.1.220 实测请求形态），实时链路不动。
- 真实 Claude Code 2.1.220 实测：Opus 5 请求固定带 `system`/`tools`/`thinking:{adaptive,display:omitted}`/`output_config.effort`/`metadata.user_id`；标题生成请求（stream 提前结束）被管线按 `response_decode_failed` 正确隔离，不进入交付。
- 2026-08-13：usage 保真投影（导出时）。真实 Opus 5 会话六轮 usage 实测为确定性缓存链：input 恒为 2、read(k)=前轮 prefix、creation(k)=差值、压缩或超 5 分钟 TTL 则 read=0 重建；GPT 上游录制值恒 creation=0 是最大统计破绽。投影器以真实上游 token 总量为基数做会话模拟，并用真实六轮序列逐值回放验证（49713/7863/1205/6635/65438/573 全部精确复现）；同时补齐 server_tool_use/service_tier/inference_geo 等 Opus 5 全字段。
