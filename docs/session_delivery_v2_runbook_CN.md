# Session Delivery V2 运行手册

## 1. 当前交付状态

V2 的代码、独立 migration、采集/转发进程、小时级导出、Google Drive rclone 后端和安全 purge 门禁均可在本地构建。默认配置全部关闭；当前没有连接生产 Session 数据库，也没有配置 Google Drive 或启用自动删除。

供应商规范快照见 [`vendor-delivery-spec-claude-20260811_CN.md`](vendor-delivery-spec-claude-20260811_CN.md)。输出是 Anthropic Messages 格式兼容投影，固定公开模型 `claude-opus-5`；真实上游仍是 GPT-5.6。规范化层按既有实现为缺失的 thinking 块生成格式同构的 `thinking.signature`，仅作用于交付记录，不修改客户端实时响应，也不构成 Claude 来源证明。

## 2. 推荐拓扑

```text
生产 Sub2API 主机
  sub2api.service -> 本机 0600 zstd spool
  session forwarder -> HTTPS/HMAC

隔离 Session 主机
  Caddy TLS + 来源 IP 白名单 -> loopback sessiond
  PostgreSQL 18 独立数据库（ingest-hour 分区）
  30-minute export timer -> Google Drive rclone remote
  read-back SHA-256 -> verified -> drop 单小时 partition
```

Google Drive 比在现生产机再挂一块数据盘更适合做异机归档，因为它和在线库、主机故障域分离；但它不适合承载在线 PostgreSQL，也不是不可删除的 WORM。在线 Session 库放独立机器的本地 SSD，Google Drive 只保存小时级 tar.zst。

当前生产根盘约 30 GB、可用约 11 GB，因此不能把在线 Session 库放回当前生产根盘。腾讯云隔离机 40 GB、当前可用约 33 GB；每 30 分钟归档上一闭合小时，75% 磁盘水位拒绝新写入，生产机 4 GB spool 负责故障缓冲。

## 3. 二进制与服务

- `sub2api`：请求结束后写本机 durable spool；不直连 Session PostgreSQL。
- `sessionctl forward`：把 pending zstd envelope 通过 HTTPS/HMAC 发给隔离机；成功或幂等重复后才删除本机 pending 文件。
- `sessionctl spool-status`：只读查看本机 spool 的字节水位、pending 和 quarantine 数量。
- `sessiond`：校验时间戳、body SHA-256 和 HMAC，执行独立 migration、幂等入库。
- `sessionctl export`：导出指定 ingest UTC hour，验证 JSONL/tar.zst，并上传/回读 archive backend。
- `sessionctl status`：查看某小时数据库数量与批次状态。
- `sessionctl validate`：离线验证本地 tar.zst。
- `sessionctl purge`：人工应急 purge；必须同时提供小时、verified SHA-256 和显式 allow。

systemd 模板：

- `deploy/sub2api-sessiond.service`
- `deploy/sub2api-session-forwarder.service`
- `deploy/sub2api-session-export.service`
- `deploy/sub2api-session-export.timer`

timer 每 30 分钟从最早未完成的闭合 UTC 小时开始，单次最多追赶 48 小时，空小时不生成归档。这样 Google Drive 或网络中断超过一小时后也会自动补齐。当 `SESSION_AUTO_PURGE_ENABLED=true` 时，只有 durable backend 回读验证成功才会删除同一 ingest-hour partition；local backend 会直接拒绝自动 purge。

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
- `session_records`（按 `ingested_at` UTC 小时分区）
- `session_export_batches`
- `session_schema_migrations`

## 6. Google Drive/rclone

代码不接管 Google OAuth token 生命周期，而是调用成熟的 rclone。后续拿到 Google Drive 存储对象后：

1. 在隔离机安装受控版本的 rclone；腾讯云无法直连 Google 时，通过现有 Xray 的加密 VLESS 出口访问，禁止把公网明文 SOCKS 作为传输链路。
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
HTTPS_PROXY=socks5://127.0.0.1:10880
HTTP_PROXY=socks5://127.0.0.1:10880
NO_PROXY=127.0.0.1,localhost
SESSION_AUTO_PURGE_ENABLED=false
```

腾讯归档机通过独立的 `xray-session-egress.service` 访问 Google Drive。该服务只监听
`127.0.0.1:10880`，exporter 通过上述代理变量使用它；不要把 SOCKS 端口暴露到公网。

V2 使用“UTC 小时 + 内容 SHA-256 前缀”对象名、`rclone copyto --immutable`，随后 `rclone cat` 全量回读计算 SHA-256 和 size。上传成功但无法回读、checksum 不同、后端名称不匹配时均不会 purge。即使进程在上传成功后退出，重跑也不会覆盖已有对象；内容不同的晚到增量会生成新的对象名。

## 7. 分阶段启用

### 阶段 A：只写 spool

在主服务配置：

```text
SESSION_DELIVERY_ENABLED=true
SESSION_DELIVERY_PUBLIC_MODEL=claude-opus-5
SESSION_DELIVERY_HMAC_SECRET=受保护随机值
SESSION_DELIVERY_SPOOL_DIR=/opt/sub2api/data/session-delivery/spool
SESSION_DELIVERY_SPOOL_MAX_BYTES=2147483648
SESSION_DELIVERY_CAPTURE_MAX_BYTES=268435456
```

观察 API 延迟、spool 增长、quarantine 和磁盘水位。采集异常只写日志，不改变客户端响应。

可用以下只读命令核对 spool 水位：

```bash
/opt/sub2api/sessionctl spool-status \
  -spool-dir /opt/sub2api/data/session-delivery/spool
```

### 阶段 B：启用隔离入库

在隔离机启动 `sub2api-sessiond.service`，保持 `sessiond` 只监听 loopback；使用 `deploy/Caddyfile.session-delivery.example` 配置 TLS 和生产主机来源 IP 白名单。在主机启动 forwarder。单通道使用 `SESSION_INGEST_ENDPOINT`；多条彼此独立的受限 SSH loopback 通道使用逗号分隔的 `SESSION_INGEST_ENDPOINTS`，并让 `SESSION_FORWARD_CONCURRENCY` 不超过通道数和接收端 `SESSION_INGEST_MAX_CONCURRENT`。

网络上传并发和解压并发必须分开控制。当前 2 GiB 内存隔离机可使用 `SESSION_INGEST_MAX_CONCURRENT=16` 接收慢速上传，同时设置 `SESSION_INGEST_MAX_DECODE_CONCURRENT=1`，避免多个 100 MiB 级压缩包同时完成后并发解压导致 OOM。

当 spool 达到 2 GiB 上限时，主应用跳过新的 Session 捕获但继续正常处理用户请求；forwarder 确认旧文件后，捕获会自动恢复。不得为释放空间手工删除 pending 文件。

核对 Claude HTTP/SSE、Codex HTTP/SSE 和 Codex WS 多轮记录数量。隔离机不可用时 pending 文件必须保留。

### 阶段 C：只导出，不清理

先使用 local backend：

```bash
/opt/sub2api/sessionctl export \
  -hour 2026-08-10T08 \
  -archive-backend local \
  -archive-dir /var/lib/sub2api/session-delivery/archive
```

local 批次只能到 `archived`，不能变为 `verified`。可执行：

```bash
/opt/sub2api/sessionctl validate -archive /var/lib/sub2api/session-delivery/archive/session-delivery-20260810-08-<sha256前16位>.tar.zst
/opt/sub2api/sessionctl status -hour 2026-08-10T08
```

切到真实 Google Drive 后，保持 `SESSION_AUTO_PURGE_ENABLED=false`，至少连续观察三个完整小时批次，并抽样从 Drive 下载后再跑 `validate`。

### 阶段 D：启用小时级清理

完成数量对账、恢复演练、Drive 权限/回收策略确认和磁盘告警后，单独把：

```text
SESSION_AUTO_PURGE_ENABLED=true
```

timer 每 30 分钟完成：冻结上一 ingest hour → 生成/严格验证交付包 → Google Drive immutable 上传 → 全量回读 checksum → 标记 verified → drop 该小时 payload partition。若进程在 verified 与 purge 之间退出，下次会先重新回读同一 Drive 对象，再恢复 purge。全局幂等 key 与批次水位是紧凑控制元数据，会继续保留；不会对整个数据库执行 TRUNCATE。

## 8. 故障处理

受限 SSH 中继由 `sub2api-session-tunnel-health.timer` 每分钟检查一次最早 16 个 pending 文件的窗口指纹。窗口连续 8 次没有任何文件被确认后，只重启 `sub2api-session-tunnel.service`，不会重启 Sub2API 或删除 spool 文件；中断中的上传继续按幂等键重试。这里不使用同一 SSH TCP 连接上的 HTTP health，因为大记录上传可能让 health 请求产生队头阻塞并导致误判。

中继内外两层 SSH 必须显式设置 `IPQoS=none`。生产实测默认 DSCP 会让跨境持久连接在传输若干大记录后停止推进；关闭 IPQoS 后，4 路并发恢复连续批量确认。

```bash
systemctl status sub2api-session-tunnel-health.timer --no-pager
journalctl -t sub2api-session-tunnel-health -n 50 --no-pager
```

- `spool full`：客户端请求仍完成，但新记录无法持久化；立即修复 forwarder/隔离机并扩容，不能直接删 pending/quarantine。
- `ingest_busy`/网络错误：forwarder 保留 pending，下一轮重试。
- `invalid_envelope`：记录移入 quarantine，不阻塞后续人工排查；不会进入外部交付包。
- export `failed`：该 ingest hour 重新变为可写，可修复后重跑；已生成但未提交的临时文件自动清理。
- batch `archived`：仅 local/non-durable，禁止 purge；切换 rclone 后可重导。
- batch `verified`：数据冻结；自动任务在再次回读成功后可恢复 purge。
- batch `purged`：对应 partition 已删除，交付包是权威恢复源；不要手工重建同名 partition。

任何人工 purge 前先运行 `status`，从批次输出复制完整 SHA-256，并确认归档对象可下载和离线 `validate`。不要对 Session 库执行全库 `TRUNCATE`，也不要让任何命令连接 Sub2API 主库 DSN。
