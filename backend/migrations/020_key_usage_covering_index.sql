-- Cover token/amount aggregation and force bounded per-Key time ranges in readers.
ALTER TABLE usage_logs
  ADD INDEX idx_usage_key_time_totals (api_key_id, created_at, total_tokens, cost_amount),
  ALGORITHM=INPLACE, LOCK=NONE;
