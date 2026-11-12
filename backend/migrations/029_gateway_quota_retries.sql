-- Durable outbox for gateway quota settlements that could not be committed
-- after the upstream request completed. Rows are removed only after the
-- idempotent usage/quota transaction succeeds.
CREATE TABLE gateway_quota_retries (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  request_id VARCHAR(128) NOT NULL,
  usage_id VARCHAR(64) NOT NULL,
  organization_id BIGINT UNSIGNED NOT NULL,
  api_key_id BIGINT UNSIGNED NOT NULL,
  model VARCHAR(191) NOT NULL,
  endpoint VARCHAR(128) NOT NULL,
  status_code SMALLINT UNSIGNED NOT NULL,
  error_code VARCHAR(64) NOT NULL DEFAULT '',
  input_tokens BIGINT UNSIGNED NOT NULL DEFAULT 0,
  cache_read_tokens BIGINT UNSIGNED NOT NULL DEFAULT 0,
  cache_write_tokens BIGINT UNSIGNED NOT NULL DEFAULT 0,
  cache_read_cost_amount DECIMAL(20,8) NOT NULL DEFAULT 0,
  cache_write_cost_amount DECIMAL(20,8) NOT NULL DEFAULT 0,
  output_tokens BIGINT UNSIGNED NOT NULL DEFAULT 0,
  total_tokens BIGINT UNSIGNED NOT NULL DEFAULT 0,
  cost_amount DECIMAL(20,8) NOT NULL DEFAULT 0,
  client_ip VARBINARY(16) NULL,
  is_stream TINYINT(1) NOT NULL DEFAULT 0,
  duration_ms INT UNSIGNED NOT NULL DEFAULT 0,
  first_token_ms INT UNSIGNED NULL,
  created_at DATETIME(3) NOT NULL,
  UNIQUE KEY uq_gateway_quota_retry (request_id, usage_id),
  KEY idx_gateway_quota_retry_org (organization_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
