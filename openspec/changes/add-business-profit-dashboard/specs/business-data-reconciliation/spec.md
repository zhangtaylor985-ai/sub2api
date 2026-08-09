## ADDED Requirements

### Requirement: 分组价格规则
系统 SHALL 允许管理员将 API Key 分组映射到经营车型与月价格，并支持启用、停用和更新规则。

#### Scenario: 解析四人车价格
- **WHEN** 有效 API Key 所属分组命中启用的四人车规则
- **THEN** 系统使用该规则的 CNY 月价格计算收入

#### Scenario: 缺少价格规则
- **WHEN** 有效 API Key 的分组没有启用价格规则
- **THEN** 系统不猜测价格，并在对账异常中报告该 Key

### Requirement: Key 级排除与金额覆盖
系统 SHALL 允许管理员按 API Key ID 排除收入、覆盖月金额或恢复默认分组价格；名称变化不得使配置失效。

#### Scenario: 排除指定 Key
- **WHEN** Key 配置标记为 excluded
- **THEN** 系统不计该 Key 收入并在明细中保留排除原因

#### Scenario: Key 改名后仍排除
- **WHEN** 已排除 Key 的名称发生变化但 ID 不变
- **THEN** 排除配置继续生效

### Requirement: 客户订阅独立核算
系统 MUST 将有效私下客户订阅作为独立收入来源，不要求、推断或校验其存在 API Key。

#### Scenario: 订阅用户没有 API Key
- **WHEN** 一个有效私下客户订阅没有任何 API Key
- **THEN** 系统正常计入客户订阅收入且不产生缺少关联异常

#### Scenario: 旧关联字段不影响口径
- **WHEN** 兼容字段中仍保存历史 API Key 关联值
- **THEN** 系统仍按独立客户订阅收入计算，不用关联替换月租收入

### Requirement: 不自动按名称合并
系统 MUST NOT 仅凭名称合并 API Key 与客户订阅，也 MUST NOT 将同名本身报告为阻断项。

#### Scenario: 同名记录出现
- **WHEN** 系统发现 API Key 与客户订阅名称相同
- **THEN** 系统保持两类来源各自的既定经营口径，不自动改写收入

### Requirement: 经营异常检测
系统 SHALL 检测并分类报告无过期时间、缺少价格规则、缺少汇率、无有效付费成本和历史快照缺口。已过期但状态 active 是正常的正交状态组合，MUST NOT 作为异常或阻断项。

#### Scenario: 无过期时间异常
- **WHEN** API Key 状态 active 且 `expires_at` 为空
- **THEN** 对账接口返回该 Key ID、名称、分组和异常类型，不返回原始 Key

#### Scenario: 过期但仍 active
- **WHEN** API Key 已超过到期时间但管理状态仍为 active
- **THEN** 经营计算忽略该 Key，且对账接口不生成 `expired_active` 异常

### Requirement: 初始价格和排除配置
系统 SHALL 支持幂等初始化独享车 ¥1460、2人车 ¥730、3人车 ¥485、4人车 ¥365，以及用户确认的6个排除 Key。

#### Scenario: 初始化价格规则
- **WHEN** 管理员首次执行经营配置初始化
- **THEN** 系统创建四档价格和当前排除配置，且后续重复执行不产生重复记录
