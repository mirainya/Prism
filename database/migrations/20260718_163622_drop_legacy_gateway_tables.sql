-- Reason: the pre-Gateway-V2 credential subsystem is no longer referenced by Prism.
-- Requirement: remove retired credential data and the one-time gw_channel_keys backup table.
-- Impact: permanently deletes gateway_credentials, gateway_request_logs, and gw_channel_keys_bak_20260716.
-- Deployment: confirm the application no longer runs a pre-Gateway-V2 binary before applying.

DROP TABLE IF EXISTS `gateway_request_logs`;
DROP TABLE IF EXISTS `gateway_credentials`;
DROP TABLE IF EXISTS `gw_channel_keys_bak_20260716`;
