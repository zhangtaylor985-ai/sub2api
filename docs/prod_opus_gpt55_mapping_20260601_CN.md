# 生产 Opus -> GPT-5.5 映射收敛记录（2026-06-01）

## 目标

让生产 Sub2API 的 OpenAI `/v1/messages` dispatch 分组在处理 Claude Opus 系列请求时稳定路由到 `gpt-5.5`，尤其覆盖 `claude-opus-4-6`、`claude-opus-4-7`、`claude-opus-4-8`。

## 变更范围

- 只改生产数据库中的 OpenAI 分组配置：`groups.messages_dispatch_model_config`。
- 未改业务代码，未改账号 credentials 密钥，未改 API Key raw key。
- 未给原本没有 `credentials.model_mapping` 的账号新增账号级 mapping，避免把无限制账号意外变成模型白名单账号。
- 仅重启应用容器 `sub2api` 以刷新进程内状态；Postgres/Redis 容器未重启。

## 变更前发现

- 线上实际运行镜像：`zhangtaylor985/sub2api:main-378405f6`，容器 healthy，公开 `/health` 正常。
- 6 个未删除 OpenAI 分组均已开启 `allow_messages_dispatch=true`。
- 这些分组的 `opus_mapped_model` 仍为 `gpt-5.4`。
- `claude-opus-4-8` 已有精确映射到 `gpt-5.5`，但 `claude-opus-4-6` 和 `claude-opus-4-7` 精确映射缺失。
- 8 个 active+schedulable OpenAI 账号没有 `claude-opus-4-6/4-7/4-8` 的冲突账号级映射。

## 执行内容

对所有 `platform='openai'`、未删除、`allow_messages_dispatch=true` 的分组幂等更新：

- `opus_mapped_model = gpt-5.5`
- `exact_model_mappings.claude-opus-4-6 = gpt-5.5`
- `exact_model_mappings.claude-opus-4-7 = gpt-5.5`
- `exact_model_mappings.claude-opus-4-8 = gpt-5.5`

执行结果：更新 6 个分组。

随后清理 Redis `apikey:auth:*` 快照并重启 `sub2api` 应用容器。第一次 Redis 清理命令因 redis-cli 继承空 `REDISCLI_AUTH` 出现 AUTH 提示，未完成删除；已用 `env -u REDISCLI_AUTH` 复核并删除 15 个 auth snapshot，最终剩余 0。

## 验证结果

- Docker health：`sub2api` healthy。
- 公开健康检查：`https://cc.claudepool.com/health` 返回 ok。
- 配置聚合：6/6 个 OpenAI dispatch 分组的 Opus family、4-6、4-7、4-8 都是 `gpt-5.5`。
- 生产直接 `/v1/messages` smoke：
  - `claude-opus-4-6` 返回 HTTP 200，usage log 显示 `claude-opus-4-6→gpt-5.5`。
  - `claude-opus-4-7` 返回 HTTP 200，usage log 显示 `claude-opus-4-7→gpt-5.5`。
  - `claude-opus-4-8` 返回 HTTP 200，usage log 显示 `claude-opus-4-8→gpt-5.5`。

## 观察到的非本次问题

生产最近日志仍有以下独立问题，需要后续排期，不属于本次分组映射收敛导致：

- 个别 `/v1/messages` 请求等待 API key 并发槽超时。
- 个别上游 SSE 出现 HTTP/2 `INTERNAL_ERROR`。
- `/v1/chat/completions` 里直接请求 Claude Opus 模型会被 Codex 上游拒绝；这条不是 Claude `/v1/messages` dispatch 路径。
- 大上下文请求仍可能触发上游 context-window 错误。

## 回滚边界

如需回滚本次配置，只需要把同一批 OpenAI dispatch 分组的 `opus_mapped_model` 改回 `gpt-5.4`，并删除 `claude-opus-4-6`、`claude-opus-4-7` 的精确映射；`claude-opus-4-8 -> gpt-5.5` 是本次前已有配置，默认不回滚。
