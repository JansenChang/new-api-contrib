# 企业账户、角色、计费与 API Key：执行计划

状态：ACTIVE（L2 设计通过结构校验；角色保护与企业基础 A 已整合，企业关系启用受发布门约束）

负责人：Codex

更新时间：2026-08-17

## 目标与非目标

目标：将已确认的产品规格 [`enterprise-accounts-and-billing.md`](../../product-specs/enterprise-accounts-and-billing.md) 落为可实施、可迁移、可审计的技术设计；把当前项目迁移到 PostgreSQL 的方案纳入独立切片。审阅通过后，在独立分支和隔离工作树中按切片实现，并在不触及生产的前提下以生产一致性快照建立 UAT 验收环境。

非目标：不重构既有个人计费；不把企业管理员加入 `User.Role` 数值层级；不在首版实现企业订阅、跨企业成员关系、前端作为授权边界或生产 PostgreSQL 切换。生产数据只可在获得明确快照授权后以只读、加密、隔离副本进入 UAT，绝不作为运行中的生产数据库挂载或连接目标。

## 现状证据

- `model.User.Role` 是数值平台角色，`Quota` 是当前个人余额；两者不能承载企业成员关系或企业钱包。
- `model.Token.RemainQuota`、`UnlimitedQuota` 可由当前 Token 管理接口更新，不能承载企业成员硬额度。
- `service.BillingSession` 与 `service.FundingSource` 当前只覆盖个人钱包/订阅，且存在信任额度旁路；企业路径必须独立处理。
- `model.Log` 可使用独立日志库或 ClickHouse；企业账务权威记录不能依赖新增 Log 列。
- `model.Task.PrivateData` 已有内部账务上下文字段，可保存任务创建时的企业计费快照。
- `model.AuthFlow`、`User.AuthVersion`、原 Token 记录轮换和邮件能力可复用；SMTP 与数据库提交不可构成一个原子事务。
- `model.InitDB()` 已可由 `SQL_DSN=postgres://` / `postgresql://` 选择 PostgreSQL，仓库开发 compose 已使用 PostgreSQL 15；`AutoMigrate` 只维护 schema，不负责把现有 SQLite/MySQL 数据复制到 PostgreSQL。

## 修改范围

### L2 文档（本阶段）

- `docs/design-docs/enterprise-accounts-and-billing.md`
- `docs/design-docs/index.md`
- 本执行计划

### 预期源码范围（后续切片；以每片实现计划为准）

- `model/`：企业、成员、邀请、账本、使用记录、投递记录和必要的兼容迁移。
- `service/`：企业授权、成员生命周期、企业账务和计费资金来源。
- `controller/`、`router/`：显式的企业与平台业务管理接口。
- `relay/`、任务结算：计费快照、预留、结算、退款和异常账。
- `web/`：仅在后端授权和账务闭环通过后加入企业界面与 i18n。

## 风险与回滚

| 风险 | 控制 | 回滚边界 |
| --- | --- | --- |
| 跨租户越权 | 所有企业资源由服务端的有效成员关系解析；`enterprise_id` 仅作目标校验 | 新接口、策略和前端入口可单独关闭；不回滚已产生的账务流水 |
| 账务双扣、负数或错退 | 同事务行锁、不可变流水、请求幂等键、企业专用预留/结算路径 | 以冲正流水修复，不修改或删除历史账 |
| 成员移除时在途任务结算错误 | `DRAINING` 排空、超时人工处理、任务快照与原企业退款 | 禁止强制移除，保留关系和资金直到人工处理 |
| Key 已轮换但邮件失败 | 持久化待交付记录，只能重发同一新 Key | 旧 Key 保持失效；继续受控重发，不生成第三把 Key |
| 迁移影响现有个人用户 | 新模型和新增列均兼容空值/零值；先只回填 Root/Admin；演练三数据库 | 新功能开关关闭；禁止回滚已生效账务迁移，改用兼容读取和前向修复 |
| UAT 误触生产 | 先只读识别主机 158 上的生产容器、端口、网络、卷、环境路径；UAT 使用独立项目名、端口、网络、卷、数据库、Redis 与配置 | 只停止/删除明确带 UAT 项目标识的资源；绝不重启、挂载或读取生产数据卷 |
| 生产快照泄露或 UAT 外发 | 生产侧仅做一致性只读快照；UAT 恢复后立即替换/失效 Token、渠道、SMTP、支付、OAuth 等外部凭据，并阻断到生产和公网业务目标的网络 | 删除明确的 UAT 副本与卷；生产源库从未被 UAT 写入 |
| PostgreSQL 迁移漏表、漏序列或引用断裂 | 迁移工具保留主键、按依赖顺序导入、重置序列，并以表行数/关键汇总/抽样引用检查验收 | 生产切换未授权；UAT 可销毁后重建，问题通过迁移工具前向修复 |

## 实施步骤

1. 完成 L2 设计，建立需求、数据、接口、验收与切片的可追溯关系。
2. 对权限、认证、数据库迁移、计费、支付、Key 邮件交付以及 PostgreSQL 数据迁移进行人工技术审阅；未通过不得开始对应高风险切片。
3. 每个切片在独立 `codex/enterprise-*` 分支和独立工作树实施；每片只实现其明确 AC，完成后回写本计划、L2 设计和产品规格的实现状态。
4. 先集成后端账务与授权，再开发前端；前端隐藏或视图切换不得取代后端校验。
5. UAT 前先做主机 158 的只读隔离检查；确认无生产重叠后才部署专用 UAT 实例。

## 切片与分支

| 切片 | 建议分支 | 交付范围 | 前置审阅 | 完成后必须回写 |
| --- | --- | --- | --- | --- |
| P（审阅未通过，待修复） | `codex/postgres-uat-migration` | 合成清单格式校验器；不得称为真实快照验证或 UAT 隔离门禁 | 数据库、数据保护、UAT 隔离 | PostgreSQL 迁移设计、真实运行证据、回滚边界 |
| A（已整合） | `codex/enterprise-foundation` | 企业/成员模型、跨库迁移、Root/Admin 幂等补建；已修复幂等索引和运行时建企业路径 | 数据库、权限 | L2 数据模型、迁移证据、产品实现状态 |
| B（已整合；发布门关闭） | `codex/enterprise-membership` | 企业邀请、接受、Owner 设定、暂停/恢复、企业授权 | 权限、认证、C/D/E 已通过 | 生命周期、接口与测试证据 |
| C1（已整合；多数据库/UAT 验证待完成） | `codex/enterprise-ledger` | 企业钱包、成员真实划转、不可变账本、订单计费主体快照与历史回填；无公开路由/回调改造 | 计费、支付、数据库 | 账务状态机、对账测试证据 |
| C2（已整合；发布门关闭） | `codex/enterprise-payment-dispatch` | 全部支付商下单快照、回调按快照统一分派、订阅 TopUp 镜像和回调幂等 | 计费、支付、C1 | 支付回调、快照与回归证据 |
| D（已整合；发布门关闭） | `codex/enterprise-relay-billing` | Relay/异步任务企业预留、结算、退款、异常账、排空移除 | 计费、可靠性 | 资金流、任务退款与故障证据 |
| E（已整合；发布门关闭） | `codex/enterprise-key-and-freeze` | 单 Key 约束、定向轮换、投递 Outbox、订阅冻结、成员个人支付拒绝 | 认证、Key、支付 | Key/冻结流程和安全测试证据 |
| F | `codex/enterprise-web` | 企业页面、账单可视化、状态与 i18n | 前端、可访问性 | 前端接口映射与构建证据 |
| G（部分完成） | `codex/enterprise-uat` | 基线 PostgreSQL 迁移演练和内部 HTTP 验证已完成；企业 E2E 未开始 | UAT 隔离、人工验收 | 部署拓扑、验收结果、已知限制 |

## 多 Agent 分工原则

- 设计与对抗性审计优先使用可用的最高能力模型（上限 GPT-5.6-Terra）。
- 目标清晰、范围受限的常规代码/测试切片可以使用 GPT-5.6-Luna；若当前运行环境不提供该模型，则记录实际使用模型，不以模型名阻塞实施。
- 任何 Agent 不得直接提交、合并、连接生产、修改生产配置或改变其他切片的文件。
- 切片间通过已审阅的 L2 契约衔接；发现规格外需求、公开接口破坏、账务/授权不确定性时暂停并记录问题，不自行补全。
- D 的额外开工门禁：不得把当前每请求生成的 `request_id` 当作扣费幂等键；必须先实现 Token 绑定的稳定 `Idempotency-Key`、独立企业结算事务和流式重试不重放语义。
- P/G 的额外开工门禁：生产快照/UAT 拓扑尚为 UNKNOWN；在只读检查确认前不得连接或复制。清洗清单必须覆盖 `tasks.private_data` 等 JSON/TEXT 敏感载体，任一清洗/隔离验证失败即销毁 UAT 副本。

## 验证命令与通过条件

### 设计阶段

```bash
python3 /Users/jansen/.agents/skills/spec-driven-workflow/scripts/spec_validator.py --file docs/design-docs/enterprise-accounts-and-billing.md --strict
git diff --check
```

通过条件：设计文件通过结构校验或明确记录工具不适用项；所有产品 FR 都有技术实现点和可测试 AC；未报告为已实现。

### 每个源码切片

- 运行该切片相关的确定性 Go 测试；账务、认证和并发路径必须覆盖异常与幂等场景。
- 受影响模块执行 `go test`；修改 `relaykit/` 时额外执行 `cd relaykit && GOWORK=off go build ./...`。
- 修改前端时执行 `cd web && bun run build`；所有新增可见文案完成 i18n。
- 数据库改动至少做 SQLite 运行验证，并记录 MySQL/PostgreSQL 的静态兼容性审查或实测证据。

### UAT

- 先输出 158 的只读隔离证据；UAT 的项目名、端口、网络、卷、数据库、Redis、环境文件与生产均不同。
- 若用户要求生产数据复制，先以只读一致性快照恢复到 UAT PostgreSQL；UAT 启动前必须完成外部密钥清洗、真实用户 Token 失效、专用 UAT 管理账户建立和网络出站阻断。业务 E2E 仍只使用合成账户与本地/模拟渠道。
- 验证邀请、接受、划转、调用、结算、暂停、排空移除、迟到退款、Key 重置和账单快照的完整链路。

## 结果与未解决项

- 2026-08-17：L1 规格已确认；L2 设计通过 `spec_validator.py --strict`（100/100）。
- 2026-08-17 切片 A：企业基础模型与平台特权角色保护已分别以 `a53cfbef2`、`bc78a4b0e` 受控整合。A 的定向 SQLite 测试通过；MySQL/PostgreSQL 企业模型实测为 `NOT_RUN`。
- 2026-08-17 切片 P：在 `codex/postgres-uat-migration` 独立工作树完成合成清单格式校验器，未整合。独立审阅否决其作为 UAT 安全门禁：清单可自报清洗、表/聚合/序列/隔离证据，无法覆盖真实 `options`、用户认证字段、2FA、渠道 JSON/TEXT 等敏感载体，也不能验证全表导入、引用完整性或真实网络隔离。它只能保留为离线合成格式 lint，真实 UAT 必须另有可复核的受控证据。
- 2026-08-17 L2 对抗审计：D 被阻塞在 Relay 请求 ID 非稳定、个人账务不是企业原子事务；P/G 被阻塞在主机拓扑 UNKNOWN 和任务 JSON 可能含渠道 Key。已将稳定幂等、独立结算和逐表/JSON 清洗写入 L2 门禁。
- 2026-08-17 隔离 UAT：主机 158 上已完成当前基线到独立 PostgreSQL 的清洗导入、逐表行数/序列核验与内部网络 HTTP 200。导入载体已删除，生产未修改；但 127.0.0.1:3002 未实际监听，企业功能尚未整合，不能宣称企业 E2E 或主机端口验收。详见 [`2026-08-17-postgres-uat-run-result.md`](2026-08-17-postgres-uat-run-result.md)。
- 2026-08-17 Root-only 路由只读清单：已将现有 33 条 Root-only 路由按业务管理、敏感业务动作、平台治理或 `UNKNOWN` 写入 L2；`/api/option` 仍须按 key 分类，所有未明确授权的 Root-only 路由保持 Root-only。
- 2026-08-17 产品确认：完整渠道 Key 继续仅 Root 可查看；`/system-settings/site` 的 Admin 后端白名单限定为 `SystemName`、`Logo`、`Footer`、`About`、`HomePageContent`、`Notice`、`legal.user_agreement`、`legal.privacy_policy`、`HeaderNavModules`、`SidebarModulesAdmin`。`ServerAddress` 与其他 Option 继续 Root-only。
- 2026-08-17 启用门禁审计：不得单独公开 Owner 指派、成员接受或暂停/恢复。只有 C（钱包/账本）、D（Relay/任务结算）、E（冻结与个人支付拒绝）和 B（关系启用）同批通过，才允许使任何 Owner/Member 成为企业资助调用主体。
- 2026-08-17 C 账务审阅：原 C 规格因账本全局幂等索引、业务引用去重、订单历史快照、企业支付入口边界和冲正方向未定义被阻断。现已拆为 C1（账本与快照基础）和 C2（支付回调分派），并在 C1 规格补充企业范围哈希唯一约束、引用哈希、历史订单回填、内部企业订单边界、冲正关联与 SQLite 条件更新规则；独立 Terra 复审已确认可以启动 C1 隔离实现。
- 2026-08-17 切片 C1：已以 `d94426c56` 受控整合企业账本内核和订单主体快照。整合后的静态审阅确认企业余额更新与账本写入使用同一外层事务；`gofmt` 与 `git diff --check` 通过。定向 Go 测试因本机工具链在构建阶段无输出被终止，记为 `NOT_RUN`；MySQL/PostgreSQL、真实支付回调和企业 UAT 也均为 `NOT_RUN`。C1 未注册企业公开入口，企业关系仍受 C/D/E/B 同批发布门限制。
- 2026-08-17 产品确认：企业邀请创建的新用户继续沿用 `QuotaForNewUser`。该默认赠额归用户本人，加入企业即冻结，不转入企业钱包或成员额度，Owner 不可见且不可回收；离开企业后按个人资产规则恢复可用。
- 2026-08-17 受控整合：E、C2、D、B 分别以 `a73d550f3`、`53afa0958`、`e7fadcff8`、`1e567160e` 提交，并整合为 `a73d550f3`、`4fdf91db3`、`63df9d6d7`、`6633b1e67`。D/E 的共同 `EnterpriseBillingEnabled` 定义在整合时收敛为一处，默认 `false`；未推送、未部署，未开启任何企业入口。
- 待人工技术审阅：敏感业务动作的 Root/Admin 平级授权、企业支付接入的现有订单/回调落点、当前生产库类型与规模、UAT 主机实际隔离拓扑、生产快照的脱敏/失效策略和可用测试凭据。
