---
name: sub2api-deploy
description: Use when deploying Sub2API to production on 161.153.91.242, managing the ARM64 systemd binary, PostgreSQL/Redis production runtime, Cloudflare DNS, or documenting rollback.
---

# Sub2API Deploy

## Current Production

- Host: `ssh -i /Users/taylor/.ssh/ssh-key-oracle.key opc@161.153.91.242`
- Hostname: `default`
- OS / architecture: Oracle Linux 9.8 / ARM64
- Directory: `/opt/sub2api`
- Public endpoint: `cc.claudepool.com`
- New host Caddy: `cc.claudepool.com -> 127.0.0.1:8080`
- Current app runtime: systemd service `sub2api.service`
- Database: host PostgreSQL 18 database `sub2api`
- Redis: host service `redis`
- Cloudflare DNS: `cc.claudepool.com` is a DNS-only A and `usage.claudepool.com` is a proxied A; both point to `161.153.91.242`.
- Direct rollback host: `ssh -p 41012 root@172.247.109.38`, hostname `C20260613138680`, app stopped, PostgreSQL/Redis/Caddy/Xray/data retained.
- Legacy rollback host: `ssh root@204.168.245.138`, directory `/root/cliapp/sub2api`, Docker Compose app stopped with Postgres/Redis retained.

## Deployment Principle

当前新生产已经完成宿主机化迁移：Sub2API app、PostgreSQL 18、Redis 都在新机 systemd/宿主机服务中运行。

因此：

- 新生产发布优先替换 `/opt/sub2api/sub2api` binary 并 `systemctl restart sub2api`。
- 修改数据库前必须先做 `pg_dump -Fc --no-owner --no-acl` 备份。
- `172.247.109.38` 的 systemd 环境是当前直接回滚点；更早的 `204.168.245.138` Docker Compose 仅作灾备参考。不要让任一旧环境恢复写入，除非明确执行迁移级回滚。

## Safe Systemd App Release Shape

推荐流程：

1. 本地提交并推送到 `git@github.com:zhangtaylor985-ai/sub2api.git`。
2. 本地或构建机使用项目要求的 Go 版本构建 Linux arm64 release binary，带 `-tags embed`。
3. 传到新机临时路径，保留当前 `/opt/sub2api/sub2api` 备份。
4. 替换 binary 后 `systemctl restart sub2api`。
5. 验证新机 `127.0.0.1:8080/health`、公开 `https://cc.claudepool.com/health` 和必要 API smoke。
6. 如触达 Claude/OpenAI 协议，按 `sub2api-production-regression` 做 cc1 黑盒。

## Rollback

新机 systemd binary 回滚：

```bash
cp /opt/sub2api/sub2api.bak.<timestamp> /opt/sub2api/sub2api
systemctl restart sub2api
```

迁移级回滚：

- 先停止新机应用，核对切换后的 usage/账号增量，避免静默丢失数据。
- 把 Cloudflare `cc.claudepool.com` 改回 DNS-only A `172.247.109.38`，把 `usage.claudepool.com` 恢复为原 proxied Tunnel CNAME。
- 在直接回滚机执行 `systemctl reset-failed sub2api && systemctl start sub2api`，再验证 health。

## Smoke Checks

```bash
curl -fsS http://127.0.0.1:8080/health
systemctl status sub2api --no-pager
journalctl -u sub2api -n 100 --no-pager
```

Canary 可使用新机 systemd 临时端口或手动运行独立 binary，例如 `18080`：

```bash
curl -fsS http://127.0.0.1:18080/health
```
