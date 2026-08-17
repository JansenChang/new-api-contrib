# 企业管理核心 API（切片 H1）技术设计

**Author:** Codex  
**Date:** 2026-08-17  
**Status:** APPROVED（仅限本切片；发布门默认关闭）  
**Review basis:** 已确认的企业角色、企业钱包、成员额度与移除边界；H 的既有 DRAFT 设计。

关联：[企业账户、角色、计费与 API Key 产品规格](../product-specs/enterprise-accounts-and-billing.md)、[企业账户与计费设计](enterprise-accounts-and-billing.md)、[企业管理 API（完整后续范围）](enterprise-management-api.md)。

## Context

切片 B、C1 已实现企业、成员关系、企业钱包、不可变账本和排空移除领域函数，但尚未有安全的管理入口。H 的完整设计包含企业邀请邮件；该链路的落地页、未注册受邀者的建户和主 Key 交付契约仍是 UNKNOWN。本切片不猜测或实现它们。

H1 只把已可由后端事实支撑的管理能力公开给已认证用户：平台 Root/Admin 将普通用户设为企业 Owner；Owner/Member 读取自己的企业摘要；Owner 读取本企业成员、划转或回收成员额度、暂停/恢复/排空移除成员。它不新增表、不改变支付、订阅、Key 或现有邀请领域函数。

## Functional Requirements

- FR-1: 发布门

所有 H1 路由 MUST 在既有认证后检查 `EnterpriseBillingEnabled`。门关闭时 MUST 返回 HTTP 403、机器码 `ENTERPRISE_FEATURE_DISABLED`，且不得写企业、成员、账本、个人资产或审计。

- FR-2: Owner 指派

`POST /api/user/:id/enterprise-admin` MUST 仅允许既有 `AdminAuth`（Root/Admin）调用。服务端只可将未删除、无有效企业关系、锚点为零的普通 `USER` 原子设为 Owner，同时创建 ACTIVE 企业、ACTIVE Owner membership 与 `active_enterprise_id`；不得改变其平台角色。Root/Admin、已有关系或并发冲突 MUST 无副作用地拒绝。

- FR-3: 自助摘要

`GET /api/enterprise/self` MUST 仅返回认证用户自身的一份安全摘要。ACTIVE Owner 可见本企业基本信息、企业钱包汇总和非 REMOVED 成员数；锚点一致且状态为 ACTIVE、PAUSED、DRAINING、MANUAL_REVIEW 或 RECLAIMED 的 Member 仅可见本企业基本信息与自己的企业成员额度汇总。

- FR-4: 成员列表

`GET /api/enterprise/members` MUST 只允许 ACTIVE Owner 读取本企业非 REMOVED 成员的分页安全投影。投影只含 membership ID、用户 ID、用户名、展示名、角色、状态、加入/暂停时间和成员额度汇总；MUST NOT 返回密码、完整 API Key、Token、个人余额、个人订阅或其他企业成员。

- FR-5: 真实额度划转

Owner 的分配/回收接口 MUST 分别调用 `AllocateEnterpriseQuota` / `ReclaimEnterpriseQuota`，不得直接修改企业或成员额度汇总。金额 MUST 是 `1..common.MaxQuota` 的整数；分配对象必须是 ACTIVE Member，回收对象只能是 ACTIVE 或 PAUSED Member，且只能回收未预留可用额度。

- FR-6: 资金幂等

分配/回收 MUST 要求长度为 1 至 128 的 `Idempotency-Key`。服务端以 `(enterprise_id, operation, key)` 构建稳定账本命令；同键同请求返回原账本结果并标记 `replayed=true`，同键改变成员、金额或操作返回 `ENTERPRISE_IDEMPOTENCY_CONFLICT`，不写第二笔流水。原始 header 值不得进入日志或审计。

- FR-7: 成员状态

暂停/恢复 MUST 仅允许 `ACTIVE -> PAUSED` 与 `PAUSED -> ACTIVE`。它们只改变企业成员关系，不得写 `User.Status`、平台角色、Token、个人资产或 API Key；相同目标状态的重复请求 MAY 返回当前安全投影且不再写库。

- FR-8: 安全移除

移除 MUST 只调用已有排空函数。成员有企业可用额度或在途企业使用记录时，系统 MUST 保持/进入 `DRAINING`，不得清除企业锚点、扣个人资产或把退款转给个人；所有在途记录终态且可用额度回收后才可变为 `REMOVED`。

- FR-9: 脱敏审计

Owner 指派、额度操作和成员状态改变 SHOULD 使用既有管理审计，记录 actor、企业/成员/账本标识、动作、请求 ID 和重放标志；MUST NOT 写入完整 Key、Token、个人数据或原始幂等键。

## Non-Functional Requirements

- NFR-1：授权必须由 `AdminAuth` / `UserAuth` 加服务端企业关系校验组成；前端显示与请求参数不是授权边界。
- NFR-2：Owner 指派和已存在的账本/移除命令 MUST 在主库事务中工作，使用现有 `lockForUpdate`，兼容 SQLite、MySQL >= 5.7.8、PostgreSQL >= 9.6。
- NFR-3：写接口 MUST 设置 `Cache-Control: no-store`；读企业数据也 SHOULD 禁止缓存。错误响应必须有稳定 `code` 和非敏感 `message`。
- NFR-4：本切片的确定性 SQLite 回归必须通过；MySQL、PostgreSQL 运行验证与隔离 UAT 分开记录，不能以构建或 HTTP 200 替代。

## Acceptance Criteria

### AC-1: 关闭门无副作用 (FR-1)

Given 发布门关闭，When 已认证 Root/Admin/Owner/Member 请求每一条 H1 路由，Then 都返回 403 与 `ENTERPRISE_FEATURE_DISABLED`，并且相关表无新增/更新。

### AC-2: 原子 Owner 指派 (FR-2)

Given 发布门在测试中开启且目标为合格普通用户，When Root 或 Admin 指派 Owner，Then 该用户平台角色不变，并原子拥有一个 ACTIVE 企业、Owner membership 与一致锚点；特权目标、已有关系和并发竞争至多一方成功。

### AC-3: 企业隔离投影 (FR-3, FR-4)

Given 两个企业各有 Owner 和成员，When 读取摘要/成员列表，Then Owner 只读本企业安全投影，Member 只读自身额度摘要，响应没有凭据、个人资产或跨企业数据。

### AC-4: 账本幂等 (FR-5, FR-6)

Given ACTIVE Owner 和余额足够的 ACTIVE Member，When 以相同幂等键重复分配或回收，Then 只有一条对应账本流水且第二次为回放；改变金额、成员或操作时余额与账本均不变。

### AC-5: 状态不影响平台身份 (FR-7)

Given Owner 管理本企业 Member，When 暂停后恢复该成员，Then 只发生允许的成员状态变更，平台用户状态、角色、Key 与个人资产均不变。

### AC-6: 排空后移除 (FR-8)

Given Owner 移除存在可用额度或在途记录的 Member，When 调用移除，Then 成员保持 DRAINING；只有排空和终态后才 REMOVED 与清锚点。

### AC-7: 错误及审计脱敏 (FR-9, NFR-3)

Given 成功、拒绝或回放的 Owner 指派/额度/成员状态操作，When 输出响应和审计，Then 有稳定错误码或非敏感审计字段，且不包含完整 Key、Token 或原始幂等键。

## Edge Cases

- EC-1: 数据库竞争

SQLite `BUSY/LOCKED`、唯一约束竞争或金额越界不得改用个人额度、伪造成功或换一个幂等键。

- EC-2: 跨企业对象

跨企业 membership ID 按不存在处理，避免泄露对象存在性。

- EC-3: 未确认调度

DRAINING 超过 24 小时转人工处理的调度入口尚未确认；H1 只保持既有安全排空状态，不伪造自动扫描。

- EC-4: 非法资金目标

已移除成员、Owner membership、DRAINING/MANUAL_REVIEW/RECLAIMED 成员不能成为额度操作目标。

## API Contracts

成功沿用 `{success:true,message:"",data}`；明确错误采用 `{success:false,code:"...",message:"..."}`。

```ts
interface EnterpriseQuotaRequest { quota: number }
interface EnterpriseMemberProjection {
  membership_id: number; user_id: number; username: string; display_name: string;
  role: "OWNER" | "MEMBER"; status: string; joined_at: number; paused_at: number;
  available_quota: number; reserved_quota: number;
}
interface EnterpriseMoneyResponse {
  enterprise_available_quota: number; member_available_quota: number;
  member_reserved_quota: number; ledger_id: number; replayed: boolean;
}
```

| 方法与路径 | 授权 | 请求 | 成功数据 | 主要错误码 |
| --- | --- | --- | --- | --- |
| `POST /api/user/:id/enterprise-admin` | `AdminAuth` + 门 | 无 | enterprise/membership 安全 ID | `ENTERPRISE_FEATURE_DISABLED`、`ENTERPRISE_OWNER_TARGET_INVALID`、`ENTERPRISE_MEMBERSHIP_CONFLICT` |
| `GET /api/enterprise/self` | `UserAuth` + 门 | 无 | 角色裁剪摘要 | `ENTERPRISE_MEMBERSHIP_REQUIRED` |
| `GET /api/enterprise/members` | `UserAuth` + ACTIVE Owner + 门 | `p`、`page_size` | 安全分页成员 | `ENTERPRISE_OWNER_REQUIRED` |
| `POST /api/enterprise/members/:id/allocations` | `UserAuth` + ACTIVE Owner + 门 | `{quota}`、`Idempotency-Key` | 余额、ledger ID、replayed | `ENTERPRISE_IDEMPOTENCY_CONFLICT`、`ENTERPRISE_QUOTA_INVALID` |
| `POST /api/enterprise/members/:id/reclaims` | 同上 | `{quota}`、`Idempotency-Key` | 同上 | 同上 |
| `POST /api/enterprise/members/:id/pause` | `UserAuth` + ACTIVE Owner + 门 | 无 | 成员安全投影 | `ENTERPRISE_INVALID_MEMBER_STATE` |
| `POST /api/enterprise/members/:id/resume` | 同上 | 无 | 成员安全投影 | 同上 |
| `POST /api/enterprise/members/:id/remove` | 同上 | 无 | 成员安全投影 | `ENTERPRISE_REMOVAL_PENDING` |

## Data Models

H1 不新增表。复用 `User.ActiveEnterpriseId`、`Enterprise`、`EnterpriseMembership`、`EnterpriseLedger` 与 `EnterpriseUsageRecord`。所有对外模型均为显式安全投影，禁止直接序列化 `User`、`Token` 或账本原始引用。

| Field | Type | Constraints |
| --- | --- | --- |
| `User.ActiveEnterpriseId` | `int` | Owner/Member 锚点；Owner 指派仅可从 0 条件更新 |
| `Enterprise` | GORM model | Owner、ACTIVE 状态、企业钱包汇总；仅由现有事务维护 |
| `EnterpriseMembership` | GORM model | `(enterprise_id,user_id)` 关系、角色、状态、成员可用/预留额度 |
| `EnterpriseLedger` | GORM model | 不可变资金流水；使用 C1 幂等命令，禁止 Controller 直接写入 |
| `EnterpriseUsageRecord` | GORM model | 移除排空时的在途/终态依据 |

## Out of Scope

- OS-1: 邀请交付

企业邀请创建/邮件投递、邀请落地页、未注册受邀者建户、首次主 Key 展示与重置交付。

- OS-2: 商业与前端范围

企业充值、支付、订阅、白名单、渠道/模型策略、企业账单/用量页面、前端页面与 i18n。

- OS-3: 发布与调度范围

变更发布门、生产部署、生产数据库迁移、DRAINING 自动扫描、企业所有权转移、历史 REMOVED 成员列表。

## 实施约束

先写路由/模型/服务的定向失败测试，再实现最小行为；不引入依赖或通用框架。完成后回写本设计和对应执行计划，保留 `NOT_RUN` 验证边界。
