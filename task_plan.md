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
