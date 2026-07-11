-- Reason: route Chat/Responses requests only to endpoints that support every requested feature.
-- Requirement: persist independently editable capabilities for each gateway ability.
-- Impact: adds one nullable JSON column to gw_abilities; existing rows keep protocol defaults.
ALTER TABLE gw_abilities
    ADD COLUMN capabilities JSON NULL AFTER output_price;
