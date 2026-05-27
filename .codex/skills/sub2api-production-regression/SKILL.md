---
name: sub2api-production-regression
description: Use when preparing Sub2API changes for production, especially Claude /v1/messages, OpenAI/Codex compatibility, streaming, web search, billing, auth, Docker/systemd deployment, or cc1 blackbox validation.
---

# Sub2API Production Regression

## Goal

用于 `/Users/taylor/sdk/sub2api` 的生产上线前回归决策与执行。

上线前必须做到：

- 明确变更影响面。
- 跑足够的 Go / frontend / build / smoke 测试。
- 触达 Claude `/v1/messages`、streaming、tool_use、thinking、web search 时必须做 `cc1` 真实 TTY/PTY 黑盒。
- 有明确回滚路径，不在无法回滚的情况下切生产。

## Change Classification

先执行：

```bash
git status --short
git diff --stat
git diff --check
```

按影响面选择回归：

- **Claude/OpenAI protocol compatibility**：`/v1/messages`、Responses/Anthropic 转换、streaming、thinking、tool_use、web_search、模型映射。
- **Runtime / scheduler / billing**：账号调度、sticky session、usage、quota、Redis、Postgres。
- **Management UI / API**：admin 页面、groups/accounts/settings、用户自助页。
- **Deploy only**：Dockerfile、systemd、compose、env/config。
- **Docs only**：文档、计划文件、说明。

## Required Backend Checks

后端 Go 代码改动至少跑：

```bash
cd /Users/taylor/sdk/sub2api/backend
go test ./internal/pkg/apicompat
go test ./internal/service -run 'TestForwardAsAnthropic|TestNormalizeOpenAIMessagesDispatchModelConfig|TestResolveOpenAIForwardModel|TestOpenAI'
go test ./internal/handler -run 'OpenAIGateway|Messages|Gateway'
```

生产发布前默认还要跑：

```bash
cd /Users/taylor/sdk/sub2api/backend
go test ./...
```

如果 `go test ./...` 失败，必须区分是本次改动导致、环境依赖、还是历史 flaky，并记录结论。

## Required Frontend / Image Checks

前端代码改动时跑：

```bash
pnpm --dir frontend run lint:check
pnpm --dir frontend run typecheck
pnpm --dir frontend run build
```

即使没有前端代码改动，只要生产使用 embedded frontend Docker image，上线前也要确认 Docker build 成功。

## CC1 Blackbox Gate

以下任一情况必须做 `cc1` / Claude Code 真实 TTY/PTY 黑盒：

- 改动触达 Claude `/v1/messages`。
- 改动触达 OpenAI Responses -> Anthropic 转换。
- 改动触达 streaming / SSE / partial messages / final flush。
- 改动触达 thinking、tool_use、tool_result、web_search 可见性。
- 用户明确要求黑盒。

最小验证：

- 临时修改 `/Users/taylor/.claude_local/settings.json` 指向候选 Sub2API endpoint。
- 非交互 `-p` smoke：确认 debug-file 里命中目标 endpoint 和 `/v1/messages`。
- Web search 样本：用 `--output-format stream-json --verbose --include-partial-messages` 观察搜索开始、搜索完成、正文输出、最终结果。
- 真实 TTY 连续多轮：确认不是只在单发 `-p` 下成功。

测试结束后记录临时改过的 settings，并按需要恢复或保留。

## Production Report

最终上线报告必须包含：

- commit / tag / image 或 binary 来源。
- 测试命令和结果。
- cc1 黑盒结果。
- canary endpoint 和 production endpoint smoke 结果。
- 是否重启或切换生产。
- 回滚命令。
