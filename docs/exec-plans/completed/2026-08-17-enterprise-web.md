# 企业管理前端（F1）执行计划

状态：COMPLETED

负责人：Codex

更新时间：2026-08-17

## 目标与非目标

实现已认证企业摘要、Owner 成员管理和平台用户页 Owner 设置入口；复用 H1 API，且发布门保持关闭。

不实现邀请注册/投递、支付、订阅、白名单、账单、历史成员、视图权限切换或生产部署。

## 现状证据

- H1 已提供关闭门的摘要、成员、额度、成员生命周期与 Owner 指派 API。
- 现有前端已有 `SectionPageLayout`、TanStack Router、React Query、DataTable、Dialog 和 `api` 请求封装。
- 企业邀请落地页/未注册交付契约仍为 UNKNOWN，不能在本片猜测。

## 修改范围

- `web/src/features/enterprise/`：API 投影、查询/操作、页面与小型确定性测试。
- `web/src/routes/_authenticated/enterprise/`：企业页面路由。
- 已有侧边栏配置：仅增加企业入口。
- `web/src/features/users/`：安全的 Owner 设置 UI。
- 各 i18n locale：F1 新文案。

## 风险与回滚

- 前端误显示权限：仅作展示优化，全部以后端 HTTP 结果为准。
- 资金重复提交：每次确认生成键、pending 禁用、同一提交的网络重试复用键。
- 发布门误开：本片不写 Option/配置；API 403 只显示状态。
- 已有脏工作区：只在 `codex/enterprise-web` 独立工作树提交，不改主工作区未提交文件。

## 实施步骤

1. 添加显式 API 类型和受控错误映射，先以测试覆盖提交幂等键和关闭门 UI 状态。
2. 实现企业摘要、Owner 成员表与额度/状态动作。
3. 在用户表加入 Owner 设置确认动作。
4. 添加路由和导航，补齐 i18n。
5. 执行 typecheck、lint、相关测试、构建；回写实际结果和 NOT_RUN。

## 通过条件

- 不存在任何完整 Key、个人资产、订阅或跨企业成员的前端投影。
- Owner 管理写操作具备确认、pending 防重和 `Idempotency-Key`。
- 关闭门、无关系、Member、Owner、DRAINING 和常见错误均有可理解的状态。
- `git diff --check` 通过；前端验证结果明确记录。

## 结果与未解决项

- 已实现 `/enterprise` 已认证路由、固定侧边栏入口、企业摘要和 Owner 成员列表；成员操作只对 `MEMBER` 展示。
- 已实现分配、回收、暂停、恢复、排空移除和 Root/Admin 指派企业管理员入口。额度提交复用同一次提交的 `Idempotency-Key`，发布门在读取或写入接口返回 `ENTERPRISE_FEATURE_DISABLED` 后进入全页受控关闭状态。
- 审计修复：Owner 行只读；`DRAINING` 可重试完成移除；`MANUAL_REVIEW` 只提示人工处理；成员列表按 20 条分页；用户页业务错误只显示一次。
- 已手工同步 TanStack Router 生成的路由树；当前依赖目录缺少 `rsbuild`，无法由构建器重新生成，后续具备依赖环境时应重新生成并确认无差异。
- 通过：`bun run i18n:sync`（全部 locale 缺键为 0）、全部 locale JSON 解析、`git diff --check`。
- NOT_RUN：`bun run format:check`、`bun run lint`、`bun run typecheck`、`bun run build` 均因 `web/node_modules/.bin` 缺少 `oxfmt`、`oxlint`、`tsgo`、`rsbuild` 而未能执行；未安装依赖。`bun run copyright:check` 仅报告三个既有非本切片文件的头部待更新。
- NOT_RUN：浏览器 E2E、真实企业 API、MySQL/PostgreSQL/UAT、生产发布；企业发布门未修改且保持关闭。
