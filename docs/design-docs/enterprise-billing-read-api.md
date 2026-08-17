# 企业账本与用量只读 API（F2）技术设计

**Status:** APPROVED

**Author:** Codex

**Date:** 2026-08-17

审阅人：权限与字段投影审计（GPT-5.6-Terra）

## Context（背景）

企业 Owner 已能查看钱包汇总和成员，但还不能查看分配、预留、结算、退款或异常账的账本历史，也不能查看企业资助调用的用量快照。企业 Member 也没有自助查看自己的企业资助调用记录的入口。

当前 `EnterpriseLedger` 和 `EnterpriseUsageRecord` 已是主库中的企业账务和调用事实；它们不是可直接公开的 DTO。其中保存的幂等键、请求指纹、账务引用和内部命令摘要不能暴露给 Owner 或 Member。

企业邀请、白名单和成员自设 Key 上限尚缺经过确认的公开契约或强制执行语义。本设计只打开现有事实的最小安全只读投影，不改变资金、数据库、发布门或外部交付。

## Functional Requirements（功能需求）

- FR-1: 账本只读查询

系统 MUST 提供 `GET /api/enterprise/ledger`。只有当前企业的 ACTIVE Owner 可以查询，且结果 MUST 始终限定为该 Owner 当前企业的账本记录。Owner 查看历史 MUST 不依赖被关联成员的当前状态，因而已移除成员的历史仍保留在原企业账本中。

- FR-2: 用量只读查询

系统 MUST 提供 `GET /api/enterprise/usage`。当前 ACTIVE Owner MUST 只看到自己企业的全部企业用量；当前可见 Member MUST 同时按当前企业、当前 Membership 与当前用户 ID 限定，只看到自己的企业资助调用历史。REMOVED Member MUST 被拒绝，不能通过客户端传入企业或成员 ID 恢复访问。

两个读取路径 MUST 先从 `User.active_enterprise_id` 解析 Enterprise，要求 `Enterprise.status = ACTIVE`，再应用 Owner 或 Member 的既有关系判断。不得让 CLOSING/CLOSED 企业的已锚定用户绕过现有企业摘要的拒绝语义读取历史。

- FR-3: 显式安全投影

账本和用量接口 MUST 只返回本设计 API 契约列出的字段。它们 MUST NOT 返回完整 Key、个人资产、请求/响应正文、渠道凭据、`idempotency_key`、`request_fingerprint`、`request_id`、`reference_id`、`command_summary`、`reason`、账务哈希或内部关联 ID。

- FR-4: 稳定分页

两个接口 MUST 使用现有 `p`、`page_size` 响应信封。列表 MUST 使用 `created_at DESC, id DESC` 的稳定排序；COUNT 与列表 MUST 使用同一企业范围谓词。Model 层 MUST 将负 offset 归零，并把无效或大于 100 的 limit 归一化为项目默认页大小。

- FR-5: 统一授权与关闭门

两个接口 MUST 注册在现有 `/api/enterprise` 路由组并复用 `UserAuth`、`DisableCache`、`EnterpriseFeatureEnabled`。发布门关闭时 MUST 返回 `403 ENTERPRISE_FEATURE_DISABLED` 且不产生写入。无有效企业关系 MUST 返回 `403 ENTERPRISE_MEMBERSHIP_REQUIRED`；Member 查询账本 MUST 返回 `403 ENTERPRISE_OWNER_REQUIRED`；读取链路 MUST NOT 以 404 区分跨企业、不存在或无权资源。

- FR-6: 前端只读展示

`/enterprise` 页面 MUST 使用服务端投影展示只读账本和用量表格，含空状态、分页和受控错误状态。前端 MUST NOT 添加付款、退款、调账、人工处理、邀请、白名单或 Key 管理按钮；收到关闭门错误 MUST 清空企业相关查询并显示已有受控关闭状态。

## Non-Functional Requirements（非功能需求）

- NFR-1（安全）：确定性测试 MUST 断言序列化响应不包含 FR-3 禁止字段。
- NFR-2（隔离）：确定性测试 MUST 覆盖两个企业、两个成员和 Owner/Member 的范围组合。
- NFR-3（资源）：单页 `page_size` MUST 不超过 100；不引入新依赖、缓存、迁移、导出或客户端排序。
- NFR-4（可访问性）：前端表格 MUST 保留语义化表头、具名分页按钮和可理解的空/错误状态。
- NFR-5（兼容性）：所有数据库查询 MUST 保持 SQLite、MySQL 5.7.8+ 与 PostgreSQL 9.6+ 兼容；不使用数据库专有 JSON 或分页语法。

## Acceptance Criteria（验收标准）

### AC-1: Owner 账本隔离 (FR-1)

Given 两个企业各有账本记录，When Owner 查询账本，Then 只得到自己的企业记录，且已移除成员相关历史仍可见。

### AC-2: Member 用量隔离 (FR-2)

Given 同一企业两个 Member 各有用量记录，When 其中一个可见 Member 查询用量，Then 只得到由 enterprise_id、membership_id、actor_user_id 在服务端同时限定的自己的记录；这些内部关联 ID 不返回给客户端。

### AC-3: 关系错误不枚举 (FR-2)

Given Member、无关系用户或 REMOVED Member，When 分别查询账本或用量，Then 得到对应的 403 企业关系或 Owner 权限错误，而非跨企业 404。

### AC-4: 敏感字段裁剪 (FR-3)

Given 账本和用量记录含内部字段，When 任一成功响应序列化，Then 不含 `idempotency_key`、`request_fingerprint`、`request_id`、`reference_id`、`command_summary`、`reason` 或任何完整 Key/个人资产字段。

### AC-5: 分页稳定 (FR-4)

Given 同秒创建的多条记录和非法分页输入，When 查询相邻页，Then 结果按创建时间和 ID 稳定倒序、无重叠，并采用受限页大小。

### AC-6: 发布门关闭 (FR-5)

Given 发布门关闭，When 请求两条路由，Then 均为 `403 ENTERPRISE_FEATURE_DISABLED` 且账本、用量计数不变。

### AC-7: 只读页面 (FR-6)

Given Owner、Member、无记录或关闭门响应，When 用户进入企业页面，Then 页面只展示其服务端允许的只读数据或受控状态，且没有资金写操作。

## Edge Cases（边界情况）

- EC-1: `p` 或 `page_size` 为负、零、非数值或大于 100 时，Model 层归一化分页边界，不能产生负 OFFSET 或无界读取。
- EC-2: Owner 已有企业关系但 Owner Membership 非 ACTIVE、企业非 ACTIVE 或 Owner anchor 不匹配时，拒绝 Owner 读取，不回退到个人或其他企业数据。
- EC-3: Member 处于 PAUSED、DRAINING、MANUAL_REVIEW 或 RECLAIMED 时，沿用 `enterpriseMemberSelfVisible` 读取自己的历史；REMOVED 不可读。
- EC-4: 账本或用量为空时，成功返回空分页，而不是伪造余额、默认调用或 404。
- EC-5: 发布门在页面停留期间关闭时，任一查询失败触发既有全页关闭状态，不能继续显示可用企业数据。

## API Contracts（API 契约）

```ts
interface EnterpriseLedgerItem {
  id: number
  kind: string
  amount: number
  enterprise_available_delta: number
  enterprise_reserved_delta: number
  member_available_delta: number
  member_reserved_delta: number
  created_at: number
}

interface EnterpriseUsageItem {
  id: number
  model_name: string
  funding_source: string
  membership_role_snapshot: number
  state: string
  reserved_quota: number
  settled_quota: number
  refunded_quota: number
  anomaly_quota: number
  created_at: number
  settled_at: number
  refunded_at: number
}

interface Page<T> {
  items: T[]
  total: number
  page: number
  page_size: number
}
```

`GET /api/enterprise/ledger?p=&page_size=` 返回 `ApiSuccess<Page<EnterpriseLedgerItem>>`；`GET /api/enterprise/usage?p=&page_size=` 返回 `ApiSuccess<Page<EnterpriseUsageItem>>`。错误仅复用 FR-5 明确的企业错误码或既有非敏感内部错误响应。

## Data Models（数据模型）

本片不新增或修改持久化模型、列、索引或迁移。实现必须从现有 `EnterpriseLedger`、`EnterpriseUsageRecord` 读取到专用投影结构，投影不持久化。

| Field | Type | Constraints |
| --- | --- | --- |
| `EnterpriseLedgerItem` | 临时只读投影 | 仅 API 契约字段；不持久化；Owner 企业范围 |
| `EnterpriseUsageItem` | 临时只读投影 | 仅 API 契约字段；不持久化；Owner 企业或 Member 自身范围 |

## Out of Scope（非范围）

- OS-1: 不修改企业账本、余额、用量、预留、结算、退款、异常账、支付或任何数据库迁移，因为本片只读。
- OS-2: 不实现企业邀请、匿名注册、邮件链接、首次 Key 交付，因为 URL/交付契约仍为 UNKNOWN。
- OS-3: 不实现企业白名单或成员自设 Key 上限写入，因为其 Relay 强制执行和写入语义尚未确认。
- OS-4: 不支持 REMOVED Member 自助查看历史；这需要独立的历史访问身份契约，不能以客户端企业 ID 代替。
- OS-5: 不打开 `EnterpriseBillingEnabled`，不部署生产；PostgreSQL UAT 与完整企业 E2E 是独立门禁。

## 实现约束

```text
Router
  -> UserAuth + DisableCache + EnterpriseFeatureEnabled
  -> Controller（仅读取分页参数）
  -> Service（当前有效企业关系与角色）
  -> Model（显式 Projection、同范围 COUNT、稳定排序）
  -> React Query + 只读表格
```

- 客户端不得传入 `enterprise_id`、`user_id` 或 `membership_id` 作为授权范围。
- 不得在 Controller 先读取完整模型再裁剪；查询必须在 Model 层以显式投影完成。
- 若审计发现任何投影字段可暴露凭据、跨企业信息或个人资产，MUST 删除该字段而非扩大权限。
