---
name: sub2api-cc1-tty-blackbox-testing
description: Use when running Claude Code / cc1 / claude / claude2 real TTY or PTY blackbox tests against Sub2API, especially to verify ANTHROPIC_BASE_URL reaches a local, canary, or production Sub2API endpoint; inspect --debug-file evidence; align Claude client logs with Sub2API server logs, usage_logs, request IDs, model mapping, WebSearch visibility, streaming behavior, or multi-turn TTY behavior.
---

# Sub2API CC1 TTY Blackbox Testing

## Goal

用于 `/Users/taylor/sdk/sub2api` 的 Claude Code / `cc1` / `claude2` 真实客户端黑盒验证。

目标是证明四件事：

- 实际启动的是哪个 Claude CLI binary 和哪个配置目录。
- `ANTHROPIC_BASE_URL` / `ANTHROPIC_AUTH_TOKEN` 是否真的命中目标 Sub2API endpoint。
- 客户端 `--debug-file`、Sub2API 日志、`usage_logs` / request id 能对上。
- 真实 TTY 连续多轮不是只在非交互 `-p` 下成功。

## Source Of Truth

不要只相信 PTY 屏幕。TTY/PTY 输出有 ANSI 重绘、截断和滚屏复写，屏幕静止也不等于请求完成。

证据优先级：

1. Claude CLI `--debug-file`：看实际 env、`[API REQUEST] /v1/messages`、stream first chunk、错误文本。
2. Sub2API 服务端日志：本地容器日志、host-run server 日志、生产 `journalctl -u sub2api`。
3. Sub2API DB 证据：`usage_logs.request_id`、`model_mapping_chain`、`upstream_model`、`status`。
4. Claude session JSONL：看 `type=user`、`isApiErrorMessage`、`API Error`、turn 结束记录。
5. 最后才参考 PTY 屏幕。

常见噪声：

- `HEAD /`、marketplace/plugin 拉取、标题生成请求。
- 固定字符串 prompt 触发的非致命标题 JSON parse 噪声。
- 客户端配置里的 token 覆盖 shell 临时环境变量。
- HTTP 200 后 SSE 中途断流；状态码不是完整成功证据。

## Prepare Client

先确认真实 binary。不要假设 `cc1` 在当前 shell 的 `PATH`。

```bash
which claude || true
which claude2 || true
command -v cc1 || true
command -v claude || true
command -v claude2 || true
ls -la ~/.local/bin ~/.cac/bin 2>/dev/null | sed -n '1,80p'
```

常见入口：

- `~/.local/bin/claude2`
- `~/.cac/bin/claude`
- `claude` / `cc1` wrapper

检查配置时只输出脱敏信息，不要打印 raw token：

```bash
jq '{env: ((.env // {}) | with_entries(if (.key|test("TOKEN|KEY|SECRET"; "i")) then .value="<redacted>" else . end)), model, permissions}' \
  ~/.claude_local/settings.json 2>/dev/null || true
jq '{env: ((.env // {}) | with_entries(if (.key|test("TOKEN|KEY|SECRET"; "i")) then .value="<redacted>" else . end)), model, permissions}' \
  ~/.claude_local/settings.local.json 2>/dev/null || true
```

`~/.claude_local/settings.json` 里的 `env.ANTHROPIC_AUTH_TOKEN` 可能覆盖一次性 shell 变量。黑盒前先备份，记录备份路径到 `progress.md`，不要记录 token：

```bash
cp ~/.claude_local/settings.json "/tmp/claude-local-settings.$(date -u +%Y%m%dT%H%M%SZ).json"
```

## Prepare Endpoint

默认先打本地沙盒，不直接打生产。

- 本地默认 endpoint：`http://127.0.0.1:8080`
- 候选 canary：只绑定远端 `127.0.0.1` 的临时端口，例如通过 SSH tunnel 暴露到本机。
- 生产 endpoint：仅在用户明确要求或上线门禁需要时使用 `https://cc.claudepool.com`。

本地/localhost 测试必须绕过全局代理：

```bash
export NO_PROXY=127.0.0.1,localhost,::1
export no_proxy=127.0.0.1,localhost,::1
unset HTTP_PROXY HTTPS_PROXY http_proxy https_proxy
```

先确认服务可达：

```bash
curl -fsS http://127.0.0.1:8080/health
```

如果是生产或 canary，通过对应日志源观察：

```bash
ssh -p 41012 root@172.247.109.38 "journalctl -u sub2api -n 100 --no-pager"
```

## Direct API Smoke First

先用测试 API key 直接打 `/v1/messages`，确认 Sub2API 侧基础路由、鉴权和模型映射正常，再启动 Claude CLI。

不要在文档、日志或最终回复里输出 raw API key。可以记录 key 名称，例如 `Local Claude GPT Blackbox`。

验收点：

- HTTP 成功，返回 Claude 风格消息。
- 用户侧 `model` 保持请求的 Claude model。
- 内部 `usage_logs.model_mapping_chain` / `upstream_model` 显示预期映射，例如 `claude-opus-4-6 -> gpt-5.5`。

## Noninteractive Claude Smoke

用 `-p + --debug-file` 先证明请求真的打到 Sub2API，再升级到 TTY。

```bash
CLAUDE_CONFIG_DIR=$HOME/.claude_local \
ANTHROPIC_BASE_URL=http://127.0.0.1:8080 \
NO_PROXY=127.0.0.1,localhost,::1 no_proxy=127.0.0.1,localhost,::1 \
HTTP_PROXY= HTTPS_PROXY= http_proxy= https_proxy= \
~/.local/bin/claude2 \
  --debug-file /tmp/sub2api-cc1-smoke.log \
  --dangerously-skip-permissions \
  --model claude-opus-4-6 \
  -p 'Reply with exactly SUB2API_LOCAL_HIT'
```

检查 debug-file：

```bash
rg -n "ANTHROPIC_BASE_URL|\\[API REQUEST\\]|/v1/messages|Stream started|stream first chunk|API Error" \
  /tmp/sub2api-cc1-smoke.log -S
```

如果 debug-file 没有目标 endpoint 或 `/v1/messages`，先修配置，不要继续 TTY。

## WebSearch Sample

WebSearch 回归要同时验证过程可见性和最终结果质量。不要只看到 `Searching the web` 就判定通过。

```bash
CLAUDE_CONFIG_DIR=$HOME/.claude_local \
ANTHROPIC_BASE_URL=http://127.0.0.1:8080 \
NO_PROXY=127.0.0.1,localhost,::1 no_proxy=127.0.0.1,localhost,::1 \
HTTP_PROXY= HTTPS_PROXY= http_proxy= https_proxy= \
~/.local/bin/claude2 \
  --debug-file /tmp/sub2api-cc1-websearch.log \
  --dangerously-skip-permissions \
  --model claude-opus-4-6 \
  -p --output-format stream-json --verbose --include-partial-messages \
  '搜索 OpenAI 官网标题并用中文简短回答'
```

验收点：

- JSONL 或输出中能看到搜索开始、搜索完成、正文输出、最终 `message_stop`。
- 不应退回客户端原生 `WebSearch(...) Found 0 results` 路径。
- 最终回答不能只是“检索异常 / 没有可靠结果”等降级文案，除非上游确实失败。
- 没有额外触发无关工具，例如 Bash 通知工具。

## Real TTY

非交互 smoke 通过后，再跑真实 TTY/PTY。

```bash
CLAUDE_CONFIG_DIR=$HOME/.claude_local \
ANTHROPIC_BASE_URL=http://127.0.0.1:8080 \
NO_PROXY=127.0.0.1,localhost,::1 no_proxy=127.0.0.1,localhost,::1 \
HTTP_PROXY= HTTPS_PROXY= http_proxy= https_proxy= \
~/.local/bin/claude2 \
  --debug-file /tmp/sub2api-cc1-tty.log \
  --dangerously-skip-permissions \
  --model claude-opus-4-6
```

在同一 PTY 内做连续多轮，例如：

- 第一轮要求返回唯一 token：`TTY_SUB2API_ONE`
- 第二轮要求返回唯一 token：`TTY_SUB2API_TWO`
- 如任务涉及 WebSearch，再追加一轮真实搜索后追问。

操作注意：

- 写入 prompt 后单独发送回车。
- 注入下一轮前，确认上一轮真的结束；不要只看屏幕静止。
- 优先用 debug-file、session JSONL、服务端日志确认每一轮都形成了新的 `/v1/messages`。

## Server Evidence

本地 Docker 沙盒可用：

```bash
docker logs sub2api-dev --tail 200 2>/dev/null || true
docker logs sub2api --tail 200 2>/dev/null || true
```

生产 systemd 可用：

```bash
ssh -p 41012 root@172.247.109.38 \
  "journalctl -u sub2api --since '15 minutes ago' --no-pager"
```

查 request id 或最近 usage 时，避免输出 raw API key：

```sql
SELECT id, request_id, api_key_id, account_id, platform, model,
       model_mapping_chain, upstream_model, status, created_at
FROM usage_logs
ORDER BY id DESC
LIMIT 20;
```

如果用户报告可见错误，优先让用户提供 `X-Request-ID` 或错误体里的 `request_id`，再结合 `sub2api-production-inspection` 查询 `ops_error_logs`。

## Reporting

黑盒报告至少包含：

- 实际 client binary 路径。
- 实际 `CLAUDE_CONFIG_DIR`。
- 目标 endpoint。
- settings 是否临时改过，备份路径是什么，是否恢复。
- debug-file 是否看到目标 `ANTHROPIC_BASE_URL` 与 `/v1/messages`。
- Sub2API 日志或 DB 是否看到对应请求。
- usage evidence：`model_mapping_chain` / `upstream_model` / request id。
- 非交互 `-p`、WebSearch、真实 TTY 多轮各自结果。
- 若屏幕输出和日志不一致，明确以哪一侧为 source of truth。

一句话规则：先用 `-p + --debug-file` 证明请求真的命中 Sub2API，再做真实 TTY；PTY 屏幕只能辅助观察，不能单独作为链路成败结论。
