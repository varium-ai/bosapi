-- Private reusable API key settings. Credentials are never stored in templates.
CREATE TABLE IF NOT EXISTS api_key_templates (
    id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY,
    created_by bigint unsigned NOT NULL,
    name varchar(100) NOT NULL,
    settings json NOT NULL,
    created_at datetime(3) NOT NULL,
    updated_at datetime(3) NOT NULL,
    KEY idx_api_key_templates_creator (created_by, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
