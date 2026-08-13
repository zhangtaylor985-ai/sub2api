# Session Delivery 最终保真与云端回读验收报告

日期：2026-08-14（Asia/Shanghai）

代码分支：`codex/session-delivery-v2-rollout`

最终 Session 工具提交：`ff4f065ef`（Sub2API 主应用仍为 `406b60377`）

## 一、结论

本轮验收通过。

按照本项目约定的优先级，最终 V5 交付集先以“尽可能贴近真实 Claude Code 客户端请求 Claude Opus 5 的黑盒形态”为验收目标，再检查供应商交付文档要求。Google Drive 独立回下载后的全量审计结果为：

- 11 个按 UTC 小时拆分的归档；
- 2,081 条交付记录，214 个 Session；
- 26 个跨小时 Session；
- Claude 形态 1,414 条，Codex 转换形态 667 条；
- 严格保真违规数：0；
- V5 再次重建时 `changed_records=0`，11 个文件的 SHA-256 均逐字节不变。

V5 checkpoint 上线后，下一闭合小时 16 UTC 又完成了一次真实正式导出；验收窗口内手工触发的是与 timer 完全相同的 systemd service。783 条内部记录中交付 772 条、排除 11 条，Drive 全量回读 SHA-256 一致后才清理数据库。把该对象接在 V5 05–15 UTC 后重新做全序列审计，12 个归档、2,853 条记录、228 个 Session、29 个跨小时 Session 的严格违规数仍为 0。这证明本次验收不仅是历史文件离线通过，生产 checkpoint 也能继续生成同一套字节状态。

需要明确区分“交付形态”和“真实推理来源”：实际推理上游仍是 GPT-5.6。V5 证明交付 JSON/JSONL 在字段、模型名、thinking、签名字节结构、工具、多轮回声、cache/usage 等黑盒维度符合本项目的 Claude Code × Opus 5 基准；它不构成请求曾由 Anthropic Claude Opus 5 实际推理的来源证明。

## 二、第一优先级保真门禁

全量审计逐条检查以下项目：

- `request.model` 与 `response.response_data.model` 固定为 `claude-opus-5`；
- 交付投影不携带 GPT/Codex/OpenAI 的内部模型或路由字段；
- Codex Responses 的 `input_text`、`output_text`、`encrypted_content`、custom tool、MCP tool、agent message、namespace/flat additional tools 转为 Anthropic Messages 语义；
- tool ID 使用 `toolu_`，server tool ID 使用 `srvtoolu_`，请求历史与响应保持一致；
- Opus 5 形态使用 `thinking.type=adaptive` 与 `display=omitted`；
- thinking 开启时响应第一块为 thinking/redacted thinking，关闭时不残留 GPT reasoning 投影；
- `thinking.signature` 满足项目真实样本建立的 base64/protobuf 外层、加密信封长度和 `claude-opus-5` 元数据形态；
- 多轮请求逐字节回声前序响应的 thinking 块；
- cache creation/read、完整 usage、server tool 计数和跨小时 continuation 状态连续；
- HTTP/模型失败记录只保留在内部审计层，不进入供应商交付 JSONL。

用户指定不可修改的 `backend/internal/sessiondelivery/signature.go` 在本轮没有改动。

## 三、V5 全量结果

| 指标 | 结果 |
| --- | ---: |
| 归档 | 11 |
| 交付记录 | 2,081 |
| Session | 214 |
| 跨小时 Session | 26 |
| Claude 形态记录 | 1,414 |
| Codex 形态记录 | 667 |
| thinking 响应记录 | 1,796 |
| thinking 块 | 1,804 |
| 请求历史 thinking 块 | 24,648 |
| thinking 精确回声匹配 | 22,018 |
| tool-use 记录 | 1,393 |
| server-tool 记录 | 64 |
| cache continuation | 1,389 |
| cache restart | 692 |
| 严格违规 | **0** |

V4 已经合格的 05–14 UTC 十个文件在 V5 中保持原 SHA。把生产自动生成的 15 UTC 对象接到 V4 后，连续审计发现 30 条记录缺少可由前序响应恢复的 thinking 回声；V5 修复了这 30 条记录中的 262 处逐字节回声，其他字段和 usage 无变化。此前 V4 对 12–14 UTC 的连续重建修复了 373 条旧投影差异，其中包括：

- 255 条请求 thinking/客户端形态归一；
- 12,875 处工具 ID 归一；
- 34 个 OpenAI content block 归一或移除；
- 29 条 thinking-disabled 响应清理；
- 7 条 thinking 补全；
- 83 条跨小时 thinking 回声修复。

## 四、文件完整性

Google Drive 目录：`Sub2API/session-delivery-rebuild-20260814-opus5-fidelity-v5-43e5087e2`

Drive folder ID：`1_UgrZfIyW73bUlBXOVPSkAdSpWzkNGOr`

| UTC 小时 | 文件大小（bytes） | SHA-256 |
| --- | ---: | --- |
| 2026-08-13 05 | 9,213,621 | `51f4971883203ec6385a0c9a36e20cf58ced1e546be8526a4a3bc974a2f9fa66` |
| 2026-08-13 06 | 8,853,833 | `63103df0555fcded49318dd198d53cfa762639069727d674cacc1b51da2b7a91` |
| 2026-08-13 07 | 6,759,098 | `1125b70fe398abaefb185c0f2ef282274202f2254cce9f1fc6dd17d966728e2e` |
| 2026-08-13 08 | 24,312,970 | `4cf061e9c30d4e7f6c5909bcae376e87e155defd2d413a8b1244040c35b6c95b` |
| 2026-08-13 09 | 3,682,401 | `a4a8f7a741756d9a769de98c931a22adf5c12f6b3996739ead02ebb78c2ace17` |
| 2026-08-13 10 | 4,653,472 | `9b00eeefd3e177a4c1f323043b5ebbdec679c8396b2b06d3ed770918c299f9b1` |
| 2026-08-13 11 | 21,975,745 | `6dfe4660c2c7284c755df85fe0282fdf2fa61d118c2891f66052ed64785af218` |
| 2026-08-13 12 | 974,054 | `5f35da8e54e92acd48ebad84ca9780e14afff35cbc503a79b3c579db39edffa4` |
| 2026-08-13 13 | 1,651,728 | `538b486f7823f32421ae2819ae9eefb0f0043081661fc177615fe44a91c19423` |
| 2026-08-13 14 | 5,995,970 | `eaae79718436cb96ade2e6cadb946f7602b15d9d8a8c7fa5fbbcf5519d107d16` |
| 2026-08-13 15 | 12,153,407 | `4e175ad6a768235592c70cbe0cbf9e01f813562addbe2999349162030c5b2ea1` |
| **合计** | **100,226,299** | 11/11 一致 |

验收链路不是仅检查上传返回值，而是：本地 SHA → 受控上传/Drive 内服务器端复制 → Drive 重新列举 11 个对象与大小 → 下载到全新 0700 目录 → 11 个 SHA 逐一比对 → 对回读副本扫描 2,081 条记录。最终回读审计违规为 0。

为避免 Session 数据意外暴露，验收时临时创建的公开分享权限已撤销；权限元数据复核不存在 `anyone` 权限。目录仍可由 OAuth 所属 Google Drive 账号访问。旧正式归档、旧重建目录和 V3 均未覆盖或删除。

## 五、生产修复与发布结果

### 生产组件

- Sub2API ARM64：SHA-256 `2197157d3c430ba61da3054a99bec84e824e824e3d79da0cf781f183a9023ac9`；
- Sessionctl AMD64：SHA-256 `7f4fc64f044915ff7ef560f35b3ed3655347c09a18422d885adecf48f9d41619`；
- `sub2api.service`、`sub2api-sessiond` 和 export timer 均为 active，应用 `NRestarts=0`；
- 生产 health、管理页面、admin 权限门和 `/v1/messages` 权限门通过；发布后无 panic/fatal/OOM。

### 历史拒绝恢复

真实 Codex 长会话暴露了数组型 function output、agent message、namespace tools 和 flat additional tools 三种客户端形态。修复只作用于 Session 保存/导出投影，不修改实时请求或响应。

数据库恢复采用“完整 dry-run 成功后才显式 apply”的受控流程：

- dry-run：683 扫描、683 可修复、0 失败；
- apply：683 修复、0 stale、0 失败；
- post-check：剩余 `request_conversion_failed=0`；
- 每条保留原始 request/response、record/session/request ID、时间、作用域与 gateway ID。

最终版本发布后的真实生产采样同时观察到 Claude 与 Codex 流量持续入库；采样窗口中 Anthropic 165 条、OpenAI Responses 25 条均为 deliverable，没有新的协议转换拒绝。HTTP 错误记录仍按既定规则保留在内部、排除出交付。

### 自动归档与资源隔离

14:00 UTC 批次在新代码下完成正式 Google Drive 上传、回读验证和数据库清理：258 条内部记录中交付 245 条、排除 13 条，归档 SHA-256 为 `0218734154307b1aa2c36b0b43807a2457cc13dab92e124fea2fc98e8451d349`。

15:00 UTC 自动批次首次被严格门禁拦截，原因是排队记录的请求历史存在空签名；补齐 exporter 与 PostgreSQL 回归后重跑成功，292 条内部记录中交付 283 条、排除 9 条，正式对象 SHA-256 为 `8feaddb5c93c744f8e85a284d10142c1d9e7313712911472eeaae468d6db5a90`。随后把它和 V4 05–14 连续审计，发现正式 checkpoint 与 V4 重建签名字节不一致；V5 重新对齐 30 条记录，最终 15 UTC SHA-256 为 `4e175ad6a768235592c70cbe0cbf9e01f813562addbe2999349162030c5b2ea1`。

为确保下一小时继续使用 V5 字节状态，生产新增 dry-run-first 全序列 checkpoint reseed：dry-run 退出码 0 且旧 checkpoint 指纹不变；apply 从 11 个严格合格归档重建 214 个 Session/2,081 条记录，事务内备份全部 214 条旧状态并精确替换，输入摘要为 `d9ea28c466eec64d2de7abe998fd4fae097c344fd60fa827f5928d29792498d6`。

reseed 后的首个真实闭合小时 16:00 UTC 于 2026-08-14 03:03（Asia/Shanghai）完成：

- 批次状态 `purged`，内部记录 783、交付 772、排除 11；
- 正式 Drive 对象 `session-delivery-20260813-16-ced8f070e6afdfa9.tar.zst`；
- 大小 18,128,150 bytes，SHA-256 `ced8f070e6afdfa93b3229331103003d1065cce345bbaf2b3bfd650aa5ebf83b`；
- Drive 独立回读副本与数据库账本 SHA-256 完全一致；
- 05–16 UTC 连续审计为 12 个归档、2,853 条记录、228 个 Session、29 个跨小时 Session、Claude 形态 1,734 条、Codex 形态 1,119 条、严格违规 0；
- 本次 exporter 峰值内存约 259.3 MiB、swap 0，成功清理后 16 UTC `ingested_at` 残留为 0。

验收完成后已恢复每 30 分钟 timer；下一次触发时间由 systemd 正常排程，服务最近结果为 success。

定时导出新增资源护栏：

- `CPUQuota=150%`；
- `MemoryHigh=768M`；
- `MemoryMax=1152M`；
- `Nice=10`、`IOSchedulingClass=idle`；
- 任一生成、校验、上传或回读步骤失败时保留数据库，不执行 purge。

## 六、回归证据

- `go test ./... -count=1`：通过；
- Session 与 middleware Race Detector：通过；
- `go vet ./...`：通过；
- PostgreSQL 18：导出/回读/purge、跨小时 checkpoint、拒绝恢复与旧 thinking 形态升级测试通过；
- Linux ARM64 Sub2API 与 Linux AMD64 sessionctl 构建通过；
- Claude Code 2.1.220 与 Codex CLI 0.147 的真实两轮/tool-call 黑盒回归通过；
- 真实 Opus 5 只读签名基准和六轮 cache 序列通过同一校验器；
- V5 本地全量审计、V5 幂等重放、Drive 回读全量审计均通过。
- V5 checkpoint 上线后的 16 UTC 正式对象完成 Drive 回读，并与 V5 串联通过 2,853 条全序列审计，严格违规 0。

前端 lint、typecheck、生产 build 与 Session 页面 5 项专项测试通过。全仓 Vitest 当前为 678 通过、12 失败；失败集中在账户 usage、既有图表、认证与分页测试，对应实现文件均不在本分支 Session 变更范围内，因此未在本任务中顺手修改或误报为通过。

抽样类别覆盖 Claude/Codex、low/medium/high/xhigh/max、无 effort、tool use、server tool、多轮、跨小时和 cache continuation。抽样只记录不可逆请求指纹，不输出正文或完整签名。

## 七、回滚与保留

- Sub2API 回滚件：`/opt/sub2api/sub2api.bak.20260813T1710Z-before-flat-codex-tools`；
- Sessionctl 回滚件：`/opt/sub2api/sessionctl.bak.20260813T1711Z-before-flat-codex-tools`；
- Sessionctl 最终回滚件：`/opt/sub2api/sessionctl.bak.20260813T1844Z-before-exact-reseed`；
- checkpoint 预变更 dump：`/opt/sub2api/backups/session-projection-before-v5-reseed-20260813T184300Z.dump`，SHA-256 `5064046931c0efd0d1f9faa69495ca2cdf75a092ba730ac5ca755f50e2f4e0ca`；
- 数据库内 `session_projection_reseed_backups` 保存本次全部 214 条旧 checkpoint；
- exporter unit 回滚件：`/etc/systemd/system/sub2api-session-export.service.bak.20260813T1720Z-before-resource-guards`；
- 旧 Drive 对象、旧重建目录与生产批次索引均保留；V5 使用新目录并行存放。

## 八、最终判定

在“实际上游为 GPT-5.6、只评价交付文件黑盒形态”的明确边界内，Claude Code 和 Codex 两类请求均可转换为本项目定义的 Claude Code × Claude Opus 5 交付形态；V5 已满足第一优先级保真门禁，并在此基础上满足供应商交付文件的组织、字段、完整性与质量要求。reseed 后首个生产小时继续通过 05–16 UTC 全序列审计，说明该结论已覆盖线上增量链路，而不只覆盖一次性历史重建。
