---
name: sub2api-session-delivery-ops
description: 用于 Sub2API Session V2 的生产发布、双机二进制部署、exporter 积压恢复、spool 与磁盘监控、timer 恢复、Drive SHA 回读和上线验收。用户提到 Session 代码上线、sessionctl/sessiond 发布、小时归档失败、数据库阻塞、spool 堆积、timer/exporter、生产回滚或 Session 生产验收时使用。历史 Drive 文件离线重建改用 sub2api-session-history-rebuild。
---

# Sub2API Session 生产发布

## 先确定任务边界

读取项目 `AGENTS.md`、当前 Git 状态和 [references/production-rollout.md](references/production-rollout.md)。生产写操作必须已有用户明确授权；本 Skill 自带脚本只负责固定提交构建和只读观测，不会替换远端二进制、重启服务、恢复 timer、修改数据库或清理文件。

历史归档重建、幂等审计和版本化上传使用 `$sub2api-session-history-rebuild`，不要和实时小时追赶混在一次命令中。

## 标准流程

1. 用 `$sub2api-production-inspection` 确认当前生产拓扑、服务和磁盘；不要凭旧记录选择主机。
2. 用 `$sub2api-production-regression` 完成 Session 专项、全包、race、vet、PostgreSQL integration 和必要黑盒回归。保真改动只能进入 `backend/internal/sessiondelivery`，不得顺带改变实时响应。
3. 固定并推送一个已提交 commit，先 `git fetch origin`；构建脚本会拒绝不在已 fetch `origin/*` refs 中的 commit。确认本地不落后远端后，运行 `scripts/build_session_release.sh` 从 Git archive 构建前端 embed 的 Linux ARM64 app、两架构 `sessionctl` 和 Linux AMD64 `sessiond`，保存 manifest 与 SHA。
4. 运行 `scripts/session_production_status.py --json` 获取同一时间点的双机只读快照。Exporter 运行时禁止对父表做精确 `session_records` count/size 查询。
5. 按参考文档做 timer 冻结、回滚件、远端 staging、SHA 校验和原子替换。主应用使用 `$sub2api-local-binary-deploy` 或 `$sub2api-deploy`；Session DB 二进制只替换实际发生改动的组件。
6. 发布后先验证 app/forwarder/tunnel/sessiond、健康接口、管理 UI、mode/owner、binary SHA 和 `NRestarts`，再启动最早失败小时追赶。
7. 用 `scripts/session_production_status.py --watch` 有界观察批次、spool、quarantine、磁盘与服务；异常时保留原始记录并停止扩散，不跳过 validator、不提前 purge。
8. 每个完成小时核对批次计数、Token 覆盖、Drive 对象大小和数据库 SHA；再从 exporter 的 systemd 环境独立执行 `rclone cat | sha256sum`，但绝不打印环境文件或 OAuth 配置。
9. 追赶完成后恢复 timer，并观察一次自然触发成功。最终快照必须无 failed/exporting 批次、quarantine 为 0、spool 回到安全水位。

## 必须保留的门禁

- App 必须用 `-tags=embed` 构建；缺少管理 UI 即发布失败，不接受“API 健康所以可继续”。
- 生产 spool 路径固定核对 `/opt/sub2api/data/session-delivery/spool`；容量上限必须读取 forwarder 进程的实际环境值，读取不到即 fail-closed，不用 CLI 默认值猜测。
- 隔离机 `sessiond` 的磁盘拒绝阈值必须从实际 systemd 参数或运行进程环境读取；读取不到即 fail-closed。当前基线是 75%，70% 开始预警。
- 单次快照把非零累计 `NRestarts` 标为警告；watch 期间任何服务重启次数增长都立即失败，不能只看当前 active。
- 2GB DB 主机不盲目提高解码并发；先靠小时 purge 释放空间。
- 长时间 exporter 需同时看 CPU、I/O、批次状态与锁等待，不能只凭耗时判定卡死。
- Drive durable read-back 成功前不得 purge；独立回读 SHA 成功前不得宣布交付完成。
- 不回显 DSN、API Key、HMAC、OAuth/rclone token、私钥或 Session payload。

## 完成证据

最终报告至少包含：固定 commit、四个构建产物 SHA、远端替换前后 SHA、回滚件路径、回归结果、服务与 restart 状态、失败/追赶批次、spool 水位、quarantine、双机磁盘、Drive 独立回读 SHA、timer 下一触发和自然触发结果。

## 资源

- `scripts/build_session_release.sh`：从固定提交生成不可覆盖的生产 release 与 SHA manifest。
- `scripts/session_production_status.py`：双机只读一次快照或有界追赶监控。
- `references/production-rollout.md`：生产发布、回滚和验收的精确阶段门禁。
