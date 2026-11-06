-- The installation-only protocol replaces the earlier usage-reporting draft.
-- Discard every existing body without reading it, including legacy report data.
UPDATE usage_reporting_outbox
SET payload = NULL, payload_hash = '', lease_token = '', lease_until = NULL,
    status = CASE WHEN status IN ('pending', 'sending') THEN 'cancelled' ELSE status END
WHERE payload IS NOT NULL;

-- Fence any scheduler/sender that obtained a lease before this migration.
UPDATE usage_reporting_state
SET config_revision = config_revision + 1, schedule_token = '', schedule_until = NULL,
    next_scan_at = UTC_TIMESTAMP(6)
WHERE id = 1;
