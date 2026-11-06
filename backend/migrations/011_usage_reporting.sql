-- No installation identity or enabled setting is created by this migration.
CREATE TABLE IF NOT EXISTS usage_reporting_state (
    id TINYINT UNSIGNED NOT NULL PRIMARY KEY,
    installation_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
    collection_epoch CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '',
    enabled_since DATETIME(6) NULL,
    config_revision BIGINT UNSIGNED NOT NULL DEFAULT 0,
    schedule_owner CHAR(36) NOT NULL DEFAULT '',
    schedule_token CHAR(36) NOT NULL DEFAULT '',
    schedule_until DATETIME(6) NULL,
    next_scan_at DATETIME(6) NULL,
    scan_cursor DATETIME(6) NULL,
    coverage_since DATETIME(6) NULL,
    last_success_at DATETIME(6) NULL,
    last_failure_at DATETIME(6) NULL,
    last_error VARCHAR(64) NOT NULL DEFAULT '',
    dropped_count BIGINT UNSIGNED NOT NULL DEFAULT 0
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
INSERT IGNORE INTO usage_reporting_state (id) VALUES (1);

CREATE TABLE IF NOT EXISTS usage_reporting_outbox (
    report_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL PRIMARY KEY,
    installation_id CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    collection_epoch CHAR(36) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    report_kind VARCHAR(16) NOT NULL,
    window_key VARCHAR(10) NOT NULL,
    payload TEXT NULL,
    payload_hash CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    status VARCHAR(16) NOT NULL,
    attempts INT UNSIGNED NOT NULL DEFAULT 0,
    next_attempt_at DATETIME(6) NOT NULL,
    lease_owner CHAR(36) NOT NULL DEFAULT '',
    lease_token CHAR(36) NOT NULL DEFAULT '',
    lease_until DATETIME(6) NULL,
    created_at DATETIME(6) NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    UNIQUE KEY uq_usage_reporting_window (installation_id, collection_epoch, report_kind, window_key),
    KEY idx_usage_reporting_due (status, next_attempt_at),
    KEY idx_usage_reporting_expiry (expires_at),
    KEY idx_usage_reporting_created (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS usage_reporting_events (
    id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
    action VARCHAR(32) NOT NULL,
    actor_id BIGINT UNSIGNED NULL,
    created_at DATETIME(6) NOT NULL,
    KEY idx_usage_reporting_event_time (created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
