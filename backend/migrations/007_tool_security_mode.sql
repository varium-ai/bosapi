-- Preserve an explicit existing choice when upgrading.
INSERT IGNORE INTO settings (setting_key, value_text, is_secret, updated_at)
VALUES ('security.tool_call_mode', 'observe', 0, UTC_TIMESTAMP(3));
