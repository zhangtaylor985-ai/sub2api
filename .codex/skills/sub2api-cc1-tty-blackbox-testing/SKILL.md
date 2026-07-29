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
- `cc2` 常见为交互 shell alias：`CLAUDE_CONFIG_DIR=~/.claude_cc claude2 --dangerously-skip-permissions`。非交互脚本不要依赖 alias 展开，应显式调用底层 binary 和配置目录。

确认客户端是否支持 effort：

```bash
~/.local/bin/claude2 --version
~/.local/bin/claude2 --help | rg -n -- '--effort|--debug-file|--resume|--continue'
```

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

优先创建独立临时 `CLAUDE_CONFIG_DIR`，复制非敏感设置后只在临时目录修改 endpoint、测试 API key、model 与 effort。这样不需要覆盖 `~/.claude_local` / `~/.claude_cc` 的日常配置。临时配置目录必须在仓库外且权限为 `0700`。

## Safe Local OpenAI OAuth Bootstrap

当本地 Sub2API 没有可用 OpenAI/Codex OAuth 账号，而用户明确授权从生产复制测试凭据时，遵循以下边界：

- 当前生产 OpenAI OAuth 凭据存储在 PostgreSQL `accounts.credentials`，不一定存在独立 auth file；可以导出为临时 JSON，但不得在终端回显内容。
- 先只读选择 1–2 个 `active + schedulable`、目标分组可用、支持控制模型和候选模型、`proxy_id IS NULL` 的账号。
- 只读确认 `expires_at` 至少覆盖预计测试窗口；优先要求剩余有效期大于 20 分钟。
- 导出目录必须位于仓库外，目录权限 `0700`、文件权限 `0600`。
- 导入本地前删除 `refresh_token`；本地服务设置 `TOKEN_REFRESH_ENABLED=false`。不得让本地和生产并发消费同一个可轮换 refresh token。
- 本地账号使用隔离数据库 ID、`proxy_id=NULL`，只绑定本地测试分组；不要复制生产 API key、用户余额、代理密码或无关账号。
- 测试负载保持最小，避免给生产账号 entitlement/额度造成不必要压力。
- 如果 access token 已过期、缺少 refresh token 后无法调度、或需要重新 OAuth 授权，立即停止；请用户在本地 Sub2API 完成 Codex OAuth 登录，不要改为使用生产 endpoint 冒充本地测试。
- 测试结束后永久移除临时凭据文件和临时数据库账号，保留的 debug/session/usage 证据必须脱敏。

安全导出时不要把 JSON 放入 shell 变量、命令参数或日志。使用受限权限临时文件或不回显的管道，并在导入前用程序化检查只报告字段是否存在、token 有效期和文件权限。

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
ssh -i /Users/taylor/.ssh/ssh-key-oracle.key opc@161.153.91.242 \
  "sudo journalctl -u sub2api -n 100 --no-pager"
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

### cc2 / effort smoke

Claude Code 支持 `--effort` 时，对每个候选 effort 显式传入，不依赖用户目录的默认值：

```bash
CLAUDE_CONFIG_DIR=/path/to/temporary-claude-config \
ANTHROPIC_BASE_URL=http://127.0.0.1:8080 \
NO_PROXY=127.0.0.1,localhost,::1 no_proxy=127.0.0.1,localhost,::1 \
HTTP_PROXY= HTTPS_PROXY= http_proxy= https_proxy= \
~/.local/bin/claude2 \
  --debug-file /tmp/sub2api-cc2-effort-high.log \
  --model claude-opus-4-8 \
  --effort high \
  -p 'Reply with exactly SUB2API_CC2_EFFORT_HIGH'
```

Claude → OpenAI Responses 的验收映射：

- `low → low`
- `medium → medium`
- `high → high`
- `max → xhigh`
- 客户端未传 effort 时，记录当前代码采用的默认值；不要把“默认”写成“客户端原样传入”。

以本地 `usage_logs.reasoning_effort`、转换单测或脱敏上游捕获为证据；PTY 屏幕无法证明上游收到哪个 effort。

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

真实 `cc2` 形态可显式指定临时配置与 effort：

```bash
CLAUDE_CONFIG_DIR=/path/to/temporary-claude-config \
ANTHROPIC_BASE_URL=http://127.0.0.1:8080 \
NO_PROXY=127.0.0.1,localhost,::1 no_proxy=127.0.0.1,localhost,::1 \
HTTP_PROXY= HTTPS_PROXY= http_proxy= https_proxy= \
~/.local/bin/claude2 \
  --debug-file /tmp/sub2api-cc2-tty.log \
  --dangerously-skip-permissions \
  --model claude-opus-4-8 \
  --effort high
```

操作注意：

- 写入 prompt 后单独发送回车。
- 注入下一轮前，确认上一轮真的结束；不要只看屏幕静止。
- 优先用 debug-file、session JSONL、服务端日志确认每一轮都形成了新的 `/v1/messages`。

## GPT Target A/B Matrix

Claude → GPT 目标模型变更必须使用同账号、同 API key 策略、同 Claude model、同 effort 和同 prompt 做对照。推荐先以 `gpt-5.4` 为控制组，再切到候选精确模型（例如 `gpt-5.6-sol`）。

最小矩阵：

- Claude model：至少 Opus 与 Sonnet 各一款。
- effort：`low / medium / high / max`。
- 请求形态：非流式、SSE、普通 function tool、WebSearch。
- 任务：常识、可验证推理、短代码、严格格式输出。
- 客户端：直接 `/v1/messages`、`claude2 -p`、真实 TTY 连续多轮。

每个样本记录：

- 请求的 Claude model 与 effort。
- 内部 `model_mapping_chain` / `upstream_model`。
- `reasoning_effort`。
- HTTP/SSE 是否完整、首 token 和总耗时。
- 工具开始/完成、最终正文、严格格式是否符合。
- 客户端响应是否仍保持 Claude model，是否泄漏 GPT/Codex/OpenAI 内部标识。

候选模型失败时先换第二个已确认支持候选模型的本地 OAuth 账号复验。两个账号都稳定复现模型/参数/协议错误时判定候选未通过，不切全局默认。

## Server Evidence

本地 Docker 沙盒可用：

```bash
docker logs sub2api-dev --tail 200 2>/dev/null || true
docker logs sub2api --tail 200 2>/dev/null || true
```

生产 systemd 可用：

```bash
ssh -i /Users/taylor/.ssh/ssh-key-oracle.key opc@161.153.91.242 \
  "sudo journalctl -u sub2api --since '15 minutes ago' --no-pager"
```

查 request id 或最近 usage 时，避免输出 raw API key：

```sql
SELECT id, request_id, api_key_id, account_id, model,
       model_mapping_chain, upstream_model, reasoning_effort, created_at
FROM usage_logs
ORDER BY id DESC
LIMIT 20;
```

当前 `usage_logs` 没有 `platform` / `status` 列；请求是否失败应结合
`ops_error_logs`、服务日志和客户端 session JSONL 判断，不要复制旧 schema
的查询语句。

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
- 若做目标模型升级，包含控制模型/候选模型 A/B 表、所有 effort 的上游证据与失败重试结论。
- 临时生产 OAuth access token 是否导入、是否剥离 refresh token、本地 token refresh 是否关闭、临时凭据是否清理。
- 若屏幕输出和日志不一致，明确以哪一侧为 source of truth。

一句话规则：先用 `-p + --debug-file` 证明请求真的命中 Sub2API，再做真实 TTY；PTY 屏幕只能辅助观察，不能单独作为链路成败结论。
