## Context

Sub2API 当前已有 API Key、分组、上游账号和私下客户订阅管理，但这些数据分散在不同领域。API Key 只有当前状态与当前到期时间，私下客户订阅更新续费日期时也会覆盖旧值；因此直接查询实时表既不能稳定重放历史，也无法解释某个月的收入组成。现有管理后台基于 Vue 3、Tailwind CSS 和 Chart.js，后端使用 Go、Ent 与 PostgreSQL，适合在现有 admin-only 架构中增加独立经营域。

经营口径已经确定：不做未来预测；当前收入由有效月租 API Key、流量包发放收入与有效私下客户订阅组成；流量包每 USD 100 额度售价 CNY 60；两项菲律宾订阅账号各为 USD 150/月；域名为 USD 30/年，服务器与代理免费。当前 USD/CNY 使用 ECB 工作日参考汇率自动更新，失败时保留最近成功值，首次无数据才回退 6.75；历史月份使用各自锁定汇率。私下客户订阅独立计入，不要求 API Key。

## Goals / Non-Goals

**Goals:**

- 提供当前实时经营收入、直接成本、毛利、运营费用、净利润及对应利润率。
- 提供按月稳定、可审计、可下钻的历史经营快照。
- 让价格、Key 排除/覆盖、流量包销售、成本和汇率成为结构化数据，而不是临时 SQL 常量。
- 明确区分实时、已锁账、估算和人工修正数据，避免虚假精度。
- 在现有管理后台中提供高密度但易读的“精密经营账本”体验。

**Non-Goals:**

- 不预测未来收入、续费概率或未来利润。
- 不实现完整会计总账、发票、税务、应收应付或银行对账。
- 不根据名字自动合并客户，也不读取或展示原始 API Key/OAuth 凭据。
- 不在本 change 中部署生产或写入生产初始数据。

## Decisions

### 1. 采用独立经营域，不改写请求调度与支付域

新增经营相关表、repository、service 和 admin handler，只读取 `api_keys`、`groups`、`users`、`accounts`、`private_customer_subscriptions` 的非敏感字段。这样不会把经营配置放入鉴权热路径，也不会污染现有 `user_subscriptions` 支付域。

备选方案是直接扩展现有 dashboard 聚合，但它缺少经营配置与历史明细，且缓存/usage 语义与月度经营不同，因此不采用。

### 2. 收入解析使用“月租规则 + 流量包发放 + 独立订阅”的组合

API Key 的基础价格由启用的 `business_pricing_rules` 按 `group_id` 解析；`business_api_key_configs` 可排除某个 Key或覆盖金额。解析顺序为：

1. Key 配置为排除时不计收入。
2. `token_package_required=true` 的 Key 不使用车型月价；流量包按发放月份以“USD 100 额度 = CNY 60”确认销售收入，有余额时计为有效客户。
3. 月租 Key 有金额覆盖时使用覆盖值。
4. 其他月租 Key 使用分组价格规则。
5. 所有有效私下客户订阅作为独立收入计入，不要求或推断 API Key 关联。

不按名称自动合并客户记录。

### 3. 当前实时与历史快照采用不同读取路径

当前月份每次查询都从实时来源和当前经营配置计算。历史月份只读取已保存快照，不回查当前 API Key/订阅/价格。这样续费、改名或修改汇率不会重写过去。

历史使用两层表：

- `business_monthly_snapshots`：每月一条汇总、状态、数据质量、汇率和利润指标。
- `business_monthly_snapshot_items`：快照内每个收入/成本项目的名称、来源、类别、金额、币种、折算金额、到期日和计入原因。

只有明细才能支持图表悬停、月份下钻与审计；仅保存汇总 JSON 不利于查询、约束和测试，因此不采用。

### 4. 月末锁账采用北京时间与幂等生成

经营月份按 `Asia/Shanghai` 自然月计算。后台任务在每月1日生成并锁定上月快照，数据库对月份设置唯一约束；重复执行必须返回同一结果而非新增重复记录。管理员可以在锁账前重算当前月，也可以创建“估算/人工修正”历史，但已锁账记录默认不可变。

上线前历史无法从覆盖式到期字段精确重建。回填时必须标记 `estimated` 或 `manual`；从功能上线后的自动月末快照才标记 `actual`。

### 5. 成本定义与月度换算分离

`business_cost_items` 保存成本定义：直接成本/运营费用、类别、原币金额、币种、周期、起止日期、关联账号和免费标记。`business_exchange_rates` 以月份和币种唯一，保存折算到 CNY 的精确 decimal 汇率。

当前或锁账月份计算成本时，将原币金额与所用汇率一起复制进快照明细。历史不跟随汇率配置变化。年度成本按 12 个经营月等额摊销；只有实际付费项目进入成本台账，免费服务器、代理和订阅账号不生成零金额噪音。

### 6. 利润公式保持清晰且可审计

- 总收入 = 月租 API Key 收入 + 流量包销售收入 + 独立客户订阅收入。
- 毛利 = 总收入 - 直接成本。
- 净利润 = 毛利 - 运营费用。
- 利润率在收入为零时返回 0，避免除零或无穷值。

所有 CNY 金额存储为分（int64）；外币原始金额使用最小单位，汇率使用精确 decimal。前端只负责格式化，不自行重新计算权威汇总。

### 7. API 采用聚合读接口和窄范围 CRUD

新增 admin-only 路由：

- `GET /api/v1/admin/business/dashboard/current`
- `GET /api/v1/admin/business/dashboard/history`
- `GET /api/v1/admin/business/dashboard/months/:month`
- `GET/POST/PUT/DELETE /api/v1/admin/business/costs`
- `GET/PUT /api/v1/admin/business/exchange-rates/:month`
- `GET/POST/PUT /api/v1/admin/business/pricing-rules`
- `GET/PUT /api/v1/admin/business/api-key-configs/:api_key_id`
- `GET /api/v1/admin/business/reconciliation`
- `POST /api/v1/admin/business/snapshots/:month/close`

所有写入都做服务层校验和事务处理。普通用户路由不暴露经营数据。

### 8. 前端使用现有依赖实现“精密经营账本”

侧边栏新增可展开“经营管理”：经营看板、客户订阅、成本管理、数据对账。主看板采用海军蓝/账本纸色基底、翡翠绿利润、琥珀预警和朱红异常，金额使用 tabular numerals。Chart.js 展示历史收入/成本柱与毛利/净利折线；只渲染已锁账月份和当前月份，不构造未来月份。

月份 Tooltip 展示汇总，点击后打开明细抽屉。所有图表同时提供文本摘要、键盘可达入口和空状态，兼容现有亮暗主题及响应式布局。

## Risks / Trade-offs

- [上线前历史缺少完整续费事件] → 只允许估算/人工回填并显式展示数据质量；不声称为精确历史。
- [订阅管理用户没有 API Key] → 客户订阅始终独立计入，不把缺少关联当成异常。
- [月末任务失败会缺少历史] → 使用幂等手动 close API、任务错误日志和缺口提示，可安全补跑。
- [价格或成本配置错误会影响当前汇总] → 当前页展示数据来源和异常，对历史只在锁账时复制；写入做范围校验。
- [成本周期规则复杂] → 第一版只支持 monthly、yearly、one_time；不实现任意 cron。
- [Ent 生成代码与迁移改动较大] → 采用项目既有 schema/generate/migration 流程，定向与全量测试后再发布。
- [经营菜单增加后台密度] → 使用折叠菜单组、默认聚焦核心指标，详细配置放独立页面。

## Migration Plan

1. 本地新增 Ent schema、生成代码和可回滚 PostgreSQL migration。
2. 实现 service/handler/job 与前端页面，通过本地迁移、Go、前端和浏览器验收。
3. 生产发布前创建完整 PostgreSQL dump，并记录当前 binary 与环境回滚点。
4. 应用 migration 后初始化四档价格、6个排除 Key、2个 USD 150/月付费账号和 USD 30/年域名；当前月汇率由 ECB 同步。
5. 对账当前 API Key、流量包与客户订阅后启用月末快照；不上线未来预测数据，也不在证据不足时伪造历史。
6. 回滚时恢复旧 binary；新增表不影响请求路径，可保留数据。若必须完全回滚 schema，使用备份恢复而不是手工删除生产表。

## Open Questions

无阻塞问题。新增实际付费成本由管理员在成本菜单补录；服务器和代理当前免费，域名按 USD 30/年记录并月度摊销。
