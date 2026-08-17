# 企业支付主体分派 C2：执行计划

状态：ACTIVE（保持既有支付流程）

负责人：Codex
更新时间：2026-08-17

## 目标与非目标

按订单创建时的计费主体快照分派全部成功支付回调。首版不公开企业支付入口、不销售企业订阅、不启用企业关系。

## 现状证据

现有五种充值和四种订阅支付回调都落入个人钱包或个人订阅；C1 已以 `d94426c56` 整合订单快照和企业账本内核，但尚未接入任一支付回调。

## 修改范围

在既有 `model/topup.go`、`model/subscription.go` 和相关确定性测试中实现；controller 继续承担既有验签与支付商协议，模型层不按成员关系分派资金。

## 风险与回滚

支付回调、金额、订单创建顺序和日志均为高风险。无验证或用户决策不得改写支付商协议。回滚只停止未整合分支；已产生账务仅可冲正，不回写历史。

## 实施步骤

1. 保持现有支付商下单、跳转、回调、验签和订单状态流；只复用已存在的订单主体快照。
2. 以订单锁和主体快照实现统一结算，企业路径调用同事务账本核心。
3. 补充重放、并发、快照、拒绝企业订阅和失败回滚测试。
4. 在隔离 PostgreSQL UAT 验证后才考虑发布门。

### 2026-08-17 审计补充：个人钱包上限与支付并发幂等

- P1-1（本切片）：个人主体共用结算路径使用数据库条件更新，保证充值后 `User.Quota <= common.MaxQuota`；超限时回滚订单状态和资产，并补 SQLite 边界回归。
- 支付回调并发幂等：现有每个结算器均在同一事务内锁定订单行并检查成功状态，已覆盖同订单重放的逻辑；并发回调的真实 SQLite/MySQL/PostgreSQL 与支付商回调 UAT 尚未运行，记录为 `NOT_RUN`。若现有测试模式可稳定构造并发场景，仅补最小回归，否则不引入脆弱并发测试。

## 验证

已运行定向 Go/SQLite 回归；MySQL/PostgreSQL、真实支付并发回调与隔离 UAT 仍为 `NOT_RUN`。

## 实施结果（待审阅）

- 在 `model/topup.go` 增加事务内主体分派：个人快照只更新创建订单用户的个人额度；企业快照只调用 C1 企业账本，不读取成员关系，也不更新 `User.Quota`。
- Epay、Stripe、Creem、Waffo、Waffo Pancake 以及管理员手动补单的既有成功结算入口均复用该分派；对应支付商原有额度换算、下单、跳转、验签、订单号与金额语义未改。
- `CompleteSubscriptionOrder` 在任何企业主体快照下于创建订阅或 TopUp 镜像前返回 `ErrEnterpriseSubscriptionUnsupported`，订单保持 `PENDING`。
- 新增确定性 SQLite 测试覆盖：五个充值结算器与管理员手动补单的企业快照、个人快照不因现有企业关系改变、企业不存在回滚、企业订阅拒绝和 Epay 重放。
- 既有 controller 的回调日志和 `ProviderPayload` 存档语义未改；其敏感载荷脱敏作为独立安全切片处理，不能与本 C2 的主体分派一并宣称完成。

### 验证记录

- `gofmt`：通过。
- `git diff --check`：通过。
- 定向 Go/SQLite 测试：通过。
  - `go test ./model -run '^TestPersonalTopUpRejectsQuotaOverflowWithoutCompletingOrder$' -count=1 -timeout 60s`：退出码 0；覆盖个人额度上限拒绝。
  - `go test ./model -run 'Test(AllTopUpSettlersUseEnterpriseSnapshot|RechargeEpaySettlesEnterpriseSnapshotExactlyOnce|RechargeEpayKeepsPersonalSnapshotPersonalEvenForEnterpriseOwner|ManualCompleteTopUpSettlesEnterpriseSnapshotWithoutPersonalCredit|PersonalTopUpRejectsQuotaOverflowWithoutCompletingOrder)' -count=1 -timeout 90s`：退出码 0；覆盖五种充值结算器、Epay 重放、个人快照隔离、管理员补单和额度上限。
- MySQL、PostgreSQL、真实支付回调、隔离 UAT：`NOT_RUN`。

## 未解决项

产品已确认保持所有现有支付商的下单、跳转、回调和校验流程，不添加统一金额/币种快照或改变 Stripe/Creem Checkout 顺序，不禁用 Epay，也不按新字段限制 Waffo/Waffo Pancake。C2 只增加订单快照驱动的到账主体分派；仍不得猜测未暴露字段或记录敏感回调载荷。

## 2026-08-17 源码阻断记录

在开始 C2 前已完成五种充值、四种订阅的下单与成功回调字段追踪。原先拟新增的“本地 `PENDING` 在外部支付创建之前持久化并固化最终应付总额、币种和最小货币单位”规则已被产品否决：C2 保持各支付商现有支付流程，不把未暴露字段补造成新校验合同。

- Epay 的当前 SDK `VerifyRes` 只提供 `Money`，不提供币种；不能证明回调币种与本地快照一致。
- Stripe 当前下单仅传 `Price` ID；充值允许 promotion codes。创建 Checkout 之前，本地代码没有最终总额、税费或最小货币单位，不能固化确认值。
- Creem 当前本地只持有产品配置价格；回调才带 `AmountDue` / `AmountPaid` 和币种，含税最终金额在外部 checkout 创建前不可得。
- Waffo 有 `OrderAmount`、`OrderCurrency`；Waffo Pancake 有 `amount`、`currency`（SDK 还定义 `total`），但这不能弥补上述全路径缺口。

因此 C2 只在已有成功回调的结算落点按订单快照选择个人或企业资产，不修改支付商协议。各支付商继续使用当前已存在的校验与金额语义。
