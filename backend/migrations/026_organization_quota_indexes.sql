-- Organization quota probes read only a tenant's unacknowledged tail, even
-- during historical rollup backfills. Cover sums without scanning acknowledged
-- history or every API Key in the tenant.
ALTER TABLE usage_logs
  ADD INDEX idx_usage_org_pending_time (organization_id, rollup_applied, created_at, total_tokens, cost_amount),
  ALGORITHM=INPLACE, LOCK=NONE;
ALTER TABLE usage_logs_v2
  ADD INDEX idx_usage_org_pending_time (organization_id, rollup_applied, created_at, total_tokens, cost_amount),
  ALGORITHM=INPLACE, LOCK=NONE;
