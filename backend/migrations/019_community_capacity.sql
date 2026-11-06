-- A single shared row serializes all resource creation transactions.
INSERT IGNORE INTO settings (setting_key, value_text, is_secret, updated_at)
VALUES ('community.capacity_lock', '', 0, UTC_TIMESTAMP(6));
