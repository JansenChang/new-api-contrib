# D-async：企业异步 Task 账务执行计划

状态：INTEGRATED_PENDING_UAT；运行验证与 UAT 尚未执行。

1. 新增 Task 草稿状态与私有企业快照，并让轮询/超时扫描明确排除未确认提交和人工处理草稿。
2. 将企业 Usage 预留事务扩展为“Usage + RESERVE + Task 草稿”单一主库事务，建立固定 `usage_record_id` 关联。
3. 在 `relay.RelayTaskSubmit` 的首次 `DoRequest` 前接入草稿事务；提交成功后更新既有草稿，提交未知转人工。
4. 在 `controller.RelayTask` 移除企业路径的个人结算、个人退款与二次 `Task.Insert`。
5. 为轮询、超时、失败、退款和差额结算按企业快照分流，并验证迟到结果仍归属原企业。
6. 运行 SQLite、MySQL、PostgreSQL 与隔离 UAT 验收；未运行项保持 `NOT_RUN`。
7. 为 `MANUAL_REVIEW` 增加 Root/Admin 专属人工结案：成功按原 Usage 结算、未执行退款到原企业；同一事务更新 Task/Usage/账本，并保留操作者与原因审计。

详细不变量、模块边界和验收条件见 `docs/design-docs/enterprise-task-async-billing.md`。

## 本轮实施回写

- 已采用独立 `TaskStatusManualReview`；`SUBMITTING` 与 `MANUAL_REVIEW` 已从普通轮询、超时扫描和 Gemini/Vertex 实时查询中排除。
- 已建立统一企业终态处理：先按固定 `enterprise_usage_record_id` 结算/退款 Usage，再以 CAS 写入 Task 的 `SUCCESS`/`FAILURE`；Task 状态回写失败时保留可重试状态，不触碰个人资金。
- 超时、空上游 ID、Suno/视频渠道读取失败均转 `MANUAL_REVIEW`，不自动退款；Suno、视频轮询和 Gemini/Vertex 实时查询的明确 `SUCCESS`/`FAILURE` 已进入统一终态处理。
- 发送前明确本地失败为 `FAILURE + REFUNDED`；进入上游提交边界后的失败为 `MANUAL_REVIEW`。同一异步幂等 Key 重放返回 Usage 摘要（处理中 `202`、人工 `409`、终态 `200`），不创建新草稿或再次发送上游。
- 2026-08-17：产品确认人工处理必须可由 Root/Admin 结案。独立候选已提供 `POST /api/task/:id/enterprise-resolution`，模型层再次校验平台角色；成功/退款与 Task、Usage、账本（权威审计）在同一主库事务内完成，账本记录操作者与原因。通用管理审计为辅助展示，重试不会落入其兜底记录。已补模型层成功、退款、普通用户拒绝和同结果重试回归；Go 运行测试仍为 `NOT_RUN`，仅可在发布门默认关闭的条件下受控整合，不能据此发布。
- `gofmt` 与 `git diff --check`：通过。定向 Go 测试在本机超过 30 秒仍无输出，已终止以避免环境阻塞，结果为 `NOT_RUN`；SQLite/MySQL/PostgreSQL、隔离 UAT、真实上游 Task 链路均为 `NOT_RUN`。

- 2026-08-17：随 D 主切片以 `e7fadcff8` 提交，并由 `63df9d6d7` 受控整合；发布门保持关闭，未推送、未部署。
