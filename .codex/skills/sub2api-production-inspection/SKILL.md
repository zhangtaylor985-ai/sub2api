---
name: sub2api-production-inspection
description: Use when inspecting Sub2API local/production addresses, containers, runtime status, production database state, API key to user/group relationships, group permissions, model mappings, or when answering how to view Sub2API production safely without exposing secrets.
---

# Sub2API Production Inspection

用于 `/Users/taylor/sdk/sub2api` 的地址库、线上只读排查、API Key / 用户 / 分组关系说明。默认中文回复；命令和日志保持英文。

## Scope

- 本 Skill 只记录线上入口、容器/数据库位置、安全规则和可复用只读排查命令。
- 不记录业务需求、页面展示口径、一次性任务计划、发布回归流程或具体修复结论。
- 业务/任务结论放在项目 `AGENTS.md`、`task_plan.md`、`findings.md`、`progress.md` 或 `docs/` 中；发布流程使用 `sub2api-deploy`，上线回归使用 `sub2api-production-regression`。

## Address Book

- Local source: `/Users/taylor/sdk/sub2api`
- Backend: `/Users/taylor/sdk/sub2api/backend`
- Frontend: `/Users/taylor/sdk/sub2api/frontend`
- Maintained remote: `origin git@github.com:zhangtaylor985-ai/sub2api.git`
- Upstream reference: `upstream https://github.com/Wei-Shaw/sub2api.git`
- Production SSH: `ssh root@204.168.245.138`
- Production host name: `PG-01`
- Production app directory: `/root/cliapp/sub2api`
- Production compose file: `/root/cliapp/sub2api/docker-compose.yml`
- Public endpoint: `https://cc.claudepool.com`
- Local app endpoint on production host: `http://127.0.0.1:8080`
- Caddy route: `cc.claudepool.com -> 127.0.0.1:8080`
- Main containers: `sub2api`, `sub2api-postgres`, `sub2api-redis`
- Docker network: `sub2api_sub2api-network`
- Data mounts: `/root/cliapp/sub2api/data`, `/root/cliapp/sub2api/postgres_data`, `/root/cliapp/sub2api/redis_data`

## Safety Rules

- 默认只读排查；不要直接改线上源码、容器文件、`.env`、数据库敏感字段。
- 不要输出或写入 API Key、OAuth token、数据库密码、Redis 密码、cookie、refresh token。
- 查 API key 时只输出 `id/user_id/name/group_id/status/last_used_at` 等非敏感字段；不要 SELECT `api_keys.key`，除非为了本机内部缓存失效脚本且不回显。
- 查 `accounts.credentials` 时只看模型映射等非密钥字段，优先用 JSON 运算输出布尔或非敏感摘要。
- 需要修改生产配置或数据库时，先说明影响面、回滚方式和是否需要清缓存；用户确认后再执行。
- 线上应用代码来自 Docker image；发布或回滚使用 `sub2api-deploy` skill，不要用远程编辑器热改源码。

## Basic Production Checks

```bash
ssh root@204.168.245.138 'hostname'

ssh root@204.168.245.138 \
  "docker ps --filter name=sub2api --format 'table {{.Names}}\t{{.Image}}\t{{.Status}}\t{{.Ports}}'"

ssh root@204.168.245.138 \
  "curl -fsS http://127.0.0.1:8080/health"

ssh root@204.168.245.138 \
  "docker logs sub2api --tail 200"
```

## Keep This Skill Current

- 线上地址、容器名、反代入口、部署目录或关键排查 SQL 变化时，优先更新本文件。
- 本文件是后续 Codex 进入 Sub2API 线上环境的地址库；不要把密钥、token、数据库密码或用户凭据写进来。
- 若某次排查形成可复用流程，把只读命令、判断口径和风险边界追加到对应小节。

## Production Postgres

Use container-local `psql`; do not read `.env` just to get credentials.

```bash
ssh root@204.168.245.138 \
  "docker exec -i sub2api-postgres sh -lc 'psql -U \"\${POSTGRES_USER:-sub2api}\" -d \"\${POSTGRES_DB:-sub2api}\"'"
```

For one-off read-only SQL, prefer a quoted heredoc:

```bash
ssh root@204.168.245.138 \
  "docker exec -i sub2api-postgres sh -lc 'psql -U \"\${POSTGRES_USER:-sub2api}\" -d \"\${POSTGRES_DB:-sub2api}\" -F \"|\" -At'" <<'SQL'
SELECT id, name, platform, status
FROM groups
ORDER BY id;
SQL
```

Useful schema checks:

```sql
SELECT column_name
FROM information_schema.columns
WHERE table_schema='public' AND table_name='api_keys'
ORDER BY ordinal_position;
```

## User / API Key / Group Model

Runtime routing is primarily API-key scoped:

- `users`: user identity, balance, concurrency, status, RPM limit.
- `api_keys`: each key belongs to one user and may bind one `group_id`; request auth loads this key, user, and group into the auth snapshot.
- `groups`: platform, rate multiplier, image permissions, message-dispatch config, routing config, limits.
- `account_groups`: which upstream accounts a group can schedule.
- `user_allowed_groups`: only gates exclusive standard groups; public non-exclusive groups do not require a row here.
- `user_subscriptions`: gates subscription groups; a key can bind a subscription group only when the user has an active subscription for that group.

For public standard groups, an empty `user_allowed_groups` row set can be normal. To know which group a request uses, inspect `api_keys.group_id`.

## Find A User And Its Key Group

Do not output the key string.

```sql
SELECT u.id, u.email, u.username, u.role, u.status, u.balance, u.concurrency, u.rpm_limit
FROM users u
WHERE u.email = '<email>' OR u.username = '<email>';

SELECT ak.id, ak.user_id, ak.name, ak.group_id,
       g.name AS group_name, g.platform, g.status AS group_status,
       g.allow_image_generation, ak.status, ak.last_used_at
FROM api_keys ak
LEFT JOIN groups g ON g.id = ak.group_id
JOIN users u ON u.id = ak.user_id
WHERE u.email = '<email>' OR ak.id = <api_key_id>
ORDER BY ak.id;

SELECT us.id, us.user_id, us.group_id, g.name, g.platform, us.status, us.starts_at, us.expires_at
FROM user_subscriptions us
JOIN groups g ON g.id = us.group_id
JOIN users u ON u.id = us.user_id
WHERE u.email = '<email>'
ORDER BY us.id;

SELECT uag.user_id, uag.group_id, g.name, g.platform, g.status
FROM user_allowed_groups uag
JOIN groups g ON g.id = uag.group_id
JOIN users u ON u.id = uag.user_id
WHERE u.email = '<email>'
ORDER BY g.id;
```

## Inspect OpenAI Groups And Image Support

```sql
SELECT id, name, platform, status, is_exclusive, subscription_type,
       allow_image_generation, allow_messages_dispatch
FROM groups
WHERE platform='openai'
ORDER BY id;

SELECT ag.group_id, a.id, a.type, a.status, a.schedulable,
       (a.credentials::jsonb->'model_mapping' ? 'gpt-image-1') AS gpt_image_1,
       (a.credentials::jsonb->'model_mapping' ? 'gpt-image-1.5') AS gpt_image_15,
       (a.credentials::jsonb->'model_mapping' ? 'gpt-image-2') AS gpt_image_2
FROM account_groups ag
JOIN accounts a ON a.id=ag.account_id
WHERE ag.group_id=<group_id> AND a.deleted_at IS NULL
ORDER BY a.id;
```

## Change An API Key Group

Preferred path: use the admin API / UI because it validates the target group and invalidates auth cache.

- Admin API route: `PUT /api/v1/admin/api-keys/:id`
- Body: `{"group_id": <target_group_id>}`
- `group_id=0` unbinds the key.
- Missing `group_id` means no group change.

If SQL is unavoidable, do it only after user approval, then invalidate that key's auth cache. Avoid printing the key; fetch it into a shell variable and hash it locally.

## Auth Cache After Group Or Permission Changes

Group fields such as `allow_image_generation` are embedded in API key auth snapshots. After changing group permissions or key group binding, make the change through the app/admin service when possible. If a direct DB change was explicitly approved, delete Redis `apikey:auth:<sha256(api_key)>` and publish `auth:cache:invalidate <sha256(api_key)>` for affected keys, without printing raw keys.
