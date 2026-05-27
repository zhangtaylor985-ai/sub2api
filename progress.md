# Sub2API Claude -> GPT Web Search 兼容进度

## 2026-05-27

- 建立本次任务的 planning 文件。
- 用户明确授权：Sub2API 后续作为主要维护项目，本次需要梳理模型映射并落实方案一。
- 只读检查线上 Sub2API：确认 SSH 入口、部署目录、Docker Compose 容器和端口暴露。
- 初始本地 `rg` 搜索范围过大导致输出被截断；后续改为精确读取相关文件。
- 完成 Claude -> GPT 模型映射链路初步梳理：分组层先给 Claude family 选择目标 GPT，账号层再以 `credentials.model_mapping` 做最终映射/白名单。
- 读取当前 CLIProxyAPI 的 Web search 兼容实现，确认方案一需要移植客户端识别、CLI 合成文本搜索标记、VSCode thinking 进度和 reasoning summary 抑制。
- 完成第一轮代码实现：新增 Anthropic client compat helper，扩展 Responses -> Anthropic 转换状态与 OpenAI `/v1/messages` service 调用点，并补充 apicompat 单测。
- 测试错误：在仓库根目录运行 `go test` 导致 Go 没有进入 `backend/go.mod`，依赖按 GOPATH 解析失败；改为在 `backend/` 目录运行。
- 命令错误：在 `backend/` 目录运行 `gofmt` 时误用 `backend/...` 前缀，文件路径不存在；改用模块内相对路径。
- 完成线上反代只读确认：Caddy active，`cc.claudepool.com` 反代到 `127.0.0.1:8080`，Nginx inactive。
- 新增项目级 `AGENTS.md` 与 `docs/claude_gpt_web_search_compat_20260527_CN.md`，并在 `.gitignore` 中添加精确例外，避免这两个维护文件被忽略。
- 验证通过：
  - `go test ./internal/pkg/apicompat`
  - `go test ./internal/service -run 'TestForwardAsAnthropic|TestNormalizeOpenAIMessagesDispatchModelConfig|TestResolveOpenAIForwardModel|TestOpenAI'`
  - `go test ./internal/handler -run 'OpenAIGateway|Messages|Gateway'`
  - `git diff --check`
- 用户要求按生产上线标准处理，并允许迁移必要 skill 到 Sub2API 项目。
- 新增 `.codex/skills/sub2api-production-regression` 与 `.codex/skills/sub2api-deploy`，把上线门禁、cc1 黑盒、Docker/app-container 发布和回滚原则迁入当前项目。
- 生产门禁执行中：`python3 tools/secret_scan.py` 失败，因为 Sub2API 仓库当前没有该脚本；本次先用改动范围 `rg` 扫描敏感词兜底，后续应补项目级 secret-scan 工具。
- 上下文恢复：已确认变更暂存状态、任务计划和 Sub2API 生产回归 skill；下一步进入 Git 提交/tag、线上 canary、`cc1` 黑盒与正式切换判断。
- 生产回归本地门禁复跑通过：`cd backend && go test ./...` 全量通过；`git diff --check` 通过；改动范围敏感词兜底扫描仅命中文档中的门禁描述与代码变量名，没有发现明文密钥。
