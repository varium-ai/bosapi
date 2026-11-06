-- Removing partitioning preserves retained rows; expired partitions cannot be restored.
ALTER TABLE request_logs REMOVE PARTITIONING;
ALTER TABLE request_logs DROP PRIMARY KEY, ADD PRIMARY KEY (id);
