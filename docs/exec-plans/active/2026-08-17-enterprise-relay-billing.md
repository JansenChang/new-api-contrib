# 企业 Relay 账务 D：执行计划

状态：READY_FOR_CONTROLLED_INTEGRATION（发布门默认关闭；运行验证与 UAT 尚未执行）

## 目标与非目标

建立独立企业预留、结算、退款和异常账；不复用个人 BillingSession，不启用企业关系。共同发布门为 Root-only `EnterpriseBillingEnabled`、默认 `false`；D 的企业资金入口在门关闭时拒绝，现有个人路径不受影响。D 不得单独接线或启用企业调用，需待 C、D、E、B 完整链路共同验收。

## 现状证据

当前 `request_id` 非稳定，异步任务在上游提交后才落库，Realtime/Midjourney 直接走个人扣费。源码追踪已确认：同步/SSE 使用个人 `BillingSession` 与异步退款；Task 轮询/超时使用个人资金调整；C1 Usage 的唯一键仍保存 Idempotency-Key 原文，且尚无 D 的 RESERVE/SETTLE/REFUND/ANOMALY 执行事务。

## 实施步骤

1. **已确认：** 重复请求：处理中 `202`、人工处理 `409`、终态 `200`，不重放原 API/SSE。
2. **已确认：** 指纹为 Token ID、方法、规范化路径、规范化请求体的 SHA-256，仅保存 hash；C1 原始 Key 列不在 D 迁移范围。
3. **已确认：** 异步提交未知转 `MANUAL_REVIEW`，不自动重试/退款。
4. **已完成（窄范围）：** `POST /v1/chat/completions` 与 `POST /v1/completions` 的非流式 JSON 请求，在共同发布门开启且用户已锚定企业时走企业预留 → 上游提交标记 → 企业结算；无用量或上游错误一律转人工处理，不走个人退款或自动渠道重试。
5. **已完成：** 异步草稿、未知结果人工处理、人工结案和明确发送前失败退款已由 D-async 规格及实现覆盖；Task 查询只使用冻结的企业 Usage 快照，不改变个人资金。
6. **已完成（拒绝边界）：** 企业资助身份的 Realtime 在 WebSocket 升级前拒绝，Midjourney 在分派前拒绝；其他 Relay 格式在一般 Relay 入口拒绝。隔离 SQLite/MySQL/PostgreSQL/UAT 仍待验证。

## 未解决项

Owner 异常暂停范围仍须在成员关系切片中落地；Realtime/Midjourney 首版拒绝范围已确认，但只能在企业身份解析存在后实现。C1 原始 Idempotency-Key 列的长期安全取舍不在 D 范围，后续单独复审。

## 本次验证

- 源码追踪：完成；证据已回写至 D 技术规格。
- Go 测试：本机定向构建无可观察完成结果，记为 `NOT_RUN`；不能据此宣称通过。
- SQLite/MySQL/PostgreSQL 运行验证：NOT_RUN（仅完成源码接线）。
- SSH/UAT/生产：158 仅完成基线 UAT 的只读隔离复核；D 未部署，生产未触碰。
