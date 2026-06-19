ALTER TABLE bots ADD COLUMN qq_number VARCHAR(20) DEFAULT '';
CREATE UNIQUE INDEX IF NOT EXISTS idx_bots_qq_number ON bots(qq_number) WHERE qq_number != '' AND deleted_at IS NULL;
