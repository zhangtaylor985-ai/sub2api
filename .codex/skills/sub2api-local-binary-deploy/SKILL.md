---
name: sub2api-local-binary-deploy
description: Use when deploying Sub2API temporarily without GitHub/CI by building a local Linux amd64 binary, transferring it to the production systemd host, optionally using zstd patch-from to avoid slow uploads, backing up /opt/sub2api/sub2api, restarting sub2api.service, and verifying health/smoke/usage. Trigger on requests like 本地 build 二进制上传, 临时二进制上线, 不走 GitHub 部署, zstd patch 发布, or fast production hotfix deploy for Sub2API.
---

# Sub2API 临时本地二进制发布

## 定位

这是 GitHub/CI 发布链路恢复前的临时上线方案：在本地已验证源码上构建 Linux amd64 二进制，上传到新生产机 `/opt/sub2api/releases/`，备份并替换 `/opt/sub2api/sub2api`，最后重启 `sub2api.service`。

优先使用正式 GitHub commit/tag 发布；只有用户明确同意临时不走 GitHub，或生产需要快速热修且当前线上不是 Git checkout 时，使用本 Skill。

生产发布时同时遵循项目级 `sub2api-deploy` Skill；如果改动涉及 Claude/OpenAI 协议、计费、streaming、web search、auth cache 或黑盒兼容，同时遵循 `sub2api-production-regression`。

## 安全边界

- 发布前先说明动作、影响范围和回滚方式；替换 binary 和重启 systemd 前必须得到用户明确确认。
- 不要回滚或整理无关 dirty worktree；只构建当前用户确认过的工作区状态。
- 不要把 `.env`、API Key、OAuth token、数据库密码、Redis 密码写入日志、文档或命令输出。
- 不要在生产机远端编译大 Go 工程作为默认方案；新机内存约 2G 且无 swap，曾在编译 `ent`/依赖时被 OOM kill。
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
ssh -o IPQoS=none -p 41012 root@172.247.109.38 '
  set -e
  systemctl is-active sub2api
  sha256sum /opt/sub2api/sub2api
  curl -fsS http://127.0.0.1:8080/health
  curl -fsS https://cc.claudepool.com/health
'
```

记录当前 binary sha256，它是回滚和 zstd patch basis。

## 本地构建

使用脚本生成 Linux amd64 binary 和 `.zst`：

```bash
.codex/skills/sub2api-local-binary-deploy/scripts/build-linux-amd64.sh fable5-billing
```

脚本会在 `backend/bin/` 下输出：

- `sub2api-linux-amd64-<timestamp>-<suffix>`
- `sub2api-linux-amd64-<timestamp>-<suffix>.zst`

记录 binary sha256 和 zst sha256。

## 传输策略

### 首次发布或没有旧 binary basis

上传 `.zst` 到 release 目录：

```bash
rsync -av --partial --append -e 'ssh -o IPQoS=none -p 41012' \
  backend/bin/<binary>.zst \
  root@172.247.109.38:/opt/sub2api/releases/<binary>.zst
```

远端校验并解压：

```bash
ssh -o IPQoS=none -p 41012 root@172.247.109.38 '
  set -euo pipefail
  ZST=/opt/sub2api/releases/<binary>.zst
  OUT=/opt/sub2api/releases/<binary>
  sha256sum "$ZST"
  zstd -d -f "$ZST" -o "$OUT"
  chmod 0755 "$OUT"
  chown sub2api:sub2api "$OUT"
  sha256sum "$OUT"
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
rsync -av --partial --append -e 'ssh -o IPQoS=none -p 41012' \
  backend/bin/<new-binary>-from-<old-sha>.zst \
  root@172.247.109.38:/opt/sub2api/releases/
```

远端用当前线上 binary 还原 release：

```bash
ssh -o IPQoS=none -p 41012 root@172.247.109.38 '
  set -euo pipefail
  PATCH=/opt/sub2api/releases/<patch>.zst
  BASIS=/opt/sub2api/sub2api
  OUT=/opt/sub2api/releases/<new-binary>
  sha256sum "$BASIS" "$PATCH"
  zstd -d --patch-from="$BASIS" "$PATCH" -o "$OUT"
  chmod 0755 "$OUT"
  chown --reference="$BASIS" "$OUT"
  sha256sum "$OUT"
'
```

只有远端 release sha256 与本地新 binary sha256 完全一致，才能进入替换步骤。

## 替换与回滚

替换前再次说明将发生一次短暂 `sub2api.service` 重启。用户确认后执行：

```bash
ssh -o IPQoS=none -p 41012 root@172.247.109.38 '
  set -euo pipefail
  NEW=/opt/sub2api/releases/<new-binary>
  CUR=/opt/sub2api/sub2api
  BAK=/opt/sub2api/sub2api.bak.<timestamp>-before-<reason>
  systemctl is-active sub2api
  sha256sum "$CUR" "$NEW"
  cp -a "$CUR" "$BAK"
  install -m 0755 -o sub2api -g sub2api "$NEW" "$CUR"
  systemctl restart sub2api
  sleep 2
  systemctl is-active sub2api
  curl -fsS http://127.0.0.1:8080/health
  curl -fsS https://cc.claudepool.com/health
  sha256sum "$CUR" "$BAK"
'
```

回滚使用对应备份：

```bash
ssh -o IPQoS=none -p 41012 root@172.247.109.38 '
  set -euo pipefail
  BAK=/opt/sub2api/sub2api.bak.<timestamp>-before-<reason>
  install -m 0755 -o sub2api -g sub2api "$BAK" /opt/sub2api/sub2api
  systemctl restart sub2api
  systemctl is-active sub2api
  curl -fsS http://127.0.0.1:8080/health
'
```

## 发布后验证

- 本机和公网 `/health` 必须返回 ok。
- 按改动类型做最小 smoke；Claude/OpenAI dispatch 必须查 usage log 的 `requested_model`、`upstream_model`、`model_mapping_chain`、费用字段。
- 查最近日志，确认没有 panic/fatal/migration failed。
- 确认没有残留本地/远端上传或构建进程：

```bash
ps -o pid,ppid,etime,stat,command | rg 'rsync|ssh -o IPQoS=none -p 41012|go build|zstd.*patch-from' || true
ssh -o IPQoS=none -p 41012 root@172.247.109.38 \
  'ps -eo pid,ppid,etime,stat,cmd | grep -E "rsync|go build|sub2api-build" | grep -v grep || true'
```

## 记录

更新 `task_plan.md`、`findings.md`、`progress.md`：

- 本地测试命令和结果。
- 本地 binary/zst/patch sha256。
- 远端 release 路径、当前线上 sha256、backup 路径。
- health、smoke、usage 证据。
- 遇到的错误和处理方式。
