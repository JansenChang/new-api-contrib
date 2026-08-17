# 切片 B：企业成员与邀请

状态：`IMPLEMENTED_FOR_ISOLATED_DOMAIN_REVIEW`（未提交、未整合、未发布）

负责人：Codex

更新时间：2026-08-17

## 精确基线与范围

- 本分支开始时位于 `da559c451`，不含已整合的 C1。
- 为使用已审阅的账本基础，先将唯一未跟踪的本计划以含未跟踪 stash 临时保存，再执行 `git merge --ff-only b0bac6410`，随后恢复该计划；最终基线为 `b0bac6410`。未提交、未推送、未修改主线。
- 本片只新增 `model/enterprise_membership.go`、其 SQLite 确定性测试与设计/计划文档；未改 Controller、Router、前端、支付、Relay、Key 或部署配置。

## 已实现领域能力

1. 独立 `AuthFlowPurposeEnterpriseInvite=enterprise_invite`：企业邀请不复用平台注册 `user_invite`，payload 只保存邀请 ID；邀请固定为 `MEMBER`，调用方不能指定 Owner 或平台角色。
2. Owner 范围授权：创建/撤销邀请与成员暂停/恢复都验证 actor 的 `active_enterprise_id`、Enterprise ACTIVE、`owner_user_id`、ACTIVE Owner membership；所有目标查询带企业 ID。
3. 已有账户接受邀请：锁定用户与邀请，要求规范化登录账户邮箱完全等于邀请邮箱；在同一主库事务消费 AuthFlow、写 Member、CAS 写 `active_enterprise_id` 并标记邀请 ACCEPTED。
4. 未注册受邀者：用户在接受页自行填写用户名；领域命令只接受用户名，邮箱/企业/平台角色均从邀请固定取值，Token Group 只沿用服务端 `DefaultUseAutoGroup`。在同一事务创建普通 User、唯一主 Key、Member、锚点并消费邀请。若 `QuotaForNewUser > 0`，复用既有注册事务把默认赠额记为该用户个人资产；有效企业关系期间该资产冻结，不进入企业钱包或成员额度，Owner 不可见且不可回收；没有接入现有注册路由或邮件投递。
5. 成员移除：开始移除时转 DRAINING 并通过 C1 真实回收当前可用额度；没有在途 Usage 时同一事务移除并清除锚点。有在途 Usage 时保留 DRAINING；从 `draining_at` 起 24 小时后可转 MANUAL_REVIEW，所有 Usage 终态后才最终移除。生产超时入口固定读取服务端当前时间，测试时间注入仅保留包内未导出的辅助函数，不能由 Controller 提前触发。D 负责把迟到退款按原企业快照回原企业钱包。
6. 单企业关系：目标已有非 REMOVED 关系、非零锚点、平台特权角色或同企业历史关系时，邀请在同一提交中标为 `CONFLICTED` 并被消费，不创建第二关系。
7. 暂停/恢复只改 `EnterpriseMembership.Status` 与 `paused_at`。没有写入 `User.Status`、Token、个人额度或平台角色。

## 发布门

公共企业关系入口数量保持为 0。`EnterpriseBillingEnabled` 是 Root-only、默认 false 的共同发布门；只有 C1、C2、D、E、B 和隔离 UAT 完整通过后，未来 Router/编排层才可检查并开放功能。本片的模型函数没有当前调用者，不能构成绕过门禁的公开能力。

## 验证

- `gofmt -w model/enterprise_membership.go model/enterprise_membership_test.go`：通过。
- `git diff --check`：通过。
- 测试夹具已为 Owner/Member 分配确定性唯一 `AffCode`，不再触发 `users.aff_code` 空值唯一约束。随后运行 `go test ./model -run '^TestEnterpriseMembership' -count=1 -timeout 60s` 时，Go 构建阶段超过 60 秒仍无输出而被终止；模型测试整体仍记为 `NOT_RUN/本机构建环境`，不宣称通过。
- SQLite 测试已覆盖：用途隔离、已有账户邮箱匹配/不匹配不消费、未注册账户自填用户名并原子建户/建 Key、已有关系冲突、锚点写入、暂停不全局封号、非法重复状态、跨企业操作、无在途直接回收移除、在途排空与 24 小时人工处理。
- 新增确定性边界回归：未满 24 小时的包内测试时钟不得进入 `MANUAL_REVIEW`；生产导出入口不接受调用方时间参数。
- MySQL/PostgreSQL、SMTP、UAT、SSH/生产：`NOT_RUN`。

## 明确未实现的契约阻断

1. **Key 交付**：未注册受邀者创建的完整主 Key 仍须由 E/未来受保护编排层按已确认的首次 HTTP 一次展示及邮件投递规则交付；B 只返回内部 Token，绝不发送邮件或开放响应。
2. **迟到退款执行**：B 已固定排空 24 小时和关系状态；D 尚未实现。D 必须只从原企业 Usage 快照结算/退款到原企业钱包，B 不得改写个人资产或替 D 结算。
3. **同企业 REMOVED 成员再加入**：现有 `(enterprise_id,user_id)` 唯一索引不允许保留历史又创建新行；是否允许恢复原关系尚未确认。本片明确拒绝，不删除或复用历史行。
4. 成员列表是否包含 REMOVED 历史关系尚未确认；本片不新增读取投影。

## 回滚

不整合本分支即可；不删除已产生的企业关系历史，不执行数据库/生产回滚。
