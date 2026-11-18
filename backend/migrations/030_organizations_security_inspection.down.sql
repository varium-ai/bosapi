-- Rollback removes the new toggles and the global inspection mode row.
ALTER TABLE organizations
  DROP COLUMN security_inspection_upstream_enabled,
  DROP COLUMN security_inspection_downstream_enabled;

DELETE FROM settings WHERE setting_key = 'security.inspection_mode';
