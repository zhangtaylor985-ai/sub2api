## Why

当前 Sub2API 只能分别查看 API Key、私下客户订阅和上游账号，缺少统一的经营收入、直接成本、毛利、运营费用和净利润视图，也无法可靠保存历史月份。经营数据必须从临时 SQL 计算升级为可审计、可锁账、可解释的管理能力。

## What Changes

- 新增管理员专用“经营管理”菜单组，包含经营看板、成本管理和数据对账，并复用现有客户订阅入口。
- 以当前有效 API Key 和有效私下客户订阅为收入来源，支持四档车型价格、Key 级排除/覆盖以及客户订阅显式关联去重。
- 新增多币种成本台账和月度汇率，区分直接成本与运营费用，自动计算毛利、毛利率、净利润和净利率。
- 新增当前实时经营汇总，以及月末汇总快照和逐项明细；历史锁账后不因后续改名、续费、价格或汇率变化而改写。
- 新增历史月份图表与月份明细，支持查看每月有效客户、收入结构、成本结构和客户增减；明确不提供未来月份预测。
- 新增数据对账能力，提示无过期时间、未纳入经营统计、客户订阅与 API Key 到期不一致、疑似重复等异常。
- 支持上线前历史的估算回填或人工修正，并显式标记数据质量，禁止把估算显示为精确历史。

## Capabilities

### New Capabilities

- `business-profitability-dashboard`: 当前及历史月份的收入、毛利、净利、客户数量、结构图表与可解释明细。
- `business-cost-ledger`: 多币种、周期性或一次性成本的维护、分类、汇率折算和账号关联。
- `business-monthly-snapshots`: 月度经营汇总与逐项明细的生成、锁账、数据质量和历史稳定性。
- `business-data-reconciliation`: API Key、私下客户订阅、价格规则、排除规则和到期时间之间的异常检测与显式去重关联。

### Modified Capabilities

无。

## Impact

- 后端：新增 Ent schema、PostgreSQL migration、repository/service/admin handler、月度锁账任务及依赖注入。
- 前端：新增 admin 路由、侧边栏菜单、经营看板、成本管理、历史图表、月份明细和对账页面；复用 Vue 3、Tailwind CSS 与 Chart.js。
- 现有模块：读取 `api_keys`、`groups`、`accounts`、`private_customer_subscriptions` 的非敏感经营字段；不改变请求调度、鉴权或原始凭据存储。
- 运维：生产迁移需要数据库备份、已知价格/排除/成本/汇率初始化和可回滚发布；本 change 的实施阶段仅修改本地仓库。
