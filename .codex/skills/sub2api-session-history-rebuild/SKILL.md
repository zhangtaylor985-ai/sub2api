---
name: sub2api-session-history-rebuild
description: 用于 Sub2API Session V2 历史 Google Drive 交付文件的本地重建、Claude Opus 5 保真审计、二次重建逐字节幂等验证、版本化上传与远端 SHA-256 回读。用户提到重新生成既有交付文件、历史 archive rebuild、历史 Drive 目录换版、离线 fidelity audit、projection reseed 或历史 Token 回填时使用。生产小时 exporter 发布和追赶改用 sub2api-session-delivery-ops。
---

# Sub2API Session 历史重建

## 先读取完整流程

完整读取 [references/historical-rebuild.md](references/historical-rebuild.md)，再决定是重建、仅审计、上传既有成功 run、幂等比对、projection reseed 还是 Token 回填。代码仍在调整或用户要求暂停历史重建时，立即停止计算与上传，保留已有目录，不把半成品当正式交付。

生产发布、实时小时积压和 timer 恢复使用 `$sub2api-session-delivery-ops`，不要在本 Skill 中改生产二进制或启动 exporter。

## 标准流程

1. 固定一个已提交且已推送到 `origin` 的 Git commit，先 `git fetch origin`，确认交付逻辑冻结、输入 Drive 目录与小时范围明确。
2. 把输入对象下载到新的只读本地目录，记录对象清单与 SHA。不得覆盖旧 Drive 目录。
3. 用 `scripts/build_sessionctl.sh` 从固定 commit 的 Git archive 构建本机 `sessionctl` 及身份 manifest；脚本要求本机 Go 满足 `go.mod` 的最低版本，并固定 `GOTOOLCHAIN=local`、`-mod=readonly`。
4. 把 build manifest 传给 `scripts/run_historical_rebuild.sh --plan-only`，检查输入数量、磁盘、输出位置、commit、manifest SHA 和二进制 SHA。
5. 正式执行脚本。每次创建全新 run，自动记录输入 SHA、状态标记、输入审计、重建、逐文件 validator、输出全序列审计和输出 SHA。
6. 使用相同 commit、输入和 `sessionctl` 完成第二个全新 run，再用 `scripts/verify_rebuild_idempotence.sh` 做逐对象字节比较并生成不可覆盖的 attestation。
7. 人工抽样 Claude Code/Anthropic API、thinking 回声、cache、tool/web search/citations、usage 与 Token；不得用自洽产物替代真实 Claude Code 转录对照。
8. 只有两个 run 都成功、幂等一致且 audit 0 violations，才把 attestation 传给 `scripts/upload_verified_run.sh`。脚本只接受 `gdrive:Sub2API/session-delivery-rebuild-YYYYMMDD-VERSION-COMMIT` 的全新空版本目录，且 commit 后缀必须匹配 attestation，逐对象从 Drive 回读 SHA，不删除或覆盖远端对象。
9. Projection reseed 与历史 Token 回填默认 dry-run，必须单独取得 apply 授权；purge 不属于历史本地重建。

## 中断与复用

- 当前 `rebuild-archives` 没有经过证明的 checkpoint resume。中断 run 会留下 `FAILED` 与 `STATE`，必须从同一输入新建 run，不能拼接局部小时。
- 上传失败不需要重新计算。复用已有 `SUCCESS` run 与对应幂等 attestation 执行上传脚本，它会重新 validator/audit、核对构建身份和原始 SHA，再上传到新的空目录。
- 长任务优先在本地运行；使用 `caffeinate` 防止 macOS 睡眠。不要并行拆小时，多轮 thinking/cache 状态需按 UTC 小时连续重放。

## 完成证据

最终报告至少包含：commit、`sessionctl` SHA、输入/输出小时范围与对象数、两个 run 路径、逐字节幂等结果、记录数/Session 数/Token、audit 违规数、Drive 新目录、每个对象本地与远端 SHA，以及 reseed/backfill 是否仅 dry-run、已 apply 或未执行。

## 资源

- `scripts/build_sessionctl.sh`：固定提交的本机隔离构建与二进制身份 manifest。
- `scripts/run_historical_rebuild.sh`：不可覆盖的单次重建、状态标记与完整审计。
- `scripts/verify_rebuild_idempotence.sh`：两个成功 run 的逐对象字节一致性验证与 attestation。
- `scripts/upload_verified_run.sh`：复用成功 run，上传到全新 Drive 目录并完整回读 SHA。
- `references/historical-rebuild.md`：下载、重建、幂等、上传、reseed 与 Token 回填细节。
