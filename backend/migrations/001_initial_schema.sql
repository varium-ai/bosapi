-- Initial schema for fresh Bos API databases.
-- schema_migrations is created and managed by the migration runner before this file executes.
-- Tables use IF NOT EXISTS so an already initialized database can record this baseline safely.

CREATE TABLE IF NOT EXISTS access_revocations (
    jti        varchar(64) not null
        primary key,
    expires_at datetime(3) not null,
    KEY idx_access_revocations_expires_at (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS accounts (
    id                     bigint unsigned auto_increment
        primary key,
    name                   varchar(128)                      not null,
    platform               varchar(32)                       not null,
    account_type           varchar(32)                       not null,
    api_format             varchar(32) default 'openai_chat' not null,
    base_url               varchar(512)                      not null,
    credentials_ciphertext longblob                          not null,
    status                 varchar(24) default 'active'      not null,
    priority               bigint      default 0             not null,
    concurrency_limit      int unsigned                      null,
    schedulable            tinyint(1)  default 1             not null,
    rate_limit_rpm         int unsigned                      null,
    capabilities_json      json                              null,
    pricing_json           json                              null,
    created_at             datetime(3)                       null,
    updated_at             datetime(3)                       null,
    KEY idx_account_platform_status (platform, status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS admin_otp_challenges (
    id           bigint unsigned auto_increment
        primary key,
    admin_id     bigint unsigned               not null,
    target_email varchar(320)                  not null,
    code_hash    varchar(64)                   not null,
    attempts     smallint unsigned default '0' not null,
    expires_at   datetime(3)                   not null,
    consumed_at  datetime(3)                   null,
    created_at   datetime(3)                   null,
    KEY idx_admin_otp_challenges_admin_id (admin_id),
    KEY idx_admin_otp_challenges_expires_at (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS api_keys (
    id                   bigint unsigned auto_increment
        primary key,
    name                 varchar(128)    not null,
    key_hash             varbinary(128)  not null,
    key_prefix           varchar(24)     not null,
    user_id              bigint unsigned not null,
    organization_id      bigint unsigned not null,
    status               varchar(24)     not null,
    expires_at           datetime(3)     null,
    quota_amount         double          null,
    quota_tokens         bigint unsigned null,
    quota_used_amount    double          null,
    quota_used_tokens    bigint unsigned null,
    rate_limit_5h        int unsigned    null,
    rate_limit_1d        int unsigned    null,
    rate_limit_7d        int unsigned    null,
    ip_allowlist         json            null,
    ip_blocklist         json            null,
    last_used_at         datetime(3)     null,
    created_at           datetime(3)     null,
    updated_at           datetime(3)     null,
    daily_quota_amount   decimal(20, 8)  null,
    weekly_quota_amount  decimal(20, 8)  null,
    monthly_quota_amount decimal(20, 8)  null,
    daily_quota_tokens   bigint unsigned null,
    weekly_quota_tokens  bigint unsigned null,
    monthly_quota_tokens bigint unsigned null,
    constraint idx_api_keys_key_hash
        unique (key_hash),
    KEY idx_api_keys_organization_id (organization_id),
    KEY idx_api_keys_status (status),
    KEY idx_api_keys_user_id (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS daily_usages (
    usage_date      date                               not null,
    organization_id bigint unsigned                    not null,
    api_key_id      bigint unsigned                    not null,
    model           varchar(191)                       not null,
    request_count   bigint unsigned default '0'        not null,
    error_count     bigint unsigned default '0'        not null,
    input_tokens    bigint unsigned default '0'        not null,
    output_tokens   bigint unsigned default '0'        not null,
    total_tokens    bigint unsigned default '0'        not null,
    cost_amount     decimal(20, 8)  default 0.00000000 not null,
    primary key (usage_date, api_key_id, model),
    KEY idx_daily_org_date (organization_id, usage_date)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS `groups` (
    id                bigint unsigned auto_increment
        primary key,
    organization_id   bigint unsigned                              not null,
    name              varchar(128)                                 not null,
    platform          varchar(32)                                  not null,
    model_allowlist   json                                         null,
    rpm               int unsigned                                 null,
    concurrency_limit int unsigned                                 null,
    pricing_json      json                                         null,
    status            enum ('active', 'disabled') default 'active' not null,
    KEY idx_groups_organization_id (organization_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS key_ip_stats (
    api_key_id    bigint unsigned             not null,
    client_ip     varchar(45)                 not null,
    first_seen_at datetime(6)                 not null,
    last_seen_at  datetime(6)                 not null,
    request_count bigint unsigned default '0' not null,
    primary key (api_key_id, client_ip),
    KEY idx_key_ip_last_seen (last_seen_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS login_logs (
    id         bigint unsigned auto_increment
        primary key,
    user_id    bigint unsigned         null,
    email      varchar(320) default '' not null,
    client_ip  varchar(45)             not null,
    success    tinyint(1)              not null,
    reason     varchar(64)             null,
    created_at datetime(3)             not null,
    KEY idx_login_logs_created (created_at),
    KEY idx_login_logs_email_created (email, created_at),
    KEY idx_login_logs_user_created (user_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS model_metadata (
    id                 bigint unsigned auto_increment
        primary key,
    model_id           varchar(191)                              not null,
    display_name       varchar(191) default ''                   not null,
    input_modalities   json                                      null,
    output_modalities  json                                      null,
    supports_vision    tinyint(1)   default 0                    not null,
    supports_video     tinyint(1)   default 0                    not null,
    supports_audio     tinyint(1)   default 0                    not null,
    supports_tools     tinyint(1)   default 1                    not null,
    supports_reasoning tinyint(1)   default 0                    not null,
    context_window     int          default 32000                not null,
    max_output_tokens  int          default 4096                 not null,
    source             varchar(64)  default 'builtin'            not null,
    created_at         datetime(6)  default CURRENT_TIMESTAMP(6) not null,
    updated_at         datetime(6)  default CURRENT_TIMESTAMP(6) not null on update CURRENT_TIMESTAMP(6),
    constraint uk_model_id
        unique (model_id),
    KEY idx_supports_vision (supports_vision)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS organization_provider_bindings (
    organization_id bigint unsigned              not null,
    account_id      bigint unsigned              not null,
    priority        bigint      default 0        not null,
    model_allowlist json                         null,
    status          varchar(24) default 'active' not null,
    primary key (organization_id, account_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS organizations (
    id                   bigint unsigned auto_increment
        primary key,
    name                 varchar(128)                 not null,
    description          varchar(512)                 null,
    status               varchar(24) default 'active' not null,
    allowed_provider_ids json                         null,
    allowed_models       json                         null,
    created_at           datetime(3)                  null,
    updated_at           datetime(3)                  null,
    constraint idx_organizations_name
        unique (name),
    KEY idx_organizations_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS provider_models (
    id                       bigint unsigned auto_increment
        primary key,
    account_id               bigint unsigned              not null,
    model_name               varchar(191)                 not null,
    capabilities_json        json                         null,
    price_input_per_1m       double                       null,
    price_output_per_1m      double                       null,
    price_cache_read_per_1m  double                       null,
    price_cache_write_per_1m double                       null,
    status                   varchar(16) default 'active' not null,
    discovered_at            datetime(3)                  null,
    created_at               datetime(3)                  null,
    updated_at               datetime(3)                  null,
    constraint uq_provider_model
        unique (account_id, model_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS refresh_records (
    id         bigint unsigned auto_increment
        primary key,
    user_id    bigint unsigned not null,
    token_hash varchar(64)     not null,
    expires_at datetime(3)     not null,
    revoked_at datetime(3)     null,
    created_at datetime(3)     null,
    constraint idx_refresh_records_token_hash
        unique (token_hash),
    KEY idx_refresh_records_expires_at (expires_at),
    KEY idx_refresh_records_user_id (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS request_logs (
    id              bigint unsigned auto_increment
        primary key,
    created_at      datetime(6)                                not null,
    level           varchar(16)                                not null,
    event           varchar(128)                               not null,
    message         varchar(2048)                              not null,
    request_id      varchar(128) CHARACTER SET ascii COLLATE ascii_bin default ''  not null,
    client_ip       varchar(64)                    default ''  not null,
    organization_id bigint unsigned                default '0' not null,
    api_key_masked  varchar(128)                   default ''  not null,
    KEY idx_request_logs_created_at (created_at),
    KEY idx_request_logs_org_request_time (organization_id, request_id, created_at, id),
    KEY idx_request_logs_request_time (request_id, created_at, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS security_events (
    id               bigint unsigned auto_increment
        primary key,
    request_id       longtext                         null,
    organization_id  bigint unsigned                  null,
    api_key_id       bigint unsigned                  null,
    client_ip        longblob                         null,
    rule_id          longtext                         null,
    action           longtext                         null,
    hit_count        int unsigned                     null,
    http_status      smallint unsigned                null,
    created_at       datetime(3)                      null,
    tool_name        varchar(128)    default ''       not null,
    tool_protocol    varchar(16)     default ''       not null,
    tool_direction   varchar(16)     default ''       not null,
    call_id_hash     varchar(64)     default ''       not null,
    rule_version     bigint unsigned default '0'      not null,
    rule_scope       varchar(16)     default ''       not null,
    event_type       varchar(32)     default 'other'  not null,
    severity         varchar(16)     default 'info'   not null,
    rule_sensitivity varchar(16)     default 'normal' not null,
    KEY idx_security_events_severity (organization_id, severity, created_at),
    KEY idx_security_events_tool (organization_id, tool_name, created_at),
    KEY idx_security_events_type (organization_id, event_type, created_at),
    KEY idx_security_key_time (api_key_id, created_at),
    KEY idx_security_org_time (organization_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS settings (
    setting_key       varchar(128)    not null
        primary key,
    value_text        text            null,
    secret_ciphertext longblob        null,
    is_secret         tinyint(1)      null,
    updated_by        bigint unsigned null,
    updated_at        datetime(3)     null
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS usage_logs (
    id              bigint unsigned auto_increment
        primary key,
    request_id      varchar(128)      not null,
    usage_id        varchar(64)       not null,
    user_id         bigint unsigned   null,
    organization_id bigint unsigned   not null,
    api_key_id      bigint unsigned   not null,
    model           varchar(191)      not null,
    upstream_model  varchar(191)      null,
    endpoint        varchar(128)      not null,
    status_code     smallint unsigned not null,
    input_tokens    bigint unsigned   not null,
    output_tokens   bigint unsigned   not null,
    total_tokens    bigint unsigned   not null,
    cost_amount     decimal(20, 8)    not null,
    client_ip       varbinary(16)     null,
    is_stream       tinyint(1)        null,
    duration_ms     int unsigned      null,
    created_at      datetime(3)       not null,
    constraint uq_usage_request_usage
        unique (request_id, usage_id),
    KEY idx_usage_key_time (api_key_id, created_at),
    KEY idx_usage_logs_api_key_id (api_key_id),
    KEY idx_usage_logs_created_at (created_at),
    KEY idx_usage_logs_organization_id (organization_id),
    KEY idx_usage_logs_user_id (user_id),
    KEY idx_usage_model_time (model, created_at),
    KEY idx_usage_org_time (organization_id, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS users (
    id                    bigint unsigned auto_increment
        primary key,
    email                 varchar(320)         not null,
    password_hash         varchar(255)         not null,
    organization_id       bigint unsigned      not null,
    role                  varchar(32)          not null,
    status                varchar(24)          not null,
    totp_secret_encrypted text                 null,
    totp_enabled          tinyint(1) default 0 not null,
    totp_enabled_at       datetime(3)          null,
    created_at            datetime(3)          null,
    updated_at            datetime(3)          null,
    constraint idx_users_email
        unique (email),
    KEY idx_users_organization_id (organization_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

CREATE TABLE IF NOT EXISTS refresh_sessions (
    id         bigint unsigned auto_increment
        primary key,
    user_id    bigint unsigned not null,
    token_hash char(64)        not null,
    expires_at datetime(6)     not null,
    revoked_at datetime(6)     null,
    created_at datetime(6)     not null,
    constraint uq_refresh_token_hash
        unique (token_hash),
    constraint fk_refresh_user
        foreign key (user_id) references users (id)
            on delete cascade,
    KEY idx_refresh_expiry (expires_at),
    KEY idx_refresh_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_0900_ai_ci;

-- Seed the built-in model catalogue used before provider discovery runs.
INSERT INTO model_metadata (model_id, display_name, input_modalities, output_modalities, supports_vision, supports_video, supports_audio, supports_tools, supports_reasoning, context_window, max_output_tokens, source)
VALUES
('zai-org/GLM-5.3-Flash', 'GLM 5.3 Flash', '["text", "image"]', '["text"]', TRUE, FALSE, FALSE, TRUE, FALSE, 200000, 8192, 'builtin'),
('zai-org/GLM-5.3', 'GLM 5.3', '["text", "image"]', '["text"]', TRUE, FALSE, FALSE, TRUE, TRUE, 200000, 8192, 'builtin'),
('zai-org/GLM-5.2-FP8', 'GLM 5.2 FP8', '["text", "image"]', '["text"]', TRUE, FALSE, FALSE, TRUE, TRUE, 200000, 8192, 'builtin'),
('zai-org/GLM-5.1-FP8', 'GLM 5.1 FP8', '["text", "image"]', '["text"]', TRUE, FALSE, FALSE, TRUE, FALSE, 200000, 8192, 'builtin'),
('zai-org/GLM-5-FP8', 'GLM 5 FP8', '["text", "image"]', '["text"]', TRUE, FALSE, FALSE, TRUE, FALSE, 200000, 8192, 'builtin'),
('glm-5.3-flash', 'GLM 5.3 Flash', '["text", "image"]', '["text"]', TRUE, FALSE, FALSE, TRUE, FALSE, 200000, 8192, 'builtin'),
('glm-5.3', 'GLM 5.3', '["text", "image"]', '["text"]', TRUE, FALSE, FALSE, TRUE, TRUE, 200000, 8192, 'builtin'),
('glm-5', 'GLM 5', '["text", "image"]', '["text"]', TRUE, FALSE, FALSE, TRUE, FALSE, 200000, 8192, 'builtin'),
('glm-4v', 'GLM 4V', '["text", "image"]', '["text"]', TRUE, FALSE, FALSE, TRUE, FALSE, 128000, 4096, 'builtin'),
('glm-4v-plus', 'GLM 4V Plus', '["text", "image", "video"]', '["text"]', TRUE, TRUE, FALSE, TRUE, FALSE, 128000, 4096, 'builtin'),
('deepseek-ai/DeepSeek-V4.1-Flash', 'DeepSeek V4.1 Flash', '["text"]', '["text"]', FALSE, FALSE, FALSE, TRUE, FALSE, 64000, 8192, 'builtin'),
('deepseek-chat', 'DeepSeek V3', '["text"]', '["text"]', FALSE, FALSE, FALSE, TRUE, FALSE, 64000, 8192, 'builtin'),
('deepseek-reasoner', 'DeepSeek R1', '["text"]', '["text"]', FALSE, FALSE, FALSE, FALSE, TRUE, 64000, 8192, 'builtin'),
('Qwen3.8-27B', 'Qwen 3.8 27B', '["text"]', '["text"]', FALSE, FALSE, FALSE, TRUE, FALSE, 32000, 4096, 'builtin'),
('qwen2.5-vl-72b-instruct', 'Qwen 2.5 VL 72B', '["text", "image", "video"]', '["text"]', TRUE, TRUE, FALSE, TRUE, FALSE, 128000, 8192, 'builtin'),
('qwen2.5-vl-7b-instruct', 'Qwen 2.5 VL 7B', '["text", "image", "video"]', '["text"]', TRUE, TRUE, FALSE, TRUE, FALSE, 128000, 8192, 'builtin'),
('gpt-4o', 'GPT-4o', '["text", "image", "audio"]', '["text", "audio"]', TRUE, FALSE, TRUE, TRUE, FALSE, 128000, 16384, 'builtin'),
('gpt-4o-mini', 'GPT-4o Mini', '["text", "image"]', '["text"]', TRUE, FALSE, FALSE, TRUE, FALSE, 128000, 16384, 'builtin'),
('claude-3-5-sonnet', 'Claude 3.5 Sonnet', '["text", "image"]', '["text"]', TRUE, FALSE, FALSE, TRUE, FALSE, 200000, 8192, 'builtin'),
('claude-3-7-sonnet', 'Claude 3.7 Sonnet', '["text", "image"]', '["text"]', TRUE, FALSE, FALSE, TRUE, TRUE, 200000, 64000, 'builtin'),
('gemini-1.5-pro', 'Gemini 1.5 Pro', '["text", "image", "audio", "video"]', '["text"]', TRUE, TRUE, TRUE, TRUE, FALSE, 2000000, 8192, 'builtin'),
('gemini-2.0-flash', 'Gemini 2.0 Flash', '["text", "image", "audio", "video"]', '["text"]', TRUE, TRUE, TRUE, TRUE, FALSE, 1000000, 8192, 'builtin')
ON DUPLICATE KEY UPDATE
    display_name = VALUES(display_name),
    input_modalities = VALUES(input_modalities),
    output_modalities = VALUES(output_modalities),
    supports_vision = VALUES(supports_vision),
    supports_video = VALUES(supports_video),
    supports_audio = VALUES(supports_audio),
    supports_tools = VALUES(supports_tools),
    supports_reasoning = VALUES(supports_reasoning),
    context_window = VALUES(context_window),
    max_output_tokens = VALUES(max_output_tokens);

-- BEGIN INITIAL SUPER ADMIN BOOTSTRAP
-- The migration runner binds these session variables using the generated
-- BOS_ADMIN_EMAIL and Argon2id hash of BOS_ADMIN_PASSWORD. Never store plaintext.
-- The organization and user are created together, only when no super admin exists.
START TRANSACTION;
INSERT INTO organizations (name, description, status, allowed_provider_ids, allowed_models, created_at, updated_at)
SELECT 'Platform Administration', 'Initial super administrator organization', 'active', '[]', '[]', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)
    WHERE @bos_initial_admin_email IS NOT NULL
  AND @bos_initial_admin_password_hash IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM users WHERE role = 'super_admin')
ON DUPLICATE KEY UPDATE id = id;

INSERT INTO users (email, password_hash, organization_id, role, status, created_at, updated_at)
SELECT @bos_initial_admin_email, @bos_initial_admin_password_hash, id, 'super_admin', 'active', UTC_TIMESTAMP(3), UTC_TIMESTAMP(3)
FROM organizations
WHERE name = 'Platform Administration'
  AND @bos_initial_admin_email IS NOT NULL
  AND @bos_initial_admin_password_hash IS NOT NULL
  AND NOT EXISTS (SELECT 1 FROM users WHERE role = 'super_admin');
COMMIT;
