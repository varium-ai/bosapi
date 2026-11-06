-- Rebuild the explicit views first, then remove the cache fields.
CREATE OR REPLACE VIEW usage_logs_read AS
SELECT id, request_id, usage_id, user_id, organization_id, api_key_id, model, upstream_model, endpoint, status_code, error_code,
  input_tokens, output_tokens, total_tokens, cost_amount, client_ip, is_stream, duration_ms, first_token_ms, created_at, rollup_applied
FROM usage_logs_v2
UNION ALL
SELECT id, request_id, usage_id, user_id, organization_id, api_key_id, model, upstream_model, endpoint, status_code, error_code,
  input_tokens, output_tokens, total_tokens, cost_amount, client_ip, is_stream, duration_ms, first_token_ms, created_at, rollup_applied
FROM usage_logs;

CREATE OR REPLACE VIEW usage_logs_pending AS
SELECT id, request_id, usage_id, user_id, organization_id, api_key_id, model, upstream_model, endpoint, status_code, error_code,
  input_tokens, output_tokens, total_tokens, cost_amount, client_ip, is_stream, duration_ms, first_token_ms, created_at, rollup_applied
FROM usage_logs_v2
WHERE rollup_applied = 0
UNION ALL
SELECT id, request_id, usage_id, user_id, organization_id, api_key_id, model, upstream_model, endpoint, status_code, error_code,
  input_tokens, output_tokens, total_tokens, cost_amount, client_ip, is_stream, duration_ms, first_token_ms, created_at, rollup_applied
FROM usage_logs
WHERE rollup_applied = 0;

ALTER TABLE usage_daily_rollups
  DROP COLUMN cache_write_cost_amount,
  DROP COLUMN cache_read_cost_amount,
  DROP COLUMN cache_write_tokens,
  DROP COLUMN cache_read_tokens;

ALTER TABLE usage_logs_v2
  DROP COLUMN cache_write_cost_amount,
  DROP COLUMN cache_read_cost_amount,
  DROP COLUMN cache_write_tokens,
  DROP COLUMN cache_read_tokens;

ALTER TABLE usage_logs
  DROP COLUMN cache_write_cost_amount,
  DROP COLUMN cache_read_cost_amount,
  DROP COLUMN cache_write_tokens,
  DROP COLUMN cache_read_tokens;
