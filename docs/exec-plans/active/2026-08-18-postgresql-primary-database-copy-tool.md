# PostgreSQL 主库一次性复制器（P-M2）：执行计划

状态：DRAFT — 等待技术审阅；仅可开发合成 fixture

负责人：Codex

关联设计：[PostgreSQL 主库一次性复制器（P-M2）](../../design-docs/postgresql-primary-database-copy-tool.md)

## 目标与边界

目标：为当前已证实为 SQLite 的生产主库准备可重复验证的 SQLite/MySQL → PostgreSQL 复制器；先以合成数据证明严格预检、值校验、全表复制、序列与企业账务对账。

边界：不读取/复制生产 SQLite 数据、WAL、日志库或 DSN；不在 158 创建数据库、运行 DDL、挂载卷、启动容器或修改网络。生产写入冻结、UAT 数据恢复和发布门开启不属于本切片。

## 已确认前提

- 当前候选的主库模型为 40 张 allowlist 表；`logs` 是否属于主库取决于未知的 `LOG_SQL_DSN`，必须以 manifest 显式声明。
- `migrateDB()` 不是纯 schema 建立器，带有运行期回填和企业补建；P-M2 不能复用它作为导入入口。
- 新企业账本摘要的 PostgreSQL NUL 问题已修复；历史 NUL 行仍是失败关闭前置项。
- 生产 `new-api` 运行时日志表明主库为 SQLite；版本、WAL、规模、日志库和写入者仍为 `UNKNOWN`。

## 实施顺序

1. 技术审阅本设计，确认“历史 NUL 一律失败关闭”、日志范围 manifest、40 表 allowlist 与不使用运行时 `AutoMigrate` 的边界。
2. ✅ `codex/postgres-primary-copy-preflight` 完成纯预检骨架；`codex/postgres-primary-copy-static-profile` 转录 `SQLite-34` 34 张来源表的完整编译期列/索引签名，并强化候选 SHA、身份哈希、快照证明、SQLite+主日志范围与批次上限门禁；`codex/postgres-primary-copy-static-profile-hardening` 移除任意 profile 的导出校验入口，成功路径只能使用内置静态 profile；`codex/postgres-primary-copy-static-profile-baseline` 删除第二套表名常量并增加固定 34/40 表名与全签名 SHA-256 基线回归。切片只接收内存元数据快照，不打开连接、不执行 DDL/DML。
3. 先写 SQLite → PostgreSQL 合成回归，再接入真实 MySQL 驱动 fixture；两者都覆盖 NUL/UTF-8/JSON/范围/sequence/企业账务失败关闭。
4. 候选启动验证不引入未批准回填；逐表、引用和聚合对账通过后，提交独立分支并回写证据。
5. 仅在 P-M2 合成合同完成且另获授权后，做 P-M3 的生产快照脱敏 UAT；当前 158 的双 UAT 应用共享 PostgreSQL 容器，数据库级隔离确认前不得使用。

## 验证命令与通过条件

实现分片必须至少执行：

```bash
go test ./... -run 'TestPostgresPrimaryMigration' -count=1 -timeout 120s
git diff --check
```

通过条件：AC-1 至 AC-6 有对应确定性合成回归；SQLite 与真实 MySQL fixture 的结果分开记录；每个失败用例证明无发布资格且不泄露原始值。`NOT_RUN` 必须保留生产数据、158 UAT、生产切换和真实外部系统。

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
