# 企业 Relay 与异步任务账务技术设计（D）

**Author:** Codex

**Date:** 2026-08-17

**Status:** DRAFT — 仅技术设计；D-01、D-02、D-03 未经产品确认，禁止开始源码实现

**Reviewers:** 平台计费、Relay、可靠性与安全负责人

**Related specs:** `enterprise-accounts-and-billing`（企业账户、账务与成员生命周期）；C1 企业账本与订单主体快照；E 个人资产冻结与成员个人支付拒绝。

## Context

当前同步 Relay 在 `controller.Relay` 中先按当前价格调用 `service.PreConsumeBilling`，再进入渠道重试；其 `BillingSession` 只实现个人钱包/订阅和 Token `RemainQuota` 的预扣、结算、异步退款。现有 `request_id` 在 `relay/common.GenRelayInfo` 缺失时由网关新生成，因此每次 HTTP 请求都可能不同，不能作为客户端重试的企业扣费幂等键。

异步任务的当前顺序同样不满足企业账务：`relay.RelayTaskSubmit` 在向上游发送前才预扣个人资金，而 `controller.RelayTask` 在上游成功、个人结算和写消费日志之后才插入 `Task`。后续轮询、超时退款和差额结算从 `Task.PrivateData` 重新走个人钱包/订阅与 Token 配额。若将企业关系直接接到这些路径，会出现企业调用误用个人资产、上游成功但无企业快照、或迟到退款错退新归属的风险。

D 的目标是在已通过的 C1 企业账本基础上，增加企业资助调用的稳定幂等、预留、结算、退款、异常账和异步任务创建时快照。企业资金权威仍是主库中的 `Enterprise`、`EnterpriseMembership`、`EnterpriseLedger` 与 `EnterpriseUsageRecord`；`model.Log`、ClickHouse、Token 余额和请求日志只可作为展示/关联信息，不能反向决定企业资金。

## Functional Requirements

- FR-1: 对有效企业关系的企业资助同步 HTTP 调用，系统 MUST 要求认证 Token 绑定的 `Idempotency-Key`；不得把 `request_id`、上游请求 ID、Task ID 或客户端传入的 `enterprise_id` 当作该键。
- FR-2: 系统 MUST 以 `(token_id, idempotency_key)` 唯一约束企业 `EnterpriseUsageRecord`，并把请求方法、标准化路径、模型标识和不落库的请求摘要指纹与该记录一并固化；同键不同摘要 MUST 拒绝且 MUST NOT 触发上游调用。
- FR-3: 企业资助调用 MUST 从认证用户的有效企业成员关系解析计费主体；不得以客户端 `enterprise_id`、前端模式、`User.Quota`、个人订阅、`Token.RemainQuota` 或 `Token.UnlimitedQuota` 决定资金来源。
- FR-4: 同步调用在首次上游发送前 MUST 在一个主库事务中完成企业授权、余额/自限校验、`EnterpriseUsageRecord` 创建或锁定、企业预留汇总更新和 `RESERVE` 账本行写入。余额不足或自限不足 MUST 不产生账本行、不调用上游。
- FR-5: 企业预留、结算、退款、异常账和使用记录状态更新 MUST 采用企业专用的同事务路径；不得复用个人 `WalletFunding`、`SubscriptionFunding`、`TryReserveTokenQuota`、`PostConsumeQuota` 或进程内异步退款作为企业资金正确性的依据。
- FR-6: 企业成员调用的成员额度和 Key 自限 MUST 同时校验。成员预留不得把企业或成员额度写成负数；成员 Key 自限只约束企业资助调用，不得改写个人资金或创建个人退款。
- FR-7: 同步/SSE 调用得到实际费用后 MUST 通过同一 `EnterpriseUsageRecord` 仅结算一次。实际费用小于预留时，未使用预留 MUST 回到原企业或原成员可用额度；失败且可确定未向上游提交时 MUST 仅退回原预留。
- FR-8: 实际费用超过预留时，系统 MUST 最多结算已预留额度；超额部分 MUST 写入原企业 `ANOMALY` 账、使用记录异常字段和可审计状态，MUST NOT 扣减个人钱包、个人订阅或使企业/成员余额为负。
- FR-9: 异步任务 MUST 在任何上游提交前持久化任务草稿、企业使用记录 ID 和不可变企业计费快照；上游成功后更新同一草稿，轮询、超时、失败、退款和差额结算 MUST 只使用该快照，不得按用户当前成员关系重新归属。
- FR-10: 异步任务的创建、提交状态、最终结算和退款 MUST 可重复执行而不重复扣款或退款；上游提交结果不确定、任务草稿落库失败后的上游成功、或资金事务失败 MUST 进入持久化人工处理状态，MUST NOT 猜测退款或重发上游请求。
- FR-11: 首版企业资助身份 MUST 拒绝 Realtime WebSocket 与 Midjourney 全部提交/动作接口，并且 MUST NOT 回退到个人钱包、个人订阅或 Token 配额；只读任务查询的企业归属行为由对应任务快照决定。
- FR-12: 企业调用产生的普通消费日志 MAY 写入非敏感 `enterprise_usage_record_id` 以供展示，但完整 `Idempotency-Key`、请求正文、渠道 Key、个人资产余额和企业账务控制字段 MUST NOT 写入普通日志、错误响应、Task 公共数据或 ClickHouse 权威账本。

## Non-Functional Requirements

- NFR-1: 所有企业金额输入、价格计算结果和上游实际扣费 MUST 先经过现有 `common.Quota*Checked` 饱和/异常检查；任何非正、NaN、无穷大或超出 quota 列上限的费用 MUST 在资金变动前失败并留下可关联的管理员审计标记。
- NFR-2: 企业资金变动事务 MUST 以稳定顺序锁定 `User`（升序）→ `Enterprise` → `EnterpriseMembership` → `EnterpriseUsageRecord`；MySQL/PostgreSQL MUST 用 `lockForUpdate(tx)`，SQLite MUST 使用事务和带状态条件的更新，禁止 SQLite `FOR UPDATE` 语法。
- NFR-3: 同一个 `(token_id, idempotency_key)` 的并发第一次请求在 SQLite、MySQL 和 PostgreSQL 上 MUST 最终只保留一条使用记录和一组对应资金流水；SQLite 的 `BUSY/LOCKED` 只可有限重试，耗尽后返回可重试错误且不得发送上游。
- NFR-4: 企业账务提交后缓存失效、普通消费日志、统计指标、邮件提醒和前端展示失败 MUST NOT 回滚已提交账本；它们的失败必须可记录/重试，且不得生成第二次资金动作。
- NFR-5: D 的发布验收 MUST 包含 SQLite 运行测试、MySQL/PostgreSQL 运行测试与隔离 UAT 企业端到端验证。任一未运行项必须标记 `NOT_RUN`，不得由静态审查或 HTTP 200 代替。

## Design and State Model

### 资金归属和固定快照

企业路径只在服务端从认证 `UserId + TokenId` 获取有效关系后创建 `EnterpriseBillingSnapshot`。它不是客户端 DTO，也不是以当前关系重算的缓存。建议快照字段如下：

| 字段 | 来源/约束 | 用途 |
| --- | --- | --- |
| `enterprise_id` | 当前有效关系 | 原企业计费主体 |
| `membership_id` | Owner 可为 0；Member 为稳定关系 ID | 原成员分配账户 |
| `membership_role_snapshot` | OWNER / MEMBER | 决定企业钱包或成员分配扣费 |
| `actor_user_id`、`token_id` | 当前认证上下文 | 审计与幂等作用域 |
| `usage_record_id` | `EnterpriseUsageRecord.ID` | 后续结算、退款唯一锚点 |
| `billing_account_id`、`allocation_id` | 企业 ID；成员为 membership ID、Owner 为 0 | 账单快照，不从当前关系重算 |
| `funding_source` | 固定为 `enterprise_wallet` 或 `enterprise_allocation` | 禁止个人回退 |
| `reserved_quota`、`request_fingerprint` | 预留事务写入 | 幂等、差额和审计 |

`EnterpriseUsageRecord` 使用下列 D 状态，而不是从普通日志或 Task 状态推断：`PENDING`、`UPSTREAM_SUBMITTED`、`SETTLED`、`REFUNDED`、`ANOMALY`、`MANUAL_REVIEW`。状态转移与账本动作在同一主库事务完成；终态不可回到可上游提交状态。

### 预留与账本语义

Owner 使用企业钱包：预留 `R` 时 `Enterprise.available_quota -= R`、`Enterprise.reserved_quota += R`，写 `RESERVE`。Member 使用已真实划转的成员额度：预留 `R` 时 `Membership.available_quota -= R`、`Membership.reserved_quota += R`，并同时占用 `self_key_reserved_quota`；不得再次减少企业钱包。两类路径都写入同一使用记录，并保持 `amount >= 0`。

实际费用为 `A` 时：

| 条件 | 汇总余额与使用记录 | 追加账本 |
| --- | --- | --- |
| `0 <= A <= R` | 释放全部 `R` 的 reserved；将 `R-A` 返还原可用余额；使用记录写 `settled_quota=A` | `SETTLE`，必要时同事务体现未使用预留回流 |
| 上游确定未提交 | 释放 `R` 并返还原可用余额；使用记录写 `REFUNDED` | `REFUND` |
| `A > R` | 先按 `R` 正常结算；使用记录写 `settled_quota=R`、`anomaly_quota=A-R`；企业 `anomaly_quota += A-R` | `SETTLE` + `ANOMALY` |
| 上游提交/结果不确定或资金事务失败 | 不移动可确定性不足的余额；使用记录写 `MANUAL_REVIEW` | 只写已成功提交的流水，人工只能追加 `ADJUSTMENT`/`REVERSAL` |

每个 `RESERVE`、`SETTLE`、`REFUND`、`ANOMALY` 使用 C1 定义的企业范围幂等/引用约束。业务动作不能用裸 `Update` 覆盖汇总余额或修改既有账本行。

### 同步 HTTP 与 SSE 流程

1. `TokenAuth` 与 `Distribute` 完成后，解析请求、价格与渠道所需上下文；企业模式不能相信请求中的企业标识。
2. 企业调用在首次可发送上游前检查稳定 `Idempotency-Key`，取得或创建使用记录。若渠道重试提高预扣上限，只允许同一记录在尚未结算前通过企业事务补充预留；不得创建第二条记录。
3. 建议在现有 `relaycommon.BillingSettler` 接口上实现企业专用会话：`Reserve`、`Settle`、`Refund` 均调用企业事务；它必须同步完成资金事实，不能沿用 `BillingSession.Refund` 的 `gopool` 退款方式。现有 `PostTextConsumeQuota`、`PostAudioConsumeQuota`、`PostWssConsumeQuota` 等最终统一调用 `SettleBilling` 的路径，应由该会话进入企业结算分支。
4. 只要已经开始上游请求，使用记录至少为 `UPSTREAM_SUBMITTED`。SSE 分块、客户端断开或上游超时本身不能证明未消费；未得到可结算使用量且无法证明未提交时转 `MANUAL_REVIEW`，不得自动退款并重放上游。
5. 普通消费日志及渠道/用户统计可以在企业账务成功后记录，但其失败不改变企业使用记录和账本。统计不属于资金真相。

### 异步任务草稿与回调

现有代码在上游返回后才 `task.Insert()`，因此 D 必须把企业异步路径拆成“草稿 → 上游提交 → 更新草稿”，不能继续沿用现有成功后插入顺序：

1. 校验稳定 `Idempotency-Key`、解析企业快照并计算全额预留。
2. 在同一主库事务中写使用记录、`RESERVE`、以及本地 `Task` 草稿。草稿使用预生成的公开 `task_id`，状态为不可对外当作成功的提交中状态，并在 `Task.PrivateData` 只保存 `EnterpriseBillingSnapshot` 和必要的上游任务关联位；不得保存 `Idempotency-Key` 原文或渠道 Key。
3. 事务提交后才调用上游。成功时把上游 Task ID、结果数据和草稿状态更新为 `SUBMITTED`，并把使用记录推进到 `UPSTREAM_SUBMITTED`。这两个本地更新若任一失败，进入 `MANUAL_REVIEW`，不重发提交。
4. 轮询成功、上游失败、超时、取消、人工退款和实际用量差额都从草稿快照的 `usage_record_id` 进入企业专用结算/退款事务；不得调用 `taskAdjustFunding`、`taskAdjustTokenQuota`、`RefundTaskQuota` 或 `RecalculateTaskQuota` 的个人资金分支。
5. 成员进入 `DRAINING` 后不能新建预留，但已有草稿继续按快照结算。超时/迟到结果、退款和人工处理必须等 B 的成员移除规则一起落地；D 不自行释放 `active_enterprise_id` 或把资金改回个人资产。

### 首版明确拒绝路径

企业资助关系存在时，`/v1/realtime` WebSocket 与 `/mj` 的所有会创建、动作或可能扣费的入口在上游连接/发送之前返回企业不支持错误。不得以“该接口没有企业实现”为由走既有个人 `PreWssConsumeQuota`、`PostWssConsumeQuota`、Midjourney 个人预扣或个人退款。普通个人调用的既有行为不在 D 改动范围内。

## Decisions Required Before Implementation

- D-01（HTTP 幂等响应）：相同 `Idempotency-Key` 处于 `PENDING`、`UPSTREAM_SUBMITTED`、`MANUAL_REVIEW` 或终态时，公开 HTTP 状态码、错误码、摘要字段以及是否提供查询接口尚未确认。技术约束仅为“不再次调用上游、不重放原始 SSE/模型响应”；实现不得自行选择 `202`、`409`、`200` 或新增查询路由。
- D-02（摘要指纹规范化）：JSON 空白/字段顺序、multipart 边界、流式请求体和请求头中哪些参与“同键不同请求”判定尚未确认。实现前必须确定可重复、不会记录敏感正文的摘要规则及版本策略；不能临时用 `request_id` 或裸请求文本替代。
- D-03（Owner 超额状态）：Member 超额后的“暂停其后续企业资助调用”已是产品规则；Owner 自己的超额应当暂停 Owner 成员关系、整个企业资助调用、还是进入何种企业状态，及其恢复权限尚未确认。D 只能记录 `ANOMALY`/`MANUAL_REVIEW` 与阻断新调用的技术能力，不能自行变更平台用户状态或授权范围。
- D-04（异步提交不确定性 SLA）：上游请求超时后，自动轮询/人工处理的时限、通知对象和人工可执行的具体冲正权限尚未确认。实现前不能把超时直接等同于失败并退款。

## Acceptance Criteria

### AC-1: 稳定企业请求幂等 (FR-1, FR-2, FR-4, NFR-3)

Given 一个有效企业成员、同一 Token 和同一个有效 `Idempotency-Key`

When 两个完全相同的同步请求并发抵达

Then 数据库最终仅有一条对应的 `EnterpriseUsageRecord` 和一组 `RESERVE` 流水

And 只有首次取得该记录执行权的请求可以向上游发送。

### AC-2: 同键不同语义 (FR-2)

Given 某 Token 已以一个 `Idempotency-Key` 创建企业使用记录

When 同 Token 以该 Key 提交摘要不一致的请求

Then 请求在上游发送前被拒绝

And 企业余额、成员额度、Key 自限和账本均不产生第二次变化。

### AC-3: Member 预留和自限 (FR-3, FR-4, FR-6)

Given 一个 `ACTIVE` Member 的可用分配额度和自限剩余额度均大于等于 `R`

When 创建企业资助调用预留 `R`

Then 只减少该 Member 的可用分配和可用自限、增加其预留计数并写入 `RESERVE`

And `User.Quota`、个人订阅和 Token `RemainQuota` 保持不变。

### AC-4: Owner 正常结算 (FR-5, FR-7)

Given Owner 企业钱包已预留 `R`

When 上游返回可验证实际费用 `A` 且 `0 <= A <= R`

Then 同一事务写入一次 `SETTLE`、使用记录 `SETTLED` 和未用预留 `R-A` 的回流

And 再次执行结算不会改变余额或增加流水。

### AC-5: 异常超额不回退个人资产 (FR-8)

Given 企业调用已预留 `R` 且上游可验证实际费用为 `A > R`

When 企业结算执行

Then 最多扣除 `R`，并写入 `A-R` 的企业异常账和使用记录异常字段

And 不扣个人钱包、个人订阅、不写负企业/成员余额，且新企业资助调用遵循 D-03 未决状态规则。

### AC-6: 异步任务草稿快照 (FR-9, FR-10)

Given 企业成员提交异步任务且预留校验通过

When 系统第一次发送上游请求

Then 在发送前已有同一事务创建的 Task 草稿、使用记录和 `RESERVE` 流水

And 后续成员暂停、排空或离开后，任务结算仍使用草稿内的原企业使用记录 ID。

### AC-7: 提交结果不确定 (FR-10, NFR-4)

Given 异步任务草稿已提交本地事务而上游提交超时

When 系统无法证明上游未接受该任务

Then 使用记录和草稿进入持久化人工处理状态

And 系统不自动退款、不重发上游、不调用个人资金路径。

### AC-8: 首版拒绝 Realtime/Midjourney (FR-11)

Given 一个当前企业资助身份的认证 Token

When 请求 `/v1/realtime` 或任一 Midjourney 创建/动作入口

Then 请求在任何上游连接或提交前被拒绝

And 个人钱包、个人订阅、Token 额度和企业账本均不发生资金变化。

### AC-9: 账务与日志分离 (FR-12, NFR-4)

Given 一笔企业使用记录已成功结算

When 普通消费日志或统计异步/后置写入失败

Then 企业账本、汇总余额和使用记录保持已结算状态

And 重试日志不会再次结算或退款企业资金。

## Edge Cases

- EC-1: 缺少、空白、超过长度限制或不符合 D-02 规范的 `Idempotency-Key`：在任何预留和上游发送前拒绝。
- EC-2: 同键并发插入遇到 SQLite `BUSY/LOCKED`：仅有限重试整个企业预留事务；耗尽时不发上游、不创建临时个人计费记录。
- EC-3: 上游已接收同步/SSE 请求但客户端连接断开或未返回 usage：保留预留并进入 `MANUAL_REVIEW`，不能凭连接断开退款。
- EC-4: 渠道自动重试导致价格上升：只能对同一未结算使用记录补充预留；补充失败时停止重试且不得以较低预留继续发送。
- EC-5: 企业/成员在请求在途期间被暂停、进入 DRAINING 或移除：禁止新预留；已有使用记录/Task 草稿按固定快照完成结算、退款或人工处理。
- EC-6: 已预留后 Task 草稿更新、使用记录更新或账本事务失败：不得继续以个人路径提交；保存可诊断失败事实或进入人工处理，禁止盲目重试上游。
- EC-7: 上游回调重复、任务轮询重复或退款工作重复：同一使用记录只允许一次终态资金动作；重复只返回既有状态。
- EC-8: 企业资金事务提交后普通日志、缓存、指标或通知失败：记录后置失败，不回滚账本，不生成第二个 `REFUND`。
- EC-9: Realtime/Midjourney 路由变化或新入口遗漏：企业身份默认拒绝，直到该类接口有单独经过审阅的企业账务设计。

## API Contracts

本切片不新增公开企业账务 HTTP 路由；是否公开幂等状态摘要/查询由 D-01 决定。以下是现有 Relay 入口的附加请求约束与内部契约，不改变普通个人调用的既有响应。

```ts
// 企业资助同步 HTTP 调用的必需请求头。
interface EnterpriseRelayHeaders {
  "Idempotency-Key": string; // 长度/字符与指纹版本由 D-02 确认
}

// 只在服务端使用；不得由客户端传入或从当前成员关系重算。
interface EnterpriseBillingSnapshot {
  enterpriseId: number;
  membershipId: number; // Owner 为 0
  membershipRole: "OWNER" | "MEMBER";
  usageRecordId: number;
  billingAccountId: number;
  allocationId: number;
  actorUserId: number;
  tokenId: number;
  fundingSource: "enterprise_wallet" | "enterprise_allocation";
  reservedQuota: number;
}

interface EnterpriseUsageSummary {
  usageRecordId: number;
  state: "PENDING" | "UPSTREAM_SUBMITTED" | "SETTLED" | "REFUNDED" | "ANOMALY" | "MANUAL_REVIEW";
  reservedQuota: number;
  settledQuota: number;
  refundedQuota: number;
  anomalyQuota: number;
}
```

适用现有入口：`POST /v1/chat/completions`、`POST /v1/completions`、`POST /v1/messages`、`POST /v1/responses`、图像/音频/嵌入/Rerank/Gemini 的已有 Relay 路由，以及现有异步 Task 提交路由。`GET /v1/realtime` 与 `/mj/*` 企业资助请求首版拒绝。D-01 未确认前，不定义重复请求的 HTTP 响应状态码、正文或查询 URL。

## Data Models

| 实体/字段 | 类型 | 约束与 D 用途 |
| --- | --- | --- |
| `EnterpriseUsageRecord.token_id + idempotency_key` | int + varchar(191) | C1 已定义的联合唯一键；D 以其作同步请求幂等边界 |
| `EnterpriseUsageRecord.request_fingerprint` | varchar(191) | 仅保存不可逆、版本化摘要；具体规范等待 D-02 |
| `EnterpriseUsageRecord.state` | varchar(32) | D 状态机；所有资金终态与其同事务更新 |
| `EnterpriseUsageRecord.reserved/settled/refunded/anomaly_quota` | int | 非负且不超过 quota 上限；不可由日志回填 |
| `EnterpriseLedger` | 追加表 | D 写 `RESERVE`、`SETTLE`、`REFUND`、`ANOMALY`；不得更新/删除既有行 |
| `Task.PrivateData.enterprise_billing` | JSON 私有快照 | 新增 `EnterpriseBillingSnapshot`，包含 `usage_record_id` 等固定 ID；不含 Key、请求正文或 `Idempotency-Key` 原文 |
| `Task` 草稿 | 现有表 | 使用预生成公开 `task_id`，上游提交前已持久化；需要新增/明确不可对外宣称成功的草稿状态语义 |
| `Enterprise/EnterpriseMembership` 汇总字段 | int | 每次 D 资金事务与账本行同步更新；Owner 与 Member 的预留字段不能混用 |

## Out of Scope

- OS-1: 本切片不实现企业邀请、Owner 指派、成员暂停/恢复、排空完成或 `active_enterprise_id` 释放；这些属于 B，且必须与 C/D/E 发布门一起启用。
- OS-2: 本切片不实现个人钱包/订阅冻结、个人充值/购买拒绝、主 Key 定向轮换或邮件投递；这些属于 E。
- OS-3: 本切片不为 Realtime 或 Midjourney 建立企业计费；首版只拒绝企业资助身份，后续必须另有已审阅设计。
- OS-4: 本切片不改变普通个人 Relay、个人异步任务、既有消费日志结构或 ClickHouse schema 的资金权威性。
- OS-5: 本切片不选择 D-01 至 D-04 未决产品语义，不新增推测性的公开 HTTP 重放、状态查询、自动退款 SLA 或 Owner 平台身份状态。
