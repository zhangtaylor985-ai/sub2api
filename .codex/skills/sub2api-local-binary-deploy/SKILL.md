---
name: sub2api-local-binary-deploy
description: Use when deploying Sub2API temporarily without GitHub/CI by building a local Linux arm64 binary, transferring it to the production systemd host, optionally using zstd patch-from to avoid slow uploads, backing up /opt/sub2api/sub2api, restarting sub2api.service, and verifying health/smoke/usage. Also use for quickly enabling or disabling Claude Fable 5 support in Sub2API production. Trigger on requests like 本地 build 二进制上传, 临时二进制上线, 不走 GitHub 部署, zstd patch 发布, 开启/关闭 Fable/Fables 模型支持, or fast production hotfix deploy for Sub2API.
---

# Sub2API 临时本地二进制发布

## 定位

这是 GitHub/CI 发布链路恢复前的临时上线方案：在本地已验证源码上构建 Linux arm64 二进制，上传到生产机 `/opt/sub2api/releases/`，备份并替换 `/opt/sub2api/sub2api`，最后重启 `sub2api.service`。

优先使用正式 GitHub commit/tag 发布；只有用户明确同意临时不走 GitHub，或生产需要快速热修且当前线上不是 Git checkout 时，使用本 Skill。

生产发布时同时遵循项目级 `sub2api-deploy` Skill；如果改动涉及 Claude/OpenAI 协议、计费、streaming、web search、auth cache 或黑盒兼容，同时遵循 `sub2api-production-regression`。

## 安全边界

- 发布前先说明动作、影响范围和回滚方式；替换 binary 和重启 systemd 前必须得到用户明确确认。
- 不要回滚或整理无关 dirty worktree；只构建当前用户确认过的工作区状态。
- 不要把 `.env`、API Key、OAuth token、数据库密码、Redis 密码写入日志、文档或命令输出。
- 不要在生产机远端编译大 Go 工程作为默认方案；即使当前 ARM64 生产机约 5.5 GiB 内存并有 4 GiB swap，发布构建仍应在本地或独立构建机完成。
- 大文件上传很慢时，优先用 `zstd --patch-from` 基于旧 binary 生成补丁；不要反复全量传 80M+ binary。
- 备份和 release 产物默认保留；删除临时文件或清理 release/backups 前另行确认。

## 前置检查

1. 在仓库根目录检查现场：

```bash
git status --short
git diff --stat
```

2. 跑与改动相关的测试。协议/计费改动至少跑：

```bash
cd backend
NO_PROXY=127.0.0.1,localhost,::1 no_proxy=127.0.0.1,localhost,::1 go test ./internal/pkg/apicompat ./internal/service ./internal/handler -run 'OpenAI|Messages|Gateway|Billing|Pricing|ForwardAsAnthropic' -count=1
NO_PROXY=127.0.0.1,localhost,::1 no_proxy=127.0.0.1,localhost,::1 go test ./...
```

3. 只读检查生产：

```bash
ssh -o IPQoS=none -i /Users/taylor/.ssh/ssh-key-oracle.key opc@161.153.91.242 '
  set -e
  sudo systemctl is-active sub2api
  sudo sha256sum /opt/sub2api/sub2api
  curl -fsS http://127.0.0.1:8080/health
  curl -fsS https://cc.claudepool.com/health
'
```

记录当前 binary sha256，它是回滚和 zstd patch basis。

## Claude Fable 5 开关速查

当前 Fable 方案是“默认支持并保持 Claude 黑盒”。Claude Code 恢复旧 session 失败时可能回退到 `claude-fable-5`，所以 OpenAI `/v1/messages` dispatch 必须默认把 Fable 映射到内部 `gpt-5.4`，但用户侧响应、SSE 和错误仍显示 Claude 模型，不暴露 GPT/Codex/OpenAI 内部模型。

生产 OpenAI dispatch 分组也应保留精确映射，作为配置层兜底：

```json
{
  "exact_model_mappings": {
    "claude-fable-5": "gpt-5.4"
  }
}
```

路由仍走 Claude `/v1/messages` -> OpenAI/Codex dispatch，目标模型默认 `gpt-5.4`。计费优先按 requested model `claude-fable-5` 查动态 pricing；如果动态价格不可用，再按上游模型候选兜底，不维护 Fable 硬编码 fallback price。

UI 开关仅用于显式覆盖或确认配置层兜底：

- 单 API Key：Admin -> API Key 管理 -> 编辑策略 -> `允许 Claude Fable 5`。这只写入该 key 的 `api_keys.messages_dispatch_model_config.exact_model_mappings.claude-fable-5`。
- 分组/全局：Admin -> 分组管理 -> 编辑 OpenAI 分组 -> 开启 `/v1/messages` 调度 -> `分组开启 Claude Fable 5`。这写入该分组的 `groups.messages_dispatch_model_config.exact_model_mappings.claude-fable-5`，该分组下未单独覆盖的 key 都会继承。
- 不建议全局关闭 Fable；如确需关闭，必须同时评估 Claude Code fallback 对 `claude -r` 用户的影响。

只有 UI 暂不可用、需要临时热修时，才直接操作 DB 并清 Redis auth snapshot。

### 关闭 Fable（高风险例外）

代码侧最小关闭点：

- 从 `backend/internal/pkg/claude/constants.go` 的 `DefaultModels` 移除 `claude-fable-5`。
- 从 `backend/internal/service/openai_messages_dispatch.go` 移除 Fable 默认 dispatch family。
- 从 `backend/internal/service/openai_gateway_service.go` 移除 Fable dispatch 计费强制使用 requested model 的 helper/调用。
- 从 `backend/internal/service/billing_service.go` 移除 Fable 专用 fallback price。历史 usage/pricing 统计可保留 `pricing_service.go`、`usage_service.go` 中的 Fable 识别，避免旧账单展示退化。
- 更新测试：模型列表不再包含 Fable；`claude-fable-5` 不再被识别为 messages dispatch family。

生产 DB 侧最小关闭点。备份不要让 `postgres` 用户直接写 `/root`；用 `COPY ... TO STDOUT`，由 root shell 重定向：

```bash
TS=$(date -u +%Y%m%dT%H%M%SZ)
mkdir -p /root/cliapp/sub2api/ops_backups

sudo -u postgres psql -d sub2api -P pager=off -c "\
COPY (\
  SELECT id, name, messages_dispatch_model_config\
  FROM groups\
  WHERE deleted_at IS NULL\
    AND platform = 'openai'\
    AND allow_messages_dispatch IS TRUE\
) TO STDOUT WITH (FORMAT csv, DELIMITER '|', HEADER true)" \
  > "/root/cliapp/sub2api/ops_backups/groups_messages_dispatch_before_disable_fable_${TS}.psv"

sudo -u postgres psql -d sub2api -P pager=off -c "\
COPY (\
  SELECT ak.id, ak.name, ak.group_id, ak.messages_dispatch_model_config\
  FROM api_keys ak\
  JOIN groups g ON g.id = ak.group_id\
  WHERE ak.deleted_at IS NULL\
    AND ak.status = 'active'\
    AND g.deleted_at IS NULL\
    AND g.platform = 'openai'\
    AND g.allow_messages_dispatch IS TRUE\
) TO STDOUT WITH (FORMAT csv, DELIMITER '|', HEADER true)" \
  > "/root/cliapp/sub2api/ops_backups/api_keys_messages_dispatch_before_disable_fable_${TS}.psv"
```

更新 SQL：

```sql
UPDATE groups
SET messages_dispatch_model_config =
  COALESCE(messages_dispatch_model_config::jsonb, '{}'::jsonb) #- '{exact_model_mappings,claude-fable-5}'
WHERE deleted_at IS NULL
  AND platform = 'openai'
  AND allow_messages_dispatch IS TRUE;

UPDATE api_keys ak
SET messages_dispatch_model_config =
  COALESCE(ak.messages_dispatch_model_config::jsonb, '{}'::jsonb) #- '{exact_model_mappings,claude-fable-5}'
FROM groups g
WHERE g.id = ak.group_id
  AND ak.deleted_at IS NULL
  AND ak.status = 'active'
  AND g.deleted_at IS NULL
  AND g.platform = 'openai'
  AND g.allow_messages_dispatch IS TRUE;
```

随后清理受影响 key 的 Redis auth snapshot。不要打印 raw key；在远端 shell 里用 SQL 取 key、hash 后删除 `apikey:auth:<sha256>` 并 publish `auth:cache:invalidate <sha256>`。

关闭后验证：

- `/v1/models` 不含 `claude-fable-5`。
- `claude-fable-5` `/v1/messages` 不应出现 `claude-fable-5→gpt-5.4` usage 映射链。
- 健康检查和 Opus/Sonnet 正常 smoke 仍通过。

### 开启 Fable

代码侧开启点：

- 保持 Fable 默认 dispatch 映射到 `gpt-5.4`，并确保客户端 display model 仍是 `claude-fable-5`。
- 确认 `GatewayHandler.Models` 对 OpenAI dispatch Claude family key 可追加 Fable，但不能暴露任何 GPT/Codex 内部模型。
- 确认 API Key / Group UI 开关读写 `exact_model_mappings.claude-fable-5`。
- 确认 `PricingService` 保留 Fable family 动态价格识别；`BillingService` 不需要 Fable 硬编码 fallback。
- 测试至少覆盖：默认展示/路由 Fable、单 key exact mapping 覆盖、分组 exact mapping 覆盖、动态 Fable pricing 优先于上游 GPT pricing、用户侧响应不含 `gpt-`。

生产 DB 侧开启点：

- 备份 active OpenAI dispatch groups 和 active dispatch API Keys。
- 分组兜底开启时，为目标 OpenAI dispatch group 的 `groups.messages_dispatch_model_config.exact_model_mappings.claude-fable-5` 写入 `gpt-5.4`。不需要批量写入所有 key；没有 key 级 Fable exact mapping 时会继承分组。
- 单 key 只在需要覆盖分组默认时写 `api_keys.messages_dispatch_model_config.exact_model_mappings.claude-fable-5`。
- 清 Redis auth snapshot 并 smoke。

开启后验证：

- `/v1/models` 包含 `claude-fable-5`。
- `claude-fable-5` `/v1/messages` HTTP 200。
- usage 最新行应为 `requested_model=claude-fable-5`、`model=gpt-5.4`（或 `upstream_model=gpt-5.4`，取决于转发结果填充）、`model_mapping_chain=claude-fable-5→gpt-5.4`。
- 对照 token 数验证 input/output/cache 费用按 Fable 官方价计算。

## 本地构建

使用脚本生成 Linux arm64 binary 和 `.zst`：

```bash
.codex/skills/sub2api-local-binary-deploy/scripts/build-linux-arm64.sh fable5-billing
```

脚本会在 `backend/bin/` 下输出：

- `sub2api-linux-arm64-<timestamp>-<suffix>`
- `sub2api-linux-arm64-<timestamp>-<suffix>.zst`

记录 binary sha256 和 zst sha256。

## 传输策略

### 首次发布或没有旧 binary basis

上传 `.zst` 到 release 目录：

```bash
rsync -av --partial --append -e 'ssh -o IPQoS=none -i /Users/taylor/.ssh/ssh-key-oracle.key' \
  backend/bin/<binary>.zst \
  opc@161.153.91.242:/tmp/<binary>.zst
```

远端校验并解压：

```bash
ssh -o IPQoS=none -i /Users/taylor/.ssh/ssh-key-oracle.key opc@161.153.91.242 '
  set -euo pipefail
  sudo install -o sub2api -g sub2api -m 0644 /tmp/<binary>.zst /opt/sub2api/releases/<binary>.zst
  rm -f /tmp/<binary>.zst
  ZST=/opt/sub2api/releases/<binary>.zst
  OUT=/opt/sub2api/releases/<binary>
  sudo sha256sum "$ZST"
  sudo zstd -d -f "$ZST" -o "$OUT"
  sudo chmod 0755 "$OUT"
  sudo chown sub2api:sub2api "$OUT"
  sudo sha256sum "$OUT"
'
```

### 线上当前 binary 与本地旧 binary 一致

如果本地保留了旧 binary，且它的 sha256 等于生产 `/opt/sub2api/sub2api`，优先生成 patch：

```bash
.codex/skills/sub2api-local-binary-deploy/scripts/make-zstd-patch.sh \
  backend/bin/<old-binary> \
  backend/bin/<new-binary>
```

上传 patch：

```bash
rsync -av --partial --append -e 'ssh -o IPQoS=none -i /Users/taylor/.ssh/ssh-key-oracle.key' \
  backend/bin/<new-binary>-from-<old-sha>.zst \
  opc@161.153.91.242:/tmp/
```

远端用当前线上 binary 还原 release：

```bash
ssh -o IPQoS=none -i /Users/taylor/.ssh/ssh-key-oracle.key opc@161.153.91.242 '
  set -euo pipefail
  sudo install -o sub2api -g sub2api -m 0644 /tmp/<patch>.zst /opt/sub2api/releases/<patch>.zst
  rm -f /tmp/<patch>.zst
  PATCH=/opt/sub2api/releases/<patch>.zst
  BASIS=/opt/sub2api/sub2api
  OUT=/opt/sub2api/releases/<new-binary>
  sudo sha256sum "$BASIS" "$PATCH"
  sudo zstd -d --patch-from="$BASIS" "$PATCH" -o "$OUT"
  sudo chmod 0755 "$OUT"
  sudo chown --reference="$BASIS" "$OUT"
  sudo sha256sum "$OUT"
'
```

只有远端 release sha256 与本地新 binary sha256 完全一致，才能进入替换步骤。

## 替换与回滚

替换前再次说明将发生一次短暂 `sub2api.service` 重启。用户确认后执行：

```bash
ssh -o IPQoS=none -i /Users/taylor/.ssh/ssh-key-oracle.key opc@161.153.91.242 '
  set -euo pipefail
  NEW=/opt/sub2api/releases/<new-binary>
  CUR=/opt/sub2api/sub2api
  BAK=/opt/sub2api/sub2api.bak.<timestamp>-before-<reason>
  sudo systemctl is-active sub2api
  sudo sha256sum "$CUR" "$NEW"
  sudo cp -a "$CUR" "$BAK"
  sudo install -m 0755 -o sub2api -g sub2api "$NEW" "$CUR"
  sudo systemctl restart sub2api
  sleep 2
  sudo systemctl is-active sub2api
  curl -fsS http://127.0.0.1:8080/health
  curl -fsS https://cc.claudepool.com/health
  sudo sha256sum "$CUR" "$BAK"
'
```

回滚使用对应备份：

```bash
ssh -o IPQoS=none -i /Users/taylor/.ssh/ssh-key-oracle.key opc@161.153.91.242 '
  set -euo pipefail
  BAK=/opt/sub2api/sub2api.bak.<timestamp>-before-<reason>
  sudo install -m 0755 -o sub2api -g sub2api "$BAK" /opt/sub2api/sub2api
  sudo systemctl restart sub2api
  sudo systemctl is-active sub2api
  curl -fsS http://127.0.0.1:8080/health
'
```

## 发布后验证

- 本机和公网 `/health` 必须返回 ok。
- 按改动类型做最小 smoke；Claude/OpenAI dispatch 必须查 usage log 的 `requested_model`、`upstream_model`、`model_mapping_chain`、费用字段。
- 查最近日志，确认没有 panic/fatal/migration failed。
- 确认没有残留本地/远端上传或构建进程：

```bash
ps -o pid,ppid,etime,stat,command | rg 'rsync|ssh -o IPQoS=none.*161\\.153\\.91\\.242|go build|zstd.*patch-from' || true
ssh -o IPQoS=none -i /Users/taylor/.ssh/ssh-key-oracle.key opc@161.153.91.242 \
  'ps -eo pid,ppid,etime,stat,cmd | grep -E "rsync|go build|sub2api-build" | grep -v grep || true'
```

## 记录

更新 `task_plan.md`、`findings.md`、`progress.md`：

- 本地测试命令和结果。
- 本地 binary/zst/patch sha256。
- 远端 release 路径、当前线上 sha256、backup 路径。
- health、smoke、usage 证据。
- 遇到的错误和处理方式。
