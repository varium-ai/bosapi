-- Add organization-level security inspection toggles and the global mode default.
ALTER TABLE organizations
  ADD COLUMN security_inspection_upstream_enabled tinyint(1) NOT NULL DEFAULT 1,
  ADD COLUMN security_inspection_downstream_enabled tinyint(1) NOT NULL DEFAULT 1;

-- Preserve an explicit existing choice when upgrading.
INSERT IGNORE INTO settings (setting_key, value_text, is_secret, updated_at)
VALUES ('security.inspection_mode', 'observe', 0, UTC_TIMESTAMP(3));
