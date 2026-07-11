-- 变更原因: 支持聊天模型「思考模式」配置化(档位数量/取值由每个模型自带,非全局固定枚举)
-- 相关需求: 不同模型思考档位差异大(火山5档/Gemini2档/DeepSeek多档/Claude用token预算),
--           档位必须是模型数据而非代码常量;支持模型默认档 + 会话/请求覆盖 + 管理员锁定
-- 字段说明:
--   thinking_config  JSON。为空=该模型不支持思考(不显示选择器/不注入参数,向后兼容)。
--     结构: {
--       "locked": false,              // true=禁止会话/请求覆盖,强制用 default
--       "default": "medium",          // 默认选中的档位 value
--       "options": [                  // 该模型支持的档位列表,数量任意
--         {"label":"关闭","value":"off","body":{"reasoning":{"effort":"minimal"}}},
--         {"label":"中",  "value":"medium","body":{"reasoning":{"effort":"medium"}}}
--       ]
--     }
--     每个 option 的 body 是"要合并进上游请求体的原始 JSON",字段名已按协议写好,后端零翻译。
--     body 留空 {} = 该档不注入任何参数(厂商默认)。
-- 影响范围: 仅 models 表新增一列,无数据变更;未配置的模型行为与改动前完全一致
-- 回滚: ALTER TABLE models DROP COLUMN thinking_config;

ALTER TABLE models
  ADD COLUMN thinking_config JSON COMMENT '思考模式配置(档位/默认/锁定,空=不支持思考)';
