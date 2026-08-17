# 企业成员与邀请（切片 B）

## Metadata（元数据）

**Author:** Codex

**Date:** 2026-08-17
**Status:** APPROVED_FOR_ISOLATED_DOMAIN_IMPLEMENTATION

- 发布状态：`PAUSED`。企业关系公开入口必须等待 C1、C2、D、E 与本切片共同验收，并仅能在 Root 控制的 `EnterpriseBillingEnabled=true` 后受控发布。
- 范围：主库领域模型、事务服务和 SQLite 确定性测试；不注册 Router，不新增 Controller，不启用任何企业关系。

## Context（背景）

企业 Owner 需要以企业关系而非平台 `User.Role` 管理成员。成员可受企业邀请加入；暂停只影响企业资助调用，绝不改变平台用户身份。一个用户在同一时间只能拥有一个未结束的企业关系。

已有平台注册邀请 (`AuthFlowPurposeUserInvite`) 的含义是“创建平台账户”，企业邀请的含义是“加入已存在的企业”。两者不得复用同一 `AuthFlow.Purpose`、payload 或消费处理，避免企业邀请被解释为平台管理员邀请。

现有 C1 已提供企业钱包、成员可用额度与不可变账本，但 D 尚未实现企业预留/结算状态机，E 尚未实现个人资产冻结与后端支付拒绝。因此本切片只能准备不公开的领域能力，不能使任何用户实际进入已启用的企业计费状态。

## Functional Requirements（功能需求）

- FR-1: 企业邀请 MUST 使用独立 `enterprise_invite` AuthFlow 用途；其持久化邀请记录的 `expected_role` 固定为 `MEMBER`，调用方不得指定企业 Owner 或平台角色。
- FR-2: 创建、撤销邀请和暂停/恢复成员 MUST 在服务端验证当前 actor 是其 `active_enterprise_id` 所指的 ACTIVE 企业的 ACTIVE Owner；查询与更新均 MUST 带 enterprise ID。
- FR-3: 已登录用户接受邀请时，系统 MUST 锁定该用户，要求其规范化邮箱与 `target_email` 完全一致，并在同一事务中消费 AuthFlow、锁定邀请、创建 `MEMBER` 和以 CAS 写入 `active_enterprise_id`。
- FR-4: 存在任何未 `REMOVED` 成员关系或非零企业锚点的用户 MUST 不能接受另一企业邀请。目标邀请 MUST 持久化为 `CONFLICTED`，不能创建第二关系。
- FR-5: 暂停仅将目标 `MEMBER` 的关系从 ACTIVE 改为 PAUSED 并记录时间；恢复仅将 PAUSED 改为 ACTIVE。两者 MUST NOT 修改 `User.Status`、Token、个人余额或平台角色。
- FR-6: 未注册受邀者在接受页面 MUST 自行填写用户名。系统 MUST 使用邀请目标邮箱、固定普通平台 User 角色和该用户名，在同一事务创建用户、唯一主 Key、Member、企业锚点并消费企业邀请；不得接受客户端提交的邮箱、平台角色或企业 ID。创建 User MUST 沿用既有 `QuotaForNewUser` 默认赠额；该赠额仍写为用户个人资产，加入企业后立即冻结，不得写入企业钱包或成员额度，Owner 不可见且不可回收。
- FR-7: 成员移除 MUST 先真实回收成员可用额度、等待所有在途企业使用记录终态；从进入 DRAINING 起满 24 小时仍有在途记录时 MUST 进入 MANUAL_REVIEW。迟到退款 MUST 回原企业钱包，具体结算由 D 的不可变企业快照事务执行，B 不得改写个人资产。
- FR-8: 本切片 MUST NOT 注册公开企业路由、修改现有 `/api/user/register` 对企业邀请的解释，或用前端隐藏作为发布边界。

## Non-Functional Requirements（非功能需求）

- NFR-1: 所有状态变更使用主库事务、`lockForUpdate` 与条件更新；实现必须兼容 SQLite、MySQL >= 5.7.8、PostgreSQL >= 9.6。
- NFR-2: 跨企业成员/邀请目标按不存在处理；领域函数不得返回密码、完整 Key、AuthFlow payload 或邮箱以外的认证资料。
- NFR-3: 相同邀请只能成功消费一次；失败的邮箱匹配和数据库动作必须回滚消费。
- NFR-4: SQLite 定向测试必须覆盖邮箱匹配、唯一关系、跨企业隔离、邀请角色固定、暂停不封号和一次性消费。MySQL/PostgreSQL、SMTP、UAT 均在未执行前记为 `NOT_RUN`。

## API Contracts（领域契约）

```go
// 仅供未来 Controller/邮件交付编排使用；不是 HTTP DTO。
type EnterpriseInvitationCommand struct {
    ActorUserID int
    TargetEmail string
    ExpiresAt   time.Time
}

type EnterpriseInvitationResult struct {
    Invitation EnterpriseInvitation
    FlowToken  string // 仅调用方用于一次邮件交付；不得持久化或记录日志。
}

type EnterpriseInviteeRegistrationCommand struct {
    FlowToken string
    Username  string // 受邀者自填；邮箱、角色、企业和 Token Group 均由服务端决定。
}

type EnterpriseInviteeRegistrationResult struct {
    User       User
    Membership EnterpriseMembership
    Token      *Token
}

func CreateEnterpriseInvitation(EnterpriseInvitationCommand) (EnterpriseInvitationResult, error)
func RevokeEnterpriseInvitation(actorUserID, invitationID int) error
func AcceptEnterpriseInvitation(flowToken string, userID int) (*EnterpriseMembership, error)
func RegisterEnterpriseInviteeWithPrimaryToken(EnterpriseInviteeRegistrationCommand) (EnterpriseInviteeRegistrationResult, error)
func PauseEnterpriseMember(actorUserID, membershipID int) (*EnterpriseMembership, error)
func ResumeEnterpriseMember(actorUserID, membershipID int) (*EnterpriseMembership, error)
func BeginEnterpriseMemberRemoval(actorUserID, membershipID int) (*EnterpriseMembership, error)
func FinalizeEnterpriseMemberRemoval(actorUserID, membershipID int) (*EnterpriseMembership, error)
func MarkEnterpriseMemberRemovalTimedOut(actorUserID, membershipID int) (*EnterpriseMembership, error)
```

本切片没有公开 HTTP 入口。未来 Router MUST NOT 注册 `POST /api/enterprise/invitations`、`POST /api/enterprise/invitations/:id/accept`、`POST /api/enterprise/members/:id/pause` 或 `POST /api/enterprise/members/:id/resume`，直到共同发布门满足。

```ts
interface EnterpriseInviteeRegistrationCommand {
  flowToken: string;
  username: string;
}

interface EnterpriseInviteeRegistrationResult {
  userId: number;
  membershipId: number;
  tokenId: number;
}
```

超时判定的生产入口只读取服务端当前时间；用于确定性测试的带时间参数辅助函数保持包内未导出，Controller 或其他公开编排层不能注入未来时间提前进入 `MANUAL_REVIEW`。

这些函数不会检查 `EnterpriseBillingEnabled`：该开关是公开发布门，必须由未来路由/编排层在全部 C1/C2/D/E/B 通过后检查。当前没有调用者，故不会绕过该门。

## Data Models（数据模型）

| 实体 | 本片用法 | 不变量 |
| --- | --- | --- |
| `Enterprise` | 当前 Owner 的企业锚点 | ACTIVE、`owner_user_id=actor` 才允许管理 |
| `EnterpriseMembership` | Owner/Member 关系与状态 | `(enterprise_id,user_id)` 已唯一；仅 Member 可暂停/恢复 |
| `EnterpriseInvitation` | 企业邀请审计记录 | `ExpectedRole=MEMBER`；与独立 AuthFlow 一对一关联 |
| `AuthFlow` | 一次性邀请链接 | `Purpose=enterprise_invite`；payload 只保存 invitation ID |
| `User.ActiveEnterpriseId` | 单企业关系 CAS 锚点 | 接受时从 0 条件写为企业 ID |

## Acceptance Criteria（验收条件）

### AC-1: 邀请用途隔离 (FR-1)

Given ACTIVE Owner 创建邀请; When 记录持久化; Then 记录为 PENDING、ExpectedRole 为 MEMBER，AuthFlow purpose 为 `enterprise_invite`；平台邀请用途不受影响。

### AC-2: 已有用户原子接受 (FR-2, FR-3)

Given 同一企业 Owner 创建的邀请; When 登录用户邮箱规范化后相等且无现存关系地消费; Then 成员、锚点和邀请 ACCEPTED 在同一提交后同时存在。

### AC-3: 无效链接不产生成员 (FR-3)

Given 邮箱不匹配、已过期或重复链接; When 尝试接受; Then 不创建成员；邮箱不匹配不会消耗有效链接。

### AC-4: 单企业约束 (FR-4)

Given 目标已有企业关系; When 消费邀请; Then 邀请为 CONFLICTED，用户锚点/成员均不变。

### AC-5: 暂停不封号 (FR-5)

Given Owner 暂停/恢复其自身企业的 Member; When 操作完成; Then User.Status 始终不变；Owner、跨企业和无效转换失败。

### AC-6: 新用户默认赠额归个人 (FR-6)

Given 未注册受邀者只提交用户名; When 接受企业邀请; Then 固定为普通平台 User；当 `QuotaForNewUser > 0` 时默认赠额仍归该 User；User、主 Key、Member、锚点和 ACCEPTED 邀请要么同一提交全部存在，要么全部不存在。赠额不进入企业钱包或成员额度，且在有效企业关系期间不可用、Owner 不可见且不可回收。

### AC-7: 先排空再移除 (FR-7)

Given Owner 开始移除成员; When 在途记录存在; Then 成员可用额度通过 C1 RECLAIM 回企业钱包、关系保持 DRAINING，24 小时后进入 MANUAL_REVIEW；所有记录终态后才可移除并清除锚点。

### AC-8: 共同发布门 (FR-8)

Given 切片 B 已整合但共同发布门未满足; When 检查 Router; Then `router/api-router.go` 没有企业业务路由；现有平台注册邀请仍只使用 `user_invite`。

## Edge Cases（边界情况）

- EC-1: 邀请邮件投递失败不在本片处理。未来编排层必须撤销未投递的 AuthFlow/邀请，不能把投递当作数据库事务的一部分。
- EC-2: 同一用户并发接受两个邀请时，`active_enterprise_id=0` 的 CAS 至多允许一条关系提交；另一条必须回滚或持久化为冲突。
- EC-3: 同一企业已 REMOVED 的历史 Member 是否允许重新加入尚未确认。因现有 `(enterprise_id,user_id)` 唯一约束，本片明确拒绝再次加入该企业，不删除或复用历史行。
- EC-4: 没有邮箱、软删除用户、关闭企业、暂停 Owner、跨企业目标均拒绝；不泄露其他企业资源是否存在。
- EC-5: DRAINING/MANUAL_REVIEW 的成员不能恢复为 ACTIVE；只有 D 完成在途结算后，由 Owner 发起最终移除。D 对超过 24 小时后才结算的退款只读取原企业使用快照，直接回原企业钱包。

## 未决/阻断项

1. 已确认：未注册受邀者在接受页自行填写用户名；固定为普通平台 User，邮箱只取邀请记录，主 Key 创建复用既有事务，Token Group 沿用服务端 `DefaultUseAutoGroup`，不得由受邀者传入。若 `QuotaForNewUser > 0`，既有注册事务继续写入该用户个人赠额；关系生效后由共同发布门中的 E 冻结路径禁止其使用，绝不转为企业资产。完整 Key 的邮件/HTTP 一次展示仍由 E/未来编排层交付，本片不发送邮件。
2. 已确认：排空超时固定 24 小时；超时进入人工处理。B 只管理关系状态和 C1 可用额度回收；迟到退款必须由 D 的原企业快照事务直接回原企业钱包。
3. 成员清单是否包含 REMOVED 历史成员尚未确认；本片不新增读取投影。

## Out of Scope（非范围）

- OS-1: Owner 指派、企业创建、企业钱包/额度分配与账本动作、付款主体、订阅、兑换与个人资产冻结。
- OS-2: Relay/异步任务预留、结算、退款、异常账和 Realtime/Midjourney 策略。
- OS-3: API Key、邮件正文投递、前端/i18n、Controller、Router、UAT/SSH/生产部署。
