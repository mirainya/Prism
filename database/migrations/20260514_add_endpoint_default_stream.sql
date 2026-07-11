-- 为 endpoints 表添加 default_stream 字段
-- 默认值与 supports_stream 保持一致
ALTER TABLE endpoints ADD COLUMN default_stream TINYINT(1) NOT NULL DEFAULT 1 COMMENT '默认使用流式' AFTER supports_stream;
UPDATE endpoints SET default_stream = supports_stream;
