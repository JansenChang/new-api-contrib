# 企业 Key 轮换与个人资产冻结（切片 E）

## Metadata

**Author:** Codex
**Date:** 2026-08-17
**Status:** APPROVED_FOR_ISOLATED_IMPLEMENTATION
Reviewers: 认证、安全、支付人工审阅
Scope: 选定 API Key 的轮换和邮件交付、普通用户单 Key 边界、企业关系期间的个人支付后端拒绝。

## Context

现有单主 Key 模式已限制普通用户创建第二把 Key，并提供普通用户的主 Key 轮换与找回流程。该流程只支持普通用户，重置完成后仅向邮件发送通知，完整 Key 仍在 HTTP 响应中一次展示；它不能满足 Root/Admin 多 Key 账户“重置哪一把就轮换哪一把”、邮件发送完整新 Key、SMTP 失败后重发同一新 Key 的已确认企业需求。

`EnterpriseMembership` 已可保存 Owner/Member 与未结束状态，Root/Admin 的独立 Owner 企业也会在基础切片中建立。但现有充值、兑换和订阅入口仍会修改个人资产。若先启用成员关系或资产冻结，会使 Owner/Member 缺少可用的企业计费链路，或让成员以隐藏菜单之外的 API 继续获得个人资产。

本切片只建立可独立验证的 Key 与冻结后端边界；企业资助调用、企业充值与成员关系公开入口仍必须等待 C2、D、B 同批发布。

## Functional Requirements

- FR-1: 普通 `USER` 在单主 Key 模式下 MUST 始终只有一条未删除 Key；Root/Admin MUST 保留多 Key 能力。
- FR-2: 已认证用户轮换 Key 时 MUST 只轮换其指定且归属自己的 Token；Root/Admin 可以选择任一自己的 Token，普通用户只能选择唯一主 Key；不得创建第二条 Token。
- FR-3: 轮换事务 MUST 锁定用户和目标 Token，原子更换目标 Key、使旧 Key 失效并递增 `auth_version`；不得改变非目标 Token 的 Key、额度或状态。
- FR-4: 数据库事务成功后 MUST 创建已有的 `APIKeyDelivery` 记录、向绑定邮箱发送完整新 Key，并在首次 HTTP 成功响应中返回完整新 Key 供前端一次展示。记录只引用目标 Token，不复制完整 Key；重发时必须读取同一 Token 的当前 Key。
- FR-5: SMTP 发送失败 MUST NOT 恢复旧 Key，也 MUST NOT 再次轮换目标 Key；记录保持 `PENDING_DELIVERY` 并更新最小失败分类/下次尝试时间，同一用户后续找回或已认证重发只能发送同一把新 Key。
- FR-5b: 每次 SMTP 投递尝试 MUST 在服务端原子检查并占用 `next_attempt_at` 冷却窗口；冷却未到期时不得调用 SMTP。有效期 5 分钟的安全证明只证明操作身份，不能绕过该窗口或重复发送完整 Key；并发重发也只能有一个请求进入 SMTP。
- FR-5a: Root/Admin 的匿名邮箱找回 MUST NOT 接受客户端提交的任意 `token_id`。服务端必须为其每一把现有 Key 创建独立、短时、一次性且在 `AuthFlow.Payload` 中绑定目标 Token 的确认链接；邮件中由用户选择目标 Key，确认页消费的 Flow 才能轮换该 Flow 绑定的 Token。普通用户保持原有唯一 Key 找回链接。
- FR-6: 存在 `PENDING_DELIVERY` 的 Token MUST NOT 再次轮换，直到该记录变为 `DELIVERED` 或由受控人工流程关闭；防止重发时错误发送另一把 Key。
- FR-7: Root-only `EnterpriseBillingEnabled` 共同发布总开关默认 MUST 为 `false`；关闭时，现有个人充值、兑换和订阅行为 MUST 保持不变。它是 C2、D、B、E 同批发布的共同门，不得通过 Admin 站点设置暴露。开关开启后，具有任一未结束企业关系（Owner/Member 的 `ACTIVE`、`PAUSED`、`DRAINING`、`MANUAL_REVIEW`、`RECLAIMED`）的用户 MUST 被后端拒绝新的个人充值、兑换、外部支付下单、个人订阅购买，以及会增加或转入个人额度的签到和推广额度转入。
- FR-8: 资产冻结 MUST NOT 读取前端视图、客户端企业 ID 或 Token 的 `RemainQuota` 决定。只有成员关系已变为 `REMOVED` 后才恢复个人支付；暂停企业关系不等于全局封号，不得阻止登录、邮箱验证、Key 找回或重置。
- FR-9: 支付回调、既有待支付订单和既有订阅的结算 MUST NOT 由 E 重判归属；C2 依据订单创建时计费主体快照处理。

## Non-Functional Requirements

- NFR-1: Token、已有 `APIKeyDelivery` 与 `auth_version` 的写入必须在同一主数据库事务内，兼容 SQLite、MySQL >= 5.7.8、PostgreSQL >= 9.6。
- NFR-2: 完整 API Key、SMTP 凭据、邮件正文和原始 SMTP 错误 MUST NOT 进入数据库审计字段、应用日志或 HTTP 错误响应。邮件正文与首次成功响应的 `full_key` 是已接受的明文交付例外；前端不得持久化、埋点或二次读取该值。
- NFR-3: 轮换、重发、冻结判断必须有确定性 SQLite 测试；真实 SMTP、MySQL、PostgreSQL、生产/UAT 结果在未执行前标记 `NOT_RUN`。
- NFR-4: 发布总开关默认关闭，且只能在 C2、D、B、E 全部通过并完成隔离 UAT 后由 Root 控制的发布流程开启。

## Acceptance Criteria

### AC-1: 单主 Key 边界 (FR-1)

Given 单主 Key 模式下的普通用户，When 并发创建第二把 Key，Then 只有一条 Token 成功持久化；Root/Admin 可保留多条。

### AC-2: 选定 Key 轮换 (FR-2, FR-3)

Given Root/Admin 拥有两把 Key，When 在安全验证后指定其中一把轮换，Then 仅该 Token 的 Key 改变、旧值不可登录、另一把不变，且既有会话失效。

### AC-3: 同 Key 重发 (FR-4, FR-5, FR-6)

Given 目标 Token 被轮换且首次 SMTP 发送失败，When 用户触发受控重发，Then 邮件使用同一新 Key，旧 Key 不恢复，也不新增 Token 或再次轮换。

若 `next_attempt_at` 尚未到期，重发请求只返回待交付状态，不调用 SMTP；冷却到期后才允许再次发送同一把新 Key。

### AC-3a: 特权账户邮箱选择 (FR-5a)

Given Root/Admin 拥有两把 Key，When 匿名邮箱找回被请求，Then 邮件只包含由服务端分别绑定两把 Key 的确认链接；用户点击其中一个链接并确认后，只轮换该链接绑定的 Key。请求体、URL 除该不透明 Flow Token 外不得携带可改变目标的 `token_id`。

### AC-4: 后端冻结 (FR-7)

Given 发布开关开启且用户处于任一未结束企业关系，When 调用兑换、任一充值下单或任一订阅购买入口，Then 后端在调用支付商或修改个人资产前拒绝；与前端菜单是否显示无关。

### AC-5: 暂停与历史订单边界 (FR-8, FR-9)

Given 成员为 `PAUSED` 或 `DRAINING`，When 用户登录、找回或重置 Key，Then 功能仍可用；当关系 `REMOVED` 后新个人支付恢复，既有支付回调仍不由 E 改写。

## Edge Cases

- EC-1: Token 已被删除、非本人 Token、普通用户存在历史多 Token 或 Token 有待投递记录时，轮换必须失败且无任何新 Key 写入。
- EC-2: 事务提交后缓存发布或会话撤销失败时，投递记录仍可审计；不得把完整 Key 写入错误日志。
- EC-3: SMTP 最终失败后，匿名找回请求必须保持防枚举响应；若存在待投递记录，只能排队同一 Key 的重发。Root/Admin 的一次找回请求只会生成一组服务端绑定目标的确认 Flow；已有未消费 Flow 时不重复生成。
- EC-4: Root/Admin 因基础切片自动拥有 Owner 关系但企业发布总开关未开时，个人支付不得被提前冻结。
- EC-5: 关系处于 `REMOVED` 前，即使成员可用额度已回收为零，也不得恢复个人支付；`RECLAIMED` 仍属冻结状态。
- EC-6: 用户进入企业前创建的个人待支付订单在回调时不得被 E 拦截或改记企业。

## API Contracts

本切片拟新增或替换的自助接口，均须 `UserAuth`、限流、`no-store` 与现有 2FA/Passkey 安全证明：

```ts
interface RotateAPIKeyRequest {
  token_id: number;
}

interface APIKeyDeliveryResponse {
  delivery_id: number;
  delivery_status: "pending" | "sent";
  full_key: string; // 仅首次成功响应；前端一次展示后立即从内存清除
}

POST /api/user/rotate-api-key
{ token_id: number }
// 200: { delivery_id: number, delivery_status: "pending" | "sent" }
// 403: SECURITY_PROOF_INVALID | TOKEN_NOT_OWNED | PENDING_DELIVERY_EXISTS

POST /api/user/api-key-deliveries/:id/resend
// 200: { delivery_id: number, delivery_status: "pending" | "sent" }
// 403: SECURITY_PROOF_INVALID | DELIVERY_NOT_OWNED
```

匿名找回接口保持防枚举的成功响应；若命中待投递记录则重发同一 Key，否则创建一次性找回流程。完整 Key 是否同时由 HTTP 成功响应返回，见“未决项”。

Root/Admin 的找回请求不会接收 `token_id`。服务端按其当前所有 Key 生成一组目标绑定的确认链接；确认请求仍只提交 `email` 和不透明 `token`，`ResetAPIKey` 仅读取服务器 Flow 的 Payload 决定目标 Token。

## Data Models

| 模型 | 字段/行为 | 约束 |
| --- | --- | --- |
| `Token` | 目标 Key 原位轮换 | 事务行锁；普通用户一条；待投递时不得再轮换 |
| `User` | `auth_version` 递增 | 同一事务；提交后撤销会话 |
| `APIKeyDelivery`（基础切片已有） | `user_id`、`token_id`、`email`、`status`、`idempotency_key`、`attempts`、`next_attempt_at`、`last_error_code`、`delivered_at` | 不存完整 Key；同一 Token 同时最多一条未结束投递；SMTP 前原子占用 `next_attempt_at`，由服务端统一执行重发冷却 |
| `EnterpriseMembership` | 冻结判断的权威关系状态 | 只读查询；不得由请求参数决定 |
| Root-only `EnterpriseBillingEnabled` 发布开关 | 默认 `false` | 仅复用现有 Root Option；不加入 Admin 站点白名单或普通用户配置；开启受 C2/D/B/E/UAT 同批发布门约束 |

## Out of Scope

- OS-1: 企业支付商下单、金额/币种快照与成功回调分派（C2）。
- OS-2: Relay、流式调用、异步任务预留/结算/退款和异常账（D）。
- OS-3: 企业邀请、Owner 指派、成员暂停/移除公开接口（B）。
- OS-4: 企业管理前端、投递管理页面与邮件供应商实际投递回执（F/运维）。
- OS-5: 支付回调原始 body/签名的统一脱敏修复。

## 未决项

产品已确认：完整新 Key 同时由邮件明文发送，并在首次 HTTP 成功响应供前端一次展示。HTTP 响应必须 `no-store`，前端不得持久化、埋点或二次读取；SMTP 失败时仍以待投递记录重发同一 Key。
