# PostgreSQL 主库一次性复制器（P-M2）技术设计

**Author:** Codex
**Date:** 2026-08-18
**Status:** DRAFT — 仅允许合成 SQLite/MySQL fixture 开发；不得读取或复制生产数据
**Reviewers:** 应用负责人、数据库运维、安全负责人、计费负责人

## Context（背景）

生产 `new-api` 的本次容器启动日志已报告未配置 `SQL_DSN`，因此当前主库类型有运行时证据为 SQLite；其 SQLite 文件、WAL 状态、规模、日志库身份、写入者和备份方式仍未读取或确认。已采集的 34 表只读元数据基线见[SQLite-34 源 Schema 签名](postgresql-sqlite34-schema-signature.md)；它只用于编译期固定 `TableSpec` 比对，不能动态扩大导入范围。项目具备 SQLite、MySQL 与 PostgreSQL 的运行驱动，但没有跨库实体复制器。`AutoMigrate` 只能建立/演进当前库 schema，并会执行认证版本、企业关系、账本索引和支付主体回填，不能被当作历史数据导入器。

P-M2 的目标是提供一个运营者显式运行、一次性的 SQLite/MySQL → 空 PostgreSQL 主库复制与验证工具。它不是应用启动功能、不提供 HTTP 接口、不做双写或 CDC，也不改变现有 SQLite/MySQL 运行支持。只有完成合成数据的三方言合同后，才可进入独立 UAT 演练；真实生产复制、停写和切换仍需单独人工授权。

企业账本新写入已不再持久化 NUL，但历史 SQLite/MySQL 可能存在旧 V1 NUL 摘要。首版复制器不推测或转换任何含 NUL 的历史值：它必须失败关闭并输出非敏感定位。经审阅的 V1→V2 专项转换只能作为后续独立分片，不能混入本工具。

## Functional Requirements（功能需求）

- FR-1: 复制器 MUST 是独立命令，只接受显式的非敏感 manifest 路径及由受管环境注入的源/目标连接信息；不得从应用启动、HTTP 路由或 `AutoMigrate` 自动触发。
- FR-2: 复制器 MUST 仅接受 SQLite 或 MySQL 作为源、PostgreSQL 作为目标；源与目标标识相同、目标非 PostgreSQL、源类型未知、日志范围未声明、目标非空或源无法以只读方式连接时，MUST 在建立 schema/写入前拒绝。
- FR-3: 复制范围 MUST 使用候选 SHA 固化的 40 张主库 allowlist、固定表/列签名、固定列映射和固定依赖顺序；SQLite metadata 只可与该编译期签名比较，绝不用于动态扩展 allowlist。不得通过动态表发现、`SELECT *`、GORM 结构体自动扫描或“跳过未知列”扩大范围。
- FR-4: `logs` MUST 由 manifest 明确标记为 `primary`、`separate-retain`、`separate-migrate` 或 `clickhouse-retain`。未声明或与实际 `LOG_SQL_DSN` 拓扑不一致时 MUST 拒绝；ClickHouse 不属于复制器。
- FR-5: 复制器 MUST 先在空专属 PostgreSQL 中以候选版本的确定顺序建立 schema，并保存表、列、主键、索引和 sequence 的非敏感清单；不得直接使用会写入业务数据的应用启动迁移流程建立目标。
- FR-6: 每条来源记录 MUST 在写入前按目标列验证可空性、整数范围、布尔语义、时间精度、十进制精度、字符串长度、UTF-8、NUL 和 JSON。未知表/列、类型不兼容、非法值或目标不支持的来源语义 MUST 失败关闭，不得截断、替换、置零或跳过。
- FR-7: 所有 text/varchar/JSON/TEXT 载体 MUST 拒绝原始 NUL、非法 UTF-8、超目标长度；JSON 还 MUST 解析有效且递归拒绝字符串值中的 `\\u0000`。`NULL`、空字符串、`{}` 与 `[]` MUST 保持可区分。
- FR-8: 企业账本、用量、支付主体快照和成员关系 MUST 保持原始主键、时间、金额/配额、引用及历史归属。任何 `enterprise_ledgers.command_summary`、持久化摘要或相关字段含 NUL 时 MUST 拒绝本次复制；不得猜测旧命令、补造摘要或重算历史业务金额。
- FR-9: 复制器 MUST 显式写入主键与时间字段，并在导入后从 PostgreSQL catalog 确认每个自增列的关联 sequence；`nextval` MUST 严格大于目标最大 ID。空表、非自增表、复合主键和无关联 sequence MUST 分别处理，不得猜测序列名或插入业务测试数据。
- FR-10: 复制器 MUST 在导入后验证逐表精确行数、主键集合摘要、逻辑引用、目标 schema/索引清单和关键聚合。关键聚合至少包括用户/Token 额度、订单/订阅状态、企业钱包/成员可用和预留/异常额度、企业账本 delta 与用量状态；任一不一致时目标 MUST 标记为不可发布。
- FR-11: 报告 MUST 仅包含候选 SHA、数据库类型、表名、计数、摘要、阶段、耗时和 `table/primary-key-hash/column/reason` 失败定位；MUST NOT 写入或输出 DSN、原始用户文本、Key、OAuth/SMTP/支付字段或数据库行内容。
- FR-12: 复制器 MUST 只在已验证停写的一致性源快照上运行。P-M2 的合成测试可自行构造 fixture；生产快照、写入冻结、备份和公开切换均不属于本分片。
- FR-13: 候选启动验证前，复制器 MUST 验证已知启动回填为零：所有 `users.auth_version >= 1`，每个非空 `users.telegram_id` 均已有唯一、同属该用户的 Telegram `external_identity_claims`。候选启动若仍产生任意 DDL/DML，目标不可发布；不得把这些运行时副作用混入复制器。

## Non-Functional Requirements（非功能需求）

- NFR-1：工具 MUST 对 SQLite、MySQL 5.7.8+、PostgreSQL 9.6+ 保持可构建；不得引入只适用于单一来源方言的无回退路径。
- NFR-2：每张表 MUST 以受控批次处理，内存上界由 manifest 批次大小限定；不得一次读入未知规模的整张生产表。
- NFR-3：任一验证失败 MUST 在当前表事务回滚并使运行结果不可发布；不会删除或修改源，已写目标只能由后续明确的隔离清理流程处理。
- NFR-4：工具日志、报告和测试快照 MUST 不包含秘密或个人原文；错误定位只允许主键哈希。
- NFR-5：同一 manifest 与相同源快照的重复预检 MUST 产生稳定的 schema/表范围结论；导入本身不是可自动重试的业务幂等操作，失败后 MUST 新建空目标再重跑。

## Acceptance Criteria（验收标准）

### AC-1: 来源与目标拒绝 (FR-1, FR-2, FR-3, FR-4, FR-5)

Given 来源不是 SQLite/MySQL、目标不是空 PostgreSQL、日志范围缺失或目标已有业务行，When 运行预检，Then 工具在任何 DDL/DML 前拒绝，报告只说明失败类别。

### AC-2: SQLite 合成复制与候选静态启动 (FR-3, FR-5, FR-6, FR-9, FR-10, FR-13)

Given 当前候选模型生成的合成 SQLite fixture 与空 PostgreSQL，When 执行复制并通过 `auth_version`/Telegram claim 预检后启动候选，Then allowlist 内的表、显式主键、空值、时间、关键聚合、引用和 sequence 都精确通过，候选启动不产生 DDL 或 DML；不满足已知启动前置条件时必须在启动前拒绝。

### AC-3: MySQL 合成复制 (FR-2, FR-6, FR-9, FR-10, NFR-1)

Given 真正 MySQL 驱动创建的等价 fixture，When 执行复制，Then bool、datetime、JSON、decimal、大小写唯一性和 sequence 验证通过；不得以 SQLite 结果替代该证据。

### AC-4: 值失败关闭 (FR-6, FR-7, FR-8, NFR-3, NFR-4)

Given 任意 allowlist 值含 NUL、非法 UTF-8、`\\u0000` JSON 字符串、非法 JSON、整数/decimal 溢出、零日期、未知列或历史账本 NUL 摘要，When 扫描或导入，Then 当前表不提交、目标不可发布，报告不含原始值。

### AC-5: 日志范围 (FR-4)

Given manifest 标明独立日志库保留或 ClickHouse 保留，When 执行主库复制，Then `logs` 不被误复制且报告注明范围；Given 未声明范围，Then 预检拒绝。

### AC-6: 企业历史一致性 (FR-8, FR-10)

Given 含企业钱包、成员关系、账本、用量与支付主体快照的合成来源，When 导入并验证，Then Owner 锚点、成员引用、可用/预留/异常额度、账本 delta、用量状态和幂等唯一键均一致。

### AC-7: 非敏感报告 (FR-11, NFR-4)

Given 合成数据中含可识别的 Key、邮件和失败值，When 预检或导入失败，Then 报告只包含主键哈希与原因码，不包含任何原始敏感值或 DSN。

### AC-8: 一致性来源门禁 (FR-12)

Given manifest 未证明来源为固定的合成 fixture 或经批准的一致性快照，When 运行复制器，Then 工具拒绝导入并报告缺失的快照证明。

## Edge Cases（边界情况）

- EC-1: SQLite 存在 WAL/SHM 或源仍可写时，拒绝以单个文件复制代替一致性快照。
- EC-2: MySQL 出现 unsigned 超范围、零日期、非 UTF-8 字符集或大小写不敏感唯一值冲突时，失败关闭。
- EC-3: 目标 schema 的自增列没有可验证 sequence、或 sequence 不在预期 catalog 关联时，停止，不推断名称。
- EC-4: 源有 allowlist 外业务表、allowlist 内缺列或候选 schema 多列时，停止并报告对象名，不自动忽略。
- EC-5: 企业账本 V1 NUL 摘要、未知版本摘要或指纹异常时，停止；该运行不实施转换。
- EC-6: 复制后候选启动尝试额外 DDL、Root/Admin 自动补建或历史回填导致数据改变时，目标不可发布并转入专项审阅。
- EC-7: 日志库为 ClickHouse 或独立库而 manifest 没有明确策略时，停止主库复制。
- EC-8: 已失败目标含部分数据时，不对其就地重试或继续写入；保留非敏感报告，使用新的空目标重新演练。

## API Contracts（API 契约）

N/A — 工具不注册 HTTP、Relay 或管理 API。后续 CLI 只接受受管 manifest 文件路径与环境注入连接信息；完整命令行字段在实现分片前单独评审，且不得允许 DSN 作为可记录命令行参数。

## Data Models（数据模型）

| 对象 | 字段/约束 | 敏感性 |
| --- | --- | --- |
| `MigrationManifest` | 候选 SHA、源/目标类型、日志范围、allowlist 版本、批次大小、目标标识哈希；不含 DSN | 非敏感审计输入 |
| `TableSpec` | 表名、固定列名/类型/可空性/长度/主键、依赖序号、sequence 策略、JSON 规则 | 编译期固定，不接受动态扩展 |
| `MigrationReport` | 阶段、表计数、摘要、聚合、schema/sequence 结论、失败定位 | 不含原始行、Key 或秘密 |
| `FailureLocation` | 表名、主键哈希、列名、原因码 | 不含来源正文或主键原值 |

## SQLite-34-pre-enterprise 固定映射

本 profile 只对应 2026-08-18 已只读确认的生产基线：同一主/日志 SQLite 中恰有下列 34 张表，`logs` 属于主库范围；没有企业六表，`users` 没有 `active_enterprise_id`，`top_ups` 与 `subscription_orders` 没有支付主体快照列。完整的来源列/索引签名由 [SQLite-34 schema signature](postgresql-sqlite34-schema-signature.md) 固化；P-M2 必须将其转录为编译期 `TableSpec`，不能在运行时从该文档或来源 metadata 生成规则。它不是通用的“旧 SQLite”兼容模式。

预检必须同时满足下列条件：

1. 源表名集合与下表 34 项完全相同；缺表、额外表、企业表或任一新增列都拒绝。
2. 每张旧表只允许 profile 编译期 `TableSpec` 固化的旧列签名（列名、SQLite 声明类型、`notnull`、主键序位、目标类型/长度/精度、索引和 sequence 策略）；同名旧列按名称显式读取、显式写入，保留原主键、`NULL`、时间和所有既有值。SQLite metadata 仅验证这一签名，不能生成或扩大它。不得使用 `SELECT *`、目标默认值、运行时 `AutoMigrate` 回填或当前时间替代来源值。
3. 仅本节列出的 7 个目标新增列可由 profile 受控填充；其余任何目标列差异均拒绝。预检失败时目标不得执行 DDL 或 DML。
4. 此 profile 只接受 `source_type=sqlite`、`log_scope=primary` 和候选 SHA `e451c93f1d44a1a84f9bab07937510458c8bd642`；来源/目标身份哈希及快照证明标识均须非空，来源与目标身份哈希不得相同。MySQL、独立日志库或其他候选 SHA 必须新增独立审阅 profile，不能复用本 profile。

| 34 张来源表 | 目标处理 |
| --- | --- |
| `abilities`、`auth_flows`、`authz_roles`、`casbin_rules`、`channels`、`checkins`、`custom_oauth_providers`、`external_identity_claims`、`logs`、`midjourneys`、`models`、`options`、`passkey_credentials`、`perf_metrics`、`prefill_groups`、`quota_data`、`redemptions`、`setups`、`subscription_plans`、`subscription_pre_consume_records`、`system_instances`、`system_task_locks`、`system_tasks`、`tasks`、`tokens`、`two_fa_backup_codes`、`two_fas`、`user_oauth_bindings`、`user_sessions`、`user_subscriptions`、`vendors` | 每个旧列同名显式复制；不补值、不重算、不重新生成 ID/时间。 |
| `users` | 所有旧列同名显式复制；额外按下表填充 `active_enterprise_id`。 |
| `top_ups` | 所有旧列同名显式复制；额外按下表填充 3 个支付主体快照列。 |
| `subscription_orders` | 所有旧列同名显式复制；额外按下表填充 3 个支付主体快照列。 |

候选目标的 40 表为上列 34 表，另加 `api_key_deliveries`、`enterprises`、`enterprise_memberships`、`enterprise_invitations`、`enterprise_ledgers`、`enterprise_usage_records`。这些新增表和新增列必须由 P-M2 的纯 schema 建立器和本 profile 写入；不得在导入后启动应用，以 `migrateDB()` 或 `migrateEnterpriseFoundation()` 隐式生成。

| 目标对象 | 固定来源/填充值 | 失败关闭条件 |
| --- | --- | --- |
| `users.active_enterprise_id` | 接受角色集合固定为 Guest=`0`、User=`1`、Admin=`10`、Root=`100`；Guest/User 为 `0`，每个 Admin/Root（包括禁用状态）为其 `users.id`，与同 ID 的目标企业一致。 | ID 非正数或不能作为目标 `int`；角色不在固定集合。 |
| `enterprises` | 只为来源 Root/Admin 各建一行：`id=owner_user_id=users.id`，`status=ACTIVE`，各额度/`closed_at=0`；`name=(display_name 非空 ? display_name : username) + " Enterprise"`；`created_at=updated_at=normalize(users.created_at)`。 | 名称为空、超过目标 `varchar(100)` 或时间/ID 不可表示；不得截断或使用当前时间。 |
| `enterprise_memberships` | 只为上述 Owner 各建一行：`id=enterprise_id=user_id=users.id`，`role=OWNER`、`status=ACTIVE`，所有额度/自限/生命周期计数为 `0`，`allow_ips=''`，`joined_at=normalize(users.created_at)`。 | 同一 Owner 推导出多行、任一引用不能与已复制 `users`/`enterprises` 对齐。 |
| `api_key_deliveries`、`enterprise_invitations`、`enterprise_ledgers`、`enterprise_usage_records` | 均为零行；基线没有可转换来源。 | 来源发现对应企业表/列、或实现尝试编造任何历史业务记录。 |
| `top_ups.billing_subject_type`、`top_ups.billing_subject_id`、`top_ups.billing_enterprise_id` | 对每一历史行固定为 `personal`、`user_id`、`0`。 | `user_id <= 0`、不可表示、未引用已复制 `users.id`，或任何旧列/主键/金额/时间在复制中改变。 |
| `subscription_orders.billing_subject_type`、`subscription_orders.billing_subject_id`、`subscription_orders.billing_enterprise_id` | 对每一历史行固定为 `personal`、`user_id`、`0`。 | `user_id <= 0`、不可表示、未引用已复制 `users.id`，或任何旧列/主键/金额/时间在复制中改变。 |

`normalize(users.created_at)` 是 profile 的唯一时间规则：来源 `NULL` 映射为 `0`，非 `NULL` 保留原值；不得调用 `common.GetTimestamp()`。这使对同一 SQLite-34 快照导入两个独立空目标时，Root/Admin 企业、Owner membership、用户锚点与支付主体快照完全一致。

### SQLite-34 profile 最小回归

P-M2 开发必须先实现以下合成 SQLite 回归；不得用生产快照替代。

1. `TestPostgresPrimaryMigrationSQLite34PreEnterpriseProfile`：34 表旧基线导入后，旧 34 表保留主键/字段，目标完整 40 表；仅 Root/Admin 有企业/Owner 关系，其他四张企业新增表为零行。
2. `TestPostgresPrimaryMigrationSQLite34BillingSnapshotBackfill`：`top_ups`、`subscription_orders` 的旧行保留主键、金额和时间，且三列严格为 `personal,user_id,0`。
3. `TestPostgresPrimaryMigrationSQLite34DeterministicAdminEnterprise`：同一 fixture 连续导入两个空目标，推导的企业/成员 ID、名称、时间、用户锚点完全相同；后续候选启动不得再修改这些行、支付快照、`auth_version` 或 `external_identity_claims`。`auth_version<1`、非空 Telegram 绑定缺少唯一 claim 的 fixture 必须在启动前拒绝。
4. `TestPostgresPrimaryMigrationSQLite34ProfileRejectsDrift`：缺任一旧表/列、列签名不匹配、出现企业表/新增列、或有额外表/列时，预检失败且目标没有 DDL/DML；支付表的 `user_id<=0` 或孤儿引用同样拒绝。
5. `TestPostgresPrimaryMigrationSQLite34ProfileRejectsUnrepresentableOwner`：Root/Admin 的 ID、名称或时间无法映射时失败关闭；覆盖 `users.created_at=NULL → 0`、非法角色拒绝和禁用 Admin/Root 仍确定性创建企业，绝不截断或采用当前时间。

## Out of Scope（非范围）

- OS-1: 生产 SQLite 文件、WAL、日志库、DSN、备份、写入冻结、流量切换和回退操作。
- OS-2: 双写、CDC、在线零停机复制、自动回切或对现有运行时数据库迁移逻辑的替换。
- OS-3: 历史 NUL 数据的自动替换或企业账本 V1→V2 转换；发现即失败关闭，另立专项设计。
- OS-4: ClickHouse 日志导入、文件/对象存储、SMTP、支付、OAuth、上游模型和真实用户 E2E。
- OS-5: 对生产 schema 的“兼容猜测”、未知表/列复制、清洗源库、删除目标或覆盖已有 PostgreSQL 数据。
