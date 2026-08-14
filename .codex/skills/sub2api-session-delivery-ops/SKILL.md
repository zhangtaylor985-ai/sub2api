---
name: sub2api-session-delivery-ops
description: 用于 Sub2API Session V2 交付链路的历史归档本地重建、Claude Opus 5 黑盒保真审计、Google Drive 版本化上传与 SHA-256 回读、projection reseed、Token 指标回填、小时 exporter 故障恢复和上线验收。用户提到 Session 交付文件重建、归档补跑、Drive 上传、fidelity audit、reseed、Token 回填、sessionctl 或 Session 交付运维时使用。
---

# Sub2API Session 交付运维

## 核心原则

先读取项目 `AGENTS.md` 和当前执行记录，再确定代码版本、生产拓扑与授权边界。复杂任务继续维护 `task_plan.md`、`findings.md`、`progress.md`。

始终遵守以下门禁：

- 只从已提交的固定 Git commit 构建 `sessionctl`；不得从脏工作区重建正式历史文件。
- 输入归档只读，输出写入全新空目录；不得原位重建或覆盖 Google Drive 正式目录。
- 历史重建、Drive 上传、projection reseed、Token 回填和数据库清理是独立阶段；不得把后四项隐含在重建中。
- 上传前必须通过逐文件 validator 和全序列 fidelity audit；上传后必须从 Drive 下载字节流重算 SHA-256。
- reseed 与 Token 回填先 dry-run，只有用户明确批准后才 apply；purge 只允许针对已完成持久化回读验证的小时。
- 不回显 DSN、OAuth token、rclone token、API Key 或私钥。实际 GPT 上游与公开 `claude-opus-5` 投影必须如实区分。
- 保真改动只进入 `backend/internal/sessiondelivery` 的保存/导出链路；不得顺带修改实时响应链路，也不得改动用户要求保留的 `signature.go`。

## 任务路由

- **历史归档重建或重新上传**：完整读取 [references/historical-rebuild.md](references/historical-rebuild.md)，使用本 Skill 的三个脚本。
- **线上小时归档失败、积压、磁盘或服务异常**：完整读取 [references/live-operations.md](references/live-operations.md)，并结合项目的 `$sub2api-production-inspection`、`$sub2api-production-regression`、`$sub2api-deploy` 或 `$sub2api-local-binary-deploy`。
- **仅审计现有文件**：运行 `sessionctl validate` 逐文件校验，再运行 `sessionctl audit-fidelity` 做完整时间序列校验；不要重建。
- **仅查看 Token 指标**：优先读取管理 API/数据库已有精确聚合；不得按文件大小估算 Token。

## 标准历史重建流程

1. 获得用户“代码已冻结，可以重建”的明确指示；记录 Git commit、输入 Drive 文件夹、小时范围和旧目录状态。
2. 下载完整且连续的输入归档到本地只读目录，记录输入文件清单与 SHA-256。
3. 运行 `scripts/build_sessionctl.sh`，从固定 commit 构建原生 `sessionctl`。
4. 先用 `scripts/run_historical_rebuild.sh --plan-only` 核对范围、磁盘和目标路径。
5. 执行本地重建。脚本自动完成输入 audit、重建、逐文件 validate、输出全序列 audit 和 SHA 清单。
6. 人工抽样 Claude Code/Codex、多轮 thinking 回声、cache、tool use/web search/citations、Token usage；确认 `thinking.signature` 与历史回声连续。
7. 只有前述门禁全部通过，才运行 `scripts/upload_verified_run.sh`，把同一 run 上传到全新版本目录。保留旧目录作为回滚证据。
8. 使用脚本生成的 `REMOTE_SHA256SUMS` 与本地 `SHA256SUMS` 比对；再独立抽一个或多个对象执行 `rclone cat | shasum -a 256`。
9. 若需要延续线上投影状态，先 dry-run `reseed-projection`，单独取得 apply 授权后再执行；Token 历史回填同理。
10. 恢复并观察小时 timer，至少验证一次无工作成功运行或一个真实闭合小时归档。

## 完成交付证据

最终报告至少包含：固定 commit 与二进制 SHA、输入/输出小时范围、记录数、Token 数、全序列 audit 违规数、Drive 路径、每个对象本地/远端 SHA、DB 是否 reseed/backfill/purge、服务状态、定时器下一触发时间、磁盘余量、不可恢复的数据缺口。

不得用“文件已生成”代替完整验收，也不得把尚未回填的历史 Token 显示为 0；应显示为未知或覆盖率不足。

## 资源

- `scripts/build_sessionctl.sh`：从固定 Git commit 隔离构建本机 `sessionctl`。
- `scripts/run_historical_rebuild.sh`：以不可覆盖方式执行一次本地重建与完整审计。
- `scripts/upload_verified_run.sh`：重新验证既有 run 后上传到全新 Drive 目录并逐对象回读。
- `references/historical-rebuild.md`：历史重建、上传、reseed 与 Token 回填细节。
- `references/live-operations.md`：小时 exporter 故障恢复与生产验收清单。
