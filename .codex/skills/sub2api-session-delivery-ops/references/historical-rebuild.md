# 历史 Session 归档重建

## 目录

1. 适用边界
2. 前置清单
3. 构建与本地重建
4. Google Drive 上传
5. Projection reseed
6. Token 指标回填
7. 中断与恢复

## 适用边界

只在用户明确冻结交付逻辑后重建。代码仍在调整时，停止所有历史重建、上传、reseed 与历史 Token 回填；保留已有半成品，不删除也不作为正式交付。

重建是 archive-to-archive 的离线投影，不读取生产原始记录，也不补回未被 Session recorder 捕获的数据。正式结论必须明确实际 GPT 上游与公开 `claude-opus-5` 交付投影的区别。

## 前置清单

- 固定一个已提交 Git commit，并确认 `backend/internal/sessiondelivery/signature.go` 没有非预期改动。
- 记录输入 Drive 文件夹 ID/路径、对象列表、小时范围、总大小与 SHA-256。
- 输入必须是完整、连续、按 UTC 小时唯一的 `.tar.zst` 序列。
- 本地输出空间至少为输入大小的两倍；长任务优先在本机运行，不占用 2GB DB 主机。
- 保持旧 Drive 目录不变，新输出使用带日期、语义版本和 commit 短 SHA 的目录名。

## 构建与本地重建

以下示例中的路径应替换为本次实际目录：

```bash
SKILL_DIR="$HOME/.codex/skills/sub2api-session-delivery-ops"
REPO="/Users/taylor/sdk/sub2api"
BUILD_DIR="$(mktemp -d /private/tmp/sub2api-sessionctl-build-output.XXXXXX)"

"$SKILL_DIR/scripts/build_sessionctl.sh" \
  --repo "$REPO" \
  --git-ref '<frozen-commit>' \
  --output "$BUILD_DIR/sessionctl"

"$SKILL_DIR/scripts/run_historical_rebuild.sh" \
  --sessionctl "$BUILD_DIR/sessionctl" \
  --input-dir '<immutable-input-dir>' \
  --output-root '<existing-output-root>' \
  --label '<semantic-version>' \
  --source-ref '<frozen-commit>' \
  --plan-only
```

计划核对无误后去掉 `--plan-only`。脚本在新 run 目录中保存：

- `artifacts/*.tar.zst`
- `reports/input-audit.json`
- `reports/rebuild.json`
- `reports/validate-*.json`
- `reports/output-audit.json`
- `reports/SHA256SUMS`
- `reports/RUN.txt`

长任务可用 `caffeinate` 包裹，防止 macOS 睡眠。不要为了提速并行拆分小时：多轮 thinking/cache 投影状态需要跨小时顺序延续。

## Google Drive 上传

上传必须复用已通过审计的同一 run 目录，并指定全新空目录：

```bash
"$SKILL_DIR/scripts/upload_verified_run.sh" \
  --sessionctl "$BUILD_DIR/sessionctl" \
  --run-dir '<completed-run-dir>' \
  --drive-dest 'gdrive:Sub2API/session-delivery-rebuild-<date>-<version>-<sha>'
```

脚本重新验证既有 run，确认文件 SHA 与重建时完全一致后才上传，因此上传失败重试不会重复耗时重建。它拒绝正式 `session-delivery` 目录和任何非空目标，使用 `--immutable` 上传，并通过 `rclone cat` 对每个远端对象重算 SHA-256。上传后仍需独立抽样回读。

若本机没有可用 rclone，不要临时复制或回显生产 OAuth 配置。优先把整个 completed run 复制到隔离 DB 主机的新建 0700 staging 目录，在磁盘预检通过后，使用该机已验证的 rclone binary/config/Xray 环境运行同一上传脚本。通过 systemd `EnvironmentFile` 注入环境，不要 `source` 或打印环境文件；上传完成后默认保留 staging，清理需单独确认。

## Projection reseed

只允许用完整、连续且最终审计为 0 violations 的新目录。先在 DB 主机或安全隧道中 dry-run：

```bash
sessionctl reseed-projection \
  -input-dir '<artifacts-dir>' \
  -dsn-env SESSION_DATABASE_DSN
```

保存 dry-run JSON、旧 checkpoint 备份与摘要。用户明确批准后才增加 `-apply`。执行时暂停 exporter，apply 成功后恢复 timer，并验证后续真实小时延续同一投影状态。

## Token 指标回填

历史归档 Token 只能从与数据库已验证批次在 hour/SHA/size/count 上完全匹配的正式对象计算，不能按文件大小估算。

先 dry-run：

```bash
sessionctl backfill-tokens \
  -archive-dir '<exact-verified-artifacts-dir>' \
  -dsn-env SESSION_DATABASE_DSN
```

核对覆盖小时、未知小时和总 Token 后，单独取得授权并增加 `-apply`。尚未回填的历史值在 UI 中显示未知或覆盖率不足，不得显示成 0。

## 中断与恢复

当前 `rebuild-archives` 按小时写出文件，但没有经过验证的 checkpoint resume 协议。中断后：

1. 保留半成品 run 目录作为证据。
2. 不上传、不 reseed、不回填。
3. 从同一输入和固定 commit 新建另一个空 run 目录重新执行。

不要把局部成功小时与另一个版本的输出拼接，除非先实现并测试可证明状态连续的 resume 协议。
