-- Request logs are operational diagnostics, not the immutable usage ledger.
-- Include the partition key in the primary key, retaining all existing rows.
-- Build all initial daily partitions in the same table rebuild.
SET @request_log_partition_start = DATE_FORMAT(UTC_DATE() - INTERVAL 7 DAY, '%Y-%m-%d');
WITH RECURSIVE days AS (
  SELECT UTC_DATE() - INTERVAL 7 DAY AS day
  UNION ALL
  SELECT day + INTERVAL 1 DAY FROM days WHERE day < UTC_DATE() + INTERVAL 7 DAY
)
SELECT CONCAT(
  'ALTER TABLE request_logs DROP PRIMARY KEY, ADD PRIMARY KEY (id, created_at) ',
  'PARTITION BY RANGE COLUMNS (created_at) (',
  'PARTITION p_before VALUES LESS THAN (''', @request_log_partition_start, '''), ',
  GROUP_CONCAT(CONCAT('PARTITION p', DATE_FORMAT(day, '%Y%m%d'),
    ' VALUES LESS THAN (''', DATE_FORMAT(day + INTERVAL 1 DAY, '%Y-%m-%d'), ''')') ORDER BY day SEPARATOR ', '),
  ', PARTITION p_future VALUES LESS THAN (MAXVALUE))'
) INTO @request_log_partition_sql FROM days;
PREPARE request_log_partition_statement FROM @request_log_partition_sql;
EXECUTE request_log_partition_statement;
DEALLOCATE PREPARE request_log_partition_statement;
