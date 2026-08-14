# 线上 Session 交付运维

## 目录

1. 确认拓扑
2. 只读诊断
3. 小时归档恢复
4. 部署边界
5. 验收清单

## 确认拓扑

每次先读取项目 `AGENTS.md`，不要凭历史记忆选择主机。当前拓扑通常为：

- Sub2API 应用：Oracle ARM64 生产机，`sub2api.service` 与独立 spool forwarder。
- Session DB/归档：40GB 隔离机，PostgreSQL、`sub2api-sessiond.service`、`sub2api-session-export.timer`。
- Google Drive：rclone durable backend，正式增量目录与历史 rebuild 目录分离。

所有主机、SSH key、部署路径和 systemd unit 以 `AGENTS.md` 最新值为准。

## 只读诊断

先收集同一时间点证据：

- 应用、forwarder、sessiond、export timer/service 的 active/enabled 状态与 restart 次数。
- 应用 `/health`、管理后台静态资源、最近错误日志。
- 应用 spool 的 pending/quarantine/实际目录大小与最近写入时间。
- Session DB 未归档记录数、最早/最晚 `ingested_at`、failed/exporting batch、最近验证批次。
- DB 主机与应用主机磁盘、内存、swap、load。
- Drive 正式目录最近对象、大小和对象名小时连续性。

查询数据库时只读取非敏感字段，不回显 DSN。先把原始记录保留情况和失败发生在上传前还是上传后说清楚。

## 小时归档恢复

1. 保持 timer 停止或确认 exporter 不并发运行。
2. 修复只落在 Session 保存/导出链路；运行 Session 单测、race、全后端测试、vet、PostgreSQL 集成测试。
3. 先手动处理最早失败小时。严格 validator 失败时不得跳过、不得 purge。
4. 每个成功小时记录：internal/delivery/rejected 数、archive bytes、Token、对象名、SHA-256。
5. exporter 自带 durable read-back 后，再用独立 `rclone cat | sha256sum` 回读抽检。
6. 只有数据库 batch 为 verified/purged 且 Drive 字节匹配，才追赶下一小时。
7. 追赶完成后恢复 `sub2api-session-export.timer`，确认 active/enabled、下一次触发时间，并观察一次 `processed: 0` 的健康空跑或真实小时成功归档。

不要把 timer 的实时小时补跑与“历史版本化重建”混为一谈。用户暂停历史重建时，正常新会话小时归档仍应运行。

## 部署边界

- 使用 `$sub2api-production-regression` 完成上线前门禁。
- 使用 `$sub2api-local-binary-deploy` 做临时固定二进制发布，或使用 `$sub2api-deploy` 走正式发布链路。
- 固定 commit、目标架构、构建标签和二进制 SHA；server 必须保留前端 embed 标签。
- 发布前保存可执行回滚件，发布后检查 mode/owner、systemd 状态、NRestarts、health 与管理 UI 路由。
- `sessionctl` 与 app 可来自不同构建，但必须记录各自 commit/SHA；不要默认认为二者一致。
- 不要修改实时响应来修复交付文件，也不要改动用户锁定的签名实现。

## 验收清单

- 新记录持续进入隔离 DB，spool pending 可排空且 quarantine 为 0。
- timer active/enabled，有明确 next trigger；最近 exporter 退出码为 0。
- failed/exporting batch 为 0，所有可结算小时均有 verified/purged 审计链。
- Drive 回读 SHA 与数据库记录一致。
- 管理后台显示 DB/磁盘/服务状态、记录数、归档数、上传大小、精确 Token 与历史覆盖率。
- 明确报告不可恢复的 capture gap，不用 usage metadata 冒充原始 Session。
- 历史重建若被用户延期，记录“未上传、未 reseed、未 backfill”，同时保持实时归档运行。
