-- IP summaries read only these columns. Avoid fetching every matching request
-- row (and its unrelated metadata) while counting all historical IP activity.
ALTER TABLE usage_logs
  ADD INDEX idx_usage_key_ip_summary (api_key_id, client_ip, organization_id, created_at),
  ALGORITHM=INPLACE, LOCK=NONE;
ALTER TABLE usage_logs_v2
  ADD INDEX idx_usage_key_ip_summary (api_key_id, client_ip, organization_id, created_at),
  ALGORITHM=INPLACE, LOCK=NONE;
