# 平台特权角色同级保护：切片结果

状态：已提交并受控整合

负责人：Codex

更新时间：2026-08-17

## 目标与范围

落实已确认的权限边界：Root 与 Admin 在平台业务管理上平级，但任何平台特权账号都不能通过用户管理、认证管理或身份管理接口修改、禁用、删除、升降级或清理另一平台特权账号。

本切片只修改共享的目标用户角色判定，不分类或下放现有 Root-only 平台治理路由，不实现企业接口，也不改变 Token 自助管理语义。

## 实施

- `controller/user.go`：共享判定仅允许 Root/Admin 管理普通 User；目标为 Root/Admin 一律拒绝。删除接口同步复用该判定，避免 Root 对 Admin 的硬删除旁路。
- `controller/user_manage_test.go`：覆盖 Root/Admin 对普通 User 的允许路径、Root/Admin 两两组合与普通 User 对普通 User 的拒绝路径，以及删除端点对四种特权目标组合的拒绝和无副作用。

共享判定已由用户资料、资料更新、全局状态/删除/升降级、管理员 2FA、Passkey 与管理员 OAuth 绑定管理复用，因此这些写入入口使用同一保护边界。用户列表/搜索仅保留现有业务管理所需的基础资料可见性，不属于本次“禁止修改认证和身份”的边界；它们继续不返回密码和访问令牌。

## 验证

已通过：`go test ./controller -run '^(TestCanManageTargetRoleRejectsPrivilegedTargets|TestDeleteUserRejectsPrivilegedTargets)$' -count=1`

已通过：`git diff --check`

独立审阅发现的删除旁路已修复并复审通过。2026-08-17 已提交为 `4123daa37`，并合入 `codex/single-primary-api-key-clean` 的受控集成提交 `bc78a4b0e`；干净集成工作树已复跑定向 controller 测试和 `git diff --check`。

## 未解决项

- Root-only 路由事实分类已完成；完整渠道 Key 保持 Root-only，受限站点设置白名单已获确认，后续以独立切片实现，未在本片顺手扩大 Admin 路由权限。
- 本切片不替代后续企业 Owner 的资源级授权；企业管理员不写入 `User.Role`。
