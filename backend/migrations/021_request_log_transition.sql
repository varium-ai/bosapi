-- Create an empty partitioned destination. Never ALTER or copy the legacy table
-- during startup. Recent history is copied later by the bounded backfill worker.
SET @request_log_partition_start = DATE_FORMAT(UTC_DATE() - INTERVAL 7 DAY, '%Y-%m-%d');
WITH RECURSIVE days AS (
  SELECT UTC_DATE() - INTERVAL 7 DAY AS day
  UNION ALL
  SELECT day + INTERVAL 1 DAY FROM days WHERE day < UTC_DATE() + INTERVAL 7 DAY
)
SELECT CONCAT(
  'CREATE TABLE request_logs_v2 (',
  'id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT, ',
  'legacy_id BIGINT UNSIGNED NULL, ',
  'created_at DATETIME(6) NOT NULL, ',
  'level VARCHAR(16) NOT NULL, event VARCHAR(128) NOT NULL, message VARCHAR(2048) NOT NULL, ',
  'request_id VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL DEFAULT '''', ',
  'client_ip VARCHAR(64) NOT NULL DEFAULT '''', organization_id BIGINT UNSIGNED NOT NULL DEFAULT 0, ',
  'api_key_masked VARCHAR(128) NOT NULL DEFAULT '''', ',
  'PRIMARY KEY (id, created_at), UNIQUE KEY uq_request_logs_legacy (legacy_id, created_at), ',
  'KEY idx_request_logs_created_at (created_at), ',
  'KEY idx_request_logs_org_request_time (organization_id, request_id, created_at, id), ',
  'KEY idx_request_logs_request_time (request_id, created_at, id)',
  ') ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci ',
  'PARTITION BY RANGE COLUMNS (created_at) (',
  'PARTITION p_before VALUES LESS THAN (''', @request_log_partition_start, '''), ',
  GROUP_CONCAT(CONCAT('PARTITION p', DATE_FORMAT(day, '%Y%m%d'),
    ' VALUES LESS THAN (''', DATE_FORMAT(day + INTERVAL 1 DAY, '%Y-%m-%d'), ''')') ORDER BY day SEPARATOR ', '),
  ', PARTITION p_future VALUES LESS THAN (MAXVALUE))'
) INTO @request_log_partition_sql FROM days;
PREPARE request_log_partition_statement FROM @request_log_partition_sql;
EXECUTE request_log_partition_statement;
DEALLOCATE PREPARE request_log_partition_statement;

CREATE TABLE request_log_backfill (
  id TINYINT UNSIGNED NOT NULL PRIMARY KEY,
  from_at DATETIME(6) NOT NULL,
  until_at DATETIME(6) NOT NULL,
  cursor_at DATETIME(6) NOT NULL,
  cursor_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
  copied_rows BIGINT UNSIGNED NOT NULL DEFAULT 0,
  next_batch_at DATETIME(6) NULL,
  completed_at DATETIME(6) NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

INSERT INTO request_log_backfill (id, from_at, until_at, cursor_at)
VALUES (1, UTC_TIMESTAMP(6) - INTERVAL 24 HOUR, UTC_TIMESTAMP(6), UTC_TIMESTAMP(6) - INTERVAL 24 HOUR);
