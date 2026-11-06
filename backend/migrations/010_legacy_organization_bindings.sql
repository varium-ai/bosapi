-- Convert explicit legacy organization policy only when no binding policy has
-- been saved. Any existing row, including a paused row, remains authoritative.
-- Empty channel lists do not grant global access. Keys and historical ledgers
-- are intentionally untouched; empty Key allowlists keep inheriting policy.
-- Keep explicit model IDs even when a channel has no matching models today:
-- replacing a nonmatching list with [] would silently grant every model.
INSERT INTO organization_provider_bindings
    (organization_id, account_id, priority, model_allowlist, status)
SELECT o.id, a.id, 0,
       CASE
           WHEN o.allowed_models IS NULL
                OR JSON_TYPE(o.allowed_models) = 'NULL'
                OR JSON_CONTAINS(o.allowed_models, JSON_QUOTE('*'))
           THEN JSON_ARRAY()
           ELSE o.allowed_models
       END,
       'active'
FROM organizations AS o
JOIN accounts AS a
    ON JSON_CONTAINS(o.allowed_provider_ids, CAST(a.id AS JSON))
LEFT JOIN organization_provider_bindings AS existing
    ON existing.organization_id = o.id
WHERE existing.organization_id IS NULL
  AND JSON_SCHEMA_VALID(
      '{"type":"array","items":{"type":"integer","minimum":1}}',
      o.allowed_provider_ids
  )
  AND (o.allowed_models IS NULL
       OR JSON_TYPE(o.allowed_models) = 'NULL'
       OR JSON_SCHEMA_VALID(
           '{"type":"array","items":{"type":"string"}}',
           o.allowed_models
       ));
