# Prism v2.0.0 统一网关运行与配置

> 当前配置流程以 [Prism 配置简化方案](specs/2026-09-15-prism-configuration-simplification.md) 为准。
> 运维台直接修改活动模型、上游、映射、价格和 Key，保存后对下一次请求生效。
> 旧表、迁移报告和兼容响应中仍可能出现目录发布、部署代次和证明字段；它们仅是兼容数据，不是当前操作步骤。

本文记录可执行的迁移和运行要求。生产数据库命令必须在备份、停机窗口和单独审批后执行。

## 1. 运行前提

服务按以下顺序启动：

1. 加载 `configs/config.yaml`；
2. 连接数据库并检查所有受管迁移已应用；
3. 检查活动目录和运行依赖；
4. 初始化 Redis、HTTP Client 和统一 Engine；
5. 启动目录发现 Worker；
6. 数据面就绪时启动统一 SQL Worker；
7. 注册 HTTP 路由。

目录尚未配置或运行条件不满足时，控制面仍可启动，统一数据面不接收调用。服务不会改走旧执行链。

## 2. 运行密钥

统一运行时始终需要两个独立的 32 字节 Base64 Payload 密钥：

```text
PRISM_GATEWAY_PAYLOAD_KEK_B64
PRISM_GATEWAY_PAYLOAD_HMAC_B64
```

启用中或停止分配中的旧凭据尚无明文 Key、仍依赖旧密文时，数据面还需要：

```text
PRISM_GATEWAY_KEK_B64
PRISM_GATEWAY_HMAC_B64
```

| 变量 | 用途 |
|---|---|
| `PRISM_GATEWAY_PAYLOAD_KEK_B64` | 加密请求、结果、回调目标和敏感资料 |
| `PRISM_GATEWAY_PAYLOAD_HMAC_B64` | Payload 完整性、幂等键和回调签名 |
| `PRISM_GATEWAY_KEK_B64` | 读取仍在使用的旧加密凭据，或执行一次性迁移核验 |
| `PRISM_GATEWAY_HMAC_B64` | 核验仍在使用的旧加密凭据，或执行一次性迁移核验 |

新建和替换的 Key 明文保存在 `gw_credentials.secret`，不要求凭据密钥环、版本轮换或就绪证明。旧密文版本可以作为兼容数据保留；凭据已有明文 Key 后，它们不再影响数据面就绪状态。

图片和视频结果使用 Prism Token 单独设置的 XFileStorage Key。Token 有 Key 时，新 Call 使用 `managed_copy`；无 Key 时，新 Call 使用 `reference`。创建调用时再把该 Key 固定到 `gw_api_calls`。更换或清除 Token 的 Key 只影响后续调用；在途任务和历史结果的读取、签名与清理继续使用对应 Call 固定的 Key。

## 3. 管理面

管理员路由位于 `/api/admin/unified-gateway`：

- `overview`：迁移、目录和管理接口修订；
- `channels`、`pools`、`credentials`：渠道、凭据池、用途授权和 Key 元数据；
- `catalog`、`model-entries`：活动目录的只读兼容查询；
- `catalog-sources`、`discoveries`、`price-candidates`：来源发现与审核；
- `currencies`、`rate-evidence`：结算币种与费率证据；
- `model-meta`、`catalog-changes`、`pools`、`credentials`、`offerings/*/runtime-state`：直接修改活动配置；
- `calls`、`requests`、`payloads`：统一调用、Attempt、请求日志及受控正文读取。

凭据创建与编辑使用版本检查，读取接口不返回 Key。请求和任务并发上限以 `null` 表示不限，正整数表示上限；`0` 不是“不限”。停止分配进入 draining，相关名额全部释放后才能 disabled。

旧 `POST /catalog` 及 `catalog/:id` 下的草稿编辑接口仍为旧客户端保留，但当前控制台不使用它们，公开管理路由也不提供对应的发布、激活或回滚操作。

管理员视频页面只读 `gw_video_tasks` 投影：

```text
GET /api/admin/video/tasks
GET /api/admin/video/tasks/:id
GET /api/admin/video/stats
```

旧能力渠道、视频渠道和旧模型映射管理接口不是 v2.0.0 配置入口。

## 4. 统一执行

一次路由会固定请求开始时的 Product、SKU、Operation Contract、Product Transport、Route、Offering、成本方案、凭据及用途授权配置。

新执行事实只写入 `gw_*`：

- `gw_api_calls` 与 `gw_api_call_attempts`；
- `gw_api_call_payloads` 和加密 Blob；
- `gw_channel_request_logs`；
- `gw_async_executions` 与 `gw_async_outbox`；
- `gw_api_resources` 及各资源投影；
- 预授权、结算、费率和上游成本证据；
- 媒体资产、结果交付和客户端回调投递。

`gw_api_calls.xfs_api_key` 是任务提交时固定的结果存储凭据，不从 Token 当前设置动态回读。

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

消费者通过租约认领记录，服务退出时停止认领并等待当前任务结束。单次上游交换由 Transport 超时限制，异步任务本身没有总时限：上游持续返回有效非终态时会继续轮询。提交结果不明确时不自动重发；已绑定上游任务 ID后连续三次轮询通信失败会停止本地轮询并标记 `terminated_unknown`，保留日志、名额和迟到终态恢复入口。

## 6. 回调与交付

上游回调入口：

```text
POST /internal/gateway/callback
X-Gateway-Callback-Token: ...
X-Gateway-Event-ID: ...
```

入口只接受 JSON（`application/json` 或明确的 `application/*+json` 媒体类型），正文上限 1 MiB；路径不携带 `scope`，事件作用域由服务端固定。入口按客户端地址限速并设置并发上限，只写 Receipt/Alias、事件 HMAC 和限期加密正文，并返回 `202`。回调消费者验真后才推进 AsyncExecution、结果交付与结算；Receipt 处理完成或过期后清除正文，绑定令牌在执行窗口结束后撤销。

客户端 `callback_url` 在创建视频时通过公网地址校验，并加密绑定 Call。终态事务创建不可变载荷和内部 HMAC 完整性证据；结果载荷只包含交付 ID，不包含私有定位符或临时签名 URL。客户端通过任务查询接口获取新的签名 URL。投递发送稳定事件 ID、内容 SHA256，使用 10 秒请求超时、禁止重定向、SSRF 防护和最多 5 次尝试。临时网络或服务退出错误不会被误判为永久解密失败。

结果交付与生成终态独立：

- reference 保存加密来源并按权限解析；
- managed copy 保存私有持久定位符，任务查询时使用 Call 固定的 XFileStorage Key 生成新的临时签名 URL；
- 交付失败只改变交付状态，不会把成功生成改为失败；
- remote URL 的临时下载、上传或校验失败会加密保存来源并创建 `reconcile_delivery`，该任务只重试转存，不重新调用或查询上游生成；内联结果和永久失败不自动重试；
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
| `audit` | 汇总旧数据、目标目录和迁移状态；输出仍含旧部署兼容字段 |
| `audit-deep` | 核对目标表、迁移运行、映射及开放问题；检查旧控制面兼容表 |
| `import-legacy` | 导入旧渠道、凭据和模型目录 |
| `import-runtime` | 导入可证明的历史调用、任务、Responses 与账务证据 |
| `import-video-assets` | 导入有归属、完整性和受控存储证据的视频素材 |
| `import-ai-files` | 导入可证明的 Files 资源 |
| `verify-crypto` | 数据库仍有旧加密凭据时解密核验这些记录 |
| `cleanup-legacy` | 深度审计通过后删除旧网关配置表 |

所有导入都以映射和源修订保持幂等。无法证明的历史行写入 `gw_migration_issues` 并使导入失败，不会猜测归属、状态或金额。

## 8. 生产启动顺序

1. 构建 v2.0.0 软件包，执行 `prism version` 并记录 SHA256；
2. 备份当前二进制、配置、数据库和环境文件；
3. 停止所有 Prism HTTP 与后台进程；
4. 配置 Payload KEK/HMAC；启用中或停止分配中的凭据仍依赖旧密文时，同时提供对应旧凭据 KEK/HMAC；
5. 执行 `migrate status` 和 `migrate up`；
6. 启动服务，检查日志、`/health` 和 `/metrics`；
7. 在运维台检查模型、上游、映射、价格和 Key；
8. 使用专用测试 Token 验证 Models、对话、图片、视频创建与查询；
9. 核对 Call、Attempt、Outbox、回调与计费事实。

旧库首次转入 `gw_*` 结构时，才执行 `audit`、`audit-deep`、`import-legacy`、`import-runtime`、`import-video-assets`、`import-ai-files`。需要核验旧加密凭据时执行 `verify-crypto`。这些命令仍会读取历史目录、发布和部署字段；它们是迁移兼容约束，不应通过已移除的发布、证明或激活接口补做日常操作。

日常修改直接更新活动配置，不创建配置快照或部署代次。

`migrate up` 会把既有 `sk-prism-<48位十六进制>` Token 的单向摘要标记为旧版摘要格式；Token 明文无需进入数据库，现有客户端 Key 仍可使用。迁移集成测试会核验该兼容路径，切换后的专用测试 Token 验证也不得省略。

不得滚动混跑会同时处理同一旧任务的两个版本。正式切换前不能把仓库测试结果写成生产上线结果。

## 9. 软件发布验证

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
