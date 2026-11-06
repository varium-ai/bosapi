-- Setup quota probes read only a Key's unacknowledged tail, even during a
-- historical rollup backlog. Cover sums without scanning acknowledged history.
ALTER TABLE usage_logs
  ADD INDEX idx_usage_key_pending_time (api_key_id, rollup_applied, created_at, total_tokens, cost_amount),
  ALGORITHM=INPLACE, LOCK=NONE;
ALTER TABLE usage_logs_v2
  ADD INDEX idx_usage_key_pending_time (api_key_id, rollup_applied, created_at, total_tokens, cost_amount),
  ALGORITHM=INPLACE, LOCK=NONE;
