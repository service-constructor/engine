ALTER TABLE services
    DROP COLUMN IF EXISTS description,
    DROP COLUMN IF EXISTS icon_url,
    DROP COLUMN IF EXISTS miniapp_url;
