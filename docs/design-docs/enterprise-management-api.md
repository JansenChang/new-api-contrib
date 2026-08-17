# 企业管理员设置与企业管理 API（切片 H）技术设计

**Author:** Codex
**Date:** 2026-08-17
**Status:** DRAFT（仅规格与执行计划；不代表已注册路由或已开启企业发布门）
**Reviewers:** 待权限、账务与接口审阅

关联产品规格：[企业账户、角色、计费与 API Key 产品规格](../product-specs/enterprise-accounts-and-billing.md)。
关联基础能力：切片 B 的成员领域函数、切片 C1 的企业账本命令、切片 D 的企业调用结算、切片 E 的个人资产冻结。
关联执行计划：[企业管理员设置与企业管理 API：执行计划](../exec-plans/active/2026-08-17-enterprise-management-api.md)。

## Context（背景与目标）

当前主线已经有 `Enterprise`、`EnterpriseMembership`、企业邀请、成员暂停/移除领域函数和 C1 的 `AllocateEnterpriseQuota`/`ReclaimEnterpriseQuota` 账本命令，但没有把它们暴露为受控的管理 API。平台管理员也尚无把符合条件的普通用户设置为企业 Owner 的业务入口。直接复用现有 `User.Role`、`User.Quota` 或 Token 的可编辑额度会破坏平台/企业角色分离、企业真实划转和个人资产冻结边界。

本切片只定义一组最小后端 API：平台业务管理员设置普通 User 为 Owner；Owner 邀请和已登录接受成员、查看企业和成员、分配/回收成员额度、暂停/恢复/移除成员；Owner/Member 查看自己的企业摘要。它只复用 B/C1 已有模型与事务能力，不开启 `EnterpriseBillingEnabled`，不改变支付、订阅、Key 或前端。

这是一项发布门后的接口设计，不是通过“先创建关系、以后再补账务”绕开发布门的设计。所有企业管理 API 在 `EnterpriseBillingEnabled=false` 时必须拒绝，因此本切片合入或部署后不会产生新的 Owner/Member 关系，也不会改变现有 Root/Admin 的兼容行为。

## Functional Requirements（功能需求）

- FR-1: 所有本切片 API MUST 在既有认证成功后检查 Root-only `EnterpriseBillingEnabled`。开关为 `false` 时 MUST 返回 `ENTERPRISE_FEATURE_DISABLED`，且不得创建关系、写账本、发送邮件或修改成员状态；该中间件不得提供任何启用、修改 Option 或绕过 Root-only 设置的能力。
- FR-2: `POST /api/user/:id/enterprise-admin` MUST 仅允许现有 `AdminAuth`（Root 与 Admin 均可通过）调用。服务端 MUST 仅将无有效企业关系、未删除的普通 `USER` 设置为 Owner，并在同一主库事务创建 `Enterprise`、ACTIVE Owner `EnterpriseMembership` 及 `User.ActiveEnterpriseId`；目标平台角色 MUST 保持普通 User。Root/Admin、已有非 REMOVED 关系、非零锚点或并发占用目标 MUST 被拒绝且不产生部分数据。
- FR-3: Owner 创建企业邀请 MUST 只使用 B 的 `enterprise_invite` AuthFlow 与固定 `MEMBER` 预期角色。请求只接受规范化邮箱；有效期 MUST 由服务端固定为 24 小时；响应、日志和审计记录 MUST NOT 包含不透明邀请 Token。系统发送邮件失败时 MUST 撤销同一邀请并使关联 AuthFlow 失效，不能留下可用邀请链接。
- FR-4: 已登录用户接受企业邀请 MUST 使用认证用户 ID 和服务端保存的邀请目标邮箱调用 B 的原子接受函数。客户端不得提供 `enterprise_id`、目标 User、成员角色或平台角色。邮箱不匹配、已属于任何未结束企业、无效/过期/已消费邀请和企业非 ACTIVE 时 MUST 不建立成员关系。
- FR-5: Owner 的企业摘要、成员列表、邀请创建、额度操作和成员生命周期操作 MUST 通过服务端验证 ACTIVE Owner 关系：当前 User 的锚点、Enterprise 的 `owner_user_id`/ACTIVE 状态、以及 ACTIVE Owner membership 三者一致。目标成员查询和更新 MUST 同时限制当前 `enterprise_id`、`membership_id`、`role=MEMBER` 和预期状态；跨企业对象按不存在处理。
- FR-6: Owner 成员列表 MUST 只返回当前企业的非 REMOVED 成员安全投影：membership ID、用户 ID、用户名、展示名、角色、状态、加入/暂停时间与企业成员额度汇总。它 MUST NOT 返回密码、Token、完整 API Key、个人余额、个人订阅或历史 REMOVED 成员。历史成员可见性尚未确认，不得通过请求参数临时开放。
- FR-7: Owner 分配与回收额度 MUST 分别调用 C1 的 `AllocateEnterpriseQuota` 和 `ReclaimEnterpriseQuota`，不得直接更新任何额度字段。请求金额 MUST 为 `1..common.MaxQuota` 的整数；分配目标必须为 ACTIVE Member，回收目标只能为 ACTIVE 或 PAUSED Member，且只允许回收其未预留的可用额度。资金不足、额度不足、目标在 DRAINING/MANUAL_REVIEW/RECLAIMED/REMOVED、或命令校验失败时 MUST 在账本和汇总余额写入前失败。
- FR-8: 分配和回收 MUST 要求长度为 1 至 128 的 `Idempotency-Key` 请求头。服务端 MUST 以 `(enterprise_id, key)` 直接形成稳定的 C1 命令幂等键，并以操作和 key 形成引用；同键同命令返回原账本结果并标记 `replayed=true`，同键但成员、金额或操作不同返回 `ENTERPRISE_IDEMPOTENCY_CONFLICT`，不得产生第二笔流水。不得把网关 `request_id` 当作资金幂等键。
- FR-9: Owner 暂停/恢复成员 MUST 分别只允许 `ACTIVE -> PAUSED` 和 `PAUSED -> ACTIVE`，复用 B 的服务端企业范围校验。操作 MUST NOT 写 `User.Status`、平台角色、Token、个人资产或 API Key。对已处于目标状态的重复请求可返回当前安全投影且不写入；其他非法转换 MUST 返回 `ENTERPRISE_INVALID_MEMBER_STATE`。
- FR-10: Owner 移除成员 MUST 先进入 B 定义的暂停/排空/回收流程，绝不把成员直接改为个人用户。接口只可调用 B 的开始/完成移除能力；只有成员可用额度已回收、在途企业使用记录终态且锚点清除成功后才返回 `REMOVED`。存在在途记录时返回当前 `DRAINING` 摘要，不得扣个人资产、自动退款到个人钱包或绕过人工处理。
- FR-11: `GET /api/enterprise/self` MUST 允许 ACTIVE Owner，以及锚点一致且状态为 ACTIVE、PAUSED、DRAINING、MANUAL_REVIEW 或 RECLAIMED 的 Member 读取自己的企业摘要。Owner 可见本企业钱包汇总和非 REMOVED 成员数；Member 只可见自己的成员额度汇总及企业基本信息。两者都 MUST NOT 读取完整 Key、个人资产、其他成员额度或其他企业信息。
- FR-12: 每个成功或被拒绝的 Owner 指派、邀请创建、额度操作及成员生命周期变更 SHOULD 写入现有操作审计，至少记录 actor、企业/成员/邀请标识、动作、请求 ID、幂等重放标志（如适用）和时间；审计内容 MUST NOT 保存完整 Key、邀请 Token、邮件正文或原始 `Idempotency-Key`。

## Non-Functional Requirements（非功能需求）

- NFR-1: 新路由 MUST 使用既有 `AdminAuth` 或 `UserAuth`，随后再执行企业发布门与服务端关系授权；前端隐藏、企业视图和请求参数不是安全边界。
- NFR-2: Owner 创建、邀请接受、状态变更和账务命令 MUST 使用主库事务、`lockForUpdate` 与条件更新，兼容 SQLite、MySQL >= 5.7.8、PostgreSQL >= 9.6。实现不得引入数据库专有 SQL 或把 SQLite 的竞争失败解释为成功。
- NFR-3: 所有变更响应 MUST 使用明确、稳定的机器错误码；成功响应沿用现有 `{success,message,data}` 包装。不得以仅有中文错误文本作为前端分支依据。
- NFR-4: 读接口不应写数据库；天然幂等的状态请求重复调用不得再次写入。创建邀请的重复邮箱请求必须不生成第二条 PENDING 邀请或再次发送邮件；接受的重复/已消费链接不得产生第二关系或第二账户。
- NFR-5: 默认关闭发布门下的 API 路由验证、SQLite 确定性测试、MySQL/PostgreSQL 运行测试和隔离 UAT 必须分别记录。任何 `NOT_RUN` 不得以构建成功或 HTTP 200 替代。

## Acceptance Criteria（验收条件）

### AC-1: 默认关闭的发布门 (FR-1, NFR-1)

Given `EnterpriseBillingEnabled=false`，且 Root/Admin、Owner 或 Member 已通过既有认证
When 请求本设计中的任一企业管理 API
Then 返回 `ENTERPRISE_FEATURE_DISABLED`，且企业、成员、邀请、账本、邮件发送器和个人资产均无变化。

### AC-2: 平台业务管理员设置 Owner (FR-2, NFR-2)

Given 发布门已在隔离测试中开启，且 Root 或 Admin 选择一个无企业关系的普通 User
When 调用 `POST /api/user/:id/enterprise-admin`
Then 该 User 仍是普通平台 User，并在同一事务拥有一个 ACTIVE 企业、一个 ACTIVE Owner membership 和一致的 `active_enterprise_id`。

### AC-3: Owner 指派拒绝与并发 (FR-2, NFR-2)

Given 目标是 Root/Admin、已有非 REMOVED 关系、非零锚点，或两个请求并发设置同一普通 User
When 调用 Owner 指派接口
Then 无效目标被拒绝；并发至多一个请求成功；不会留下孤立企业、孤立 Owner membership 或错误锚点。

### AC-4: 邀请创建与投递失败收口 (FR-3, NFR-4)

Given ACTIVE Owner 为本企业输入有效邮箱
When 创建邀请且邮件发送成功
Then 仅创建一条 `PENDING`、`ExpectedRole=MEMBER`、24 小时过期的邀请，响应不含邀请 Token；同企业同邮箱已有 PENDING 邀请时不新建或重发。
Given 邮件发送失败
When 创建流程结束
Then 对应邀请与 AuthFlow 均不可再消费，且接口返回 `ENTERPRISE_INVITATION_DELIVERY_FAILED`。

### AC-5: 已登录接受与单企业约束 (FR-4, NFR-2)

Given 收件邮箱对应的已登录普通 User 且没有有效企业关系
When 该用户提交有效企业邀请 Token
Then 同一事务消费邀请、创建 ACTIVE Member 并写入企业锚点。
Given 邮箱不匹配、链接无效/过期/已消费或用户已有有效企业关系
When 用户尝试接受
Then 不创建或覆盖关系，且不会泄露其他企业信息。

### AC-6: 企业隔离的摘要与列表 (FR-5, FR-6, FR-11)

Given 两个企业各有 Owner 和成员
When Owner 请求成员列表或自助摘要，Member 请求自助摘要
Then Owner 只看到本企业的非 REMOVED 成员和钱包汇总；Member 只看到自己的分配额度；所有响应均不含完整 Key、个人资产或其他企业数据。

### AC-7: 真实划转与幂等回放 (FR-7, FR-8)

Given ACTIVE Owner 的企业可分配余额足够，目标为 ACTIVE Member
When 以相同 `Idempotency-Key` 和相同金额重复调用分配接口
Then 仅产生一条 `ALLOCATE` 账本流水，首次和重放均返回同一 ledger ID，第二次标记 `replayed=true`。
Given 同一 key 改变金额、成员或调用回收接口
When 请求到账本命令
Then 返回 `ENTERPRISE_IDEMPOTENCY_CONFLICT`，所有余额和流水保持不变。

### AC-8: 回收和成员状态边界 (FR-7, FR-9)

Given Owner 管理本企业 Member
When 对 ACTIVE Member 暂停、对 PAUSED Member 回收未预留额度并恢复
Then 只改变成员关系与 C1 企业账本；`User.Status`、Token、个人余额和平台角色不变。
Given 目标跨企业、为 Owner、处于 DRAINING/MANUAL_REVIEW/RECLAIMED/REMOVED，或回收金额超过可用额度
When 调用暂停、恢复、分配或回收
Then 请求被拒绝且不产生资金或状态副作用。

### AC-9: 排空后移除 (FR-10)

Given Owner 请求移除拥有可用额度或在途企业使用记录的 Member
When 调用移除接口
Then 系统先阻断新的企业资助调用并进入/保持 `DRAINING`，只回收可用额度；在途记录未终态时不清除锚点、不恢复个人资产。
Given 在途记录全部终态
When Owner 再次调用同一移除接口
Then 关系变为 `REMOVED` 并清除该用户企业锚点，且迟到退款仍由 D 的原企业快照处理。

### AC-10: 错误与响应保密 (NFR-3, NFR-4)

Given 未认证、非 Owner、跨企业 Owner 或不符合输入约束的调用者
When 请求任一受保护接口
Then 返回对应机器码，不返回密码、Token、完整 Key、邀请 Token、邮件内容、内部 SQL 错误或跨企业存在性信息。

### AC-11: 操作审计脱敏 (FR-12)

Given Owner 指派、邀请创建、额度变更或成员状态请求成功、重放或被拒绝
When 写入现有操作审计
Then 审计可关联 actor、动作、企业/成员/邀请 ID 和请求 ID；不会保存完整 Key、邀请 Token、邮件正文或原始 `Idempotency-Key`。

## Edge Cases（边界情况）

- EC-1: Root/Admin 已由基础迁移拥有独立企业，不能调用 Owner 指派接口把自身或另一个特权用户转换为普通 Owner；该接口只创建普通 User 的新企业。
- EC-2: 发送邀请的数据库提交与 SMTP 不能是原子事务。邮件超时或失败后，服务端必须做可重试的撤销/失效收口；若该收口自身失败，必须记录操作失败并返回 5xx，不得报告邀请已发送。
- EC-3: 同一邀请链接在两个已登录会话并发接受时，AuthFlow 消费和 `active_enterprise_id=0` 条件写入最多允许一方成功；另一方不获得成员关系。
- EC-4: C1 账本返回 SQLite `BUSY/LOCKED`、唯一约束竞争或金额越界时，Controller 不得重试为另一笔 Key、改用个人额度或直接更新汇总字段；只返回可重试/业务失败结果。
- EC-5: 成员移除超过 24 小时需要进入 `MANUAL_REVIEW`。当前已验证的 B 领域函数需要外部调度或受控调用触发，但本切片尚未确认可复用的后台调度入口；不得在 Controller 中伪造客户端时间或宣称已自动扫描。
- EC-6: 未注册邮箱接受企业邀请会创建普通 User 和主 Key，涉及首次 HTTP 展示及邮件交付的 Key 合同。本切片不定义该匿名注册 API，避免在没有已审阅前端链接/交付编排时泄露或丢失 Key；其领域函数保持未公开。

## API Contracts（HTTP 契约）

所有路径均位于 `/api`；除特别说明外，成功采用 HTTP `200` 和既有 `{success:true,message:"",data}` 包装。发布门关闭使用 HTTP `403`。认证失败继续采用现有认证中间件响应。所有变更请求均返回 `Cache-Control: no-store`；只有额度接口强制要求 `Idempotency-Key`。

| 方法与路径 | 认证与服务端授权 | 请求 | 成功数据 | 拒绝前提/错误码 |
| --- | --- | --- | --- | --- |
| `POST /api/user/:id/enterprise-admin` | `AdminAuth` + 发布门 | 无 body | 新 Owner、安全的 enterprise/membership ID | `ENTERPRISE_FEATURE_DISABLED`、`ENTERPRISE_OWNER_TARGET_INVALID`、`ENTERPRISE_MEMBERSHIP_CONFLICT` |
| `POST /api/enterprise/invitations` | `UserAuth` + ACTIVE Owner + 发布门 | `email` | 邀请 ID、状态、过期时间 | `ENTERPRISE_OWNER_REQUIRED`、`ENTERPRISE_INVITATION_ALREADY_PENDING`、`ENTERPRISE_INVITATION_DELIVERY_FAILED` |
| `POST /api/enterprise/invitations/accept` | `UserAuth` + 发布门 | `invite_token` | 自己的安全 Member 投影 | `ENTERPRISE_INVITATION_INVALID`、`ENTERPRISE_INVITATION_EMAIL_MISMATCH`、`ENTERPRISE_MEMBERSHIP_CONFLICT` |
| `GET /api/enterprise/self` | `UserAuth` + ACTIVE Owner 或锚点一致的非 REMOVED Member + 发布门 | 无 | 按角色裁剪的自助摘要 | `ENTERPRISE_MEMBERSHIP_REQUIRED` |
| `GET /api/enterprise/members` | `UserAuth` + ACTIVE Owner + 发布门 | 无 | 非 REMOVED Member 安全列表 | `ENTERPRISE_OWNER_REQUIRED` |
| `POST /api/enterprise/members/:id/allocations` | `UserAuth` + ACTIVE Owner + 发布门 | `quota` + `Idempotency-Key` | 钱包/成员余额、ledger ID、replayed | `ENTERPRISE_INVALID_QUOTA`、`ENTERPRISE_INSUFFICIENT_QUOTA`、`ENTERPRISE_IDEMPOTENCY_CONFLICT` |
| `POST /api/enterprise/members/:id/reclaims` | `UserAuth` + ACTIVE Owner + 发布门 | `quota` + `Idempotency-Key` | 钱包/成员余额、ledger ID、replayed | 同上，且拒绝非 ACTIVE/PAUSED Member |
| `POST /api/enterprise/members/:id/pause` | `UserAuth` + ACTIVE Owner + 发布门 | 无 | 当前 Member 安全投影 | `ENTERPRISE_MEMBER_NOT_FOUND`、`ENTERPRISE_INVALID_MEMBER_STATE` |
| `POST /api/enterprise/members/:id/resume` | `UserAuth` + ACTIVE Owner + 发布门 | 无 | 当前 Member 安全投影 | `ENTERPRISE_MEMBER_NOT_FOUND`、`ENTERPRISE_INVALID_MEMBER_STATE` |
| `POST /api/enterprise/members/:id/removal` | `UserAuth` + ACTIVE Owner + 发布门 | 无 | 当前 Member 安全投影及 `removal_pending` | `ENTERPRISE_MEMBER_NOT_FOUND`、`ENTERPRISE_INVALID_MEMBER_STATE` |

`enterprise_id`、`user_id`、成员角色、账本引用、过期时间和原因均不接受客户端提交。`invite_token` 仅允许在接受端点的请求体中出现，必须被标为敏感字段，禁止写入访问日志、审计详情或响应。

```ts
interface SetEnterpriseAdminResponse {
  user_id: number;
  enterprise_id: number;
  membership_id: number;
  platform_role: "USER";
}

interface EnterpriseInvitationCreateRequest { email: string; }
interface EnterpriseInvitationAcceptRequest { invite_token: string; }

interface EnterpriseMemberProjection {
  membership_id: number;
  user_id: number;
  username: string;
  display_name: string;
  role: "OWNER" | "MEMBER";
  status: "ACTIVE" | "PAUSED" | "DRAINING" | "MANUAL_REVIEW" | "RECLAIMED" | "REMOVED";
  joined_at: number;
  paused_at: number;
  available_quota: number;
  reserved_quota: number;
}

interface EnterpriseQuotaRequest { quota: number; }
interface EnterpriseQuotaResponse {
  enterprise_available_quota: number;
  enterprise_reserved_quota: number;
  member_available_quota: number;
  member_reserved_quota: number;
  ledger_id: number;
  replayed: boolean;
}

interface EnterpriseSelfSummary {
  enterprise: { id: number; name: string; status: "ACTIVE" | "CLOSING" | "CLOSED" };
  membership: Pick<EnterpriseMemberProjection, "membership_id" | "role" | "status" | "available_quota" | "reserved_quota">;
  wallet?: { available_quota: number; reserved_quota: number; anomaly_quota: number; member_count: number };
}

interface EnterpriseErrorResponse { success: false; code: string; message: string; }
```

## Data Models（数据模型与实现落点）

本切片复用已有模型，不新增通用角色、企业钱包、支付或 Key 表。Owner 指派需要补充一个明确的模型/服务事务；控制器只解析输入、调用服务、映射错误和写审计，Router 只注册并排序中间件。

| 对象 | 已有事实 | 本切片的使用/约束 |
| --- | --- | --- |
| `User.Role`、`User.ActiveEnterpriseId` | 平台角色与单企业锚点 | 只允许普通 User 成为 Owner；绝不写平台 Admin；Owner 创建用 `active_enterprise_id=0` 条件更新。 |
| `Enterprise` | Owner、状态、钱包汇总 | Owner 指派创建 ACTIVE 企业；摘要只读取安全字段；额度不得由 Controller 直接更新。 |
| `EnterpriseMembership` | Owner/Member、状态、成员额度 | 所有企业授权都由它与 User/Enterprise 一致性决定；列表默认排除 REMOVED。 |
| `EnterpriseInvitation` + `AuthFlow` | B 的独立邀请与一次消费 | 创建固定 `MEMBER` 和 24 小时过期；投递失败要同时失效两者；已登录接受只复用 B。 |
| `EnterpriseLedger` | C1 不可变流水与企业范围幂等 | 分配/回收只经 C1 命令；Header key 原文不直接写审计。 |
| `EnterpriseUsageRecord` | D 的在途/终态账务事实 | 移除只通过 B 读取其状态；不从普通 Log 或当前成员关系重算退款归属。 |
| `APIKeyDelivery`、`Token` | E 的凭据与交付 | 不被本切片读取、返回、轮换或代管。 |

建议的最小代码边界如下，具体文件名以实施前实际树为准：

```text
router/api-router.go
  -> 新 enterpriseRoute：既有 UserAuth/AdminAuth -> 发布门 -> controller
controller/enterprise_management.go
  -> DTO 校验、敏感字段脱敏、HTTP/机器码、现有操作审计
service/enterprise_management.go
  -> Owner 指派、投递失败收口、摘要/投影、状态幂等编排
model/enterprise.go / enterprise_membership.go / enterprise_ledger.go
  -> 仅补充缺少的事务性 Owner 指派、查询投影和投递失效原子操作；复用 B/C1 命令
```

## Out of Scope（非范围）

- OS-1: 不新增企业白名单/IP 策略、渠道/模型策略、企业充值、支付商下单、企业订阅、个人订阅冻结实现或支付回调改造；已有 E/C2/D 行为不是本切片的变更目标。
- OS-2: 不增加任何完整 API Key 读取、成员 Key 重置、代管 Key、Token 管理或邮件正文查询接口；Owner 永远不能取得成员的完整 Key。
- OS-3: 不开放未注册受邀者的匿名注册/接受接口，不定义前端邀请落地页，不为其猜测 URL、Cookie、Key 首次展示或邮件投递编排。
- OS-4: 不改变 `EnterpriseBillingEnabled` 的 Root-only 配置归属、默认值或发布流程；不将其加入 Admin 站点设置白名单，也不在测试以外开启它。
- OS-5: 不实现 24 小时 DRAINING 自动扫描、人工处理后台、所有权转移、企业关闭、历史 REMOVED 成员列表、企业账单/用量列表或前端/i18n 页面。

## 明确未知项与实施前决策

1. **邀请邮件落地页：UNKNOWN。** 当前已验证的普通邀请使用 `/sign-up?invite_token=...`，不能复用为企业邀请。企业邀请的前端链接路径、已登录用户把 Token 提交给 API 的交互和无注册入口时的提示尚未确认。后端可先完成受测试控制的投递接口，但不得猜测并上线链接。
2. **DRAINING 超时触发器：UNKNOWN。** B 有服务端时间的 `MarkEnterpriseMemberRemovalTimedOut`，但本切片尚未确认可复用的周期任务入口。没有该入口时，只能保证移除的安全排空，不能声称“24 小时后自动进入人工处理”。
3. **历史 REMOVED 成员可见性：未确认。** 本设计采用默认不可见且不提供查询参数；若产品需要审计历史，必须另行定义授权、分页、字段和保留期。
4. **未注册企业受邀者交付：已知但不在本切片。** B 已有原子建户/建主 Key 领域函数，E 已有 Key 投递原则；匿名 HTTP 契约、前端一次展示和邮件失败状态仍需一个单独经过审阅的注册交付切片。
