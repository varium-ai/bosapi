-- Identities and credentials have independent tenant lifecycles. Historical
-- usage/log attribution is intentionally left unchanged.
ALTER TABLE users MODIFY organization_id bigint unsigned NULL;
UPDATE users SET organization_id = NULL WHERE role = 'super_admin';
ALTER TABLE api_keys MODIFY user_id bigint unsigned NULL;
UPDATE api_keys SET user_id = NULL;
ALTER TABLE api_keys ADD allowed_provider_ids json NULL, ADD allowed_models json NULL;
