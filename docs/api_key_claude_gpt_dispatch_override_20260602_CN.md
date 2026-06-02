# API Key 级 Claude -> GPT 目标模型覆盖

日期：2026-06-02

## 背景

Sub2API 原本只有两层 Claude -> GPT 模型映射：

- 分组级：`groups.messages_dispatch_model_config`
- 账号级：`accounts.credentials.model_mapping`

这两层无法表达“同一个 OpenAI 分组内，某一把 API Key 单独把 Claude 请求转到 `gpt-5.4`，其他 key 继续继承分组默认”的需求。旧 CLIProxyAPI 中有 per-key `claude-gpt-target-family`，因此 Sub2API 本次补齐对应能力。

## 实现

新增字段：

- 表：`api_keys`
- 字段：`messages_dispatch_model_config`，JSONB，默认 `{}`。
- 迁移：`backend/migrations/145_add_api_key_messages_dispatch_model_config.sql`

优先级：

1. API Key 级 `messages_dispatch_model_config` 命中时，作为 OpenAI `/v1/messages` dispatch 的默认目标模型。
2. API Key 级为空或未命中时，回退分组级 `groups.messages_dispatch_model_config`。
3. 账号级 `accounts.credentials.model_mapping` 仍保留最终上游模型改写和账号支持模型约束语义。

空 JSON 表示“不覆盖，继承分组”，不会隐式使用代码默认值。

## 管理入口

管理端 API Key 创建/编辑弹窗新增：

- Opus 目标模型
- Sonnet 目标模型
- Haiku 目标模型

留空表示继承分组配置。当前 UI 暂不展示 exact mapping 行，但会保留已有 exact mapping，避免编辑时误清空未来高级配置。

## 本次生产配置

生产 API Key：

- `api_keys.id=125`
- 名称：`dyer`
- 分组：`CP Legacy double`

本次配置：

```json
{
  "opus_mapped_model": "gpt-5.4",
  "sonnet_mapped_model": "gpt-5.4",
  "haiku_mapped_model": "gpt-5.4",
  "exact_model_mappings": {}
}
```

现有模型族权限未改变。

## 验证

本地门禁：

- `git diff --check`
- `go test ./internal/service -run 'Test(APIKeyResolveMessagesDispatchModel|NormalizeOpenAIMessagesDispatchModelConfig|APIKeyService_SnapshotRoundTrip)'`
- `go test ./internal/handler -run 'TestResolveOpenAIMessagesDispatchMappedModel'`
- `go test -tags=unit ./internal/repository -run 'TestAPIKeyRepository_GetByKeyForAuth_PreservesMessagesDispatchModelConfig_SQLite|TestGroupEntityToService_PreservesMessagesDispatchModelConfig'`
- `go test ./...`
- `corepack pnpm@9.15.9 run lint:check`
- `corepack pnpm@9.15.9 run typecheck`
- `corepack pnpm@9.15.9 run build`

生产 canary：

- 镜像：`zhangtaylor985/sub2api:main-85cd117b`
- canary：`127.0.0.1:18080`
- `claude-opus-4-7` 返回 200，usage log：`claude-opus-4-7→gpt-5.4`
- `claude-sonnet-4-6` 返回 200，usage log：`claude-sonnet-4-6→gpt-5.4`

正式生产：

- 正式镜像：`zhangtaylor985/sub2api:main-85cd117b`
- `http://127.0.0.1:8080/health` 正常
- `https://cc.claudepool.com/health` 正常
- `claude-opus-4-7` 返回 200，usage log：`claude-opus-4-7→gpt-5.4`
- `claude-sonnet-4-6` 返回 200，usage log：`claude-sonnet-4-6→gpt-5.4`

## 回滚

应用回滚：

1. 将 `/root/cliapp/sub2api/docker-compose.yml` 中 app image 切回上一版 `zhangtaylor985/sub2api:main-32ddc96c`。
2. 执行 `docker compose up -d sub2api`。

配置回滚：

```sql
UPDATE api_keys
SET messages_dispatch_model_config = '{}'::jsonb,
    updated_at = NOW()
WHERE id = 125;
```

直接改库后需要清理认证快照：删除 Redis `apikey:auth:*`，或至少删除受影响 key 的认证快照。

## 观察项

上线后日志仍能看到其他 API Key 的 Sonnet 请求继承分组配置后命中 `gpt-5.3-codex`，上游返回 ChatGPT account 不支持错误。这不是本次 key 级覆盖导致；如需全局收敛，应单独评估是否把更多 key 或分组的 Sonnet 目标模型改成 `gpt-5.4`。
