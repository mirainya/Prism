-- 变更原因: 图片流经 Cloudflare 时可能在上游排队期间被中断。
-- 相关需求: 图片渠道改用已解析到源站、由 nginx 直接提供服务的域名。
-- 影响范围: 仅更新 sub2API-MiraiNya 图片渠道的上游地址。

UPDATE `channels`
SET
  `base_url` = 'https://image.mirainya.com',
  `updated_at` = CURRENT_TIMESTAMP(3)
WHERE `type` = 'sub2API-MiraiNya'
  AND `base_url` = 'https://sub2api.mirainya.com'
  AND `deleted_at` IS NULL;
