# PostgreSQL 主数据库迁移：执行计划

状态：READY_FOR_REVIEW（仅技术设计完成；没有迁移器、UAT 操作或生产操作）

负责人：Codex  
关联设计：[PostgreSQL 主数据库迁移与生产切换](../../design-docs/postgresql-primary-database-migration.md)

## 目标与非目标

目标：在不移除 SQLite/MySQL 运行支持的前提下，建立 SQLite/MySQL 主库到 PostgreSQL 主库的可验证、可恢复、停写切换设计和后续实施门禁。

非目标：本切片不操作 SSH 158，不读取生产 DSN/数据/卷，不创建或修改迁移器，不执行 UAT/生产 DDL、复制、备份、容器变更或流量切换。

## 现状证据

- `model/chooseDB` 已按 `SQL_DSN` 前缀选择 PostgreSQL，`LOG_SQL_DSN` 可独立配置；`migrateDB()` 负责 schema 演进，不负责跨库复制。
- `docker-compose.yml` 和 `docker-compose.dev.yml` 使用 PostgreSQL 15，属于开发/示例配置，不能推断生产事实。
- 基线隔离 UAT 的清洗导入和内部 HTTP 200 已被记录，但结果明确排除生产切换、企业 E2E、MySQL 和真实外部系统。
- 企业账本 NUL 兼容修复已在独立提交 `df33b9ec9` 完成并通过专用 PostgreSQL 合同验证；候选整合和历史 NUL 数据转换仍是切换前置项。

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
2. 待人工授权：P-M0 只读盘点生产库类型/版本/规模、日志库、实例/定时任务/回调写入者、备份恢复能力、磁盘、网络隔离和 Redis keyspace；不操作数据。
3. 待 P-M0：将已完成的 P-M1 企业账本 NUL 兼容修复整合到候选，完成历史数据转换设计，并在整合 SHA 上复跑受控 PostgreSQL 合同验证。
4. 待 P-M1：单独设计、实现和测试 P-M2 一次性迁移器；使用合成 SQLite/MySQL fixture 后才进入 UAT。
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

## 结果与未解决项

- 已完成：迁移技术设计和实施顺序，明确 `AutoMigrate` 不复制历史数据、UAT 不等于生产、没有全局写闸/CDC/自动回切这一事实边界。
- 未运行：真实 PostgreSQL 数据复制、NUL 扫描、SQLite/MySQL 实体迁移、UAT 脱敏/出站验证、生产预检和生产切换。
- 阻塞条件：生产拓扑与数据事实仍 UNKNOWN；企业账本 PostgreSQL NUL 兼容修复尚未作为候选整合版本验收，历史 NUL 账本转换规则尚未设计。
