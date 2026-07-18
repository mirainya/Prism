-- 变更原因: gw_channel_keys 表残留了 AIFile 模型的孤儿列。
--   生产曾跑过一个未入库的中间二进制, 其 AIFile.TableName() 返回 "gw_channel_keys",
--   GORM AutoMigrate 据此把 AIFile 的 7 个文件列 (NOT NULL 无默认值) 加到了该表上
--   (表上残留的 idx_ai_files_user/token/purpose 索引即铁证)。
--   现有代码 AIFile.TableName()="ai_files" 已独立建表, 但 GORM AutoMigrate 从不删列,
--   生产库残留这些列导致新建渠道 key 报 "Field 'user_id' doesn't have a default value"。
--   已核实: 13 行渠道 key 在这 7 列全为空, ai_files 表 0 行, 删列零数据损失。
-- 相关需求: 修复渠道添加 key 报错 Error 1364 (HY000)
-- 影响范围: 仅 gw_channel_keys 表, 删除 7 个孤儿列及其单列索引。
--   真实渠道 key 数据 (id/channel_id/name/api_key/weight/status/max_conc/current_conc) 不受影响。
--   AIFile 真实数据在独立的 ai_files 表, 不涉及。
-- 注: MySQL DROP COLUMN 会自动删除该列上的单列索引
--   (idx_ai_files_user/idx_ai_files_token/idx_ai_files_purpose)。

ALTER TABLE gw_channel_keys
  DROP COLUMN user_id,
  DROP COLUMN token_id,
  DROP COLUMN filename,
  DROP COLUMN purpose,
  DROP COLUMN bytes,
  DROP COLUMN mime_type,
  DROP COLUMN content;
