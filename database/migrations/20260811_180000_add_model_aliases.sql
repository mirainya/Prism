-- Reason: allow upstream model names to be configured without provider-specific routing code.
-- Requirement: resolve an incoming model name through the canonical Prism model definition.
-- Impact: adds a nullable JSON array to models; existing rows remain unchanged.
-- Deployment: metadata-only ALTER TABLE; no endpoint or request data is rewritten.

ALTER TABLE `models`
  ADD COLUMN `aliases` JSON NULL COMMENT 'upstream model aliases' AFTER `features`;
