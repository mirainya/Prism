-- Restore the host authorization facts omitted by the first legacy catalog import.
-- Only the still-draft legacy-import-1 release is eligible. The protocol, host,
-- and port are derived from each transport base URL; malformed URLs remain
-- visible to catalog validation instead of producing guessed authorization data.

INSERT INTO `gw_transport_allowed_hosts`
    (`release_id`, `channel_transport_id`, `protocol`, `host_pattern`, `port`, `created_at`)
SELECT parsed.`release_id`,
       parsed.`channel_transport_id`,
       parsed.`protocol`,
       parsed.`host_pattern`,
       parsed.`port`,
       parsed.`created_at`
FROM (
    SELECT parts.*,
           CASE
               WHEN parts.`port_text` <> ''
                   AND parts.`port_text` REGEXP '^[0-9]+$'
                   AND CHAR_LENGTH(TRIM(LEADING '0' FROM parts.`port_text`)) <= 5
                   THEN CAST(parts.`port_text` AS UNSIGNED)
               WHEN parts.`port_text` <> '' THEN 0
               WHEN parts.`protocol` = 'http' THEN 80
               WHEN parts.`protocol` = 'https' THEN 443
               ELSE 0
           END AS `port`
    FROM (
        SELECT scoped.*,
               LOWER(TRIM(TRAILING '.' FROM CASE
                   WHEN LEFT(scoped.`authority`, 1) = '['
                       THEN SUBSTRING_INDEX(SUBSTRING(scoped.`authority`, 2), ']', 1)
                   ELSE SUBSTRING_INDEX(scoped.`authority`, ':', 1)
               END)) AS `host_pattern`,
               CASE
                   WHEN LEFT(scoped.`authority`, 1) = '['
                       AND LOCATE(']:', scoped.`authority`) > 0
                       THEN SUBSTRING(scoped.`authority`, LOCATE(']:', scoped.`authority`) + 2)
                   WHEN LEFT(scoped.`authority`, 1) <> '['
                       AND scoped.`authority` REGEXP ':[0-9]+$'
                       THEN SUBSTRING_INDEX(scoped.`authority`, ':', -1)
                   ELSE ''
               END AS `port_text`
        FROM (
            SELECT ct.`release_id`,
                   ct.`id` AS `channel_transport_id`,
                   LOWER(SUBSTRING_INDEX(ct.`base_url`, '://', 1)) AS `protocol`,
                   ct.`created_at`,
                   SUBSTRING_INDEX(
                       SUBSTRING_INDEX(
                           SUBSTRING_INDEX(
                               SUBSTRING(ct.`base_url`, LOCATE('://', ct.`base_url`) + 3),
                               '/', 1
                           ),
                           '?', 1
                       ),
                       '#', 1
                   ) AS `authority`
            FROM `gw_channel_transports` ct
            JOIN `gw_catalog_releases` r
              ON r.`id` = ct.`release_id`
             AND r.`status` = 'draft'
             AND r.`semantic_version` = 'legacy-import-1'
            WHERE LOCATE('://', ct.`base_url`) > 0
        ) scoped
    ) parts
) parsed
WHERE parsed.`protocol` IN ('http', 'https')
  AND (
      (
          LEFT(parsed.`authority`, 1) <> '['
          AND parsed.`authority` REGEXP '^[A-Za-z0-9.-]+(:[0-9]+)?$'
      )
      OR
      (
          LEFT(parsed.`authority`, 1) = '['
          AND LOCATE(']', parsed.`authority`) > 1
          AND SUBSTRING(parsed.`authority`, 2, LOCATE(']', parsed.`authority`) - 2)
              REGEXP '^[0-9A-Fa-f:.]+$'
          AND (
              SUBSTRING(parsed.`authority`, LOCATE(']', parsed.`authority`) + 1) = ''
              OR SUBSTRING(parsed.`authority`, LOCATE(']', parsed.`authority`) + 1)
                  REGEXP '^:[0-9]+$'
          )
      )
  )
  AND parsed.`host_pattern` <> ''
  AND parsed.`port` BETWEEN 1 AND 65535
  AND NOT EXISTS (
      SELECT 1
      FROM `gw_transport_allowed_hosts` existing
      WHERE existing.`release_id` = parsed.`release_id`
        AND existing.`channel_transport_id` = parsed.`channel_transport_id`
        AND existing.`protocol` = parsed.`protocol`
        AND existing.`host_pattern` = parsed.`host_pattern`
        AND existing.`port` = parsed.`port`
  );
