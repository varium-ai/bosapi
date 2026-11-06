-- Request-level prompt-cache accounting fields. Existing records keep 0.
ALTER TABLE usage_logs
  ADD COLUMN cache_read_tokens      bigint unsigned NOT NULL DEFAULT 0 AFTER input_tokens,
  ADD COLUMN cache_write_tokens     bigint unsigned NOT NULL DEFAULT 0 AFTER cache_read_tokens,
  ADD COLUMN cache_read_cost_amount  decimal(20,8) NOT NULL DEFAULT 0 AFTER cache_write_tokens,
  ADD COLUMN cache_write_cost_amount decimal(20,8) NOT NULL DEFAULT 0 AFTER cache_read_cost_amount;

ALTER TABLE usage_logs_v2
  ADD COLUMN cache_read_tokens      bigint unsigned NOT NULL DEFAULT 0 AFTER input_tokens,
  ADD COLUMN cache_write_tokens     bigint unsigned NOT NULL DEFAULT 0 AFTER cache_read_tokens,
  ADD COLUMN cache_read_cost_amount  decimal(20,8) NOT NULL DEFAULT 0 AFTER cache_write_tokens,
  ADD COLUMN cache_write_cost_amount decimal(20,8) NOT NULL DEFAULT 0 AFTER cache_read_cost_amount;

-- The union views enumerate columns explicitly; expand them together.
CREATE OR REPLACE VIEW usage_logs_read AS
SELECT id, request_id, usage_id, user_id, organization_id, api_key_id, model, upstream_model, endpoint, status_code, error_code,
  input_tokens, cache_read_tokens, cache_write_tokens, cache_read_cost_amount, cache_write_cost_amount,
  output_tokens, total_tokens, cost_amount, client_ip, is_stream, duration_ms, first_token_ms, created_at, rollup_applied
FROM usage_logs_v2
UNION ALL
SELECT id, request_id, usage_id, user_id, organization_id, api_key_id, model, upstream_model, endpoint, status_code, error_code,
  input_tokens, cache_read_tokens, cache_write_tokens, cache_read_cost_amount, cache_write_cost_amount,
  output_tokens, total_tokens, cost_amount, client_ip, is_stream, duration_ms, first_token_ms, created_at, rollup_applied
FROM usage_logs;

CREATE OR REPLACE VIEW usage_logs_pending AS
SELECT id, request_id, usage_id, user_id, organization_id, api_key_id, model, upstream_model, endpoint, status_code, error_code,
  input_tokens, cache_read_tokens, cache_write_tokens, cache_read_cost_amount, cache_write_cost_amount,
  output_tokens, total_tokens, cost_amount, client_ip, is_stream, duration_ms, first_token_ms, created_at, rollup_applied
FROM usage_logs_v2
WHERE rollup_applied = 0
UNION ALL
SELECT id, request_id, usage_id, user_id, organization_id, api_key_id, model, upstream_model, endpoint, status_code, error_code,
  input_tokens, cache_read_tokens, cache_write_tokens, cache_read_cost_amount, cache_write_cost_amount,
  output_tokens, total_tokens, cost_amount, client_ip, is_stream, duration_ms, first_token_ms, created_at, rollup_applied
FROM usage_logs
WHERE rollup_applied = 0;

-- Aggregates use a wider decimal because many requests can be summed.
ALTER TABLE usage_daily_rollups
  ADD COLUMN cache_read_tokens       bigint unsigned NOT NULL DEFAULT 0 AFTER input_tokens,
  ADD COLUMN cache_write_tokens      bigint unsigned NOT NULL DEFAULT 0 AFTER cache_read_tokens,
  ADD COLUMN cache_read_cost_amount  decimal(38,8) NOT NULL DEFAULT 0 AFTER cache_write_tokens,
  ADD COLUMN cache_write_cost_amount decimal(38,8) NOT NULL DEFAULT 0 AFTER cache_read_cost_amount;
