ALTER TABLE usage_logs_v2 DROP INDEX idx_usage_key_pending_time, ALGORITHM=INPLACE, LOCK=NONE;
ALTER TABLE usage_logs DROP INDEX idx_usage_key_pending_time, ALGORITHM=INPLACE, LOCK=NONE;
