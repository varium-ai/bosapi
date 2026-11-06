-- Explicit rollback removes first-token timings without changing request counters.
ALTER TABLE usage_logs
    DROP COLUMN first_token_ms;
