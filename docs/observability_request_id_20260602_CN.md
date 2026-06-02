# Sub2API 错误可观测性与 Request ID 记录

时间：2026-06-02

## 背景

用户截图中的错误为：

```text
Claude's response exceeded the 64000 output token maximum. To configure this behavior, set the CLAUDE_CODE_MAX_OUTPUT_TOKENS environment variable.
```

该报错更符合 Claude Code 客户端本地输出上限，而不是 Sub2API 上游错误原样返回。线上最近 2 小时日志未检索到 `CLAUDE_CODE_MAX_OUTPUT_TOKENS` 或 `64000 output token` 原文。

## 当前日志系统

线上 Sub2API 当前有三层日志入口：

- Docker stdout/stderr：`docker logs sub2api`
- 容器文件日志：`/app/data/logs/sub2api.log`，带轮转压缩日志
- Postgres 运维索引：`ops_system_logs`、`ops_error_logs`

当前日志级别按运行环境和实际输出判断为 `info`。日志默认同时写 stdout 和文件，文件默认 100MB 轮转、保留 7 天、压缩。

## 本次改动

本次增强不改变成功响应、调度、模型映射、账号选择或计费逻辑，只增强错误排查能力：

- 保留原有 `X-Request-ID` 响应头能力。
- API key auth / gateway middleware 错误体增加 `request_id`。
- Anthropic `/v1/messages` JSON 错误体增加顶层 `request_id`。
- Anthropic SSE error event 增加顶层 `request_id`。
- OpenAI Chat/Images/Responses 错误体在 `error.request_id` 中返回网关 request id。
- Responses SSE `response.failed.response.error.request_id` 返回网关 request id。

错误 message 仍保持用户侧黑盒，不返回 GPT/Codex/auth file/内部路由细节。

## 排查口径

用户报错时优先收集：

- 可见错误文本
- 出错时间
- `request_id` 或 `X-Request-ID`，如果客户端 debug log / HTTP headers / 错误体能看到

服务端查找顺序：

1. 用 `request_id` 查 `/app/data/logs/sub2api.log`。
2. 用 `request_id` 或 `client_request_id` 查 `ops_error_logs`。
3. 必要时用 `api_key_id`、模型、时间窗口查 `usage_logs` 补证据。

注意：`usage_logs.request_id` 常见 `generated:...`，属于用量/上游记录口径，不等同于 HTTP 网关 `X-Request-ID`。
