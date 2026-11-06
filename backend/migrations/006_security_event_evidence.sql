-- Existing migrations are immutable. Historical events have no evidence and
-- remain readable; only subsequent detections collect privacy-safe evidence.
ALTER TABLE security_events
    ADD COLUMN rule_name_snapshot varchar(128) NOT NULL DEFAULT '',
    ADD COLUMN processing_result varchar(32) NOT NULL DEFAULT '',
    ADD COLUMN evidence_count tinyint unsigned NOT NULL DEFAULT '0';

CREATE TABLE IF NOT EXISTS security_event_evidence (
    id bigint unsigned NOT NULL AUTO_INCREMENT PRIMARY KEY,
    event_id bigint unsigned NOT NULL,
    ordinal tinyint unsigned NOT NULL,
    source varchar(32) NOT NULL,
    path varchar(256) NOT NULL,
    start_line int unsigned NOT NULL DEFAULT '0',
    start_column int unsigned NOT NULL DEFAULT '0',
    end_line int unsigned NOT NULL DEFAULT '0',
    end_column int unsigned NOT NULL DEFAULT '0',
    reason_code varchar(32) NOT NULL,
    category varchar(32) NOT NULL,
    `trigger` varchar(32) NOT NULL DEFAULT '',
    snippet varchar(160) NOT NULL DEFAULT '',
    visibility varchar(16) NOT NULL,
    created_at datetime(3) NOT NULL,
    expires_at datetime(3) NOT NULL,
    UNIQUE KEY idx_security_evidence_event_ordinal (event_id, ordinal),
    KEY idx_security_evidence_expiry (expires_at),
    CONSTRAINT fk_security_evidence_event FOREIGN KEY (event_id)
        REFERENCES security_events (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
