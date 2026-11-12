-- Rebuild usage log partitions around UTC business-day boundaries.
-- The repartition preserves all rows and changes only the physical ranges.
SET @usage_day = DATE(UTC_TIMESTAMP());
SET @usage_old_group_concat_max_len = @@SESSION.group_concat_max_len;
SET SESSION group_concat_max_len = 16384;
WITH RECURSIVE days AS (
  SELECT @usage_day - INTERVAL 30 DAY AS day
  UNION ALL SELECT day + INTERVAL 1 DAY FROM days WHERE day < @usage_day + INTERVAL 7 DAY
)
SELECT CONCAT('ALTER TABLE usage_logs_v2 PARTITION BY RANGE COLUMNS (created_at) (',
  'PARTITION p_before VALUES LESS THAN (''', DATE_FORMAT(@usage_day - INTERVAL 30 DAY, '%Y-%m-%d %H:%i:%s'), '''), ',
  GROUP_CONCAT(CONCAT('PARTITION p', DATE_FORMAT(day, '%Y%m%d'), ' VALUES LESS THAN (''',
    DATE_FORMAT(day + INTERVAL 1 DAY, '%Y-%m-%d %H:%i:%s'), ''')') ORDER BY day SEPARATOR ', '),
  ', PARTITION p_future VALUES LESS THAN (MAXVALUE))') INTO @usage_partition_sql FROM days;
PREPARE usage_partition_statement FROM @usage_partition_sql;
EXECUTE usage_partition_statement;
DEALLOCATE PREPARE usage_partition_statement;
SET SESSION group_concat_max_len = @usage_old_group_concat_max_len;

-- Keep the retention fence aligned with the new UTC partition calendar.
UPDATE usage_log_retention
SET retained_from = @usage_day - INTERVAL 30 DAY
WHERE id = 1;
