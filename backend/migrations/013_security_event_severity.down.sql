-- Restore the previous severity policy for the events changed by this migration.
UPDATE security_events
SET severity = CASE
    WHEN rule_id = 'tool_call_inspection_unavailable' THEN 'critical'
    WHEN action = 'block' THEN 'high'
    WHEN action = 'observe' THEN 'warning'
    ELSE 'info'
END
WHERE event_type = 'tool_call'
   OR rule_id IN (
       'tool_call_invalid',
       'prompt_secret',
       'model_not_allowed',
       'authentication_key_not_found_or_unusable',
       'invalid_provider',
       'authentication_missing_credential',
       'model_output_code'
   );
