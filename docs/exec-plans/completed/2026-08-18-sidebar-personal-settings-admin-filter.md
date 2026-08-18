# 普通用户侧边栏个人设置遵从管理员开关
状态：COMPLETED
负责人：Codex
更新时间：2026-08-18

## 目标与非目标

目标：普通用户的“侧边栏个人设置”仅展示管理员在 `SidebarModulesAdmin` 中启用的模块。

非目标：不改变路由授权、后端权限或部署配置。

## 现状证据

- 主侧边栏已由 `web/src/hooks/use-sidebar-config.ts` 按管理员与用户配置的交集过滤。
- `web/src/features/profile/components/sidebar-modules-card.tsx` 的模块定义为静态数组，未读取管理员配置。

## 修改范围

- 暴露管理员侧边栏配置的共享 Hook。
- 个人资料的侧边栏设置卡片按该配置过滤分组和模块。

## 风险与回滚

- 风险：管理员状态尚未加载时会先采用已有默认配置，状态返回后重新渲染。
- 回滚：还原本次提交即可恢复原有卡片展示；不涉及数据迁移。

## 实施步骤

1. 复用现有管理员配置解析逻辑。
2. 过滤普通用户可见的配置项，空分组不渲染。
3. 执行格式、lint、类型检查和生产构建。

## 验证命令与通过条件

- `cd web && bun run format:check`：未通过；仅报告 9 个既有、未修改文件的格式问题，本次两个文件不在列表中。
- `cd web && bun x oxlint -c .oxlintrc.json src/hooks/use-sidebar-config.ts src/features/profile/components/sidebar-modules-card.tsx`：通过。
- `cd web && bun run typecheck`：通过。
- `cd web && bun run build`：通过。

通过条件：管理员关闭的模块不会出现在普通用户的个人设置卡片中，且前端构建通过。

## 结果与未解决项

- 卡片使用与真实左侧栏相同的管理员配置解析结果；关闭的模块及其空分组不再渲染。
- 项目未提供可运行的前端测试脚本或已安装测试运行器，因此未新增不可执行测试；类型检查和生产构建已完成。
