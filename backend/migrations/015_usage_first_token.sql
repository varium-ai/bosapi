-- Missing timing remains NULL so historical requests are not reported as instant.
ALTER TABLE usage_logs
    ADD COLUMN first_token_ms int unsigned NULL AFTER duration_ms;
