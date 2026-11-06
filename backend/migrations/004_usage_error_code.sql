ALTER TABLE usage_logs
    ADD COLUMN error_code varchar(64) NOT NULL DEFAULT '' AFTER status_code;
