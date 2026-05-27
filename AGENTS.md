# Sub2API 维护说明

## 基本约定

- 默认用中文回复；代码注释和日志使用英文。
- 复杂任务必须使用 `planning-with-files`，在项目根目录维护 `task_plan.md`、`findings.md`、`progress.md`。
- 搜索优先使用 `rg` / `rg --files`。
- 不要把 `.env`、API Key、OAuth token、数据库密码、Redis 密码或用户凭据写入文档、日志、提交信息。
- 修改代码前先确认当前 Git 状态；不要覆盖或回滚非本次任务产生的改动。

## 本地源码

- 本地源码路径：`/Users/taylor/sdk/sub2api`
- 当前维护远端：`origin git@github.com:zhangtaylor985-ai/sub2api.git`
- 上游参考远端：`upstream https://github.com/Wei-Shaw/sub2api.git`
- 后端目录：`backend/`
- 前端目录：`frontend/`

## 线上 Sub2API

- SSH 入口：`ssh root@204.168.245.138`
- 线上主机名：`PG-01`
- 线上部署目录：`/root/cliapp/sub2api`
- 线上运行方式：Docker Compose
- Compose 文件：`/root/cliapp/sub2api/docker-compose.yml`
- 当前容器：
  - `sub2api`，镜像 `zhangtaylor985/sub2api:main-decdc6d0`，健康检查通过，宿主机 `0.0.0.0:8080 -> 8080/tcp`
  - `sub2api-postgres`，镜像 `postgres:18-alpine`
  - `sub2api-redis`，镜像 `redis:8-alpine`
- 线上挂载：
  - `/root/cliapp/sub2api/data -> /app/data`
  - Postgres 数据在 `/root/cliapp/sub2api/postgres_data`
  - Redis 数据在 `/root/cliapp/sub2api/redis_data`
- 反代入口：
  - Caddy 当前 active。
  - `/etc/caddy/Caddyfile` 中 `cc.claudepool.com` 反代到 `127.0.0.1:8080`。
  - `/management.html` 重定向到 `https://admin.claudepool.com/`。
  - Nginx 当前 inactive。
- 健康检查：
  - `curl -fsS http://127.0.0.1:8080/health`
  - `docker ps --format "table {{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}" | grep -E "sub2api|NAMES"`

## 线上只读排查

- 查看容器：
  - `docker ps --filter name=sub2api`
- 查看应用日志：
  - `docker logs sub2api --tail 200`
- 查看数据库 schema 或非敏感配置时，优先通过容器内 `psql`，不要读取 `.env` 明文：
  - `docker exec -i sub2api-postgres sh -lc 'psql -U ${POSTGRES_USER:-sub2api} -d ${POSTGRES_DB:-sub2api}'`
- 可以查询 group/account 的非敏感字段，例如 `groups.name/platform/allow_messages_dispatch/messages_dispatch_model_config`、账号状态与 `credentials.model_mapping`。
- 不要在未确认发布流程前直接进入容器修改文件；当前线上应用代码来自镜像，不是宿主机 Git 工作区。

## 黑盒测试偏好

- 后续 Sub2API Claude/Codex auth file 黑盒测试，优先本地启动 Sub2API，并在本地授权测试用 Codex auth file。
- 只有需要生产同配置验证时，再使用生产机临时 canary；canary 必须只绑定远端 `127.0.0.1` 非正式端口，验证结束后清理容器、镜像、临时源码和本机 SSH 隧道。

## 项目级 Skills

- 线上地址库、容器状态、生产库只读查询、用户/API Key/分组关系、生图权限与模型映射排查：优先使用 `.codex/skills/sub2api-production-inspection/SKILL.md`。
- 生产部署、Docker app 容器替换、回滚与 systemd/Docker 取舍：使用 `.codex/skills/sub2api-deploy/SKILL.md`。
- 上线前回归、Claude/OpenAI/Codex 协议兼容、Web search、streaming、cc1 黑盒：使用 `.codex/skills/sub2api-production-regression/SKILL.md`。

## Claude -> GPT 模型映射

- OpenAI 分组开启 `allow_messages_dispatch=true` 后，Claude `/v1/messages` 可调度到 OpenAI/Codex 账号。
- 第一层映射在分组：
  - 表字段：`groups.messages_dispatch_model_config`
  - 代码入口：`Group.ResolveMessagesDispatchModel`
  - 优先级：`exact_model_mappings` > Claude family 映射 > 代码默认值
  - 默认值：Opus -> `gpt-5.4`，Sonnet -> `gpt-5.3-codex`，Haiku -> `gpt-5.4-mini`
- 第二层映射在账号：
  - 表字段：`accounts.credentials.model_mapping`
  - 代码入口：`Account.GetMappedModel` / `Account.ResolveMappedModel`
  - 支持精确映射和 `*` 通配符，最长匹配优先
  - 账号级 mapping 既是支持模型白名单，也是最终上游模型改写规则
- `/v1/messages` 链路中，分组映射作为 `defaultMappedModel` 传入 OpenAI 转发服务；账号级映射优先命中，未命中且请求是 Claude family 时才使用分组映射。

## 本次任务记录

- 2026-05-27：已将线上 OpenAI 分组 `allow_messages_dispatch` 调整为 `true`。
- 2026-05-27：确认后续只做 Web search 方案一：参考 CLIProxyAPI 的 Claude -> GPT 兼容层，改善 Claude CLI / VSCode 对 OpenAI `web_search_call` 的展示；不默认改用 Brave/Tavily 模拟。
- 2026-05-27：已上线 `zhangtaylor985/sub2api:main-decdc6d0`。线上 Compose 备份：`/root/cliapp/sub2api/docker-compose.yml.bak.20260527T105427Z`；上一版应用镜像为 `zhangtaylor985/sub2api:v0.1.131-claude-websearch.2`，更早回滚可切回 `weishaw/sub2api:latest`，然后 `docker compose up -d sub2api`。
- 2026-05-27：Postgres/Redis 仍保持 Docker Compose 管理；宿主机化迁移应单独做备份、恢复演练、停写窗口和回滚方案，不与应用协议修复混发。
- 任务细节见：
  - `task_plan.md`
  - `findings.md`
  - `progress.md`
