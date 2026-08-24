# 私下客户订阅管理与 Telegram 到期提醒计划

## 当前目标

在现有 Sub2API 管理后台新增一个与用户、API Key、分组、计费和调度完全隔离的“订阅管理”模块，用于记录私下客户的名称、订阅类型、金额和到期日期，并在到期前一天向 Telegram `CC subscription` 频道发送一次提醒。

## 当前范围

- 新增独立的私下客户订阅数据表与 admin-only CRUD API。
- 业务字段：名称、订阅类型、金额（人民币元）、到期日期。
- 管理页面支持新增、编辑、软删除、搜索、状态筛选和到期状态展示。
- 到期状态按 `Asia/Shanghai` 自然日计算。
- 到期前一天向 Telegram 频道发送提醒，并记录幂等状态，避免重复推送。
- Telegram 密钥只从运行环境读取，不写入数据库、日志、文档或 Git。
- 完成本地后端、前端、浏览器和提醒集成测试后发布生产。

## 当前阶段

| 阶段 | 状态 | 输出 |
| --- | --- | --- |
| 1. 现状与 Telegram 配置只读梳理 | complete | CRUD、后台导航、后台任务、旧环境 Telegram 配置 |
| 2. 数据模型、迁移与后端实现 | complete | schema、migration、repository/service/handler/job |
| 3. 管理后台页面实现 | complete | route、menu、API client、CRUD view |
| 4. 自动化与本地集成验证 | complete | Go、前端、migration integration、真实 API/浏览器 CRUD、Telegram 测试 |
| 5. Git 同步与生产发布准备 | complete | fetch、提交、推送、Linux 构建、数据库/二进制/环境回滚点 |
| 6. 生产发布与验收 | complete | migration、binary、systemd、health、权限边界、前端资源、提醒启动 |

## 当前决策

- 新模块使用独立表，不复用现有支付域的 `subscription_plans` / `user_subscriptions`。
- 金额按人民币元输入和展示，数据库使用精确十进制/最小货币单位，禁止浮点误差。
- 订阅类型预置 `5X`、`20X`，数据层保留扩展为其他文本类型的能力。
- 业务到期日使用 PostgreSQL `DATE`，不让 UTC/时区转换改变日历日期。
- 提醒条件是北京时间“明天到期”；同一订阅同一到期日最多成功发送一次，修改到期日后可对新日期再次提醒。
- 删除使用软删除；软删除记录不参与提醒。
- Telegram 频道和 Bot Token 通过生产环境变量注入。

## 风险与回滚

- 数据库：上线前执行 `pg_dump -Fc --no-owner --no-acl`；新增表不修改现有业务表。
- 应用：替换前备份 `/opt/sub2api/sub2api`；异常时恢复备份并重启 `sub2api.service`。
- Telegram：测试消息会明确标注为测试；发送失败只记录脱敏错误并重试，不影响主服务健康。
- 密钥：任何命令输出、规划文件、提交和最终报告都不得包含 Bot Token 或其他凭据。

## 当前错误记录

| 时间 | 错误 | 处理 |
| --- | --- | --- |
| 2026-07-26 | `planning-with-files` 的 session catchup 不支持解析 Codex 原生 session | 已改用 Git 状态与现有 planning 文件恢复上下文 |
| 2026-07-26 | `go generate ./cmd/server` 命中 `main.go` 中未带 `-mod=mod` 的 Wire 指令，因 `github.com/google/subcommands` 缺少 go.sum 条目失败 | 改用生成文件声明的项目兼容命令 `go run -mod=mod github.com/google/wire/cmd/wire`，不重复原失败命令 |
| 2026-07-26 | 新增 cleanup 依赖后 `cmd/server/wire_gen_test.go` 的直接调用参数少一项，导致 cmd/server 编译失败 | 更新现有 lifecycle 测试，注入可停止的 `PrivateSubscriptionReminderService` stub 并验证清理 |
| 2026-07-26 | 首次 Telegram update 脱敏查询的 shell 引号嵌套错误，zsh 在 jq 变量处解析失败 | 改为无 jq 临时变量的投影表达式，并用 `rg/cut/tr` 提取 token，仍不输出 token 或消息正文 |
| 2026-07-26 | repository integration harness 在 Docker Desktop 可用时仍因 testcontainers 自动探测报 `rootless Docker not found` | 显式传入当前 Docker context 的 socket 作为 `DOCKER_HOST` 后再运行，不重复原环境 |
| 2026-07-26 | 生产 `schema_migrations` 只读查询误用了不存在的 `version` 列 | 先查询 `information_schema.columns` 确认真实列名，再按 schema 查询；未产生任何写入 |
| 2026-07-26 | Computer Use 首次按显示名读取 Telegram 返回 `noWindowsAvailable`，快捷键上下文菜单也未稳定出现 | 改用 Telegram bundle ID 并通过可访问性树直接定位频道与输入框；未操作其他频道 |
| 2026-07-26 | 生产机没有 `rg`，首次提醒日志过滤命令无法执行 | 改用生产机已有的 `grep -E` 精确过滤 `PrivateSubscriptionReminder`；未改变服务状态 |
| 2026-07-26 | 本地清理检查把 zsh 特殊变量 `path` 用作循环变量，临时覆盖了命令搜索路径 | 改用 `target_path` 并调用绝对路径的 `stat`；未删除或修改文件 |
| 2026-07-26 | 首次临时文件清理命令中的 `rm -f` 被安全策略拒绝 | 改用系统 `trash` 将三个精确目标移入废纸篓；两个测试容器先 `stop` 再按名称删除 |

---

# Sub2API Admin API Key 策略管理与 CLIProxyAPI 回迁计划

## 当前目标

将 `cc.claudepool.com` 最终切回 Sub2API，并把 CLIProxyAPI 当前线上 API Key、用量、过期时间、并发等数据迁回 Sub2API。切换前先把 Sub2API 改成 admin-managed API Key 模型：管理员可直接创建、编辑 API Key，并按 key 设置总额度、日/周限额、过期时间和并发量；Sub2API 内部仍保留 user 外键作为承载，但运营上不再要求“一用户一 key”。

## 当前范围

- 后端支持 API Key 级并发字段与限流作用域。
- 后端 admin API 支持创建 API Key，并更新状态、分组、总额度、日/周/5h 限额、过期时间、并发与用量重置。
- 前端 admin 增加 API Key 管理入口，支持直接添加和编辑 key 策略。
- 提供 CLIProxyAPI -> Sub2API 的迁移/对账脚本。
- 完成生产备份、发布、迁移、Caddy 切换和 smoke。
- 保持用户侧 `/keys` 页面不变。

## 当前阶段

| 阶段 | 状态 | 输出 |
| --- | --- | --- |
| 1. 现状确认 | complete | admin/user API Key 能力边界 |
| 2. 后端 key 级并发与 admin 创建 API Key | complete | schema/service/handler/middleware |
| 3. 前端 admin API Key 管理入口 | complete | Admin API key list/create/edit UI |
| 4. 迁移与对账脚本 | complete | CLIProxyAPI -> Sub2API 数据脚本 |
| 5. 本地验证 | complete | Go/前端定向测试、lint、typecheck、build |
| 6. 生产发布与 canary | pending | 新镜像、健康检查、回滚点 |
| 7. 生产数据迁移与切域名 | pending | 数据对账、Caddy 切换、smoke |

## 当前决策

- 2026-05-28：保持“一 API Key 一用户”模型，用用户隔离并发/RPM，用 API Key 字段管理过期时间和额度。
- 2026-05-28：本轮只改 admin 管理面；用户侧目前未开放登录，暂不收紧 `/keys` 页面。
- 2026-06-01：用户确认最终切回 Sub2API；本次把运营模型改为 admin-managed API Key，内部 user 仅作为承载。
- 2026-06-01：API Key 设置并发时按 `api_key_id` 独立限流；未设置或为 0 时回退到用户并发，兼容现有用户侧 key。
- 2026-06-01：API Key 未设置并发但所属组设置并发时，按 `group_id` 共享组级并发池；否则回退 user 并发。
- 2026-06-01：生产切换顺序为先发布 Sub2API 新代码，再迁移 CLIProxyAPI 数据，最后 Caddy 从 `127.0.0.1:8317` 切回 `127.0.0.1:8080`。

## 当前错误记录

| 时间 | 错误 | 处理 |
| --- | --- | --- |
| 2026-05-28 | 本机没有直接可用 `pnpm`；`corepack pnpm` 触发 pnpm 11 依赖状态检查并生成 lockfile 噪音 | 改用已安装的 `frontend/node_modules/.bin/vue-tsc --noEmit`，并清理本次生成的 lockfile/workspace 文件 |
| 2026-06-01 | 本机仍无 `pnpm` 命令 | 改用 `npm run lint:check`、`npm run typecheck`、`npm run build`，三项均通过 |
| 2026-06-01 | 认证热路径最初未在 group preload 中选择 `group.concurrency` | 已补字段选择，并在 `GetByKeyForAuth` SQLite 回归中断言组并发保留 |

---

# Sub2API Claude -> GPT Web Search 兼容任务计划

## 目标

接管 Sub2API 作为后续主要维护项目，梳理 Claude `/v1/messages` -> GPT/OpenAI Responses 的模型映射关系，并按“方案一”移植当前 CLIProxyAPI 中更稳妥的 Claude 客户端 Web search 兼容逻辑。

## 范围

- 记录线上 Sub2API 部署与 SSH 运维信息到项目级 `AGENTS.md`。
- 梳理两层 Claude -> GPT 模型映射：
  - Codex/OpenAI 账号侧模型映射。
  - OpenAI 分组 `/v1/messages` 调度侧模型映射。
- 实现方案一：参考 CLIProxyAPI 当前 Claude -> GPT Web search 兼容层，在 Sub2API 的 OpenAI `/v1/messages` 路径改善 Claude CLI / VSCode 对 `web_search_call` 的展示与流式兼容。
- 将本次改动、线上事实、验证结果落到项目文档。

## 非目标

- 不默认把 OpenAI 原生 web_search 替换为 Brave/Tavily 模拟。
- 不把方案三作为主路径，不依赖不稳定的 OpenAI 搜索引用内部字段。
- 不泄露线上 API Key、账号密钥、数据库密码等敏感信息。

## 阶段

| 阶段 | 状态 | 输出 |
| --- | --- | --- |
| 1. 建立 planning 文件 | complete | `task_plan.md`、`findings.md`、`progress.md` |
| 2. 线上与本地项目接管信息梳理 | complete | `AGENTS.md` 运维记录与项目 skills |
| 3. Claude -> GPT 模型映射关系梳理 | complete | `findings.md` 与任务文档 |
| 4. 方案一设计落地 | complete | 代码实现与单测 |
| 5. 本地回归验证 | complete | Go/前端相关测试结果 |
| 6. 文档归档 | complete | `docs/` 任务记录 |
| 7. 线上发布/验证决策 | paused | 用户暂停上线，先做稳定性迁移评估 |
| 8. 旧项目稳定性经验迁移矩阵 | complete | `docs/claude_gpt_stability_migration_matrix_20260527_CN.md` |
| 9. 测试缺口清单 | complete | 文档矩阵与 KnownGap characterization tests |
| 10. 业务逻辑修复 | complete | 已修复 4 个 KnownGap |
| 11. 上线前黑盒与发布 | complete | canary、Claude CLI 黑盒、生产部署、健康检查与生产 SSE smoke 已完成 |
| 12. 线上 WebSearch 路径纠偏 | complete | 已补 Claude Code `WebSearch` -> OpenAI 原生 `web_search` 入口映射、单测、本地 cc1/TTY 黑盒 |
| 13. WebSearch query 泄漏修复 | complete | 已屏蔽 continuation summary 作为 action/fallback 搜索词，并在 SSE 出口抑制文本型 web_search tool_call 泄漏；生产镜像 `main-2e01e876` 已 healthy |
| 14. WebSearch 来源/链接可见性分析 | complete | 已确认并本地修复 `sources/url/annotations` 丢失；通过后端全量测试，待本地黑盒和上线决策 |
| 15. 生产账号分组绑定整理 | in_progress | 已完成当前账号-分组绑定只读快照，待用户确认后批量补齐未删除账号到所有未删除分组 |
| 16. `/key-usage` 模型黑盒展示 | complete | 已修复用户侧 `model_stats` 聚合口径：Claude 内部转 GPT 不向用户显示 GPT，直接请求 GPT 仍显示 GPT |
| 17. Claude -> GPT 兼容库边界 | complete | 新增 `internal/pkg/claudegptcompat`，把客户端识别、WebSearch query 清洗、synthetic 搜索进度、sources/url/citation 辅助从 `apicompat` 抽出 |
| 18. 本地黑盒沙盒与维护边界固化 | complete | 本地 Sub2API dev 镜像重建；本地 Opus->GPT-5.5 分组/API Key 配置；直接 API smoke、Claude CLI `-p`、WebSearch stream-json 黑盒通过；两个项目 `AGENTS.md` 和回归 skill 已更新 |
| 19. 2026-06-01 本地黑盒复验与上线门禁 | complete | 直接 API、Claude CLI `-p`、WebSearch stream-json、真实 TTY 连续两轮、Go 全量测试、前端 lint/typecheck/build 均通过 |
| 20. 2026-06-01 生产发布与上线观察 | complete | 已推送主线、构建并上线 `zhangtaylor985/sub2api:main-19663655`；canary 与正式 `/health`、直接 `/v1/messages` smoke 通过，canary 已清理 |
| 21. 2026-06-01 生产 Opus -> GPT-5.5 映射收敛 | complete | 已更新 6 个 OpenAI dispatch 分组、清理 auth cache、重启 app；生产 4-6/4-7/4-8 direct smoke 与 usage log 均确认 `→gpt-5.5` |
| 22. OpenAI dispatch 多轮 session 粘性修复 | complete | 已调整 session 信号优先级为显式 session > `metadata.user_id` > content fallback；补回归并通过后端全量测试 |
| 23. API Key 模型族限制迁移 | complete | 已上线 key 级 Claude/GPT family policy，从旧 audit policy 回填并完成生产 smoke |
| 24. 2026-06-02 生产数据本地恢复 | complete | 已备份本地 PG17 沙盒，创建独立 PG18 恢复库并从线上 PG18 dump 恢复；关键表校验通过 |
| 25. Claude -> GPT 上游错误黑盒 | complete | 已上线 `/v1/messages` dispatch 上游错误泛化；生产 smoke 确认客户端不暴露 GPT/Codex/ChatGPT/auth file/internal routing 错误 |
| 26. API Key 级 Claude -> GPT 目标模型覆盖 | complete | 已上线 key 级覆盖并把生产 `api_keys.id=125` 配为 `gpt-5.4`；canary 与正式生产 smoke 均确认 `→gpt-5.4` |
| 27. 生产错误可观测性与 Request ID 排查 | complete | 已确认截图为 Claude Code 本地输出上限口径；Sub2API 日志落点/级别已梳理；错误体 request_id 已实现、测试、上线到 `main-191cbfcd` |
| 28. 生产全 API Key Claude -> GPT 映射收敛 | complete | 已全量写入 82 个有效 API Key 的 key 级 Opus/Sonnet 映射；Opus `→gpt-5.4` 黑盒通过，Sonnet `→gpt-5.3-codex` 映射生效但当前生产 ChatGPT/Codex 账号不支持该目标模型 |
| 29. `/v1/models` GPT 黑盒展示修复 | complete | 已上线 `zhangtaylor985/sub2api:main-d271fbbf`；同一 Claude-only key 公开 `/v1/models` 只返回 Claude 模型，`contains_gpt=false` |
| 30. 2026-06-05 新服务器迁移快照 | complete | 已重新同步生产 PG18 dump 到本地恢复库，并打包 Sub2API runtime、部署配置、Caddyfile 和 50 个 Codex auth JSON；迁移说明已归档 |
| 31. 2026-06-08 新服务器迁移快照 | complete | 已重新同步生产 PG18 dump 到本地恢复库，并再次打包 Sub2API runtime、部署配置、Caddyfile 和 50 个 Codex auth JSON |
| 32. 2026-06-09 新服务器迁移快照 | complete | 已重新同步生产 PG18 dump 到本地恢复库，并再次打包 Sub2API runtime、部署配置、Caddyfile 和 50 个 Codex auth JSON |
| 33. 2026-06-10 生产 502 代理认证失败排查 | complete | 截图中的 `502 Upstream request failed` 已确认为 Codex/OpenAI 上游请求走 SOCKS5 代理时认证失败；生产服务本身 healthy，账号 UI 状态正常不代表代理可用 |
| 34. 2026-06-10 生产 SOCKS5 代理续费后更新验证 | complete | 已按用户提供文本更新 4 条代理为 `socks5` 并验证均可认证连通；另发现未包含在本次文本里的两个旧代理仍在报认证失败 |
| 35. 2026-06-10 生产代理收敛与账号均摊 | complete | 已将 9 个 active+schedulable OpenAI/Codex 账号按 `3/2/2/2` 均摊到 4 个可用 SOCKS5 IP，软删除旧代理并重启 app；重启后未再见旧代理认证失败 |
| 36. 2026-06-10 新服务器迁移快照 | complete | 已重新同步生产 PG18 dump 到本地恢复库，并再次打包 Sub2API runtime、部署配置、Caddyfile 和 50 个 Codex auth JSON |
| 37. 2026-06-13 生产迁移到新机器 | complete | 新机 systemd + PostgreSQL 18 + Redis 已运行；Cloudflare DNS A 记录已直切到 `172.247.109.38`；新机 Caddy 已签发证书并通过公网 health/API smoke |
| 38. 2026-06-13 坏 SOCKS 代理下线与 Codex auth 重新均摊 | complete | 已将 `69.3.236.211:443` 软删除并设为 inactive；7 个 active+schedulable OpenAI OAuth 账号已按 `3/2/2` 均摊到剩余 3 个 SOCKS5 IP，重启 systemd app 后 `.codex_capi` `/responses` smoke 连续成功 |
| 39. 2026-06-15 API Key 纯流量包模式修复 | complete | 本地已修复 token-package-only API Key 后扣不进入流量包账本的问题；后端全量测试通过；按要求尚未上线 |
| 40. 2026-06-15 生产 Codex 流量包分组 | complete | 已新增 `Codex Token Package Pool` (`group_id=19`) 标准 OpenAI 分组，绑定当前 10 个未删除 OpenAI 账号，其中 6 个 active+schedulable；未自动切换任何 API Key |
| 41. 2026-06-18 生产 Claude -> GPT 默认映射调整 | complete | 已将 8 个 active OpenAI 分组默认 Opus 和 Opus exact mapping 调整为 `gpt-5.4`，Sonnet 保持 `gpt-5.3-codex`；已备份、清 Redis auth cache 并复核 `/health` |
| 42. 2026-06-26 生产 API Key 日限额时区排查 | complete | 只读确认 app 环境为 `TZ=Asia/Shanghai`，但 API Key rate-limit DB 更新仍用 PostgreSQL UTC `date_trunc('day', NOW())` 写入日窗口；该 key 北京时间当天确有 `/v1/messages` 用量入账，截图中环形日限额与明细日表口径不一致 |
| 43. 2026-06-26 新机 SOCKS5 入口 | complete | 已复用现有 Xray 服务新增认证 SOCKS5 入站 `172.247.109.38:26812`，并切换本机 `.codex_td/.env` 代理目标；带认证 smoke 通过，无认证被拒绝 |
| 44. 2026-07-02 Claude Fable 5 支持 | complete | 已上线 `claude-fable-5`：OpenAI dispatch 内部路由到 `gpt-5.4`，usage 保持请求模型并按 Fable 官方价计费；生产 smoke HTTP 200，`model_mapping_chain=claude-fable-5→gpt-5.4` |
| 45. 2026-07-06 关闭 Claude Fable 5 支持 | complete | 已移除 Fable 模型列表、dispatch family、专用计费 override 和生产 exact mapping；当前线上 binary `f9d4f750...`，`/v1/models` 不含 Fable，Fable 请求不再产生成功 usage，Opus smoke 正常 |
| 46. 2026-07-06 Claude Fable 5 显式开关 | complete | 本地已实现默认关闭、单 API Key 和 OpenAI 分组级 Fable 5 UI 开关；底层复用 `exact_model_mappings.claude-fable-5=gpt-5.4`，不新增 schema，不恢复默认 family 放行；已通过后端全量测试和前端 typecheck/build |
| 47. 2026-07-06 Claude Fable 5 单 Key 生产开启 | complete | 已通过本地二进制 patch 上线显式开关与 body/计费修复；当前线上 binary `c6f16bc9...`，仅 `api_key_id=110` 有 Fable exact mapping，group 全局为 0；生产 smoke 与 Fable 动态价 usage 均通过 |
| 48. 2026-07-07 Claude -> GPT 用户侧模型黑盒 | complete | 已上线 binary `b0239cc6...`；OpenAI `/v1/messages` 上游仍走 `gpt-5.4`，但用户侧非流式/流式响应模型保持原始 Claude 模型；Fable 最新 usage 已恢复按 Fable 动态价计费 |

## 决策记录

- 2026-05-27：用户明确选择只做方案一。
- 2026-05-27：Sub2API 将作为后续主要维护项目，旧 CLIProxyAPI 项目只作为参考来源。
- 2026-05-27：线上 Sub2API 使用 `weishaw/sub2api:latest` 镜像，宿主机不是源码 Git 工作区；本次先完成本地实现、测试和文档，线上发布需先确定镜像 tag/registry/回滚流程。
- 2026-05-27：因 Postgres/Redis 仍在 Docker 网络内，短期生产发布采用应用容器替换/重启；宿主机 systemd 直跑作为后续独立迁移任务。
- 2026-05-27：用户要求先完成旧项目 Claude -> GPT 稳定性经验的迁移矩阵与测试缺口清单；第二步允许补测试代码，但不改业务逻辑。
- 2026-05-27：用户要求后续黑盒优先使用本地启动 Sub2API 并在本地授权 Codex auth file；远端 canary 只作为生产同配置验证手段。
- 2026-05-27：本次发布不打 Git tag；生产 Docker 镜像使用 `zhangtaylor985/sub2api:main-decdc6d0`。
- 2026-05-27：Claude Code/VSCode 的 `name:"WebSearch"` 客户端工具应在 Claude -> GPT 入口映射为 OpenAI 原生 `web_search`；否则会退回 Claude Code 原生 Web Search，表现为慢且常见 0 results。
- 2026-05-29：新增问题边界：Sub2API 目前能显示 `Searching/Searched`，但没有像 CLIProxyAPI 那样在搜索过程或最终答案中展示来源/链接；本阶段先做对照分析，不急于线上热修。
- 2026-05-29：生产账号分组整理采用“先快照、再确认、后写库”的流程；默认只处理 `deleted_at IS NULL` 的账号和分组，不恢复已删除账号绑定。
- 2026-05-29：`/key-usage` 用户侧用量展示必须保持黑盒；Claude 请求经内部调度转 GPT 时不显示 GPT，只有用户客户端直接请求 GPT 时才显示 GPT。
- 2026-05-29：Claude -> GPT 的专用兼容逻辑应放在 `internal/pkg/claudegptcompat`，`apicompat` 只做协议类型和转换编排；原生 Claude 账号路径不应依赖该库。
- 2026-05-29：Sub2API 后续主要维护目录是 `/Users/taylor/sdk/sub2api`；`/Users/taylor/code/tools/CLIProxyAPI-ori` 只作为 Claude -> GPT 兼容迁移参考。两个项目共享线上环境，排查/部署前必须确认目标服务。
- 2026-05-29：Claude->GPT 黑盒优先本地沙盒：直接 API smoke 先验证分组/API Key/模型映射，再用 Claude CLI/`cc1` 验证真实客户端；生产 canary 只作为上线前同配置验证。
- 2026-06-01：本地 Docker 环境缺失旧 dev compose 容器时，可以用“Postgres/Redis Docker 依赖 + 当前源码 tmux 直跑后端”的沙盒形态完成黑盒；该形态需要单独记录端口和数据目录，避免误认为只能使用 `sub2api-dev`。
- 2026-06-01：本次上线门禁采用“本地真实 Codex auth file 黑盒 + 全量自动化测试 + 生产 canary”三段式；生产只在 canary health/smoke 通过后替换 app 容器，Postgres/Redis 不随应用协议修复一起迁移。
- 2026-06-01：本次发布只替换 Sub2API app 容器；运行镜像从 `main-853b8019` 切到 `main-19663655`，Postgres/Redis 不动。生产测试 key 所在分组当前仍把 `claude-opus-4-7` 映射到 `gpt-5.4`，该配置问题不在本次代码发布中修改。
- 2026-06-01：生产 Opus -> GPT-5.5 收敛优先改 OpenAI 分组 `messages_dispatch_model_config`；不为原本没有 `model_mapping` 的 active OpenAI OAuth 账号新增账号级映射，避免把“无限制账号”意外变成模型白名单账号。
- 2026-06-01：OpenAI `/v1/messages` dispatch 的账号粘性应优先使用显式 session header / prompt_cache_key，其次使用 Claude `metadata.user_id`，最后才回退 content-based seed；这样与原生 Claude/Gateway 路径保持一致，也避免 compact/resume 改写首轮内容后换账号。
- 2026-06-01：API Key 的 Claude-only/GPT-only 应按用户请求模型族判断，而不是按内部上游模型判断。Claude-only key 可以内部 Claude -> GPT，但用户不能直接请求 GPT family；GPT-only key 不能请求 Claude family。该策略必须 key 级表达，不能用当前空置且偏 group/channel 维度的 channel model restriction 代替。
- 2026-06-02：Claude `/v1/messages` 经 OpenAI dispatch 到 GPT/Codex 时，上游错误属于内部路由错误；客户端错误响应必须泛化，不得包含 GPT/Codex/ChatGPT account/auth file/内部账号细节。该脱敏只作用于 Claude -> GPT 的 Anthropic 响应格式，不扩大到 OpenAI 原生 passthrough。
- 2026-06-02：API Key 级 Claude -> GPT 目标模型覆盖应高于分组级 `messages_dispatch_model_config`，低于账号级 `credentials.model_mapping` 的最终上游改写/白名单语义。空 key 级配置必须表示“不覆盖”，继续走分组默认，避免影响已有 key。
- 2026-06-02：全量 API Key Opus/Sonnet 目标模型收敛使用 API Key 级 `messages_dispatch_model_config`，不改账号级 `credentials.model_mapping`。Sonnet 目标按用户要求保持 `gpt-5.3-codex`；即使黑盒验证发现当前生产账号不支持该模型，也不擅自改成其他可用模型。
- 2026-06-04：`/v1/models` 属于用户可见模型清单，必须按 API Key 的 `allow_claude_family` / `allow_gpt_family` 做黑盒过滤；OpenAI dispatch 分组中账号 `model_mapping` 的 GPT key 是内部上游白名单，不应直接暴露给 Claude-only 用户。
- 2026-06-13：生产迁移目标新机为 `root@172.247.109.38:41012`；新生产建议采用 systemd 管理 Sub2API app，并把 PostgreSQL/Redis 一并迁到新机宿主机服务，避免 app 宿主机化后仍依赖旧 Docker 网络。
- 2026-06-15：API Key 纯流量包模式必须同时覆盖前置限额检查和后扣账本写入；只有前置检查存在时，流量包用尽拦截会生效，但成功请求不会真实扣减 `api_key_token_packages.used_usd`。本次本地修复只改后扣命令构造，不上线。
- 2026-06-15：生产新增的 `Codex Token Package Pool` 只作为可选目标分组，不自动迁移现有 API Key；API Key 切换分组后应清理/失效对应 auth cache 或通过管理 API 更新，以确保新分组快照立即生效。
- 2026-06-17：图片生成权限需要支持 API Key 级开关，默认允许；最终生效仍需同时满足 API Key 级 `allow_image_generation=true` 与分组级 `groups.allow_image_generation=true`。现有 key 默认迁移为允许，避免发布后意外关停。
- 2026-06-18：生产 Claude -> GPT 默认映射按用户要求收敛到分组层：Opus family default 与 `claude-opus-4-6/4-7/4-8` exact mapping 均为 `gpt-5.4`，Sonnet family default 保持 `gpt-5.3-codex`；API Key 级覆盖仍高于分组默认。
- 2026-06-26：API Key rate-limit 的 `usage_1d/window_1d_start` 不能继续依赖 DB session `date_trunc('day', NOW())`，否则生产 PostgreSQL 为 UTC 时会与应用 `TZ=Asia/Shanghai`、用户侧 `/v1/usage` 明细日期口径错位。
- 2026-07-02：`claude-fable-5` 对外应保持 Claude 请求模型，OpenAI dispatch 内部路由按 Opus family 走 `gpt-5.4`；计费必须按 Claude Fable 5 官方 API 价格，不按内部 `gpt-5.4` 成本价。
- 2026-07-06：Fable 5 后续采用“默认关闭、显式 exact mapping 开启”的策略。单 key 开关写 `api_keys.messages_dispatch_model_config.exact_model_mappings.claude-fable-5=gpt-5.4`；分组/全局开关写 OpenAI dispatch 分组同名 exact mapping；不再把 Fable 加入全局 `DefaultModels` 或 Claude family 默认路由。
- 2026-07-06：Fable 5 单 key 生产开启以 API Key exact mapping 为准，不批量写 group 或其它 key；即使请求体为上游兼容改写成 `gpt-5.4`，计费仍必须优先使用 `requested_model=claude-fable-5` 的动态价格。
- 2026-07-07：OpenAI `/v1/messages` dispatch 允许在上游请求体中改写为内部 GPT/Codex 模型，但 Anthropic 响应体和流式 `message_start.message.model` 必须保持原始 Claude 请求模型；内部 GPT 模型只出现在后台 usage/log 的 `upstream_model` / mapping 证据中。
- 2026-07-07：Claude Code 恢复失败会回退 `claude-fable-5`，因此 OpenAI dispatch 的 Claude family 黑盒策略必须默认覆盖 Fable；现有 OpenAI dispatch 分组统一补 `claude-fable-5 -> gpt-5.4`，代码默认也将 Fable 映射到 `gpt-5.4`，避免新分组/新 key 再出现同类漏配。

## 错误记录

| 时间 | 错误 | 处理 |
| --- | --- | --- |
| 2026-05-27 | 初始 `rg` 组合模式未返回结果 | 后续改为更精确的分文件/分目录搜索 |
| 2026-05-27 | 一次 `find ... | sed` 文件列表命令在 macOS sed 下参数错误 | 改用 `find`/`rg --files` 直接列文件 |
| 2026-05-27 | 在仓库根目录运行 Go test 导致 module 解析失败 | 改到 `backend/` 模块目录运行测试 |
| 2026-05-27 | 在 `backend/` 目录执行 gofmt 时误带 `backend/` 路径前缀 | 使用模块内相对路径重跑 |
| 2026-05-27 | `python3 tools/secret_scan.py` 不存在 | 记录门禁缺口，本次改用改动范围敏感词扫描兜底 |
| 2026-05-27 | `git push origin main` 被 GitHub 拒绝，当前 SSH 身份 `DevDynamo2024` 对 `zhangtaylor985-ai/sub2api.git` 无写权限 | 本地 commit/tag 已完成；继续检查本机是否有可用 GitHub 凭据或 host alias，若没有则先用线上可拉取方式做 canary/生产验证 |
| 2026-05-27 | 线上 HTTPS clone `zhangtaylor985-ai/sub2api` 长时间未完成并早退 | 已中断该 canary 拉取；后续改用本地打包传输或 SSH 方案，避免阻塞生产验证 |
| 2026-05-27 | 在 `backend/` 目录执行 gofmt 时误用 `backend/internal/...` 路径 | 改用模块内 `internal/...` 相对路径重跑，测试通过 |
| 2026-05-27 | 远端轻量 canary 首次启动进入 setup wizard | 改为按生产容器形态挂载 `/root/cliapp/sub2api/data` 并设置生产 env，`/health` 通过 |
| 2026-05-27 | Claude CLI 环境变量覆盖 `ANTHROPIC_BASE_URL` 未生效，仍读取 settings 中的 `127.0.0.1:8080` | 临时修改 `~/.claude_local/settings.json` 到 `127.0.0.1:18080` 测试，完成后恢复 `127.0.0.1:8080` |
| 2026-05-27 | 固定字符串 TTY 测试触发 Claude Code debug 中非致命标题 JSON parse 噪音 | 追加自然 TTY prompt 验证正常交互无该 parse 噪音；服务端请求均为 HTTP 200 |
| 2026-05-27 | 本地启动 Sub2API 首次使用 32 字符 `TOTP_ENCRYPTION_KEY` 失败，服务要求 64 hex 字符 | 清理临时 data dir 后使用 64 hex 字符重启，健康检查通过 |
| 2026-05-27 | 线上用户截图显示 `Searched:` 后泄漏 Claude Code continuation summary | 根因是 OpenAI `web_search_call` 缺失 `action.query` 时使用 unsafe fallback query；已补清洗、防泄漏单测和 generic searched 文案 |
| 2026-05-29 | 账号快照查询首次 SELECT 未给 `accounts.id` 加别名，Postgres 报 `column reference "id" is ambiguous` | 未写入任何数据；改为 `a.id/a.name/...` 别名查询后成功 |
| 2026-05-29 | 本地 dev compose 重建首次缺 `POSTGRES_PASSWORD` | 当前 shell 未加载 compose 所需 env；改为从运行中容器读取非打印 env 并导出后重建 |
| 2026-05-29 | 查询 usage log 时误选不存在的 `status_code` 列 | 先用 `\d usage_logs` 查看 schema，再改用存在的 `request_type/model/upstream_model/model_mapping_chain` 等字段 |
| 2026-05-29 | 首次 Claude CLI 黑盒 401 `Invalid API key` | 原因是 `~/.claude_local/settings.json` 内 `env.ANTHROPIC_AUTH_TOKEN` 覆盖临时 shell token；备份并更新 settings 后通过 |
| 2026-06-01 | 生产 Redis auth snapshot 首次清理时 redis-cli 继承空 `REDISCLI_AUTH`，出现 AUTH 提示且未删除快照 | 改用 `env -u REDISCLI_AUTH` 复核并删除 15 个 `apikey:auth:*` 快照，最终剩余 0 |
| 2026-06-01 | 生产 `claude-opus-4-8` usage log 查询的 shell 单引号被外层命令吃掉，SQL 报 `column "claude" does not exist` | 请求本身 HTTP 200；改用独立 quoted heredoc 重新查询，确认 `claude-opus-4-8→gpt-5.5` |
| 2026-06-01 | 生产 canary 首次 `docker run` 复制正式容器 env 时带入空行，Docker 报 `invalid environment variable` | 未启动 canary、未影响正式容器；过滤空 env 后重新启动 canary，健康检查通过 |
| 2026-06-02 | 在 `backend/` 工作目录内误用 `backend/internal/repository/api_key_repo.go` 路径执行 gofmt，报 `lstat ... no such file or directory` | 无文件改动；改用模块内相对路径 `internal/repository/api_key_repo.go` 后通过 |
| 2026-06-02 | 本地黑盒插入测试用户时误用 `ON CONFLICT(email)`，但本地 `users.email` 无唯一约束 | 该 SQL 未写入测试数据；改为 `WHERE NOT EXISTS` 显式插入/更新后继续验证 |
| 2026-06-02 | 首次生产更新 `messages_dispatch_model_config` 的 SQL 被 shell 引号吃掉 JSON 双引号，Postgres 报 `syntax error at or near ":"` | 该 SQL 未写入任何数据；改用外层 heredoc 直接喂给远端 `psql` 后更新成功 |
| 2026-06-02 | 生产 Redis 清 auth cache 第一次命令被本地 shell 展开远端变量，导致 `tmp` 为空且未清理 | 改用远端 heredoc 执行脚本，成功删除 14 个 auth cache snapshot |
| 2026-06-02 | 生产日志筛选使用 `rg`，但远端主机未安装 `rg` | 改用 `grep -Ei` 检查最近日志 |
| 2026-06-02 | 全 API Key 映射收敛首次备份 SQL 被 shell 引号吃掉 `'{}'`，生成 0 行备份 | 未写库；立即重跑 heredoc 备份，得到 82 行有效备份文件 |
| 2026-06-02 | 全 API Key 映射收敛清理 Redis auth cache 时先后遇到空 `REDISCLI_AUTH` 提示和一次远端变量引用失败 | 用 `env -u REDISCLI_AUTH` 复核并重新删除，最终 `apikey:auth:*` 剩余 0 |
| 2026-06-02 | 全 API Key 映射收敛后一次只读 SQL 复核因 docker exec 环境变量和 JSON 引号处理失败 | 未写库；改为通过 stdin 把 SQL 传入容器内 `psql`，复核结果为 82/82/82/82 |
| 2026-06-18 | 调整生产映射时 SSH 入口多次在握手阶段 reset | 改用更保守 SSH 参数并等待入口恢复；确认失败尝试均未进入远端写库命令，最终通过 `IPQoS=none` 连接完成 |
| 2026-06-18 | 首次 `pg_dump -f /root/...` 由 `postgres` 用户写 root 目录失败 | 更新 SQL 尚未执行；改为 `pg_dump` stdout 由 root 重定向写备份文件后重跑成功 |
| 2026-07-02 | 生产 Fable 热配置后 smoke 仍把 `claude-fable-5` 发到上游 Codex，返回不支持该 Claude 模型 | 分组/API Key exact mapping 已写入并清 Redis，但线上旧二进制不识别 Fable family；改为发布代码修复 |
| 2026-07-02 | 首版 Fable 二进制上线后路由正确，但 usage 费用按 `gpt-5.4` 价计费 | 补 `OpenAI /v1/messages` dispatch 的 Fable 计费优先使用请求模型，新增单测并二次上线 |
| 2026-07-02 | 新机 SSH 上传大文件极慢，远端 2G 内存无 swap 导致远端 Go 构建被 OOM 杀掉 | 改用本地 Linux 构建；首版通过 `.zst` 上传，二次修复用 `zstd --patch-from` 基于上一版二进制生成 2.4M patch |
| 2026-07-06 | 生产 Fable 关闭前确认当前线上 binary 已不是 7 月 2 日版本，不能直接回滚旧备份 | 基于当前线上 sha256 `427066...` 构建关闭版并用 `zstd --patch-from` 生成 2.5M patch，避免覆盖 7 月 4 日后续功能 |
| 2026-07-06 | Fable 单 key 首轮上线后，调度映射已选 `gpt-5.4`，但 `/v1/messages` 转发 body 仍保留 `claude-fable-5`，上游 Codex/ChatGPT 账号拒绝 | 补 `buildOpenAIMessagesForwardBody`，无渠道级 mapping 时把 dispatch 默认映射写入 body；补单测并二次 binary patch 上线 |
| 2026-07-06 | Fable body 改写后生产 smoke 成功，但 usage 成本按 `gpt-5.4` 价而非 Fable API 价计算 | 补 usage 计费候选优先级：`requested_model=claude-fable-5` 且结果模型已改写时优先按 Fable 动态价；补单测并三次 binary patch 上线 |
| 2026-07-07 | Claude Code 报 `Session model gpt-5.4 could not be restored` | 根因是 Fable/Opus dispatch 的 body 预改写让服务层把用户侧响应模型也当成 `gpt-5.4`；新增 `ForwardAsAnthropicWithDisplayModel`，handler 传原始 `reqModel` 作为 display model，非流式和流式响应均不再暴露 GPT 模型 |
| 2026-07-07 | 第一版 display-model hotfix 修复用户侧模型后，Fable smoke usage 又按 `gpt-5.4` 价计费 | 原因是计费 helper 只看 `result.Model != requested`；display model 修复后 `result.Model=claude-fable-5`，需改为同时看 `upstream_model=gpt-5.4`。已发布第二版 `display-model-billing`，最新 Fable usage 按 Fable 动态价 |
| 2026-07-07 | 实时复线发现 `api_key_id=494` 的 `claude-fable-5` 仍 502 | 原因是 7 月 6 日显式开关策略下该 key 和分组都没有 Fable exact mapping，Claude Code 恢复失败回退 Fable 后会直接打到 ChatGPT/Codex 上游；已给 8 个 OpenAI dispatch 分组补 `claude-fable-5=gpt-5.4` 并上线代码默认映射，key 494 新建和 resume smoke 均通过 |

---

# 2026-07-08 API Key 纯流量包 `quota_exhausted` 修复

## 目标

修复 `token_package_required=true` 的 API Key 仍被传统 `quota/quota_used/status=quota_exhausted` 拦截的问题，并上线到新机 systemd 生产环境；不执行单独数据库恢复。

## 阶段

| 阶段 | 状态 | 输出 |
| --- | --- | --- |
| 1. 只读定位 | complete | 已确认目标 key 有 token package 剩余但传统 quota 超限 |
| 2. 代码修复 | complete | 认证和后扣口径改为纯流量包优先 |
| 3. 本地回归 | complete | targeted Go、后端全量、前端 build、嵌入式构建均通过 |
| 4. 生产发布 | complete | 已上线 systemd binary `4ede9ae8...`，目标 key smoke 通过 |

---

# 2026-07-10 GPT/Codex 5.6 支持

## 目标

支持 `gpt-5.6-sol`、`gpt-5.6-terra`、`gpt-5.6-luna` 的 Codex/OpenAI 请求、模型清单与准确计费；完成本地和生产 canary 回归后上线，并确保 Claude `/v1/messages`、模型列表和用户侧模型黑盒不受影响。

## 阶段

| 阶段 | 状态 | 输出 |
| --- | --- | --- |
| 1. 原版与生产只读核验 | complete | 已定位原版 `6cea1c35` 和后续 manifest `13e773ef`；确认生产价格源与账号白名单现状 |
| 2. 代码实现与单元测试 | complete | 三款精确模型、独立定价、Codex manifest、账号排除与 OAuth 策略保留均已实现；bare 5.6 不兜底 |
| 3. 本地生产级回归 | complete | Go 全量、unit tag、定向 race、前端 lint/typecheck/Vitest/build、embedded build 全部通过 |
| 4. 远端 canary | complete | 三款 Responses、OpenCode、SSE、manifest、Claude Opus/Fable、真实 `claude2`、usage/计费均通过 |
| 5. 生产发布 | complete | 已发布 sha256 `11125e55...`；health、Claude 黑盒、费用与稳定性复核通过，回滚备份已保留 |

---

# 2026-07-14 Claude Sonnet 5 完整支持与默认映射收敛

## 目标

在不覆盖当前 dirty worktree 和已上线 GPT/Codex 5.6 能力的前提下，补齐 `claude-sonnet-5` 的模型发现、管理界面、Bedrock 映射和 1M context beta 策略；将 OpenAI `/v1/messages` Sonnet family 的代码、UI 和生产有效配置收敛到 `gpt-5.4`，完成生产级回归、canary 和上线。

## 范围与决策

- 参考上游 `db041423 feat: 适配 sonnet5`，仅移植与当前 fork 相容的 Sonnet 5 增量，不直接 cherry-pick。
- 保留已有 `claude-sonnet-5` 后端默认模型和 Sonnet family `gpt-5.4` 代码默认，补齐前端默认、模型 whitelist/preset、Bedrock 映射和 1M beta 白名单。
- OpenAI dispatch 计费继续使用实际上游 `gpt-5.4` 价格；本次不改变计费语义。
- 生产仅移除值恰好为旧 UI 默认 `gpt-5.3-codex` 的 key 级 `sonnet_mapped_model` 覆盖，使其继承分组 `gpt-5.4`；保留其他显式自定义。
- 发布继续使用当前已确认的本地 Linux amd64 binary + 远端 systemd 流程，不把已有未提交改动混成本次 Git commit。

## 阶段

| 阶段 | 状态 | 输出 |
| --- | --- | --- |
| 1. 现状与上游对比 | complete | 已确认代码/前端/生产配置缺口及上游参考提交 |
| 2. 代码与测试实现 | complete | 已补齐 Sonnet 5 Bedrock/1M beta/前端和 UI 默认 5.4，定向回归通过 |
| 3. 本地生产级回归 | complete | Go/frontend/embed 全量门禁通过；本地模型清单与失败黑盒无内部模型泄漏 |
| 4. 远程 canary | complete | `127.0.0.1:18080` health、API、真实 `claude2` TTY 双轮、usage 与计费均通过 |
| 5. 生产配置收敛与发布 | complete | 完整 DB/配置备份；84 个旧 key 覆盖改为继承；systemd binary 切换成功 |
| 6. 发布后验收 | complete | 公网 API、1M beta、正式 `claude2`、usage/计费、日志、清理与回滚记录均完成 |

## 错误记录

| 时间 | 错误 | 处理 |
| --- | --- | --- |
| 2026-07-14 | 生产 `usage_logs` 不存在 `status` 列 | 先查 `information_schema.columns`，改用实际存在的 usage 字段完成只读统计 |
| 2026-07-14 | zsh 对不存在的根目录 `docker-compose*.yml` glob 直接报 `no matches found` | 未影响任何状态；改用 `rg --files` 和明确的 `deploy/docker-compose.*.yml` 路径 |
| 2026-07-14 | 本地首次清 auth cache 时对包含换行的临时 key 文件直接做 sha256，未命中实际 cache key | 对脱除 CR/LF 的 token 字节计算 sha256，成功删除 1 条 snapshot 并验证 Claude-only 模型列表 |
| 2026-07-14 | 首次真实 TTY 启动带 `--dangerously-skip-permissions`，进入首次使用确认后选择退出 | 未发起模型请求；改用不需要工具权限的普通 TTY 会话，同一会话连续两轮成功 |
| 2026-07-14 | 发布后清理核验命令中的远端 `awk` 转义错误 | 停止与端口检查已完成；单独重跑 SHA 与服务状态核验，正式 binary 和 service 均正确 |

---

# 2026-07-21 生产 Codex `/responses` 流式断连排查

## 目标

定位 `CODEX_HOME=$HOME/.codex_capi codex` 使用 `base_url = "https://cc.claudepool.com"` 时，请求 `/responses` 在流完成前断开的原因；先只读检查新生产机、反代、应用与上游错误，不做重启、配置修改或发布。

## 阶段

| 阶段 | 状态 | 输出 |
| --- | --- | --- |
| 1. 现象与基线确认 | complete | 公网/内网 health、systemd/Caddy 正常，应用无重启 |
| 2. 错误与请求链路定位 | complete | 截图请求精确命中账号 4 与代理 11，133.9 秒后 TCP timeout/502 |
| 3. 独立复现与归因 | complete | 新生产机到坏代理不可达，另一代理可达；成功与失败账号分片一致 |
| 4. 结论与处置建议 | complete | 根因为账号 4/9 所绑 SOCKS5 从生产机不可达；尚未执行修复 |

## 安全边界

- 仅连接新生产机 `172.247.109.38:41012`，不混用旧机回滚环境。
- 不读取或回显 API Key、OAuth token、代理密码、数据库/Redis 密码。
- 未经用户确认，不重启服务、不改数据库/Redis/Caddy/环境变量、不替换 binary。

## 结论

- `cc.claudepool.com`、Caddy 和 Sub2API 整体健康，故障不是域名/TLS/反代或进程宕机。
- `account_id=4/9` 仍 active+schedulable，但共同绑定的 `proxy_id=11` (`54.176.138.113:10808`) 从新生产机 TCP 不可达；请求被调度到这两个账号时约 2 分钟后 502，Codex 表现为流断开并重连。
- 其余直连账号和 `proxy_id=10` (`54.241.144.215:10808`) 可用，因此故障呈间歇性。
- 本轮只读排查已完成；修复需用户确认后执行，可先下线账号 4/9 的调度以止血，或将其迁移到可用代理/修复 proxy 11 网络策略。

## 2026-07-21 用户确认后的代理下线

| 阶段 | 状态 | 输出 |
| --- | --- | --- |
| 1. 变更前复核与回滚设计 | complete | 确认 proxy 11 当前仅绑定账号 4/9；使用现有备份表保存最小回滚字段 |
| 2. 清除账号代理与刷新调度 | complete | 账号 4/9 `proxy_id=NULL`；outbox 水位已越过本次事件 |
| 3. 生产 Codex smoke | complete | 公网 SSE 200；账号 4/9 均有成功 usage；坏代理无新连接；本机 Clash 下真实 CLI 仍有客户端 SSE 断流 caveat |

## 本次错误记录

| 时间 | 错误 | 处理 |
| --- | --- | --- |
| 2026-07-21 | 只读查询 `scheduler_outbox.processed_at`，但生产表不存在该列 | 未产生写入；按真实字段查询，使用事件被 worker 删除作为消费证据 |
| 2026-07-21 | 本机公网 curl 首版命令包含临时目录清理，被执行安全规则拒绝 | 请求未执行；改为全内存解析响应，不创建文件 |
| 2026-07-21 | Codex CLI 经本机 Clash HTTP/SOCKS 代理仍显示 stream disconnected | 服务器实际收到重试并全部返回 200；公网 curl SSE 完整，定位为本机代理传输 caveat，不继续改线上 |

---

# 2026-07-26 Claude → GPT-5.6 全局策略与真实本地 TTY

## 目标

- 用真实 `cc2`/Claude Code TTY 在本地 Sub2API 沙盒对照验证 Claude → GPT-5.4 与 Claude → GPT-5.6 Sol。
- 客户端传入的 Claude 模型、`output_config.effort`、streaming、tools 与多轮上下文按既有兼容语义传递；内部 GPT 模型继续对客户端黑盒。
- 仅在 GPT-5.6 Sol 达到 GPT-5.4 稳定性门禁后，增加全局默认目标设置、API Key/分组继承与 UI，并把生产全局默认切到 GPT-5.6 Sol。
- 完成可回滚的生产发布与发布后验收。

## 策略

- GPT-5.6 目标使用生产已支持的精确模型 `gpt-5.6-sol`，不使用无效的裸 `gpt-5.6`。
- effort 映射：`low/medium/high` 原样传递，`max` 映射为 OpenAI `xhigh`；客户端未传时保持当前 GPT-5.4 兼容基线 `medium`。
- 解析优先级：API Key 精确/系列覆盖 > 分组精确/系列覆盖 > 全局默认 > 代码安全兜底 GPT-5.4。
- 本地 OAuth 测试只复制短时 access token；不复制或使用生产 refresh token，并禁用本地自动刷新，避免影响生产账号。
- 如果临时 access token 无法完成测试，停止并请用户在本地完成 Codex OAuth 登录。

## 阶段

| 阶段 | 状态 | 输出 |
| --- | --- | --- |
| 1. 当前源码/生产基线保护 | complete | 分支、dirty patch、未跟踪文件快照、生产 binary 对应关系 |
| 2. TTY Skills 与本地凭据沙盒 | complete | 安全 OAuth 导入流程、本地 PG/Redis/backend、脱敏证据 |
| 3. GPT-5.4 / GPT-5.6 Sol A/B | complete | 16 项直连矩阵、tool/SSE、自然任务、WebSearch、真实 PTY 与 usage 证据均通过 |
| 4. 全局设置与继承实现 | complete | 后端 setting/resolver、API Key/分组/UI、迁移逻辑 |
| 5. 全量回归与本地 TTY 复验 | complete | Go、frontend、embed、真实 PTY |
| 6. Git、canary 与生产发布 | complete | 两个功能提交、远端分支、完整 DB/配置/binary 备份、隔离 canary、正式切换 |
| 7. 发布后观察与清理 | complete | 公网 cc2/TTY、usage/effort/mapping、日志与零重启证据；临时 Key/OAuth/隧道/容器均已清理 |

## 门禁

- 同账号、同请求的 5.4/5.6 测试不得出现可复现的模型不支持、参数不支持、SSE 未完成、工具协议错误或客户端模型泄漏。
- `usage_logs.reasoning_effort` 与请求 effort 一致；`max` 在上游记录为 `xhigh`。
- 非流式、流式、WebSearch、tool_use 和真实 TTY 连续多轮均完成。
- 如果 5.6 失败且换第二个已授权账号仍可复现，不开发或切换全局默认，保留 5.4 并提交失败报告。

## 错误记录

| 时间 | 错误 | 处理 |
| --- | --- | --- |
| 2026-07-26 | 只读 usage 统计误用不存在的 `usage_logs.status` 列 | 查询 `information_schema.columns` 后按真实字段重跑；未产生写入 |
| 2026-07-26 | zsh 对不存在的 `backend/config*` glob 报 `no matches found` | 改用明确目录和 `rg`；未产生写入 |
| 2026-07-26 | 本地 `schema_migrations.version` 列不存在 | 查询实际 schema，确认列为 `filename/checksum/applied_at`；未产生写入 |
| 2026-07-26 | 生产 token TTL 查询对 `double precision` 使用双参数 `round` 失败 | 显式转为 numeric 后重跑；未产生写入 |
| 2026-07-26 | 协议测试最初用 `rg -c` 判断零匹配，空输出被当成脚本失败；SSE token 又被多个 delta 拆分 | 改为容忍零匹配，并按 SSE 事件顺序重建文本；两目标 tool/SSE 共 4/4 通过 |
| 2026-07-26 | 首轮自然 A/B 对带换行的临时 key 文件直接做 sha256，且只删 Redis L2，导致标为 5.6 的样本仍命中 L1 中的 5.4 | 样本作废；改为对去除 CR/LF 的原始 key 计算 sha256，同时删除 L2 并发布 `auth:cache:invalidate`，每轮先用 usage 探针反证上游目标 |
| 2026-07-26 | 本机没有 `redis-cli`，首轮自然测试在发请求前退出 | 改用本地 Redis 容器内的 `redis-cli`；未产生错误模型请求 |
| 2026-07-26 | 首次真实 TTY 的危险权限确认两次选择流程退出 | 只更新了隔离配置的 onboarding 状态，未发模型请求；第三次进入真实 PTY 后完成两轮测试 |
| 2026-07-26 | GPT-5.4 TTY“记住口令”触发并行 memory 工具时出现一次可恢复的 account concurrency limit | Claude Code 自动回退并完成；同会话后续正确，GPT-5.6 TTY 未出现 API/网关错误 |
| 2026-07-26 | 当前 `pnpm` 包装器检测到不同版本创建的 `node_modules`，非 TTY 下请求确认重装并主动中止 | 未重装依赖；直接调用项目已有 `node_modules/.bin` 中的 ESLint、Vue TypeScript、Vitest 和 Vite |
| 2026-07-26 | 前端完整 Vitest 的既有基线有 12 项失败 | 逐个确认失败文件均未被本任务修改；本功能专项 37/37 通过，lint/typecheck/build 通过，将既有失败作为非阻断基线记录 |
| 2026-07-26 | TTY Skill 的 usage 示例仍查询当前 schema 不存在的 `platform/status` 列 | 改为查询 `model/upstream_model/model_mapping_chain/reasoning_effort`，并注明错误终态应结合 ops/server/session 证据 |
| 2026-07-26 | 生产配置迁移首次把全部 active Key 的 84 个旧覆盖当成 OpenAI dispatch 作用域，断言实际为 83 | 事务在写入前回滚；定位到 1 个 Anthropic 分组 Key 后把作用域收紧到 active OpenAI dispatch 分组，未改该 Key |
| 2026-07-26 | 两次 JSONB 清理表达式括号错误 | 两个事务均因 `ON_ERROR_STOP` 在 UPDATE 前完整回滚；先用只读 SELECT/EXPLAIN 验证简化表达式，再执行最终事务 |
| 2026-07-26 | 发布后迁移核验误查 `schema_migrations.version` | 查询真实 schema 后改用 `filename`，确认 `154_openai_messages_dispatch_global_default.sql` 已应用；未产生写入 |

# 2026-07-28 Sub2API 迁移至 161.153.91.242

## 目标与安全边界

- 将当前生产 Sub2API 的 PostgreSQL、Redis、应用二进制、运行配置、数据目录和 Codex/OAuth 账号完整迁移到 `161.153.91.242`。
- 数据库中的账号凭据、API Key、usage 数据和模型映射必须保持一致；敏感配置只通过加密 SSH 通道传输，目标权限不低于源机。
- 必须创建切换时点的最终一致性备份；不把“当前最新数据”与“无备份”混为一谈。
- 旧机在切换后停止应用写入并完整保留，作为短期回滚点；观察期结束前不删除源数据库、Redis、配置、日志或备份。
- DNS 仅在新机恢复和本地/Host Header 验证全部通过后修改。

## 阶段

| 阶段 | 状态 | 门禁与输出 |
| --- | --- | --- |
| 0. 源端与目标端只读预检 | complete | 目标规格、架构、网络、运行服务和 binary 源码基线已确认 |
| 1. 新机基础环境 | complete | PostgreSQL 18、Redis 7、Caddy、Xray、systemd 用户/目录和防火墙已准备并验证 |
| 2. 预同步与恢复演练 | complete | PG/Redis/data/Caddy/Xray 预同步、恢复、对账及 443 端到端验证通过 |
| 3. 最终停写与一致性迁移 | complete | 最终 PG dump、Redis RDB 与 data 增量已传输；目标从头恢复成功并完成一致性对账 |
| 4. 新机验收 | complete | health、binary hash、核心行数、usage、账号凭据摘要、Redis 与公网 Claude smoke 均通过 |
| 5. DNS/TLS 切换 | complete | `cc` 与 `usage` 已切到 `161.153.91.242`，公网 HTTPS、跳转与真实请求通过 |
| 6. 观察与回滚保护 | complete | 新机稳定运行、真实流量增长、零重启/零硬错误；旧机冻结并通过跨任务协调暂停清理 |

## 回滚原则

- 新机未通过全部验收时不改 DNS。
- DNS 切换后若新机异常，立即阻断新机写入、把域名切回旧机并恢复旧机 Sub2API。
- 切换后已经写入新机的数据不能静默丢弃；是否回滚必须先评估增量 usage/账号配置，必要时采用前滚修复。

## 已确认事项

- 用户已授权把 `/Users/taylor/.ssh/ssh-key-oracle.key` 从 `0644` 调整为 `0600`，并确认目标 SSH 用户为 `opc`。
- 用户确认域名为 `claudepool.com`，并授权执行最终停写与迁移。
- 本次默认迁移 `cc.claudepool.com` 与 `usage.claudepool.com`；旧源站已经 502 的 `admin.claudepool.com` 不纳入健康服务切换，除非后续发现其真实后端并通过验收。

## 当前外部阻塞

- 已解除：项目 `.env.secrets` 中的 Cloudflare API token 状态为 active，可读取 `claudepool.com` zone；不会在日志或文档中记录 token。
- 已解除：从旧生产机通过目标 `161.153.91.242:26812` 做带认证 SOCKS5 端到端测试，出口 IP 为目标机且 Google 返回 HTTP 204。
- 已进入最终停写与一致性迁移阶段。
- 当前迁移六个阶段均已完成；旧机下线或清理必须作为观察窗口结束后的独立任务重新确认。

## 迁移错误记录

| 时间 | 错误 | 处理 |
| --- | --- | --- |
| 2026-07-29 | 最终预检首次把带参数的 SSH 命令保存在普通 shell 字符串中，zsh 将整段当作命令名并在连接前退出 | 改为直接调用完整 `ssh` 命令；未连接生产、未停止服务、未产生任何远端修改 |
| 2026-07-29 | DNS 切换脚本在函数中使用 zsh 特殊变量名 `path`，覆盖命令搜索路径并在首次 GET 前报 `curl: command not found` | 改名为 `api_path` 后重跑；脚本未发出 API 请求，Cloudflare DNS 尚未改变 |
| 2026-07-29 | 公网 `/v1/messages` smoke 的首次内联 SSH 命令因本地 zsh 对嵌套 jq 引号和 `?` 通配符展开而在连接前退出 | 改用单引号 heredoc 把脚本传给远端 bash；首次未连接远端、未发模型请求 |
| 2026-07-29 | 文档地址搜索包含不存在的 `.codex/skills/sub2api-*.md` zsh glob，命令在执行前退出 | 改为直接对 `.codex/skills` 目录执行 `rg`；未修改文件 |

## 发布结论

- GPT-5.6 Sol 已达到 GPT-5.4 门禁，生产全局默认已切换为 `gpt-5.6-sol`。
- 生产最终解析顺序为 API Key > 分组 > 全局；Haiku/Fable 行为保持不变。
- 正式 binary sha256 为 `a5ae911f437dd2c21a6323ccba18db30b4330f66adf464f9f248e3ac9401dd1a`，`sub2api.service` active、`NRestarts=0`，内外 health 正常，观察窗口内 Claude 请求错误为 0。
- DB dump、配置快照和旧 binary 均已保留；canary、临时 API Key、raw key 文件、OAuth 副本、本地隧道和测试服务均已清理。

## 2026-07-29 空 502 修复与 GPT-5.6 恢复

- [x] 定位 OpenAI passthrough 空 502 直接透传，以及 buffered `/v1/messages` SSE 失败不换号的问题。
- [x] 本地实现 429/所有 5xx 与未提交响应的 SSE 终止异常换号；账号耗尽时返回带 request id 的结构化错误。
- [x] 完成定向测试、后端全量测试、真实 Codex/Claude 本地黑盒。
- [x] 提交并推送 `d137c99ee fix(openai): fail over retryable upstream errors`。
- [x] 构建并校验 Linux ARM64 embed binary，完成 `127.0.0.1:18080` canary。
- [x] 备份生产数据库、全局 setting 与旧 binary。
- [x] 将全局 Claude Opus/Sonnet 目标恢复为 `gpt-5.6-sol`，正式替换 binary 并重启一次服务。
- [x] 完成公网 Opus/Sonnet、tool/SSE、Claude Code 非交互与真实 TTY 多轮验收。
- [x] 确认 `sub2api.service` active/running、`NRestarts=0`，内外 health 正常且发布后无 HTTP 5xx。
# 管理员全局使用记录 API Key 搜索修复

## 当前目标

修复 `/admin/usage` 的 API Key 筛选交互：管理员可在全局使用记录中按 Key 名称、ID 或完整 Key 精确定位并提交筛选；个人 `/usage` 与普通用户权限保持不变。

## 当前阶段

| 阶段 | 状态 | 输出 |
| --- | --- | --- |
| 1. 生产 UI 与实现只读审计 | complete | 确认现有控件仅名称联想、无搜索按钮且完整 Key 会进入 GET 查询参数 |
| 2. 安全搜索接口与前端交互 | complete | POST 请求体搜索、完整 Key 精确匹配、沿用候选选择交互 |
| 3. 本地回归 | complete | handler/Vitest、Go 全量、前端 lint/typecheck/build、PostgreSQL 只读 SQL 验证 |
| 4. Git 同步与 ARM64 构建 | complete | `b23ab361b`、origin main、ARM64 embed binary |
| 5. 生产发布与验收 | complete | binary 回滚点、systemd 发布、health、路由和管理员页面验证 |

## 已确认边界

- 不修改个人 `/usage` 页面或普通用户的 usage 权限。
- 管理员全局页默认仍展示所有使用记录，并沿用“输入后点击候选”的现有交互。
- 完整 Key 只能做精确匹配，不做模糊或前缀扫描；响应不返回原始 Key。
- 新搜索请求使用 POST JSON body，避免敏感 Key 进入 URL、浏览器历史和访问日志请求行。
- 无数据库迁移；生产仅替换 ARM64 embed binary 并重启一次 systemd 服务。

## 风险与回滚

- 截图中的完整 Key 已暴露给当前会话，旧前端 debounce 还可能将它作为 GET 参数写入访问日志；本次不擅自轮换 Key 或删除日志。
- 当前主工作树含大量既有修改，本次只在 `/Users/taylor/sdk/sub2api-admin-usage-search-fix` 干净 worktree 开发。
- 发布前备份 `/opt/sub2api/sub2api`；异常时恢复备份并重启 `sub2api.service`。

## 错误记录

| 时间 | 错误 | 处理 |
| --- | --- | --- |
| 2026-08-09 | Chrome 只读页面检查使用 `instanceof HTMLInputElement` 时页面隔离环境未暴露该构造器 | 改用 `tagName` 与字符串长度检查；未修改页面或提交敏感信息 |
| 2026-08-09 | 在 `backend/` 工作目录执行 gofmt 时误带 `backend/` 路径前缀 | 改用相对当前目录的 `internal/...` 路径；失败命令未修改文件 |
| 2026-08-09 | 在 `frontend/` 工作目录准备依赖链接与执行工具时误带 `frontend/` 路径前缀 | 改用当前目录下的 `node_modules` 与 `.bin`；失败命令未创建链接或运行测试 |
| 2026-08-09 | PostgreSQL repository integration test 因本机 Docker 不可用而 panic：`rootless Docker not found` | 不重复启动容器；保留单元/编译回归，并在生产 PostgreSQL 只读事务中验证等价查询可解析，不写入或读取原始 Key |
| 2026-08-09 | 生产 `/tmp` 挂载禁止执行，首次 systemd canary 返回 `203/EXEC Permission denied` | canary 已由 trap 停止；改为将候选 binary 安装到 `/opt/sub2api` 唯一临时路径再启动，不重复从 `/tmp` 执行 |
| 2026-08-09 | 第二次 transient canary 未继承正式服务的完整启动上下文，进入 setup wizard 并尝试绑定正式 8080 | setup 未写配置且因端口占用立即退出，trap 已停止 unit；先只读核对正式 unit 的非敏感启动上下文，再决定第三种 canary 方式或直接走可立即回滚的原子替换 |

---

# 2026-08-20 外部额度闸门一小时固定放行策略

## 当前目标

将四个 OpenAI OAuth 账号的外部额度闸门改为：账号关闭期间确认上游出现外部消耗后，固定放行 60 分钟；本站流量不能自动续期；到期关闭并重新建立干净观察基线。

## 已确认口径

- 上游每分钟检查一次。
- 只在账号不可调度且观察区间没有本站请求时，把上游用量增长判为外部消耗。
- 一次外部信号固定放行 60 分钟，放行期间不因本站或上游增量续期。
- 到期后立即不可调度，等待在途流量排空并重新建立基线；后续出现新的外部消耗才再次放行。
- 上游明确额度耗尽、账号失效或窗口变化时立即关闭；临时检查异常保持保守状态。
- 管理 UI 必须显示观察基线、外部增量、确认时间、放行截止时间和最近判断原因。

## 当前阶段

| 阶段 | 状态 | 输出 |
| --- | --- | --- |
| 1. 现有状态机、持久化字段与 UI 回归审计 | complete | 差异清单和兼容设计 |
| 2. 后端固定一小时放行状态机实现 | complete | 状态字段、判断逻辑与单元测试 |
| 3. 管理 UI 与文案更新 | complete | 可理解的观察/放行/到期展示 |
| 4. 本地回归与构建验证 | complete | Go、Vitest、typecheck、lint、build |
| 5. Diff 审查与发布前交付 | complete | 提交候选和生产变更边界 |

## 风险与回滚

- 本次在独立 worktree `/Users/taylor/sdk/sub2api-worktrees/external-quota-gate-v2` 开发，不触碰主工作树中未提交的经营管理改动。
- 不让放行期间的本站流量续期，避免账号因自身请求永久保持可调度。
- 额度窗口切换不能直接当作外部消耗；必须重新建立可归因基线。
- 本阶段只改本地源码并验证，不修改生产账号、数据库或 systemd 服务；生产发布需单独确认。

## 本地交付结论

- 固定60分钟放行、2分钟关闭观察、本站流量不续期和有界状态历史均已实现。
- 后端全量测试、前端生产构建、相关 lint/typecheck/组件测试通过。
- 完整前端 Vitest 保持既有12项失败，没有新增本功能失败。
- 无数据库 migration；生产发布只需要包含新前端资源的 ARM64 embed binary 替换与重启，仍需单独授权。

## 错误记录

| 时间 | 错误 | 处理 |
| --- | --- | --- |
| 2026-08-20 | `planning-with-files` session catchup 不支持 Codex 原生 session | 使用 Git 状态、现有规划文件和独立干净 worktree 恢复上下文 |
| 2026-08-20 | 首次追加规划文件时补丁末尾上下文与真实文件不一致 | 失败补丁未写入任何文件；读取真实末尾后精确追加 |
| 2026-08-20 | 后端状态机首次定向测试仍使用旧版相邻快照夹具，3 个用例进入兼容基线重建分支 | 实现编译正常；下一步给测试夹具补充关闭观察基线，并按60分钟语义更新断言 |
| 2026-08-20 | 更新租约到期测试时在同一作用域重复使用 `state :=`，导致定向测试编译失败 | 改用 `updated :=`；同时修正数据库错误用例为不可调度观察状态 |
| 2026-08-20 | 完整前端 Vitest 返回12项失败 | 与项目现有基线完全一致：676通过、12个既有失败；本功能3项通过，未修改无关测试 |

---

# 2026-08-20 外部额度闸门会话安全排空策略

## 当前目标

将外部额度闸门升级为会话安全状态机：外部消耗确认后默认开放120分钟且支持账号级配置；开放到期后停止接收新会话，但继续服务已有OpenAI粘连会话，直至无粘连、无在途并发和无等待请求后再关闭调度。

## 已确认口径

- 开放时间默认120分钟，并在账号管理UI中可配置。
- 开放截止是“停止接收新会话”的时间，不是强制中断旧会话的时间。
- 排空状态只允许已经粘连到该账号的会话继续请求，新会话必须选择其他账号。
- 关闭前要求活跃粘连数、在途并发数、等待数均为0，并连续两次每分钟检查确认。
- 不设置强制排空上限；上游明确失效、额度耗尽或账号错误属于硬故障，可中止粘连。
- 排空期间的本站消耗不能作为新一轮外部消耗证据；完全关闭后重新建立干净观察基线。
- 本轮只实现和验证，不部署生产。

## 当前阶段

| 阶段 | 状态 | 输出 |
| --- | --- | --- |
| 1. OpenAI粘连、调度和并发链路审计 | complete | 证实现有不可调度会清粘连，且缺少账号反向会话索引 |
| 2. 会话索引与排空调度语义实现 | complete | Redis反向索引、仅粘连可用的排空状态 |
| 3. 额度闸门可配置状态机实现 | complete | 120分钟默认配置、排空确认、关闭观察 |
| 4. 管理UI与状态可观测性 | complete | 配置控件、活跃会话/并发/排空状态展示 |
| 5. 定向与全量回归 | complete | Go全量、Redis integration、专项Vitest、typecheck、lint、build |
| 6. Diff审查与本地交付 | complete | 安全边界、本地提交、部署继续等待授权 |

## 风险与回滚

- 排空过滤必须覆盖OpenAI所有调度入口，否则新会话可能继续进入，账号永远无法排空。
- 反向会话索引必须与正向粘连同样按最后活动时间刷新，并能容忍Redis键自然过期。
- Redis统计异常时保持排空，不得误判为0后关闭，避免活跃会话漂移。
- 本任务继续使用独立worktree，不触碰主工作树；不新增生产写入或服务重启。

## 错误记录

| 时间 | 错误 | 处理 |
| --- | --- | --- |
| 2026-08-20 | `planning-with-files` catchup仍不支持Codex原生session | 使用Git状态、既有提交与规划文件恢复；未修改源码 |
| 2026-08-20 | 首次批量更新排空测试时补丁上下文与实际测试文件不一致 | 失败补丁未写入；改为读取精确测试段并拆分为小补丁 |
| 2026-08-20 | 一次只读 `rg` 命令包含不存在的zsh测试文件glob，命令在执行前中止 | 改用 `rg --glob` 参数，不再让shell展开可选路径 |
| 2026-08-20 | `cmd/server` 首次编译仍使用旧Wire生成参数，缺少GatewayCache与ConcurrencyService | 使用仓库既有Wire命令刷新 `wire_gen.go`，等待复编译 |
| 2026-08-20 | Redis integration测试夹具把接口构造结果直接赋给具体类型字段，带标签构建失败 | 测试同包直接构造 `gatewayCache`，重跑真实Redis integration通过 |
| 2026-08-20 | 前端全量Vitest仍有5个文件共12项既有失败 | 与任务前基线一致；本次专项5项全部通过，未扩大范围修复无关基线 |
| 2026-08-20 | 隔离worktree内不存在项目级Skill脚本路径，首次读取构建脚本失败 | 改读主工作树的项目级Skill脚本；构建时按同一参数在隔离worktree手动执行，避免误构建主工作树 |
| 2026-08-20 | 首次生产账号只读SQL在SSH双层引号中丢失JSON键的单引号 | 该命令只读且未执行查询；改用quoted heredoc把SQL直接传给psql，避免继续嵌套转义 |
| 2026-08-20 | 重启时旧进程因仍有长流式请求，5秒优雅关闭截止后以status=1退出 | 新generation正常启动；切换窗口未见已记录5xx，当前Result=success/NRestarts=0，后续error级日志为空 |

## 生产发布阶段

用户已确认：功能具备UI且回归无新增问题后发布生产。本次只发布应用代码，不修改账号策略配置，也不执行数据库结构或数据写入。

| 阶段 | 状态 | 输出 |
| --- | --- | --- |
| 1. 生产与Git只读预检 | complete | 生产systemd/health/binary SHA、远端主线无漂移 |
| 2. 发布门禁复验 | complete | Go全量、前端lint/typecheck/build、Redis integration均通过 |
| 3. 推送正式主线并构建ARM64 binary | complete | origin/main=`78320aef5`、binary与压缩包SHA |
| 4. 上传、备份、替换与重启 | complete | release路径、回滚binary、systemd active |
| 5. 生产UI/API/调度状态验收 | complete | 内外health、静态资源、接口、四账号状态与日志均已核验 |

### 发布边界与回滚

- 生产目标：`opc@161.153.91.242` 的 `/opt/sub2api/sub2api`，由 `sub2api.service` 管理。
- 预计影响：替换binary并重启服务，可能产生数秒连接抖动；PostgreSQL、Redis、Caddy和DNS不变。
- 当前生产binary SHA256：`facf843d35ee76b7b1448df5d535407d5c14748fa10c4d8f8b824df0f7115aa0`。
- 替换前创建 `/opt/sub2api/sub2api.bak.<timestamp>-before-external-quota-drain`；异常时原位恢复并重启。
- 发布后不自动启用或修改四个账号的外部额度闸门，账号级配置由管理UI另行操作。

---

# 2026-08-24 API Key 日/周限额快捷重置

## 目标与口径

- 管理员可以对单个 API Key 独立重置日限额用量；只清零当天用量，保留北京时间每日 00:00 的自然周期边界。
- 管理员可以快捷重置周限额；清零周用量，并以点击确认时的服务器时间作为新起点，下一次结束时间固定为起点后 7 天。
- 两种重置都通过独立管理接口执行，不提交编辑弹窗内其他未保存字段。
- 操作后失效认证缓存和限额缓存，并返回更新后的 API Key 状态。
- 完成本地后端、前端和浏览器回归后发布 ARM64 生产 binary；不新增数据库迁移，不修改 DNS。

## 阶段计划

| 阶段 | 状态 | 输出 |
| --- | --- | --- |
| 1. 生产手工日限额重置 | complete | API Key ID 525 的 `usage_1d` 已清零并回读，缓存已失效 |
| 2. 隔离工作树与基线审计 | complete | 基于 `origin/main=7228c24f2` 创建独立分支与工作树 |
| 3. 后端窗口级重置 API | complete | 1d/7d 原子更新、鉴权路由、缓存失效和更新后响应 |
| 4. 管理 UI 快捷按钮 | complete | 独立按钮、确认提示、执行状态、结果刷新和中英文文案 |
| 5. 定向/全量/浏览器回归 | complete | Go 全量、PostgreSQL integration、专项 Vitest、lint/typecheck/build、本地真实 API E2E |
| 6. Git、ARM64 canary 与生产发布 | in_progress | fetch/rebase、提交推送、binary、回滚、内外 smoke |

## 风险与回滚

- 周重置会改变下一次周限额结束时间；UI 必须在确认文案中明确“从现在起 7 天”。
- 日重置不得顺带修改 5 小时或周窗口；周重置不得修改日窗口。
- 重置 API 发生并发计费写入时必须由服务端单次更新并清缓存，响应以数据库更新结果为准。
- 发布只替换应用 binary；替换前保留 `/opt/sub2api/sub2api.bak.<timestamp>-before-api-key-rate-limit-resets`，异常时原位恢复并重启。

## 错误记录

| 时间 | 错误 | 处理 |
| --- | --- | --- |
| 2026-08-24 | `planning-with-files` catchup 不支持 Codex 原生 session | 使用 Git 状态、远端主线和现有规划文件恢复；未覆盖主工作树改动 |
| 2026-08-24 | 首次格式化命令在 `backend/` 中仍带 `backend/` 前缀，目标路径不存在 | 源码未受影响；改用相对 `internal/...` 路径重新格式化 |
| 2026-08-24 | 原子仓储实现调用了轻量 `sqlExecutor` 未暴露的 `QueryRowContext` | 改用项目既有 `QueryContext` + 单行读取 + `Rows.Close` 模式 |
| 2026-08-24 | handler 测试未给非目标窗口设置有效起点，DTO 将其视为过期并显示 0 | 给夹具补充真实有效窗口，继续验证非目标窗口不变 |
| 2026-08-24 | `pnpm v11` 自动迁移旧锁文件并生成 workspace 文件 | 精确恢复锁文件、删除生成文件，后续直接使用项目现有 `node_modules/.bin`，未提交依赖噪音 |
| 2026-08-24 | Testcontainers 未跟随 OrbStack Docker context，报 `rootless Docker not found` | 显式传入现有 OrbStack socket，真实 PostgreSQL integration 通过 |
| 2026-08-24 | 前端全量 Vitest 有 12 个既有失败 | 684 项通过、12 项失败，失败集中在未改动模块；本功能专项 3 项全部通过 |
| 2026-08-24 | 内置浏览器和 Chrome 连接层均拦截本机 HTTP | 补充真实挂载 `ApiKeysView` 组件交互测试，并用独立本地应用/数据库完成真实 HTTP E2E；生产发布后再做 HTTPS 可见验收 |
| 2026-08-24 | 临时应用首次启动的测试 TOTP key 不是合法 hex | 初始化完成但应用未监听；改用合法测试 key 重启，未影响生产或既有本地服务 |
| 2026-08-24 | 本地 E2E 首次 `psql -c` 未展开命名变量，第二次 jq 无法解析带时区偏移的 RFC3339 | 使用已验证整数 ID，并由 PostgreSQL 回读周起点与服务器当前时间差，最终 E2E 通过 |
| 2026-08-24 | 临时目录永久删除命令被安全策略拒绝 | 停止临时容器并将精确目录移动到废纸篓，可恢复；源码与其他容器未受影响 |
