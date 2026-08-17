# Admin 受限站点设置：后端切片执行计划

状态：INTEGRATED（运行测试待补证据）

负责人：Codex

更新时间：2026-08-17

## 目标与非目标

目标：按已确认白名单，为 Root/Admin 提供受限的站点设置读取和更新接口；任何未列出的配置，尤其是 `ServerAddress`、认证、SMTP、支付、计费、模型路由、运行与安全设置，继续仅允许 Root 通过既有通用接口管理。

非目标：不降低 `GET/PUT /api/option` 的 Root-only 权限；不修改前端；不改变完整渠道 Key 的 Root-only 边界；不实现未知设置项的分类。

## 已确认白名单

```text
SystemName
Logo
Footer
About
HomePageContent
Notice
legal.user_agreement
legal.privacy_policy
HeaderNavModules
SidebarModulesAdmin
```

## 修改范围

- `router/api-router.go`：新增独立 `/api/option/site` 路由，使用 `AdminAuth`。
- `controller/option.go`：白名单、受限读取与受限更新；Root-only 通用接口保持原行为。
- `controller/option_site_test.go`：白名单读取、拒绝非白名单写入和允许白名单写入的确定性回归。
- 本结果文档。

## 风险与回滚

- 禁止把现有 `/api/option` 改为 `AdminAuth`；新接口是唯一新增授权面。
- 白名单外 key 返回拒绝且不得写数据库或更新进程内配置。
- 回滚仅移除新路由/处理器；不删除已写入的合法站点配置。

## 验证

```sh
go test ./controller -run 'Test(SiteOption|GetSiteOptions|UpdateSiteOption)' -count=1
git diff --check
```

## 结果与未解决项

- 已新增 `GET/PUT /api/option/site`，仅 Root/Admin 可访问，且只允许既定 10 个站点 key；旧 `GET/PUT /api/option` 保持仅 Root。
- 站点接口只接受字符串；`HeaderNavModules` 和 `SidebarModulesAdmin` 还必须为 JSON 对象。白名单外 key、数组和非法 JSON 均不会写入数据库或进程内配置。
- `model.UpdateOption` 现在先确认数据库持久化成功，才发布 `OptionMap`；单项和批量写入用同一低频锁串行，避免数据库与进程内配置倒挂。
- 独立安全复审无阻塞 P1；未发现完整渠道 Key、白名单外配置值或审计值泄露路径。建议后续补充数据库写入失败、单项/批量并发一致性的 P2 回归。
- `git diff --check` 已通过。定向 `go test ./controller -run 'Test(SiteOption|GetSiteOptions|UpdateSiteOption)' -count=1` 连续两次被本机 Go 工具进程向外部遥测地址建立连接而挂起，已停止残留进程；依 `loop-constraints.md` 暂停继续重试，测试结果为 `NOT_RUN`。
- 2026-08-17：在用户授权后，以 `7c86cfc84` 提交并通过 `9edfc6bd8` 整合至主分支；未部署。
- 前端仍调用 Root-only `/api/option`。在本后端切片通过、合并后另起前端切片接入 `/api/option/site`，不在本片跨越 10 个既有源码文件上限。
