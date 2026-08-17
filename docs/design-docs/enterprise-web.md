# 企业管理前端（F1）技术设计

**状态：** APPROVED（仅 F1；企业发布门保持关闭）

## 目标

为已整合的 H1 企业管理 API 提供最小、可操作且不扩权的已认证界面：企业摘要、Owner 成员管理，以及平台 Root/Admin 把合格普通用户设为企业管理员的入口。

F1 只消费现有 HTTP 契约：`GET /api/enterprise/self`、`GET /api/enterprise/members`、成员额度/状态/移除接口，以及 `POST /api/user/:id/enterprise-admin`。前端永远不是授权边界。

## 范围与非范围

### 范围

- 新增 `/enterprise` 路由与 `features/enterprise`，在一页内显示企业基本信息与当前成员关系。
- `/enterprise` 是侧边栏的固定独立入口，不接入既有 `SidebarModules` 的钱包/个人菜单开关；该展示决策不改变后端授权或发布门。
- Owner 额外显示企业钱包、成员表格、分配/回收额度、暂停/恢复、移除成员操作。
- 管理员用户列表为合格普通用户提供“设为企业管理员”操作，操作后刷新列表。
- 显示 API 返回的企业发布门关闭、无企业关系、状态与业务错误；请求中的完整 Key、个人钱包、订阅和其他企业数据均不渲染。
- 所有可见文本使用现有 i18n；复用既有 `SectionPageLayout`、数据表、Dialog/Drawer、Query 和 `api` 封装。

### 非范围

- 不修改 `EnterpriseBillingEnabled`，不以按钮隐藏代替后端权限，也不做生产发布。
- 不提供企业邀请、匿名注册/接受、邀请邮件链接、首次 Key 展示或邮件投递。现有完整设计把该链接路径和未注册交付契约标为 UNKNOWN，F1 不猜测。
- 不提供企业支付、订阅、白名单、企业账单/用量、历史 REMOVED 成员、所有权转移或人工排空处理。
- 不做管理/企业视图切换；它是展示上下文，不是 API 权限控制。

## 信息架构与交互

```text
/enterprise
  ├─ 未开启发布门：明确说明功能未启用，不显示可提交资金/状态操作
  ├─ 无企业关系：说明当前账户不属于企业
  ├─ Member：企业名称、成员状态、可用/预留企业额度
  └─ Owner：以上信息 + 企业钱包 + 成员表 + 管理动作

/users（Root/Admin）
  └─ 普通 USER："Set as enterprise administrator" 确认操作
```

- Owner 的分配、回收需要明确成员和额度；每次确认提交生成一个新的 `Idempotency-Key`，提交期间禁用同一表单，网络重试复用本次提交生成的键。
- 暂停、恢复、移除均使用确认对话框，明确目标用户名与影响。移除返回 `DRAINING` 时显示“仍在排空”，不宣称已变为个人用户。
- 成员列表可能包含 Owner；Owner 行只读，只有 `MEMBER` 行可出现成员管理动作。`DRAINING` 可再次请求检查并完成移除；`MANUAL_REVIEW` 只显示人工处理状态。
- 所有查询失败都保留用户可恢复路径：刷新、返回用户页或联系管理员；不展示内部错误、响应体中的敏感字段或跨企业对象存在性。

## 数据与 API 边界

前端定义显式 TypeScript 投影，字段必须与 H1 安全响应一致。成员表只使用 `membership_id`、用户名/展示名、角色、状态、加入/暂停时间、企业可用/预留额度；不得复用 `User` 全量模型。

| 前端动作 | API | 角色前置 | 前端约束 |
| --- | --- | --- | --- |
| 读取摘要 | `GET /api/enterprise/self` | Owner/Member | 响应按角色裁剪 |
| 读取成员 | `GET /api/enterprise/members` | Owner | 仅 Owner 渲染表格 |
| 分配/回收 | `POST .../allocations` / `reclaims` | Owner | `quota` 正整数、带 Idempotency-Key |
| 暂停/恢复/移除 | 对应成员接口 | Owner | 确认后刷新摘要和列表 |
| 设置 Owner | `POST /api/user/:id/enterprise-admin` | Root/Admin | 只在普通 USER 行提供；后端仍决定合格性 |

`ENTERPRISE_FEATURE_DISABLED` 是全页面受控关闭状态；`ENTERPRISE_OWNER_REQUIRED`、`ENTERPRISE_MEMBERSHIP_REQUIRED` 和成员范围错误均以接口返回为准。客户端角色仅用于减少无意义按钮，不能据此假定权限。

## 可访问性与响应式

- 全部动作使用具名按钮；额度字段有可见 Label、错误文字、键盘提交与 pending 状态。
- 危险移除操作在对话框中显示成员和排空影响，取消/关闭后焦点回到触发按钮。
- 窄屏成员表保留成员、状态、可用额度与动作；次要时间字段允许折叠但不隐藏状态或额度。

## 验收

1. 发布门关闭时，`/enterprise` 显示不可操作状态，所有写动作不可提交；直接构造 API 请求仍由后端返回 403。
2. Member 只能看到自己的企业摘要；Owner 仅看到本企业成员与钱包，不出现凭据、个人资产或跨企业数据。
3. Owner 每次额度提交只携带一次生成的幂等键；重复点击不会产生并行提交，重放结果能显示。
4. 暂停/恢复/移除后刷新服务端状态；DRAINING/错误不会被前端伪装成成功。
5. Root/Admin 用户管理页的 Owner 设置入口不改变用户平台角色展示，并处理后端拒绝。
6. 新文案在全部现有 locale 有键值；`bun run typecheck`、`bun run lint`、相关前端测试与构建按环境可运行情况记录。

## 实施边界

实现仅改前端路由、feature、导航和用户管理动作。若现有 API 与本设计不一致，优先收缩 F1 到已存在的安全接口；不得为页面便利新增或开启后端能力。
