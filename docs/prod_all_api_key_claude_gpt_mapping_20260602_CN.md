# 2026-06-02 生产全 API Key Claude -> GPT 映射收敛

## 目标

- 将线上 Sub2API 所有未删除 API Key 的 Opus family 映射统一为 `gpt-5.4`。
- 将线上 Sub2API 所有未删除 API Key 的 Sonnet family 映射统一为 `gpt-5.3-codex`。
- 完成后做生产黑盒验证，重点确认 Sonnet 是否还能通过。

## 执行方式

- 使用 API Key 级 `api_keys.messages_dispatch_model_config`。
- 不修改账号级 `accounts.credentials.model_mapping`，避免把原本无限制的 OAuth/ChatGPT 账号变成模型白名单账号。
- 对 `deleted_at IS NULL` 的 API Key 合并写入：
  - `opus_mapped_model = "gpt-5.4"`
  - `sonnet_mapped_model = "gpt-5.3-codex"`
- 保留原有 JSON 中的其他字段，例如既有 Haiku 配置或 exact mapping。

## 生产快照

- 有效 API Key：82 个。
- 所有有效 key 均已绑定分组。
- 写入前只有 3 个 key 存在 key 级 `messages_dispatch_model_config`。
- OpenAI dispatch 分组层仍有 Opus `gpt-5.5` 配置，但 key 级 family override 优先级更高，会覆盖分组层结果。
- active+schedulable OpenAI 账号未设置相关 `credentials.model_mapping`，不会把本次 key 级目标再改成其他模型。

## 备份与缓存

- 有效备份文件：
  `/root/cliapp/sub2api/ops_backups/api_key_messages_dispatch_config_before_opus54_sonnet53codex_20260602T100209Z.tsv`
- 备份行数：82。
- 首次备份命令因 shell 引号问题生成过 0 行文件，不作为回滚依据。
- Redis auth cache 已清理：
  - 删除 `apikey:auth:*` snapshot 16 个。
  - 最终剩余 0。

## 写入结果

生产复核结果：

```text
total=82
opus_54=82
sonnet_53_codex=82
with_override=82
```

## 黑盒验证

抽样策略：

- 在 5 个 OpenAI 分组中各选 1 个有效且允许 Claude family 的代表 API Key。
- 不记录 raw API Key。
- 分别请求 `claude-opus-4-7` 和 `claude-sonnet-4-6`。

Opus 结果：

- 5/5 HTTP 200。
- usage log 确认映射链为 `claude-opus-4-7→gpt-5.4`。

Sonnet 结果：

- 5/5 HTTP 502。
- 客户端响应保持黑盒：`api_error "Upstream request failed"`，并带 `request_id`。
- 服务端日志真实根因为当前生产 ChatGPT/Codex 账号不支持 `gpt-5.3-codex`。

## 结论

- 本次 Opus `→gpt-5.4` 已生效且生产黑盒通过。
- 本次 Sonnet `→gpt-5.3-codex` 已生效，但当前生产账号不支持该目标模型，因此 Sonnet 黑盒不通过。
- 这不是 Sub2API 映射未生效，而是目标上游模型不可用。
- 因用户明确指定 Sonnet 目标为 `gpt-5.3-codex`，本次没有擅自改为其他可用模型。

## 回滚/后续选择

可选路径：

1. 保持当前配置，等待或更换支持 `gpt-5.3-codex` 的生产账号。
2. 若要求 Sonnet 立即可用，把 Sonnet key 级目标改为当前生产账号支持的模型，例如 `gpt-5.4`。
3. 若要回滚到写入前状态，使用有效备份 TSV 按 API Key ID 恢复 `messages_dispatch_model_config`，再清理 Redis `apikey:auth:*` auth snapshot。
