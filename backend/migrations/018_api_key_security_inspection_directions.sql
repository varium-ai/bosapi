ALTER TABLE api_keys
  ADD COLUMN security_inspection_upstream_enabled tinyint(1) NOT NULL DEFAULT 1,
  ADD COLUMN security_inspection_downstream_enabled tinyint(1) NOT NULL DEFAULT 1;

UPDATE api_keys
SET
  security_inspection_upstream_enabled = security_inspection_enabled,
  security_inspection_downstream_enabled = security_inspection_enabled;

ALTER TABLE api_keys DROP COLUMN security_inspection_enabled;
