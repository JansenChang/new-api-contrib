# 企业 Relay 与异步任务账务（D）技术规格

## Metadata

**Author:** Codex

**Date:** 2026-08-17

**Status:** PARTIALLY_IMPLEMENTED — 仅开放窄范围同步 Relay 接线，发布门默认关闭

## Context

现有同步 Relay、流式 Relay、异步任务、Realtime 和 Midjourney 都使用个人 `BillingSession`、用户余额或 Token 余额。`request_id` 每个 HTTP 请求新建，不能作为企业扣费幂等键；任务还在上游提交后才创建本地记录。企业路径必须拥有独立的预留、结算、退款与异常事务，绝不回退个人资产。

## Functional Requirements

- FR-1: 企业资助调用 MUST 在上游调用前从 Token 所属用户的有效企业关系生成快照；客户端企业 ID、Token 可编辑余额和 request_id MUST NOT 决定资金归属。
- FR-2: 企业调用 MUST 要求 Token 范围的稳定 `Idempotency-Key`；同 Key 不同请求摘要 MUST 冲突，处理中或人工处理中 MUST 不重复调用上游。
- FR-3: 预留、结算、退款、异常账和 `EnterpriseUsageRecord` 状态转换 MUST 在单一主库事务内更新企业、成员、账本和使用记录。
- FR-4: 实际费用超过预留时 MUST 只结算预留、记录原企业异常账且不得扣个人资产；成员后续企业调用 MUST 暂停。
- FR-5: 异步任务 MUST 在上游提交前创建预留 Usage 和本地 `SUBMITTING` 草稿；提交结果未知 MUST 转人工处理，不得自动重试或退款。
- FR-6: 首版企业资助身份 MUST 拒绝 Realtime WebSocket 与 Midjourney，直到各自具备等价的快照和终态账务。

## Non-Functional Requirements

- NFR-1: 账务正确性不得依赖 Redis、进程内锁或已有个人 `BillingSession`。
- NFR-2: 事务与 schema MUST 兼容 SQLite、MySQL >= 5.7.8、PostgreSQL >= 9.6。
- NFR-3: `request_fingerprint` MUST 仅保存 Token ID、方法、规范化路径和规范化请求体的 SHA-256 摘要；不得保存 API Key、请求正文、渠道 Key 或个人资产数据。C1 既有的 Idempotency-Key 存储不在本切片迁移范围内。
- NFR-4: 失败、客户端中断或网络未知不能被视为零消耗；只有已确认未上游执行时才可退款。

## Acceptance Criteria

### AC-1: 稳定幂等 (FR-1, FR-2)

Given 同一 Token 使用相同 Idempotency-Key；When 请求摘要相同或不同；Then 相同摘要只获得既有处理状态，不同摘要返回冲突，均不重复上游调用。

### AC-2: 原子企业预留与结算 (FR-3, FR-4)

Given 有效企业成员的可分配额度；When 预留并按实际用量结算或退款；Then 企业、成员、账本和 Usage 同事务变化，任何个人余额不变；超额只记异常账并暂停成员。

### AC-3: 异步提交未知 (FR-5)

Given 任务在提交上游后结果未知；When 进程中断或超时；Then 草稿和 Usage 保持人工处理，轮询不自动把它退款或重提。

### AC-4: 不支持路径 (FR-6)

Given 企业资助身份请求 Realtime 或 Midjourney；When 请求到达上游前；Then 返回明确不支持，个人资金与上游均不被调用。

## Edge Cases

- EC-1: 流式客户端断开不代表上游未消耗，必须结算已知用量或转人工。
- EC-2: 自动渠道重试只有在适配器证明请求未发出时才允许；网络超时属于未知。
- EC-3: Owner 异常超额的企业资助暂停状态尚未确认，不能把它等同于平台封号。

## API Contracts

本切片新增的 HTTP 语义仍待确认，不新增路由。内部契约：

```ts
interface EnterpriseUsageOutcome { usage_id: number; status: "pending" | "settled" | "refunded" | "anomaly" | "manual_review"; replayed: boolean }
```

## Data Models

`EnterpriseUsageRecord` 保存 Token、企业、成员、价格和请求摘要快照；`EnterpriseLedger` 追加 `RESERVE`、`SETTLE`、`REFUND`、`ANOMALY`；异步 `Task.PrivateData` 仅保存可审计的非敏感快照。

| Field | Type | Constraints |
| --- | --- | --- |
| `EnterpriseUsageRecord.status` | string | `pending`、`settled`、`refunded`、`anomaly`、`manual_review`；只允许终态一次转换 |
| `EnterpriseUsageRecord.request_fingerprint` | hash | 不保存请求正文、Key 或 Idempotency-Key 原文 |
| `EnterpriseLedger.kind` | string | 仅追加 `RESERVE`、`SETTLE`、`REFUND`、`ANOMALY` 流水 |

## 已确认决策

1. 同 Key 完成后只返回使用摘要；不重放原同步响应或 SSE 字节。
2. Owner 异常超额时暂停企业资助调用，不封禁平台账号；恢复走人工处理。
3. Realtime 与 Midjourney 首版对企业资助身份明确拒绝。
4. 共同发布门为 Root-only `EnterpriseBillingEnabled`，默认 `false`。D 的 `ReserveEnterpriseUsage` 首层拒绝门关闭的企业资金路径；该门仅用于 C、D、E、B 完整链路均验收后的受控发布，不影响现有个人路径，且不得作为绕过本节契约或提前接线企业调用的理由。
5. 同 Key 的 `PENDING` / `UPSTREAM_SUBMITTED` 重复请求返回 `202` 使用状态且不得重发上游；`MANUAL_REVIEW` 返回 `409`；终态返回 `200` 使用摘要且不得重放原 API 响应或 SSE 字节。
6. 请求指纹固定为 `Token ID + HTTP 方法 + 规范化路径 + 规范化请求体` 的 SHA-256；数据库只保存该摘要。规范化请求体的具体实现必须覆盖当前 Relay 可解析的 JSON 请求；不支持安全规范化的请求类型不得进入企业资助路径。
7. 异步上游提交结果未知时，Usage 与 Task 草稿进入 `MANUAL_REVIEW`；不得自动重试上游或退款。

## 实施前证据与阻断项（2026-08-17）

源码追踪完成。已确认的重复响应、指纹和异步未知契约是 D 的实施边界；不得以现有个人账务路径、`request_id` 或未规范化原始正文替代。

### 已追踪链路

| 范围 | 当前事实 | 与 D 的差异 |
| --- | --- | --- |
| 同步与 SSE Relay | `controller.Relay` 在定价后调用个人 `service.PreConsumeBilling`，失败 defer 调用异步 `BillingSession.Refund`；成功处理器最终经 `PostTextConsumeQuota` / `PostAudioConsumeQuota` 调用 `SettleBilling`。自动渠道重试复用同一个进程内 `RelayInfo`。 | 个人资金、Token 余额、异步退款均不满足 FR-1、FR-3、NFR-1、NFR-4。企业会话必须在首次上游发送前取得稳定记录执行权，并替换这些资金动作。 |
| Request ID | `relay/common.genBaseRelayInfo` 缺失上下文值时生成新的 `common.NewRequestId()`。 | 不能作为 Token 范围、跨 HTTP 重试的幂等键。 |
| 异步任务 | `relay.RelayTaskSubmit` 在 `adaptor.DoRequest` 前预扣个人账务；`controller.RelayTask` 只在上游成功、个人结算和日志后调用 `Task.Insert`。轮询/超时会调用 `RefundTaskQuota` 或 `RecalculateTaskQuota`，二者直接调整个人钱包/订阅及 Token。 | 现有流程无法在上游提交前同事务写入草稿、Usage 与 `RESERVE`，也会在未知提交时自动退款，违反 FR-5。 |
| Realtime / Midjourney | Realtime 在 WebSocket 升级后才进入 Relay，随后调用 `PreWssConsumeQuota` / `PostWssConsumeQuota`；Midjourney 直接分派提交/动作/查询入口。 | D 必须在升级或上游连接、提交之前识别企业资助身份并拒绝；不能只在最终扣费点分流。 |
| C1 企业模型 | `EnterpriseUsageRecord` 已有 `(token_id, idempotency_key)` 唯一约束和 `request_fingerprint`；`EnterpriseLedger` 已有 `RESERVE`、`SETTLE`、`REFUND`、`ANOMALY` 常量，但 C1 的可执行金额命令仅覆盖 TOPUP/ALLOCATE/RECLAIM/ADJUSTMENT/REVERSAL。 | D 仍须定义并实现独立 Usage 状态机和预留资金事务，不能把 C1 管理侧命令当作调用侧账务实现。 |

### 已确认的实施契约

1. **重复请求：** `PENDING` / `UPSTREAM_SUBMITTED` 返回 `202` 使用摘要，`MANUAL_REVIEW` 返回 `409`，终态返回 `200` 使用摘要；均不调用上游、不重放原 API/SSE。
2. **摘要指纹：** 输入是 Token ID、HTTP 方法、规范化路径、规范化请求体；输出为 SHA-256 十六进制字符串，且只保存输出。无安全规范化路径的请求不进入企业资助调用。
3. **异步未知：** 提交不确定性转为 `MANUAL_REVIEW`，不自动重发/退款。D 应新增内部 `SUBMITTING` 草稿状态及仅企业草稿使用的轮询/超时分流，避免触发现有个人退款。
4. **C1 原始 Key：** 本切片不迁移已整合的 C1 `IdempotencyKey` 列；NFR-3 的禁止对象限定为请求指纹和普通日志。该历史存储取舍须在后续 C1 安全复审中单独处理。

### 恢复实施的最小输入

按上述契约，D 以如下最小范围实施：先新增企业专用主库事务（预留/结算/退款/异常）与确定性模型测试；再在同步/SSE 入口替换个人 `BillingSession`；最后增加 Task 草稿状态机及企业快照分流。Realtime/Midjourney 拒绝应随企业身份解析一并接入，且必须早于 WebSocket 升级和上游发送。

## Out of Scope

- OS-1: 企业订阅、成员支付冻结、邀请/移除、前端、生产切换。
- OS-2: 未有独立账务快照的 Realtime/Midjourney 企业支持。

## 当前源码接线状态（2026-08-17）

- `EnterpriseBillingEnabled=false` 时，所有既有个人 Relay、Task、Realtime 和 Midjourney 路径保持不变。
- 门开启且认证用户有 `active_enterprise_id` 时，仅非流式 JSON 的 `POST /v1/chat/completions` 和 `POST /v1/completions` 可进入企业账务：先以 Token 范围 Idempotency-Key 创建预留，再在调用 Relay handler 前写入 `UPSTREAM_SUBMITTED`；实际用量经现有 `SettleBilling` 调用企业会话结算。上游错误、没有使用量或未进入结算的成功返回均保留预留并转 `MANUAL_REVIEW`，不自动重试渠道、不退款到个人资金。
- 该窄路径的企业会话不使用个人钱包、订阅或 Token 额度。普通消费日志/统计仍可能记录使用量，但不是企业资金真相。
- 企业身份的其余一般 Relay 格式、流式 OpenAI、Realtime、Midjourney 和异步 Task 提交均在上游连接/发送前拒绝；Task 查询是只读路径，不触发资金动作。
- 未实现：Task 草稿/提交未知状态机、企业异步结算与退款、SSE、其他 JSON Relay 格式，以及跨数据库/UAT 运行验证。

### 异步 Task 的当前阻断证据

异步 Task 不能把现有 `Task.Insert` 前移一行便视为完成。当前 `relay/relay_task.go` 在上游 `DoRequest` 前调用个人 `service.PreConsumeBilling`，而 `controller/relay.go` 仅在上游成功、个人 `SettleBilling` 与消费日志之后才创建 `Task`。同时，`model/task.go` 没有 `SUBMITTING` 状态，未完成任务查询会把任何草稿交给 `service/task_polling.go`；空上游 ID 会被标记失败，超时/失败随后调用 `RefundTaskQuota` 与 `RecalculateTaskQuota`，两者直接调整个人钱包、订阅和 Token 余额。

因此，在下列改动同时完成前，企业 Task 只能上游前拒绝：新增不可轮询的 `SUBMITTING` 草稿状态；单一主库事务创建草稿、Usage 与 `RESERVE`；提交成功的 Task/Usage 状态联动；提交未知转 `MANUAL_REVIEW`；轮询、超时、失败和差额结算按 `usage_record_id` 进入企业专用资金路径。任何只创建草稿而不替换这些轮询/退款分支的实现，都会违反“不扣个人资产”的已确认边界。
