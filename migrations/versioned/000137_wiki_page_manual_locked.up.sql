ALTER TABLE wiki_pages ADD COLUMN IF NOT EXISTS manual_locked boolean NOT NULL DEFAULT false;
