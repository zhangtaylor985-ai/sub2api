# Sub2API Claude -> GPT Web Search 兼容任务计划

## 目标

接管 Sub2API 作为后续主要维护项目，梳理 Claude `/v1/messages` -> GPT/OpenAI Responses 的模型映射关系，并按“方案一”移植当前 CLIProxyAPI 中更稳妥的 Claude 客户端 Web search 兼容逻辑。

## 范围

- 记录线上 Sub2API 部署与 SSH 运维信息到项目级 `AGENTS.md`。
- 梳理两层 Claude -> GPT 模型映射：
  - Codex/OpenAI 账号侧模型映射。
  - OpenAI 分组 `/v1/messages` 调度侧模型映射。
- 实现方案一：参考 CLIProxyAPI 当前 Claude -> GPT Web search 兼容层，在 Sub2API 的 OpenAI `/v1/messages` 路径改善 Claude CLI / VSCode 对 `web_search_call` 的展示与流式兼容。
- 将本次改动、线上事实、验证结果落到项目文档。

## 非目标

- 不默认把 OpenAI 原生 web_search 替换为 Brave/Tavily 模拟。
- 不把方案三作为主路径，不依赖不稳定的 OpenAI 搜索引用内部字段。
- 不泄露线上 API Key、账号密钥、数据库密码等敏感信息。

## 阶段

| 阶段 | 状态 | 输出 |
| --- | --- | --- |
| 1. 建立 planning 文件 | complete | `task_plan.md`、`findings.md`、`progress.md` |
| 2. 线上与本地项目接管信息梳理 | complete | `AGENTS.md` 运维记录与项目 skills |
| 3. Claude -> GPT 模型映射关系梳理 | complete | `findings.md` 与任务文档 |
| 4. 方案一设计落地 | complete | 代码实现与单测 |
| 5. 本地回归验证 | complete | Go/前端相关测试结果 |
| 6. 文档归档 | complete | `docs/` 任务记录 |
| 7. 线上发布/验证决策 | in_progress | 先 canary + cc1 黑盒，再决定是否切生产 |

## 决策记录

- 2026-05-27：用户明确选择只做方案一。
- 2026-05-27：Sub2API 将作为后续主要维护项目，旧 CLIProxyAPI 项目只作为参考来源。
- 2026-05-27：线上 Sub2API 使用 `weishaw/sub2api:latest` 镜像，宿主机不是源码 Git 工作区；本次先完成本地实现、测试和文档，线上发布需先确定镜像 tag/registry/回滚流程。
- 2026-05-27：因 Postgres/Redis 仍在 Docker 网络内，短期生产发布采用应用容器替换/重启；宿主机 systemd 直跑作为后续独立迁移任务。

## 错误记录

| 时间 | 错误 | 处理 |
| --- | --- | --- |
| 2026-05-27 | 初始 `rg` 组合模式未返回结果 | 后续改为更精确的分文件/分目录搜索 |
| 2026-05-27 | 一次 `find ... | sed` 文件列表命令在 macOS sed 下参数错误 | 改用 `find`/`rg --files` 直接列文件 |
| 2026-05-27 | 在仓库根目录运行 Go test 导致 module 解析失败 | 改到 `backend/` 模块目录运行测试 |
| 2026-05-27 | 在 `backend/` 目录执行 gofmt 时误带 `backend/` 路径前缀 | 使用模块内相对路径重跑 |
| 2026-05-27 | `python3 tools/secret_scan.py` 不存在 | 记录门禁缺口，本次改用改动范围敏感词扫描兜底 |
