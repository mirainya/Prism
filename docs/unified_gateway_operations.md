# Prism v2.0.0 统一网关运行与发布

本文记录可执行的迁移和运行要求。生产数据库命令必须在备份、停机窗口和单独审批后执行。

## 1. 运行前提

服务按以下顺序启动：

1. 加载 `configs/config.yaml`；
2. 连接数据库并检查所有受管迁移已应用；
3. 检查活动目录、部署代次和成员就绪证明；
4. 初始化 Redis、HTTP Client 和统一 Engine；
5. 启动目录发现 Worker；
6. 数据面就绪时启动统一 SQL Worker；
7. 注册 HTTP 路由。

目录尚未配置或就绪证明不足时，控制面仍可启动，统一数据面不接收调用。服务不会改走旧执行链。

## 2. 必需密钥

统一运行时需要四个独立的 32 字节 Base64 密钥与两个实例身份环境变量：

```text
PRISM_GATEWAY_KEK_B64
PRISM_GATEWAY_HMAC_B64
PRISM_GATEWAY_PAYLOAD_KEK_B64
PRISM_GATEWAY_PAYLOAD_HMAC_B64
PRISM_GATEWAY_INSTANCE_ID
PRISM_GATEWAY_INSTANCE_ROLE
```

| 变量 | 用途 |
|---|---|
| `PRISM_GATEWAY_KEK_B64` | 包裹和解开渠道凭据密钥 |
| `PRISM_GATEWAY_HMAC_B64` | 凭据身份、导入修订和用途证据 |
| `PRISM_GATEWAY_PAYLOAD_KEK_B64` | 加密请求、结果、回调目标和敏感资料 |
| `PRISM_GATEWAY_PAYLOAD_HMAC_B64` | Payload 完整性、幂等键和回调签名 |
| `PRISM_GATEWAY_INSTANCE_ID` | 当前进程的稳定部署成员 ID；多实例环境必须逐实例唯一 |
| `PRISM_GATEWAY_INSTANCE_ROLE` | 当前进程的部署角色；默认 `api-worker` |

升级时必须保留已有 `PRISM_GATEWAY_KEK_B64`。直接换值会使现有凭据无法解密。密钥轮换应通过带版本的密钥环流程完成，不能通过覆盖环境变量模拟轮换。

## 3. 管理面

管理员路由位于 `/api/admin/unified-gateway`：

- `overview`：迁移、目录、部署及管理接口修订；
- `channels`、`pools`、`credentials`：渠道、凭据池、用途授权和加密凭据；
- `catalog`、`products`、`skus`、`rates`：目录草稿、产品、规格和费率；
- `catalog-sources`、`discoveries`、`price-candidates`：来源发现与审核；
- `currencies`、`rate-evidence`：结算币种与费率证据；
- `deployments`、`members`、`catalog-readiness`、`crypto-readiness`：部署代次和成员证明；
- `calls`、`requests`、`payloads`：统一调用、Attempt、请求日志及受控正文读取。

凭据创建与编辑使用版本检查。请求和任务并发上限以 `null` 表示不限，正整数表示上限；`0` 不是“不限”。停止分配进入 draining，相关名额全部释放后才能 disabled。

管理员视频页面只读 `gw_video_tasks` 投影：

```text
GET /api/admin/video/tasks
GET /api/admin/video/tasks/:id
GET /api/admin/video/stats
```

旧能力渠道、视频渠道和旧模型映射管理接口不是 v2.0.0 配置入口。

## 4. 统一执行

一次路由会固定：目录发布版、Product、SKU、Operation Contract、Product Transport、Route、Offering、成本方案、凭据、凭据版本及用途授权。

新执行事实只写入 `gw_*`：

- `gw_api_calls` 与 `gw_api_call_attempts`；
- `gw_api_call_payloads` 和加密 Blob；
- `gw_channel_request_logs`；
- `gw_async_executions` 与 `gw_async_outbox`；
- `gw_api_resources` 及各资源投影；
- 预授权、结算、费率和上游成本证据；
- 媒体资产、结果交付和客户端回调投递。

旧 `api_calls`、`tasks`、`ai_responses` 等表只作为迁移源或兼容数据保留，不承担新调用的执行事实。

## 5. SQL Worker

统一 Worker 直接消费 MySQL，不使用 Redis 任务队列：

- 4 个异步 Outbox 消费者；
- 2 个后台 Responses Attempt 消费者；
- 上游回调消费者；
- 客户端回调投递消费者；
- 结果交付到期与恢复消费者；
- 能力调用恢复消费者；
- 敏感正文与媒体资产清理消费者；
- 独立目录发现消费者。

消费者通过租约认领记录，服务退出时停止认领并等待当前任务结束。提交超时、响应损坏或发送后失联会保留未知事实，通过查询恢复或人工核验处理；不会自动重复生成、提前退款或释放任务名额。

## 6. 回调与交付

上游回调入口：

```text
POST /internal/gateway/callback
X-Gateway-Callback-Token: ...
X-Gateway-Event-ID: ...
```

入口只接受 JSON（`application/json` 或明确的 `application/*+json` 媒体类型），正文上限 1 MiB；路径不携带 `scope`，事件作用域由服务端固定。入口按客户端地址限速并设置并发上限，只写 Receipt/Alias、事件 HMAC 和限期加密正文，并返回 `202`。回调消费者验真后才推进 AsyncExecution、结果交付与结算；Receipt 处理完成或过期后清除正文，绑定令牌在执行窗口结束后撤销。

客户端 `callback_url` 在创建视频时通过公网地址校验，并加密绑定 Call。终态事务创建不可变载荷和内部 HMAC 完整性证据；投递发送稳定事件 ID、内容 SHA256，使用 10 秒请求超时、禁止重定向、SSRF 防护和最多 5 次尝试。临时网络或服务退出错误不会被误判为永久解密失败。

结果交付与生成终态独立：

- reference 保存加密来源并按权限解析；
- managed copy 只返回明确的公开 `storage_locator`，不暴露内部 object key；
- 交付失败不会把成功生成改为失败；
- 到期扫描将已到期 reference 交付写为 expired。

## 7. 迁移命令

```bash
./prism migrate status
./prism migrate up
./prism migrate adopt
./prism migrate audit
./prism migrate audit-deep
./prism migrate import-legacy
./prism migrate import-runtime
./prism migrate import-video-assets
./prism migrate import-ai-files
./prism migrate verify-crypto
./prism migrate cleanup-legacy
```

| 命令 | 作用 |
|---|---|
| `status` | 查看已应用与待执行迁移 |
| `up` | 在 MySQL 命名锁下应用迁移 |
| `adopt` | 将核验后的基线前旧库登记为受管基线 |
| `audit` | 汇总旧数据、目标目录和切换状态 |
| `audit-deep` | 核对目标表、迁移运行、映射及开放问题 |
| `import-legacy` | 导入旧渠道、凭据和模型目录 |
| `import-runtime` | 导入可证明的历史调用、任务、Responses 与账务证据 |
| `import-video-assets` | 导入有归属、完整性和受控存储证据的视频素材 |
| `import-ai-files` | 导入可证明的 Files 资源 |
| `verify-crypto` | 解密核验已有凭据 |
| `cleanup-legacy` | 深度审计通过后删除旧网关配置表 |

所有导入都以映射和源修订保持幂等。无法证明的历史行写入 `gw_migration_issues` 并使导入失败，不会猜测归属、状态或金额。

## 8. 推荐生产切换顺序

1. 构建 v2.0.0 软件包，执行 `prism version` 并记录 SHA256；
2. 备份当前二进制、配置、数据库和环境文件；
3. 停止所有 Prism HTTP 与旧后台进程；
4. 执行 `migrate status`、`audit` 和 `audit-deep`；
5. 补齐四个密钥，保留原凭据 KEK；
6. 执行 `migrate up`；
7. 执行 `import-legacy`，启动仅开放控制面的 v2.0.0，并在控制面审核、发布导入的目录；
8. 停止控制面进程，执行 `import-runtime`、`import-video-assets` 和 `import-ai-files`；
9. 执行 `verify-crypto`、`audit` 和第二次 `audit-deep`；
10. 启动控制面，创建部署代次，登记当前实例及真实运行二进制 SHA256，并提交目录和加密就绪证明；
11. 激活目录发布版和部署代次，重启 v2.0.0 以启用统一 Worker 与数据面；
12. 检查服务状态、日志、`/health` 和 `/metrics`；
13. 使用专用测试 Token 验证 Models、对话、图片、视频创建与查询；
14. 观察 Call、Attempt、Outbox、回调与计费事实；
15. 只有 `ready_for_cleanup=true` 且业务核验完成后，另行安排 `cleanup-legacy`。

`import-runtime` 只接受已经发布或退役的目录版本，因此不能在 `import-legacy` 后立即执行。目录发布必须先完成；部署代次和数据面激活应在全部导入、加密核验与深度审计通过后完成。

`migrate up` 会把既有 `sk-prism-<48位十六进制>` Token 的单向摘要标记为旧版摘要格式；Token 明文无需进入数据库，现有客户端 Key 仍可使用。迁移集成测试会核验该兼容路径，切换后的专用测试 Token 验证也不得省略。

不得滚动混跑会同时处理同一旧任务的两个版本。正式切换前不能把仓库测试结果写成生产上线结果。

## 9. 发布验证

```bash
go test ./...
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.7.0 ./...
go run honnef.co/go/tools/cmd/staticcheck@v0.7.0 '-checks=all,-ST*' ./...
go test -tags integration ./internal/migrate -count=1 -v

cd console
npm audit --audit-level=high
npm test
npx tsc --noEmit
npm run build
```

完成构建后还应在新启动的本地服务上验证桌面与手机控制台、公开 API 404 边界和实际路由响应。

正式软件包由 `.github/workflows/release.yml` 生成。推送与
`console/package.json` 版本一致的 `v*` 标签后，工作流会重复执行前后端、
Race、MySQL 迁移和浏览器验收，再发布 Linux AMD64、Windows AMD64 与
`SHA256SUMS`。生产部署只使用该 Release 的 Linux 软件包，并核对哈希。
