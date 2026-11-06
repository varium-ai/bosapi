-- Record who created each centrally managed provider.
ALTER TABLE accounts ADD COLUMN created_by_user_id bigint unsigned NULL;
