# PostgreSQL 隔离 UAT 演练结果

状态：集成镜像启动通过；企业功能 E2E 未开始

日期：2026-08-17

## 已验证

- 生产 `new-api` 容器、生产卷、生产网络和 3000 端口未被修改；本次对生产仅做容器/端口/网络与磁盘的只读识别。
- UAT 使用专属目录、专属 PostgreSQL 容器、内部 Docker 网络和 UAT 专用镜像；PostgreSQL 未发布宿主机端口。
- 从已清洗的一致性 SQLite 副本导入 UAT PostgreSQL 前，使用当前仓库基线镜像重新建立 schema；此前不兼容的 `midjourneys.token_id` 列已确认存在。
- 导入时逐表 CSV 行数与目标表行数一致；ID 序列均不落后于已导入的最大 ID。
- 导入后进行附加脱敏：Token、渠道、任务、会话、2FA、OAuth 绑定、Options、原始用量和性能数据均已清空；所有用户的用户名、邮箱、显示名及外部身份字段已替换为合成值，仅保留一个启用的 UAT Root。
- UAT 应用运行于内部网络，从同网络无状态 HTTP 探针访问返回 `200`，证明当前基线镜像可连接 UAT PostgreSQL 并提供 HTTP 服务。

## 未通过与边界

- Docker 已记录 `127.0.0.1:3002:3000` 端口绑定，但主机未出现监听，主机侧 `curl` 返回连接失败。不得把 UAT 宣称为可经 3002 访问；本轮只有内部网络 HTTP 证据。
- 部署镜像对应当前基线，不包含未整合的企业功能切片；这不是企业邀请、账务、Key 或异步结算的 UAT 验收。
- 生产 PostgreSQL 切换不在本次范围，未进行。
- 真实支付、SMTP、OAuth、上游 AI 与生产服务均未调用；企业 E2E 仍为 `NOT_RUN`。

## 2026-08-17 只读复核

- 只读 SSH 盘点确认生产 `new-api` 仍独占宿主机 `3000`，`new-api-l1-test` 使用 `3001`；两者均未被修改。
- UAT 应用 `new-api-pg-uat-app` 与 UAT PostgreSQL `new-api-pg-uat-pg` 运行在专属 `new-api-pg-uat-net`，Docker 端口列表未显示对宿主机的发布端口。
- UAT 应用镜像仍为基线 `new-api-pg-uat:78749ec79`，不含 B/C2/D/E 未整合候选；不得用它验证企业功能。
- 本次未执行容器、网络、卷、镜像或数据库写操作。后续只读挂载检查已确认生产应用使用其 `new-api.l1-final-20260814` 与 `data` 目录，UAT 应用和 PostgreSQL 分别只使用 `new-api-pg-uat/runtime-data` 与 `new-api-pg-uat/pgdata`；三者来源路径不重叠。

## 已删除的 UAT 导入载体

导入和验证完成后，已删除 UAT 目录内的清洗 SQLite 快照、CSV 目录、导入 SQL 与导入日志，减少敏感副本保留。UAT PostgreSQL 与运行所需的受限配置仍保留；生产数据和生产路径未删除或修改。

## 2026-08-17 集成镜像部署与启动验证

- 已由当前主线提交 `3b7d220b8` 构建隔离 UAT 镜像 `new-api-pg-uat:3b7d220b8`；构建完成，其中前端 `bun run build` 与 Go 二进制构建均成功。
- 仅替换 UAT 应用容器 `new-api-pg-uat-app`：沿用专属网络 `new-api-pg-uat-net`、UAT 运行数据挂载 `/docker/new-api-pg-uat/runtime-data:/data` 和回环端口绑定 `127.0.0.1:3002:3000`。替换前容器保留为 `new-api-pg-uat-app-pre-3b7d220b8`，用于需要时的回滚；未删除。
- 已确认新容器持续运行（退出码 `0`），UAT PostgreSQL `new_api_uat` 接受连接；从应用容器内部请求 `http://127.0.0.1:3000/` 以及从同一 UAT 网络临时探针请求 `http://new-api-pg-uat-app:3000/` 均返回 `HTTP/1.1 200 OK`。
- 启动后日志未发现数据库连接失败、迁移失败或致命异常的匹配项。企业发布门 `EnterpriseBillingEnabled` 保持默认 `false`，本次未开启。

## 本轮仍未验证

- 上述证据只覆盖集成镜像启动、PostgreSQL 连通和内部 HTTP 可用性，不等同于企业功能验收。
- 企业邀请、成员额度划转/回收、暂停、企业钱包扣费与异常超额、Key 重置投递及人工处理完整 E2E 均为 `NOT_RUN`。
- SQLite/MySQL 迁移与回归、真实支付、SMTP、OAuth、上游 AI 及生产环境均未调用或验证。

## 后续门禁

1. 用合成 Root、企业管理员和成员数据执行企业授权、账务、Relay 结算和 Key 的端到端验收；全程保持发布门关闭。
2. 如需主机侧访问，再单独诊断 `127.0.0.1:3002` 监听现象；当前内部网络探针已足够证明容器内服务可用。
3. 完成 SQLite/MySQL 兼容验证及真实支付、SMTP、OAuth、上游 AI 的独立验收后，才能评估生产发布。
