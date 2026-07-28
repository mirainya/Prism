-- 变更原因: 端点 13 与 35 同为 interaction_mode='stream'，但 supports_stream 一个 0 一个 1，配置不一致。
--           实际是否走流式由 provider/factory.go 按 interaction_mode 判定，supports_stream 在能力路径不参与决策，
--           故本次仅为消除配置歧义，不改变运行时行为。
-- 相关需求: 排查 gpt-image-2 流式端点 response_format=url 报错时发现的配置不一致（真因是 SSE 解析器不读 url，已在代码侧修复）。
-- 影响范围: endpoints 表中 interaction_mode='stream' 且 supports_stream=0 的行（现网为 id=13 一行）。

UPDATE `endpoints`
SET `supports_stream` = 1,
    `updated_at` = CURRENT_TIMESTAMP(3)
WHERE `interaction_mode` = 'stream'
  AND `supports_stream` = 0
  AND `deleted_at` IS NULL;
