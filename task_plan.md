# Sub2API Admin API Key 策略管理增强计划

## 当前目标

增强 admin 侧能力，让管理员可以在“用户管理 -> 用户 API 密钥”中直接控制每个 API Key 的运营策略，包括过期时间、总额度、日/周/5h 限额、状态、重置用量与分组；普通用户侧暂不修改。

## 当前范围

- 后端 admin API 支持更新 API Key 策略字段。
- 前端 admin 用户 API Key 弹窗支持编辑策略字段。
- 保持用户侧 `/keys` 页面不变。
- 添加必要测试与本地验证。

## 当前阶段

| 阶段 | 状态 | 输出 |
| --- | --- | --- |
| 1. 现状确认 | complete | admin/user API Key 能力边界 |
| 2. 后端 admin API 增强 | complete | service/handler/API contract |
| 3. 前端 admin UI 增强 | complete | `UserApiKeysModal.vue` 与 admin API client |
| 4. 验证 | complete | Go/前端定向测试与类型检查 |

## 当前决策

- 2026-05-28：保持“一 API Key 一用户”模型，用用户隔离并发/RPM，用 API Key 字段管理过期时间和额度。
- 2026-05-28：本轮只改 admin 管理面；用户侧目前未开放登录，暂不收紧 `/keys` 页面。

## 当前错误记录

| 时间 | 错误 | 处理 |
| --- | --- | --- |
| 2026-05-28 | 本机没有直接可用 `pnpm`；`corepack pnpm` 触发 pnpm 11 依赖状态检查并生成 lockfile 噪音 | 改用已安装的 `frontend/node_modules/.bin/vue-tsc --noEmit`，并清理本次生成的 lockfile/workspace 文件 |

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

## 决策记录

- 2026-05-27：用户明确选择只做方案一。
- 2026-05-27：Sub2API 将作为后续主要维护项目，旧 CLIProxyAPI 项目只作为参考来源。
- 2026-05-27：线上 Sub2API 使用 `weishaw/sub2api:latest` 镜像，宿主机不是源码 Git 工作区；本次先完成本地实现、测试和文档，线上发布需先确定镜像 tag/registry/回滚流程。
- 2026-05-27：因 Postgres/Redis 仍在 Docker 网络内，短期生产发布采用应用容器替换/重启；宿主机 systemd 直跑作为后续独立迁移任务。
- 2026-05-27：用户要求先完成旧项目 Claude -> GPT 稳定性经验的迁移矩阵与测试缺口清单；第二步允许补测试代码，但不改业务逻辑。
- 2026-05-27：用户要求后续黑盒优先使用本地启动 Sub2API 并在本地授权 Codex auth file；远端 canary 只作为生产同配置验证手段。
- 2026-05-27：本次发布不打 Git tag；生产 Docker 镜像使用 `zhangtaylor985/sub2api:main-decdc6d0`。
- 2026-05-27：Claude Code/VSCode 的 `name:"WebSearch"` 客户端工具应在 Claude -> GPT 入口映射为 OpenAI 原生 `web_search`；否则会退回 Claude Code 原生 Web Search，表现为慢且常见 0 results。

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
