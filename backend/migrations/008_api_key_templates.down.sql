-- Explicit rollback for 008_api_key_templates.sql; the forward runner ignores down files.
DROP TABLE IF EXISTS api_key_templates;
