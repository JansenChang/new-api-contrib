# 企业管理核心 API（H1）执行计划

状态：ACTIVE（代码与本地定向回归完成；等待隔离 UAT）
负责人：Codex  
更新时间：2026-08-17

关联设计：[企业管理核心 API（H1）](../../design-docs/enterprise-management-core-api.md)。

## 目标与非目标

在独立分支交付受 `EnterpriseBillingEnabled` 保护的 Owner 指派、企业摘要、成员列表、真实额度划转/回收和成员生命周期 API。门默认保持关闭。

不实现企业邀请邮件/注册交付、支付、订阅、Key、前端、发布门变更或生产部署。

## 现状证据

- B 已有成员暂停、恢复、排空移除与 Owner 三重一致性校验。
- C1 已有事务化额度划转、回收、账本幂等和冲正。
- H1 缺公开 Router/Controller、普通 User → Owner 原子指派，以及安全投影查询。

## 修改范围

预计只修改 Router、一个企业发布门 middleware、企业管理 model/service/controller、审计模板与相应定向测试；复用 B/C1，不直接更新额度汇总。

## 风险与回滚

- 门关闭路由仍可写：每个路由测试 403 与无副作用；不启用门。
- 越权/跨企业读取：每次目标查询都绑定企业、成员角色和状态。
- 资金不一致：仅调用 C1 命令，以幂等键回放或冲突拒绝。
- 排空错误：仅调用 B，保持 DRAINING/人工处理，不改个人资产。

## 实施步骤

1. 根据 H1 规格补充定向失败测试。
2. 实现发布门、Owner 指派和安全投影。
3. 实现 Controller/Router/审计，保留关闭门。
4. 运行定向 Go 回归、`git diff --check`；记录 SQLite、MySQL/PostgreSQL 与 UAT 的实际证据或 `NOT_RUN`。
5. 自审 FR/AC，回写本计划和设计，独立提交后再申请整合。

## 验证命令与通过条件

```bash
go test ./model ./service ./controller ./router -run 'TestEnterprise(Management|Membership|Ledger|AssetFreeze|Usage)' -count=1 -timeout 120s
git diff --check
```

通过：每条 H1 路由关闭门无副作用；跨企业隔离；账本幂等；状态不触及平台身份/个人资产；未运行的跨数据库/UAT 明确标注。

## 结果与未解决项

- 2026-08-17：H1 规格已从完整 H 中分离，以避免猜测企业邀请的邮件和建户交付契约。
- 2026-08-17：已实现独立 Owner 指派、安全摘要/成员投影、额度划转/回收包装、暂停/恢复/排空移除 HTTP API、关闭门和脱敏审计；所有资金写入仍经 C1，不直接更新额度汇总。
- 2026-08-17：根据权限审计补充 C1 事务内 Owner 三重一致性（用户锚点、企业 Owner/ACTIVE、ACTIVE Owner membership），并在用户禁用、软删除、硬删除前拒绝仍拥有企业的 Owner，防止无主企业；旧数据库缺企业表时生命周期保护按无企业处理。
- 已通过：`go test ./model -run 'Test(SetEnterpriseOwnerCreatesOnlyEnterpriseRelationship|EnterpriseManagementQuotaUsesC1AndReplaysByOperationKey|EnterpriseManagementReadsRequireConsistentOwnerEnterpriseAnchor|EnterpriseLedgerRejectsInconsistentOwnerAnchor|EnterpriseMemberProjectionDoesNotExposeUserCredentials|EnterpriseOwnerLifecycle)' -count=1 -timeout 120s`；`go test ./middleware ./service ./controller ./router -run 'TestEnterprise' -count=1 -timeout 120s`；`git diff --check`。
- 基线验证：全量多包回归失败于既有 `controller/user_manage_test.go` 的会话撤销/同级权限断言；相同精确测试在未改动基线工作树 `codex/enterprise-management-core-spec` 亦失败，非本切片引入。完整 `./model` 亦存在既有单主 Key 测试失败，未据此修改无关行为。
- 2026-08-17 隔离 UAT：已从提交 `b7657695d` 构建 `new-api-pg-uat:b7657695d`，仅替换 SSH `158` 的专用 `new-api-pg-uat-app` 容器；旧 UAT 容器以 `new-api-pg-uat-app-rollback-3b7d220b8` 保留为停止状态。新容器在既有专用 PostgreSQL、网络和 `/docker/new-api-pg-uat/runtime-data` 挂载上完成迁移并启动；容器内 `GET /api/status` 返回 `success:true`。未触碰生产容器、卷、网络、端口或配置。
- `NOT_RUN`：MySQL 运行回归、打开门后的合成 Owner/Member UAT E2E、真实 SMTP/支付/前端、生产部署。企业发布门仍保持关闭；由于 UAT 网络为 internal bridge，主机 3002 端口不作为本次健康证据，改以容器内 HTTP 检查验证。
