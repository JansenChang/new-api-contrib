# 企业账本内核切片 C
状态：INTEGRATED（多数据库、支付回调与 UAT 验证待完成）
负责人：Codex
更新时间：2026-08-17

## 目标与非目标

目标：在 `model/` 增加企业钱包入账、Owner 向成员真实划转/回收、不可变账本和稳定幂等的主库事务内核，并为充值订单固化计费主体快照。

非目标：不注册 HTTP 路由，不接入 Relay、异步任务、支付回调或个人资产行为，不启用企业成员关系，不连接 SSH/UAT。

## 现状证据

- `model/enterprise.go` 已有企业、成员和账本汇总字段，但没有资金操作函数。
- `model/locking.go` 提供跨数据库 `lockForUpdate`；SQLite 不使用 `FOR UPDATE`。
- `common/quota_math.go` 提供严格额度边界转换工具。
- `TopUp` 与 `SubscriptionOrder` 当前只有 `user_id`，订单主体快照尚未存在。

## 修改范围

- 新增企业账本事务服务及 SQLite 测试：复合唯一索引、余额回滚、并发同键重放和冲正/调账边界。
- 为 `TopUp`、`SubscriptionOrder` 增加创建时计费主体快照字段；不改变现有支付回调的主体分派、授权、金额或资产归属，调用方分派留给后续 C2。
- 幂等重放使用含 kind、membership、amount、actor、reference、adjustment_direction、reverses_ledger_id 的规范化命令摘要；历史缺少摘要时不从旧 delta 猜测方向。
- SQLite 首次同键并发时，对驱动明确返回的 `SQLITE_BUSY`/`SQLITE_LOCKED` 做有限事务级重试；每次重试都丢弃原事务并按企业与幂等键重新读取、比较完整命令摘要。其它数据库错误不重试。
- 冲正仅允许 `TOPUP`、`ALLOCATE`、`RECLAIM`、`ADJUSTMENT`，调账/冲正校验 Root/Admin；引用类型按动作固定白名单。
- 订单快照在创建后不可改变，阻止 GORM map/model 更新改写主体字段；订阅完成事务仅保证 TopUp 镜像复制并校验订单的主体快照和 `PaymentProvider`，不据此改变回调主体分派；历史缺失或损坏的个人快照回填为 `personal/user_id/0`。
- 新增仅包内可用的企业 TopUp 结算入口：锁定成功订单、校验 enterprise 主体快照，以订单 ID 派生稳定幂等键，并且仅通过企业 `TOPUP` 账本入企业钱包；入口必须显式接收已由调用方按支付商规则换算完成的内部额度，绝不从语义不统一的 `TopUp.Amount` 推导。C2 负责回调签名/金额核验及各支付商换算后才可调用；不注册路由且不接入支付回调。
- 账本迁移仅补齐缺失摘要哈希，创建复合索引前检测重复并返回 enterprise/hash/count 诊断。
- 仅修改 `model/` 与本执行计划；不扩展公开 API。

## 风险与回滚

- 资金/计费与数据库迁移属于高风险，完成后必须人工审阅。
- 已整合后不通过删除或改写账本回滚；后续发现数据问题仅可前向修复或冲正。未执行生产迁移和部署。

## 实施步骤

1. 复核已有模型、锁、迁移和测试设施。
2. 实现事务内入账、分配、回收及幂等冲突校验。
3. 添加余额非负、事务回滚、重复请求和冲突测试，以及企业 TopUp 结算、快照 map 更新拒绝、正常订单状态/金额更新和镜像 PaymentProvider 一致性测试。
4. 运行定向 SQLite 测试与静态兼容检查，回写结果和 NOT_RUN 边界。并发测试通过测试专用屏障让两个首次幂等读取同时发生，验证一创建、一重放、单账本和单次余额变更。

## 验证命令与通过条件

- `go test ./model -run 'TestEnterpriseLedger' -count=1`：新增模型行为测试通过。
- `go test ./model -run 'TestEnterpriseFoundation' -count=1`：基础模型回归通过。
- `git diff --check`：无空白错误。
- MySQL/PostgreSQL 实测、支付回调和 UAT：`NOT_RUN`。

## 结果与未解决项

已完成内部企业 TopUp 结算与订单快照不变量修复；该内部入口只接收已标准化的额度，C2 必须完成支付商换算后才能调用。订阅 TopUp 镜像仅复制/校验主体快照和 PaymentProvider，未改变支付回调主体分派，后者仍为 C2。`gofmt` 与 `git diff --check` 通过。定向 `go test ./model -run 'EnterpriseLedger|BillingSubject' -count=1` 已启动但在本机 Go 工具链遥测/构建阶段无输出，手动终止，记为 `NOT_RUN`；未进行第二次重试。MySQL/PostgreSQL、支付回调和 UAT 仍为 `NOT_RUN`。代码已在人工授权后以 `d94426c56` 整合至主分支，未部署。
