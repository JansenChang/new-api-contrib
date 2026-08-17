# PostgreSQL 主库一次性复制器（P-M2）：执行计划

状态：ACTIVE — P-M2 SQLite-34 元数据采集器硬化完成；复制与 UAT 仍未开始

负责人：Codex

关联设计：[PostgreSQL 主库一次性复制器（P-M2）](../../design-docs/postgresql-primary-database-copy-tool.md)

## 目标与边界

目标：为当前已证实为 SQLite 的生产主库准备可重复验证的 SQLite/MySQL → PostgreSQL 复制器；先以合成数据证明严格预检、值校验、全表复制、序列与企业账务对账。

边界：不读取/复制生产 SQLite 数据、WAL、日志库或 DSN；不在 158 创建数据库、运行 DDL、挂载卷、启动容器或修改网络。生产写入冻结、UAT 数据恢复和发布门开启不属于本切片。

## 已确认前提

- 当前候选的主库模型为 40 张 allowlist 表；已确认本次生产 SQLite 的 `logs` 与主库同文件，故 SQLite-34 profile 固定纳入 `logs`；其他源拓扑仍必须由 manifest 显式声明。
- `migrateDB()` 不是纯 schema 建立器，带有运行期回填和企业补建；P-M2 不能复用它作为导入入口。
- 新企业账本摘要的 PostgreSQL NUL 问题已修复；历史 NUL 行仍是失败关闭前置项。
- SQLite-34 profile 已固定：仅接受来源 SQLite、主库日志和候选 SHA `e451c93f1d44a1a84f9bab07937510458c8bd642`；34 张旧表必须按编译期 `TableSpec` 的列/索引签名比较和显式复制。仅 `users.active_enterprise_id`、两张支付表的 6 个主体快照列以及 Root/Admin 衍生企业/Owner 关系由复制器确定性填充。`users.created_at=NULL` 固定映射为 `0`，不得取当前时间。

## 实施顺序

1. ✅ 技术审阅已补齐 SQLite-34-pre-enterprise 固定映射：34 表逐表范围、6 张目标新增表、7 个新增列、Root/Admin 确定性企业锚点和支付快照均已定义；确认“历史 NUL 一律失败关闭”、日志范围 manifest、40 表 allowlist 与不使用运行时 `AutoMigrate` 的边界。
2. ✅ `codex/postgres-primary-copy-preflight` 完成纯预检骨架；`codex/postgres-primary-copy-static-profile` 转录 `SQLite-34` 34 张来源表的完整编译期列/索引签名，并强化候选 SHA、身份哈希、快照证明、SQLite+主日志范围与批次上限门禁；`codex/postgres-primary-copy-static-profile-hardening` 移除任意 profile 的导出校验入口，成功路径只能使用内置静态 profile；`codex/postgres-primary-copy-static-profile-baseline` 删除第二套表名常量并增加固定 34/40 表名与全签名 SHA-256 基线回归。切片只接收内存元数据快照，不打开连接、不执行 DDL/DML。
3. ✅ `codex/postgres-primary-sqlite-metadata-collector` 新增只读 SQLite-34 采集器：仅接收调用方已打开的 `*sql.DB` 和 `context.Context`，按内置固定 profile 读取 `table_info`、`index_list`、`index_info` 与 `COUNT(*)`，缺表/列/索引漂移由现有 `Preflight` 失败关闭；合成 SQLite 回归未读取真实库。
4. ✅ `codex/postgres-primary-sqlite-metadata-collector-hardening` 在采集前用固定只读 `main.sqlite_schema` 精确枚举 34 张业务表，过滤 SQLite 系统表；`IndexSpec`/`IndexMetadata` 增加并严格比较 `Partial`，按 PRAGMA `seq` 稳定排序索引及索引列；额外企业表、缺表、索引缺失和 partial 漂移均回归失败关闭。
5. 待后续切片：先写 SQLite → PostgreSQL 合成复制回归，再接入真实 MySQL 驱动 fixture；两者都覆盖 NUL/UTF-8/JSON/范围/sequence/企业账务失败关闭。
6. 候选启动验证不引入未批准回填；逐表、引用和聚合对账通过后，提交独立分支并回写证据。
7. 仅在 P-M2 合成合同完成且另获授权后，做 P-M3 的生产快照脱敏 UAT；当前 158 的双 UAT 应用共享 PostgreSQL 容器，数据库级隔离确认前不得使用。

## 验证命令与通过条件

实现分片必须至少执行：

```bash
go test ./... -run 'TestPostgresPrimaryMigration' -count=1 -timeout 120s
git diff --check
```

通过条件：SQLite-34 profile 的 5 项回归和 AC-1 至 AC-6 有对应确定性合成证据；SQLite 与真实 MySQL fixture 的结果分开记录；每个失败用例证明无发布资格且不泄露原始值。`NOT_RUN` 必须保留生产数据、158 UAT、生产切换和真实外部系统。

## 风险与停止条件

| 风险 | 停止条件 |
| --- | --- |
| 复制器把未知 schema 当作兼容 | 任一未知表/列或目标非空即停止。 |
| NUL/编码转换破坏账本幂等 | 任一 NUL 或异常历史摘要即停止，不转换。 |
| UAT 候选并发写同一 PostgreSQL | 未确认数据库/schema/账号隔离前不部署或测试。 |
| 日志范围遗漏 | `LOG_SQL_DSN` 与 manifest 策略不明确即不运行。 |
| 合成成功被误称生产可切换 | 未有真实一致性快照、写入冻结、备份/恢复和 UAT 脱敏证据时不得进入 P-M4。 |

## 当前结果与 NOT_RUN

- 已完成：固定 `e451c93f1d44a1a84f9bab07937510458c8bd642` 候选的 34 张表完整列签名与索引签名转录；表/列/类型/notnull/PK/索引漂移和目标非空均失败关闭；manifest 要求不同的源/目标身份哈希、非空快照证明、SQLite 源与 `primary` 日志范围；外部调用方不能注入自定义 profile 绕过内置签名；固定基线回归锁定 34 张来源表、40 张候选目标表及签名指纹 `a307c47470b78e8b4f540c580994db2870970fcd57113a877e7a11e221ec8f17`。
- 验证：`go build ./pkg/postgresmigration`、`git diff --check`。定向 `go test` 在本机 Go 测试进程未返回，结果记为 `UNKNOWN`，未据此宣称通过。
- `NOT_RUN`：实际 SQLite/MySQL 连接读取、PostgreSQL schema/DML、完整 SQLite-34 复制、MySQL 合成 fixture、全模型 PostgreSQL 合同、158 UAT、生产快照、生产切换。
- 本切片新增：采集器只读合成 SQLite 回归已编写；本机 Go 测试进程在 30 秒内未返回（与并发测试进程争用），结果记为 `UNKNOWN`，未据此宣称通过。
- 硬化切片：`go build ./pkg/postgresmigration` 与 `git diff --check` 通过；定向测试仍受本机并发 Go 测试进程影响未返回，记为 `UNKNOWN`。
