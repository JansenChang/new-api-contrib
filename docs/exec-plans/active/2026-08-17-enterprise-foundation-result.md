# 切片 A：企业基础模型结果

日期：2026-08-17

分支：`codex/enterprise-foundation`

## 已实现

- 新增 `Enterprise`、`EnterpriseMembership`、`EnterpriseInvitation`、`EnterpriseLedger`、`EnterpriseUsageRecord` 和 `APIKeyDelivery` 主库模型及跨库可用的普通/唯一索引。
- `EnterpriseUsageRecord` 以 `(token_id, idempotency_key)` 作为稳定联合唯一键，保存请求语义摘要；`request_id` 只保留普通关联索引，不能用于扣费幂等。
- `EnterpriseMembership` 使用 `(enterprise_id, user_id)` 联合唯一索引；锚点 CAS 更新必须影响一行，否则整笔事务回滚，避免 SQLite 竞争时生成重复 Owner 关系。
- 补齐成员 `MANUAL_REVIEW`、企业邀请 `expected_role`、`REJECTED` 和 `rejected_at` 的持久化字段。
- 为 `User` 增加 `active_enterprise_id` 事务锚点；未进入企业的用户保持零值。
- 在 `migrateDB` 与 `migrateDBFast` 的既有模型迁移完成后，按固定顺序注册企业模型迁移。
- 修复独立执行基础迁移时的旧库兼容性：固定顺序首步迁移 `User`，确保缺少 `active_enterprise_id` 的既有 `users` 表先补列，再执行 Root/Admin 回填。
- 迁移后按用户 ID 顺序锁定 Root/Admin，幂等补建独立企业和 Owner 成员关系；已有 Owner 企业只补锚点/缺失成员，不创建第二企业。新建 Root/Admin 与普通用户升级为 Root/Admin 均在原 User 写入事务中调用同一补建入口。Owner 的 `billing_account_id` 和成员 `allocation_id` 由后续切片按企业/成员 ID 使用，Owner allocation 为 0。
- 检测 `active_enterprise_id` 指向其他用户企业的冲突并回滚本次迁移事务。

## 验证

- `go test ./model -run 'Test(Enterprise|Ensure)' -count=1`：通过。
- `go test ./model -run 'Test(MigrateEnterpriseFoundation|EnterpriseFoundation|EnsurePlatformAdmin)' -count=1`：通过，含由完整当前 `User` schema 生成、仅删除 `active_enterprise_id` 并保留既有 Root/Admin 数据的 SQLite 回归测试。
- `go test ./controller -run 'TestManageUserPromoteCreatesPlatformAdminEnterprise' -count=1`：通过。
- SQLite 确定性测试覆盖：联合唯一索引、请求幂等索引、成员状态与邀请字段、Root/Admin 与普通用户分离、二次迁移不重复创建企业/成员、CAS 冲突回滚、新建/升级管理员建立企业关系。
- `git diff --check`：通过。

## 未运行与边界

- `TEST_POSTGRES_DSN`、`TEST_MYSQL_DSN` 未配置，PostgreSQL/MySQL 实例测试 NOT_RUN；仅做 GORM 类型和索引的静态跨库审查。
- 未连接 SSH、未读取或使用生产数据、未新增路由/计费/支付/Key/前端逻辑。
- 未提交 Git commit；其他切片的成员生命周期、账本动作、Relay 结算、Key 轮换和企业 E2E 均未实现。

## 可能冲突文件

- 后续企业模型切片若扩展同一 `model/enterprise.go`，应保留本片的字段、表名和常量；迁移注册须继续位于既有用户迁移之后。
- 后续切片会依赖 `User.active_enterprise_id` 和本片模型，修改字段类型或唯一索引前需重新审阅跨库迁移。
