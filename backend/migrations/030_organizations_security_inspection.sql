-- Add organization-level security inspection toggles.
ALTER TABLE organizations
  ADD COLUMN security_inspection_upstream_enabled tinyint(1) NOT NULL DEFAULT 1,
  ADD COLUMN security_inspection_downstream_enabled tinyint(1) NOT NULL DEFAULT 1;
