-- Initialize the two environment-backed keyrings required by the unified
-- gateway. Readiness still requires each deployment member to prove that the
-- referenced keys are present and usable before activation.
INSERT INTO `crypto_keyring_state` (`purpose`,`current_version`,`created_at`,`updated_at`)
VALUES
    ('gateway-credential',1,UTC_TIMESTAMP(3),UTC_TIMESTAMP(3)),
    ('gateway-payload',1,UTC_TIMESTAMP(3),UTC_TIMESTAMP(3))
ON DUPLICATE KEY UPDATE `purpose`=VALUES(`purpose`);

INSERT INTO `crypto_key_versions`
    (`keyring_id`,`key_version`,`status`,`provider_key_ref`,`algorithm`,`created_at`)
SELECT `id`,`current_version`,'current',
       CASE `purpose`
           WHEN 'gateway-credential' THEN 'env:PRISM_GATEWAY_KEK_B64'
           WHEN 'gateway-payload' THEN 'env:PRISM_GATEWAY_PAYLOAD_KEK_B64'
       END,
       'aes-256-gcm',UTC_TIMESTAMP(3)
FROM `crypto_keyring_state`
WHERE `purpose` IN ('gateway-credential','gateway-payload')
ON DUPLICATE KEY UPDATE `keyring_id`=VALUES(`keyring_id`);
