# Session 生产发布与恢复

## 目录

1. 当前拓扑
2. 发布阶段门禁
3. 只读预检
4. 构建与 staging
5. 双机原子替换
6. 追赶与 Drive 验证
7. timer 恢复与回滚

## 当前拓扑

每次先以项目 `AGENTS.md` 为准，不能从本文件推断未来主机。当前基线为：

- App 主机：Oracle Linux ARM64，`sub2api.service`、`sub2api-session-forwarder.service`、`sub2api-session-tunnel.service`。
- Session 隔离机：Ubuntu AMD64、PostgreSQL `session_delivery`、`sub2api-sessiond.service`、`sub2api-session-export.service/timer`、`xray-session-egress.service`。
- App 主机使用 ARM64 app 与 ARM64 `sessionctl`；隔离机使用 AMD64 `sessionctl`，只有 `sessiond` 代码变化时才替换 AMD64 `sessiond`。
- Drive 正式实时目录和历史 rebuild 目录相互独立。

## 发布阶段门禁

严格按以下顺序推进，每一阶段留下可回读证据：

```text
固定提交 -> 回归 -> 四产物构建 -> 双机预检 -> 冻结 timer
-> 回滚件 -> staging/SHA -> 原子替换 -> 健康验证
-> 小时追赶 -> Drive 独立 SHA -> timer 自然触发 -> 最终快照
```

如果代码尚未冻结、远端分支领先、关键测试失败、生产拓扑不明、回滚件无法创建或磁盘达到危险水位，停止在当前阶段，不通过跳过数据或降低 validator 来推进。

## 只读预检

先运行：

```bash
SKILL_DIR="$(git rev-parse --show-toplevel)/.codex/skills/sub2api-session-delivery-ops"
python3 "$SKILL_DIR/scripts/session_production_status.py" --json
```

快照必须包含：

- app、forwarder、tunnel、sessiond、exporter、timer 的 active/sub/result/重启次数；
- `sessiond` 实际 `reject-disk-usage` 阈值；读取不到时不允许继续发布；
- app health、管理 UI 路由、双机二进制 SHA；
- 双机磁盘、内存与 load；
- 正确 spool 目录的真实 used/max/pending/quarantine；forwarder 实际 max 读取不到或与 inspector 返回值不一致时 fail-closed；
- failed/exporting/purged 批次、最近批次计数和未授予锁；
- exporter 当前是否在运行。

Exporter 活跃时不要执行 `COUNT(*) FROM session_records`、父表关系大小或扫 payload 的诊断。长 cursor 会干扰分区 DDL；精确记录数由完成批次提供。

## 构建与 staging

固定提交必须已推送到授权 remote；先 `git fetch origin`。脚本不仅要求 commit 可被 Git 解析，还要求它存在于已 fetch 的 `origin/*` remote-tracking ref。构建命令：

```bash
OUT_ROOT="$(mktemp -d /private/tmp/sub2api-session-release.XXXXXX)"
"$SKILL_DIR/scripts/build_session_release.sh" \
  --repo "$(git rev-parse --show-toplevel)" \
  --git-ref '<frozen-commit>' \
  --output-root "$OUT_ROOT" \
  --label '<change-name>'
```

脚本从 Git archive 构建前端，并要求本机 Go 满足 `go.mod` 最低版本，使用 `GOTOOLCHAIN=local`、`-mod=readonly`，随后生成：

- `sub2api-linux-arm64`，构建标签必须含 `embed`；
- `sessionctl-linux-arm64`；
- `sessionctl-linux-amd64`；
- `sessiond-linux-amd64`；
- `SHA256SUMS`、`MANIFEST.txt`、`SUCCESS`。

上传到两台主机的新 release 目录，不覆盖 live 路径。逐个比对本地 manifest SHA 与远端 staging SHA 后，才可进入替换。

## 双机原子替换

写操作前再次确认用户授权。先停止 exporter 并冻结 timer；不要停止 sessiond，除非确实需要替换它。

每个 live binary 都执行相同安全模式：

1. 读取 live SHA、owner、group、mode。
2. 把 live 文件复制到带 UTC 时间戳的 `/opt/sub2api/backups/` 回滚件。
3. 把已验证 release 安装为相同 owner/group、`0755`。
4. 用同目录临时文件加原子 `mv` 切换；不要直接把上传流写进 live 文件。
5. 回读 live SHA；不一致立即恢复回滚件。

替换顺序建议：

1. App 主机 app + ARM64 `sessionctl`，重启 `sub2api.service` 和 forwarder；验证本机 health、公网 health、管理 UI 200、三服务 active、`NRestarts` 无异常增长。
2. 隔离机 AMD64 `sessionctl`；若 `sessiond` 产物 SHA 与 live 不同且本次代码涉及 ingest，再替换并重启 `sub2api-sessiond.service`。
3. Timer 继续冻结，手工运行最早失败小时或 drain。

主应用替换细节复用 `$sub2api-local-binary-deploy`；正式发布渠道可用时改用 `$sub2api-deploy`。不要在 2GB 隔离机编译。

## 追赶与 Drive 验证

有界监控命令：

```bash
python3 "$SKILL_DIR/scripts/session_production_status.py" \
  --watch \
  --interval 60 \
  --timeout 14400 \
  --allow-timer-frozen
```

监控脚本只读；它会在服务退出、watch 期间重启次数增长、failed batch、quarantine、DB 达到 `sessiond` 的实际拒绝阈值或 spool 达 85% 时非零退出。CPU 或 I/O 持续增长说明仍在工作；只有同时无 CPU、无 I/O、无批次变化并超过合理窗口，才进一步诊断。

每个小时完成后核对：

- `record_count = delivery_count + rejected_count`；
- `delivery_tokens_counted` 与可计数交付覆盖一致；
- archive backend、size、object、SHA 已落批次；
- 状态为 `purged`，表示 durable upload 与完整 read-back 门禁已通过。

独立 SHA 回读应继承 exporter 的 rclone binary/config/代理环境，但只能通过受控 systemd 环境运行；禁止 `cat` 或 `source` 环境文件，禁止把环境打印到日志。回读命令本身只输出 SHA 与对象名。

## timer 恢复与回滚

追赶结束后恢复并启用 timer，再观察一次自然触发：

- service `Result=success`、`ExecMainStatus=0`；
- batch 无 failed/exporting；
- timer active/waiting 且有下一触发；
- spool 回落、quarantine 为 0；
- DB 磁盘低于预警线。

最终监控不允许 timer 冻结：

```bash
python3 "$SKILL_DIR/scripts/session_production_status.py" \
  --watch --interval 60 --timeout 7200 --pending-target 100
```

发生以下任一情况执行回滚而不是继续热修：app/管理 UI 不可用、live SHA 不符、服务反复重启、sessiond 无法 ingest、validator 新增违例、Drive 回读不一致。回滚只恢复二进制和服务状态；已经 durable 验证并 purge 的批次不反向伪造，数据库状态需单独评估。
