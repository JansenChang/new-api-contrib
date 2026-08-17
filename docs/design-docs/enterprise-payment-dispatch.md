# 企业支付主体分派（C2）技术规格

## Metadata

**Author:** Codex

**Date:** 2026-08-17

**Status:** APPROVED_FOR_ISOLATED_IMPLEMENTATION（保持既有支付商流程）

**Reviewers:** 支付/计费人工审阅待完成
**范围：** 企业充值订单的支付回调分派；不启用企业成员关系或公开企业支付入口。

## Context

当前 `TopUp`、`SubscriptionOrder` 的成功回调按 `user_id` 修改个人资产。C1 将为订单固化个人或企业计费主体快照，但尚未接入回调。C2 必须只依据创建时快照结算，不能在回调时读取成员关系；否则用户入企前创建的个人订单会被错误记入企业。产品已明确要求保持既有支付商下单、跳转、回调和金额语义，不为企业需求重写支付流程。

已确认的现有充值支付商为 Epay、Stripe、Creem、Waffo、Waffo Pancake；订阅支付走 Epay、Stripe、Creem、Waffo Pancake。各入口已有验签、支付商匹配和订单行锁，C2 不得削弱它们。

## Functional Requirements

- FR-1: 所有已验证成功的充值回调 MUST 锁定本地 `TopUp` 并按其不可变计费主体快照结算；MUST NOT 读取回调时企业关系决定归属。
- FR-2: 个人主体订单 MUST 只入创建订单的用户个人资产；企业主体订单 MUST 只通过 C1 企业账本进入企业钱包，MUST NOT 修改 `User.Quota`。
- FR-3: 企业充值结算 MUST 使用稳定订单身份作为企业账本幂等键和引用；重复或并发成功回调 MUST 只产生一次资产变化和一条企业 `TOPUP` 流水。
- FR-4: 企业主体订单 MUST 复用对应既有支付商路径已经确认的额度语义，再传给 C1；C2 不得新增跨支付商的金额、币种、税费或最小单位快照合同，也不得改变既有支付商的下单顺序。
- FR-5: 订阅首版 MUST 仅接受 `personal / order.UserId / 0` 快照；企业主体订阅 MUST 拒绝且不产生订阅、TopUp 镜像或资产变化。
- FR-6: 订阅成功创建的 TopUp 镜像 MUST 保留用户、主体快照、金额、订单号、支付方式、支付商、创建/完成时间和成功状态；重复回调 MUST 不重复创建。
- FR-7: 公开路由、验签、支付商匹配和现有授权 MUST 保持；C2 MUST NOT 开放企业支付下单、Owner 指派或成员邀请。
- FR-8: 个人充值结算 MUST 以数据库条件更新保证结算后 `User.Quota <= common.MaxQuota`；超过上限时 MUST 保持订单待支付且个人资产不变。

## Non-Functional Requirements

- NFR-C2-001：订单状态、企业汇总、账本和主体结算 MUST 在同一主数据库事务中完成，且兼容 SQLite、MySQL >= 5.7.8、PostgreSQL >= 9.6。
- NFR-C2-002：失败的快照验证、企业不存在、既有支付商校验或既有额度换算失败 MUST 保持订单 `PENDING`，不得留下余额或账本半边写入。C2 不新增或弱化任何支付商既有签名、金额与订单号校验。
- NFR-C2-003：完整支付回调 body、签名和密钥 MUST NOT 被新增日志记录；既有敏感日志问题不在本切片悄然扩大。
- NFR-C2-004：新增模型逻辑 MUST 有确定性 SQLite 测试；MySQL/PostgreSQL、真实支付商回调和 UAT 未运行时必须标为 `NOT_RUN`。

## Acceptance Criteria

### AC-1: 个人订单快照 (FR-1, FR-2)

Given 加入企业前创建的个人待支付订单；When 已验证回调成功；Then 仅进入其冻结个人资产，企业钱包和企业账本不变。

### AC-2: 企业充值快照 (FR-2, FR-3, FR-4)

Given 已验证的企业主体充值订单与该支付商既有的已确认额度；When 回调成功或重放；Then 企业钱包只增加一次、生成一条订单引用的 `TOPUP`，用户个人余额不变，重放返回既有结果，支付商下单/跳转/回调参数不变。

### AC-3: 订阅边界 (FR-5, FR-6)

Given 个人或企业主体订阅订单；When 成功回调到达；Then 个人订单仅创建一份订阅及完整镜像，企业主体订单返回领域错误且无副作用。

### AC-4: 失败回滚 (NFR-C2-002)

Given 支付商不匹配、非法快照、企业不存在或额度无效；When 尝试结算；Then 订单仍为待支付且所有资产不变。

### AC-5: 无公开企业入口 (FR-7)

Given C2 已整合；When 检查全局路由；Then 不存在企业支付、邀请、Owner 指派、暂停或恢复的新公开接口。

### AC-6: 个人钱包上限 (FR-8)

Given 个人订单的当前 `User.Quota` 加充值额度将超过 `common.MaxQuota`；When 结算回调进入共用主体分派；Then 结算失败，订单保持待支付，`User.Quota` 不增加且不得超过上限。

## Edge Cases

- EC-1: 同一订单不同支付商回调必须因支付商不匹配失败，不可复用成功状态。
- EC-2: 回调抵达时用户已加入、暂停或移除企业，仍只使用订单快照；冻结与成员状态由 E/B 处理。
- EC-3: 企业账本引用冲突或额度换算失败必须回滚订单状态与企业变动。
- EC-4: 支付商订单早于本地 `PENDING` 订单创建而回调属于待确认的竞态，未确认前不得假设现有行为安全。
- EC-5: 个人钱包已接近 `common.MaxQuota` 时，条件更新必须拒绝超限入账；并发回调不能通过先读后写绕过上限。

## API Contracts

本切片不新增 HTTP 路由；下列既有接口的路径、认证与支付商语义保持不变，主体快照仅在其后续结算路径生效：

- `POST /api/user/topup`
- `POST /api/user/epay/notify`
- `POST /api/stripe/webhook`
- `POST /api/creem/webhook`
- `POST /api/waffo/webhook`
- `POST /api/waffo-pancake/webhook/:env`
- `POST /api/subscription/epay/notify`
- `POST /api/subscription/stripe/pay`
- `POST /api/subscription/creem/pay`
- `POST /api/subscription/waffo-pancake/pay`

模型层内部结果：

```ts
interface PaymentSettlementResult {
  order_kind: "topup" | "subscription";
  billing_subject: { type: "personal" | "enterprise"; subject_id: number; enterprise_id: number };
  replayed: boolean;
}
```

领域错误：`PAYMENT_PROVIDER_MISMATCH`、`BILLING_SUBJECT_INVALID`、`ENTERPRISE_NOT_FOUND`、`ENTERPRISE_IDEMPOTENCY_CONFLICT`、`ENTERPRISE_SUBSCRIPTION_UNSUPPORTED`。

## Data Models

| 模型 | C2 使用方式 | 约束 |
| --- | --- | --- |
| `TopUp` | 读取不可变主体快照和支付商 | 状态只在同一结算事务中推进 |
| `SubscriptionOrder` | 仅允许个人主体 | 成功时生成同快照镜像 |
| `Enterprise` / `EnterpriseLedger` | 企业充值的权威资产与流水 | 复用 C1 同事务账务核心 |

## 已确认决策

1. 保持各支付商现有下单、跳转、回调、验签、金额与订单号校验流程；不改为统一本地草稿/Checkout 后固化模式。
2. 企业归属只能由创建时的不可变订单主体快照决定：企业快照入企业钱包，个人快照仍入原个人资产；不得在回调时按当前成员关系改记。
3. Epay 企业充值不禁用，既有 Epay 个人支付路径不改变；其回调仍按现有支付商语义核验。
4. 原始支付回调 body 与签名作为独立安全修复统一脱敏；C2 不得保留或新增敏感载荷日志。

## Out of Scope

- OS-1: 企业订阅、自动续费、公开企业充值接口、成员个人支付拒绝、资产冻结、Relay/任务结算、生产 PostgreSQL 切换。
- OS-2: 真实支付商或生产/UAT 调用。

## 验证与发布门

前置条件是 C1 已经审阅、提交和整合。C2、D、E、B 必须共同满足企业关系启用门；任一缺失时不得让 Owner/Member 成为企业资助调用主体。
