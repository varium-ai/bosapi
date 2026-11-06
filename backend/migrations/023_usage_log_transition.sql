-- Only the empty destination is altered. The live source is drained in bounded
-- transactions after its daily rollups have been acknowledged.
CREATE TABLE usage_logs_v2 LIKE usage_logs;
ALTER TABLE usage_logs_v2
  MODIFY id BIGINT UNSIGNED NOT NULL,
  DROP PRIMARY KEY, DROP INDEX uq_usage_request_usage,
  ADD PRIMARY KEY (id, created_at);

-- Usage partitions follow the UTC+8 quota calendar; stored timestamps stay UTC.
SET @usage_day = DATE(UTC_TIMESTAMP() + INTERVAL 8 HOUR);
SET @usage_old_group_concat_max_len = @@SESSION.group_concat_max_len;
SET SESSION group_concat_max_len = 16384;
WITH RECURSIVE days AS (
  SELECT @usage_day - INTERVAL 30 DAY AS day
  UNION ALL SELECT day + INTERVAL 1 DAY FROM days WHERE day < @usage_day + INTERVAL 7 DAY
)
SELECT CONCAT('ALTER TABLE usage_logs_v2 PARTITION BY RANGE COLUMNS (created_at) (',
  'PARTITION p_before VALUES LESS THAN (''', DATE_FORMAT(@usage_day - INTERVAL 30 DAY - INTERVAL 8 HOUR, '%Y-%m-%d %H:%i:%s'), '''), ',
  GROUP_CONCAT(CONCAT('PARTITION p', DATE_FORMAT(day, '%Y%m%d'), ' VALUES LESS THAN (''',
    DATE_FORMAT(day + INTERVAL 1 DAY - INTERVAL 8 HOUR, '%Y-%m-%d %H:%i:%s'), ''')') ORDER BY day SEPARATOR ', '),
  ', PARTITION p_future VALUES LESS THAN (MAXVALUE))') INTO @usage_partition_sql FROM days;
PREPARE usage_partition_statement FROM @usage_partition_sql;
EXECUTE usage_partition_statement;
DEALLOCATE PREPARE usage_partition_statement;
SET SESSION group_concat_max_len = @usage_old_group_concat_max_len;

-- Preserve the original unique key's collation and lifetime retry protection.
CREATE TABLE usage_log_receipts (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT PRIMARY KEY,
  request_id VARCHAR(128) NOT NULL,
  usage_id VARCHAR(64) NOT NULL,
  cost_amount DECIMAL(20,8) NOT NULL,
  UNIQUE KEY uq_usage_receipt (request_id, usage_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE usage_log_retention (
  id TINYINT UNSIGNED NOT NULL PRIMARY KEY,
  retained_from DATETIME(3) NOT NULL,
  legacy_high_watermark BIGINT UNSIGNED NOT NULL DEFAULT 0,
  partition_writes TINYINT(1) NOT NULL DEFAULT 0
) ENGINE=InnoDB;
INSERT INTO usage_log_retention (id, retained_from) VALUES (1, @usage_day - INTERVAL 30 DAY - INTERVAL 8 HOUR);
CREATE TABLE usage_log_backfill (
  id TINYINT UNSIGNED NOT NULL PRIMARY KEY,
  enabled TINYINT(1) NOT NULL DEFAULT 0,
  moved_rows BIGINT UNSIGNED NOT NULL DEFAULT 0,
  next_batch_at DATETIME(6) NULL
) ENGINE=InnoDB;
INSERT INTO usage_log_backfill (id) VALUES (1);

-- Activation records the maximum legacy ID under the writer fence. New public
-- IDs add that fixed watermark to receipt IDs; original IDs remain unchanged.
-- MySQL >= 8.0.29 pushes outer filters into both UNION branches.
CREATE VIEW usage_logs_read AS
SELECT id, request_id, usage_id, user_id, organization_id, api_key_id, model, upstream_model, endpoint, status_code, error_code, input_tokens, output_tokens, total_tokens, cost_amount, client_ip, is_stream, duration_ms, first_token_ms, created_at, rollup_applied FROM usage_logs_v2
UNION ALL
SELECT id, request_id, usage_id, user_id, organization_id, api_key_id, model, upstream_model, endpoint, status_code, error_code, input_tokens, output_tokens, total_tokens, cost_amount, client_ip, is_stream, duration_ms, first_token_ms, created_at, rollup_applied FROM usage_logs;

-- Pending accounting rows must remain visible even when older than retention.
CREATE VIEW usage_logs_pending AS
SELECT id, request_id, usage_id, user_id, organization_id, api_key_id, model, upstream_model, endpoint, status_code, error_code, input_tokens, output_tokens, total_tokens, cost_amount, client_ip, is_stream, duration_ms, first_token_ms, created_at, rollup_applied FROM usage_logs_v2 WHERE rollup_applied = 0
UNION ALL
SELECT id, request_id, usage_id, user_id, organization_id, api_key_id, model, upstream_model, endpoint, status_code, error_code, input_tokens, output_tokens, total_tokens, cost_amount, client_ip, is_stream, duration_ms, first_token_ms, created_at, rollup_applied FROM usage_logs WHERE rollup_applied = 0;
