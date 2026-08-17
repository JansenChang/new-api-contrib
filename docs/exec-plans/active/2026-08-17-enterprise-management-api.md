# 企业管理员设置与企业管理 API：执行计划（切片 H）

状态：DRAFT（仅文档；未开始源码实现）

负责人：Codex

更新时间：2026-08-17

关联技术设计：[企业管理员设置与企业管理 API](../../design-docs/enterprise-management-api.md)

## 目标与非目标

目标是在独立 `codex/enterprise-management-api` 分支与工作树中，把已整合的 B 企业成员领域和 C1 企业账本内核接入最小受控管理 API，并保持 `EnterpriseBillingEnabled=false`。交付仅在发布门开启后的可调用能力；本切片本身不改变发布门、生产或支付配置。

非目标：不做前端、企业充值、订阅、白名单、Key 读取/代管、支付/订阅回调、匿名企业受邀者注册、DRAINING 自动扫描、生产部署或 UAT 自动启用企业功能。

## 现状证据

- `model/enterprise_membership.go` 已有企业邀请、已登录接受、暂停/恢复、排空移除和 Owner 范围校验，但没有公开 Router/Controller。
- `model/enterprise_ledger.go` 已有真实 `AllocateEnterpriseQuota` / `ReclaimEnterpriseQuota`、不可变账本和企业范围幂等命令；Controller 不得直接更新余额字段。
- `model/enterprise_billing_gate.go`、切片 D/E 已以 `common.EnterpriseBillingEnabled` 表达共同发布门，默认 `false`；个人资产冻结与企业 Relay 均依赖该门。
- 既有 `/api/user` 已有 `AdminAuth` 平台用户管理路由，`/api/enterprise/*` 尚未注册；Root 与 Admin 的目标用户同级身份保护已由现有用户管理策略处理。
- 上述模型的跨数据库运行、真实 SMTP、企业完整 E2E 仍不能由当前已整合代码证明，实施后必须分项记录。

## 修改范围

| 层 | 预计最小改动 |
| --- | --- |
| `router/api-router.go` | 仅注册设计文档列出的企业管理路径，顺序为既有认证后、发布门再到 Controller。 |
| `middleware/` | 仅增加或复用一个默认拒绝的企业发布门检查；不得扩展 Option 权限。 |
| `controller/` | 新建或复用企业管理 Controller：DTO、机器错误码、安全响应、邮件调用和审计。 |
| `service/` | Owner 指派事务、邀请投递失败收口、Owner/Member 安全投影与自然幂等状态编排。 |
| `model/` | 仅补足事务性 Owner 指派、企业范围投影/查询、邀请与 AuthFlow 同事务失效；复用 B/C1。 |
| `docs/` | 回写本计划、技术设计与主企业执行计划中的实施/验证事实。 |

不修改 `relaykit/`、支付回调、订阅、Token/Key、前端和数据库连接配置。除为最小事务补充的模型测试外，不新增表或通用幂等框架。

## 风险与回滚

| 风险 | 控制 | 回滚边界 |
| --- | --- | --- |
| 门关闭时形成企业关系 | 所有路由在认证后统一检查发布门；测试显式设置并清理开关 | 不合并本分支；不在生产/UAT 开启开关 |
| Owner 越权或跨企业读取 | 三重 Owner 一致性验证；每次目标查询带 enterprise ID、成员角色和预期状态 | 删除/关闭新路由；已创建关系按既有生命周期处理，不硬删历史 |
| 额度双扣或负数 | 仅调用 C1 账本命令；分配/回收要求稳定 Idempotency-Key；不直接更新汇总 | 以 C1 冲正流程修复已提交账，不编辑账本行 |
| 邀请邮件失败留下可用链接 | 邮件失败后原子撤销邀请和 AuthFlow；失败收口失败要显式 5xx/audit | 不报告成功；人工只处理明确的 pending 记录 |
| 移除中错误恢复个人资金 | 只调用 B 的排空移除；不接触个人资产或 D 的退款快照 | 保持 DRAINING/人工处理，不强制清锚点 |
| 未确认落地页/超时调度被猜测实现 | 两项均保持 UNKNOWN，独立决策后再扩展 | 本切片不注册匿名注册路由、不加入后台扫描 |

## 实施步骤

1. 实施前再次核对当前主线的 B/C1/D/E API、模型状态常量、发布门唯一实现和 Router 顺序；若与本设计冲突，先更新规格，不直接找补代码。
2. 先写失败测试：发布门默认拒绝、Root/Admin 设置 Owner 原子性、Owner 企业隔离、成员状态、账本幂等、邀请投递失败收口和响应脱敏。
3. 实现最小模型/服务事务。Owner 指派只创建普通 User 企业关系；额度只复用 C1；接受只开放已登录用户路径；不实现匿名建户或完整 Key 返回。
4. 实现 Controller/Router，并在每条变更路径添加发布门、既有认证、DTO 上限、机器码和审计；确认敏感 Token/header 不进入日志。
5. 先运行 SQLite 确定性测试和路由枚举，再做 MySQL/PostgreSQL 运行验证。仅当这些验证满足主计划发布条件，才在隔离 UAT 使用合成 Owner/Member 测企业 E2E；发布门始终默认关闭，UAT 的临时测试开启必须有明确记录和回滚。
6. 完成后将真实命令、退出码、运行数据库、UAT 证据、NOT_RUN 和未知项回写到本计划、设计和主企业执行计划；本切片独立提交，等待人工审阅后才可申请整合。

## 验证命令与通过条件

### 文档阶段（本次）

```bash
python3 /Users/jansen/.agents/skills/spec-driven-workflow/scripts/spec_validator.py \
  --file docs/design-docs/enterprise-management-api.md --strict
git diff --check
```

通过条件：严格规格校验达到 80 分以上且无警告；所有 FR 都有 AC；最小 API、授权、错误、幂等、拒绝前提、数据来源与非范围可追溯。

### 源码阶段（后续，未运行）

```bash
go test ./model ./service ./controller ./router \
  -run 'TestEnterprise(Management|Membership|Ledger|AssetFreeze|Usage)' -count=1 -timeout 120s
git diff --check
```

通过条件至少包括：

- 门关闭时每条企业 API 无副作用；
- Root 与 Admin 都能设置符合条件的普通 User 为 Owner，不能修改特权目标；
- Owner 跨企业读写完全拒绝，响应不泄露敏感凭据；
- 邀请一次消费、邮箱匹配、投递失败收口与重复邀请无双发；
- C1 分配/回收的同键重放只产生一条账本流水，冲突键不改变余额；
- 暂停不全局封号，移除不在在途期间清除锚点；
- SQLite、MySQL、PostgreSQL 分别有运行证据。真实 SMTP、支付、前端和生产均不属于本切片验收。

## 结果与未解决项

- 2026-08-17：已完成技术设计与执行计划；`spec_validator.py --strict` 通过（100/100，无警告），`git diff --check` 通过。本次仅提交文档；未写生产代码、未注册路由、未改发布门、未合并、未推送、未部署。
- 阻断扩展而非本设计的问题：企业邀请前端落地页路径、未注册受邀者的主 Key 交付 API、DRAINING 24 小时自动触发器、REMOVED 历史成员可见性，均保持 UNKNOWN/待确认。
