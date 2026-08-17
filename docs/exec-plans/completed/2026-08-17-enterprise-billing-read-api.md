# F2：企业账本与用量只读 API 执行计划

状态：COMPLETED（待受控整合）

负责人：Codex

更新时间：2026-08-17

## 目标与非目标

为现有企业账本和用量快照增加最小、安全的 Owner/Member 只读 API 与 `/enterprise` 可视化。不得改动资金、数据库迁移、发布门、支付、邀请、白名单或 Key。

## 现状证据

- `model.EnterpriseLedger` 与 `model.EnterpriseUsageRecord` 已持久化企业账务与调用快照，但包含不能对用户公开的内部字段。
- H1 路由已使用 `UserAuth`、`DisableCache` 和 `EnterpriseFeatureEnabled`；F1 已提供企业摘要与成员管理页面。
- 企业邀请、白名单和成员自设 Key 上限没有完整、已确认的 HTTP 契约，不纳入本片。

## 修改范围

- `model/enterprise_management.go` 或同一企业读取模型文件：显式只读 Projection 与范围查询。
- `service/`、`controller/`、`router/`：两条只读接口与统一授权。
- `web/src/features/enterprise/`：只读账本、用量请求及表格。
- `docs/design-docs/enterprise-billing-read-api.md`、本计划及必要 i18n。

## 风险与回滚

- 跨租户与敏感字段泄露：服务端解析当前关系、显式 Projection、确定性字段裁剪测试。
- 将删除成员历史误隐藏：Owner 查询只按企业范围；Member 查询必须受当前有效关系限制。
- 发布门误开：本片不写 Option；路由继续使用关闭门中间件。
- 回滚只移除读路由/前端入口；不修改历史账本或用量。

## 实施步骤

1. Terra 已完成权限与字段投影审阅，设计状态为 APPROVED。
2. 已在本独立工作树实现后端显式投影、授权、稳定分页与确定性 SQLite 测试。
3. 前端只读表格、分页、空状态、错误映射和全 locale 文案由并行前端切片负责；本切片未修改前端。
4. 已运行定向 Go、i18n 同步与 diff 检查；前端 typecheck/lint/build 因当前依赖工具不可用而明确记录为 NOT_RUN。
5. 独立产品/安全审计已通过；待主分支受控整合，不部署、不打开发布门。

## 验证命令与通过条件

```bash
go test ./model ./service ./controller ./router -run 'TestEnterprise' -count=1 -timeout 120s
cd web && bun run i18n:sync
cd web && bun run typecheck && bun run lint && bun run build
git diff --check
```

通过条件以设计文档“验收”章节为准。无法运行的命令必须记录原因，不得把静态检查或 HTTP 200 当作 PostgreSQL/UAT 或完整企业 E2E 证据。

## 结果与未解决项

- 已实现：`GET /api/enterprise/ledger` 与 `GET /api/enterprise/usage`，包含服务端关系解析、显式字段投影、同范围 COUNT、稳定排序和分页边界归一化。
- 已实现：Owner 账本范围、Owner 全企业用量范围、Member 当前 membership + actor 范围，以及 REMOVED/无关系拒绝。
- 已实现：确定性 SQLite 测试覆盖跨企业隔离、Owner/Member 组合、敏感字段序列化裁剪、同秒稳定分页和非法分页输入。
- 已验证：`GOTELEMETRY=off GOTOOLCHAIN=local go test ./model -run 'TestEnterpriseRead' -count=1 -timeout 120s` PASS。
- 已验证：`GOTELEMETRY=off GOTOOLCHAIN=local go test ./model ./service ./controller ./router -run 'TestEnterprise' -count=1 -timeout 120s` PASS。
- 已验证：`cd web && bun run i18n:sync` PASS；七个 locale 均无缺失或多余 key，locale JSON 解析 PASS。
- 已验证：`git diff --check` PASS。
- 已审计：独立产品/安全审计 PASS；最终投影不返回 membership、actor 或 Token 内部 ID，关系错误码与关闭门语义已复核。
- NOT_RUN：PostgreSQL/MySQL UAT、生产部署、前端 typecheck/lint/build、完整企业 E2E。尝试 Bun TSX 语法检查时当前依赖环境缺少可解析的 `react` 包；不自行安装依赖。
- UNKNOWN：邀请邮件链接、匿名受邀注册交付、企业白名单强制执行和成员自设 Key 上限写入语义，均不纳入本片。
