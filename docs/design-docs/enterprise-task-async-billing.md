# 企业异步 Task 账务（D-async）技术设计

**状态：** APPROVED_IMPLEMENTATION；仅限独立 D 工作树实现

**Author:** Codex

**Date:** 2026-08-17
**Status:** APPROVED_IMPLEMENTATION

## 目标

让企业资助的异步任务在首次发送上游之前，以单一主库事务创建企业 Usage 预留、`RESERVE` 账本和本地 `SUBMITTING` 草稿；之后所有提交确认、未知、轮询终态、超时、退款与差额结算均只使用固定 `usage_record_id`，绝不回退个人钱包、订阅或 Token 额度。

## 不变量

- 企业 Task 的资金事实只能由 `EnterpriseUsageRecord`、`Enterprise`、`EnterpriseMembership` 和 `EnterpriseLedger` 决定。
- `SUBMITTING` 草稿不进入普通轮询、超时失败或自动个人退款。
- 上游请求超时、断连或本地成功确认写入失败均视为“可能已提交”：Usage 与草稿转 `MANUAL_REVIEW`，不自动重发、退款或改用个人资金。
- 明确证明尚未发送上游的本地失败，才允许释放原企业预留；退款只释放原企业钱包或原成员分配额度。
- 成员之后被暂停、排空、移除或个人资产状态变化，均不能改变已创建草稿的资金归属。
- `MANUAL_REVIEW` 只能由平台 Root/Admin 结案：确认上游成功时按原 Usage 结算；确认未执行时退回原企业钱包或成员额度。结案必须在同一主库事务内同时写入 Task 终态、Usage、企业账本（权威审计）及操作者/原因；通用管理操作日志只作辅助展示，不能替代账本事实。

## 精确模块范围

| 模块 | 必须改动 | 禁止行为 |
| --- | --- | --- |
| `model/task.go` | 新增 `TaskStatusSubmitting`；在 `TaskPrivateData` 新增仅内部可见的企业快照：`usage_record_id`、企业/成员/资金来源快照。 | 不保存完整 Idempotency-Key、请求正文、API Key 或渠道 Key。 |
| `model/enterprise_usage_billing.go` | 抽出可复用的企业预留事务，使其可在同一事务创建 Usage、`RESERVE` 和 Task 草稿；新增 Task 成功关联/未知状态转换的事务。 | 不先提交企业预留再单独插入草稿。 |
| `relay/relay_task.go` | 在完成请求校验、模型映射、价格计算与公开 Task ID 生成后、`adaptor.DoRequest` 前调用草稿事务；发送前将 Usage/草稿标记为提交中。 | 调用个人 `PreConsumeBilling`、或在无草稿时发送上游。 |
| `controller/relay.go` | 企业 Task 成功时更新既有草稿，而非 `Task.Insert`；本地确认写失败时转人工处理。 | 对企业草稿调用 `SettleBilling`、个人 `Billing.Refund` 或重复插入 Task。 |
| `service/task_polling.go` | 排除 `SUBMITTING` 与企业 `MANUAL_REVIEW` 草稿；已提交企业任务终态按企业 Usage 结算/退款。 | 将空上游 ID 的企业草稿标记失败，或调用个人退款。 |
| `service/task_billing.go` | 对企业快照分流：失败退款、成功差额、超时和迟到结果统一进入企业专用事务。 | 调用 `taskAdjustFunding`、`taskAdjustTokenQuota`、`RefundTaskQuota`、`RecalculateTaskQuota` 的个人分支。 |
| `controller/task.go`、`router/api-router.go` | 新增仅平台 Root/Admin 可调用的人工结案接口。 | 不向企业 Owner/Member 或普通用户开放，不通过前端隐藏代替鉴权。 |

## 状态机

```text
Task: SUBMITTING
Usage: PENDING
  ├─ 明确本地发送前失败 → Task FAILURE + Usage REFUNDED
  ├─ 即将发送上游 → Usage UPSTREAM_SUBMITTED
  │   ├─ 已确认上游任务 ID + 本地更新成功 → Task SUBMITTED
  │   └─ 超时/断连/本地确认失败 → Task MANUAL_REVIEW + Usage MANUAL_REVIEW
  │       ├─ Root/Admin 确认上游成功 → Task SUCCESS + Usage SETTLED/ANOMALY
  │       └─ Root/Admin 确认未执行 → Task FAILURE + Usage REFUNDED
  └─ 已提交 Task 轮询终态
      ├─ 成功/实际费用 → Usage SETTLED 或 ANOMALY
      └─ 确认上游失败 → Usage REFUNDED
```

已确认使用独立的 `TaskStatusManualReview`。它必须被普通轮询、超时扫描和 Gemini/Vertex 用户实时查询排除；只能由 Root/Admin 使用 `POST /api/task/:id/enterprise-resolution` 后续人工结案，不能自动重试上游、退款或改走个人资金。请求体为 `{ "outcome": "success" | "refund", "actual_quota": number, "reason": string }`：`success` 的 `actual_quota` 必须在 `0..MaxQuota`，`refund` 必须为 `0`。相同结案结果重试只返回既有终态，不新增账本；不同结果必须冲突。管理员原因和操作者必须写入账本及操作审计。

## 可测试验收条件

### AC-1 草稿与预留原子性

Given 企业成员有足够分配额度；When 创建 Task 草稿；Then 同一事务产生一条 `PENDING` Usage、一条 `RESERVE` 账本和一条 `SUBMITTING` Task；任一写入失败时三者均不存在且余额不变。

### AC-2 上游前保护

Given 企业 Task 请求缺少 Idempotency-Key、资金不足、成员暂停或草稿事务失败；When 到达 `DoRequest` 前；Then 上游调用次数为零，个人钱包、订阅与 Token 额度均不变。

### AC-3 提交未知

Given 草稿和 Usage 已持久化，`DoRequest` 返回超时或上游 ID 的本地确认更新失败；When 请求结束；Then Task 与 Usage 均处于人工处理，预留仍在原企业资金池，重试相同 Key 不再次发送上游。

### AC-4 成功与重复回调

Given 已提交企业 Task；When 轮询或回调重复报告成功、失败或实际费用；Then Usage 仅结算/退款一次，Task 终态稳定，个人资金始终不变。

### AC-5 超时与迟到结果

Given 企业 Task 超时后仍可能被上游执行；When 扫描超时或迟到成功结果到达；Then 不自动退款；超时进入人工处理；Root/Admin 确认成功后费用结算到原 Usage、原企业异常账或原企业钱包，确认未执行后退款到原企业资金来源，均不读取当前成员关系。

### AC-6 移除与个人资产隔离

Given 草稿创建后成员进入 `DRAINING`、被移除或个人余额/订阅变化；When Task 终态结算；Then 只使用草稿内 `usage_record_id` 关联的企业快照，个人资产和新企业关系不变。

### AC-7 并发与数据库兼容

Given 同一 Token/Idempotency-Key 并发提交；When SQLite、MySQL、PostgreSQL 执行；Then 最终仅有一组草稿/Usage/账本；SQLite BUSY/LOCKED 耗尽时不发送上游。

## 验证范围

- Go 确定性模型测试：原子草稿、同 Key 并发、状态转移、个人资产不变。
- Task 提交单测：`DoRequest` 前预留、明确发送前失败退款、未知转人工。
- 轮询/超时测试：企业草稿不进入个人退款；重复终态不重复资金动作。
- 人工结案测试：仅 Root/Admin 可用；成功/退款均原子写入 Task、Usage、账本；重复同结果不重复记账，不同结果冲突；个人资产不变。
- SQLite、MySQL、PostgreSQL 运行测试，以及隔离 UAT 的“企业 Task 提交 → 上游 → 终态”闭环。

所有运行验证在实际执行前均为 `NOT_RUN`。

## Context（背景）

企业异步任务在上游超时、断连或本地确认失败时不能证明未执行。此前只能冻结预留并标记人工处理，缺少安全的结案动作，导致原企业资金不能收敛。本规格定义最小的 Root/Admin 人工结案边界；不改变普通轮询和企业成员权限。

## Functional Requirements（功能需求）

- FR-1: 系统 MUST 仅允许平台 Root/Admin 对处于 `MANUAL_REVIEW` 的企业 Task 结案。
- FR-2: 系统 MUST 在单一主库事务中写入 Task 终态、原 Usage 状态、企业余额、企业账本（权威审计）及操作者和原因。
- FR-3: 成功结案 MUST 按冻结的原 Usage 结算，未执行结案 MUST 退款至冻结的原企业资金来源；两者 MUST NOT 读取当前成员关系或个人资产。
- FR-4: 相同结案结果 MUST 幂等返回既有状态且不新增账本或操作审计；不同结果 MUST 冲突。

## Non-Functional Requirements（非功能需求）

- NFR-1: 接口授权 MUST 由后端 `AdminAuth` 和模型层平台管理员校验共同保证。
- NFR-2: SQLite、MySQL 与 PostgreSQL MUST 使用 GORM 事务和既有 `lockForUpdate` 兼容路径。
- NFR-3: 原因长度 MUST 限制为 255 字节；不得写入 API Key、请求正文或渠道密钥。

## Acceptance Criteria（验收条件）

### AC-1: Root/Admin 成功结案 (FR-1, FR-2, FR-3)

Given 企业 Task 和 Usage 均为 `MANUAL_REVIEW`; When Root/Admin 提交 `success`、实际额度和原因; Then Task 为 `SUCCESS`，Usage 为 `SETTLED` 或 `ANOMALY`，且账本记录该操作者和原因。

### AC-2: Root/Admin 未执行退款 (FR-1, FR-2, FR-3)

Given 企业 Task 和 Usage 均为 `MANUAL_REVIEW`; When Root/Admin 提交 `refund`、额度 `0` 和原因; Then Task 为 `FAILURE`，Usage 为 `REFUNDED`，预留退回原企业资金来源。

### AC-3: 普通用户拒绝 (FR-1)

Given 普通用户尝试直接调用模型层结案; When 请求包含有效 Task ID; Then 操作失败且 Task、Usage 与账本均不变。

### AC-4: 重试不重复记账 (FR-4)

Given 已由 Root/Admin 成功结案的企业 Task; When 使用相同结案结果再次提交; Then 返回既有 Usage 摘要且不增加账本、权威审计或通用管理审计。

## Edge Cases（边界情况）

- EC-1: Task 不是企业 Task 或不处于 `MANUAL_REVIEW` 时，拒绝结案。
- EC-2: `refund` 的实际额度不是 `0`、成功额度越界、原因为空或超长时，拒绝结案。
- EC-3: Task/Usage 状态不一致、事务冲突或数据库锁耗尽时，整笔结案回滚，不产生部分账务。

## API Contracts（接口契约）

POST /api/task/:id/enterprise-resolution

```ts
interface EnterpriseTaskResolutionRequest {
  outcome: 'success' | 'refund'
  actual_quota: number
  reason: string
}

interface EnterpriseTaskResolutionResponse {
  enterprise_usage: {
    usage_id: number
    state: 'SETTLED' | 'ANOMALY' | 'REFUNDED'
    reserved_quota: number
    settled_quota: number
    refunded_quota: number
    anomaly_quota: number
  }
}
```

## Data Models（数据模型）

| 实体 | 字段/状态 | 约束 |
| --- | --- | --- |
| `Task` | `status` | 仅 `MANUAL_REVIEW` 可被人工结案为 `SUCCESS` 或 `FAILURE`。 |
| `EnterpriseUsageRecord` | `state` | 仅同一事务内从 `MANUAL_REVIEW` 转为 `SETTLED`、`ANOMALY` 或 `REFUNDED`。 |
| `EnterpriseLedger` | `actor_user_id`、`reason` | 记录平台操作者和人工结案原因；不新增表或字段。 |

## Out of Scope（范围外）

- OS-1: 不实现企业 Owner/Member 的人工结案权限或前端运营界面。
- OS-2: 不自动重新查询、重发或自动退款 `MANUAL_REVIEW` 任务。
- OS-3: 不修改个人钱包、个人订阅、Token 额度或现有支付流程。
