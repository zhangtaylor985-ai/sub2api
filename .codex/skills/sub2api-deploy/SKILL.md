---
name: sub2api-deploy
description: Use when deploying Sub2API to production on 204.168.245.138, deciding between Docker app-container replacement and host systemd binary, restarting sub2api, or documenting rollback.
---

# Sub2API Deploy

## Current Production

- Host: `root@204.168.245.138`
- Directory: `/root/cliapp/sub2api`
- Public endpoint: `cc.claudepool.com`
- Caddy: `cc.claudepool.com -> 127.0.0.1:8080`
- Current app runtime: Docker container `sub2api`
- Database: Docker container `sub2api-postgres`
- Redis: Docker container `sub2api-redis`
- Docker network: `sub2api_sub2api-network`

## Deployment Principle

当前 Postgres/Redis 在 Docker 内网，配置里使用 `postgres` / `redis` 容器名。

因此：

- 短期生产发布优先替换 app container，保留 Postgres/Redis。
- 不要直接把宿主机 systemd binary 切到生产，除非先把 DB/Redis 连接方式改成稳定宿主机可访问地址，或完成 DB/Redis 迁移。
- systemd 直跑可以作为后续单独迁移任务，不能和协议修复发布混在一起冒险。

## Safe Docker App Release Shape

推荐流程：

1. 本地提交并推送到 `git@github.com:zhangtaylor985-ai/sub2api.git`。
2. 打 tag，例如 `v0.1.131-claude-websearch.1`。
3. 线上 clone/pull 我们的仓库到独立源码目录，例如 `/root/cliapp/sub2api-src`.
4. 线上构建带 tag 的镜像，例如 `zhangtaylor985/sub2api:<tag>`。
5. 先运行 canary app container，连接 `sub2api_sub2api-network`，使用独立宿主机端口。
6. canary smoke + cc1 黑盒通过后，再替换生产 `sub2api` app container。
7. Postgres/Redis 容器不动。

## Rollback

当前已知旧生产镜像：

```bash
weishaw/sub2api:latest
```

回滚应用容器时，保留 data/Postgres/Redis，只将 `sub2api` app container 换回旧镜像并复用原 `.env` / `data` mount / network / port。

## Smoke Checks

```bash
curl -fsS http://127.0.0.1:8080/health
docker ps --filter name=sub2api
docker logs sub2api --tail 100
```

Canary 使用独立端口，例如 `18080`：

```bash
curl -fsS http://127.0.0.1:18080/health
```
