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

### Requirement: 客户订阅与 API Key 显式关联去重
系统 SHALL 允许一个 API Key 显式关联一个私下客户订阅；关联有效时 MUST 只计一次收入，并以客户订阅金额和到期日为准。

#### Scenario: 关联后避免双算
- **WHEN** 有效 API Key 与有效私下客户订阅建立关联
- **THEN** 总收入只包含一条客户订阅收入，API Key 基础价格不再额外计入

#### Scenario: 未关联订阅独立计入
- **WHEN** 有效私下客户订阅没有关联任何 API Key
- **THEN** 系统将其作为独立客户订阅收入计入

### Requirement: 不自动按名称合并
系统 MUST NOT 仅凭名称自动创建 API Key 与客户订阅关联，但 MAY 提供疑似同名建议供管理员确认。

#### Scenario: 同名记录出现
- **WHEN** 系统发现 API Key 与客户订阅名称相同但没有显式关联
- **THEN** 系统报告疑似重复且继续按未关联规则计算，直到管理员确认

### Requirement: 经营异常检测
系统 SHALL 检测并分类报告无过期时间、已过期但状态 active、缺少价格规则、客户订阅与 Key 到期不一致、未关联同名和历史快照缺口。

#### Scenario: 无过期时间异常
- **WHEN** API Key 状态 active 且 `expires_at` 为空
- **THEN** 对账接口返回该 Key ID、名称、分组和异常类型，不返回原始 Key

#### Scenario: 到期日不一致
- **WHEN** 已关联客户订阅与 API Key 的到期日不是同一北京时间日历日期
- **THEN** 对账接口报告两边日期和差异类型

### Requirement: 初始价格和排除配置
系统 SHALL 支持幂等初始化独享车 ¥1460、2人车 ¥730、3人车 ¥485、4人车 ¥365，以及用户确认的6个排除 Key。

#### Scenario: 初始化价格规则
- **WHEN** 管理员首次执行经营配置初始化
- **THEN** 系统创建四档价格和当前排除配置，且后续重复执行不产生重复记录
