-- Rollback removes the new inspection toggles.
ALTER TABLE organizations
  DROP COLUMN security_inspection_upstream_enabled,
  DROP COLUMN security_inspection_downstream_enabled;
