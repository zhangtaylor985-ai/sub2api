# 历史 Session 归档本地重建

## 目录

1. 适用边界
2. 输入与代码冻结
3. 构建和计划
4. 两轮重建与幂等
5. Google Drive 上传
6. Projection reseed 与 Token 回填
7. 中断、复用与性能预期

## 适用边界

历史重建是 archive-to-archive 的离线投影，只处理已存在的归档，不能补回未被 recorder 捕获的 Session。交付件公开投影为 `claude-opus-5`，实际上游模型和内部诊断仍需如实留在受限内部证据中，不能对用户正文或助手散文做伪造性改写。

只有用户明确表示代码已经冻结、可以重新生成历史交付文件时才启动。代码继续变化时停止重建、上传、reseed 与回填。

## 输入与代码冻结

- 固定一个已提交并推送到授权 remote 的 commit；先 `git fetch origin`。构建脚本会要求该 commit 存在于已 fetch 的 `origin/*` remote-tracking ref，不从脏工作区构建正式工具。
- 本机 Go 必须满足该 commit 的 `backend/go.mod` 最低版本；构建固定使用 `GOTOOLCHAIN=local` 和 `-mod=readonly`，禁止静默下载另一套工具链或改依赖，并把实际 Go 版本写入 manifest。
- 输入使用新建本地目录，下载后设为只读；记录 Drive 来源、对象名、大小与 SHA-256。
- 输入必须是唯一、连续、按 UTC 小时排序的 `.tar.zst`。允许真实无流量小时，但不能有同小时重复对象。
- 输出根目录必须与输入隔离且已存在；可用空间至少为输入大小两倍，实际建议三倍。
- 旧 Drive 正式目录保持不变；新目标名包含日期、语义版本和 commit 短 SHA。

## 构建和计划

在项目工作树中：

```bash
REPO="$(git rev-parse --show-toplevel)"
SKILL_DIR="$REPO/.codex/skills/sub2api-session-history-rebuild"
BUILD_DIR="$(mktemp -d /private/tmp/sub2api-sessionctl-build-output.XXXXXX)"

"$SKILL_DIR/scripts/build_sessionctl.sh" \
  --repo "$REPO" \
  --git-ref '<frozen-commit>' \
  --output "$BUILD_DIR/sessionctl"

"$SKILL_DIR/scripts/run_historical_rebuild.sh" \
  --sessionctl "$BUILD_DIR/sessionctl" \
  --build-manifest "$BUILD_DIR/sessionctl.manifest" \
  --input-dir '<immutable-input-dir>' \
  --output-root '<existing-output-root>' \
  --label '<semantic-version>-pass1' \
  --plan-only
```

计划输出的 archive 数、输入大小、空闲空间、固定 commit、build manifest SHA、二进制 SHA 与新 run 路径都应人工核对。commit 不允许由操作员手填；必须从 manifest 派生并与实际二进制 SHA 对上。

## 两轮重建与幂等

去掉 `--plan-only` 执行第一轮，完成后用相同参数、仅把 label 改为 `pass2` 执行第二轮。每个 run 保存：

- `artifacts/*.tar.zst`；
- `reports/RUN.txt`、`STATE`、`SUCCESS` 或 `FAILED`；
- `reports/BUILD_MANIFEST`，绑定 commit、二进制 SHA、Go 版本和 `go.mod/go.sum` SHA；
- `reports/INPUT_SHA256SUMS`；
- `reports/input-audit.json`、`rebuild.json`；
- `reports/validate-*.json`、`output-audit.json`；
- `reports/SHA256SUMS`。

两轮成功后：

```bash
"$SKILL_DIR/scripts/verify_rebuild_idempotence.sh" \
  --first-run '<pass1-run-dir>' \
  --second-run '<pass2-run-dir>' \
  --attestation '<new-idempotence-attestation-file>'
```

脚本要求对象集合、SHA 和每个 archive 字节完全一致，并生成绑定两轮 run 名、commit、构建 manifest、输入 manifest、输出 manifest、二进制和对象数的机器可验证 attestation。失败时不挑选“看起来更好”的一轮上传，先定位非确定性或输入污染。

## Google Drive 上传

复用任一已完成且通过幂等门禁的 run：

```bash
"$SKILL_DIR/scripts/upload_verified_run.sh" \
  --sessionctl "$BUILD_DIR/sessionctl" \
  --run-dir '<completed-run-dir>' \
  --idempotence-attestation '<idempotence-attestation-file>' \
  --drive-dest 'gdrive:Sub2API/session-delivery-rebuild-<YYYYMMDD>-<version>-<commit-prefix>'
```

脚本要求 `SUCCESS` 与有效 attestation，重新逐文件 validator 和全序列 audit，核对 commit、构建 manifest、输入/输出 SHA 与实际二进制。目标被硬限制为项目 remote 下的 `gdrive:Sub2API/session-delivery-rebuild-YYYYMMDD-VERSION-COMMIT`，末尾 commit 必须匹配 attestation 且目录必须为空；随后使用 `--immutable` 上传，再对每个远端对象执行 `rclone cat` 回读 SHA。

如果本机没有可用 rclone，不复制或回显生产 OAuth 配置。可以把完整成功 run 复制到隔离机的新建 0700 staging，在磁盘预检后通过 systemd EnvironmentFile 的既有 rclone/代理环境执行上传；禁止 `source` 或打印环境文件。上传失败留下的非空目标不复用，改用另一个新版本目录。

## Projection reseed 与 Token 回填

只有完整连续、audit 0 violations 且 Drive SHA 回读完成的目录可用于 reseed。默认 dry-run：

```bash
sessionctl reseed-projection \
  -input-dir '<verified-artifacts-dir>' \
  -dsn-env SESSION_DATABASE_DSN

sessionctl backfill-tokens \
  -archive-dir '<verified-artifacts-dir>' \
  -dsn-env SESSION_DATABASE_DSN
```

保存 dry-run 结果，单独获得授权后才增加 `-apply`。执行 reseed 时 exporter 必须串行，且保存旧 checkpoint 备份。Token 只从 hour/SHA/size/count 与数据库已验证批次完全匹配的对象回填，不按文件大小估算；未覆盖值显示未知，不显示 0。

## 中断、复用与性能预期

`rebuild-archives` 需要按会话时间序列重算，CPU 时间不能用 Skill 消除。Skill 优化的是人工时间和错误率：一次命令完成所有审计、留下明确成功/失败标记、上传重试复用成功 run、监控无需读取 payload。

当前没有可信 checkpoint resume。中断后保留失败 run，使用同一 commit 和输入新建 run；不要拼接两轮局部小时，也不要为了提速并发拆小时。长任务可这样防止睡眠并保留终端日志：

```bash
caffeinate -dimsu "$SKILL_DIR/scripts/run_historical_rebuild.sh" ... \
  2>&1 | tee '<outside-input-output-root>/rebuild-pass.log'
```
