-- Organization zero is reserved for a super administrator's global read scope.
-- Receipts track visible events individually because IDs can commit out of order.
CREATE TABLE IF NOT EXISTS security_event_reads (
    user_id bigint unsigned NOT NULL,
    organization_id bigint unsigned NOT NULL,
    event_id bigint unsigned NOT NULL,
    PRIMARY KEY (user_id, organization_id, event_id),
    CONSTRAINT fk_security_event_reads_user FOREIGN KEY (user_id)
        REFERENCES users (id) ON DELETE CASCADE,
    CONSTRAINT fk_security_event_reads_event FOREIGN KEY (event_id)
        REFERENCES security_events (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;
