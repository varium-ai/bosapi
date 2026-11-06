-- No synchronous history scan: existing and future logs start pending and are
-- materialized in bounded transactions by the application worker.
CREATE TABLE usage_daily_rollups (
    usage_date      DATE NOT NULL,
    organization_id BIGINT UNSIGNED NOT NULL,
    api_key_id      BIGINT UNSIGNED NOT NULL,
    model           VARCHAR(191) NOT NULL,
    requests        BIGINT UNSIGNED NOT NULL,
    errors          BIGINT UNSIGNED NOT NULL,
    input_tokens    BIGINT UNSIGNED NOT NULL,
    output_tokens   BIGINT UNSIGNED NOT NULL,
    total_tokens    BIGINT UNSIGNED NOT NULL,
    cost_amount     DECIMAL(38,8) NOT NULL,
    PRIMARY KEY (usage_date, organization_id, api_key_id, model),
    KEY idx_rollup_org_date (organization_id, usage_date),
    KEY idx_rollup_key_date (api_key_id, usage_date),
    KEY idx_rollup_model_date (model, usage_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

ALTER TABLE usage_logs ADD COLUMN rollup_applied TINYINT(1) NOT NULL DEFAULT 0;
ALTER TABLE usage_logs ADD KEY idx_usage_rollup_pending (rollup_applied, id);
