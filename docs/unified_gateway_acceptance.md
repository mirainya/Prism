# Prism v2.0.0 统一网关验收状态

日期：2026-09-09

结论：仓库实现已进入最终发布验证；生产尚未迁移、发布或重启，因此当前不能标记为生产验收通过。

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
- [x] 渠道、凭据池、凭据、目录、部署和统一调用管理 API 已注册。
- [x] 管理员视频任务改为 `gw_video_tasks` 只读投影。
- [x] 旧 `/v1/channels`、`/v1/capabilities`、通用 Task 与视频取消路由未注册。
- [x] 迁移 CLI 覆盖结构升级、审计、目录导入、运行数据导入、素材/文件导入、加密核验和旧配置清理。
- [x] 四个生产密钥有独立用途，统一数据面启动时检查目录和密钥就绪状态。

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

## 发布前待完成

- [ ] 等待所有并行实现修改完成，检查最终工作树。
- [x] 重跑 `go test ./...`、`go vet ./...`、staticcheck 和关键包 race 测试。
- [x] 重跑前端测试、TypeScript 与生产构建。
- [ ] 在新鲜隔离数据上完成 MySQL、Redis、真实登录、桌面、手机及公开 API 路由验收。
- [ ] 创建提交并推送 `main`。
- [ ] 构建 Linux AMD64 v2.0.0 软件包，核对版本与 SHA256。
- [ ] 创建 GitHub `v2.0.0` Release 并上传软件包。

## 生产切换待完成

- [ ] 备份生产数据库、当前二进制、配置和环境文件。
- [ ] 保留现有 `PRISM_GATEWAY_KEK_B64`，新增并核验其余三个密钥。
- [ ] 在停机窗口执行迁移、历史目录和运行数据导入。
- [ ] 执行 `verify-crypto`、`audit` 与 `audit-deep`。
- [ ] 创建并激活目录发布版和部署代次。
- [ ] 替换二进制并重启服务。
- [ ] 验证 systemd、`/health`、`/metrics`、日志和实际公开调用。
- [ ] 核对统一 Call、Attempt、异步状态、交付、客户端回调、售价结算和上游成本。

## 验收判定

只有以下条件全部满足，才能把 v2.0.0 标记为可交付生产成品：

1. 最终提交的自动化检查全部通过；
2. GitHub Release 软件包与部署二进制哈希一致；
3. 生产迁移和所有导入无开放问题；
4. 活动目录、部署代次与成员加密证明有效；
5. 对话、图片和视频真实调用通过；
6. 计费、上游成本、交付和回调事实可核对；
7. 服务重启后 SQL Worker 能恢复未完成任务；
8. 观察期内无新增严重日志或状态不一致。

当前状态未满足生产切换与观察项，文档不得写成已上线。
