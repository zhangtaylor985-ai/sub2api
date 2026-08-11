# Session Delivery V2 运行手册

## 1. 当前交付状态

V2 的代码、独立 migration、采集/转发进程、每日导出、Google Drive rclone 后端和安全 purge 门禁均可在本地构建。默认配置全部关闭；当前没有连接生产 Session 数据库，也没有配置 Google Drive 或启用自动删除。

供应商规范快照见 [`vendor-delivery-spec-claude-20260811_CN.md`](vendor-delivery-spec-claude-20260811_CN.md)。输出是 Anthropic Messages 格式兼容投影，固定公开模型 `claude-opus-5`；真实上游仍是 GPT-5.6，V2 不生成 Claude 来源证明或伪造 thinking signature。

## 2. 推荐拓扑

```text
生产 Sub2API 主机
  sub2api.service -> 本机 0600 zstd spool
  session forwarder -> HTTPS/HMAC

隔离 Session 主机
  Caddy TLS + 来源 IP 白名单 -> loopback sessiond
  PostgreSQL 18 独立数据库（ingest-day 分区）
  daily export timer -> Google Drive/Shared Drive rclone remote
  read-back SHA-256 -> verified -> drop 单日 partition
```

Google Drive 比在现生产机再挂一块数据盘更适合做异机归档，因为它和在线库、主机故障域分离；但它不适合承载在线 PostgreSQL，也不是不可删除的 WORM。建议在线 Session 库仍放独立机器的本地 SSD，Google Drive 只保存每日 tar.zst。

当前生产根盘约 30 GB、可用约 12 GB，而历史原始 Session 约 200 GB/两周，因此不能把在线 Session 库放回当前生产根盘。独立主机容量应按压缩实测和故障缓冲确定；在没有实测前，建议至少保留 3–7 天未导出数据、WAL、临时导出和 30% 空闲水位。

## 3. 二进制与服务

- `sub2api`：请求结束后写本机 durable spool；不直连 Session PostgreSQL。
- `sessionctl forward`：把 pending zstd envelope 通过 HTTPS/HMAC 发给隔离机；成功或幂等重复后才删除本机 pending 文件。
- `sessionctl spool-status`：只读查看本机 spool 的字节水位、pending 和 quarantine 数量。
- `sessiond`：校验时间戳、body SHA-256 和 HMAC，执行独立 migration、幂等入库。
- `sessionctl export`：导出指定 ingest UTC day，验证 JSONL/tar.zst，并上传/回读 archive backend。
- `sessionctl status`：查看某日数据库数量与批次状态。
- `sessionctl validate`：离线验证本地 tar.zst。
- `sessionctl purge`：人工应急 purge；必须同时提供日期、verified SHA-256 和显式 allow。

systemd 模板：

- `deploy/sub2api-sessiond.service`
- `deploy/sub2api-session-forwarder.service`
- `deploy/sub2api-session-export.service`
- `deploy/sub2api-session-export.timer`

timer 默认北京时间 09:30 导出“上一 UTC 自然日”。当 `SESSION_AUTO_PURGE_ENABLED=true` 时，只有 durable backend 回读验证成功才会删除同一 ingest-day partition；local backend 会直接拒绝自动 purge。

## 4. 密钥与权限

必须生成两个不同的随机 secret：

- `SESSION_DELIVERY_HMAC_SECRET`：只在 Sub2API 主机使用，用于派生公开 Session/Request/Message ID。
- `SESSION_INGEST_SECRET`：主机与隔离机共享，只用于 ingest transport HMAC。

两者至少 32 bytes，不得写入 Git、运行手册、命令历史或日志。环境文件建议：

```text
/etc/sub2api/sub2api.env             root:sub2api 0640
/etc/sub2api/session-delivery.env    root:sub2api 0640
/etc/sub2api/rclone.conf             root:sub2api 0640
```

spool、ingest temp、export temp 和 local archive 目录使用 `sub2api:sub2api`，目录权限 `0700`；文件由程序以 `0600` 创建。

## 5. 首次数据库初始化

在隔离 Session 主机配置独立 DSN 后执行：

```bash
SESSION_DATABASE_DSN='从受保护环境注入' /opt/sub2api/sessionctl migrate
```

不要把 Session 表迁入 Sub2API 主库。migration 会创建：

- `session_record_keys`
- `session_records`（按 `ingested_at` UTC 日分区）
- `session_export_batches`
- `session_schema_migrations`

## 6. Google Drive/rclone

代码不接管 Google OAuth token 生命周期，而是调用成熟的 rclone。后续拿到 Google Drive 存储对象后：

1. 在隔离机安装受控版本的 rclone。
2. 使用专用服务账号或专用 Google 账号创建 remote；优先 Shared Drive 和最小目录权限。
3. 把配置放到 `/etc/sub2api/rclone.conf`，通过 `RCLONE_CONFIG` 指定。
4. 配置目标，例如 `gdrive:Sub2API/session-delivery`；如需要客户端侧加密，可改用 rclone `crypt` remote。
5. 先用专用测试目录执行写入、`cat` 回读、重复 immutable 上传和恢复演练。

生产相关环境变量：

```text
SESSION_ARCHIVE_BACKEND=rclone
SESSION_ARCHIVE_RCLONE_BINARY=/usr/bin/rclone
SESSION_ARCHIVE_RCLONE_REMOTE=gdrive:Sub2API/session-delivery
RCLONE_CONFIG=/etc/sub2api/rclone.conf
SESSION_AUTO_PURGE_ENABLED=false
```

V2 使用“日期 + 内容 SHA-256 前缀”对象名、`rclone copyto --immutable`，随后 `rclone cat` 全量回读计算 SHA-256 和 size。上传成功但无法回读、checksum 不同、后端名称不匹配时均不会 purge。即使进程在上传成功后退出，重跑也不会覆盖已有对象；内容不同的晚到增量会生成新的对象名。

## 7. 分阶段启用

### 阶段 A：只写 spool

在主服务配置：

```text
SESSION_DELIVERY_ENABLED=true
SESSION_DELIVERY_PUBLIC_MODEL=claude-opus-5
SESSION_DELIVERY_HMAC_SECRET=受保护随机值
SESSION_DELIVERY_SPOOL_DIR=/opt/sub2api/data/session-delivery/spool
SESSION_DELIVERY_SPOOL_MAX_BYTES=4294967296
SESSION_DELIVERY_CAPTURE_MAX_BYTES=268435456
```

观察 API 延迟、spool 增长、quarantine 和磁盘水位。采集异常只写日志，不改变客户端响应。

可用以下只读命令核对 spool 水位：

```bash
/opt/sub2api/sessionctl spool-status \
  -spool-dir /opt/sub2api/data/session-delivery/spool
```

### 阶段 B：启用隔离入库

在隔离机启动 `sub2api-sessiond.service`，保持 `sessiond` 只监听 loopback；使用 `deploy/Caddyfile.session-delivery.example` 配置 TLS 和生产主机来源 IP 白名单。在主机启动 forwarder，`SESSION_INGEST_ENDPOINT` 必须使用 HTTPS。

核对 Claude HTTP/SSE、Codex HTTP/SSE 和 Codex WS 多轮记录数量。隔离机不可用时 pending 文件必须保留。

### 阶段 C：只导出，不清理

先使用 local backend：

```bash
/opt/sub2api/sessionctl export \
  -day 2026-08-10 \
  -archive-backend local \
  -archive-dir /var/lib/sub2api/session-delivery/archive
```

local 批次只能到 `archived`，不能变为 `verified`。可执行：

```bash
/opt/sub2api/sessionctl validate -archive /var/lib/sub2api/session-delivery/archive/session-delivery-20260810-<sha256前16位>.tar.zst
/opt/sub2api/sessionctl status -day 2026-08-10
```

切到真实 Google Drive 后，保持 `SESSION_AUTO_PURGE_ENABLED=false`，至少连续观察三个完整批次，并抽样从 Drive 下载后再跑 `validate`。

### 阶段 D：启用每日清理

完成数量对账、恢复演练、Drive 权限/回收策略确认和磁盘告警后，单独把：

```text
SESSION_AUTO_PURGE_ENABLED=true
```

timer 每天完成：冻结上一 ingest day → 生成/严格验证交付包 → Google Drive immutable 上传 → 全量回读 checksum → 标记 verified → drop 该日 payload partition。若进程在 verified 与 purge 之间退出，下次会先重新回读同一 Drive 对象，再恢复 purge。全局幂等 key 与批次水位是紧凑控制元数据，会继续保留；不会对整个数据库执行 TRUNCATE。

## 8. 故障处理

- `spool full`：客户端请求仍完成，但新记录无法持久化；立即修复 forwarder/隔离机并扩容，不能直接删 pending/quarantine。
- `ingest_busy`/网络错误：forwarder 保留 pending，下一轮重试。
- `invalid_envelope`：记录移入 quarantine，不阻塞后续人工排查；不会进入外部交付包。
- export `failed`：该 ingest day 重新变为可写，可修复后重跑；已生成但未提交的临时文件自动清理。
- batch `archived`：仅 local/non-durable，禁止 purge；切换 rclone 后可重导。
- batch `verified`：数据冻结；自动任务在再次回读成功后可恢复 purge。
- batch `purged`：对应 partition 已删除，交付包是权威恢复源；不要手工重建同名 partition。

任何人工 purge 前先运行 `status`，从批次输出复制完整 SHA-256，并确认归档对象可下载和离线 `validate`。不要对 Session 库执行全库 `TRUNCATE`，也不要让任何命令连接 Sub2API 主库 DSN。
