ALTER TABLE offers_quoted
    DROP COLUMN IF EXISTS promised_days,
    DROP COLUMN IF EXISTS region;
