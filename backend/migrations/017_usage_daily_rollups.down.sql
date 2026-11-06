ALTER TABLE usage_logs DROP KEY idx_usage_rollup_pending, DROP COLUMN rollup_applied;
DROP TABLE usage_daily_rollups;
