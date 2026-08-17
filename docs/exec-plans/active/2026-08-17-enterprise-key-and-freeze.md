# 企业 Key 与个人资产冻结 E：执行计划

状态：INTEGRATED_PENDING_UAT（已整合；发布门关闭）
负责人：Codex
更新时间：2026-08-17

## 目标与非目标

实现已确认的“选哪把轮换哪把、明文邮件交付、SMTP 失败重发同一新 Key”及企业关系期间个人支付后端拒绝。不会提前启用企业成员关系、企业支付或企业资助调用。

## 现状证据

- `model.RotatePrimaryTokenByUserIDTx` 仅允许普通用户轮换唯一 Key；`controller.ResetAPIKey` 也仅允许普通用户。
- 现有轮换成功响应包含完整 Key，邮件仅为通知，没有持久化投递记录。
- 个人充值、兑换与订阅入口直接按用户处理；`EnterpriseMembership` 已保存状态但尚无统一资产冻结判断。
- Root/Admin 已可能具有自动创建的 Owner 关系，因此个人资产冻结必须由默认关闭的 Root-only `EnterpriseBillingEnabled` 发布门保护；该门是 C2/D/B/E 的共同发布门，且不加入 Admin 站点设置白名单。

## 修改范围

预计仅修改 `model/`、`controller/`、`router/`、`common/` 中与 Key 投递和个人支付入口有关的现有文件，并新增确定性 SQLite 测试。实现前先将“未决项”写回本计划与技术设计。

## 风险与回滚

- Key 与邮件属于认证高风险链路：事务提交后旧 Key 已失效，SMTP 失败只能重发同一新 Key，不能回滚为旧 Key。
- 冻结开关默认关闭；在 C2/D/B/E/UAT 同批通过前不得开启，避免 Root/Admin 已有 Owner 关系时误断个人支付。
- 不连接真实 SMTP、支付商或 UAT；不提交、合并或部署。

## 实施步骤

1. 建立 Token 绑定的待投递记录、选定 Token 轮换与重发不变量；Root/Admin 匿名找回通过邮件中的服务端绑定目标确认链接选择 Key，不接收客户端任意 `token_id`。
2. Root/Admin 在自己的每一条 Key 行提供安全验证后的“重置此 Key”入口；新 Key 仅以内存中的一次性弹窗展示，随后强制重新登录。
3. 用现有 Option/common 模式建立默认关闭的 `EnterpriseBillingEnabled` 共同发布门与统一“个人资产已冻结”模型判断，并逐路由覆盖兑换、全部个人充值下单、全部个人订阅购买入口，以及会增加/转入个人额度的签到和推广额度转入。已审计当前路由：不存在用户自助订阅续费入口；现有续期是后台定时任务，支付回调、return/notify 和 Admin 管理入口不拦截。
4. 添加 Key 选择、待投递重发、成员状态矩阵和支付调用前拒绝测试。
5. 运行定向 Go 测试、静态检查；MySQL/PostgreSQL/SMTP/UAT 如未运行明确记为 `NOT_RUN`。

## 验证与通过条件

- 目标 Token 的轮换只改变一行且旧 Key 不可用。
- 失败重发不创建第二条 Token，也不产生第二次轮换。
- SMTP 失败后的 `PENDING_DELIVERY` 重发统一由服务端原子执行 `next_attempt_at` 冷却；冷却内不调用 SMTP，不能依赖 5 分钟安全证明重复发送完整 Key；并发重发最多一个请求进入 SMTP。
- 冻结开启后每个个人支付写入口都在外部支付调用和个人余额写入前被拒绝。
- `gofmt`、`git diff --check` 通过；未执行环境不冒充已验证。

## 结果与未解决项

- 2026-08-17：本切片以 `a73d550f3` 提交并受控整合。`EnterpriseBillingEnabled` 仍为 Root-only、默认 `false`；未推送、未部署。

- 已实现 Root/Admin 的邮件 Key 选择：每把现有 Key 各有一条服务端 `AuthFlow.Payload` 绑定目标的短时确认链接；确认请求只提交 `email` 与不透明 Flow Token，后端不接受可改目标的 `token_id`。
- 已实现 Root/Admin 每一把 Key 的行内轮换入口：仅在单主 Key 模式下展示，必须完成 2FA/Passkey 安全验证；新 Key 只驻留在组件内存的一次性对话框，确认保存后清除认证并跳转登录。
- 已审计并冻结所有当前用户自助个人资产写入口：兑换、Epay/Stripe/Creem/Waffo/Waffo Pancake 充值下单与金额请求、推广额度转入、签到，以及余额/Epay/Stripe/Creem/Waffo Pancake 订阅购买。仓库不存在用户自助“订阅续费”路由；`service/subscription_reset_task.go` 是后台定时重置，支付回调、return/notify 和 Admin 管理路由保持不拦截。
- 已添加 SQLite 确定性测试与路由枚举测试。`gofmt`、`git diff --check` 与受改前端文件 `oxlint` 已通过。前端 `typecheck` 因当前依赖目录缺少 `tsgo` 可执行文件而为 `NOT_RUN`；本轮定向 Go 测试未获得工具的可见完成输出，因此为 `NOT_RUN`。MySQL/PostgreSQL、真实 SMTP、支付商和 UAT 均为 `NOT_RUN`。
