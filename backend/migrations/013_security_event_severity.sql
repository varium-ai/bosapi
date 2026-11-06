-- Reclassify historical events without changing their type, action or evidence.
UPDATE security_events
SET severity = CASE
    WHEN rule_id = 'tool_call_invalid' THEN 'info'
    WHEN event_type = 'tool_call' OR rule_id = 'prompt_secret' THEN 'warning'
    WHEN rule_id IN (
        'model_not_allowed',
        'authentication_key_not_found_or_unusable',
        'invalid_provider',
        'authentication_missing_credential',
        'model_output_code'
    ) THEN 'info'
    ELSE severity
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
