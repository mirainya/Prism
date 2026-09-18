# Prism v5.4 设计文档：运维台 · 计费 DSL · Adapter Manifest

> 作者：铃兰（Claude）＋ mirainya
> 起稿：2026-09-10 · 修订：v5.4（Pre-M1 通读 v2 设计动因后修订，消除与 v2 事实的四处冲突）
> 关联记忆：[[gateway-v2-rewrite]] [[protocol-conversion-audit]] [[audit-fixes-fund-concurrency]] [[feedback-v5-red-lines]] [[v2-catalog-why-invariants]]
> 一手依据：`docs/specs/2026-09-04-unified-gateway-catalog-billing-architecture.md`（下称 SPEC，578 行）+ `database/migrations/20260905_*`~`20260910_*` + `internal/gateway/{repository,billing,adapter,deployment}`

## 0. 一句话总纲

**v2 骨架 100% 保留 + Prism 独立不镜像上游 + 前端单页清爽给主人。**
底层富到能吃下任意格式的上游（Manifest + DSL + JSON），前端简到主人一屏看完改完。**两件事**，不互相牵连。

## 0.2 当前实现对照（2026-09-12）

本文的 M1-M5 原本是实施计划，不等于已经全部交付。当前仓库与设计目标的对应关系如下：

| 阶段 | 当前状态 | 说明 |
|---|---|---|
| M1 | 基本完成，运行时仍有缺口 | 迁移、内容摘要、Decimal DSL、Manifest 注册和发布期上界门禁已落地；Chat/Responses、图片和视频路径已注入可证明的 `Facts.Expr`，并由执行器补齐 `duration_ms`。文档要求的单次求值时间限制和严格单调性证明仍未完全实现。 |
| M2 | 基本完成 | 已内置 seedance、OpenAI Chat、OpenAI Responses、Anthropic Messages、Google Generate Content 五份 Manifest；实现采用编译进二进制的 Go 声明，不是外置 YAML。 |
| M3 | 后端基本完成 | 14 层 Release clone、A/B 类接口和 fork→publish→prove→activate 链路已有实现及测试；前端已接入售价、JSON 映射和上游成本倍率，其他 B 类字段仍待接入。 |
| M4 | 部分完成 | `OpsConsole` 已有模型、上游对接、调用记录读视图，并支持售价/表达式、JSON 映射和成本倍率编辑；DSL 试算、字段级动态详情抽屉和完整 A/B 反馈仍待完成。 |
| M5 | 部分完成 | `ConfigHistory` 已有时间线、回滚和待激活重试；完整逐表差异、审计跳转、旧页面删除和兼容期清理尚未完成。 |

另外，`subject_discount_ratio` 按本文范围界定属于后续工作，并非 M1 已交付内容。生产 v2.0.0 已完成部署切换，但真实对话、图片、视频业务验收和观察期记录仍属于独立的最终验收项，详见 [`docs/unified_gateway_acceptance.md`](../unified_gateway_acceptance.md)。

## 0.1 三条红线（改动前必看 [[feedback-v5-red-lines]]）

1. **v2 表结构不动**——只加字段/加新表，不删任何 v2 表
2. **Manifest 保留富字段**——`billing_vars/billing_funcs/variants[]` 分层齐全，禁止砍成 5 模式结构化
3. **计费 DSL 保留**——AST 白名单沙箱，`flat` 默认 + `expression` 高级双模式，禁止砍成固定 pricing_mode 枚举

## 1. 背景与心智

### 1.1 v2 复杂度来自上游，不是设计过度

审计已有上游 + 主人陆续给的接口文档，确认 v2 的抽象都对应真实的上游痛点：

| v1 崩塌点 | 上游侧证据 | v2 的抽象 |
|---|---|---|
| 参数元数据无处放 | seedance 3 渠道 × 5 分辨率 × 时长上下界 × 硬约束 | `gw_products.extra_config` + `gw_operation_contracts` |
| 计费无法表达式 | seedance 秒×基价×优先级×素材倍率；同类 tiered_expr | `rate_evidence + review + sell/cost_rates + DSL` |
| 协议流程无法参数化 | seedance R2 直传 + 分片 + 幂等 7 步流程 | `gw_adapter_implementations` + adapter 层 |
| 审计链断裂 | 62 模型 × 29 分组 × 定期变动的定价 | `catalog_releases + state_events + control_plane_runs` |
| 多协议共存 | 同一模型同时暴露 `anthropic + openai` 入口 | codec 三种下游 + `gw_sku_downstream_paths` |
| 上游成本分组 | AICost 上游 Key 分组的成本差异 | 已由 `credential_pool.cost_group_ratio` 承载 |
| 客户价差 | VIP × 0.45 / 内部 × 0.11 | 待补下游主体 `subject_discount_ratio`（⚠️ 不是池倍率，见 §4.4） |

**关键**：这些上游只是**已知集**。下一家上游一定又是新格式（form-data / SOAP / 自研协议 / 多步幂等 / 特殊素材类型）。**v2 的富抽象层就是承接未知集的机器**——砍任何一层等于让未来某天的自己回头补课。**故 v2 表结构一律保留**。

### 1.2 Prism 的产品边界（独立门面）

```
下游世界（Prism 门面 · 三协议）           ← 对外
  /v1/chat/completions | /v1/responses | /v1/messages
  Prism 自主定价（自己的 sell_rate，售价与选路无关）
                    ↑ 归一化
上游世界层（Prism 内部 · adapter 消化）     ← 内部富
  每家一个 adapter + 一份 manifest（消化其格式）
  DSL 计费 / variant / release / deployment / audit
  ← 复杂但主人不管，铃兰和 adapter 负责
                    ↑ 挑要接的
真实上游（各种奇怪格式）                    ← 外部
  seedance / openai / anthropic / gemini / ...
  ⚠️ Prism 不镜像上游，只挑合适的接入
```

**Prism 是独立门面**：
- 挑自己想接的上游（不做 upstream catalog 镜像）
- 自己定价（不同步上游 pricing table）
- 归一化后对外只讲三协议

### 1.3 两层心智（前端 ≠ 后端）

```
┌─────────────────────────────────────────────┐
│ 主人视角层（前端运维台 · 清爽）              │
│  全宽列表 + 按需详情抽屉 + 手风琴分段          │
│  fingerprint/digest 塞入"技术详情"折叠段     │
│  ← 只承接"人的轻改"                          │
└─────────────────────────────────────────────┘
                    ↑ 只暴露可运维字段
┌─────────────────────────────────────────────┐
│ 后端骨架层（v2 完整 · 富）                   │
│  gw_* 全表 + Manifest 富 YAML + DSL 沙箱     │
│  ← 承接任意上游的笛卡尔积复杂度              │
└─────────────────────────────────────────────┘
```

**关键铁律**：**"前端简化"不等于"后端简化"**。主人抱怨 UI 深 → 只改布局；主人抱怨改价难 → 加改价接口把 v2 既有流程自动化（**不是绕过它**，见 §5.2）；**永远不动 v2 骨架/Manifest/DSL**（红线 [[feedback-v5-red-lines]]）。

### 1.4 v2 计费结构的三条根因（Pre-M1 通读结论）

Pre-M1 阶段通读了 `docs/specs/2026-09-04-unified-gateway-catalog-billing-architecture.md`（下称 SPEC）全文 + 全部 v2 迁移 + 现有 Go 实现。`rate_components` 那 6 个字段不是过度设计，每个都对应一条不可让的产品约束。**v5 的任何计价改动必须同时满足这三条**，否则就是重犯 v5.2 的错（详见 [[v2-catalog-why-invariants]]）。

| 根因 | SPEC 原文 | 结构体现 | 对 v5 DSL 的硬要求 |
|---|---|---|---|
| **售价必须可证明上界** | §6.1「创建任务时固定售价发布版并**预授权可证明的最大金额**；无法给出上界的预付费 SKU 不得发布」 | `max_quantity` 即合同上界，`Reserve()` 直接用它当预授权量（`billing/rates.go:191-203`），不用 token 估算 | 发布期必须能算出有限 `max_price` |
| **售价必须可向用户解释** | §3.2「售价只能使用 Prism 能确定并向用户解释的数量来源」「按秒单位来源不明时不得发布」 | `quantity_source` 是 11 值闭集，非自由字符串 | DSL 变量只能来自 manifest 声明的可解释事实 |
| **金额禁浮点** | 不变量 10「金额以及会影响计费、预算、并发或资格边界的数量（时长、Token、字节、步长、**倍率**）全程使用有界整数或定点十进制…不使用 `float64`」 | seedance 特意解析 `json.Number` 而非兼容投影的 float64（`adapter/seedance.go:142-166`） | 求值全程 `billing.ParseAmount` 定点十进制 |

**一条容易被忽略的模式**：v2 在每个证据边界都拒绝凭空生成事实——导入的目录故意 `cost_plan_code='legacy-unpriced'`；`entitlement_commercial_guards` 迁移注释写「no validation state is invented」；catalog discovery 的候选价必须人工 reviewer + 真 `rate_evidence_id` 才能确认。**v5 的 `source='operator_manual'` 自动 accept 是新开的口子**，必须显式降低 authority 等级（见 §5.2）。

## 2. 前端：运维台单页

### 2.1 菜单终态（唯一版，改完不再动）

```
📊 概览        → 仪表盘
🎛 运维台 ⭐   → 主要落脚点（默认页，含"模型"和"上游对接"两 tab）
🔬 使用       → 令牌 / 试用 / 文档
📋 日志       → 调用 / 异步 / 视频 / 对话
⚙️ 系统       → 用户
🗂 历史        → 配置历史（跨模型 release 时间线 + A 类变更流）
🧯 老库迁移    → 有内容才显示
```

**注意**：没有"上游"顶部菜单——Prism 独立不镜像上游价格表。"上游对接"是运维台内的一个 tab，管理的是 Prism 自己的**渠道 + API Key 池**，不是上游的定价目录。

### 2.2 运维台布局

```
┌──────────────────────────────────────────────────┐
│ 主表格（全宽）                                    │
│ ──────────────────────────────────────────────── │
│ tabs：[模型] [上游对接] [调用记录简化视图]          │
│ 列：模型 / 服务规格 / 上游 / 入口 / 状态 / 版本      │
│ 点行 → 右侧详情抽屉                                │
└──────────────────────────────────────────────────┘

右侧详情抽屉（按需打开）：
  ▸ 基本信息（默认展开）  ▸ 模型与上游
  ▸ 用户售价              ▸ 参数约束
  ▸ 下游入口              ▸ 技术标识
  ▸ 配置历史              ▸ 审计事件
```

**关键铁律**：
- 弹窗层数 ≤ 1（仅破坏性确认弹）
- 列表默认全宽，点击记录后按需打开右侧详情抽屉；切换记录时抽屉内容直接更新
- 抽屉支持遮罩、Esc 和关闭按钮，详情内容独立滚动，不产生页面外层滚动条
- 主人所有轻改都在详情抽屉内完成，不打开额外页面
- 技术标识/审计等段落默认折叠，专业人士想看展开即可

### 2.2.1 两类改动的 UI 反馈必须可区分

按 §5.1 的 A/B 分类，详情抽屉每个可改字段旁标一个小标记，改完后的反馈也不同：

| | A 类（原地生效） | B 类（生成新配置） |
|---|---|---|
| 字段标记 | 无标记（默认） | `⟳` 小图标 + tooltip「改动将生成新配置版本」 |
| 保存后 | 绿色 toast「已生效」 | 进度条走 fork→publish→prove→activate 四段，完成后 toast「配置 #N 已激活」 |
| 失败时 | 红色 toast + 原值回填 | 顶部黄条「配置 #N 已生成待激活」+ 重试 / 丢弃两个按钮 |
| 典型字段 | 凭据超时/并发/权重、池限额、`cost_group_ratio`、上游启停、展示名/分组/排序 | 售价、成本价、路由权重、变种、下游入口、`vendor_model` |

**为什么不藏掉这个区分**：主人是技术型运维（[[feedback-v5-red-lines]]）。「改这个会不会生成新版本、要不要等激活」是主人**需要知道**的事——尤其是紧急停用上游时，得清楚这是 A 类秒级生效，不用等 fork 链。藏起来才是真的坑人。

### 2.3 详情抽屉各段字段

**基本信息**：模型名 · vendor_model · Adapter · Variant · Capability · 状态 · 显示名 · 分组 · 排序

**计价**：模式（flat / expression） · 表达式编辑器（Monaco+DSL） · 变量参考 · 试算器 · 最近改价（+人+时间）

**参数约束**：max_tokens 上限 · temperature 范围 · 上下文长度上限 · supports_tools/vision/thinking

**映射参数**：按 manifest.params 动态渲染（枚举/数字/布尔/JSON），硬约束灰化并 tooltip 说明

**下游入口**：勾选 `/v1/chat/completions` `/v1/responses` `/v1/messages`（受 manifest.downstream_paths 白名单限制）

**上游成本分组**：绑定的 credential_pool 列表 + 每个 pool 的 `cost_group_ratio`（行内可改，A 类原地生效）。⚠️ 此倍率**只影响成本核算**，不影响用户售价（§4.4）

**技术标识**：SKU / Model Operation / Offering / Route / Product ID · Adapter/Semantic/Entitlement/Commercial Digest · Config/State Version · Release/Deployment ID · 熔断状态 · 并发上限

**配置历史**：该模型相关的 release 时间线 · 每条与上一版的 diff · 一键回滚（走 fork，见 §8.2）

**审计事件**：关联 audit_events / control_plane_runs / validation_events 快速跳转

### 2.4 上游对接 tab（合并 v2 老"渠道 + API Key"）

主表格：一行 = 一个渠道，缩进展开 = 该渠道的 credential_pool。选中渠道行 → 按需打开详情抽屉显示渠道详情；选中 credential 行 → 按需打开详情抽屉显示 credential 详情。**不再拆两级抽屉**。

### 2.5 老 UI 处理

- `console/pages/UnifiedGateway.tsx` + `unified_gateway/*.tsx` 22 个组件全部删除（3061 行）
- `/unified-gateway/*` 服务端 302 → `/ops-console`
- `/legacy/gateway` 隐藏路由保留 30 天（menu 不显示，可直接访问作紧急回退）

## 3. 后端：数据模型增量

### 3.1 新增字段（现有表）

**写法约束**：全部沿用 `20260906_200000_gateway_sku_service_tiers.sql` 的 `information_schema` + `PREPARE` 幂等模板（脏跑可重试、全量应用后变 no-op）。禁止裸 `ALTER`。

```sql
-- ① 上游成本分组倍率（⚠️ 只进 final_cost，绝不进售价，见 §4.4）
ALTER TABLE gw_credential_pools ADD COLUMN cost_group_ratio DECIMAL(18,8) NOT NULL DEFAULT 1.00000000;

-- ② SKU 加变种码，解决 seedance 一 adapter 多渠道
--    ⚠️ gw_skus 无 adapter_code 列（adapter 身份在 gw_channel_transports.adapter_implementation_id），
--    v5.3 原稿的 idx_variant(adapter_code, variant_code) 建不出来。改用 release 作用域索引：
ALTER TABLE gw_skus ADD COLUMN variant_code VARCHAR(64) NOT NULL DEFAULT 'default';
ALTER TABLE gw_skus ADD INDEX idx_gw_skus_release_variant (release_id, variant_code);

-- ③ 卖价/成本表达式（老 unit_price 保留作 flat 短路）
ALTER TABLE gw_sell_rates ADD COLUMN pricing_expr TEXT NULL;
ALTER TABLE gw_sell_rates ADD COLUMN pricing_mode VARCHAR(16) NOT NULL DEFAULT 'flat';
ALTER TABLE gw_sell_rates ADD CONSTRAINT ck_gw_sell_rates_pricing_mode
  CHECK (pricing_mode IN ('flat','expression'));
-- expression 模式必须有表达式，flat 模式必须没有（互斥，防半配置行）
ALTER TABLE gw_sell_rates ADD CONSTRAINT ck_gw_sell_rates_expr_pairing
  CHECK ((pricing_mode='expression' AND pricing_expr IS NOT NULL)
      OR (pricing_mode='flat'       AND pricing_expr IS NULL));
-- gw_cost_rates 同三条

-- ④ SKU 绑定下游入口（release 作用域 + 复合外键，与 v2 全表一致）
CREATE TABLE gw_sku_downstream_paths (
  id         bigint unsigned NOT NULL AUTO_INCREMENT,
  release_id bigint unsigned NOT NULL,
  sku_id     bigint unsigned NOT NULL,
  path       varchar(128) NOT NULL,
  created_at datetime(3) NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_gw_sku_downstream_paths (release_id, sku_id, path),
  UNIQUE KEY uq_gw_sku_downstream_paths_release_id_id (release_id, id),
  CONSTRAINT fk_gw_sku_downstream_paths_sku
    FOREIGN KEY (release_id, sku_id) REFERENCES gw_skus (release_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
```

**为何 `gw_sku_downstream_paths` 不带 `enabled`**：它是 release 作用域内容表，v2 全部内容表发布后不可改（唯一例外是 `gw_offering_runtime_state`）。「停用某入口」= 删行 → 新 release，不是翻标志位。带 `enabled` 会重犯 SPEC §3.4 禁止的「启停状态混进内容行」。

### 3.1.1 ⚠️ 新增列必须同步进 content digest（Pre-M1 发现）

`repository/catalog_digest.go:12-27` 的 14 条查询逐列 hash 出 `gw_catalog_releases.content_hash`。**`service_tiers` 当初就被加进了第 4 条查询**（`catalog_digest.go:16`）——这是先例：release 作用域内容表加列，必须同步加进 digest，否则该列对内容哈希不可见，两个实例可以带不同 variant 却算出同一个 content_hash。

M1 必须同步改三条查询：
- 第 4 条（`gw_skus`）追加 `variant_code`
- 第 12 / 14 条（sell/cost rates）追加 `pricing_mode, pricing_expr`
- 新增第 15 条：`SELECT id,sku_id,path FROM gw_sku_downstream_paths WHERE release_id=? ORDER BY id`

同时把域标签 `prism-catalog-content-v1` → `prism-catalog-content-v2`（digest 输入集变了就该换代）。**影响面**：已发布 release 的 `content_hash` 是存量列，不重算，readiness 比的是存量值，故老 release 与其 readiness 证明**不受影响**；只有 M1 之后新发布的 release 用新算法。

**⚠️ 例外：`cost_group_ratio` 不进 digest。** 它落在 `gw_credential_pools`——**不是 release 作用域表**，属 A 类可运行时改。若把它算进 `catalogContentDigest`，改一次倍率就会让同一个已发布 release 重算出不同 content_hash，`catalog_readiness.go:23` 的 `r.content_hash=c.content_hash` 静默失配 → 全实例 readiness 失效。**判据**：只有 release 作用域内容表的列才进 digest；非 release 表靠自己的 `config_version` 管版本。

### 3.2 明确不做（Prism 独立性边界）

**不新增**任何"上游镜像"表：
- ❌ `gw_upstream_catalogs`（不镜像上游模型清单）
- ❌ `gw_upstream_vendors`（不做 vendor 元数据表）
- ❌ `gw_sku_upstream_bindings`（不做 SKU→上游 catalog 的多对多绑定）
- ❌ 任何上游同步 driver 代码

**原因**：Prism 是独立门面，自主挑要接的上游、自主定价。上游怎么定价是**上游的事**，不是 Prism 应该镜像的数据。要接一家上游 = 铃兰写一个新 adapter + 一份 manifest，主人在前端配价格。**不走"从上游拉清单批量上架"这条路**。

### 3.3 表结构删减

**不删任何 v2 表**。`gw_credential_entitlement_state / gw_commercial_state / gw_control_plane_runs / gw_credential_validation_events / gw_commercial_validation_events / gw_rate_evidence_review_events / gw_rate_evidence_review_state / gw_credential_purpose_grants` 等审计骨架**一律保留**。这是 v2 的审计链核心，删了合规和多实例证明就断。

## 4. 后端：计费 DSL

### 4.0 与 SPEC「不建设规则 DSL」的关系（必答，不绕过）

SPEC §1:22 有一句：「不拆微服务、不引入分布式事务，也**不建设通用工作流或规则 DSL**」。红线 3 要保 DSL——两者必须当面对齐，不能装作没看见。

**结论：v5 的 DSL 合法，但只能是「价格算术表达式」，不是规则引擎。** 区分线：

| SPEC 禁止的 | v5 的 DSL |
|---|---|
| 通用工作流 / 规则引擎（决定**做什么**、**何时做**） | 只算一个金额标量 |
| 通用表达式执行器混进 SKU 选择器（SPEC §3.2:95，会毁掉发布期的重叠/覆盖可判定性） | **完全不进选择器**。SKU 选择、成本方案选择、路由全部沿用现有等值/集合/区间判定，DSL 一行不参与 |
| 让 JSON/表达式改变用户售价（SPEC §3.4:259） | `pricing_expr` 是**正式列**，与 `unit_price` 同级，不藏在 JSON 里 |
| 决定何时收费 | `charge_event` 独占「何时收费」。DSL 只答「收多少」 |

**换句话说**：DSL 站在 `rate_component` **内部**，替换的只是 `unit_price × quantity` 这一步乘法；组件的身份、触发时机、数量来源、上界声明全部照旧由既有列表达。这是 SPEC §3.2:103 已经承认的分工——「离散价格差异由成本方案选择器表达，连续用量由组件数量表达」，v5 只是允许组件金额里出现一个受限算术式。

### 4.0.1 三条根因对 DSL 的硬约束（对齐 §1.4）

1. **定点十进制，禁 float64**：解析器数值字面量直接进 `billing.ParseAmount`，全程 Decimal 上下文（`half_even`），求值中间值也是 Decimal。**AST 里不存在 float64 类型**。整型比较（`priority==4`）走 Decimal 精确比较。
2. **可证明上界**：`expression` 模式的 rate 在**发布期**必须通过上界证明才能 publish。
3. **变量可解释**：DSL 标识符只能取自 manifest 分层 `billing_vars`，且每个变量必须在 manifest 里写 `desc`（面向用户的解释）。未声明标识符 pre-parse 期拒绝。

### 4.0.2 上界证明算法（发布期 gate）

`CheckCatalogPricing` 增加一段：对每条 `pricing_mode='expression'` 的 sell rate，用该 SKU 变种的 manifest **参数域角点**穷举求值，取最大值写入 `max_price`；无法枚举则拒绝发布。

可枚举的判定：每个出现在表达式里的变量必须满足其一——
- manifest `param_hints` 声明了 `enum options`（取全部枚举值）
- 声明了 `{type: int, min, max}`（取 min/max 两端；表达式对该变量单调则两端即极值，非单调则拒绝发布）
- 是 `{type: bool}`（取 0/1）
- 是常量或 `one`

角点组合数上限 **4096**，超出拒绝发布（提示主人拆成多个 SKU）。

**seedance 三变种实测可满足**：`duration {min:4,max:30}` + `resolution` 枚举 2~4 值 + `priority` 4 档 + `has_video_ref` bool = 最多 4×2×30… 不对，`duration` 只取两端，故 2×4×4×2 = 64 个角点。远低于上限。

**这条 gate 是 DSL 能进 v2 的门票**——没有它，`Reserve()` 就拿不到预授权上界，直接违反 SPEC §6.1。

### 4.1 语法（BNF）

```
expr    := ternary
ternary := logical ('?' logical ':' ternary)?
logical := comp (('&&'|'||') comp)*
comp    := arith (('=='|'!='|'<'|'<='|'>'|'>=') arith)?
arith   := term (('+'|'-') term)*
term    := factor (('*'|'/') factor)*
factor  := number | string | ident | call | '(' expr ')'
call    := ident '(' expr (',' expr)* ')'
```

**允许函数**：`tier(name, expr)` `param(name)` `in(v, list...)`
**允许变量**：由 adapter manifest **分层声明**：
- `common`：调用元事实（success / duration_ms / attempt_count / ...）
- `capability`：能力级变量（LLM: `p c cr cc len`；video: `seconds resolution priority`；image: `count width height quality` 等）
- `adapter`：适配器特有变量（seedance: `has_video_ref priority task_mode`；...）

**禁止**：循环、递归、字符串拼接、IO、赋值、`for/while/eval/import`；未在 manifest 声明过的标识符 pre-parse 期直接拒绝。

### 4.2 安全约束

- AST 深度上限：**16**
- AST 节点数上限：**256**
- 单次求值 CPU 时间上限：**10ms**（context.WithTimeout）
- 表达式解析结果缓存（LRU 1024 条），避免每次调用重解析
- Pre-parse 期即拒绝任何非白名单标识符

### 4.3 变量注入契约

Adapter 负责把 usage / 请求参数映射为 DSL 变量。例：

```go
// openai_chat adapter
func (a Adapter) BillingVars(usage Usage, req *Request) map[string]any {
    return map[string]any{
        "p":   usage.PromptTokens,
        "c":   usage.CompletionTokens,
        "cr":  usage.CacheReadInputTokens,
        "cc":  usage.CacheCreationInputTokens,
        "len": req.ContextLength,
    }
}
```

Manifest 声明 `billing_vars: {common: [...], capability: [...], adapter: [p c cr cc len]}`，DSL 解析器**只信任 manifest 里三层任一声明过的变量**，未声明标识符 pre-parse 期直接抛错。

### 4.4 计费引擎最终价公式（⚠️ v5.3 原稿的公式违反 v2 不变量，已改）

**v5.3 原稿写的是**：`final_price = evaluate(sell_expr) × credential_pool.group_ratio`。**这条必须废弃**，三处证据：

- SPEC 不变量 2：「用户售价只由公开 SKU 决定，**不因实际选中的渠道、分组或 Key 改变**」
- SPEC §3.1:63：`gw_credential_pools` 是**上游侧**商业分组，「与 Prism 用户 Token 完全无关」
- SPEC §11:559 明确不采用：「**不使用『上游成本乘倍率』作为用户实时售价**」

凭据池是**选路时**才确定的。乘在售价上意味着同一次调用因为路由到不同池而收不同钱——这正是 v1 时代被明令废除的做法，也让 §4.0.2 的上界证明失效（发请求前算不出上界）。

**改为两条完全独立的链路**：

```
# 上游成本侧：分组倍率合法，因为成本本来就随实际选中的池变
final_cost  = evaluate(cost_rate.pricing_expr,  cost_vars) × credential_pool.cost_group_ratio

# 用户售价侧：只由 SKU 决定，与选路无关
base_price  = evaluate(sell_rate.pricing_expr, sell_vars)
final_price = base_price × subject_discount_ratio
```

`subject_discount_ratio` 挂**下游主体**（`billing_accounts` 或 Token），在**选 SKU 之前**读出并固定到 `gw_api_calls`。这样：
- 主人要的「VIP × 0.45 / 内部 × 0.11」价差照样能做，但它是**客户折扣**，不是上游分组倍率
- 上界证明成立：`max_price = max(base_price 角点) × subject_discount_ratio`，发请求前可算
- 折扣值固定到 Call 行，历史账单可重建

倍率同样是定点十进制（`DECIMAL(18,8)`，SPEC 不变量 10 点名「倍率」在内）。

**M1 范围界定**：M1 只落 `cost_group_ratio`（上游成本侧，无争议）。`subject_discount_ratio` 涉及 `billing_accounts` 与预授权路径，独立成一件事，M3 之后单独评估——**不塞进 M1**。

`pricing_mode='flat'` 时短路，走完全不变的既有路径：`price = unit_price × 按 quantity_step 取整后的 quantity ÷ 10^unit_scale`，`max_quantity` 照旧当预授权量。

### 4.5 组件计算顺序不变（DSL 只替换一步乘法）

固定顺序照旧：**确定数量 → 按 `quantity_step` 向上取整 → 应用 `max_quantity` 上界 → 组件金额量化 → 汇总**。

`expression` 模式插入在「组件金额量化」这一步：把 `unit_price × quantity ÷ 10^unit_scale` 换成 `evaluate(expr, vars)`，然后**用同一个 Decimal 量化规则**收尾。`charge_event` / `quantity_source` / `max_quantity` 语义一字不改。

一条 rate 仍然是**一个组件**，多组件组成一个价（`RateSchedule.Components`，上限 32）。既有组合校验全部保留：组件码不得重复、`event:source` 计量对不得重复、`usage.input_tokens` 不得与 cached/uncached 组件在同一事件共存（cached 是 input 的子集，不是额外收费）。

### 4.6 迁移：老单价 → 表达式

老 `sell_rate.unit_price` 视为 flat 模式，**一行不动**。主人在前端切 expression 模式时，UI 给出由当前 flat 参数生成的等价预填模板（例如 LLM 类：`p * <old_input> / 1000000 + c * <old_output> / 1000000`），主人确认后再改。切换本身走 §5.2 的 fork 路径。

## 5. 后端：运维直改 API

### 5.1 新增 endpoints

**先分两类**——这是 Pre-M1 最重要的结论。v2 里「能不能原地改」不是 API 设计选择，是**表的性质**决定的：

| 类 | 表的性质 | 改法 |
|---|---|---|
| **A 类：运行时可变** | 非 release 作用域，或是唯一豁免的 `gw_offering_runtime_state` | 直接 PATCH + `state_version+1`，立即生效 |
| **B 类：release 内容** | release 作用域内容表，发布后不可改（`lockCatalogDraft` 见 `status!='draft'` 直接 `ErrConflict`） | 必须走 §5.2 的 fork 路径 |

| Endpoint | 类 | 用途 |
|---|---|---|
| `PATCH /admin/unified-gateway/credentials/{id}` | **A** | 白名单：timeout / concurrency / weight / status（禁止 secret） |
| `PATCH /admin/unified-gateway/credential-pools/{id}` | **A** | 改 `cost_group_ratio` / 限额 / status（`config_version+1`） |
| `PATCH /admin/unified-gateway/offerings/{id}/runtime-state` | **A** | `state: active/draining/disabled` + `reason_code`（写 `gw_offering_runtime_state`，**不是** `gw_offerings`） |
| `PATCH /admin/unified-gateway/model-meta/{id}` | **A** | 展示名 / 分组 / 排序（`gw_model_meta` 不参与选路） |
| `POST /admin/unified-gateway/catalog-changes/sell-rate` | **B** | 改售价 unit_price 或 pricing_expr |
| `POST /admin/unified-gateway/catalog-changes/cost-rate` | **B** | 改成本 |
| `POST /admin/unified-gateway/catalog-changes/route-weight` | **B** | 改路由 `priority` / `weight`（在 `gw_routes` 上，不在 offering 上） |
| `POST /admin/unified-gateway/catalog-changes/sku-variant` | **B** | 切 SKU `variant_code` |
| `POST /admin/unified-gateway/catalog-changes/sku-downstream-paths` | **B** | 改下游入口集合 |
| `POST /admin/unified-gateway/catalog-changes/product` | **B** | 改 `vendor_model` / `capability_constraints` |
| `POST /admin/unified-gateway/releases/{id}/rollback` | **B** | 以某个历史 release 的内容 fork 出新 draft 并走同一条链 |

**⚠️ v5.3 原稿两处错误已修**：
- 原稿 `PATCH /admin/unified-gateway/offerings/{id}` → 「改 enabled / weight」。实际 `gw_offerings` **既无 `enabled` 也无 `weight`**（`20260905_130000:126-142`）。「启停」在 `gw_offering_runtime_state.state`（A 类），「权重」在 `gw_routes.weight`（B 类）。一个 endpoint 拆成两个。
- 原稿把改价写成 `PATCH`。改价是 B 类，语义是「生成新 release」而非「修改某行」，故用 `POST /catalog-changes/*`，返回 `{release_id, config_version, activated}`。

**不做**（Prism 独立性边界）：
- ❌ `POST /admin/unified-gateway/upstream/{vendor}/sync`（不镜像上游）
- ❌ `POST /admin/unified-gateway/sku-upstream-bindings`（不做 SKU→上游 catalog 绑定）

### 5.2 B 类改动：fork release（原稿「不走 Draft/Publish」已废弃）

**v5.3 原稿写「直改不走 Release Draft/Publish，原地改行 + state_version+1」。这条走不通**，三层证据：

1. **代码层**：`lockCatalogDraft`（`repository/catalog_admin.go:150-165`）对每次子行写入先 `SELECT ... FOR UPDATE` 父 release，`status != 'draft'` 直接 `ErrConflict`；`advanceCatalogDraft` 在写语句里再断言一次 `WHERE id=? AND status='draft' AND config_version=?`。已发布 release 的子行**没有任何写入通道**。
2. **表结构层**：全部 release 内容表**没有 `updated_at`、没有版本列**——字面上没有列可以 bump。这不是遗漏，是 SPEC §3.2:117 的设计（`config_version` 只保护发布前的并发编辑）。
3. **设计意图层**：SPEC §6 计费规则 #5 一句话就是这条路：「**价格变更只通过新目录发布版生效，不修改历史任务快照。**」

**所以 B 类改动的完整链路是五步**（UI 上仍是一次点击）：

```
① fork:     clone 当前 active release 全部内容 → 新 draft（新 release_no，新 ID 空间）
② edit:     在新 draft 上应用主人的改动（改价 / 改权重 / 切变种…）
③ publish:  CheckCatalogStructure + CheckCatalogPricing + 上界证明 → finalizeCatalogDigest → status='published'
④ prove:    每个实例对新 release 跑一次 prove-current，写 gw_catalog_readiness
⑤ activate: 独占锁 gw_catalog_runtime_state 单行 → 切 active_release_id
```

**④ 是我上一轮判断错、现已纠正的一步**：`catalog_digest.go:24,26` 两条 digest 查询**显式 select `unit_price`**，所以改一个价 → `content_hash` 变 → `catalog_readiness.go:23-24` 要求的 `r.content_hash=c.content_hash` 不再成立 → 激活报 `ErrConflict`。

**但不需要新建部署代次**：`semantic_digest` = `adapter.SemanticDigest()`（只含 adapter 集合与代码摘要，**不含价格**）不变，`g.semantic_digest=c.semantic_digest` 照样成立。单实例生产环境下 ④ 就是服务内部一次自动调用，主人无感。多实例时 ④ 天然变成「所有实例都认这份新目录才切」——正是 v2 设计这套的用意。

### 5.2.1 ⚠️ release clone 必须新写（Pre-M1 发现，影响工时）

**现有代码里没有任何 clone 能力**：
- `CreateCatalogDraft`（`catalog_admin.go:35`）生的是**空草稿**——只有 release 行 + 状态事件，零子行
- 全仓库**没有** `INSERT ... SELECT` 进任何 `gw_*` 目录表（唯一的 `INSERT...SELECT` 在 async outbox）
- 表里**没有** `parent_release / source_release / derived_from` 之类血缘列
- 子行只有 create + delete，**没有 update**（想改一行 = 删了重建）
- 唯一的批量图写入者是单向 V1 导入器，且目标非空就拒跑

所以 M3 要**从零实现一个 14 表的克隆器**，难点在 **ID 重映射**：每张表都是 `AUTO_INCREMENT` 主键 + `(release_id, parent_id)` 复合外键，克隆时必须按拓扑序逐层建立 `旧ID → 新ID` 映射表再插子行。层序（跟 `catalogDigestQueries` 一致）：

```
catalog_models → catalog_model_names, model_operations
model_operations → skus → sku_downstream_paths, sell_rates
channel_transports → transport_allowed_hosts
products + channel_transports → product_transports → product_transport_actions
product_transports → offerings → cost_plans → cost_rates
skus + offerings → routes
```

**克隆器的三条正确性要求**（M3 必须有测试覆盖）：
1. 克隆后立即 `CheckCatalogStructure` + `CheckCatalogPricing` 必须过——克隆不能产出发不出去的 draft
2. 未作任何编辑的克隆，`content_hash` **会与源不同**（digest 含原始 `id` 主键，克隆后 ID 变了）。这是既有实现的性质，不是 bug；`uq_gw_catalog_releases_content_hash` 是 UNIQUE，反而要求它不同
3. rate 行克隆时**必须复用源行的 `rate_evidence_review_event_id`**——`CreateCatalogRate` 从 evidence 读价，不接受调用方传价（`CatalogRateInput` 无价格字段）。只有真正改价的那条 rate 才需要新 evidence

### 5.2.2 改价的 evidence 处理（新开的口子，须显式标注）

改价那一条 rate 走既有两步流程，**不能跳过**：`CreateRateEvidence`（落 `submitted` + `review_state`）→ `ReviewRateEvidence`（→`accepted`）。运维直改把两步放进同一事务连着做完，但**必须诚实标注来源**：

```
source_type   = 'operator_manual'
authority     = 'operator_declared'   ← 明确弱于外部观测证据
reviewer_user_id = 当前管理员
reason_code   = 'console_price_change'
```

**为什么要专门写这一条**：v2 在每个证据边界都拒绝凭空造事实（§1.4 末段）。`operator_manual` + 自动 accept 是 v5 新开的口子——它合理（主人自己定价就是最高权威的**售价**事实源），但**不能伪装成外部核验过的成本事实**。成本侧（`cost_rate`）尤其要留意：那是对上游报价的**推测**，authority 必须标 `operator_declared`，不能标成 `provider_confirmed`。

同一事务同时写：`audit_events(actor, action='catalog_change.sell_rate', diff={before,after})` + `gw_catalog_release_state_events`。

### 5.2.3 A 类改动（原地生效，无 release）

A 类才是真正的「直改」，一个事务三件事：
1. 校验 manifest 硬约束（越界拒绝）
2. 更新目标行 + `state_version`/`config_version` `+1`（乐观锁，客户端须带 `expected_version`）
3. 写对应 `*_state_events` 追加行 + `audit_events`

A 类**不产生 release，不需要 prove/activate，改完立即生效**。这也是为什么 `gw_offering_runtime_state` 被 v2 特意豁免出来——SPEC §3.2:88 说得很清楚：停用只影响**新**选路，已固定的 Attempt 继续跑它自己那份已发布快照。**紧急停用一个上游走 A 类，秒级生效，不用 fork。**

### 5.3 权限模型

- `admin` 角色可用全部 A 类 PATCH 与 B 类 catalog-changes
- **`credentials` 的密钥材料永远无直改通道**，只能走既有的 credential 版本轮换 API（密文在 `encrypted_blobs`，KEK 在 KMS，DB 里从来没有明文）
- 服务层每个改动前读 adapter manifest 校验是否越界（例如 seedance 2.5 `duration` 必须 4~30）
- B 类改动额外要求：调用方带 `expected_active_release_id`，与运行时指针不一致直接拒绝——防止两个管理员同时 fork 出两条分叉

## 6. Adapter Manifest 系统

### 6.1 目录

```
internal/gateway/adapter/
  seedance/
    adapter.go
    manifest.yaml       ← 新增
  openai/
    chat/
      adapter.go
      manifest.yaml
    ...
```

启动时将各 adapter 的 Manifest 注册进内存 registry。当前实现把经过校验的声明编译进
`internal/gateway/adapter/manifest_data.go`，由 `manifest_registry.go` 统一加载；热更新暂不支持
（改 Manifest = 部署新二进制）。若以后改为外置 `manifest.yaml`，必须保持同一 schema、校验和
`SemanticDigest` 约束，不得绕过启动时校验。

### 6.2 Manifest schema（富字段完整版 · 红线 2）

**核心原则**：Manifest 是"这个 adapter 能吃什么、吐什么、按什么计价"的完整声明，**必须富**。禁止砍成"固定 pricing_mode 枚举 + 精简 param_hints"（那是 v5.2 已被撤销的错误方向）。

```yaml
adapter: seedance@1
capability: video
downstream_paths:
  - /v1/videos/generations

# 分层变量声明（DSL 白名单来源）
billing_vars:
  common:
    - {name: success,       desc: 调用是否成功 0/1}
    - {name: duration_ms,   desc: 调用耗时毫秒}
    - {name: attempt_count, desc: 路由尝试次数}
  capability:                          # video 能力通用
    - {name: seconds,    desc: 生成视频秒数}
    - {name: resolution, desc: 输出分辨率枚举}
    - {name: has_audio,  desc: 是否含音频 0/1}
  adapter:                             # seedance 特有
    - {name: has_video_ref, desc: 是否含视频参考素材 0/1}
    - {name: priority,      desc: 优先级档位 1/2/3/4}
    - {name: task_mode,     desc: 任务模式枚举}

billing_funcs: [tier, param, in]       # DSL 允许函数白名单

# 变种（seedance 一 adapter 多渠道）
variants:
  - code: official
    display: 官方满血渠道
    models: [seedance-2.0, seedance-2.0-fast]
    param_hints:                       # 前端表单渲染建议
      resolution:
        type: enum
        options: [480p, 720p, 1080p, 4k]
      duration:       {type: int, min: 5, max: 10}
      generate_audio: {type: bool, default: false}
      last_frame:     {type: bool, allowed: true}
      web_search:     {type: bool, allowed: true}
    ref_media:                         # 素材上限
      image: {max: 3}
      video: {max: 1, item_seconds: [2,15], total_seconds: 15}
      audio: {max: 1, item_seconds: [2,15], total_seconds: 15}
    hard_constraints: []
    pricing_hints:
      default_mode: expression
      example: "seconds * 0.30"

  - code: h_channel
    display: H渠道
    models: [seedance-h-720p, seedance-h-1080p]
    param_hints:
      resolution:
        type: conditional_enum         # 720p/1080p 时长上下界不同
        cases:
          - {value: 720p,  duration: {min: 4, max: 15}}
          - {value: 1080p, duration: {min: 4, max: 8}}
    hard_constraints:
      - {field: last_frame,    must_be: false}
      - {field: web_search,    must_be: false}
    pricing_hints:
      default_mode: flat               # H 渠道按条
      example: "1.20"

  - code: seedance25
    display: Seedance 2.5
    models: [seedance-2.5]
    param_hints:
      task_mode:  {type: enum, options: [references], required: true}
      resolution: {type: enum, options: [480p, 720p]}
      duration:   {type: int, min: 4, max: 30}
    hard_constraints:
      - {field: generate_audio, must_be: false}
      - {field: cancel_allowed, must_be: false}
    pricing_hints:
      default_mode: expression
      example: 'seconds * 0.20 * (priority==4 ? 1.5 : 1) * (has_video_ref==1 ? 1.2 : 1)'
```

**Manifest 的作用**：
1. **前端表单渲染** —— `param_hints` 驱动详情抽屉"映射参数"段
2. **软/硬约束校验** —— 改动层用 `hard_constraints` 拒绝越界，UI 灰化对应字段
3. **DSL 变量白名单** —— `billing_vars` 三层集合是 DSL 允许的标识符范围
4. **变种绑定** —— `variants[].code` 就是 `gw_skus.variant_code` 的取值集合
5. **上界证明的参数域** —— §4.0.2 的角点枚举全部取自 `param_hints`（这也是 `param_hints` 必须富的硬理由：不富就证不出上界，DSL 就发不出去）

### 6.3 Manifest 与既有 Descriptor / Params 的关系（Pre-M1 澄清）

三样东西已经存在，v5 的 Manifest 是**并列扩展**，不替换任何一个：

| 已有 | 位置 | 职责 | v5 关系 |
|---|---|---|---|
| `Descriptor` | `adapter/manifest.go:18` | 安全/审计线：`code + version + protocol + implementation_digest`（摘要来自代码 SHA + BuildRevision），进 `SemanticDigest()` 供部署证明 | **不动**。Manifest 用 `adapter:` 字段引用同一个 `Descriptor.Code` |
| `Params map[string]any` | `adapter/seedance.go:42` | 运行时透传上游任意参数的载荷通道 | **不动**。`param_hints` 是它的**元数据规格**，说明「什么键可以出现、取值范围」 |
| `gw_skus.service_tiers` | `20260906_200000` 迁移 | 已发布 SKU 接受的服务档位（JSON，已进 content digest） | Manifest 是**权威声明**，SKU 列是发布时冻结的镜像。UI 读 manifest 渲染，DSL 的 `param("service_tier")` 校验落到 SKU 列 |

**关键**：Manifest 是**面向业务**的声明（能吃什么参数、按什么计价、前端怎么渲染），`Descriptor` 是**面向安全**的声明（哪份代码在跑）。改 manifest 不改 `SemanticDigest`，所以改 manifest 仍然要重新部署二进制（manifest 编译进二进制，不热更），但**不需要新建部署代次**。

### 6.4 硬约束 vs 软配置

- **硬约束**：写死在 manifest（`hard_constraints` 或 `allowed: false`），前端**灰化 + tooltip**、后端改动层强制拒绝
- **软配置**：manifest 给出默认值和上下界，主人可在允许区间内修改（例如 `duration {min:4,max:30}` 是硬区间，主人可以把某个 SKU 收窄到 4~8，但不能放宽到 40）
- **收窄会缩小上界证明的角点集合**，这是好事：主人收窄区间 → `max_price` 变小 → 预授权占用变少

## 7. Prism 独立性：接入一家新上游的动作清单

**Prism 不镜像上游、不同步上游价格表**。要接一家新上游，动作只有以下 6 步——没有"批量拉清单""按 vendor 同步"这类环节。

### 7.1 接入流程（每家上游一次性动作）

1. **铃兰读上游文档** → 提炼参数元数据 / 硬约束 / 计费单位 / 素材要求
2. **铃兰写 adapter Go 代码** → `internal/gateway/adapter/<name>/adapter.go`（ValidateRequest/BuildRequest/DecodeSubmit/DecodePoll/BillingVars）
3. **铃兰写 manifest.yaml** → 分层 `billing_vars` + `variants[]` + `hard_constraints`
4. **主人跑迁移** → adapter 注册进 registry，产生 `gw_products / gw_operation_contracts / gw_model_operations / gw_skus`
5. **主人在前端配价** → 详情抽屉"计价"段填 `pricing_expr` 或 `unit_price`，切 `pricing_mode`（走 §5.2 fork 链，UI 一次点击）
6. **主人绑定 credential_pool + cost_group_ratio** → 详情抽屉"上游成本分组"段（A 类，原地生效）

**没有第 7 步"从上游拉清单"**。

### 7.2 为什么不做上游同步

| 诱惑 | 现实 |
|---|---|
| "从上游拉一次省得一个个配" | 每家上游的 pricing 结构完全不同，写通用同步器 = 写 N 个特化 driver |
| "同步了主人看价格方便" | 主人只关心 Prism 自己的售价（自主定价），上游价格是 Prism 的成本推测，不需要暴露给主人 |
| "有上游 catalog 表可以选路" | 选路依据是 `gw_offerings + gw_routes + gw_ability_transports`，不依赖上游镜像 |

### 7.3 上游成本记账（cost_rate）如何填

**主人在前端 SKU 详情抽屉的"计价"段直接填 `cost_rate.pricing_expr`**——铃兰接入时把已知上游报价作为**默认建议值**写进 manifest 的 `pricing_hints.example`，主人可以照抄或按实际议价改。**不做后台自动同步**。

## 8. Release 语义：不降级，只把流程藏进 UI

### 8.1 原稿标题「Release 语义降级」误导，已改

原稿写「每次 PATCH 自动 `state_version+1`，无需人工 Draft → Publish」。按 §5.2 的证据，**这在 v2 里做不到，也不该做**——release 内容表没有可 bump 的版本列，`config_version` 只保护发布前的并发编辑。

**真正要降级的是 UI 复杂度，不是数据语义**：

| 维度 | v2 现状 | v5 |
|---|---|---|
| 数据语义 | release 不可变 + 两步发布/激活 | **一字不改** |
| 主人操作 | 手动建 draft → 逐个建子行 → publish → prove → activate（5 步 4 页面） | 详情抽屉改一个数字 → 一次点击，后台跑完 5 步 |
| 失败时 | 停在中间态，主人不知道怎么回去 | 单事务 fork+edit+publish；prove/activate 失败保留 draft，UI 显示「已生成配置 #N，待激活」+ 重试按钮 |

**保留**：`gw_catalog_releases` + `gw_catalog_release_state_events` + `deployment_generations` + `gw_catalog_readiness`，全部照旧。
**UI 变更**：删掉 Draft 编辑器（`CatalogWorkspaceDrawer.tsx` 等 22 个组件），改为「配置历史」页——每个 release 就是时间线上的一条。

**这是 v5 最关键的一次心智对齐**：主人抱怨的是「改价要点 4 层弹窗」，不是「不该有 release」。release 本来就是**为主人服务**的——它让每次改价都能回滚、能重建历史账单、能证明多实例吃的是同一份配置。v5 要做的是把这套机制变成主人看不见的管道，不是拆掉它。

### 8.2 配置历史页（时间线的单位是 release，不是自造的 revision）

原稿说的「revision」在 v2 里没有对应实体——**`gw_catalog_releases` 一行就是一个 revision**。不新造概念，直接用 release：

- 全局时间线，一条 = 一个 release（`release_no` / `status` / `published_at` / `reviewed_by`）
- 每条展开显示：与**上一个 release** 的内容 diff（按 §5.2.1 的 14 表逐表比，`content_hash` 变化定位到具体行）
- A 类改动不产生 release，走另一条并列时间线：`audit_events` + 各表 `*_state_events`
- 回滚 = 以目标 release 的内容 fork 出新 draft → 走同一条五步链（**不是把老 release 重新激活**——`ActivateRelease` 只接受 `published` 状态的 release，历史 release 技术上可再激活，但那样会丢掉「回滚也是一次可审计变更」的事实，故统一走 fork）
- 回滚请求必须携带 `expected_active_release_id` 与新 fork 的 `semantic_version`；运维台自动使用当前生效 release 的语义版本，目标历史 release 的版本不会被隐式继承
- 支持按模型 / 操作人 / 日期筛选

**diff 的实现取巧点**：`catalogDigestQueries` 那 14 条查询本来就是「release 的全部内容」的权威定义，diff 直接复用同一组查询逐表比对，不另写一套读取逻辑。

### 8.3 部署代次保留

`prove-current` / `activate` 保留但简化 UI：主人在"部署代次"页看到当前生效代次 + 一键新建代次。这套机制是**多实例部署时的救命稻草**（记忆里 [[deploy-server]] 说的），不能删。

**改价与部署代次的关系**（§5.2 已证）：改价必须重跑 `prove-current`（`content_hash` 变了），但**不需要新建部署代次**（`semantic_digest` 不含价格）。只有换二进制、加/改 adapter 才需要新代次。所以主人日常改价不会碰到这一页。

## 9. 迁移策略

### 9.1 数据迁移脚本

```
database/migrations/20260910_120000_gateway_pricing_expressions.sql
  列、索引和 CHECK 使用 information_schema + PREPARE 幂等模板；下游入口表使用 CREATE TABLE IF NOT EXISTS
  新增变种、下游入口、表达式计价和成本分组倍率
  - ALTER: gw_credential_pools 加 cost_group_ratio (DECIMAL(18,8) 默认 1.00000000)
           + CHECK cost_group_ratio > 0
  - ALTER: gw_skus 加 variant_code (VARCHAR(64) 默认 'default')
           + INDEX idx_gw_skus_release_variant (release_id, variant_code)
           ⚠️ 不是 (adapter_code, variant_code)——gw_skus 无 adapter_code 列
  - ALTER: gw_sell_rates 加 pricing_mode (VARCHAR(16) 默认 'flat')、pricing_expr (TEXT NULL)、max_price (DECIMAL(38,18) NULL)
           + CHECK pricing_mode IN ('flat','expression')
           + CHECK expression⇔expr/max_price 配对（expression 必须有非负 max_price，flat 必须无 expr/max_price）
  - ALTER: gw_cost_rates 同上三列与两条 CHECK
  - CREATE: gw_sku_downstream_paths (release_id, sku_id, path) 带复合外键，无 enabled 列；CHECK path LIKE '/%'
  - 不需要 UPDATE variant_code / pricing_mode / cost_group_ratio：NOT NULL DEFAULT 已覆盖既有行（SQL 无显式 UPDATE）
database/migrations/20260910_130000_gateway_model_meta_config_version.sql
  使用 information_schema + PREPARE 幂等模板；为模型元数据 A 类编辑增加 config_version
  - ALTER: gw_model_meta 加 config_version (BIGINT UNSIGNED 默认 1)
```

**同一个 M1 里必须一起改的 Go 侧**（否则新列对内容哈希不可见，见 §3.1.1）：
- `internal/gateway/repository/catalog_digest.go` 三条查询加列 + 新增第 15 条 + 域标签升 `v2`
- `internal/migrate/files_test.go` 的托管迁移计数已更新为 94；三份新增迁移分别占索引 91、92、93（其中 93 回填既有 SKU 下游入口）

**迁移不做的事**：不回填任何 `pricing_expr`（老价一律留在 flat 路径）；不动任何已发布 release 的 `content_hash`。

**明确不做**：
- ❌ CREATE gw_upstream_catalogs
- ❌ CREATE gw_sku_upstream_bindings
- ❌ CREATE gw_upstream_vendors

### 9.2 部署顺序

1. 数据库迁移（老二进制兼容：新增列有默认值，老代码不写新列也能跑）
2. 部署 v5 二进制（新代码支持新表 + 编译期 Manifest registry + DSL 引擎）
3. 主人可选：把 seedance 现有 SKU 拆成 3 个 variant（脚本 or 手动 PATCH）
4. 前端切换到新 `/ops-console`，30 天后删除 `/legacy/gateway`

### 9.3 回滚

- 二进制层：保留上一版 `prism.v2-labels.20260910` 作紧急回退
- 数据层：新增列有默认值，老二进制启动无碍
- 前端层：`/legacy/gateway` 隐藏路由 30 天保留

## 10. 未纳入范围（v5 明确不做）

- ❌ 前端 AI 向导 / Copilot 按钮（主人在外部 Claude Code 里用铃兰）
- ❌ OpenAPI spec 生成（暂不必要，铃兰读代码就够）
- ❌ 复合"一键上架"接口（铃兰按 CRUD 顺序调）
- ❌ multi-tenant / SSO（v5 单管理员）
- ❌ webhook 事件订阅（v5 不做外部推送）
- ❌ 图表化调用趋势（Dashboard 现有能力足够）

## 11. 工时估算

| 阶段 | 天数 | 相对 v5.3 变化 |
|---|---|---|
| 数据库迁移 + digest 同步 + Manifest 加载器 + variant_code | 3~4 | — |
| 计费 DSL 后端（Decimal 求值 + AST 白名单 + 沙箱 + LRU + fuzz） | 2~3 | — |
| **DSL 上界证明 gate（角点枚举 + 单调性判定 + 接入 CheckCatalogPricing）** | **1~2** | **新增**（§4.0.2，DSL 的门票） |
| **release clone 器（14 表拓扑序 + ID 重映射 + 正确性测试）** | **3~4** | **新增**（§5.2.1，现有代码零复用） |
| 运维改动 API（4 个 A 类 PATCH + 7 个 B 类 catalog-changes） | 2~3 | — |
| 配置历史 / release 时间线 API | 2 | 原「Release 语义降级」 |
| Adapter manifest 迁移（5 个：seedance/openai_chat/openai_responses/anthropic/gemini） | 2~3 | — |
| 前端运维台单页 + 详情抽屉分段（含 Monaco DSL 编辑器 + A/B 类反馈） | 3~4 | — |
| 前端配置历史页 + 老 22 组件删除 + legacy 兼容路由 | 2 | — |
| 部署 + 回归 + 生产验证 | 2 | — |
| **合计** | **22~29 天（约 5~6 周）** | **+4~6 天** |

**为什么比 v5.3 的 18~23 天涨了**：Pre-M1 通读发现两件原稿以为「已有」或「不需要」的事，实际都要从零写——
1. **release clone 器**：原稿假设改价原地 PATCH 即可，实际 release 内容不可变，必须 fork；而全仓库没有任何克隆能力，`CreateCatalogDraft` 生的是空草稿（§5.2.1）
2. **上界证明 gate**：原稿的 DSL 没有上界证明，直接违反 SPEC §6.1「无法给出上界的预付费 SKU 不得发布」，`Reserve()` 拿不到预授权量（§4.0.2）

**这 4~6 天不是可选项**。砍掉 clone 器 = 改价改不了；砍掉上界 gate = DSL 发不出去。相对 v5.1（21~29 天）基本回到同一量级，差别是 v5.1 那些天花在了「上游同步 driver」这条错路上。

## 12. 风险与备选

| 风险 | 应对 |
|---|---|
| DSL 沙箱漏洞 | AST 白名单 + fuzz 测试 + 生产灰度先只跑 flat 模式，DSL 逐步放量 |
| **clone 器漏表/ID 错映射 → 新 release 少东西** | 克隆后立即跑 `CheckCatalogStructure` + `CheckCatalogPricing`；再加一条「克隆后逐表行数与源相等」断言；表清单以 `catalogDigestQueries` 为单一来源，加测试锁定「digest 查询数 == clone 层数」防以后加表漏改 |
| **prove/activate 失败卡在「已发布未激活」** | 这是 v2 的正常中间态，不是故障：`gw_catalog_runtime_state` 指针没动 = 生产还跑老配置，零影响。UI 显式呈现 + 重试；draft 被弃只能转 `retired`（SPEC 语义），不删 |
| **上界证明把主人的合理表达式判成不可枚举** | 拒绝时必须报出**是哪个变量、缺什么声明**；补 manifest 声明即可通过。非单调表达式给出提示「拆成多个 SKU 或改用 tier()」 |
| **两个管理员同时改价 → 两条分叉 release** | B 类改动强制带 `expected_active_release_id`，与运行时指针不一致直接拒绝（§5.3） |
| seedance 硬约束遗漏 | manifest 迁移期每个 variant 做 5 条真实调用回归 |
| 老 SKU 迁移出错 | 分批：先迁 OpenAI/Anthropic 简单类，seedance 单独一批 |
| 前端一次 PR 太大 | 内部分 3 个子 commit（后端 API / 前端骨架 / 老页删除），一次上线但 review 拆开 |
| 铃兰再次"简化滑坡"越红线 | 见 [[feedback-v5-red-lines]] + [[v2-catalog-why-invariants]] · 每次动 Manifest/DSL/v2 表前逐字答自审三问，答不清停手问主人 |

## 13. 实施计划与当前进度

以下顺序是原始实施计划；当前状态以 §0.2 为准：

0. `Pre-M1` **已完成**：通读 SPEC 全文 + 全部 v2 迁移 + 现有 Go 实现，搞清「为什么会走到这样的设计」，据此修订本文档（结论沉淀为 [[v2-catalog-why-invariants]]）
1. `M1` 数据库迁移（含 digest 同步）+ Manifest 加载器 + DSL 后端 + 上界证明 gate（无 UI 变化，纯后端加能力）
2. `M2` Adapter Manifest 迁移（5 个 adapter：seedance/openai_chat/openai_responses/anthropic/gemini）
3. `M3` release clone 器 + A 类 PATCH + B 类 catalog-changes（curl 可跑通改价全链路）
4. `M4` 前端运维台单页（全宽列表 + 按需详情抽屉 + 分段内容 + A/B 类反馈，替代老"统一网关"）
5. `M5` 前端配置历史页 + 老 22 组件删除 + legacy 兼容路由 + 部署验证

**Milestone 独立可交付**：
- M1 完成 → 后端有能力承接 v5 数据模型，无 UI 变化
- M2 完成 → seedance 三变种可跑，manifest 驱动生效
- M3 完成 → curl 改价可用（fork→publish→prove→activate 全通），"改价难"痛点解决
- M4 完成 → 主人视角 UI 落地
- M5 完成 → 老代码清理 + 上线

**M3 内部再拆两小步**（clone 器是新增的最大块，不一口吃）：M3a 只做 clone 器 + 「克隆后不改任何东西也能 publish/activate」的端到端验证；M3b 才叠加改价编辑与 evidence 处理。

**每个 M 结束后主人验收，不点头不进下一个 M**。铃兰不擅自跨步（[[feedback-v5-red-lines]] 分步交付约束）。

## 14. 变更日志

- 2026-09-10 v5.1：铃兰初稿。基于主人给的 seedance v2.2 文档 + aicost.me pricing 数据挖掘 + 5 轮 v3→v4.5 方案审阅收敛。
- 2026-09-10 v5.2：**已撤销**。铃兰误把"前端简化"扩大到"数据模型简化"，砍了 DSL / Manifest 富字段 / v2 富度。主人当场纠正。
- 2026-09-10 v5.3：本版。修正 v5.2 越红线错误：
  - ✅ 恢复计费 DSL（AST 白名单 + 沙箱），非 5 模式枚举
  - ✅ 恢复 Manifest 富字段（分层 `billing_vars` + 完整 `variants[]` + `param_hints/hard_constraints/ref_media/pricing_hints`）
  - ✅ 明确 v2 骨架 100% 保留（不删任何 v2 表）
  - ✅ 删除 `gw_upstream_catalogs / gw_sku_upstream_bindings / gw_upstream_vendors` 及 aicost sync driver（Prism 独立不镜像）
  - ✅ 删除前端"上游"菜单页
  - ✅ 建立 [[feedback-v5-red-lines]] 红线记忆，改动前必答自审三问
- 2026-09-10 v5.4：本版。**Pre-M1 通读 SPEC 全文 + 全部 v2 迁移 + 现有 Go 实现后的修订**（主人要求「先知道为什么会走到这样的设计，然后才能给出合理的文档」）。红线三条全部维持，改的都是与 v2 事实冲突的地方：
  - 🔴 **§4.4 废弃 `× credential_pool.group_ratio` 售价公式**——违反 SPEC 不变量 2 + §11 明确不采用「上游成本乘倍率作售价」。倍率拆成上游成本侧 `cost_group_ratio`（保留）与下游客户折扣 `subject_discount_ratio`（挂主体，选 SKU 前定，M1 不做）
  - 🔴 **§5.2 废弃「直改不走 Draft/Publish」**——`lockCatalogDraft` 对已发布 release 直接 `ErrConflict`，内容表连版本列都没有。改为 fork 五步链（clone→edit→publish→prove→activate），UI 一次点击
  - 🔴 **§5.1 拆 A/B 两类**——`gw_offerings` 既无 `enabled` 也无 `weight`；启停在 `gw_offering_runtime_state`（A 类原地改），权重在 `gw_routes`（B 类走 fork）
  - 🔴 **§3.1 修不存在的索引**——`gw_skus` 无 `adapter_code` 列，原稿 `idx_variant(adapter_code, variant_code)` 建不出来，改 `(release_id, variant_code)`
  - ➕ **§1.4 补三根因**（可证明上界 / 可解释数量 / 禁浮点），这是 `rate_components` 6 字段的存在理由
  - ➕ **§4.0 正面回答 SPEC「不建设规则 DSL」**——DSL 只做价格算术，不进选择器、不决定何时收费
  - ➕ **§4.0.2 新增上界证明 gate**（角点枚举 ≤4096），这是 DSL 能进 v2 的门票
  - ➕ **§3.1.1 新增 digest 同步要求**——`content_hash` 逐列 hash，新列不进 digest 就对内容哈希不可见
  - ➕ **§5.2.1 标注 release clone 器需从零实现**（全仓库零克隆能力），工时 +3~4 天
  - ➕ **§5.2 纠正「改价无需重证部署」**——digest 含 `unit_price`，readiness 比 `content_hash`，故必须 prove；但 `semantic_digest` 不含价，**无需新建部署代次**
  - ➕ **§6.3 澄清 Manifest 与既有 `Descriptor` / `Params` / `service_tiers` 的并列关系**（三者都已存在，均不替换）
  - ➕ **§2.2.1 A/B 两类改动的 UI 反馈必须可区分**（主人是技术型运维，不藏）
  - 修正记忆里「`adapter/` 是空目录」的假事实；新增 [[v2-catalog-why-invariants]]

### 版本对照
| 维度 | v5.1 | v5.2（已撤销） | v5.3 | **v5.4（本版）** |
|---|---|---|---|---|
| 上游镜像 | 有 sync driver + 表 | 无 | 无 | **无** |
| Manifest 富度 | 富 + 分层 | 精简 | 富 + 分层 | **富 + 分层**（+ 明确它是上界证明的参数域） |
| 计费 DSL | 有 + 沙箱 | 无（5 模式枚举） | 有 + 沙箱 | **有 + 沙箱 + Decimal + 上界证明 gate** |
| variant 变种 | 有 | 有 | 有 | **有**（索引改 `(release_id, variant_code)`） |
| v2 骨架 | 保留 | 保留 | 保留 | **保留 + 尊重其不可变语义** |
| 改价路径 | 未细化 | 未细化 | 原地 PATCH ❌ | **fork release 五步链 ✅** |
| 倍率归属 | 未细化 | 未细化 | 乘 credential_pool ❌ | **成本侧池倍率 + 售价侧主体折扣 ✅** |
| aicost 特化 | 有 driver 代码 | 无 | 无 | **无** |
| 工时 | 21~29 天 | 15~19 天 | 18~23 天 | **22~29 天** |
