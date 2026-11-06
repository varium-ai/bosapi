-- Zero retains the global scope of existing super administrator publications.
ALTER TABLE announcements
    ADD COLUMN organization_id BIGINT UNSIGNED NOT NULL DEFAULT 0,
    ADD INDEX idx_announcements_organization_id (organization_id, id);
