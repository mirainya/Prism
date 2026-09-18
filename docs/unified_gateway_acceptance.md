# Prism v2.0.0 统一网关验收状态

日期：2026-09-12

> 本文是 2026-09-12 生产切换的验收快照，其中部署代次 `6` 是历史事实。
> 当前配置与密钥要求见 [Prism 配置简化方案](specs/2026-09-15-prism-configuration-simplification.md)，不再把部署代次作为日常操作。

结论：生产迁移和部署切换已完成，实例正在运行 v2.0.0；完整隔离环境、真实业务调用、Release 归档和观察期验收仍未完成，因此暂不能标记为最终交付通过。

## 已实现

- [x] 统一 `gw_*` 目录覆盖渠道、凭据池、用途授权、产品、SKU、Operation、Transport、Route、Offering、售价和成本方案。
- [x] 同步对话、Responses、图片和视频使用统一路由，不再把旧 `api_calls`、`tasks`、`ai_responses` 作为新执行事实源。
- [x] Attempt 固定成本方案，上游成本证据与事件使用固定方案计算。
- [x] 异步视频和后台 Responses 使用 SQL Outbox、租约、重试和恢复 Worker，不依赖 Redis 队列。
- [x] 视频创建、估价、列表、详情、队列及素材路由接入统一目录；公开视频取消路由已删除。
- [x] 图片输入使用统一媒体资产，列表和任务投影不读取 Base64 或加密正文。
- [x] 上游回调采用执行级令牌、事件/正文 HMAC 和异步消费。
- [x] 客户端回调采用加密目标、不可变载荷、内部 HMAC 完整性证据、稳定事件 ID、内容 SHA256、SSRF 防护、超时和有限重试。
- [x] reference 与 managed-copy 交付独立于生成终态；内部 object key 不作为公开 URL 返回。
- [x] 目录来源发现、候选价格、费率证据和审核状态进入正式控制面。
- [x] 渠道、凭据池、凭据、目录和统一调用管理 API 已注册；当时验收过的部署管理 API 现已从公开管理路由移除。
- [x] 管理员视频任务改为 `gw_video_tasks` 只读投影。
- [x] 旧 `/v1/channels`、`/v1/capabilities`、通用 Task 与视频取消路由未注册。
- [x] 迁移 CLI 覆盖结构升级、审计、目录导入、运行数据导入、素材/文件导入、加密核验和旧配置清理。
- [x] Payload KEK/HMAC 已配置；当时用于读取旧密文凭据的凭据 KEK/HMAC 也已保留。

## 已验证

- [x] `go test ./...`、`go vet ./...`、staticcheck、govulncheck 通过。
- [x] 关键 Gateway、API、Service、Video 路径的 `go test -race` 通过。
- [x] 前端 Vitest（46 个测试）、TypeScript、生产构建和 npm 高危审计通过。
- [x] 两个 GitHub Actions 工作流通过 actionlint，发布权限已拆分。
- [x] 迁移文件加载、校验和幂等结构断言通过；回调入口、生命周期清理和锁顺序回归测试通过。

本机未提供 Docker/MySQL，因此带 `integration` 标签的 MySQL 与浏览器测试仅完成测试发现并自动跳过，不能据此宣称隔离数据库或真实浏览器验收通过。CI 必须在带 MySQL、Redis 和 Chromium 的环境中重新执行这些项目。

最近一次本地迁移测试：

```bash
go test -tags integration ./internal/migrate -count=1 -v
```

结果为通过，但 MySQL 相关用例因未设置 `PRISM_MIGRATION_TEST_DSN` 而跳过。最终发布提交形成后仍需在 CI 或隔离环境重跑完整数据库与浏览器验证。

## 软件发布与隔离验收待完成

- [ ] 等待所有并行实现修改完成，检查最终工作树。
- [x] 重跑 `go test ./...`、`go vet ./...`、staticcheck 和关键包 race 测试。
- [x] 重跑前端测试、TypeScript 与生产构建。
- [ ] 在新鲜隔离数据上完成 MySQL、Redis、真实登录、桌面、手机及公开 API 路由验收。
- [ ] 创建提交并推送 `main`。
- [ ] 构建 Linux AMD64 v2.0.0 软件包，核对版本与 SHA256。
- [ ] 创建 GitHub `v2.0.0` Release 并上传软件包。

## 生产切换记录

- [x] 已完成生产备份，备份目录为 `/www/wwwroot/prism.mirai.icu/.deploy/20260912-120000/`。
- [x] 已完成生产迁移、历史导入、加密核验和部署前审计；迁移 `20260912_090000_backfill_gateway_sku_downstream_paths.sql` 已应用。
- [x] 当时已创建并激活部署代次 `6`，并记录 `runtime_ready=true`；这是旧机制的历史记录，当前运行不再读取该代次或成员证明。
- [x] 已替换二进制并重启服务，生产版本为 `Prism 2.0.0`。
- [x] 生产二进制 SHA256：`4b5b840d3e9523b42242063f55fc5c1bb95cc7b6a8cf1bc606b0f2c495715127`。
- [x] `https://prism.mirainya.icu/health` 返回 HTTP 200。
- [ ] 完成对话、图片、视频真实调用，以及 Call、Attempt、异步状态、交付、回调和计费事实核对。
- [ ] 完成观察期记录；`audit-deep` 仍有历史运行记录待核验，当前 `ready_for_cleanup=false`，不得执行 `cleanup-legacy`。

## 验收判定

只有以下条件全部满足，才能把 v2.0.0 标记为可交付生产成品：

1. 最终提交的自动化检查全部通过；
2. GitHub Release 软件包与部署二进制哈希一致；
3. 生产迁移和所有导入无开放问题；
4. 活动目录、Payload 密钥与运行状态有效；启用中或停止分配中的旧凭据仍依赖密文时，对应旧凭据 KEK/HMAC 有效；
5. 对话、图片和视频真实调用通过；
6. 计费、上游成本、交付和回调事实可核对；
7. 服务重启后 SQL Worker 能恢复未完成任务；
8. 观察期内无新增严重日志或状态不一致。

当前状态可以记录为“已部署、待完整业务验收”，不能写成“最终验收通过”或“已完成旧库清理”。
