-- Back up organization publications before rollback. Removing them prevents
-- older application versions from exposing private content as global messages.
DELETE FROM announcements WHERE organization_id <> 0;
ALTER TABLE announcements
    DROP INDEX idx_announcements_organization_id,
    DROP COLUMN organization_id;
