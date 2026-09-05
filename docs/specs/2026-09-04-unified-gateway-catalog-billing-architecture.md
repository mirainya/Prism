# 统一网关目录、凭据与计费架构

状态：目标架构设计审计版；数据库与事务基础实现已加入，运行切换仍受第 8 节门禁约束
日期：2026-09-04
范围：Gateway V2、视频生成、AICost 多分组/多 Key/多模型、用户收费、上游成本与异步恢复。

## 1. 结论

Prism 只保留一套渠道、凭据、产品、选路、异步执行和计费基础设施。通用异步能力、Responses、视频等 API 保留各自的用户领域资源与适配器，但不再保留独立渠道、密钥、上游执行表和渠道级价格。

核心关系：

```text
公开操作身份 + 公开模型 -> 发布版模型操作合同 -> 可计费规格(SKU) -> 售价组件
                                                    -> 路由 -> Offering -> 产品 Transport -> 上游产品 + Transport
                                                                         -> 凭据池 -> 凭据 -> 加密凭据版本
                                                                         -> 成本方案 -> 成本组件
```

`gw_abilities` 在迁移完成后删除。运行时直接查询规范化表，不维护同步投影，不双写。公开模型只做弃用和别名迁移，不原地改变历史含义。本文描述目标架构和实施门禁，不代表仓库当前代码已经实现这些保证。

实现保持模块化单体：目录、路由、执行、计费、交付按 Go 包隔离，但共享一个数据库事务边界。除非未来出现独立扩缩容的实证需求，不拆微服务、不引入分布式事务，也不建设通用工作流或规则 DSL。

实施模式固定为一次性切换，不保留向后兼容层：新代码不读取旧模型、不写旧表、不维护旧 API 投影、不做新旧双写，也不从旧配置回退。旧数据若需保留，只通过停机期间的单向离线导入进入新结构；导入完成并核对后删除旧表及旧字段。公开接口只实现本设计列出的操作合同，不承诺旧接口中未列出的历史行为。

## 2. 必须满足的不变量

1. 一个规范化请求在一个目录发布版中只能匹配一个公开 SKU。
2. 用户售价只由公开 SKU 决定，不因实际选中的渠道、分组或 Key 改变。
3. 上游成本只由 Attempt 固定的 Offering 与成本方案决定；每个 Attempt 按组件声明的计费事件产生独立成本记录，事实不明时保持待核对。
4. 多个同分组 Key 共享产品与成本配置，不复制模型清单。
5. 已发布目录不可修改；任务固定目录发布版，执行固定 Transport、Offering、成本和凭据版本。
6. Key 不以明文保存，不写日志，不由管理 API 返回。
7. 异步提交结果不明确时禁止自动重提，除非上游支持幂等提交或按请求 ID 查询。
8. 凭据并发名额有 `request` 和 `task` 两种范围：前者在本地 HTTP 交换结束后释放，后者只有在上游任务终态且上游已确认结束、确认不存在，或经审核的供应商合同明确保证最长执行期限届满即已结束时才能释放；`terminated_unknown` 的 task 名额只转为 `recovery_required`，不能仅凭本地 TTL 释放。
9. 用户预授权、结算、释放和退款均以调用为粒度且幂等；内部重试不会重复向用户收费。
10. 金额以及会影响计费、预算、并发或资格边界的数量（时长、Token、字节、步长、倍率）全程使用有界整数或定点十进制，明确小数位、舍入和溢出规则；输入不得先解码为二进制浮点，不使用 `float64` 表达这些事实，也不做隐式汇率换算。
11. 请求正文、提示词、素材和回调载荷分别只保存一次可执行副本；结果源 URL 只能存在于 ResultDeliverySource，且每个交付同时最多一个活动源。正文列表查询永不加载大字段或 Base64。任务表中的提示词、参数和结果字段只能是脱敏的查询投影，规范化结果只引用 ResultDelivery，不复制其源 URL、Base64 或托管对象地址。
12. 取消能力由实际产品 Transport 决定；上游不支持时，任务提交后不展示或接受取消。
13. 一次调用只从一个资金账户收费；Token 额度是预算限制，不是第二个资金余额。
14. 请求先按发布版的公开操作合同完成校验、默认值填充和规范化，再选择 SKU；SKU 与选路不得改变参与选择或影响售价的字段。
15. 同一凭据池内所有可执行凭据必须具有相同产品权益和商业条件；不一致的凭据必须拆到不同池。
16. 发布版内的目录关系必须使用带 `release_id` 的复合外键，数据库不能接受跨发布版引用。
17. Token 必须属于发起调用的用户；资金账户、预算窗口、预授权和调用的归属在创建时一次校验，之后不可变更。
18. 引用上游有状态资源的后续调用必须保持 Transport 声明的状态作用域；不能静默换到不兼容的渠道、产品或凭据。
19. 同一 Call 同时最多存在一个未终止 Attempt；自动重试严格串行，不做隐式竞速或对冲请求。
20. 一个生成 Call 的 Attempt 最多创建一个上游生成资源；提交、恢复、查询和取消始终绑定该 Attempt，不能由适配器把一个公开请求暗中拆成多个独立上游任务。对既有资源执行的具名增值动作必须创建独立 Call/Attempt，不能暗中修改原生成 Call 的 SKU 或费用。
21. 需要持久化的 Secret、执行正文、外部状态 ID、签名 URL 或回调载荷在加密密钥不可用时必须拒绝写入并使相关实例退出就绪，不能静默改存明文或省略恢复事实。
22. 对象存储键全局唯一且永不复用；写入必须使用条件创建或等价的不可变对象语义，恢复只能校验并复用首个完整对象，不能覆盖已存在字节，删除只能针对 MediaAsset 固定的对象键和版本。
23. 一个 Prism 部署只有一种不可变平台结算币种；全部 BillingAccount、Token 预算、SellRate 和用户 LedgerTransaction 必须使用该币种。上游成本可保留原币种，但不能参与用户实时换汇收费。
24. 上游错误、对象存储错误和回调响应必须先分类、限长并脱敏；Call、Attempt 和领域资源只保存稳定错误码与安全消息，可能含 Secret、URL 查询、提示词或响应片段的诊断只进入有期限的加密预览，脱敏失败时仅保存 HMAC、长度和类型。

## 3. 数据模型

### 3.1 稳定身份

- `gw_models`：稳定公开模型 ID，不承载可被重新解释的发布版配置。
- `gw_model_names`：全局稳定的规范 API 名称与别名身份；API 名称去除首尾 ASCII 空白后按 ASCII 小写规范化，并必须匹配版本化语法 `^[a-z0-9][a-z0-9._:/-]{0,127}$`，不使用随运行库变化的 Unicode 或区域化折叠；规范名称全局唯一并永久绑定一个模型 ID，停用后也不能改绑到其他模型。
- `gw_operation_contracts`、`gw_operation_routes`：前者是稳定公开 API 操作身份，例如 `responses.create`、`image.generate`、`video.generate`；后者把规范 HTTP 方法与规范路由模板永久绑定到该身份，`(method, normalized_route_template)` 全局唯一且不可改绑，同一操作可拥有多个永久别名路由。路由模板规范化固定为：方法转 ASCII 大写；模板去除首尾 ASCII 空白、必须以 `/` 开始且不得含查询串/片段；连续 `/` 合并；静态段按 ASCII 小写；参数段只能使用 `{name}` 且 `name` 匹配 `[a-z][a-z0-9_]*`；不做 Unicode 折叠或百分号解码。操作身份不依赖当前目录发布版或请求中的模型别名，是用户幂等键的命名空间。
- `gw_adapter_implementations`：不可变适配器代码身份，以 `(adapter_code, contract_version)` 永久唯一绑定实现摘要、支持的动作和最低核心语义版本；同一契约版本不能在后续部署中换成不同代码。
- `tokens`：继续作为 API 鉴权主体，保留用户归属、密钥摘要、提示、状态、失效时间和速率限制；Token 只能由服务端 CSPRNG 生成至少 192 位随机秘密并仅在创建时返回一次，摘要算法与版本必须正式保存，不接受低熵自定义 Key；不再保存资金余额或累计消费事实。
- `gateway_channels`：上游服务身份，不保存协议配置或密钥；名称刻意避开现有旧表 `gw_channels`，避免目标结构与来源表发生名称冲突。
- `gw_credential_pools`：渠道内商业分组。AICost 的上游 Key 分组对应一条凭据池，与 Prism 用户 Token 完全无关；可选的 request/task 聚合并发上限用于供应商按账户或分组共享配额的场景，状态或限额变化递增配置版本。池状态为 `active/draining/disabled`，进入 `draining` 后不再接受新 Credential 或新 Attempt，待固定执行和恢复引用结束后才可停用。
- `gw_credentials`、`gw_credential_purpose_grants`：Credential 是同一渠道内的一项逻辑上游身份，保存 `active/draining/disabled` 状态、当前版本、request 并发上限和可选凭据池；Grant 以带状态版本的类型化行授予 `execution/catalog_discovery/upstream_callback_verify` 等有限用途，状态为 `active/draining/revoked`。同一 Credential 可在供应商只提供一套认证材料时拥有多个明确 Grant，但每个引用位置都必须以复合外键证明所需用途，不能因拥有一个用途而隐式获得其他用途。只有同时为 `active` 的 Credential 与 `execution` Grant 能参与新选路；普通停用先把 Credential 或 Grant 改为 `draining`，仅允许此前已经固定它的 Attempt、回调窗口及其恢复流程继续认证。ControlPlaneRun 在进入 draining 后不得取得新发送授权，已经是 `dispatching` 的请求只作为可审计在途请求完成；全部执行和在途请求引用结束后才转为 `disabled/revoked`。安全紧急停止使用 SecretIdentity 的 `security_revoked`，不把普通用途撤销冒充安全撤销。从未授予执行用途的 Credential 不得属于池。执行 Credential 还必须具有不可变池归属、调度权重和 task 并发上限；影响选路的状态、权重、限额、用途授权或当前凭据版本变化必须递增配置版本。
- `gw_credential_secret_identities`：同一渠道内规范化认证材料的稳定不透明身份，保存当前所属逻辑凭据引用而不保存 Secret；相同认证材料只能归属一个 Credential，需要增加用途时给该 Credential 新增显式 Grant，不能复制 SecretIdentity 或凭据版本。
- `gw_credential_versions`：不可变凭据业务版本，保存 Secret 身份、可清理的加密载荷引用、创建时间和创建时已知的有效期，不直接保存可被 KEK 轮换修改的包裹结果；当前版本由逻辑凭据指针表示，停用和替换使用版本事件，安全撤销使用 Secret 身份事件。状态只能由版本状态过程修改，其他业务字段不可回写；全部引用与保留期结束后，受限过程只允许执行一次 `blob_id -> NULL` 并填写 `purged_at`。版本仍可用于发送、恢复或审计回读时 Blob 必填；清理后不得删除版本事实或重新启用。
- `encrypted_blobs`、`encrypted_blob_key_wraps`：统一保存凭据、规范化正文、上游任务/状态 ID、签名 URL 和回调载荷等敏感值的不可变密文，以及按 KEK 版本追加的 DEK 包裹记录；业务表只能引用 Blob，不能直接保存明文或包裹 DEK。
- `gw_credential_fingerprints`：Secret 身份在各保留 HMAC 密钥版本下的指纹别名；一个指纹只能解析到一个 Secret 身份。
- `gw_catalog_sources`、`gw_catalog_source_state_events`：前者是渠道目录发现入口，保存永久唯一的来源代码、不可变 AdapterImplementation、专用发现凭据引用、`active/draining/disabled` 状态、状态版本和受事务维护的非终态 Run 计数，不保存 Secret；该实现必须声明 `catalog_discovery` 动作，来源不能用可变字符串选择未审核导入器。来源的实现、渠道或凭据需要改变时创建新来源并排空旧来源，不原地改写；后者追加保存每次状态转换。只有 `active` 来源可创建新 ControlPlaneRun，并在同一来源行锁下增加计数；Run 进入 `completed/failed` 时幂等减少一次，`manual_review` 仍计入非终态。进入 `draining` 后禁止新 Run 和新发送授权，只有已经 `dispatching` 的 HTTP 可以记录返回，已经取得响应的 Run 可完成本地导入，其他非终态 Run 必须按发送事实进入 `failed/manual_review`。计数为零后才能转为 `disabled`，定期从 Run 重算不一致时阻止停用完成。
- `gw_routing_policies`、`gw_routing_policy_versions`、`gw_routing_policy_entries`：Token 可选的渠道偏好策略、`draft/published/retired` 不可变版本及类型化规则明细；明细只按稳定操作身份、公开模型和渠道表达允许/禁止与优先级，不引用发布版 Route/Offering，也不把关系 ID 藏入 JSON。Token 只能指向 `published` 版本，编辑必须创建新草稿；未发布草稿可明确放弃并退役，不能删除后重建同一版本身份。调用固定策略版本后再应用到发布版候选路由。

凭据池的渠道与外部分组标识创建后不可修改。曾获执行用途的 Credential 所属池也不可修改；Key 换组时在目标池创建新逻辑凭据并停用旧凭据，不能移动原行。改变分组等同于创建新池。活动 `execution` Grant、Credential 和凭据池的关系使用类型化复合外键与约束触发器证明，不能用无法读取父表的 CHECK 冒充；撤销 Grant 只关闭能力，不抹除历史池归属。

CatalogSource 只能引用具有 `catalog_discovery` Grant 的 Credential，并通过包含渠道 ID 与用途的复合外键固定同渠道归属；同一 Credential 同时拥有执行用途时，其目录发现调用仍使用独立的动作权限、请求日志和 request 名额，不能因此绕过运行限制。执行 Credential 可用自身身份做启用前权益探测，但只有显式 Grant 和 CatalogSource 引用才会成为正式目录来源。停用 Credential 或其发现 Grant 前，必须在同一锁序下先把全部活动 CatalogSource 转为 `draining`；来源全部 `disabled` 且非终态 Run 结束后才可完成撤销。路由策略只引用稳定渠道、公开模型和操作身份，不直接引用某个发布版的路由 ID；空策略使用系统默认顺序。Token 路由策略只能过滤或重新排序当前发布版已经合格的候选，不能增加路由或绕过能力合同、价格、Token 所有权及凭据状态。

### 3.2 不可变目录发布版

- `gw_catalog_releases`：全局目录快照，状态为 `draft/published/retired`，保存内容哈希算法、规范序列化版本、内容哈希、核心语义版本/摘要、审核人和发布时间。核心语义摘要覆盖规范化、选择器、金额/数量计算、路由哈希、状态转换、Outbox 协议、数据库约束契约版本及受限过程/触发器清单摘要，避免不同实例或数据库结构对同一目录产生不同结论；摘要输入由构建流程生成并有固定黄金向量，不能由实例启动时随意拼接。未发布草稿如被放弃，只能转为 `retired` 并保留审计事实，不能删除或重新激活。
- `gw_catalog_release_sources`、`gw_catalog_imports`：前者是草稿发布版明确选择的目录来源清单，固定 CatalogSource 及其状态版本，每个发布版与来源最多一行；后者保存该清单项按固定白名单抽取的唯一安全快照、完成的 ControlPlaneRun、抓取时间、原始响应 HMAC 和导入结果。每个清单项和每个 Run 都只能生成一份 Import；成功导入后如需重新抓取必须创建新草稿发布版，不能在同一草稿内并列两份候选事实。原始响应不持久化，未知字段默认丢弃。
- `gw_catalog_model_names`：发布版内允许解析的稳定模型名称/别名及主名称标记；只能把 `gw_model_names` 链接到同一模型的 `gw_catalog_models`，发布版只改变可见性，不能改变名称含义。
- `gw_catalog_models`：发布版内模型的展示名称、描述、能力标签、排序、可见和弃用状态；引用稳定模型身份。
- `gw_model_operations`：发布版内目录模型对稳定公开操作身份的实现，以及版本化的统一请求、响应、进度和错误合同，规范化规则及选择前默认值。
- `gw_skus`：公开操作合同下的可计费规格，保存用户幂等策略、交付策略、多结果交付判定、最大结果数、单 Call 最大公开回调事件数与载荷字节数、有限重试策略、容量等待策略及受限、版本化选择器。
- `gw_channel_transports`：Base URL、协议、路径、鉴权方案、超时及不可变 AdapterImplementation 引用；传输配置不能用同一契约名称指向另一份实现。
- `gw_transport_allowed_hosts`：Transport 下载结果或跟随重定向时允许的正式主机规则、协议和端口；规则只允许精确主机或经公共后缀校验的受控子域后缀，不接受任意通配符、IP 网段或藏在适配器 JSON 中的白名单。
- `gw_products`：一个渠道内的上游产品，保存上游模型名和能力约束，不直接绑定公开 SKU。
- `gw_product_transports`、`gw_product_transport_actions`：产品允许使用的 Transport，以及提交恢复、取消、并发名额范围、上游任务 ID 作用域、有状态资源连续性与失败副作用策略、独立状态兼容指纹、回调认证策略、验签密钥绑定时点与可选验签逻辑凭据引用、最终一致性等待、未知提交最长保留、最长执行与恢复时限、结果解析及 `fixed/refreshable` 源 URL 策略；Action 子表只声明经验证的有限具名动作、允许源状态、可执行期限、请求/响应/错误映射、幂等与不确定结果恢复合同，不提供任意动作名或脚本入口。
- `gw_offerings`：某产品 Transport 可由某凭据池购买和执行，绑定上游成本以及发布版内不可变的商业关系；不在目录行上保存可变停用状态。运行时停用由 `gw_offering_runtime_state` 管理，停用只影响新选路，已固定 Attempt 按其发布版快照继续处理。
- `gw_routes`：公开 SKU 到 Offering 的多对多映射，保存路由优先级和流量权重。
- `gw_rate_evidence`、`gw_rate_evidence_review_events`、`gw_rate_evidence_review_state`：Evidence 只保存来源类型、权威级别、来源引用、观察时间、规范化价格事实和内容 HMAC，创建后不可修改；它是历史事实，不以运行时到期续写。ReviewEvent 追加保存 `submitted/accepted/rejected/superseded` 决策、审核人、理由摘要和序号，ReviewState 以状态版本引用最新事件作为查询缓存。Evidence 创建事务同时写首个 `submitted` 事件，审核变化只能追加事件，不能回写事实。
- `gw_sell_rates`：SKU 的不可变售价，明确计量单位、数量来源、计费事件、单价、币种，并引用发布时仍为当前的 `accepted` RateEvidence ReviewEvent。
- `gw_cost_plans`：Offering 下按规范化请求选择的互斥成本方案，使用与 SKU 相同的受限选择器。
- `gw_cost_rates`：成本方案的不可变组件，明确计量单位、数量来源、确认方式、单价、币种，并引用发布时仍为当前的 `accepted` RateEvidence ReviewEvent。

公开操作合同先定义生成操作（如 `chat`、`image.generate`、`video.generate`）及可计费资源动作（如 `video.priority_queue`）的请求、响应、进度、错误分类、有限枚举、数值边界、字段依赖、必填字段、禁止字段、规范化规则和选择前默认值；只读查询不产生执行 Call，取消由固定产品 Transport 的专用取消状态机处理。未知请求字段默认拒绝，只有合同明确允许的扩展字段才能进入映射；未知上游状态、结果或错误不得猜测为成功、失败或可重试，只能保持当前可证明状态、记录有界诊断并进入告警/人工处理时限。所有参与 SKU 选择或计价的默认值只能定义在公开操作合同中，SKU 选中后不得再改变这些字段。SKU 选择器只允许等值、集合、区间和存在性判断。发布前对合同定义域做可判定的重叠与覆盖检查，拒绝同一公开操作下的重叠 SKU、未覆盖规格及永远无法匹配的 SKU，不引入通用表达式执行器。

候选路由先按公开合同、Token 策略、有状态连续性和 OfferingRuntimeState 是否为 `active` 做资格过滤，再按显式优先级分层；按优先级依次尝试，同层才使用带版本的加权 Rendezvous Hash 生成稳定候选顺序，当前层没有可用候选后才能进入下一层。候选是 Route、Offering 与池内凭据的组合，输入固定为 Call ID、路由算法版本、Route ID、Offering ID 和凭据 ID；有效权重由受限正整数的路由权重与凭据权重按版本化规则计算，零权重等同禁用，溢出直接拒绝发布。哈希、分数计算、字节序和比较规则必须使用跨平台确定的整数或明确定点算法并提供黄金测试向量，不依赖浮点库差异或进程随机数，平局按稳定 ID 排序。已尝试或不满足状态、健康和名额条件的候选被过滤后，不能改变其他候选之间的相对顺序。候选计算必须携带凭据池与凭据的配置版本及健康观测版本；事务每次只按固定顺序锁定一个候选的凭据池与凭据行，并重新验证配置版本、OfferingRuntimeState、状态、当前凭据版本和两级名额，版本变化时放弃整个旧候选顺序并从数据库新快照重新计算，其他失效只过滤该候选。健康为 `closed` 时使用主库最新已提交快照并把版本写入 Attempt，不为普通请求锁定共享健康行；健康已 `open/half_open` 时过滤候选，只有到达 `retry_at` 的 `open` 状态需要竞争半开许可时才锁定该行。健康状态是有界延迟的运行保护，不是权限、权益或计费事实；状态在选路后开启不会撤销已经创建的 Attempt。不得在一个事务中锁住整个候选集。每个 Offering 发布时按固定规范序列化计算 `execution_fingerprint`，覆盖上游地址、产品、协议、路径、鉴权方式、请求/响应/错误映射、适配器契约与实现摘要、超时、状态作用域、提交恢复和取消语义；售价、成本、权重及展示字段明确排除。任何执行语义变化都会生成新指纹，仅非执行字段变化才沿用原健康状态。状态连续性另使用 `state_compatibility_fingerprint`，只覆盖供应商状态命名空间、产品状态语义、作用域、续接请求/响应合同及适配器状态编码；地址、超时、价格、权重等不影响状态可消费性的字段不得进入。新旧 ProductTransport 只有该指纹相同且固定契约样本证明双向含义一致时才可声明兼容，不能因模型名相同或适配器名称相同直接沿用。

每条启用路由的上游产品、Transport、参数映射、响应/进度映射和错误分类必须完整覆盖 SKU 对应的公开操作合同，并通过固定样本契约测试。产品与 Offering 的能力约束只能使用和 SKU 选择器同样可判定的有限原语；供应商特有的类型化约束必须提供确定性校验器、规范化输出和覆盖证明接口，发布器无法判定重叠或覆盖时直接拒绝。JSON 只能承载该类型化结构，不能引入隐藏条件。上游不支持的公开字段必须在规范化阶段被拒绝或由公开操作合同固定默认值，不能静默丢弃。只支持合同子集的渠道必须映射为另一个 SKU。映射只能调用适配器契约注册的字段复制、重命名、常量、有限枚举转换和结构组装原语，不允许脚本、任意模板求值、动态 URL/鉴权头覆盖或访问进程环境。

费率由一个或多个具名组件组成，同一 SKU 或成本方案内组件代码唯一。计量单位至少包含：`request`、`requested_second`、`output_second`、`provider_billed_second`、`input_mtoken`、`output_mtoken`。每个组件同时保存数量来源、计费事件、步长、最小量、数量舍入方向、金额舍入模式、金额精度、单价和币种，并显式标记 `free` 或 `unknown`；零单价只有在审核确认 `free` 时有效，未知成本不能以零单价表示。同一 SKU 的售价组件必须使用一种平台结算币种，同一成本方案的组件也必须使用一种上游账单币种，禁止跨币种直接汇总。计算顺序固定为“确定数量 -> 按步长取整 -> 应用最小量 -> 组件金额量化 -> 汇总”；数量或金额越界必须报错。金额在数据库与账务接口中使用对应币种最小单位的有界整数；费率、倍率和需要小数的计量数量使用声明精度与小数位的规范十进制字符串或明确单位的整数传递。所有数值均禁止指数形式、NaN/Inf、未声明的小数位和从 `float32/float64` 往返；解析、规范化、量化和比较使用固定 Decimal 上下文，负零归一化，溢出或非零截断一律拒绝。仅用于非账务展示的比例统计可以使用浮点，但不得回写金额、数量或资格判断。售价只能使用 Prism 能确定并向用户解释的数量来源；成本可以使用经上游账单确认的数量。按秒单位来源不明时不得发布。

每条启用路由必须证明其 SKU 合同内的每个规范化请求恰好匹配 Offering 的一个成本方案；重叠、空缺和不可判定选择器都阻止发布。分辨率、服务档位等离散价格差异由成本方案选择器表达，时长或 Token 数等连续用量由组件数量表达。成本方案在发送上游请求前选定并固定到 Attempt；上游响应不得改选更有利的方案，只能补充该方案声明的实际数量。无法在发送前确定商业分组或成本方案的 Offering 不得启用。

每次选路时，候选 Credential 的当前 CredentialVersion 必须处于有效期内，其已知 `not_after` 还必须覆盖 Call 快照的最长执行、恢复、回调和可执行具名动作窗口；该版本必须拥有与目标池全部 Offering 权益指纹一致且未过期的 `valid` EntitlementState。候选 Offering 也必须拥有未过期的 `valid` CommercialState，且其商业指纹与发布版固定成本方案、凭据池分组和权威证据一致。发现权威价格、币种、计量单位、分组条件、产品权益或可售性漂移时，必须在同一事务追加对应校验事件并以状态版本把 EntitlementState/CommercialState 改为 `drift`，随后停止新 Attempt；已创建 Attempt 仍按原快照执行并把实际差异记为成本修正。只有审核并发布新目录或确认观察事实无效后才能通过新事件恢复。证据源暂时不可用时按发布版预先声明的有限宽限期处理，超过期限进入 `expired`，不能无限沿用旧权益或成本。无法证明 CredentialVersion 剩余有效期时，不得用于可能跨越其已知期限的异步任务。

新 Call 固定 SKU 前必须锁定其全部 SellRate 引用的 RateEvidenceReviewState，确认 latest event 仍等于发布版引用的 `accepted` 事件；新 Attempt 固定成本方案前对全部 CostRate 做相同检查，并同时锁定对应 EntitlementState 与 CommercialState。ExecutionHealth 使用主库当前读快照，只有状态转换或半开许可竞争才锁定；它不能替代前述商业与权益锁。审核事件被 `superseded` 后立即阻止新 Call/Attempt，不等待异步派生状态；成本观察有效期只由 CommercialState 表达，Evidence 不承担第二套运行时 TTL。既有 Call/Attempt 继续使用创建时固定的费率与证据事件。后台校验仍负责产生可观察的 `drift/expired` 状态和告警，但不是审核撤销判定的唯一来源。

启用任何自动重试的 SKU 必须能从请求合同、候选成本方案、最大总 Attempt 数、最大已接受 Attempt 数，以及提交、恢复、查询、取消、结果下载和具名动作各自的最大 HTTP 次数计算累计上游成本上界；按请求或动作发生收费的组件使用对应动作总次数上限，按接受、用量或结果事件收费的组件使用该事实可能发生的上限。轮询时限与退避必须能推导有限最大查询次数，结果复制也必须有固定下载次数和总字节上限。任何动作的成本未知或次数无界时，必须关闭可能再次产生该成本事件的自动重试，不能只限制生成 Attempt 数。

目录发布与激活分成两步。发布事务独占锁定草稿 Release，确认其每个 CatalogReleaseSource 都有唯一成功 Import、没有以该草稿清单项为目标的非终态 ControlPlaneRun，完成全部校验、冻结内容并把版本改为 `published`，但不改变运行时指针；Run 写入 Import 的事务也必须先共享锁定目标 Release 并确认仍为 `draft`，因此发布不能越过尚未提交的发现结果。未列来源的人工草稿必须通过同一发布校验和审核，不能以空来源清单跳过价格证据。运行时以事务维护的分片需求表记录各实例角色仍需加载的发布版，以及未过期 ProviderStateRef、可能产生状态引用的非终态 Attempt、排队 Call、非终态 ControlPlaneRun、尚在具名动作可执行期限内的公开资源和仍在调试重建保留期内的 Call/Attempt 所需的有限兼容性指纹或实现身份；每个来源按稳定 ID 落入固定分片，增减需求只锁定该分片 Guard、调整引用数并递增该分片代次，避免所有调用争用一行。任何可能增加、减少或转移需求的业务事务都必须先锁定来源分片 Guard，再锁业务行并复核状态；不能先修改或锁定业务行后才等待 Guard。控制面基于完整分片代次向量生成预加载与兼容性证明，各实例逐版本上报目录内容哈希、适配器实现摘要集合与就绪状态。目标部署代次及其预期实例/角色集合必须先登记为不可变激活清单，未启动的预期实例或任一必需版本未就绪都会阻止激活，不能把“当前已上报的实例”误当成完整部署范围。激活短事务按固定顺序独占锁定单行运行时指针和全部固定 Guard，确认代次向量仍等于证明所用值、清单全部就绪、记录未过期、目标发布版覆盖全部活动兼容性指纹、目标部署已上报全部活动实现身份且目录与适配器摘要一致后，才原子切换 `active_release_id`；由于业务事务先持有对应 Guard，激活不可能越过尚未提交的新需求，也不得逐行锁定全部历史 Call、Attempt、ControlPlaneRun 或 ProviderStateRef。分片数量是迁移固定常量，不能在线缩减；增加分片须新代次迁移并同时支持旧向量。扩容实例也必须加载其角色所需全部版本并核对摘要后才可进入服务；缩容通过独立部署清单变更完成，不能删除成员来绕过正在进行的激活检查。`published` 版本可同时保留多个，`retired` 禁止再次激活；退役事务必须锁定指针并拒绝退役当前活动版本或仍有需求引用的版本。回退也必须先确认目标版本已加载并覆盖当前兼容性需求，再原子切换指针；它只影响普通新调用，已有任务及其仍在承诺期内的具名资源动作继续使用原发布版。

激活核对必须同时比较目录内容哈希、核心语义版本/摘要、数据库迁移代次及受限过程/触发器摘要和发布版引用的完整 AdapterImplementation 集合；还必须核对每个预期角色对当前可写 HMAC/KEK、目标发布版可用凭据版本以及全部活动执行/恢复引用所需 KeyVersion 的 CryptoKey Readiness。任一不一致都使该实例不就绪。核心语义、数据库约束协议或适配器实现发生变化必须使用新版本身份和摘要，不能只保持配置版本字符串不变。CredentialVersion 启用和 Keyring 当前版本切换使用同一部署成员清单与就绪证明，不能绕开目录激活单独引入实例无法读取的密钥版本。

目标部署代次不是当前活动代次时，目录激活事务必须同时执行 DeploymentGeneration 的原子切换；目标就是当前代次时只更新目录指针。目标发布版激活前必须为其中每个启用 Offering 创建 `gw_offering_runtime_state=active` 的运行态行及初始状态事件，并为其共享范围及每个可执行 CredentialVersion 的凭据范围创建带初始 `closed` 事件的 ExecutionHealth；Offering 或 CredentialVersion 后续启用也必须先创建对应运行态/健康行。缺少 Offering 运行态、健康行或对象存储加密 Key 的读写就绪证明均视为就绪失败，运行时不能把缺失状态默认为健康或可用。

每个发布版目录实体都直接带 `release_id`，并具有 `(release_id, id)` 唯一键。路由到 SKU/Offering、Offering 到产品 Transport、产品 Transport 到产品/Transport、售价组件到 SKU、成本组件到 Cost Plan 均使用复合外键；产品、Transport、凭据池及可选回调验签逻辑凭据还通过渠道复合键保证属于同一渠道，凭据用途也必须由复合键或 CHECK 匹配引用位置。每次草稿内容写入必须先共享锁定父 Release 行并确认 `draft`；所有内容表的 INSERT/UPDATE/DELETE 触发器也必须先对父 Release 做锁定当前读，再对非 `draft` 状态直接 `SIGNAL` 拒绝。发布事务独占锁定同一父行后完成最终校验、规范内容哈希和状态切换，从而消除校验与编辑竞态；仅做普通快照读的触发器不足以提供此保证。发布后所有内容行及其启停状态都拒绝更新和删除；发布版只能从 `published` 转为 `retired`，放弃的草稿只能从 `draft` 转为 `retired`，两者均不可恢复。渠道、凭据等稳定运行身份可单独停用，临时健康状态只写运行态表。历史调用引用的发布版只能标记 retired，不能物理删除；不存在活动发布版时新调用必须明确失败，不能回退读取旧表或进程内默认配置。

Call 快照保存 `release_id`、公开操作合同、SKU、用户意图 HMAC、规范化执行 HMAC、路由策略版本、路由算法版本、售价组件引用、交付策略、重试策略、容量等待策略和 schema 版本；Attempt 快照保存路由、Offering、产品 Transport、适配器契约版本、执行 PurposeGrant、凭据 ID/版本、上游幂等 HMAC KeyVersion、成本方案与组件引用、未知提交最长保留期限、最长执行与恢复时限和 schema 版本，并保存选中时池/凭据配置版本、Credential 与 PurposeGrant 状态版本、实际权重、确定性分数、健康判定版本及名额计数摘要。目录内容已经由发布版保证不可变，因此快照以类型化 ID、枚举、数值和内容 HMAC 引用为主，不复制整份目录 JSON；只有外部可变事实和计算结果才保存值快照。没有创建 Attempt 的 `no_route/capacity_exhausted/capacity_timeout` 也必须在 Call 状态事件中保存有界原因码、候选集合 HMAC 和所用运行配置版本。用户意图 HMAC 在服务端默认值填充前计算，规范化执行 HMAC 在固定发布版后计算。快照是历史执行事实，不是可编辑配置，也不能反向成为新调用的默认值。任何执行阶段都不得重新按当前活动目录解析这些值。

关系契约：稳定身份表使用单独主键；`gw_model_names.normalized_name` 全局唯一且模型引用不可更新。路由策略明细在 `(policy_version_id, operation_contract_id, model_id, channel_id)` 上唯一，明确的全局或模型通配规则使用专门布尔列和生成列唯一索引，不能靠多个 NULL 逃过冲突；策略版本发布后与目录发布版一样拒绝修改或删除。发布版内容表使用 `id` 主键加 `(release_id,id)` 唯一键，所有跨内容引用同时带 `release_id` 并建立复合外键。`gw_catalog_release_sources` 在 `(release_id, catalog_source_id)` 上唯一并固定来源状态版本，`gw_catalog_imports` 对 `catalog_release_source_id` 和 `control_plane_run_id` 分别唯一；`gw_catalog_models` 在 `(release_id, model_id)` 上唯一；`gw_catalog_model_names` 在 `(release_id, model_name_id)` 上唯一，并用同时包含 `model_id` 的复合外键证明名称与目录模型一致，每个目录模型恰有一个主名称；`gw_model_operations` 在 `(release_id, catalog_model_id, operation_contract_id)` 上唯一，并以稳定操作身份的路由和方法限制可用合同；`gw_skus` 在 `(release_id, model_operation_id, selector_hash)` 上唯一，同时保存规范化选择器字节并在哈希相同而字节不同时拒绝写入；`gw_channel_transports` 在 `(release_id, channel_id, transport_code)` 上唯一，`gw_transport_allowed_hosts` 在 `(release_id, transport_id, scheme, normalized_host, port, include_subdomains)` 上唯一并复合引用同版 Transport，`gw_products` 在 `(release_id, channel_id, product_code)` 上唯一；`gw_product_transports` 在 `(release_id, product_id, transport_id)` 上唯一；`gw_offerings` 在 `(release_id, product_transport_id, credential_pool_id)` 上唯一；`gw_routes` 在 `(release_id, sku_id, offering_id)` 上唯一；`gw_cost_plans` 在 `(release_id, offering_id, selector_hash)` 上唯一；费率组件在其所属 SKU 或成本方案、组件代码和版本上唯一。发布器还必须按规范化执行语义拒绝用不同 ID 复制同一 ProductTransport/Offering/Route 后重复获得流量权重；确需拆分的商业条件必须由不同凭据池或明确产品约束表达。必需关系禁止空值，删除采用禁止删除或迁移前置检查，不使用级联删除破坏历史快照。

运行态也必须由数据库证明归属一致。ModelOperation 提供 `(release_id, operation_contract_id, id)`，SKU 提供 `(release_id, model_operation_id, id)` 唯一键；Call 同时保存稳定操作身份和发布版模型操作，并用复合外键引用二者，再引用对应 SKU，不能把一个端点的幂等记录拼到另一个发布版合同。Call 分别提供 `(id, catalog_release_id, sku_id)` 和 `(id, user_id, token_id)` 复合唯一键。Route 提供 `(release_id, sku_id, offering_id, id)`，Offering 提供 `(release_id, id, product_transport_id, credential_pool_id)`，Cost Plan 提供 `(release_id, offering_id, id)` 复合唯一键；Attempt 保存这些父级列并逐级引用，同时以 `(call_id, catalog_release_id, sku_id)` 引用 Call，不能拼接同发布版内互不相关的配置。Attempt 提供 `(call_id, id)` 唯一键并对 `(call_id, attempt_no)` 唯一；Call 可空的 `current_attempt_id/final_attempt_id` 以包含 Call ID 的复合外键引用，不能使用 `0` 代替空值，也不能指向其他 Call。Attempt 使用只在 `started/recovery_pending` 时返回 `call_id` 的生成列建立唯一索引，从数据库层保证一个 Call 至多有一个活跃 Attempt。Call 的请求/结果 Payload 引用以 `(call_id, payload_id)` 复合外键指向自身载荷；`api_resources` 以 `(call_id, user_id, token_id)` 复合外键引用 Call，协议子表再以 `(resource_id, resource_kind)` 引用父资源，不能拼接其他调用或类型。PurposeGrant 提供 `(credential_id, id, purpose)` 复合唯一键；Attempt 以 `(credential_id, execution_grant_id, execution_purpose)` 引用它并用 CHECK 固定 `execution_purpose='execution'`，CatalogSource 同理固定 `catalog_discovery`，AsyncExecution 的回调验签引用固定 `upstream_callback_verify`。凭据链还以 `(credential_pool_id, credential_id)` 和 `(credential_id, credential_version_id)` 复合外键固定，使运行态不能选到其他池、用途或 Key。Token、Call、预算窗口、Reservation 与领域资源使用包含 `user_id/token_id` 的复合唯一键和外键固定所有权，不能只依赖应用层校验。

服务从规范化表编译按 `release_id` 寻址的只读内存索引；索引可随时重建，不是第二份配置源。每个实例启动、收到待激活版本通知和周期性检查时，从主库读取角色必需发布版集合并逐个预加载、校验和上报；所有自动执行、人工核对、未知持有或状态连续性引用结束前不得驱逐对应索引。普通首次 Call 创建事务从数据库主库共享锁定运行时指针并固定当时的 `active_release_id`，同时完成幂等记录和预授权；具名资源动作则锁定目标资源的运行需求并固定其原发布版，不读取当前指针。目录激活使用同一指针的独占锁，不能越过尚未提交的普通 Call 创建。执行端只读取 Call 固定 ID 对应的不可变索引，不对每个请求额外查询目录。任一必需索引未加载、内容或适配器摘要不一致、校验失败或不存在活动版本时，实例不进入就绪状态且相关事务不提交，不使用其他版本缓存。目录指针切换后意外未加载新版本的实例立即退出服务并告警；发布控制面分别报告数据库指针状态和各实例逐版本加载状态。凭据状态、权益验证和名额仍在主数据库实时确认。发布校验覆盖所有复合关系、能力合同、费率组件、凭据池权益和路由策略可用性；激活只切换已经发布且完成预加载的版本。

### 3.3 运行状态

状态事实归属补充：`CatalogRelease` 使用目录发布/激活审计事件，`RoutingPolicyVersion` 使用独立的策略版本审计事件，Token 撤销使用带 `auth_version` 的 Token 状态事件，BillingAccount 冻结/关闭使用账户状态事件；这些事件都保存旧/新状态、状态版本、操作者、原因和时间，不以无来源的通用事件替代，也不重复写两套等义事实。

控制面与策略补充：`ControlPlaneRun` 的失败重试只允许同一 Run `failed -> running`，必须递增状态版本并复用原对象映射；不得新建 Run 掩盖未知交换。`RoutingPolicyVersion` 只有在没有 Token 指向且没有待重放 Call 需要其版本时才可 `retired`；已创建 Call 继续使用其快照，不能因策略退役重写历史。

资金与名额状态补充：`Reservation` 只允许 `active -> settled/released/unknown_hold`，`unknown_hold -> settled/released`，不允许回到 `active`；`CredentialSlot` 只允许 `active -> recovery_required/released`、`recovery_required -> released`，恢复名额必须有明确终止、明确不存在或合同最长期限证据，不能由 TTL 直接释放。

状态机补充约束：目录发布版与路由策略版本允许 `draft -> published/retired`、`published -> retired`；草稿退役表示明确放弃，必须保留审计事实且不能再次激活。Token 只允许 `active -> revoked`，撤销不可恢复，轮换必须创建新 Token，旧行只读保留。`CredentialEntitlementState` 与 `CommercialState` 允许 `valid -> drift/unknown/expired`、`drift/unknown -> valid/drift/unknown/expired`、`expired -> valid/drift/unknown`；从 `expired` 恢复必须由新的 `ControlPlaneRun`、新的 `ValidationEvent` 和匹配指纹共同证明，不能复活旧事件或只刷新缓存。`MediaAsset` 只允许 `staging -> active/deleting`、`active -> deleting`、`deleting -> deleted`；上传失败、租约失效和完整性错误以状态事件记录并进入 `deleting`，不引入未声明的失败状态。BillingAccount 只有在不存在 `active/unknown_hold` Reservation、未过账 LedgerTransaction、待处理 Billing Outbox 或仍需该账户结算的执行时才可从 `open/frozen` 转为 `closed`；关闭不可恢复。上述规则同时适用于数据库过程、触发器、应用状态机和迁移映射。

- `gw_credential_slots`：凭据并发名额持有，正式保存凭据、可空凭据池及 `request/task` 范围；task 名额绑定 Attempt 且必须带执行凭据池，request 名额绑定一次 `channel_request_logs` 记录。执行请求的未释放记录同时占用池级与凭据级上限；没有执行池的目录发现等控制面请求只占对应凭据级上限。
- `gw_credential_pool_state_events`、`gw_credential_state_events`、`gw_credential_version_events`：凭据池事件保存 `active/draining/disabled` 追加式状态事实及配置版本；Credential 事件保存 Credential 的 `active/draining/disabled` 状态；凭据版本事件保存 `preparing/active/superseded/retired` 状态；`gw_credential_purpose_grant_events` 保存用途授予和撤销事实；`gw_credential_secret_identity_events` 保存认证材料身份级的 `security_revoked` 事实。普通轮换只替换当前版本；任一认证材料被安全撤销时，所有引用该 Secret 身份的历史和当前版本都立即禁止解密，不能遗漏转移前后的副本。
- `gw_execution_health`、`gw_execution_health_events`：前者按 `channel_id + execution_fingerprint` 保存共享 Transport 状态，并按 `channel_id + execution_fingerprint + credential_version_id` 保存凭据版本状态，两种范围由有限枚举和 CHECK 区分；后者只追加保存健康状态决策及其请求日志证据范围/HMAC，当前行以复合外键引用最新事件。原始成功失败事实只存在于 ChannelRequestLog，不复制为逐请求健康事件；后台评估器按有界时间窗和固定游标聚合，紧急错误可直接以状态版本尝试开启。候选必须同时通过适用范围。名称刻意避开现有旧表 `gw_route_states`，同一稳定渠道内执行语义与 CredentialVersion 都相同的新发布版才能沿用对应熔断记录，任一改变都自动使用新状态，避免不同渠道或新 Key 继承无关状态。
- `gw_credential_validation_events`、`gw_credential_entitlement_state`：前者按 CredentialVersion 和预期权益指纹追加保存探测序号、ControlPlaneRun、`valid/drift/unknown/expired` 结论、响应 HMAC、检查时间和有效期；后者以状态版本维护相同有限状态及最新事件引用。到达有效期时必须以数据库时间追加引用上一有效事件的 `expired` 事件并条件更新状态，不能只由进程内定时器改缓存。新 CredentialVersion 不继承旧版本结果；仅价格变化不会使相同权益指纹失效，状态只能阻止新选路，不能改写历史 Attempt。
- `gw_commercial_validation_events`、`gw_commercial_state`：前者按 Offering 商业指纹追加保存每次权威成本校验的序号、ControlPlaneRun、`valid/drift/unknown/expired` 结论、检查时间、有效期、观察事实 HMAC 和告警引用；后者以状态版本维护相同有限状态及最新事件引用。到达有效期时采用与权益状态相同的追加事件和条件更新协议。二者只能阻止新选路，不能修改发布版成本方案或历史 Attempt。
- `gw_control_plane_runs`：目录发现、权益探测和商业校验的有限类型执行单元，以可空 CatalogReleaseSource、Credential、Offering 复合外键和 CHECK 固定恰好一个与动作匹配的目标，并保存待验证指纹/配置版本、认证 Credential/Version/PurposeGrant、`scheduled/running/completed/failed/manual_review` 状态、状态版本、下一动作时间及 Worker 租约/栅栏。目录发现 Run 必须固定唯一目标 `draft` CatalogRelease 的来源清单项及其 CatalogSource 状态版本，其他动作禁止填写这些列。Run 在任何 HTTP 之前创建，重试沿用原 Run 并追加 RequestLog；进入 `completed` 的同一事务必须创建恰好一个引用该 Run 和结论 RequestLog 的 CatalogImport 或 ValidationEvent，数据库过程拒绝没有结果或结果类型不匹配的完成状态。它不承载用户调用、价格或目录正文，也不允许任意动作名。
- `gw_catalog_runtime_state`：固定主键的单行运行时指针，保存可空的初始 `active_release_id`、可空的初始 `active_deployment_generation_id`、激活代次和状态版本；普通新 Call 从主库锁定此行，目录或部署代次激活只通过此行切换。
- `gw_offering_runtime_state`、`gw_offering_state_events`：按 `(release_id, offering_id)` 保存发布版 Offering 的运行时 `active/draining/disabled` 状态、状态版本、原因和最近变更；它是可变运行保护而不是目录配置，必须通过包含 `release_id` 的复合外键引用 Offering，历史 Attempt 固定的 Offering 不受停用影响。
- `gw_deployment_generations`、`gw_deployment_members`：前者保存 `preparing/active/retired` 部署代次、核心语义、构建清单摘要和成员冻结时间，后者保存该代次的预期实例/角色清单；`preparing` 代次先显式冻结成员，冻结后才接受 Readiness 且清单不再修改。一个实例只有属于运行时指针指向的活动代次才可接受新 Call 或 Worker 租约；旧代次实例只完成已持有的有栅栏工作并退出。
- `gw_catalog_readiness`：部署代次、实例 ID/角色、发布版内容哈希、核心语义版本/摘要、适配器实现身份/摘要集合、加载结果、心跳和过期时间；记录必须引用部署成员，激活事务逐项验证清单，空清单也不得激活。
- `gw_runtime_requirement_guards`、`gw_runtime_requirements`、`gw_runtime_requirement_refs`：前两者保存固定数量分片的需求代次及按分片聚合的活动引用数；Ref 为每个来源实际占用的需求保存幂等成员事实。需求只允许三种带 CHECK 的类型化形态：`release` 必须且只能带角色与发布版，`compatibility` 必须且只能带角色、稳定操作身份、状态作用域和兼容性指纹，`implementation` 必须且只能带角色与 AdapterImplementation；不得用任意 JSON 或“类型 + ID”替代外键。Ref 使用可空的类型化父外键且恰好引用一个 Call、Attempt、ControlPlaneRun、ProviderStateRef、ApiResource 或 `gw_credential_slots` 中状态为 `recovery_required` 的恢复名额（具体列为 `credential_slot_id`），并保存 `active/released` 状态和状态版本；同一父对象与 Requirement 只能有一条 Ref。相关事务通过 Ref 的条件创建或释放幂等增减聚合计数，不能只改裸计数，也不能由异步统计修补。数据库按来源稳定 ID校验 Ref 与 Requirement 的分片一致；计数不得为负，零计数的 Requirement 保留为非活动审计行但不得出现在激活需求集合中。
- `crypto_keyring_state`、`crypto_key_versions`、`crypto_key_readiness`：KeyringState 按 HMAC、业务 KEK 或对象存储加密用途保存权威当前版本与轮换代次；KeyVersion 以正式行保存算法、外部 KMS/环境密钥引用、`preparing/readable/current/retired/security_revoked` 状态和时间窗；Readiness 证明部署成员能够执行该版本声明的 `mac/wrap/unwrap/encrypt/decrypt` 操作。对象存储用途与业务密文用途必须使用不同 Keyring 身份和访问权限。数据库不保存密钥材料，也不把可读版本集合藏入 JSON。普通写事务共享锁定 KeyringState 后查询受约束的可读版本行，轮换事务独占锁定并隔离旧写入者；当前版本必须通过复合外键属于同一用途且状态有效。
- `billing_currency_definitions`：不可变的币种代码、最小货币单位小数位、允许的金额范围和舍入规则；平台结算币种与所有费率/流水必须引用该表，未知或临时币种不得进入账务。币种定义发布后不可修改，修订只能新增代码版本且历史流水继续引用原版本。
- `billing_system_state`：固定主键单行保存部署级平台结算币种定义的 `(currency_code, definition_version)` 和初始化代次；首次产生账户、售价或流水后币种定义不可修改，所有用户资金与售价表通过包含币种及定义版本的复合外键引用该行，不能只比较应用配置。
- `billing_accounts`：用户的唯一资金账户，保存部署级平台结算币种、已入账余额、预授权总额、信用上限和冻结状态；数据库对 `user_id` 唯一，币种必须通过复合外键等于 BillingSystemState 且创建后不可修改。
- `billing_reservations`：调用级预授权，同时引用资金账户和 Token 预算窗口，记录 `active/settled/released/unknown_hold` 状态；`active` 只能转为 `settled`、`released` 或经不确定上游结果转为 `unknown_hold`，`unknown_hold` 不能由过期任务自动释放。
- `billing_events`：用户资金与预算业务事件，追加写且拥有由不可变业务父对象和动作生成的唯一事件键；正式类型至少区分 `funding_credit/funding_debit/reservation_created/reservation_held_unknown/reservation_settled/reservation_released/refund/charge_adjustment/budget_limit_exceeded`。Reservation 事件必须引用对应 Reservation 与状态版本，退款和扣款修正必须引用原结算事件，预算超额事件必须引用导致超额的结算或修正事件，外部入账或审批必须使用类型化来源引用和来源侧唯一键；不能用调用方任意生成的幂等串或一个通用“调整”事件代替充值、扣款、未知持有、释放、退款或预算异常语义。
- `upstream_cost_evidence`、`upstream_cost_events`：Evidence 是不可变的规范化供应商账单行或人工审批，保存受限来源类型、渠道、外部唯一键、事实 HMAC、审核身份和时间，不保存未脱敏原文；Event 按 Attempt 与组件追加保存事件序号、增减方向、差额、计量数量、币种、`estimated/reported/manual` 来源和 `pending/confirmed/disputed/voided` 核对状态，并使用可空的 RequestLog、ResultDelivery 或 UpstreamCostEvidence 类型化父引用及 CHECK 表达唯一来源，不覆盖前序事件。`voided` 只表示被后续差额事实取代，不删除事件；组件净额必须由全部非无效事件链重算。
- `billing_posting_rules`：不可变、带版本的账务过账规则，按 BillingEvent 类型固定金额来源、借贷系统科目、允许方向、预算影响和币种范围；规则发布后不可修改，修订只能追加新版本，历史 BillingEvent 永远引用创建时版本。
- `ledger_accounts`、`ledger_transactions`、`ledger_entries`：不可变科目、按币种平衡的追加式交易和分录，交易关联 `billing_events`；LedgerAccount 必须在 `billing_account_id` 与受限 `system_account_code` 中恰好选择一个并建立真实外键，不使用任意 owner 类型。用户资金账户映射到唯一用户科目，平台收入、应收、退款等对手科目使用显式代码，`billing_accounts` 的余额只是受事务维护并可由已过账流水重算的缓存，`api_calls` 中金额只是摘要。LedgerTransaction 固定单一币种并只允许 `assembling -> posted`，LedgerEntry 通过包含币种的复合外键同时引用 Transaction 与 Account；只有 `posted` 交易影响余额，过账后头与分录均不可修改。
- `token_budget_policies`、`token_budget_policy_activations`、`token_budget_windows`、`token_budget_adjustment_events`：Token 的不可变版本化周期/终身预算规则、追加式生效计划、具体窗口和追加式限额调整事实，全部使用部署级平台结算币种，不保存可充值余额；预算只限制调用，不参与用户资金借贷。Activation 保存同 Token 的可空前驱 Activation，形成从首条开始的唯一追加链；有效策略是数据库当前时间之前最后一条 Activation 固定的版本，因此每个 Token 在任一时刻只命中一个策略及其一个窗口，Reservation 不使用空窗口引用。周期策略的新 Activation 只能位于链尾策略确定的下一窗口边界；终身或 `unlimited` 策略没有周期边界，替换它们必须使用严格晚于链尾且不早于当前数据库时间的明确 UTC 生效时点，禁止回填生效。`unlimited` 的窗口使用 `limit_amount=NULL` 并继续记录预留与已用；后续 Activation 生效后旧窗口只供既有 Reservation、退款和审计引用，不再接受新 Call。Policy、Activation 和已生成窗口均不可修改或删除，修订只能追加更晚的 Activation；需要立即禁止新调用时直接冻结 Token。调整当前窗口限额时追加 AdjustmentEvent 并审计，不能回写初始限额，也不能把有效限额降到当前 `used_amount + held_amount` 以下。有限窗口对 `(policy_id, window_start)` 唯一且同一 Policy 内区间不得重叠，Call 创建时固定唯一命中的窗口。窗口只能由策略的时区、锚点、周期和日历算法版本确定性生成；Activation 与窗口创建都按全局顺序独占锁定 Token 和链尾 Activation，再锁定引用的 Policy/窗口，以锁定当前读复核前驱、相邻窗口和数据库时间，并由复合外键与触发器拒绝跨 Token 前驱、分叉、倒序生效或不符合算法的边界，不能接受管理端任意起止时间。
- `fx_rate_snapshots`：只供成本与利润报表使用的不可变汇率事实，保存币种对、时点、定点汇率、来源证据和审核状态；它不参与用户实时收费，也不能修改原始币种流水。
- `media_assets`、`media_asset_refs`：图片、视频、音频和允许文档等不可变原始对象的元数据、所有者用户与创建 Token、`input/result/file` 对象用途、全局唯一且永不复用的私有对象键、可选对象版本/ETag、`staging/active/deleting/deleted` 生命周期与状态版本，以及 AIFile、Call、ProviderStateRef、ResultDelivery 对对象的类型化强引用；对象键由服务端生成。输入只由 Call 引用，交付结果只由 ResultDelivery 引用，文件与会话分别由 AIFile 和 ProviderStateRef 引用，不让 Attempt 再保存同义引用。对象按用户与 Token 隔离，禁止跨所有权边界做内容去重，避免对象存在性侧信道和删除归属歧义；同一所有权范围内复用仍须创建可审计的强引用。不再由视频或 Files 模块单独保存字节和管理对象生命周期。
- `ai_files`：保留 Files API 的稳定公开文件 ID、文件名、用途、状态和所有权，只通过复合外键引用同一用户与 Token 的 MediaAsset；不得继续把完整文件存入数据库 `longblob`。删除文件资源只释放其 MediaAssetRef，仍被 Call 或 ProviderStateRef 引用的对象不能删除。
- `provider_state_refs`：上游有状态响应、会话等资源的单份加密标识、创建 Attempt、Transport/Offering/凭据链、固定状态兼容指纹、状态作用域、`active/expired/revoked` 生命周期、状态版本、到期时间和所有者；公开领域资源只引用该记录，不直接保存可执行的上游标识。加密标识 Blob 仅在状态可继续或仍被恢复/审计调试强引用时必填；保留结束后同一事务关闭全部 Alias、清除 Blob 引用并保留不可逆 HMAC、长度和状态事实。
- `provider_state_id_aliases`：ProviderStateRef 在各保留 HMAC 密钥版本下的作用域别名，供重复检测和安全核查；不复制上游状态 ID 原文。

声明有状态连续性的 ProductTransport 必须证明状态 ID 在其作用域及全部 ProviderStateRef 有效期内不复用；Alias 唯一键冲突必须停止创建并告警。无法证明时不得发布需要 `previous_response_id` 或同类上游状态续接的能力，不能仅靠本地公开资源 ID 假定两个上游状态相同。
- `async_outbox`：与业务状态同事务创建的调度和回调事件，投递成功后只更新投递状态；使用一组可空的类型化父外键并由 CHECK 保证恰好引用一个 Call、Attempt、AsyncExecution、ControlPlaneRun、UpstreamCallbackReceipt、ResultDelivery 或 CallbackDelivery，不使用无法建立外键的任意“类型 + ID”。
- `upstream_task_identities`、`upstream_task_id_aliases`：上游任务身份是独立稳定事实，保存 Transport 声明的作用域、唯一加密任务 ID Blob、可空且唯一的最终 AsyncExecution 绑定、`unbound/bound/expired` 状态、状态版本和期限；这是 TaskIdentity 与 AsyncExecution 绑定的唯一权威方向，AsyncExecution 不再保存反向指针。Alias 保存该身份在各保留 HMAC 密钥版本下的作用域摘要。提交响应与含可信任务 ID 的已认证早到回调都先创建或复用同一 TaskIdentity，再原子绑定执行；纯绑定令牌回调直接绑定候选 AsyncExecution，不创建虚假的任务身份，避免把临时 ID 分别复制到 AsyncExecution 和 Receipt。未绑定身份的期限不得早于关联 Receipt、提交不确定窗口、最大执行恢复窗口和审计查重窗口中的最晚时点；仍存在可绑定 Receipt 或 Attempt 时禁止失效。进入 `expired` 且全部恢复、查重和审计调试引用结束后，清理事务关闭全部 Alias 并清除 Blob 引用，只保留不可逆摘要与绑定历史。
- `callback_binding_token_aliases`：AsyncExecution 创建的专属回调绑定令牌在各可读 HMAC 密钥版本下的别名；令牌明文必须另存于用途隔离的 EncryptedBlob，供安全重试和恢复时重建同一回调 URL，主表、普通日志和诊断预览不得保存明文。它在同一执行的整个回调窗口内可重复认证事件，不以首次使用即失效。
- `upstream_callback_receipts`：通过认证的上游回调接收记录，保存事件唯一键、实际验签版本、可空 TaskIdentity 与最终 AsyncExecution 引用、状态版本及 `received/processed/manual_review/rejected` 状态；绑定 AsyncExecution 前不改变任务状态。入口只按 Transport 白名单抽取处理所需字段并加密限期保存，原始正文计算 HMAC 后丢弃；需要任务 ID 时引用唯一 TaskIdentity，不另存临时副本，处理后删除临时事件载荷，长期只保留审计元数据和业务规范化结果。认证失败只进入限速、聚合安全指标和有界审计，不保存可执行 Receipt 或攻击者载荷。
- `upstream_callback_receipt_aliases`：回调事件身份在各保留 HMAC 密钥版本下的别名，保证轮换前后的同一供应商事件仍只生成一个 Receipt。
- `callback_delivery_attempts`：用户回调每次真实 HTTP 投递的追加式记录，状态限于 `dispatching/succeeded/failed/unknown`，保存投递序号、固定 CallbackDelivery 载荷 Blob 引用、请求载荷 HMAC、响应 HMAC 与完整性标志、HTTP 元数据和有界脱敏错误；先持久化 `dispatching` 再发出网络请求，超时或崩溃后把本次记录为 `unknown`，后续至少一次重试创建新 Attempt，不覆盖历史，也不改变原回调事件身份或载荷字节。
- `state_transition_events`：Call、Attempt、AsyncExecution、ControlPlaneRun、TaskIdentity、ProviderStateRef、UpstreamCallbackReceipt、交付、素材、名额和 RuntimeRequirementRef 的追加式状态转换事实，保存旧/新状态、状态版本、原因码、证据摘要和操作者；使用可空的类型化父外键并由 CHECK 保证恰好一个父引用，不使用无法验证归属的多态字符串。Reservation 使用 BillingEvent，凭据、CatalogSource、ExecutionHealth 与 Secret 身份使用各自的追加式事件，目录发布/激活使用目录审计事件，不能再重复写一份通用状态事件。当前状态行是受事务维护的查询缓存，不能替代事件历史。

关键运行态唯一约束如下：

- task 范围的 `gw_credential_slots` 对 `attempt_id` 唯一，request 范围对 `request_log_id` 唯一；`async_executions` 对 `attempt_id` 唯一，`billing_reservations` 对 `call_id` 唯一。
- `gw_credential_validation_events` 对 `(credential_id, credential_version_id, entitlement_fingerprint, validation_seq)` 唯一，`gw_credential_entitlement_state` 对 `(credential_id, credential_version_id, entitlement_fingerprint)` 唯一且以复合外键引用同组最新事件；`gw_commercial_validation_events` 对 `(commercial_fingerprint, validation_seq)` 唯一，`gw_commercial_state` 对商业指纹唯一且以复合外键引用同一指纹的最新事件；`gw_deployment_generations` 对单调 `generation_no` 唯一，并使用活动生成列保证最多一个 `active` 代次；`gw_deployment_members` 对 `(deployment_generation_id, instance_id, role)` 唯一；`gw_catalog_readiness` 对 `(deployment_generation_id, instance_id, role, release_id)` 唯一。
- `gw_control_plane_runs` 为每种非空目标建立包含动作、目标 ID、待验证指纹及配置版本的活动生成列唯一索引，约束 `scheduled/running/manual_review`，使同一事实检查最多有一个非终态 Run；终态历史可保留并由新 Run 重新验证。Run 固定的认证链使用与 Attempt 相同的 CredentialVersion 和 PurposeGrant 复合外键，目标与认证 Credential 必须属于同一渠道；目录发现必须固定 CatalogReleaseSource 对应 CatalogSource 的 Credential，且创建触发器拒绝已有 Import 的清单项再次创建 Run；权益探测必须使用被验证 Credential 自身。`gw_catalog_imports` 以包含 Release、CatalogReleaseSource 与 ControlPlaneRun 的复合外键证明三者属于同一目标，唯一键禁止第二个成功 Import。
- `gw_credential_pool_state_events` 对 `(credential_pool_id, state_version)` 唯一；`gw_credential_state_events` 对 `(credential_id, state_version)` 唯一；`gw_credential_purpose_grants` 对 `(credential_id, purpose, grant_seq)` 和 `(credential_id, id, purpose)` 唯一，并使用只在 `active` 时返回 Credential 与用途的生成列保证每项用途最多一个活动 Grant。普通停用事务锁定 CredentialPool、Credential 与相关 Grant，把目标从 `active` 改为 `draining`、递增对应配置/状态版本并追加状态事件；发现用途还必须先停用全部 CatalogSource。清理事务只有在全部固定执行、恢复、非终态 ControlPlaneRun 或回调窗口引用结束，且相关 CatalogSource 均已 `disabled` 后才能改为 `disabled/revoked`。已停用行不恢复，重新授权创建新 Credential 或 Grant 序号。
- `gw_catalog_sources` 对 `(channel_id, source_code)` 永久唯一；`gw_catalog_source_state_events` 对 `(catalog_source_id, state_version)` 唯一。来源停用事务禁止新 Run，只有全部非终态 Run 结束后才允许完成停用并释放其实现需求或发现 Grant 引用。
- `gw_offering_runtime_state` 对 `(release_id, offering_id)` 唯一，状态事件对 `(release_id, offering_id, state_version)` 唯一；运行态行必须引用同一发布版 Offering，Offering 发布后不得通过修改目录行绕过该状态。
- `gw_execution_health` 分别使用 `(scope, channel_id, execution_fingerprint)` 和 `(scope, channel_id, execution_fingerprint, credential_version_id)` 的生成列唯一索引，CHECK 强制共享范围禁止凭据列、凭据范围必须填写同渠道 CredentialVersion；`gw_execution_health_events` 对 `(execution_health_id, health_version)` 唯一，当前状态以 `(execution_health_id, latest_event_id)` 复合外键引用自身最新事件；缺少适用健康行或 Offering 运行态行的候选一律视为不可用，不得默认 `closed` 或 `active`。
- `billing_currency_definitions` 对 `(currency_code, definition_version)` 唯一，同一币种在任一时点只能有一个 `active` 定义版本；SellRate、CostRate、BillingEvent、UpstreamCostEvent、LedgerTransaction 和 LedgerEntry 的金额字段都通过包含币种和版本的复合外键固定精度定义，历史流水不得随新版本重解释。
- `gw_catalog_runtime_state` 只允许固定主键的一行，`active_release_id` 只能引用 `published` 发布版，`active_deployment_generation_id` 只能引用 `active` 部署代次；两者可在首次初始化的同一事务从空值建立，此后的切换由激活事务与触发器共同验证。
- `gw_runtime_requirement_guards` 对固定 `shard_id` 唯一；`gw_runtime_requirements` 分别以生成列对 `release` 的 `(shard_id, role, release_id)`、`compatibility` 的 `(shard_id, role, operation_contract_id, scope_kind, scope_key, compatibility_fingerprint)` 和 `implementation` 的 `(shard_id, role, adapter_implementation_id)` 建立唯一索引，三种形态的必填与禁止字段由 CHECK 强制，不能依赖 NULL 参与复合唯一键。`gw_runtime_requirement_refs` 为每种非空父外键（包括 `credential_slot_id`）建立 `(parent_id, requirement_id)` 唯一索引，并以 `(shard_id, requirement_id)` 复合外键固定同分片 Requirement；只有 `active -> released`，释放不能删除后重建。
- `crypto_keyring_state` 对密钥用途唯一；`crypto_key_versions` 对 `(keyring_id, key_version)` 唯一，并以只在 `current` 状态返回 Keyring ID 的生成列保证每个用途最多一个当前版本；`crypto_key_readiness` 对 `(deployment_generation_id, instance_id, role, keyring_id, key_version, operation)` 唯一并复合引用部署成员；`encrypted_blob_key_wraps` 对 `(encrypted_blob_id, keyring_id, kek_version)` 唯一且复合引用同用途 KEK 版本。轮换事务独占锁定 KeyringState，在同一事务内把旧 `current` 降为 `readable`、把已验证的新版本升为 `current`，不能先后提交两个独立切换。
- 用户 LedgerAccount 对 `(billing_account_id, account_code, currency)` 唯一，系统 LedgerAccount 对 `(system_account_code, currency)` 唯一；`fx_rate_snapshots` 对 `(base_currency, quote_currency, effective_at, evidence_id)` 唯一。
- `gw_rate_evidence` 对类型化权威来源身份与规范化事实 HMAC 唯一；`gw_rate_evidence_review_events` 对 `(rate_evidence_id, review_seq)` 唯一，ReviewState 对 Evidence 唯一并以 `(rate_evidence_id, latest_review_event_id)` 复合外键引用自身最新事件。发布事务锁定 ReviewState 并要求当前状态为 `accepted`，SellRate/CostRate 再固定该事件 ID；同一审核事件不能引用其他 Evidence。
- `token_budget_policy_activations` 对 `(token_id, effective_at)`、`(token_id, activation_seq)` 和非空 `(token_id, predecessor_activation_id)` 分别唯一，前驱以包含 Token ID 的复合外键指向同一 Token 的上一 Activation；数据库只允许首条前驱为空，并在 Token 行锁下拒绝非链尾追加、分叉及生效时间不严格递增。预算调整事件对 `(budget_window_id, adjustment_seq)` 及外部审批来源唯一键分别唯一，窗口缓存的有效限额只能由初始限额与该事件链重算。
- `billing_events` 对 `(billing_account_id, event_key)` 唯一；每条可过账事件必须以 `(posting_rule_id, rule_version)` 复合外键引用创建时的 `billing_posting_rules` 版本，纯持有额事件固定为“不产生流水”的规则。Reservation 相关事件另对 `(reservation_id, reservation_state_version)` 唯一，外部资金或审批来源按各类型化父表的 `(source_parent_id, source_event_key)` 唯一。退款和扣款修正使用独立事件键，且必须以复合外键引用同一 BillingAccount 下的原结算事件。
- `upstream_task_id_aliases` 对 `(upstream_task_identity_id, hmac_key_version)` 唯一，`upstream_task_identities.async_execution_id` 非空时唯一；`provider_state_id_aliases` 对 `(provider_state_ref_id, hmac_key_version)` 唯一；`callback_binding_token_aliases` 对 `(async_execution_id, hmac_key_version)` 唯一；`api_call_idempotency_keys` 对 `(idempotency_id, hmac_key_version)` 唯一；`upstream_callback_receipt_aliases` 对 `(receipt_id, hmac_key_version)` 唯一。
- 上述可复用外部身份的作用域唯一性只约束 `matchable=1` 的 Alias：使用基于该布尔值的生成列建立唯一索引。任务 ID 与 Provider State ID 使用 Transport 声明的 `(scope_kind, scope_key, hmac_key_version, value_hmac)`；回调绑定令牌使用全局 `(hmac_key_version, value_hmac)`；用户幂等键使用 `(token_id, operation_contract_id, hmac_key_version, value_hmac)`；回调事件使用可信渠道与事件命名空间组成的 `(channel_id, event_scope, hmac_key_version, value_hmac)`。所有作用域列都由已固定目录或已认证入口生成并受类型化外键/CHECK 约束，不能接受上游载荷自报作用域。只有保留期限结束、父对象不再可查询/恢复且审计事件已写入后，清理事务才能把 Alias 改为不可匹配；历史 HMAC 仍保留，但唯一索引生成列返回 NULL，使供应商或用户按明确策略复用标识成为可能。
- Alias 的查询、失效和复用必须使用锁定当前读。失效事务先按稳定顺序锁定父事实及其全部可读版本 Alias，重新验证保留期和引用后，一次性关闭全部 Alias；新事实创建对全部可读版本插入 Alias，唯一键竞争者必须读取并复用胜出事实。输入原文仍在当前请求内时，命中旧版本 Alias 必须为同一父事实补齐缺失的可读版本 Alias；父事实按业务要求保留加密原文时，复用前还必须解密并以常量时间比较规范化字节，任何不一致都按摘要碰撞或数据库篡改处理。任一版本指向不同父事实也视为完整性故障并停止处理。这样轮换不依赖从旧 HMAC 反推新 HMAC，也不会在失效边界把同一输入同时绑定到两个可匹配事实。
- `state_transition_events` 为每种非空父外键分别建立 `(parent_id, state_version)` 唯一索引；`async_outbox` 为每种父外键分别建立 `(parent_id, action_seq)` 唯一索引，包括未绑定回调使用的 `callback_receipt_id`，从数据库层保证父对象内跨动作序号不重复；`result_deliveries` 对 `(attempt_id, result_ordinal)` 唯一；`result_delivery_sources` 对 `(result_delivery_id, source_seq)` 唯一，并使用只在 `active` 状态返回父 ID 的生成列保证每个 Delivery 最多一个活动源；`callback_targets` 对 `call_id` 唯一；`callback_deliveries` 对 `(call_id, callback_target_id, callback_event_seq)` 唯一；`callback_delivery_attempts` 对 `(callback_delivery_id, attempt_no)` 唯一。
- `api_call_payloads` 对 `(call_id, kind)` 唯一；`channel_request_logs` 使用可空的 `attempt_id/control_plane_run_id` 和 CHECK 保证恰好一个根引用，并分别建立 `(attempt_id, request_seq)` 与 `(control_plane_run_id, request_seq)` 唯一索引。Payload 的 `schema_version` 是格式身份，不参与同一 Call 的多版本保存。
- `media_asset_refs` 以可空的 `ai_file_id/call_id/provider_state_ref_id/result_delivery_id` 类型化父外键和 CHECK 保证恰好一个父引用，并为每种父类型建立 `(parent_id, role, ordinal)` 唯一索引；引用行同时携带 `user_id/token_id` 并以复合外键证明与 MediaAsset 及父对象归属一致，不能只比较资源 ID。

ResultDelivery 同时保存 `call_id/attempt_id/user_id/token_id`，分别以 `(call_id, attempt_id)` 引用 Attempt、以 `(call_id, user_id, token_id)` 引用 Call，并向 MediaAssetRef 提供相同所有权复合键；其可空 `current_source_id` 以 `(result_delivery_id, source_id)` 复合外键指向自身 Source，不能指向其他结果。CallbackTarget 以 `(call_id, user_id, token_id)` 引用 Call，CallbackDelivery 再以包含 Call 与 Target 的复合外键证明事件不能发送到其他调用的目标。Attempt 根的 ChannelRequestLog 通过包含 Call/Attempt 的复合外键证明归属；引用 ResultDelivery 时还必须以同时包含 Attempt ID 的复合外键证明下载属于同一执行。`result_fetch` 必须以 `(result_delivery_id, result_delivery_source_id)` 引用该交付的固定 Source，其他动作禁止填写 Source；ControlPlaneRun 根的日志禁止填写 Call、AsyncExecution、Delivery、TaskIdentity 或 ProviderState 引用，不能把其他任务的源 URL、对象或回调拼入当前请求。

只有 `result` 角色的 ResultDelivery 引用可在同一创建事务中预占尚未 `object_ready` 的 `staging` MediaAsset；该引用只保护上传和清理，不能用于读取地址。AIFile、Call 与 ProviderStateRef 创建引用时必须锁定素材并证明对象已就绪，同时把 `staging` 转为 `active` 或复用既有 `active` 行。约束触发器按父类型、对象用途、角色和就绪字段拒绝其他组合，不能让普通业务把未完成上传当成可执行输入。

需要按任务 ID 查询、取消或绑定回调的 Transport，必须证明该 ID 在声明作用域和整个最大活跃/恢复窗口内不复用；数据库 Alias 唯一键会拒绝窗口内第二次绑定。无法证明时不得按任务 ID 轮询或取消，只有供应商保证每个状态事件都回调到本次提交的不可猜测专属绑定 URL、且响应无需再次按任务 ID 获取时，才可采用纯绑定令牌回调模式；不满足该条件的异步 Transport 禁止发布，不能用 Attempt 本地 ID 掩盖上游标识歧义。同步上游 Attempt 只持有 request 名额；任务型上游 Attempt 仅在固定 Product Transport 声明 task 范围时，于活跃期持有 task 名额，每次提交、轮询和取消仍另持有短时 request 名额。客户端要求后台执行不会自行改变名额范围。任何重复插入都返回已存在记录，不得创建第二个名额、结算或回调事件。

`gw_credential_slots` 使用 CHECK 强制绑定字段互斥：`request` 范围必须且只能引用 `request_log_id`，`task` 范围必须且只能引用 `attempt_id`；每行必须引用 Credential，task 必须带凭据池，request 在其 Credential 属于池时也必须带同一池，并通过 `(credential_pool_id, credential_id)` 复合外键证明归属。无池 request 只允许来自没有执行池的控制面 Credential；执行请求不能把池置空来绕过聚合限额。`recovery_required` 只允许用于 `task`。释放只更新原行状态和时间，不删除后重建。

`async_outbox` 由类型化父外键和该父对象内的 `action_seq` 唯一标识，`action` 不参与唯一性；`action_seq` 跨所有动作种类严格单调递增且永不复用，重复轮询即使业务状态名称不变也必须生成新序号，重复扫描只能重试同一事件。父对象保存当前待执行序号，消费者必须同时匹配动作序号、目标状态版本与租约栅栏值，任一不符即拒绝；消费后安排下一次动作时，在同一业务事务推进父对象修订版本与动作序号，不能原地改写已投递事件。数据库与队列投递之间允许短暂重复，但消费者必须幂等且不能把重复投递转成第二次外部请求。`billing_events` 的事件键按业务动作单独生成，预授权、未知持有、结算、释放、退款和调整不能共用一个含义不清的键。

Outbox 到队列的投递使用指数退避和告警；队列不可用不能让内部提交、查询、取消、核对或计费动作永久停止，达到快速重试阈值后改用低频重试并持续保留原事件。只有载荷结构无效、父对象永久不兼容等不可自动处理的确定错误才可标记 `dead_letter`，恢复扫描器只能发现并告警，不能自动重投；有权限的人工修复和重放必须沿用原事件唯一键。面向用户的 CallbackDelivery 另按快照规定的有限次数进入自身 `dead_letter`，不影响生成与计费事实。

所有状态字段使用数据库约束限制到对应迁移声明的有限集合；每个聚合的状态转换以 `WHERE state_version = old_version` 的条件更新并递增版本，并在同一事务写入该聚合唯一的状态事实：Call、Attempt、AsyncExecution、ControlPlaneRun、交付、素材、名额和运行需求引用写 `state_transition_events`，Reservation 写其唯一的 BillingEvent，Credential/CatalogSource/ExecutionHealth/SecretIdentity 写各自的追加事件，目录发布/激活写目录审计事件。一次事务若同时跨越多个聚合，必须分别写入各聚合的唯一事件以及业务要求的 BillingEvent、成本事件、Outbox 或审计事件；不能用“一种事件”省略另一聚合的事实，也不能为同一转换重复写多套等义事件。重复处理只能命中已存在的同版本事件；状态行已改变但领域事件缺失，或事件存在而状态行未改变，都必须使事务失败。事件时间、金额、数量和计费步长不得为负；空字符串不能代表未知状态，未知必须使用显式枚举值。停机导入字段仅由导入器读取；线上目标结构不保留这些字段，线上写路径不可读取或写入。

生产数据层以 MySQL 8.0.16 以上和 InnoDB 为约束，依赖受执行的 `CHECK`、复合外键、唯一索引、行锁和事务；不提供缺少这些语义的降级写路径。涉及资金、名额、状态或目录指针的事务统一使用显式行锁和固定锁顺序；取得保护行锁后的余额、授权状态、活动名额统计与版本判断必须使用锁定当前读，并由测试验证 SQL 执行计划，禁止用普通一致性快照读做写入决策。唯一例外是明确作为有界延迟运行保护的 `closed` ExecutionHealth 快照，它不参与权限、金额或名额判定；状态转换和半开许可仍必须锁定当前读。死锁或锁等待超时只能有界重试整个幂等事务，任何外部 HTTP、对象存储或队列操作都不得位于可重试数据库事务内。

正式状态集合：Token 为 `active/revoked`；Call 为 `received/in_progress/retry_pending/completed/failed/cancelled/indeterminate`；Attempt 为 `started/recovery_pending/completed/failed/cancelled/not_created/terminated_unknown`；AsyncExecution 为 `allocated/submitting/submission_unknown/accepted/running/manual_review/cancel_requested/cancel_unknown/succeeded/failed/cancelled/not_created/terminated_unknown`；ControlPlaneRun 为 `scheduled/running/completed/failed/manual_review`；DeploymentGeneration 为 `preparing/active/retired`；CatalogRelease 为 `draft/published/retired`；RoutingPolicyVersion 为 `draft/published/retired`；CatalogSource、CredentialPool、Credential、OfferingRuntimeState 均为 `active/draining/disabled`；ExecutionHealth 为 `closed/open/half_open`；TaskIdentity 为 `unbound/bound/expired`，只允许 `unbound -> bound/expired` 与保留期结束后的 `bound -> expired`；CredentialPurposeGrant 为 `active/draining/revoked`，普通转换只允许 `active -> draining -> revoked`，经审计的确定失效可 `active/draining -> revoked`；CredentialEntitlementState 与 CommercialState 均为 `valid/drift/expired/unknown`，每次变化必须引用各自新的 ValidationEvent；RateEvidenceReviewState 为 `submitted/accepted/rejected/superseded`；CredentialVersion 为 `preparing/active/superseded/retired`；CryptoKeyVersion 为 `preparing/readable/current/retired/security_revoked`；LedgerTransaction 只允许 `assembling -> posted`；UpstreamCallbackReceipt 为 `received/processed/manual_review/rejected`；Reservation 为 `active/settled/released/unknown_hold`；ProviderStateRef 为 `active/expired/revoked`；CredentialSlot 为 `active/recovery_required/released`；ChannelRequestLog 为 `prepared/dispatching/not_sent/sent/response_recorded/unknown`；MediaAsset 为 `staging/active/deleting/deleted`；ResultDeliverySource 为 `active/superseded/invalid/consumed`；ResultDelivery 为 `pending/ready/delivery_failed/expired`；CallbackDelivery 为 `pending/sending/succeeded/failed/dead_letter`；CallbackDeliveryAttempt 为 `dispatching/succeeded/failed/unknown`；AsyncOutbox 为 `pending/dispatching/succeeded/failed/dead_letter`；BillingAccount 为 `open/frozen/closed`；UpstreamCostEvent 的核对状态为 `pending/confirmed/disputed/voided`；ApiCallIdempotency 为 `active/expired/retired`。除文档定义的迁移外禁止新增隐式状态；公开 API 可隐藏内部状态，但管理查询必须返回真实状态。

目录发布版与路由策略版本只允许 `draft -> published/retired`、`published -> retired`，不可恢复且发布后内容拒绝修改；`retired` 草稿表示明确放弃，必须保留审计事实且不能再次激活。CatalogSource、CredentialPool、Credential 与 OfferingRuntimeState 均只允许 `active -> draining -> disabled`，停用前必须满足全部固定执行、恢复、交付和控制面引用已结束。CredentialVersion 状态为 `preparing/active/superseded/retired`，正常路径只允许 `preparing -> active -> superseded -> retired`；未启用且确认无引用的准备版本可经审计从 `preparing -> retired`，当前版本指针只能指向 `active`，安全撤销由 SecretIdentity 事件单独表达。CryptoKeyVersion 的新版本正常路径为 `preparing -> readable -> current`，当前版本轮换时旧 `current -> readable -> retired`；未成为 `current` 且确认无引用的候选可经审计从 `preparing/readable -> retired`。任一尚未 `security_revoked` 的版本（包括 `retired`）均可因密钥安全事件转为终态 `security_revoked`；这些配置状态的变更也必须使用状态版本和追加审计事件。

ResultDeliverySource 只允许 `active -> superseded/invalid/consumed`，终态不可恢复；`consumed` 仅表示托管副本已按该 Source 完整发布，`invalid` 只能由过期或可信校验失败产生，`superseded` 只能由同一交付的新 Source 替换产生。ProviderStateRef 只允许 `active -> expired/revoked`，过期或撤销后不得重新激活；MediaAsset 的正式状态只允许 `staging -> active/deleting`、`active -> deleting`、`deleting -> deleted`，上传失败、租约失效和完整性错误以状态事件记录并进入 `deleting`，不新增隐式失败状态；删除后不能通过重建复用对象键。

CredentialEntitlementState 与 CommercialState 均允许 `valid -> drift/unknown/expired`、`drift/unknown -> valid/drift/unknown/expired`、`expired -> valid/drift/unknown`；除本地时钟追加 `expired` 外，恢复 `valid` 必须来自新的 ControlPlaneRun、全新 ValidationEvent 和匹配的权益/商业指纹，不能复活过期事件或只刷新缓存。BillingAccount 只允许 `open -> frozen/closed`、`frozen -> open/closed`，冻结期间拒绝新预授权但允许既有调用结算或释放；关闭后只读，不得重新开放。UpstreamCostEvent 核对状态只允许 `pending -> confirmed/disputed/voided`，`confirmed` 不可回退，争议通过新的人工事实与差额事件解决；任何状态变化都必须带状态版本和原因证据。

ApiCallIdempotency 只允许 `active -> expired -> retired`；`expired` 仍保留键冲突和审计事实但禁止协议重放，`retired` 仅在资源、审计和键复用保留期均结束后关闭可匹配 Alias，不能删除幂等事实后重新创建。

ControlPlaneRun 只允许 `scheduled -> running/failed`、`running -> completed/failed/manual_review`、经审计的 `manual_review -> completed/failed`，以及失败重试时同一 Run 的 `failed -> running`。创建 Run 时先锁定来源分片 Guard 并增加其 AdapterImplementation 需求；`completed/failed` 事务以同一顺序释放需求，调试保留期仍需重建时则延后释放。可证明未发送或合同允许重试的暂态错误在 `running` 内追加新 RequestLog；发送结果不明且无安全恢复合同则进入 `manual_review`，不能创建一个新 Run 来掩盖未知交换。只有 `completed` Run 能在同一事务生成有效 CatalogImport 或 `valid/drift/unknown` ValidationEvent；本地时钟触发的 `expired` 事件直接引用前一验证事件，不伪造网络 Run。

ExecutionHealth 只允许 `closed -> open`、到达数据库 `retry_at` 后由一个候选以状态版本和短租约取得 `open -> half_open`、以及 `half_open -> closed/open`；`open/half_open` 不参与普通候选，只有持有该健康行唯一半开探测租约的 Attempt 可发送一次合同允许的探测请求。共享 Transport 与凭据版本两级状态都适用时，候选必须在同一短事务按稳定 ID 同时取得所需半开许可；任一取得失败则事务回滚且不创建 Attempt。普通请求结果由后台评估器按固定游标读取 RequestLog，在事务外计算有界证据窗口，再以健康版本条件追加 HealthEvent 并更新状态；半开结果和明确的紧急错误可直接执行同一条件更新。迟到结果只能进入下一证据窗口，不能覆盖更高版本状态；租约失效把 `half_open` 重新转为 `open`，不能直接恢复为 `closed`。CatalogSource 只允许 `active -> draining -> disabled` 且不可恢复；状态变化追加 SourceStateEvent，并与新 Run 创建通过来源行锁线性化。

DeploymentGeneration 只允许 `preparing -> active/retired`、`active -> retired`；未激活且确认无成员或运行需求引用的失败准备代次可经审计直接退役。切换部署代次的事务独占锁定运行时指针，确认目标成员已冻结、全部成员对当前活动发布版及所有运行需求和密钥版本就绪后，把旧活动代次改为 `retired`、目标改为 `active` 并更新指针；三步任一失败全部回滚。`retired` 不可恢复，回退必须创建新的更高代次并重新证明成员就绪。仅切换目录发布版时可继续使用当前活动部署代次，但仍需该代次对目标发布版提供完整 Readiness。

RateEvidenceReviewState 只允许 `submitted -> accepted/rejected` 与 `accepted -> superseded`；`rejected/superseded` 终止该 Evidence，判断或事实需要修订时创建新 Evidence，不能让原记录恢复为 accepted。发布必须按来源类型的固定最大证据年龄检查 `observed_at`，并确认 ReviewState 的 latest event 正是费率引用的 accepted 事件；审核状态在发布锁内变化会使发布事务失败。证据超过发布新鲜度只阻止新发布，现有发布版的运行时成本新鲜度继续由 CommercialState 管理。

ResultDelivery 只允许 `pending -> ready/delivery_failed`、`ready -> expired`；复制重试在截止前保持 `pending` 并更新尝试元数据，`delivery_failed/expired` 不自动恢复。进入首次 `ready/delivery_failed` 时同事务只创建一个 `reconcile_delivery` Outbox；其消费者按 Call 快照在一个事务内判断全部结果是否已满足收费条件，幂等结算或释放，并创建对应 CallbackDelivery 及其发送 Outbox，避免把同一 Outbox 当成没有消费位点的广播。Delivery 自身不决定费用。公开合同允许交付过期通知时，`ready -> expired` 也必须创建独立 CallbackDelivery；不允许通知时不得因内部过期事件发送回调。CallbackDelivery 只允许 `pending/failed -> sending`、`sending -> succeeded/failed/dead_letter`；每次 `pending/failed -> sending` 都先创建新的 CallbackDeliveryAttempt 并提交，随后才允许发送网络请求；`sending` 租约失效后回到 `failed`，`dead_letter` 只有在 `replay_expires_at` 前、载荷仍存在且有权限的人工重放才能回到 `pending`，并沿用原事件 ID、版本和载荷 HMAC，期限后保持终态并清理密文。CallbackDeliveryAttempt 只允许 `dispatching -> succeeded/failed/unknown`，`unknown` 仅表示单次网络结果不明，后续重试必须新建序号，不能覆盖原记录。所有转换都使用状态版本，不能删除终态行后重建。

AsyncOutbox 只允许 `pending -> dispatching`、`dispatching -> succeeded/failed/dead_letter` 和 `failed -> dispatching`；`dead_letter` 只能在人工修复并重新核对父对象仍可处理后回到 `pending`，不得由扫描器自动重投，重放必须沿用原事件唯一键和载荷。投递租约失效时，`dispatching` 由恢复器按状态版本转为 `failed`；确定的载荷或父对象永久错误才可进入 `dead_letter`。

UpstreamCallbackReceipt 只允许 `received -> processed/manual_review/rejected` 与经审计的 `manual_review -> processed/rejected`。入口事务按已认证载荷创建或复用可选 TaskIdentity：载荷含可信任务 ID 时引用 TaskIdentity，纯绑定令牌模式可直接记录候选 AsyncExecution 而不虚构任务身份；随后创建 `received` Receipt、事件 Alias 和以该 Receipt 为父的处理 Outbox。消费者先从无锁快照解析候选父 ID，再按全局顺序锁定本次状态变化需要的账务行、Call、Attempt、AsyncExecution、可选 TaskIdentity/Alias 与 Receipt，并以锁定当前读复核全部关系。成功应用或判定为迟到无效后进入 `processed`，身份已绑定其他执行、候选不唯一或证据矛盾时进入 `manual_review`，确认认证有效但业务载荷永久不合法时进入 `rejected`。只有依赖 TaskIdentity 且它尚未绑定、也没有经绑定令牌唯一确定执行并仍在允许窗口内时，Receipt 保持 `received` 并按固定截止时间安排同一父对象的新处理序号；TaskIdentity 的首次绑定事务必须为仍处于 `received` 的关联 Receipt 创建新的处理 Outbox，避免早到回调因首次消费早于提交响应而永久停滞。超过绑定期限仍无唯一候选才进入 `manual_review`。重复回调只返回已有 Receipt，不创建第二个 Receipt 或同序号处理事件；`processed/rejected` 不回退。

上游成本允许 `estimated/reported/manual` 三种来源。每个 Attempt 与组件的首个事件记录初始增加额，后续账单或人工核对只追加相对当前净额的增加/减少差额；在 Attempt 行锁下分配单调 `event_seq`，数据库对 `(attempt_id, component_code, event_seq)` 唯一。每种非空类型化来源分别以 `(source_parent_id, component_code, source_event_code)` 建立唯一索引，UpstreamCostEvidence 另以 `(channel_id, source_type, external_key)` 唯一，使重复 Worker、重复账单和重复审批只能返回原事件。组件当前成本只等于其事件链按方向求得的净和，且结果不得小于零，报表不能把估算与报告值重复相加。未知成本必须标记待核对，不得当作零成本；未知成本的 Offering 可以留在草稿或关闭状态，但不能被启用路由选中。明确免费必须由已审核的 `free` 费率表示。

用户资金由唯一 Billing Account 持有，所有用户售价必须使用该账户的平台结算币种；其他币种只允许出现在上游成本和利润报表中。预授权在同一事务内同时占用资金可用额和固定的 Token 预算窗口，不计为消费；结算才产生平衡流水并增加预算已用量，未结算预授权的解除是 `release` 而不是 `refund`。调用创建时固定预算窗口，即使周期随后切换或策略修改，在途调用也不改变窗口。

只有已结算扣款的反向业务才是退款。预授权状态只能从 `active` 一次转为 `settled`、`released` 或 `unknown_hold`；`unknown_hold` 只能经独立证据或审计决策转为 `settled` 或 `released`，后续修正使用追加调整事件。每笔复式流水必须按单一币种借贷平衡，不能以汇率换算掩盖不平衡。`active` 与 `unknown_hold` 都计入资金和预算持有额；资金可用额必须满足 `入账余额 - held_amount >= -信用上限`，预算可用额为 `limit - used - held_amount`，任一不足都拒绝新预授权。结算造成预算 `used > limit` 时必须追加预算超额事件、冻结新预授权并保留欠费事实；退款只能将历史 `used` 减至不小于零，不能把已过期窗口重新开放或转移到当前窗口。每个退款事务必须独占锁定 BillingAccount，并在同一锁内按原结算事件汇总全部已提交退款，再校验“累计退款 + 本次退款 <= 原结算净额”；不同退款事件键也不能绕过该判定。旧 `users.balance` 与 `tokens.balance` 不得继续作为两套同时扣减的资金事实源。

原结算净额等于结算事件金额加其全部已提交 `charge_adjustment` 有向差额。退款和结算修正都在同一 BillingAccount 独占锁下读取该链：新增退款不得使累计退款超过修正后净额，负向修正不得使修正后净额低于累计退款或使原预算窗口已用量为负；正负修正都必须以平衡 LedgerTransaction 和原预算窗口的同额调整表达。无法满足约束的修正进入独立财务核对，不能截断金额、改写旧事件或借新事件键绕过。

跨模块事务统一按“目录运行时指针（仅普通新 Call 或激活时） -> Runtime Requirement Guard（仅需求变化或激活时） -> CatalogRelease/DeploymentGeneration/Readiness（仅发布、激活或控制面导入时） -> RuntimeRequirement 聚合行 -> Crypto Keyring（涉及唯一 HMAC 或密文操作授权时） -> Credential Secret Identity（涉及凭据声明、验签或出站认证授权时） -> User -> Token -> 幂等事实与键 Alias -> Provider State 与其 Alias -> RateEvidence Review/Entitlement/Commercial 状态 -> ExecutionHealth（仅状态转换或半开许可时） -> Billing Account -> Token 预算策略/Activation/窗口 -> Ledger Account -> Call -> Reservation -> Attempt -> AsyncExecution/领域资源/ControlPlaneRun -> Credential Pool -> Credential 与 PurposeGrant -> CatalogSource -> Media Asset -> Slot/交付/请求日志 -> 上游任务与回调绑定 Alias -> Callback Receipt 与事件 Alias -> RuntimeRequirementRef、状态/账务/审计事件及 Outbox”的顺序取得实际需要的行锁，同类多行按稳定主键排序；每个命令必须有覆盖其全部锁定读、唯一键写和外键父行的 SQL 锁计划，未列入计划的行不得在事务中临时补锁。普通需求事务从来源稳定 ID 计算分片，先锁对应 Guard 和 Requirement 聚合行，随后锁业务父行，以 Ref 的唯一条件创建或 `active -> released` 转换幂等增减计数；激活事务锁定运行时指针后按 `shard_id` 锁定全部 Guard、部署代次和就绪证明，不逐行进入后续业务锁。既有执行可从对应 Guard 开始且不得随后取得目录指针；不改变需求的事务不锁 Guard。回调入口只持久化 Receipt/Alias，不在同一事务锁 AsyncExecution；消费者先解析候选 ID，随后按上述顺序锁所需 Guard 与 AsyncExecution，再以锁定当前读复核 Alias 和 Receipt，避免“回调先锁 Alias、接受流程先锁 AsyncExecution”的反向锁序。普通首次 Call 创建事务预先生成稳定 Call ID，先共享锁定运行时指针和该 ID 对应 Guard，再依序处理 HMAC、User、Token 与幂等记录，只有确认是新事实后才创建 Call、Reservation、RequirementRef 及对应聚合计数；具名资源动作从 Guard 开始锁定原资源，不再取得目录指针。幂等命中快路径使用独立锁定事务，不取得目录指针或 Guard，未命中必须重新进入对应完整事务并再次检查 Alias。Call 创建对 User 与 Token 使用共享锁并复核用户状态、Token 状态和归属；User/Token 更新或撤销使用独占锁，因此不同 Call 可并发而目录切换或身份撤销不会越过已经开始的创建事务。预授权、结算和释放至少锁定 Billing Account、固定预算 Policy/Activation/窗口、全部涉及的 Ledger Account、Call 和 Reservation；Reservation 外键引用 Call，创建时先插入 Call 再插入 Reservation，现有行处理时也始终先锁 Call。`unknown_hold` 只能由明确的不确定上游结果转换事件创建，不能由清理任务或租约过期直接产生。结算先解除原预授权持有，再按实际金额写入一笔扣款；实际金额低于预授权时差额只是释放，不得再次记为退款或重复增加余额。实际金额高于预授权时只追加差额扣款。退款必须引用原结算事件，累计退款不得超过该结算净额，并在原预算窗口减少同额已用量；原窗口已过期时只修正历史已用量，不重新开放该窗口，也不把额度转入当前窗口。非资金性的额度补偿使用当前有效窗口的独立预算调整事件，不能伪装成退款。重复请求依靠唯一事件键返回已有结果。定期对账以复式流水重算账户余额和预算窗口；发现不一致时冻结该账户的新预授权，先修复差异再恢复。任何一个锁或流水写入失败都回滚整个事务，不发送上游请求，也不改变任务状态。

ControlPlaneRun 在上述 `AsyncExecution/领域资源/ControlPlaneRun` 位置按稳定 ID 锁定；其目标 CatalogReleaseSource、Credential 或 Offering 只通过 Run 的类型化复合外键固定，不虚构 Call/Attempt，也不得在发送事务中反向锁定可变目录内容。控制面发送命令必须在固定 SQL 锁计划中列出 Run、认证链、可选 CatalogSource、request 名额和日志。

账务流水、结算事件、退款事件和上游成本事件不受普通日志清理策略控制。配置中的账务保留天数只能作为归档阈值，不能直接删除仍用于余额重算或争议处理的数据。归档必须先生成经核对的期间期末余额、不可变归档文件、内容哈希和下一期间期初交易；恢复测试证明可由最近一期已认证期初值加后续流水重算当前余额后，才可移出在线库。

一个 `billing_event` 至多关联一笔 `ledger_transaction`，数据库对非空 `billing_event_id` 唯一；分录在 `(ledger_transaction_id, entry_code)` 上唯一，并以外键引用同币种 LedgerAccount。记账命令必须先按稳定 ID 锁定全部涉及的 LedgerAccount，在内存生成完整分录集合并验证同币种借贷总额相等、金额为正且至少各有一条，再于同一事务插入 `assembling` 交易和分录并立即调用受限过账过程。该过程复核调用方已经锁定的账户集合，锁定交易与全部分录，重新验证账户集合、币种、正金额、借贷双方和总额，原子更新账户缓存并把交易改为 `posted`；父表过账触发器执行相同拒绝检查，分录触发器禁止修改或删除已过账交易的子行，应用角色没有绕过过程直接过账的权限。任一检查或写入失败都回滚整个事务，因此正常路径不会提交 `assembling` 行；发现可见的 `assembling` 行即视为特权维护或程序违反协议，冻结相关账户并告警，不自动补过账。预授权与释放若只改变持有额而不改变已入账余额，可以没有 LedgerTransaction，但仍必须有唯一 BillingEvent、Reservation 状态版本和同事务账户/预算更新。普通业务不得修改或删除已过账 LedgerTransaction、LedgerEntry 和 BillingEvent；冲正只允许追加引用原事件的新事件与交易。

所有由 `state_transition_events` 管理的当前状态行在创建事务中必须同时写入版本为 1 的初始事件；当前行没有对应事件、事件版本不连续或最新事件与当前状态不一致时，读写过程均失败并告警，不允许以默认状态继续运行。使用专用事件表的 Reservation、Credential、CatalogSource、ExecutionHealth、SecretIdentity、目录发布和账务聚合沿用各自的初始事件规则，不重复写通用事件。

### 3.4 正式字段与 JSON 边界

身份、外键、状态、能力、操作、协议、HTTP 方法与路径、鉴权方式、回调认证方式、超时、幂等/恢复/取消/重试/容量等待策略、结果交付策略、优先级、权重、并发范围与上限、模型名、成本方案、计量单位、数量来源、计费事件、金额、币种、时间、状态版本、租约和栅栏值都必须是正式字段。

JSON 只保存供应商参数映射、复杂能力约束、带版本的适配器配置、白名单导入快照、脱敏正文和非关键结果元数据。规范化请求和结果必须有明确版本化类型；每种 JSON 都必须有 `schema_version`、对应 Go 类型和发布时校验器；JSON 内禁止保存关系 ID、启停状态、价格、倍率、计费数量、币种或 Secret。`fixed_price`、`markup_ratio`、`surcharge_percent`、`estimated_cost`、`unit_cost`、`currency` 等商业字段即使出现在适配器扩展或旧 `Pricing/ExtraConfig` JSON 中也必须在迁移时提取到正式费率/成本组件，目标写路径拒绝再次写入；适配器只能返回经过类型化解析的上游事实，不能让 JSON 直接改变用户售价。

数值表示必须有跨服务的单一约定：货币金额使用带币种小数位的有界最小货币单位整数保存，费率、倍率和其他需要小数的商业量使用固定精度/小数位的规范十进制字符串（数据库使用同等精度的 `DECIMAL`），计量数量使用明确最小单位的无符号整数；确需分数的数量必须声明固定小数位并使用同一 Decimal 上下文。每个字段在目录合同中固定最大位数、小数位、舍入模式（默认 `half_even`）、溢出行为和负值规则；API、队列、SQL 驱动和日志均按该规范传递，禁止把 JSON 数字、语言原生浮点或数据库 `DOUBLE` 作为中间格式。展示层可以格式化为字符串，但不得把展示值重新用于结算或资格判断。

### 3.5 实现边界

- `catalog` 负责草稿、校验、发布、激活和只读索引，不发上游请求。
- `routing` 接收固定 Call 快照与运行状态，返回可解释的候选顺序，不修改计费或任务状态。
- `billing` 只暴露预授权、结算、释放、退款、调整和对账命令，不依赖任何供应商包。
- `execution` 是状态机与事务编排入口，通过接口调用 routing、billing、transport 和 delivery；Worker 只加载 ID 并调用该入口，不复制业务分支。
- `transport` 以版本化类型接口实现提交、查询、取消、请求映射和响应映射；AICost、Seedance 等供应商判断只能存在于适配器和目录导入器中。
- `delivery` 只处理素材、结果对象和用户回调，不决定生成终态或用户费用。

包依赖保持单向，供应商包不得反向导入 execution、billing 或管理 API。跨模块原子操作由应用层在同一数据库事务中调用窄接口完成，不共享可变全局状态，也不建立通用插件脚本系统。

## 4. AICost 映射

1. 建立一个 AICost 渠道；按公开协议和操作分别建立 Transport，不能把聊天、Responses、图像生成、图像编辑和视频任务塞进同一配置。首期视频使用 `POST /v1/videos`、`GET /v1/videos/{task_id}` 与内容下载操作组成的版本化 Transport，后续操作复用相同渠道与凭据池，但拥有各自合同、映射和超时。
2. 每个 AICost 分组建立一个凭据池；属于该分组的多个上游 Key 作为池内凭据，不能写入或复用 Prism 用户 Token。
3. `/api/pricing` 的每个可计费模型身份建立上游产品；该模型实际支持的每个协议操作分别建立 ProductTransport，`enable_groups` 再生成 ProductTransport 与凭据池之间的 Offering。模型广场同名不等于协议合同相同，必须以已验证接口样本决定支持哪些操作。
4. `model_price`、分组倍率和原始响应只进入草稿；白名单价格事实及其 HMAC 进入 RateEvidence。模型存在分辨率或档位差异时生成多个成本方案。发布成本保存最终单位价并引用已审核证据，原始响应不进入运行表。
5. `quota_type=1` 不能区分按次和按秒；描述文字也不能作为可靠机器字段。计量单位必须经人工确认。
6. `auto` 分组只有在发送前能固定到确定的计费分组，或已证明其所有可能分组的权益、币种和成本公式完全相同并只匹配一个成本方案时才能用于路由；响应返回的实际分组与成本只用于核对，不能在发送后改选成本方案。其他情况不得启用。
7. 远端模型删除或权威价格漂移会立即使对应 CredentialEntitlementState/CommercialState 阻止新 Attempt，并在新草稿中停用或修订 Offering；已创建任务仍按固定快照处理，不回写历史。
8. 当前 AICost 文档没有声明取消接口或提交幂等保证，因此其产品 Transport 必须发布为 `cancel=none`、`ambiguous_submit=no_retry`。

当前已确认的费用资料存在模型名和计量单位冲突，因此导入器只能生成差异报告，把受影响商业指纹置为 `unknown` 并进入人工审核，不能自动覆盖已发布价格或继续把其视为已验证成本。

凭据启用前必须使用该 Key 查询实际模型清单并与 Offering 比较。发现分组权益或商业事实漂移时只停止新选路并生成新草稿，不修改历史执行。

一个凭据池的每个活跃执行凭据都必须通过该池全部 Offering 形成的权益指纹验证；产品集合、商业分组身份或合同条件不一致时，凭据不能留在该池。探测时间和响应 HMAC 进入凭据验证记录，过期验证会阻止新任务选用该凭据。价格变化只更新成本方案，不改变产品权益指纹；产品集合、约束或商业分组变化必须生成新指纹并重新验证。

## 5. 异步执行边界

- `APICall`：一次用户逻辑调用，也是唯一用户收费边界。
- `api_resources`：Call 的可选一对一公开资源父表，固定资源种类、公开 ID、Call、用户与 Token 归属；对 `call_id` 及 `(resource_kind, public_id)` 分别唯一，使一个 Call 最多创建一种领域资源，并为协议子表提供真实复合外键。
- `capability_tasks`：通用图片及其他异步能力 API 的领域资源子表，保存协议生命周期和有界展示投影；对外接口只实现新操作合同，不提供旧 `/v1/tasks` 兼容查询；稳定 `task_no` 使用父表公开 ID，它不保存上游执行、正文、路由、计费或回调副本。
- `ai_responses`：Responses API 的领域资源子表，只保存协议资源生命周期和投影数据；后台工作由 Call/Attempt 表示，仅任务型上游再创建 AsyncExecution。
- `video_tasks`：视频 API 的领域资源子表，只保存视频协议生命周期和有界展示投影；完整规范化请求经父资源关联的 Call 定位，只存在于 `api_call_payloads` 的一个版本。
- `APICallAttempt`：一次具体上游路由执行；内部换路会创建新 Attempt。
- `async_executions`：Attempt 的可选异步扩展，供图片、视频及其他任务型上游共用，并引用其 Attempt 固定的提交幂等键（不复制键原值或第二份 HMAC）、保存状态、持久化取消意图、下一动作时间和恢复信息；同步调用不创建。TaskIdentity 通过自身唯一的 `async_execution_id` 单向绑定，不在本表复制反向指针。
- `channel_request_logs`：一次真实上游 HTTP 交换。提交、恢复、查询、取消、具名资源动作、结果下载、目录发现、权益探测和商业校验各写一行并以正式动作枚举区分；每行恰好引用 Attempt 或 ControlPlaneRun，动作必须与根类型及其固定合同匹配。执行日志从 Attempt 取得认证链；控制面日志从 Run 取得待验证指纹/配置版本及实际认证 Credential、CredentialVersion、PurposeGrant 和用途，并以复合外键证明用途及渠道匹配；无认证请求显式标记 `auth=none` 且认证列全空。日志保存可选 AsyncExecution/ResultDelivery、结果下载所固定的 ResultDeliverySource、上游任务 ID 别名引用、发送前映射语义 HMAC、可选的实际请求/响应字节 HMAC 及各自完整性标志、可选诊断预览 Blob 引用、发送状态和 HTTP 元数据；该 Blob 只能是有长度上限且不可执行的脱敏预览，不能指向完整上游请求或响应。不复制上游任务 ID、源 URL 或对象地址，请求头与结构预览必须先做字段级脱敏。列表接口只读取索引列和摘要，不连接加密载荷表。
- `api_call_idempotencies`：用户幂等语义的唯一事实记录，保存稳定公开操作身份、用户意图 HMAC 及其版本、原 Call、重放状态、`replay_expires_at` 与 `key_reuse_after`，不保存原始键；重放期结束但键尚不可复用时返回稳定的过期冲突，不创建新 Call。
- `api_call_idempotency_keys`：同一幂等事实在各保留 HMAC 密钥版本下的键别名；数据库分别保证一条事实每版本最多一个别名，以及 `(token_id, operation_contract_id, key_hmac_version, key_hmac)` 只能解析到一条事实。
- `result_deliveries`、`result_delivery_sources`：Delivery 保存固定交付模式、`remote_url/inline_response` 来源种类、状态、当前 URL Source 指针、校验值及其来源、交付到期时间和重试时间；Source 只表示远端 URL，按单调来源序号追加保存来源版本、唯一加密 URL Blob、URL HMAC、供应商结果身份摘要、观察证据、到期时间和 `active/superseded/invalid/consumed` 状态。`reference` 必须使用 `remote_url`，且在 `ready` 期间恰有一个活动的自包含 URL Source，可保存供应商声明的校验值但不得冒充本地字节验证。`managed_copy + remote_url` 在复制期间恰有一个活动 Source；`managed_copy + inline_response` 禁止 Source，并以产生该响应的 ChannelRequestLog 作为类型化来源证据。两种 managed copy 都只能通过 MediaAssetRef 引用同一所有权范围的托管结果对象，本地验证的 SHA-256、长度和类型在进入 `ready` 前必填。交付模式、来源种类、Source、请求日志与素材引用的跨表形态由创建事务和约束触发器验证，不能同时发布 URL 与托管对象。
- `callback_targets`、`callback_deliveries`：Target 是 Call 可选且唯一的不可变回调目标，保存同一所有权、唯一加密 URL/签名配置 Blob、目标 HMAC 及其算法/密钥版本、允许协议与创建时策略版本；Delivery 引用该 Target，保存事件序号、唯一不可变加密载荷 Blob、载荷 HMAC 及其算法/密钥版本、投递状态、重试时间和固定 `replay_expires_at`。事件序号在锁定 Call 时从其独立 `callback_event_seq` 严格递增分配，不能借用可能不变化的执行或交付状态版本。有效期内所有重试读取同一 Target 和载荷字节，不能从已变化的任务投影重新生成；最后一个投递与人工重放期限结束后先清除各载荷 Blob，再清除 Target Blob，只保留审计摘要。

`APICall` 必须正式保存稳定 `operation_contract_id`、`catalog_release_id`、发布版 `model_operation_id`、`sku_id`、操作、唯一 `request_payload_id`、可空 `result_payload_id`、可空且唯一 `callback_target_id`、售价、交付、重试和容量等待快照，以及 `current_attempt_id/final_attempt_id`、状态版本、独立 `callback_event_seq`、下一动作时间和用于排队选路/后台同步工作的栅栏租约；`APICallAttempt` 必须保存同一 `catalog_release_id`、`sku_id`、`route_id`、`offering_id`、`product_transport_id`、`credential_pool_id`、执行 PurposeGrant、凭据 ID/版本 ID、选中时 Credential/PurposeGrant 状态版本、成本方案、成本组件快照和状态版本；`api_call_payloads` 每个 Call 和 `request/result` 用途最多一行，保存格式 `schema_version`、内容 HMAC、长度、保留期限、可空 EncryptedBlob 引用和 `purged_at`。Blob 在载荷仍需执行、恢复、重放或审计调试时必填；清理只解除 Blob 引用，Call 永久保留对 Payload 元数据行的引用。格式升级只影响新 Call，旧 Call 继续指向原载荷，不能给同一 Call 追加第二份请求正文。`async_executions` 必须保存 Transport 声明的 `upstream_scope_kind/upstream_scope_key`、状态版本、租约栅栏值、Attempt 的提交幂等键引用、回调绑定令牌 Blob 与 Alias、可选回调验签 Credential/PurposeGrant 及其固定状态版本、策略版本、密钥绑定模式及按模式固定的验签凭据版本集合、`cancel_intent_at/cancel_resolved_at/cancel_resolution`、上游查询游标、下一动作时间、独立动作序号和固定期限；提交幂等键原值、HMAC 和密钥版本只由 Attempt 保存，AsyncExecution 不得复制。其 TaskIdentity 只能通过 `upstream_task_identities.async_execution_id` 查询。上游任务 ID 明文只在 TaskIdentity 的 EncryptedBlob 中保存一次。取消意图待处理时只有 `cancel_intent_at` 非空；解决后必须同时写入解决时间和 `confirmed/rejected/superseded` 之一，CHECK 拒绝不完整组合，历史意图不得删除。作用域只能取全局、渠道、产品 Transport、凭据池或凭据等有限枚举，作用域键由固定目录引用计算，不能信任回调自行提供。旧 `ability_id/channel_id/key_id` 仅作为停机导入器的临时来源字段；新结构不保留这些字段，运行时不可读取或写入。

领域资源父表保存公开资源 ID、种类、Call 与所有权，协议子表只保存自身生命周期、稳定内部原因码和有界展示投影；子表主键同时是 `api_resources.id`，并携带固定 `resource_kind` 通过复合外键证明类型，不能仅靠应用约定。上游执行状态、结果交付状态、用户回调状态和计费状态分别保存，互不覆盖。公开资源 ID 在对应资源类型内唯一且不可修改，父资源与 Call 是一对一关系，并以包含 `user_id/token_id` 的复合外键固定归属。管理端通过 Call/AsyncExecution 展示真实内部状态，领域表不复制第二套内部执行状态。上游已成功但结果转存失败时，不得伪装成“生成失败”。有效交付模式在规范化阶段按“请求显式值 -> Token/用户配置快照 -> 公开操作合同默认值”确定，再参与 SKU 选择并保存到 Call；SKU 声明允许的模式及相关售价。渠道、Offering、Transport 和适配器不得覆盖该值。`reference` 模式保留上游 URL 及其已知过期时间，只允许无需暴露 Prism 或上游凭据即可读取、且有效期满足公开合同的自包含地址；需要上游鉴权头、过期时间未知或不能证明可用期的 Offering 不得用于该模式。`managed_copy` 模式按原始字节复制，校验内容类型、大小和 SHA-256 后写入由服务端生成的全局唯一且永不复用的私有对象键，不做转码；对外可见性由交付状态控制。复制失败时保留上游引用和错误，交付记录独立重试。调试详情按权限读取唯一规范化请求载荷，再使用 Attempt 固定的适配器与映射快照重建不含鉴权头、时间戳和短期签名 URL 的上游逻辑参数，并与发送前保存的映射语义 HMAC 比较。实际请求/响应字节 HMAC 只证明当时传输内容，不能声称可重建已过期的临时字段。

响应映射先为每个结果确定稳定序号和 `remote_url/inline_response` 来源种类；数据库以 `(attempt_id, result_ordinal)` 固定唯一 ResultDelivery。远端 URL 结果在接受可信上游成功事实的事务中创建 Delivery 与初始 Source：`reference` 直接进入 `ready`，`managed_copy` 同时创建 `staging` MediaAsset、强引用和上传租约后保持 `pending`。上游直接返回二进制或 Base64 时只允许 `managed_copy + inline_response`；流式解析器在读取每个结果字节前，以短事务创建 Delivery、`staging` MediaAsset、强引用和绑定当前 ChannelRequestLog 的上传租约，随后把原始二进制或流式解码后的字节写入对象，数据库、队列和内存完整字符串均不保存 Base64。准备事务失败时停止消费结果流，并按该 Transport 的响应恢复合同处理；不能先接收全部字节再补建素材事实。

规范化结果、任务投影与不可变回调载荷只携带稳定 Delivery ID 和 Prism 结果读取路由，由读取接口在授权后解析当前交付，不持久化可能刷新的源 URL、签名对象地址或 Base64；回调载荷必须包含交付状态。公开协议明确要求 `b64_json` 等内联结果时，下游 Codec 只能在已授权且 managed copy 为 `ready` 后，从固定 MediaAsset 原始字节流式编码到该次响应，同时执行输出长度限制和响应 HMAC；数据库、队列、重放缓存和内存完整字符串仍只保存 Delivery 引用，幂等重放期不得超过对象保留期。远端 managed copy 的重试截止时间必须早于活动 Source 过期时间；inline response 只有在固定 Transport 能按原请求 ID 查询或幂等恢复同一结果字节时才允许自动重取，否则首次流读取结果不明即进入 `delivery_failed` 或人工核对，禁止重提生成请求。首次完整对象写入固定内容 SHA-256；同一 Attempt 和序号再次取得不同哈希时保留首个完整事实并进入完整性告警，禁止覆盖或发布第二个对象。

所有托管结果都必须先保存服务端生成的全局唯一私有对象键和上传租约，事务提交后才允许以条件创建写入该键。远端下载还必须先取得适用的 request 名额并创建固定当前 Source ID、来源序号和 URL HMAC 的 `result_fetch` ChannelRequestLog；inline response 则固定原响应 RequestLog、结果序号、编码字节 HMAC 和解码字节 SHA-256。完成、失败或超时后按统一发送协议记录事实并释放名额。对象已存在时恢复器读取其版本/ETag、长度、类型和 SHA-256；完全一致才复用，任何差异都进入完整性告警，绝不覆盖。写入和校验完成后，发布事务先从无锁快照取得父 ID，再按全局顺序锁定 Call、MediaAsset 与 Delivery，并匹配上传租约、素材/交付状态版本、来源证据、对象键、对象版本/ETag、长度、内容类型和 SHA-256，再把素材与交付分别改为 `active/ready`；远端路径还必须匹配当前 Source ID/来源序号，inline 路径必须匹配原 RequestLog 和结果内容摘要。锁定后的任一父 ID 与快照不一致、来源已变化或证据不符时均不得发布。

数据库发布失败时对象仍由 `staging` 行定位，恢复器只能校验后重试发布。远端 Source 已刷新或失效时，必须在新事务创建具有全新对象键和租约的 MediaAsset、替换 Delivery 强引用，并把旧暂存素材转入正式删除流程；不能把新下载写入旧对象键。永久失败时先把交付标记为 `delivery_failed`，其强引用保留到诊断期限届满，再通过正式引用释放事务进入清理，不能留下不可定位对象。托管结果对象始终私有，只有数据库交付行和素材状态均变为 `ready/active` 后才可签发读取地址。扫描器必须先锁定素材行，确认过期且无强引用后以状态版本把 `staging/active` 条件更新为 `deleting`，提交后才在事务外按固定对象键和版本幂等删除对象，成功后标记 `deleted`；新引用只能在锁定素材行且状态可用时创建，不能引用 `deleting/deleted`。对象删除失败保持 `deleting` 并按有界退避重试；对象不存在视为幂等成功。

存在 CallbackTarget 时，对外领域状态变化和公开合同声明的交付成功、失败或失效各生成独立用户回调事件；未声明的内部过期或其他内部状态变化不生成用户回调。仅内部轮询、租约续期、状态版本递增或诊断检查点变化不生成用户回调。创建事件的事务先锁定 Call，递增 `callback_event_seq`，再按全局顺序锁定相关素材与 Delivery。HTTP 回调只能保证至少一次，接收方必须按事件 ID 去重并忽略旧事件序号；本地只允许一个成功状态，但网络故障下同一事件可能被对方收到多次。任务成功不等待外部回调成功，回调失败也不能改变生成状态。任务查询可返回有长度上限的脱敏提示词和参数摘要；完整可执行正文只从加密载荷按权限读取。规范化请求载荷、交付恢复所需的活动 Source、inline 响应恢复证据和用户素材由各自的活跃 Attempt、ResultDelivery、`manual_review`、`submission_unknown`、`cancel_unknown` 或 `recovery_required` 强引用保护，原过期时间只在最后一个引用释放后生效；托管结果按 Call 快照中的保留期限删除，删除事件可审计。

`fixed` 源策略固定首次可信 Source，后续不同 URL 只记录 HMAC 告警；`refreshable` 只允许同一 TaskIdentity、结果序号和供应商结果身份的更晚可信查询或已认证回调刷新。Reference 刷新事务直接锁定 Delivery 与当前 Source；pending 的 remote-url managed copy 则先从快照取得当前 MediaAsset ID，按全局顺序锁定该素材、Delivery 与当前 Source。事务创建新 URL Blob 和具有全新对象键的 `staging` MediaAsset，以状态版本把旧 Source 改为 `superseded`、失效旧素材上传租约、解除旧 Blob 与 Delivery 的旧素材引用，插入新的活动 Source、替换结果素材强引用并更新当前源指针。旧素材保持可定位的 `staging`，设置不早于原发送授权/上传租约最晚结束时间的清理时点；只有原下载执行已结束或其租约与最大请求时限均届满，扫描器重新锁定并确认无引用后才能转为 `deleting`。任一步失败都保持旧 Source、素材、租约和引用。旧 URL Blob 只在引用解除后删除，旧对象由事务外删除 Worker 按固定版本处理；任何供应商结果身份不一致都拒绝。

结果下载的发送授权事务必须确认日志固定的 Source 仍是当前活动源；刷新与下载并发时，只有固定新 Source 与新 MediaAsset 租约的请求可以进入最终发布，旧下载即使完成也只能由旧素材行定位并清理。Managed copy 一旦 `ready`，同一事务清空当前源指针、把活动 Source 改为 `consumed` 并解除 URL Blob 引用；Reference 在可用期内保留一个活动 Source。刷新失败不得提前销毁仍在合同可用期内的旧 Source；活动 Source 到期或被可信证据证明无效时，Reference 从 `ready` 转为 `expired`，remote-url managed copy 从 `pending` 转为 `delivery_failed`，并在同一事务解除当前源指针、把 Source 改为 `invalid` 和生成状态事件。Reference 失效只生成新的用户回调事件；managed copy 首次进入 `delivery_failed` 时另生成唯一计费核对 Outbox。已有本地内容哈希时，新 Source 下载结果必须完全一致，不能因 URL 刷新改变字节事实。

`channel_request_logs` 的请求 ID、动作、映射语义 HMAC、上游幂等键 HMAC、可选诊断预览引用、发送状态和响应状态在外部请求前同步保存。状态只允许 `prepared -> dispatching/not_sent`、`dispatching -> not_sent/sent/response_recorded/unknown`、`sent -> response_recorded/unknown`，其余均为终态；`dispatching -> not_sent` 仅限同一发送线程在写出任何字节前取得可验证的本地证据，进程失联不能使用该转换。发送协议固定为两个短事务：第一事务创建 `prepared` 日志并取得 request 名额；真正写网络前，第二事务必须同时匹配 Worker 租约、栅栏值、聚合状态版本和 `prepared` 日志，把日志改为 `dispatching` 后提交，随后才允许发出字节。日志写入或第二事务失败时不得发送；进程在 `prepared` 阶段崩溃可以安全标记为 `not_sent`，`dispatching` 之后无论是否真正写出字节都按该动作的 Transport 恢复合同处理，不能一律当作生成提交重试。实际请求字节 HMAC 在序列化或流式发送时同步计算，只有请求体完整生成时才标记 `request_bytes_complete=true`；实际响应字节 HMAC 同理，截断、崩溃或读取失败只能保存“不完整”事实，不能冒充完整摘要。Transport 返回后以新事务记录 `sent/response_recorded/unknown`、字节摘要及完整性标志并释放 request 名额；响应解析失败也必须先保存传输状态。日志只保存有保留期限的非完整脱敏预览，到期删除预览并保留 HMAC 与 HTTP 审计元数据。不同动作和每次重试都必须创建新日志，不能共用或覆盖一行。

凭据解密可在数据库事务外通过本地密钥环或 KMS 完成，但所得明文只是一份未授权的短生命周期候选，不能据此发送。把请求日志从 `prepared` 改为 `dispatching` 的第二事务在动作需要上游认证时，必须按全局顺序共享锁定对应 CryptoKeyring 与 CredentialSecretIdentity，再锁定 Attempt/AsyncExecution、凭据池、Credential 和 Attempt 固定的 PurposeGrant，复核密钥版本可读、数据库当前时间处于 CredentialVersion 有效期、Secret 身份未安全撤销、Attempt 固定凭据版本、租约和状态版本全部有效，并确认 Credential 与 Grant 均为 `active`，或各自为 `draining` 且本 Attempt、回调窗口或恢复流程在其进入 draining 前已固定对应身份，随后才写入唯一发送授权时间并提交；`disabled/revoked` 或已过期版本永不授权新发送。无鉴权的受控结果下载跳过凭据锁，但仍复核其 Attempt、ResultDelivery、当前 Source、租约和 URL HMAC。失败时立即丢弃明文且不得发送。安全撤销事务以同一 SecretIdentity 的独占锁为线性化点，普通 Credential/Grant 停用事务以 Credential 与 Grant 的独占锁为线性化点；对应事务提交后不能产生新的未固定授权，Secret 安全撤销提交后禁止全部新发送。此前已提交的 `dispatching` 请求属于可审计在途请求，撤销不能声称将其从网络中召回。授权必须有极短有效期，且不得越过 CredentialVersion 的 `not_after`；发送线程超时且能证明未写出任何字节时将原日志标记为 `not_sent`，释放其 request 名额并为下一次授权创建新日志，不能复用原日志或持有明文等待。供应商签名回调先完成候选验证，再在创建已认证 Receipt 的事务中按全局顺序共享锁定 SecretIdentity、Credential 与 AsyncExecution 固定的 `upstream_callback_verify` Grant，并使用相同的 active/draining 判断；安全撤销提交后不得新增使用该身份认证成功的 Receipt。纯绑定令牌回调不取得该 Grant，只锁定回调令牌 HMAC Keyring 与其 Alias，Alias 必须通过外键指向唯一 AsyncExecution；令牌安全撤销或 Alias 失效后不得创建新的认证 Receipt。

回调入口中的“AsyncExecution 固定 Grant”只能来自无锁快照候选。供应商签名模式的入口事务按 Keyring、SecretIdentity、Credential、Grant、Receipt/Alias 的顺序锁定并记录实际验签版本；纯绑定令牌模式没有上游验签凭据，入口只按 Keyring、绑定令牌 Alias、Receipt/Alias 的顺序锁定，并以 Alias 的类型化外键取得候选 AsyncExecution ID，不能为了复用通用代码虚构 Credential 或 PurposeGrant。两条入口路径都不锁 AsyncExecution，也不推进业务状态。消费者随后从无锁快照取得候选执行，再按 Guard、Call、Attempt、AsyncExecution、TaskIdentity/Alias、Receipt 的全局顺序锁定，复核 Receipt 的逻辑凭据（签名模式）、绑定模式、令牌 Alias 和版本确实属于该执行；不匹配时进入 `rejected/manual_review`。这样安全撤销以签名入口事务为线性化点，绑定令牌以 Keyring/Alias 事务为线性化点，同时不会形成 Alias -> AsyncExecution 的反向锁序。

控制面 HTTP 交换使用相同的两段发送授权；其中 Attempt/AsyncExecution 位置由 ControlPlaneRun 替代，凭据版本与 PurposeGrant 来自 Run 快照，`catalog_discovery`、`execution` 等用途必须与动作枚举匹配。无池 Credential 跳过池锁与池级计数，但不能跳过 Credential、Grant、request 名额或 RequestLog 状态复核。

CostRate 以请求授权为计费事件时，`dispatching` 事务必须同时追加该组件的 `estimated` UpstreamCostEvent；请求后来被证明未发出时只能追加反向差额，不能删除原事件。以接受、结果、用量或账单为事件的组件在相应可信事实首次出现时追加。这样进程在网络调用后崩溃不会让潜在上游成本消失，事实不明时事件保持待核对。

用户幂等键必须来自唯一请求头，拒绝多个不同值、控制字符、空值和超过 255 字节的输入；服务不裁剪或改变已接受键的字节，HMAC 覆盖其原始字节。网关、反向代理和 SDK 的规范必须保证该请求头不会被逗号合并或静默改写，相关契约测试使用原始 HTTP 样本验证。

SKU 的用户幂等策略只能是 `required/optional/unsupported`，并声明 `resource_replay/response_replay`；同步流式操作没有可持久化的协议等价响应时必须标为 `unsupported`。稳定 `operation_contract_id` 由已认证的 HTTP 路由和方法在读取活动目录前确定；请求模型字符串只进入意图，不参与幂等命名空间解析。非空用户幂等键使用独立密钥计算 HMAC，并通过 `api_call_idempotency_keys` 的全部可读版本别名查找事实。意图使用固定版本的规范化序列化（明确数字、Unicode、字段排序和空值规则），覆盖客户端明确提交的参数、按稳定模型名称规则规范化后的请求模型字符串、素材内容哈希、交付策略和回调目标摘要，不包含服务端后来填充的默认值，也不需要读取当前目录发布版。这样目录发布或名称可见性变化后，重复请求仍能先找到原 Call；同一操作身份下复用相同键但改变模型或其他意图必须返回冲突。

每个幂等写事务先共享锁定 `crypto_keyring_state` 的对应用途与轮换代次，再按全局顺序共享锁定 User 与 Token 并复核二者状态、鉴权版本和归属，对来访键计算全部可读版本 HMAC 并以锁定当前读查询 Alias。轮换事务独占锁定同一 Keyring 行，先把新版本加入可读集合并确认全部目标实例可用，再切换当前版本；不尝试从旧 HMAC 推导新 HMAC。新事实用请求内仍存在的原始键为全部可读版本创建 Alias；旧事实被任一版本命中时，也用当前输入为同一事实补齐缺失版本，二者都在原事务提交。旧 HMAC 密钥必须保留到该版本最后一条可匹配 Alias 失效；不能为了提前删密钥而保存或恢复原始幂等键。同键同意图且仍在重放期时直接返回原资源或保存的协议响应，不读取当前发布版、不新增预授权；已过重放期但未到 `key_reuse_after` 时返回过期冲突。比较意图时使用原事实保存的意图 HMAC 算法与密钥版本。同键任一用户意图字段不同均返回冲突。Alias 唯一键并发冲突时读取胜出的事实并执行相同判断。普通调用首次创建才固定活动发布版、解析模型、填充默认值并计算规范化执行 HMAC；具名资源动作首次创建则在锁定目标资源与其运行需求后固定原发布版和目标作用域。内部重试沿用 Call 但创建新 Attempt。

上游提交幂等键使用固定密钥版本对 Attempt ID、供应商作用域和操作做 HMAC 后编码生成；同一 Attempt 可确定性重建，不同 Attempt 绝不共用，数据库保存密钥版本和校验 HMAC，普通日志不保存原值。Attempt 对该 HMAC KeyVersion 保持强引用，直到提交、恢复、查询和审计重建窗口全部结束；轮换不能提前退役导致同一 Attempt 生成另一个上游键。空用户幂等键只允许 `optional/unsupported` 操作，不写入该表，也不应被当作共享键。

用户提示词、参数和结果投影必须限制长度、字段和字符集，脱敏失败时只保留 HMAC 与大小。对外列表使用有界游标分页，排序键固定为 `(created_at DESC, id DESC)`；管理页所需总数和页码使用独立的覆盖索引计数与 ID 分页查询，再按 ID 读取轻量行，禁止连接载荷表或在大字段上排序。详情按 ID 单独读取载荷；Attempt、请求日志、成本事件、状态事件和回调事件均使用独立的有界分页或最近 N 条上限，禁止按 Call 无界加载历史。

Worker 扫描使用覆盖索引：Call 按 `(status, next_action_at, id)`，AsyncExecution 按 `(state, next_action_at, id)`，Outbox 与交付表按 `(status, next_attempt_at, id)`，凭据名额分别按 `(credential_pool_id, scope, state)` 与 `(credential_id, scope, state)`；终态历史不得混入热路径全表扫描。索引、分页过滤组合和执行计划必须在生产量级副本上验证。

`replay_expires_at` 不得晚于可重放资源或协议响应的保留期；在它到期前，对应资源、响应摘要和授权关系不得删除。`key_reuse_after` 不得早于原 Call 最后可查询时间；此前 Alias 保持可匹配，过了重放期也只能返回过期冲突。只有原资源已不可查询、审计策略允许且清理事务成功写入状态事件后，才能把 Alias 标为不可匹配并允许键产生新 Call。Token 被撤销或用户失去访问权后，即使幂等记录命中也必须拒绝重放。

请求引用 `previous_response_id`、上游会话或其他有状态资源时，必须先按用户 ID、Token ID 与公开资源 ID 授权，再在创建事务中共享锁定对应 `provider_state_refs`，复核其仍为 `active` 且未到期；过期/撤销操作使用独占锁和状态版本，不能越过已开始的连续调用。新 Call 仍使用当前活动发布版与当前售价，但候选路由必须同时满足原状态作用域和适配器连续性版本；作用域可由 Transport 声明为产品 Transport、凭据池、凭据或凭据版本，不能由模型名推断。活动发布版没有兼容 Offering、固定凭据已安全撤销或状态已过期时返回明确的 `provider_state_unavailable`，不得把同一上游状态 ID 发给其他供应商。只有公开操作合同声明可从已保存的规范化历史无损重建时，才可显式转为无状态请求并重新选路；ProviderStateRef 必须强引用重建所需的全部规范化载荷及素材，最后一个相关状态过期前不得清理。创建、到期或撤销 ProviderStateRef，以及 Call/Attempt 进入或离开可能产生状态引用的阶段时，必须在同一事务更新对应分片的运行需求；目标发布版必须覆盖所有活动兼容性指纹，或相关产品已按公开承诺进入可见的弃用/到期状态。激活只验证冻结的分片代次向量与聚合需求证明，不逐行扫描或锁定业务历史；定期对账从业务表重算需求，任何差异都会阻止激活并使相关实例退出就绪。不能无提示切断仍承诺可继续的会话。

### 5.1 提交状态

```text
allocated          -> submitting/cancelled
submitting         -> accepted/running/manual_review/cancel_requested/succeeded/failed/cancelled/not_created/submission_unknown
accepted           -> running/succeeded/failed/cancelled/cancel_requested/manual_review
running            -> succeeded/failed/cancelled/cancel_requested/manual_review
submission_unknown -> accepted/running/cancel_requested/succeeded/failed/cancelled/not_created/manual_review
cancel_requested   -> cancelled/running/succeeded/failed/cancel_unknown
cancel_unknown     -> cancelled/running/succeeded/failed/manual_review
manual_review      -> accepted/running/cancel_requested/succeeded/failed/cancelled/not_created/terminated_unknown
terminated_unknown -> succeeded/failed/cancelled/not_created (仅可信迟到证据或人工核对)
```

Call 只允许 `received -> in_progress/failed/cancelled`、`in_progress -> retry_pending/completed/failed/cancelled/indeterminate`、`retry_pending -> in_progress/failed/cancelled/indeterminate`，以及 `indeterminate -> retry_pending/completed/failed/cancelled` 的受审计核对转换；其中 `indeterminate -> retry_pending` 仅限已经确认上游未创建且仍有重试预算的事实。Attempt 只允许 `started -> recovery_pending/completed/failed/cancelled/not_created/terminated_unknown`、`recovery_pending -> completed/failed/cancelled/not_created/terminated_unknown`，以及 `terminated_unknown -> completed/failed/cancelled/not_created` 的受审计核对转换；任务型上游的 Attempt 保持 `started`，直到 AsyncExecution 按固定映射更新。所有进入 `cancelled` 的转换都必须满足第 5.1 节的确定未发送或上游确认条件，客户端断开、租约过期或普通超时不得单独触发取消。同步调用只有在下游响应/流终止已按合同完成写出，或已取得可核对的失败事实后才可把 Call 置为明确终态；异步调用的 Call 终态不等待外部回调或托管结果交付。`completed/failed/cancelled/not_created` 是不可逆的明确终态；`terminated_unknown` 与 `indeterminate` 会停止普通自动处理，但保留可信迟到证据或人工核对的恢复入口。原未知转换事件永远保留，恢复通过新审计、成本、结算、释放或退款事件表达，不能删除历史。

发出请求前先持久化幂等键和 `submitting`。网络超时且无法证明上游未接受时进入 `submission_unknown`，保留凭据名额和预授权；不得自动重提或直接退款。恢复器只能通过上游幂等重放、客户端请求 ID 查询、回调或人工证据转入明确状态。确认上游未创建后，本 AsyncExecution 和 Attempt 进入终态 `not_created` 并在同一事务释放本次名额；有剩余重试预算时 Call 进入 `retry_pending` 并由新 Attempt 继续，否则 Call 进入 `failed` 并按发布版未接受计费策略释放预授权。超过调用快照中的未知提交最长保留期限仍无证据时，只能进入 `manual_review`，不能由定时任务直接释放名额或预授权。

人工处理只允许四类有审计记录的动作：绑定已找到的上游任务并继续跟踪；依据供应商证据写入明确的成功、失败或取消终态；确认 `not_created` 后按剩余重试预算令 Call 进入 `retry_pending` 或 `failed`；无法取证时进入待核对状态 `terminated_unknown`。后者不自动释放凭据名额，而是转为 `recovery_required` 并隔离该凭据或凭据池；只有明确终止、明确不存在，或供应商保证的最长执行时限已满足后才能释放 task 名额，人工承担风险不能绕过该条件。用户预授权进入 `unknown_hold`，默认保持到成本核对；发布版必须预先声明核对、结算或释放规则，不能在故障时临时选择。潜在上游成本始终保留待核对，不能伪装成普通失败或退款。已知上游任务超过最长执行与自动恢复时限、持续查询仍无法确定终态时也进入 `manual_review`，不能永久保持 `accepted/running`。`manual_review` 超过人工处理时限必须由有权限的操作者明确执行第四类动作，系统不能静默改变结果。`recovery_required` 和 `unknown_hold` 必须产生告警、值班记录和可追踪的处理时限。释放名额或处理 `unknown_hold` 必须记录操作者、理由、证据摘要 HMAC、供应商查询时间和审批结果；涉及资金释放/结算的动作需独立财务权限，不能由执行 Worker 自行完成。

只有“请求确定未发送”“上游明确拒绝且未创建任务”，或“上游执行已明确终止失败、Transport 能证明该失败未产生不可重复的外部状态副作用且 Call 快照允许”才可创建新 Attempt 并重新选路。有状态连续调用无法证明前一 Attempt 未改变上游会话时，禁止已接受后的自动重试。重试策略必须限定最大总 Attempt 数、最大已接受 Attempt 数、可重试错误类别和累计成本上界；参数错误、内容审核失败、用户取消和不明确的网络结果永不自动重试。提交后的轮询超时、5xx、限流或解析错误只重试同一 AsyncExecution 的查询，不得创建新的提交 Attempt。排队任务可本地取消；本地取消在外部请求未开始时释放预授权、凭据名额并以零上游成本完成。AsyncExecution 已为 `submitting` 但当前请求日志仍是 `prepared` 时，取消事务先以状态版本使 Worker 租约失效，再按确定未发送处理；日志已进入 `dispatching` 且尚无上游任务 ID 时，支持取消的 Product Transport 只写入一次 `cancel_intent_at` 并保持原执行状态，不支持取消时拒绝该操作。提交响应、恢复查询或早到回调绑定任务 ID 后，若取消意图仍待处理且上游尚未终止，则同一状态事务直接进入 `cancel_requested` 并创建取消 Outbox，不能先暴露为普通运行任务后遗漏取消。只有收到上游确认时才能进入 `cancelled` 并把取消意图解决为 `confirmed`；取消被拒绝时记录 `rejected` 后回到 `running`，取消请求结果不明确时进入 `cancel_unknown` 并继续查询同一上游任务。任务先进入其他终态时把取消意图解决为 `superseded`。`cancel_unknown` 不允许重发生成请求或释放名额，超过证据保留期限后只能进入 `manual_review`。客户端断开连接只记录在 Call，不自行改变 Call/Attempt/AsyncExecution、计费或凭据名额；同步请求由 Transport 的连接结果进入明确终态或恢复流程。取消与成功竞争时以上游可证明的最终结果为准。

AsyncExecution 的 `succeeded/failed/cancelled/not_created` 是明确终态；`terminated_unknown` 是停止普通自动处理的待核对状态；`manual_review`、`cancel_requested`、`cancel_unknown` 和 `submission_unknown` 仍在活跃恢复流程。进入明确终态或待核对状态时，在同一事务按 `succeeded -> completed`、`failed -> failed`、`cancelled -> cancelled`、`not_created -> not_created`、`terminated_unknown -> terminated_unknown` 更新对应 Attempt；两行都使用期望状态版本，任一更新失败则整个事务回滚。Call 在所有 Attempt 均明确终止或待核对且没有待处理重试时进入相应结果；结果交付和用户回调不阻塞 Call。普通成功/失败/取消可完成用户结算；`terminated_unknown` 对应的 Call 为 `indeterminate`，领域资源按其公开合同映射为未完成状态并携带稳定原因码，计费状态为 `unknown_hold`，而不是“计费已完成”或普通失败。可信迟到证据解决未知状态时，在一个核对事务内推进 AsyncExecution、Attempt、Call 与领域资源，处理 `recovery_required/unknown_hold` 并创建必要交付和回调事件；不得自动创建新的生成 Attempt。`terminated_unknown` 本身不代表资源已释放：凭据名额为 `recovery_required`，资金预授权为 `unknown_hold`，两者须有独立的恢复或人工决策事件。目录内不存在合格路由时，选路事务必须把 Call 与领域资源标记为失败、释放预授权并返回明确的 `no_route`，不得留下没有 Attempt 的收费记录。存在合格路由但凭据、健康或名额暂不可用时，按 Call 快照中的 `fail_fast/queue` 策略处理：前者释放预授权并返回 `capacity_exhausted`，后者保持 Call 为 `received`、领域资源为排队中并以 Outbox 延时重试；超过固定最大等待时间后以 `capacity_timeout` 失败并释放预授权。排队期间不创建 Attempt 或凭据名额，预授权继续占用且允许本地取消。

`indeterminate` 必须是 Call 的正式内部状态；公开协议没有同名状态时，领域资源只能映射为其最接近的“未完成”状态并附带稳定原因码，不能保存协议外状态或伪装成普通失败。内部 `not_created` 在没有后续 Attempt 时可令 Call 映射为普通失败，前提是保留“未创建证明”和释放事件；`manual_review`、`submission_unknown`、`retry_pending` 必须在管理界面通过关联状态明确展示，不能伪装成普通处理中。

具名资源动作必须创建独立 APICall，并以类型化 `target_async_execution_id` 和 `target_resource_id` 引用原任务；操作合同、动作 SKU 与售价来自原任务固定发布版，候选只能是原 AsyncExecution 的 ProductTransport/Offering 及其状态作用域允许的原凭据链，不能重新选到其他供应商。公开资源在动作合同声明的可执行期限内持续维护原发布版、适配器与状态兼容性运行需求；期限届满时以锁定 Guard 的状态事务关闭动作能力，不能先驱逐实现再接受请求。动作合同必须声明 `once/idempotency_keyed` 基数：单次动作由数据库对 `(target_async_execution_id, operation_contract_id)` 建立唯一事实；可重复动作必须要求用户幂等键、稳定动作实例参数和上游幂等/查询保证，禁止仅靠自增序号重发。动作 Call 使用普通预授权、Attempt、请求日志、两段发送、幂等与未知结果恢复，重复请求返回原动作 Call；创建前还必须复核固定凭据的 EntitlementState 与动作 Offering 的 CommercialState 均为未过期 `valid`。动作成功后才以状态版本更新领域投影，原生成 Call 的 SKU、售价和结算保持不变，增值费用只由动作 Call 结算。无法在发送前给出用户费用上界、上游不支持幂等且动作结果无法查询，或固定凭据已安全撤销时，该动作不得执行；网络结果不明进入动作 Call 的 `indeterminate`，不能自动重发或先按成功收费。取消不复用该通用动作流程，继续遵守取消意图、取消 Outbox 和最终状态核对规则。

### 5.2 事务边界

账务过账命令中的“先锁 LedgerAccount”仅指在其业务事务已经按全局锁序锁定 BillingAccount、预算窗口、Call、Reservation 等祖先行之后，再锁定全部 LedgerAccount；独立资金入账也必须先锁定其 BillingAccount，再取得 LedgerAccount。不得让过账接口绕过上游业务锁序形成 `LedgerAccount -> Call/Reservation` 的反向顺序。

1. 创建：按全局锁序在一个事务内创建 Call、唯一规范化执行载荷、素材强引用、幂等记录和用户预授权；普通调用共享锁定当前目录指针，具名资源动作改为锁定目标资源对应的 Requirement Guard、原 Call/AsyncExecution 和动作期限，并固定原发布版。引用 ProviderStateRef 时必须先锁定并复核所有权、状态和作用域，仅领域 API 需要时创建领域资源，仅需要后台处理时创建调度 Outbox。任何一项失败都不产生 Call 或费用，事务外遗留的暂存素材由状态扫描器删除。
2. 选路：先从 Call 与候选的无锁快照取得固定 ID 并预生成 Attempt ID；需要新增运行需求时先锁定该 ID 对应的 Requirement Guard，再按全局顺序锁定候选全部 CostRate 的 RateEvidenceReviewState、当前 CredentialVersion 的 EntitlementState 与 Offering CommercialState；健康为 `closed` 时读取主库最新已提交状态和版本但不锁行，为到期 `open` 时必须在锁定 Call 前按稳定 ID 取得唯一 `half_open` 探测许可，否则该候选不可用。随后按租约和状态版本锁定 Call，确认快照仍匹配且 `current_attempt_id` 为空或只指向已终止 Attempt，再锁定一个候选凭据池、Credential 与其 execution Grant。任一证据、状态或配置版本变化都放弃该候选，不能在已锁 Call 后反向补锁。同一事务创建 Attempt、递增 Attempt 序号、写入 `current_attempt_id` 并固定唯一成本方案及全部目录引用。任务型上游 Attempt 创建 AsyncExecution，仅其固定 Product Transport 声明 task 范围时创建 task 名额。每次真实 HTTP 交换先从不可变 Attempt 快照准备所需 ID；需要鉴权时按全局顺序锁定 CryptoKeyring、SecretIdentity，再锁定 Attempt/AsyncExecution，随后锁定凭据池、Credential 与所需 PurposeGrant；无鉴权的受控下载从 Attempt/AsyncExecution 开始。发送事务复核租约、状态版本、用途授权和凭据版本，在发送前取得 request 名额并创建或推进请求日志；任一适用的池级或凭据级范围达到上限都不得发送。
3. 异步接受：提交响应确认后，先在事务外按固定规则规范化可信 Transport 作用域和任务 ID；事务内按 Keyring 稳定 ID、Call、Attempt、AsyncExecution、本次提交 RequestLog、TaskIdentity/Alias 的固定顺序取得所需锁，对全部可读 HMAC 版本计算 Alias 并以锁定当前读查找或创建唯一身份。新身份只保存一份加密任务 ID，已有身份必须逐字节验证解密值一致。随后以 TaskIdentity 上唯一的 `async_execution_id` 绑定执行，让已锁定的 RequestLog 引用该身份并更新 Attempt/AsyncExecution 状态；存在有效取消意图且任务未终止时直接进入 `cancel_requested` 并创建取消 Outbox。若早到回调已通过绑定令牌把同一 TaskIdentity/AsyncExecution 推进到更晚状态或终态，接受事务只补全请求日志并保留较新状态；身份已绑定其他执行、Alias 版本指向不同身份或同一执行出现不同任务 ID 时进入人工处理，绝不能覆盖。首次绑定还必须为该身份下仍为 `received` 的 Receipt 安排新处理序号。只有请求日志仍为 `dispatching` 且没有可绑定 TaskIdentity、回调或响应证据时，崩溃恢复才进入 `submission_unknown`；不能从可清理正文临时解析任务 ID。
4. 终态：需要增减运行需求时先锁定对应 Guard，再按全局锁序锁定账务行、Call、Attempt、可选 AsyncExecution、领域资源和凭据名额，幂等写入上游成本并更新状态；对已确认结束的执行释放凭据名额。只有同一事务同时把 Call 推进为明确终态时，才按 Call 快照计算用户费用；全部售价组件的计费事件已经确定时立即结算或释放一次，仍有明确声明的交付终态组件尚未确定时保留原预授权并创建唯一 Billing Outbox，由首次交付事实触发后续幂等结算。进入 `retry_pending` 时同样保留原预授权，单个 Attempt 结束绝不能提前结算 Call。成功结果写入交付记录；Call 存在 CallbackTarget 时，仅对外领域状态变化才在同一事务按 `callback_event_seq` 创建唯一 CallbackDelivery 与发送 Outbox，载荷携带稳定 Call/公开资源 ID、该次领域与执行状态及已有 Delivery ID 列表，不要求尚未创建的 Delivery。`terminated_unknown` 只更新为 `recovery_required/unknown_hold`，不能在此步骤释放。
5. 外部转存和用户回调不放入数据库事务；它们使用独立可重试交付状态。交付 Worker 只能通过 ChannelRequestLog、request 名额、MediaAsset 和 ResultDelivery 的事务接口记录下载与对象发布；每次成本计费动作发生后按固定 CostRate 追加幂等 UpstreamCostEvent，进入交付终态时同事务写唯一 `reconcile_delivery` Outbox。该事件消费者按 Call 快照处理用户 Billing；Call 存在 CallbackTarget 时，再为本次交付状态变化按新的 `callback_event_seq` 创建唯一 CallbackDelivery 与发送 Outbox。任务状态回调已经由对应状态事务单独创建，不能复用或重复。回调 Worker 每次发送前创建 CallbackDeliveryAttempt，返回后更新该 Attempt 与 `callback_deliveries`。网络 Worker 都不能直接修改生成状态，回调 Worker 也不能修改计费事实。

数据库是任务意图的事实源，Asynq 只负责调度。Outbox 负责“某动作需要被投递”，Worker 租约负责“当前谁能执行状态迁移”，凭据名额负责“哪个上游凭据仍被一次执行占用”，三者不能互相替代。所有时间戳使用 UTC，租约和回调时间窗以数据库时间或有界时钟偏差计算。`next_action_at`、单调状态版本和带栅栏值的可续租 Worker 租约用于拒绝过期消息；自动化执行的续租、状态更新和释放都必须同时匹配租约所有者、栅栏值和状态版本。人工核对使用独立权限、行锁、状态版本和审计事件，不复用 Worker 身份。扫描器按数据库状态重建遗漏的 Outbox，任务消息中的轮询序号不能覆盖数据库新状态。Redis 故障不能改变计费、并发或任务事实。

凭据名额分为 `active/recovery_required/released`。`request` 名额在 HTTP Transport 返回或连接被确认关闭后释放；Worker 崩溃时只有超过该 Transport 的最大请求时长与取消宽限期，且所属租约已失效，才能回收 request 名额。响应未知只影响成本和用户计费状态，不把 request 名额当成长任务名额。`task` 名额到期只会进入恢复检查，不会自动释放；执行选路先锁定凭据池再锁定凭据，并分别统计对应范围的所有未释放名额，同时满足池级和凭据级上限。目录发现等无池控制面请求锁定 Credential 后只统计其 request 名额，但仍使用同一发送协议。超时任务必须先查询或取消上游；管理员释放也必须满足明确终止、明确不存在或供应商最长执行时限已满足的条件，并写入审计事件。

同步请求在 `dispatching` 后失去响应时，若产品 Transport 明确保证同幂等键重放可返回同一资源或响应，Attempt 进入 `recovery_pending`，恢复请求沿用原 Attempt 与上游幂等键但创建新的请求日志；恢复成功后进入明确终态，超过固定恢复时限仍无证据才进入 `terminated_unknown`。Transport 不具备该保证时直接进入 `terminated_unknown`。后者把上游成本标记待核对，并把用户预授权转为 `unknown_hold`；只有后续证据或独立财务审批才能结算或释放。

流式响应在首字节发送前必须持久化 Call、Attempt、Reservation、请求日志和 `stream_started` 栅栏；一旦向客户端发送任何内容，禁止换路或重放整个调用。可按固定间隔保存累计帧数、字节数、可验证 Token 数与滚动 HMAC 作为诊断检查点，但网络发送与数据库写入无法构成原子事务，这些检查点不能冒充精确送达证明。连接正常结束并取得合同声明的最终用量时按该事实结算；进程崩溃、连接结果不明或最终用量缺失时，只有供应商请求 ID 查询、账单或其他可信证据可以恢复，否则 Attempt/Call 进入 `terminated_unknown/indeterminate`，预授权进入 `unknown_hold`。流式 SKU 的售价必须按可恢复的生成/用量事实计费；没有客户端分片确认协议时不得承诺按“客户端实际收到的精确分片数”计费。

上游回调必须按 Transport 固定的认证策略验证：优先使用供应商签名及时间窗；供应商不提供签名但允许自定义回调 URL 时，使用至少 128 位熵的每执行专属绑定令牌。签名策略必须引用 `upstream_callback_verify` 逻辑凭据，AsyncExecution 固定逻辑凭据、策略版本和声明的密钥绑定模式；`delivery_time` 模式按接收时刻选择允许版本，`submission_bound` 模式固定提交时版本集合。Receipt 保存实际验签凭据版本、算法、接收时间和结果，绑定时必须与 AsyncExecution 的逻辑凭据、策略及允许版本一致。普通轮换为旧、新版本设置有界重叠时间窗，使在途任务仍可验证供应商使用允许版本签发的回调；安全撤销在 Secret 身份级立即排除所有关联版本，无法验证的输入进入隔离安全指标，不能降级为未认证接收。两类认证都不具备的 Transport 不得启用回调，只能轮询。创建 AsyncExecution 时生成回调绑定令牌，以用途隔离的 EncryptedBlob 保存明文并为全部可读 HMAC 版本建立 Alias；只有执行提交、恢复和回调认证路径可解密，回调窗口结束后按引用清理。应用、反向代理和链路追踪必须在记录 URL 前移除该令牌。回调入口在读取完整正文前限制方法、内容类型和字节数，并按渠道限速。回调事件身份不保存明文：有供应商事件 ID 时，以可信供应商作用域和该 ID 生成全部可读版本 Alias；没有事件 ID 时，以供应商作用域、已认证绑定令牌对应的 AsyncExecution 或上游任务 ID、事件类型和去除签名/时间戳认证包络后的规范化业务载荷 HMAC 组成规范身份后生成 Alias。入口对当前输入计算全部可读版本并查重，Receipt 与 Alias 在同一短事务创建；命中旧 Alias 时补齐同一 Receipt 的缺失版本，旧密钥保留到该版本最后一条可匹配回调 Alias 失效，因此轮换前后的重复回调只能命中原 Receipt。HTTP 入口只写入认证结果、Receipt、Alias 和处理 Outbox，不直接修改 AsyncExecution、计费或交付状态。回调可能早于提交响应到达，消费者取得 AsyncExecution 栅栏租约后，优先按绑定令牌绑定；不支持该令牌时才按 Transport 声明的作用域键与任务 ID 的全部可读 HMAC 别名、提交幂等键 HMAC 或请求 ID 匹配，作用域键只能由可信目录和已知接收端身份计算。候选不唯一或违反任务 ID 唯一键时进入人工处理，绑定前不得改变任务状态。回调中的结果 URL、内容类型、大小和时长必须通过与结果交付相同的安全解析器，未通过时保存上游终态证据但将交付标为 `delivery_failed`，不得发布不可用结果。回调消费者和轮询 Worker 都以租约栅栏及 AsyncExecution 状态版本做条件更新，只有赢得明确终态转换的一方能创建首次结算事件；迟到或重复事件不得倒退明确终态或再次结算。`terminated_unknown` 收到可信迟到终态时走独立核对事务，并以唯一核对事件防止重复处理。执行状态转换和交付状态转换使用不同版本号，任何交付重试都不能再次触发上游查询或用户结算。轮询返回短暂 `not_found` 时必须遵守 Transport 声明的最终一致性等待窗口；窗口内只能保持未知，只有供应商明确的未创建证明或窗口后审计规则才能进入 `not_created`。

回调验签密钥的绑定模式必须由固定 ProductTransport 明确声明。`delivery_time` 表示供应商按投递时有效密钥签名，入口只尝试逻辑凭据在该时刻允许的有限版本；`submission_bound` 表示供应商在提交时固定密钥，AsyncExecution 必须保存当时允许的确切凭据版本集合，并使其包裹密钥和普通轮换版本保留到回调窗口结束。两种模式不得自动互换或同时尝试未声明版本；安全撤销仍立即排除相关 Secret 身份，并将无法认证的回调隔离处理。

### 5.3 父子状态一致性

补充约束：存在 Attempt 时，`Call` 进入 `completed/failed/cancelled/indeterminate` 前必须存在同一 Call 的 `final_attempt_id`；其中 `indeterminate` 只能指向 `terminated_unknown` Attempt。Call 进入 `retry_pending` 时必须清空 `current_attempt_id` 和 `final_attempt_id`，此前已终止 Attempt 仅由历史事件保留；只有最后一次 Attempt 确定结束且 Call 不再重试时，才能写入 `final_attempt_id`。任何 Attempt 进入终态或待核对状态都必须清空 Call 的 `current_attempt_id`。没有创建 Attempt 的 Call 仅允许在 `received` 阶段因 `no_route`、`capacity_exhausted`、`capacity_timeout` 或排队取消而进入 `failed/cancelled`，此时 `final_attempt_id` 必须为空，并由 Call 状态事件保存有界原因码、预授权释放事实和候选快照摘要。`billing_system_state` 在首个账户、售价或流水产生后固定平台结算币种及定义版本，后续币种定义只能作为未被该部署选用的新版本保存，不得切换或回写既有账务语义。

`APICall.current_attempt_id` 只能为空或指向同一 Call 的 `started/recovery_pending` Attempt；`final_attempt_id` 只能为空或指向同一 Call 的明确终态/待核对 Attempt。存在 Attempt 时，Call 进入 `completed/failed/cancelled/indeterminate` 前必须存在匹配的 `final_attempt_id`，其中 `indeterminate` 只能指向 `terminated_unknown` Attempt；Call 处于 `retry_pending` 时两个 Attempt 指针都必须为空。没有创建 Attempt 的早期 `no_route/capacity_exhausted/capacity_timeout` 或排队取消例外由上一条补充约束处理。进入 `received` 且尚未选路时不得填写 Attempt 指针。数据库触发器和状态过程必须同时校验 Call、Attempt 的 `catalog_release_id/sku_id`、所有权、状态版本和指针关系，不能只依赖应用层。

`AsyncExecution` 只能由任务型 Attempt 创建，并与其 Attempt 一对一；AsyncExecution 处于 `allocated/submitting/submission_unknown/accepted/running/manual_review/cancel_requested/cancel_unknown` 时，父 Attempt 必须保持 `started`；AsyncExecution 进入 `succeeded/failed/cancelled/not_created/terminated_unknown` 时，父 Attempt 必须在同一事务映射到对应终态。禁止存在已终止 Attempt 的活跃 AsyncExecution，也禁止同步 Attempt 创建 AsyncExecution。领域资源状态只能引用这些已提交事实，不能反向推进父状态。

CallbackDelivery 只由公开领域状态变化、ResultDelivery 首次进入 `ready/delivery_failed`、公开合同允许的 `ready -> expired`，或其他公开合同声明的事件触发；内部轮询、租约、状态版本、成本核对和诊断检查点不得触发用户回调。每个 SKU 必须声明单次 Call 可产生的最大公开事件数、结果数和载荷字节上限，超过上限时在创建回调前失败并记录稳定原因码，不能让 `callback_event_seq` 或回调载荷无界增长。

幂等策略必须在请求入口执行：`required` 拒绝缺失、空值或多值 `Idempotency-Key`；`optional` 允许至多一个合法键，缺失时按无键调用处理；`unsupported` 拒绝任何非空键。所有策略都拒绝控制字符、超长值和代理合并后的不确定多值，错误响应不得创建 Call、Reservation 或上游请求。

## 6. 计费规则

1. 创建任务时固定售价发布版并预授权可证明的最大金额；无法给出上界的预付费 SKU 不得发布。
2. 成功后按费率指定的数量来源结算；实际量超过合同上限时仍记录完整应收，并产生异常事件，不能静默丢弃金额。
3. 失败或取消是否收费由售价费率的计费事件决定，不由 HTTP 状态临时推断。
4. 每个重试 Attempt 分别按自身已发生的上游计费事件记录成本；用户仍只按 Call 结算一次。
5. 价格变更只通过新目录发布版生效，不修改历史任务快照。
6. 成本和售价可以使用不同币种，但利润报表必须使用有版本的汇率快照。
7. 上游账单修正通过带来源唯一键的追加差额事件完成，不修改已记录的成本事件；核对后的净成本由同组件事件链计算，不能把估算、报告和修正当成三份成本相加。
8. 最终用量超过预授权上界属于计费异常：完整记录应收与差额、允许资金账户形成明确欠款并冻结新调用，禁止截断金额或伪造充值；正常发布校验必须证明该情况在合同范围内不可发生。
9. 售价组件的计费事件只能来自 Call/Attempt 的明确事实或 ResultDelivery 的首次终态事实。依赖交付终态的 SKU 必须固定最长交付与预授权持有期限，并声明多结果按全部、任一或逐结果判定，以及部分交付和 `ready/delivery_failed` 的收费结果；超过期限必须形成 `delivery_failed` 事实并完成结算或释放，不能无限保留 `active` 预授权。

账务事件到复式分录的映射必须由不可变、带版本的 `billing_posting_rules` 正式表声明：每个可过账事件类型固定金额来源、借方/贷方系统科目、允许的正负方向、预算窗口影响和可用币种；`BillingEvent` 必须引用创建时的规则版本，过账过程拒绝缺失、已停用或与事件类型不匹配的规则。预授权、释放等只改变持有额的事件明确声明“不产生 LedgerTransaction”；退款、扣款修正和充值必须引用原事件并使用同一规则链的追加差额分录。规则版本发布后不可修改，修订只能创建新版本，历史流水始终按原规则重算。

预算检查必须区分有限窗口和 `unlimited` 窗口：有限窗口按 `limit - used - held` 判断可用额；`unlimited` 的 `limit=NULL` 仅表示不执行上限比较，仍必须记录 `used_amount`、`held_amount`，并拒绝负值。实现不得直接对 SQL `NULL` 执行普通减法后把未知结果当作可用。

## 7. 凭据与安全

凭据使用 AES-256-GCM 信封加密：每个 EncryptedBlob 使用独立 DEK 和数据 nonce，加密主密钥 KEK 来自环境密钥环或 KMS；Blob 保存密文、数据 nonce、用途、唯一业务所有者绑定与 AAD 版本，`encrypted_blob_key_wraps` 按同用途 KEK 版本追加被包裹 DEK、包裹 nonce 和算法。数据 AAD 绑定 Blob 主键、用途、schema 版本及不可变业务所有者，包裹 AAD 另绑定 Blob、KEK 版本和算法，防止数据库内跨对象替换；任一 nonce 在对应密钥下不得重复。上游 Secret 变化时创建新凭据版本和新 Blob；KEK 轮换先把新版本置为 `preparing`，为全部仍被引用的 Blob 追加新包裹并逐项回读验证，再经全部目标实例 Readiness 证明后切为 `current`，不能修改凭据业务版本或 Blob 密文。对象存储使用独立 Keyring 身份；其密钥轮换同样追加新版本并保留旧版本，直到所有仍在线或可恢复对象完成重新包裹且对象删除/读取保留期结束，不能因新版本切换而使存量对象无法解密。旧版本在所有引用均有新包裹且观察期通过后才可 `retired`；旧包裹和凭据版本仍按执行与审计期限保留。普通停用只禁止新任务，现有任务仍可使用固定版本；安全撤销会立即禁止对应密钥操作，并把无法解密的受影响执行标记为需处理。

普通 CredentialVersion 轮换只在供应商保证旧、新认证材料具有覆盖全部既有执行、回调与恢复窗口的重叠有效期时原位进行：新版本先创建、回读和完成 EntitlementState 验证，再在 Credential 行锁下切换当前版本；新 Attempt 使用新版本，既有 Attempt 继续使用其固定旧版本。供应商不能提供重叠期时，必须创建使用新 SecretIdentity 的新 Credential，验证后启用并把旧 Credential 置为 `draining`；不得让在途任务静默改用另一版本。旧材料被供应商立即作废或发生泄露时按安全撤销处理，受影响执行进入明确人工恢复状态，不能伪装成普通轮换成功。

凭据指纹使用独立密钥的 HMAC，不保存可离线比对的裸哈希。认证材料的规范化由固定鉴权方案定义，只规范字段结构和明确允许的格式，不得擅自裁剪或改变 Secret 字节。创建或轮换凭据时先共享锁定指纹用途的 `crypto_keyring_state`，对规范化后的完整认证材料计算全部可读版本指纹；已有任一指纹解析到同一 Secret 身份时复用该身份并补齐缺失版本 Alias，全部未知时才创建新身份，并为输入材料的全部可读版本写入 Alias。数据库对 `(channel_id, hmac_key_version, fingerprint)` 唯一，并对 `(secret_identity_id, hmac_key_version)` 唯一，因此同一渠道中的相同认证材料只能归属一个 Credential；新增用途必须创建 PurposeGrant，不能复制身份或 Secret。一个 Secret 身份同时只能属于一个逻辑凭据；普通创建发现它已被其他凭据声明时必须拒绝。执行 Key 换组必须先停用旧凭据并保持目标凭据未启用，再等待全部名额释放。转移前在事务外通过受审计的密钥服务解密旧 Blob，并为新 CredentialVersion 生成绑定新所有者 AAD 的全新 EncryptedBlob/DEK 包裹候选；转移事务按全局顺序锁定 Keyring、Secret 身份、涉及的凭据池、旧凭据和新凭据，重新验证候选明文指纹、包裹版本和没有未释放名额后，创建新版本、移动 SecretIdentity 当前声明、创建所需 PurposeGrant 并写审计事件，不能改绑旧 CredentialVersion 或复用其 Blob。CatalogSource 等引用必须在同一受审计变更中改绑或明确停用，不能留下悬空授权。旧凭据版本仍引用同一 Secret 身份和原所有者 Blob 供历史执行核查，不能重新启用。安全撤销锁定 Secret 身份并追加唯一撤销事件，所有解密入口在打开 Blob 前都必须检查该事件；不能只撤销当前逻辑凭据而漏掉转移前版本。HMAC 轮换不能从旧摘要补算新摘要：新版本先加入可读集合，所有新输入同时写全部可读版本 Alias；旧版本保持可读，直到该版本最后一条需唯一匹配或审计验证的记录到期。对于已按业务需要保留加密原文的凭据、任务 ID、Provider State ID 和回调绑定令牌，受审计的轮换作业可以按所有者与用途解密原文并计算新 Alias；用户幂等键及已清除原载荷的回调事件 ID 不得为轮换而额外保留原文，只能等待对应 Alias 失效后再退役旧 HMAC 版本。所有持久化 HMAC 必须同时保存用途、算法、密钥版本和规范化版本；任何参与唯一身份或去重的 HMAC 写入都必须共享锁定轮换代次，轮换以独占锁隔离旧写事务；切换当前版本前必须证明全部目标实例可读取新版本密钥。安全事件要求立即移除旧 HMAC 密钥时，依赖该版本的匹配能力必须显式降级为隔离/人工处理，不能保存原始 Secret、幂等键或外部 ID 来绕过。非密钥内容摘要也必须保存算法和规范化版本，不能把两类摘要混用。

执行 Key 换组时，“全部名额已释放”只是必要条件。转移事务还必须确认旧凭据及其 draining PurposeGrant 不存在非终态 Attempt、未知核对、未过期 ProviderStateRef、未结束回调窗口、仍可执行具名动作的公开资源或需要原商业作用域的交付/恢复；任一引用存在都只能继续停用并等待，不能转移 SecretIdentity。所有连续性引用结束后，转移仍需重新验证供应商侧分组、目标池权益和商业状态，再以 Requirement Guard、CryptoKeyring、SecretIdentity、池、Credential 与 Grant 的固定锁序提交。普通历史审计引用可继续使用旧 CredentialVersion，但不能再次执行上游请求。

`api_call_payloads`、上游任务/状态 ID、完整签名 URL、回调载荷及其他敏感业务值都引用 EncryptedBlob，并使用与上游凭据不同的 KEK 用途。EncryptedBlob 不提供按 ID 直接读取的公开接口，解密前必须从业务引用反查所有权和用途；数据库外键禁止在仍有业务引用时删除 Blob。对象存储使用独立的对象加密密钥体系。业务包只持有短生命周期明文并在使用后释放引用，不自行实现加密、缓存明文或把解密结果写入错误与诊断日志。

用户 Token 采用“公开选择子 + 高熵秘密”格式：选择子唯一定位 Token 行，服务端只保存带算法版本和上下文域分离的秘密摘要，并使用常量时间比较；停机导入器可在导入期间按完整旧 Key 计算一次性映射，但目标运行时只使用新选择子和秘密摘要，目标结构不保留旧 Key 摘要。Token 轮换创建新 Token 并撤销旧 Token，不覆盖摘要；仍被 Call、预算或审计引用的 Token 只能撤销，不能硬删除。公开 API 的每次鉴权都必须从主库联合读取 Token 与 User 的当前状态、失效时间、`auth_version` 和归属，Redis 只做按已验证 Token ID 的限流，不能缓存五分钟授权结果继续放行已撤销 Token 或已停用用户。管理端撤销以 Token 行独占锁递增 `auth_version`；已经通过中间件但尚未创建 Call、AIFile 或其他持久资源的请求仍须在创建事务内再次共享锁定 User/Token 并复核版本。

Transport 配置不得包含 Secret。执行、目录发现和上游回调验签所需的多字段认证信息均整体存入加密凭据版本；每个使用位置必须具备对应 PurposeGrant，不能因底层 Secret 相同而把执行 Key 当作回调验签 Secret 隐式复用。管理接口只返回指纹提示。目录发布、用途授权、凭据创建/轮换/转移/撤销、价格审核全部写审计事件。

凭据迁入密文后必须做解密回读和最小权限探测，再允许新路由使用。切换完成后立即清除旧表明文列；曾进入未加密备份、导出或日志的上游 Key 必须在供应商侧轮换或撤销，并更新加密版本。备份必须加密、限制访问并遵守删除期限，不能作为继续保留旧明文的理由。

`api_call_payloads` 只保存唯一的规范化执行请求和必要的规范化响应，按用途引用 EncryptedBlob 并按权限读取；完整上游 HTTP 请求和响应不另存副本，`channel_request_logs` 只保留 HMAC 及有界、脱敏、不可执行的诊断预览。执行正文和诊断预览使用不同保留类别：关闭可观测性只停止诊断预览，不能阻止创建或删除活跃调用、恢复流程和 ProviderStateRef 仍需的规范化执行请求。列表只查询 Call、Attempt 和 HTTP 元数据。

敏感 Blob 清理必须先按全局锁序锁定业务所有者及全部强引用，复核执行、恢复、幂等重放、回调重放、状态连续性和审计调试期限均已结束，再以状态版本清除业务表的 Blob 外键并写入 `purged_at`/审计事件；提交后才删除已无引用的 EncryptedBlob 与其 KeyWrap。CredentialVersion、Payload、TaskIdentity、ProviderStateRef、CallbackTarget、CallbackDelivery，以及 AsyncExecution 上的回调绑定令牌摘要和 Alias 等非敏感元数据继续保留，读取已清理正文返回明确的 `payload_expired`，不能回退到旧表、日志或备份恢复原文。重复清理必须幂等，任何仍存在的外键都会阻止 Blob 删除。

图像、视频、音频和允许文档等用户对象在 Call 创建前统一进入受控对象存储：入口在读取完整正文前按操作合同限制 Content-Length 与流式读取字节数，内联 Base64 同时限制编码长度和解码后长度并只流式解码一次，远端 URL 通过受控下载器流式读取且只保留原始字节；所有入口均计入用户对象配额，按收到的原始字节校验类型、大小、图像像素/帧数、媒体时长或文档允许类型等资源边界及 SHA-256 后保存，不转码。规范化请求只引用不可变资源 ID。上传必须先在短事务中创建有短期限的 `staging` 素材，固定全局唯一且永不复用的服务端对象键、状态版本和上传租约；提交后才在事务外以条件创建写私有对象。对象已存在时只能校验并复用完全一致的首个完整版本，差异进入完整性告警，禁止覆盖。写入与内容校验完成后，用匹配租约和状态版本的事务保存不可变哈希、长度、类型、对象版本/ETag 及 `object_ready_at`，仍保持 `staging`；数据库提交失败时恢复器可由该行定位对象并校验后重试登记。AIFile 或 Call 创建事务随后锁定素材行，复核所有者、创建 Token、哈希、期限和 `object_ready_at`，写入 `media_asset_refs` 并转为 `active`。失败、租约失效或失去引用的暂存对象由上述删除状态机清理；扫描器不得删除上传租约仍有效或对象就绪后正被资源创建事务锁定的行，不能依赖进程内操作。适配器优先按 Transport 合同生成短期签名 URL 或流式上传原始对象；仅当上游合同只接受内联 Base64 时，才从对象存储读取原始字节并直接流式编码进一次 HTTP 请求体，同时计算请求字节 HMAC，不在内存中构造完整 Base64 字符串。数据库、队列、快照和日志始终不保存该 Base64。素材对象必须加密、私有且受强引用和保留期限控制。数据库只存资源 ID、哈希和受控对象位置；原始外部 URL 仅在审计确有必要时加密限期保存。规范化器遇到 `data:` URI、Base64 字段或二进制内联内容时，日志与任务投影只保留类型、长度、哈希和资源引用；任何脱敏失败都放弃预览保存，不能写入原文。上游只返回二进制或 Base64 结果时也必须在相同边界内流式解码为原始字节对象；这种 Offering 不能用于要求直接上游 URL 的 `reference` 交付模式。

用户素材 URL、上游结果 URL 和回调 URL 都视为不可信输入：仅允许声明的协议与端口，解析和每次重定向都阻止本机、内网、链路本地及云元数据地址，并限制响应大小、类型、时长和跳转次数；连接时再次校验解析地址以防 DNS 重绑定。上游结果还必须受 Transport 主机允许列表约束；鉴权头不得跨主机重定向。完整签名 URL 和含查询密钥的回调 URL 只存入有访问控制的加密载荷，主表仅保存主机、路径摘要、到期时间和引用。回调使用创建时快照的 URL 与签名策略，之后用户修改配置不能改变在途任务。URL 写入日志和管理列表前必须移除查询凭据、签名参数和认证信息。

输入素材的外部 URL 仅可在素材接收审计确有必要时以加密、限期形式保存；上游结果 URL 只能保存于 `ResultDeliverySource` 的加密载荷中，任何其他业务表、日志或投影均不得保存结果 URL。公开任务查询、结果读取和取消必须同时按资源 ID、Token ID 与用户 ID 限定，不能只凭可猜测任务 ID 访问。管理端读取加密正文、重放回调、释放名额、处理 `unknown_hold`、发布价格和轮换凭据分别使用最小权限角色并记录审计事件。

## 8. 一次性数据导入与切换

本节只描述一次性数据导入，不描述旧运行路径兼容。新库使用全新的目标基线；旧库在停机窗口内作为只读输入，禁止旧服务、旧 Worker、旧 API 投影和双写继续运行。导入失败时恢复备份并修复导入程序，不恢复旧代码继续接收新写入。

1. 目标基线：只在 `database/migrations` 新增目标结构迁移，禁止 GORM AutoMigrate。迁移工具先创建 `migration_runs`、`migration_object_map`、追加式 `migration_source_revisions` 与 `migration_issues`；这些表只服务离线导入和审计，不参与线上请求路由。Run 状态为 `scheduled/running/succeeded/failed`，失败重跑沿用原 Run 和对象映射；Issue 状态为 `open/resolved/waived`，`waived` 必须由独立审核权限追加解决事件。对象映射按 `(source_table, source_pk, target_type, target_discriminator)` 唯一保存稳定目标身份，源修订按 `(object_map_id, source_hmac)` 唯一保存；重复导入复用目标身份，源事实冲突即停止并报告。
2. 目录与凭据回填：先为每个公开 HTTP 路由与方法建立稳定操作身份。旧 `channels/gw_channels/video_channels` 映射为稳定渠道和版本化 Transport；每个实际目录发现入口建立 CatalogSource，并在待发布草稿中建立对应的 CatalogReleaseSource 清单项；旧 `channel_accounts/gw_channel_keys/video_channel_keys` 的每个 Key 先映射为独立凭据池、逻辑凭据和加密凭据版本；旧 `models`、`models.aliases` JSON 与 `gw_model_meta` 先经全局冲突检查映射为稳定公开模型与永久名称身份，再生成发布版模型展示、名称可见关系、模型操作合同和 SKU，同一规范化名称曾指向不同模型时必须进入迁移问题表；旧 `endpoints/account_models/endpoint_accounts/gw_abilities/gw_ability_transports` 及 `endpoint_adapters/endpoint_adapter_revisions` 映射为产品、产品 Transport、Offering、路由、路由优先级/权重和版本化适配器配置。旧 `endpoint_route_states/gw_route_states` 只迁移有界诊断审计，不把瞬时健康结论当作新运行事实；新 `gw_execution_health` 从未知/健康初始状态重新探测。旧 `token_channel_priorities` 映射为 Token 路由策略版本。配置迁移只读取白名单字段，单独扫描 `callback_secret`、`config/extra_config/extra_headers`、URL 查询、请求头、载荷和日志中的认证信息；发现的 Secret 只能进入加密凭据版本，不能复制到 Transport 或目录 JSON。仅在模型权益、约束和成本完全一致后合并凭据池。
3. 冲突检查：旧 Endpoint 价格、视频固定价/上游估价倍率和实际 BillingLog 必须先标注其真实语义；只有可证明为用户售价的字段才能生成 SellRate，不能把用户售价反推为上游成本，也不能延续“上游估价乘倍率”为实时售价。同一公开 SKU 的现有售价不一致、金额由 `float64` 往返后无法证明原值或计量单位不明时停止迁移，禁止任选一个值。
4. 资金回填：先联合旧 `billing_logs`、`balance_entries`、Call、旧计费阶段和在途任务，逐笔识别已结算金额、已退款金额、活跃预授权和不确定持有，再为每个账户选择且只能选择一种迁移方式。历史完整且可证明时，从已验证期初值重放旧流水并由新流水推导切换余额；历史不完整时，以切换时点快照创建一笔期初交易，旧流水只进入不影响余额的只读迁移审计表。禁止既导入当前余额又把历史变动再次记账，也禁止把 `billing_logs` 与 `balance_entries` 中同一业务动作记两次。若旧 `users.balance` 是已扣除预授权的可用额，则新账户入账余额为“旧可用额 + 活跃及不确定持有额”，同时创建对应 Reservation；若旧字段语义不同或无法证明，停止该账户迁移。Token 预算同理：确认旧余额表示剩余额度且 `total_used` 已包含未结算预授权时，终身窗口使用 `limit = remaining + total_used`、`used = total_used - held`、`reserved = held`；若 `total_used` 仅表示已结算用量，则使用 `limit = remaining + total_used + held`、`used = total_used`、`reserved = held`。两种情况必须由历史流水证明后择一，不能把 `held` 同时计入 `used` 和 `limit`。历史 Token 事件不得在初始化后再次增加已用量。该换算仅适用于单位为同一平台结算币种的 Token；若历史语义是次数、Token 数量或无法证明单位一致，必须停止该 Token 迁移并人工确定预算规则。归属用户缺失、余额为负、退款超额、流水与快照不一致或预授权无法绑定 Call 时均进入差异清单，禁止猜测修正。
5. 通用异步、上游状态与素材回填：`tasks`、后台 `ai_responses` 与 `video_tasks` 的全部仍在产品或审计保留期内的记录都必须迁移，不能只迁在途行后删除旧表。旧 `tasks` 的公开身份和协议投影迁入 `capability_tasks`，执行、正文、路由、计费与回调字段只迁入各自唯一事实表；旧回调 URL/签名配置按 Call 归并为一个不可变 CallbackTarget，再由每个状态或交付事件生成自己的 CallbackDelivery 载荷。所有迁移后的回调必须固定目标快照，不能继续从可变领域表拼装。在途记录逐条生成可继续执行的固定 Call/Attempt、需求计数和状态事件，只有旧路由、提交检查点或上游任务 ID 能证明属于任务型 Transport 时才创建 AsyncExecution，同步后台执行继续由 Call/Attempt 租约恢复。明确终态历史生成只读 Call/Attempt/领域资源与可证明的状态、费用和结果引用，证据不足的历史进入迁移问题表而不是补造执行事实。超过在线保留期的历史先进入已验证的不可变归档并通过恢复测试。源记录已有且归属一致的 `call_id` 时扩充原 Call，不能另建重复 Call，空引用才按对象映射创建，冲突引用进入差异清单。

   状态映射必须逐项验证。任务型旧 `tasks.pending/processing/success/failed/cancelled` 分别映射到 AsyncExecution 的 `allocated/running/succeeded/failed/cancelled`；同步型旧 `tasks` 只映射 Call/Attempt 的对应可证明状态，两类都按现有公开协议生成 `capability_tasks` 的领域状态。旧 `video_tasks.queued/submitted/tracking/completed/failed/cancelled` 分别映射为 `allocated/accepted/running/succeeded/failed/cancelled`。旧 `tasks.finalizing` 必须读取 `submit_checkpoint` 和结果字段：能证明上游已成功的迁为明确执行成功并另建 `pending` ResultDelivery，无法证明的进入迁移差异清单，不能直接映射成 `running` 或 `failed`。交付中的旧记录必须另建 ResultDelivery，不得把结果复制失败改成生成失败。Responses 状态按已验证的协议终态映射。

   `ai_responses` 与 `conversations` 中仍有效的 `provider_response_id`、原 Transport 和 Key/账号引用必须迁为加密 ProviderStateRef 并验证作用域；无法证明归属的状态只能标为不可继续，不能猜测绑定。现有 `api_call_payloads` 按 Call 和用途规范化：内容 HMAC 一致的重复行只保留一个目标引用，内容不同的候选必须依据已验证执行链确定唯一事实，否则进入差异清单，禁止任选一份。

   `capability_tasks`、`ai_responses` 与 `video_tasks` 删除执行、路由、正文、幂等和计费重复字段后作为新领域资源；旧 `tasks` 仅作为停机导入来源，导入后由新领域资源和事实表提供查询，不创建应用层兼容投影、不合并旧表历史，也不再生成旧格式任务。旧 `ai_response_idempotency_cache` 迁为一条幂等事实及其当前和全部保留版本的 Alias；同一 Token、稳定操作和键出现多个 Call 时停止迁移，禁止任选一条。

   旧 `video_assets` 校验对象存在性、大小和哈希后迁入 `media_assets/media_asset_refs`；旧 `ai_files.content` 流式写入对象存储并校验大小、类型和哈希，原 `ai_files` 行瘦身为引用该对象的公开文件资源。丢失或不一致的对象进入差异清单。迁移旧 `request_params`、`mapped_params`、`provider_response` 或日志时，发现 Base64、`data:` URI 或二进制内联字段只迁移类型、长度、哈希和资源 ID；活跃执行仍需要的字节必须流式解码并校验后写入对象存储，过期且无引用的字节不迁移，原文不得复制到新表。旧表中的原文按保留策略完成脱敏清理或随旧表删除，不能因备份脚本再次回填。
6. （不执行）影子验证：旧路由不再作为运行时参照；仅在离线导入验收时验证目标数据，不保留旧路由比较代码。
7. 切换阶段：停机旧服务，备份旧库，初始化目标结构，执行单向导入和完整核对；核对通过后仅启动新服务。不存在新旧服务并行、旧 Worker 取租约、双写或兼容回调路径。
8. 导入失败：停止新服务，保留目标库和导入审计，修复导入程序后从备份重新执行；不恢复旧代码继续写入。
9. 导入完成：删除旧表、旧字段、旧适配器和旧敏感数据；目标服务只读取新结构。发布版回退仅通过新目录发布版完成，不回退到旧配置或旧二进制。

### 8.1 当前实现差距与阻断条件

下表是基于当前仓库的代码审计结果。目标架构未完成前，任何一项对应的旧路径仍被调用，都不能宣称统一网关切换完成。

| 领域 | 当前实现 | 目标差距 | 阻断门禁 |
|---|---|---|---|
| Call/Attempt | `internal/model/api_call.go` 仍使用旧状态集合和 `ability_id/channel_id/key_id/account_id/endpoint_id`；状态转换没有 `state_version`，也没有目录、SKU、Offering、成本方案快照 | 必须迁移为带发布版的不可变快照、未知状态和单活跃 Attempt 约束 | A、C |
| 目录发布 | `internal/model/gateway.go`、`internal/model/endpoint.go`、`internal/video/model.go` 仍是可变配置；Gateway V2 与视频启动后直接读取当前行，没有 CatalogRelease、稳定操作/模型身份、核心语义摘要或原子激活指针 | 必须建立规范化草稿、发布、预加载、激活与回退协议；新 Call 只能固定已激活且不可变的完整发布版 | A、C |
| 账务 | `internal/service/billing_service.go`、`balance_entry_service.go` 及用户/Token 管理路径直接更新 `users.balance`、`tokens.balance/total_used`，仍依赖明文 `billing_logs.idempotent_key` 和两套账户流水；`internal/video/adapter.go`、`internal/video/engine.go`、`internal/video/model.go`、`internal/video/canonical/types.go`、`internal/video/generic/adapter.go`、`internal/video/generic/config.go`、`internal/video/generic/validation.go`、`internal/video/seedance/adapter.go`、`internal/video/asset_service.go`、`internal/api/open/generation.go`、`internal/api/open/video.go`、`internal/api/open/video_asset.go`、`internal/api/open/video_legacy.go`、`internal/api/admin/video.go`、`internal/api/console/playground_video*.go` 的价格、倍率、时长或计费数量仍使用 `float64` 或 JSON 字段，`internal/service/dashboard_service.go` 还把 `SUM(final_cost)` 扫描为 `float64`；视频售价可由上游估价乘倍率得到 | 必须先建立 Billing Account、Reservation、BillingEvent、复式流水和 Token 预算；金额、倍率和计费数量改为定点十进制或有界整数，所有 JSON/SQL 边界禁止二进制浮点往返，公开响应不得以浮点表达金额，统计聚合也必须保留 Decimal 语义；用户售价独立发布，重新校验所有权，退款按原结算净额限额 | A、D |
| 凭据 | `ChannelAccount.APIKey`、`GwChannelKey.APIKey`、`VideoChannelKey.APIKey` 和 `Channel.CallbackSecret` 均为数据库明文；多个管理服务直接写入 | 必须迁入用途隔离的 Secret Identity、EncryptedBlob 和不可变凭据版本；回读验证并轮换曾进入备份或日志的旧 Secret 后才能清列 | A、E |
| 权益与商业验证 | 当前探测只形成即时结果或运行健康，视频与 Gateway 路由没有 Credential EntitlementState、Offering CommercialState、权威证据有效期及 `expired` 事实 | 必须按权益/商业指纹建立追加校验事件和版本化当前状态；只有未过期 `valid` 候选可创建新 Attempt | A、C |
| 任务正文 | `internal/model/task.go`、`internal/video/model.go` 和 `internal/model/response.go` 保留请求、映射、供应商响应、结果、提示词、路由及计费副本，回调地址和上游 ID 也在主表 | 新 `capability_tasks` 及其他领域表只能保留有界脱敏投影和引用；规范化正文、上游 ID、URL 必须进入加密 Blob 并按引用保留 | A、E |
| 请求日志 | `model.ChannelRequestLog` 仍有完整 URL、请求头、请求体和响应体字段；能力调用路径直接写入这些字段 | 必须改为发送前日志、HMAC、脱敏诊断预览和独立请求/查询/取消记录，并物理移除旧正文列 | C、E |
| 任务取消 | `internal/service/task_service.go` 可把已处理或结果转存中的任务直接本地取消并退款；`internal/video/lifecycle.go` 先调用上游取消再独立更新本地终态；Responses 使用进程内 `backgroundCancels`，客户端断开也会直接取消 Call，均可能与上游成功竞争 | 排队且确定未发送时才可本地取消；其余必须由 AsyncExecution 取消意图、状态版本、取消 Outbox 和核对事务处理，客户端断开不得改变执行事实 | C、F |
| 视频增值动作 | `internal/video/engine.go` 的 priority queue 先请求上游，再在独立事务修改原任务估价并结算；重复请求、数据库失败和网络超时会造成动作与收费状态分离 | 具名动作必须使用独立 Call/Attempt、固定目标执行作用域、发送前预授权和不确定结果恢复；不得改写原生成 Call 的 SKU/结算 | C、D、F |
| 超时恢复 | `api_call_recovery.go` 依据 `updated_at`/租约把前台 Call 和所有 started Attempt 直接标记失败并退回未结算金额，无法区分确定未发送与上游结果不明 | 必须依据发送日志和 Transport 恢复合同进入 `not_created`、`recovery_pending` 或 `terminated_unknown/unknown_hold`，不能按陈旧时间直接失败退款 | C、D、F |
| 并发与选路 | `internal/video/router.go` 使用 Redis TTL、进程随机加权和 Redis 并发计数；无法证明数据库名额和恢复状态 | 必须改为数据库凭据池/凭据名额、确定性哈希选路、租约栅栏和 `recovery_required` | A、F |
| 素材 | `video_assets` 只按 Token 归属，远端 URL 可直接作为存储路径；`AssetService.Create` 先上传对象再插入数据库，缺少 staging 事实、Call 强引用和删除状态版本；Responses 还会在内存构造完整 data URI | 必须按用户与 Token 验证所有权，先建立 staging 行再下载或上传并校验，以 MediaAssetRef 保护引用；仅上游强制时流式编码 Base64 | A、E、F |
| 结果交付 | `internal/video/result_materializer.go` 在任务终态事务前直接复制结果并用新 URL 覆盖上游 URL，没有 ResultDelivery、对象 staging 记录、下载请求日志或失败恢复；数据库提交失败可能留下不可发现对象 | 必须分离生成终态与交付终态，先建 ResultDelivery、staging MediaAsset 和强引用，再下载、校验并原子发布；复制失败不能改成生成失败 | A、C、E、F |
| 用户回调 | 通用任务只在主表保存明文 URL、回调状态和次数，`notify_worker.go` 从可变任务字段临时组装载荷；视频任务也保存明文回调 URL，缺少统一 CallbackDelivery 和逐次发送记录 | 必须固定加密 URL/签名配置与唯一载荷 Blob，每次发送创建 CallbackDeliveryAttempt，按事件 ID 至少一次投递且不覆盖历史 | A、C、E、G |
| 上游回调身份 | `internal/api/middleware/callback_auth.go` 以渠道级明文 Secret 和任务号验签，`internal/api/callback/handler.go` 直接推进旧任务；没有每执行绑定令牌、Receipt/Alias、早到回调等待或验签版本快照 | 必须先认证并持久化唯一 Receipt/Alias，再由消费者按 TaskIdentity 单向绑定和状态版本处理；入口不得直接改变执行、计费或交付事实 | A、C、E、F |
| Files API | `internal/model/file.go` 把完整 `ai_files.content` 作为数据库 `longblob` 保存，上传路径整份读入内存，删除不具备 Call 强引用保护，生命周期与通用素材分离 | 保留公开文件元数据，改为有界流式上传，把原始字节迁入统一私有对象存储并以 MediaAssetRef 管理强引用与删除 | A、E、G |
| 加密 | `api_call_payload_crypto.go` 使用单个可选配置密钥，密钥缺失时会保留明文，且未记录 KEK/DEK 版本，无法在不改正文的情况下轮换 | 必须使用 EncryptedBlob 与追加式 KEK 包裹记录，区分凭据、正文、HMAC 和对象存储密钥用途；必需密钥不可用时服务拒绝就绪和写入 | A、E |
| 迁移回填 | `20260714_204500_backfill_api_call_ledger.sql` 使用 `INSERT IGNORE`，并按旧余额直接建立流水基线，不能证明重复回填和在途预授权语义 | 必须通过 Epoch、对象映射、源修订和逐账户选择的账务回填方式；冲突停止，禁止吞掉唯一键错误 | B、D |
| 幂等 | Responses 仍有独立缓存表和原始键字段，到期清理会直接删除键记录；统一 Call 创建入口未固定稳定操作身份与规范化序列化 | 必须使用幂等事实与版本化 Alias，区分重放期和键复用时间，以固定规范化字节处理重复与冲突 | C、D、E |
| 有状态续接 | `api_call_attempts.provider_response_id` 与 `conversations.provider_response_id` 直接保存上游状态 ID，缺少用户/Token 复合归属、Transport 作用域、兼容指纹、版本化 Alias 和引用保留 | 必须迁为唯一加密 ProviderStateRef；后续调用先授权并锁定状态，只能使用兼容 Transport 与原作用域 | A、C、E、F |
| 所有权 | 旧服务接口接收 `userID/tokenID` 后直接执行，账务边界未重新证明 Token 属于用户、Reservation 属于 Call | 必须以复合外键和创建事务锁定当前读证明归属，所有公开查询同时校验资源、Token、用户 | C、D、G |
| Token 鉴权 | `internal/api/middleware/auth.go` 使用完整 Key 的无版本 SHA-256 摘要并把整行 Token 缓存五分钟；撤销与并发缓存回填之间无法证明立即失效，`DeleteToken` 仅做通用 GORM 逻辑删除而没有显式撤销版本和在途创建栅栏 | 必须迁为版本化选择子/秘密摘要、主库状态复核和只撤销不删除；Redis 不得作为授权事实源 | A、C、E |
| 查询性能 | `GetCallDetail` 一次读取该 Call 的全部 Attempt 与 BillingLog，列表使用 Offset；历史事件规模增长后会拖慢异步详情 | 必须对事件、请求日志、成本和回调使用独立有界分页与覆盖索引，正文按 ID 单独读取 | G |
| 部署激活 | `cmd/server/main.go` 只检查迁移并注册当前二进制适配器；没有不可变部署成员清单、逐版本 Catalog/Key Readiness、适配器摘要核验、需求分片或扩容准入 | 必须建立完整部署代次和就绪证明，任何缺席实例、摘要不符或活动兼容性需求未覆盖都阻止激活和实例就绪 | A、C |
| 生产结构 | 目标表、触发器、复合外键和状态 CHECK 尚未存在；现有测试大量使用 GORM `AutoMigrate` | 必须先完成正式迁移并在生产结构副本、重复执行和 MySQL 8.0.16+ 上验证，生产服务拒绝未达目标代次的数据库 | A、B |

门禁含义：A=目标结构与数据库约束完成；B=历史回填和对账完成；C=Call/Attempt/AsyncExecution 新写路径独占；D=账务与预算新写路径独占；E=正文、Secret 和旧敏感列清理完成；F=异步取消、名额和恢复新路径独占；G=查询与管理 API 完成有界分页；H=停机导入完成并删除旧表、旧字段、旧适配器和旧敏感数据。H 未通过时，激活指针不得切换，目标服务不得启动。

### 8.2 迁移执行约束

目标数据库只执行新的正式迁移文件，禁止 GORM AutoMigrate。离线导入器读取停机快照，按固定顺序写入目标表，并使用 `migration_runs`、`migration_object_map`、`migration_source_revisions` 和 `migration_issues` 保证可重复执行、冲突停止和审计完整。导入器不得调用线上业务服务，不得发送上游请求，不得产生用户扣费或新的异步任务。

迁移状态只服务离线导入：Run 允许 `scheduled -> running -> succeeded/failed`，失败重跑复用同一 Run 和对象映射；Issue 允许 `open -> resolved/waived`，`waived` 必须追加独立审核事件。所有转换保留操作者、时间和证据摘要，禁止删除后重建。

导入完成后执行结构核对、账务核对、对象完整性核对、凭据可解密核对和状态引用核对。任一核对失败都停止启动新服务，从原始备份重新执行导入；不恢复旧服务写入，也不接受新旧结构之间的兼容请求。核对通过后删除旧表、旧字段和旧敏感数据，目标服务只访问新结构。

## 9. 发布门禁

- 数据库过程、触发器、复合外键、CHECK 与发布校验覆盖发布版冻结、跨渠道/跨用途引用、SKU 重叠、缺失费率、未知计量单位、空凭据池、状态转换和对象不可覆盖语义；迁移代次及其规范清单摘要必须等于 CatalogRelease 固定的核心语义摘要。生产应用数据库角色只能执行已批准的状态、计费、名额和过账过程，不能直接更新事件事实、已过账流水或绕过触发器；过程的 `SQL SECURITY DEFINER`、定义者权限、迁移顺序和回收脚本必须在生产结构副本上验证，管理员人工操作使用单独受审计角色。
- 对创建、提交、接受、轮询、终态、结算和回调每个边界做崩溃注入测试；上游 inline Base64/二进制结果必须覆盖建 Delivery/MediaAsset 前失败、流式写入中断、对象完成后数据库提交失败及恢复校验，且任何阶段都不保存完整 Base64 或覆盖既有对象。
- 用状态机模型检查所有合法转换和非法转换，覆盖排队取消、提交中取消意图、早到回调、明确拒绝、同步恢复、轮询暂态错误、取消竞态、不确定提交、`terminated_unknown` 与迟到证据；非法转换必须不改变金额、名额或版本。
- 并发测试证明同一 Call 不会产生并行活跃 Attempt、池级与凭据级名额均不超限、上游任务 ID 冲突不能绑定两个 Attempt，且回调与轮询竞争只产生一个明确终态和一组幂等成本/结算事件；还必须覆盖 Credential/PurposeGrant 进入 `draining` 与发送授权竞争、CredentialVersion 轮换及 ControlPlaneRun 重试，证明新 Attempt 被拒绝而此前固定的执行可继续，新版本不继承旧权益验证，`disabled/revoked` 后不再产生新发送。
- 结果交付并发测试覆盖 Source 刷新、旧下载在途、对象写入完成、发布和删除竞争；只有当前 Source 对应的全新对象键可发布，旧下载不得覆盖、复用或公开。
- 计费性质测试覆盖多组件、按次、按秒、步长、最小量、精度、超上界、成本估算转报告及正反修正、部分退款、重复退款、不同事件键并发退款、退款与负向扣款修正竞争、过期预算窗口和历史流水重算；还必须用规范十进制字符串覆盖指数、负零、超精度、溢出、NaN/Inf、浮点往返和不同 JSON/SQL 驱动的解码差异，验证统计聚合不会把 Decimal 转成浮点；任何序列都必须保持成本事件链净额非负、单币种借贷平衡且累计退款不超修正后净结算额。
- 预算并发测试覆盖首条 Activation、同一链尾并发追加、未来 Activation、周期边界和 `unlimited` 换版；数据库只能形成一条严格递增链，Call 只能固定数据库当前时间唯一生效的 Policy 与窗口。
- Ledger 故障注入覆盖分录插入中断、过程校验失败、账户缓存更新失败、过账状态更新失败和死锁重试；业务事务必须全部回滚，在线库不得出现部分余额变化或正常路径提交的 `assembling` 行。
- 契约测试固定 AICost 请求/响应样本；上游字段变化只会使新导入失败，不影响已发布目录。
- 安全测试证明数据库、日志、错误、管理 API 和队列载荷均不出现明文 Key、原始幂等键或 Base64 素材，并覆盖重复凭据拒绝、回调伪造/重放、DNS 重绑定、重定向、HMAC 密钥轮换，以及轮换前后并发使用同一凭据、幂等键或回调事件仍只生成一个事实记录。
- 迁移测试覆盖全新数据库、生产结构副本、在线源变化、同内容重跑、不可变源篡改、在途预授权、正在发送的请求、切换期早到回调、旧队列消息、快照与历史重放二选一、阶段回退和旧表最终删除。
- 部署测试覆盖不可变实例清单、空清单拒绝、缺席实例阻止激活、目录/适配器摘要与 CryptoKey Readiness 核对、扩容准入、有状态连续性与 ControlPlaneRun 实现需求、目录发布、原子激活、CredentialVersion 启用、Keyring 轮换、仅价格变更的发布沿用健康状态和已发布版本回退；禁止复用适配器契约版本对应不同实现，也禁止移除仍被活跃执行、非终态 ControlPlaneRun、未过期 ProviderStateRef 或审计保留期内调试重建引用的适配器实现、HMAC 密钥或加密主密钥。
- 负载测试在生产量级副本验证日志分页、Worker 扫描、名额争用、账务行锁和 Outbox 恢复，不接受未经执行计划验证的查询。
- 上线前必须配置并验证告警：未知持有数量与最长年龄、`recovery_required` 名额、Outbox/回调死信、交付失败、账务差异、凭据耗尽、目录实例未就绪和迁移状态异常。

## 10. 故障反例审计

| 反例 | 必须得到的唯一结果 |
|---|---|
| 用户重复提交相同幂等键 | 同请求返回原 Call；请求 HMAC 不同则冲突；不新增预授权或上游提交。 |
| 回调与轮询同时报告终态 | 仅一个状态版本更新成功，且只有一组按组件唯一的用户结算和上游成本事件。 |
| 活跃执行使用的凭据被普通停用或安全撤销 | 普通停用不影响固定版本；安全撤销禁止解密并进入人工处理，不能悄悄换 Key。 |
| 活动目录回退 | 只影响新 Call；旧 Call、Attempt、费率和适配器引用保持原发布版。 |
| Redis 在投递前后故障 | 数据库状态不变；扫描器重建调度；重复消息被状态版本和租约拒绝。 |
| 上游成功但结果复制失败 | 生成状态保持成功，交付状态独立重试；不重复生成或重复收费。 |
| AICost API 与人工价格资料冲突 | 导入草稿标红并禁止发布；现有发布版与在途任务不变。 |
| 提交超时且上游可能已接收 | 进入 `submission_unknown` 并保留名额和预授权；只能经取证恢复或审计式人工处理。 |
| 无法在期限内证明上游是否结束 | AsyncExecution 进入 `terminated_unknown`；名额转 `recovery_required`、预授权转 `unknown_hold`，必须有独立审计动作才能处理。 |
| `terminated_unknown` 后收到可信终态 | 走唯一核对事务恢复明确状态并处理名额、费用和交付；不创建新生成 Attempt，不删除原未知事件。 |
| 有合格路由但暂时无凭据容量 | 按快照排队或快速失败；排队不创建 Attempt/名额，超时释放预授权，不能报告 `no_route`。 |
| 同池某个 Key 权益或价格不同 | 该 Key 不得启用；拆分凭据池后重新生成并审核目录草稿。 |
| 最终用量异常超过预授权 | 记录完整应收和欠款并冻结新调用；不截断、不重复扣款。 |

## 11. 明确不采用

- 不采用 Key 与模型逐条绑定的 `video_key_models`。
- 不把 `gw_abilities` 同时当配置源和派生索引。
- 不使用“上游成本乘倍率”作为用户实时售价。
- 不从模型名或描述文字猜测按秒/按次。
- 不用 Redis TTL、进程内计数或启动清零维护异步并发。
- 不把整个异步任务生命周期记成一次 HTTP 请求。
- 不在生产期长期同时写旧模型和新模型。

## 12. 已提交的基础实现

- `internal/gateway/repository`：统一 SQL 写边界，覆盖 Call/Attempt、AsyncExecution/Outbox、租约过期接管与栅栏、请求日志与凭据名额、加密 Blob、凭据轮换、目录发布/激活、部署代次与就绪记录、预算策略与窗口、结果交付、回调、ProviderState、任务投影、媒体资产、运行需求、执行健康、成本事件和账务预授权/结算/释放；查询入口提供有界分页。
- Outbox 重新取得租约时只能进入显式恢复处理，不允许默认再次发送；租约持有者、尝试序号和未过期时间会在最终写入时同时核对，超过十次失败进入死信状态。
- 货币定义现在参与 Call、预授权和复式账务写入校验；金额必须符合货币小数位和最大金额，负零、超精度及未知货币版本会被拒绝。
- `internal/gateway/runtime.CheckReadiness` 提供只读启动门禁：部署代次、实例成员、目录发布版摘要和加密密钥就绪状态任一不满足时拒绝流量启用。
- `internal/gateway/runtime`：提交、终态处理和 Outbox 租约 Worker 编排；网络交换仍在事务外执行，结果以请求日志回写。
- `internal/gateway/catalog`、`security`：不可变目录索引编译、规范名称校验、域隔离 HMAC 和密封信封。
- 全部新包已通过 `go test ./...` 与 `go vet ./...`。真实库迁移、旧入口切换、历史导入和部署就绪检查仍必须按第 8 节门禁完成后才可启用流量。
- `20260906_100000_unified_gateway_audit_events.sql` 补齐凭据用途、凭据版本、目录发布、路由策略和账户状态的追加审计表。



