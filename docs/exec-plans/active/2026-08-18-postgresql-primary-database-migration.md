# PostgreSQL 主数据库迁移：执行计划

状态：P-M0_PARTIAL（已完成一次只读运行拓扑盘点；生产数据库与写入边界仍未确认）

负责人：Codex  
关联设计：[PostgreSQL 主数据库迁移与生产切换](../../design-docs/postgresql-primary-database-migration.md)

## 目标与非目标

目标：在不移除 SQLite/MySQL 运行支持的前提下，建立 SQLite/MySQL 主库到 PostgreSQL 主库的可验证、可恢复、停写切换设计和后续实施门禁。

非目标：本切片不读取生产 DSN/数据/卷，不创建或修改迁移器，不执行 UAT/生产 DDL、复制、备份、容器变更或流量切换。

## 现状证据

- `model/chooseDB` 已按 `SQL_DSN` 前缀选择 PostgreSQL，`LOG_SQL_DSN` 可独立配置；`migrateDB()` 负责 schema 演进，不负责跨库复制。
- `docker-compose.yml` 和 `docker-compose.dev.yml` 使用 PostgreSQL 15，属于开发/示例配置，不能推断生产事实。
- 基线隔离 UAT 的清洗导入和内部 HTTP 200 已被记录，但结果明确排除生产切换、企业 E2E、MySQL 和真实外部系统。
- 企业账本 NUL 兼容修复已在独立提交 `df33b9ec9` 完成并通过专用 PostgreSQL 合同验证；候选整合和历史 NUL 数据转换仍是切换前置项。
- 2026-08-17 17:57Z 对 SSH 158 仅运行 Docker 容器、网络、挂载元数据和磁盘空间盘点：生产 `new-api`、`new-api-l1-test`、`mysql_db` 与 PostgreSQL UAT 资源可见；本次没有读取任何环境变量、DSN、数据库数据或卷内容，也没有执行写操作。
- 生产 `new-api` 的启动日志仅筛选并返回了数据库类型行：2026-08-15 的本次容器启动报告 `SQL_DSN not set, using SQLite as database`。随后只读取了不含业务行的运行元数据：容器工作目录为 `/data`，默认 `one-api.db` 存在，`SQLITE_PATH` 与 `LOG_SQL_DSN` 均未设置。因此生产**主库和日志库均为同一 SQLite 文件**，`logs` 属于本次主库复制范围。
- 对该 SQLite 文件以 `mode=ro`、`query_only=ON` 读取的元数据为：SQLite 3.40.1、`journal_mode=delete`、页大小 4096、18430 页、无 freelist、schema version 1236；文件约 75 MB。当前无 `.db-wal`/`.db-shm` 文件。这是单时点元数据，不等于一致性快照或停写证明。
- 源 schema 当前有 34 张表，名称与候选基础模型清单一致，包含 `logs`，但没有企业六表。候选目标的企业表/新增列和确定性历史回填已固定为 [`SQLite-34-pre-enterprise` 映射规范](../../design-docs/postgresql-primary-database-copy-tool.md#sqlite-34-pre-enterprise-固定映射)；P-M2 只能按该 profile 用合成 fixture 开发，不能让启动迁移猜测历史数据。
- `new-api-pg-uat-net` 是 `internal=true`、`attachable=false` 的本地 bridge 网络，当前仅有 PostgreSQL 和两个候选应用，均未发布宿主机端口；它与生产 `new-api`、L1 测试、OpenCodex、MySQL 没有直接共享 Docker 网络。路径和网络隔离已获容器元数据证据。
- 两个候选应用仍同时连接同一 UAT PostgreSQL 网络；Docker 元数据无法证明它们使用不同数据库、schema 或账号，因此不能排除并发 `AutoMigrate`/写入竞争。该 UAT 不得直接作为下一轮候选验收环境。
- `mysql_db` 虽在运行，但它与生产 `new-api` 未共享已见 Docker 网络；不能据此判断 MySQL 是否为生产日志库。主库版本、SQLite WAL 状态、日志库、Redis 所有权、全部写入者、备份恢复和数据规模仍为 `UNKNOWN`。

## 修改范围

- 新增本技术设计与本执行计划。
- 不改源码、配置、Compose、数据库、UAT 或生产环境。

## 风险与回滚

| 风险 | 控制与恢复边界 |
| --- | --- |
| 把 UAT 证据误当生产证据 | 文档明确 UAT/生产边界；具体生产命令等待 P-M0 事实盘点 |
| 数据复制遗漏或类型变形 | 后续 P-M2 采用白名单、NUL/UTF-8/JSON/范围检查、行数/摘要/聚合/引用/序列验证 |
| 切换后误回旧源库 | 切流前可废弃目标；目标收到生产写入后只允许前向修复/目标快照恢复，不直接回切 |
| UAT 泄露或外发 | 仅在已确认独立资源执行，先清洗敏感载体并做出站阻断；失败即销毁副本 |
| 多实例/缓存/外部回调持续写入 | P-M0 盘点全部写入者；当前无全局写闸，不确认则不切换 |

## 实施步骤

1. ✅ 在独立工作树设计源/目标范围、停写切换、数据/序列/类型/NUL 校验、UAT 脱敏、影子验证、恢复边界和不可自动化风险。
2. ✅ P-M0（部分完成）：已只读盘点 SSH 158 的容器、网络、挂载元数据和磁盘空间；生产库类型/版本、日志库、实例/定时任务/回调写入者、备份恢复能力和 Redis keyspace 仍待在不暴露凭据、不读取数据的前提下确认。
3. 进行中：P-M1 企业账本 NUL 兼容修复已进入候选 `codex/enterprise-f3b-pgcompat`（`b96f0650b`）；该候选的专用 PostgreSQL 合同已记录通过。历史 NUL 行转换/阻断设计与生产数据扫描仍未完成。
4. 进行中：P-M2 的 SQLite-34-pre-enterprise 映射设计已完成；接下来独立实现、测试一次性迁移器，先使用合成 SQLite/MySQL fixture 后才进入 UAT。
5. 待 P-M2：在 158 的确认独立资源进行 P-M3 脱敏 UAT 演练；不得触碰生产。
6. 待 P-M3 与人工批准：编写并评审生产 P-M4 Runbook，明确冻结窗口、实际目标、备份、放行和事故处理。

## 验证命令与通过条件

```bash
python3 /Users/jansen/.agents/skills/spec-driven-workflow/scripts/spec_validator.py \
  --file docs/design-docs/postgresql-primary-database-migration.md --strict
git diff --check
go test ./model -run '^$' -count=1
```

本切片通过条件：设计规格校验通过、文档 diff 无空白错误、当前模型包可编译；不把这些静态结果表述为 PostgreSQL UAT 或生产迁移验收。

实际静态验证：规格校验得分 98/100，无错误；唯一警告为一次性内部迁移器没有 HTTP 方法/路径，符合本设计的非 HTTP API 契约。`go test ./model -run '^$' -count=1 -timeout 120s` 通过（仅编译，不运行测试）。

## P-M0 只读盘点记录（2026-08-17 17:57Z）

| 项目 | 只读事实 | 结论 |
| --- | --- | --- |
| 生产应用与主/日志库 | `new-api` 持续运行并公开 3000；挂载 `/docker/new-api/new-api.l1-final-20260814` 与 `/docker/new-api/data`；启动日志报告 `SQL_DSN not set, using SQLite as database`；`SQLITE_PATH`、`LOG_SQL_DSN` 均未设置 | 主/日志库同为 `/data/one-api.db`；未读取业务行、DSN 或配置值。 |
| SQLite 源元数据 | SQLite 3.40.1、`journal_mode=delete`、约 75 MB、34 张表、无 WAL/SHM | 仅为单时点只读元数据；一致性快照仍需 SQLite 备份机制和写入冻结。 |
| MySQL | `mysql_db` 持续运行并公开 3306；可见于 `mysql_default`，与 `new-api` 的已见网络不同 | 不是主库/日志库身份的证据；不得据容器名推断。 |
| PostgreSQL UAT | `new-api-pg-uat-net` 为 `internal=true`、`attachable=false` 的 bridge 网络；PostgreSQL 与两个 UAT 应用均未发布宿主机端口，挂载路径位于 `/docker/new-api-pg-uat/` | 与生产容器无直接共享 Docker 网络；两个应用共用同一 PostgreSQL 容器/网络，数据库级隔离为 `UNKNOWN`。 |
| 磁盘 | `/docker` 所在文件系统可用约 131G（30% 已用） | 仅为单时点容量线索，不是导入容量或备份能力证明。 |
| 日志库和写入者 | `LOG_SQL_DSN` 未设置，`logs` 在主 SQLite schema 中；未读取业务行、完整进程环境或业务日志 | 日志随主库进入 P-M2 范围；完整写入者仍为 `UNKNOWN`，生产切换不得开始。 |

## 结果与未解决项

- 已完成：迁移技术设计和实施顺序、P-M0 的容器级只读盘点；生产主/日志库为同一 SQLite 文件、运行时版本/日志模式/文件规模和 34 表基线已获只读证据。明确 `AutoMigrate` 不复制历史数据、UAT 不等于生产、没有全局写闸/CDC/自动回切这一事实边界。企业账本 PostgreSQL NUL 修复已进入 F3-B 候选并有专用合同证据。
- 未运行：生产 SQLite 一致性快照、生产数据 NUL/编码扫描、SQLite/MySQL 实体迁移、完整写入者/备份/Redis 盘点、UAT 脱敏/出站验证、生产预检和生产切换。
- 阻塞条件：生产写入边界仍 `UNKNOWN`；当前 UAT 有两个候选应用共用一个 PostgreSQL 容器，数据库级隔离未证实；历史 NUL 账本转换或失败关闭规则尚未设计。
