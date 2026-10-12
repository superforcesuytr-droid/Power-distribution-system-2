-- Every MCCB and MCB carries a status the engineers keep up to date: in
-- service, down for maintenance, or faulted. Who is attending to it and a note
-- of what is going on travel with the status, and so does when it was last
-- changed, so a faulted breaker can be found and someone sent to it.

DO $$ BEGIN
    CREATE TYPE breaker_status AS ENUM ('in_service', 'maintenance', 'faulted');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

ALTER TABLE mccbs
    ADD COLUMN IF NOT EXISTS status      breaker_status NOT NULL DEFAULT 'in_service',
    ADD COLUMN IF NOT EXISTS assigned_to TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS status_note TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS status_at   TIMESTAMPTZ;

ALTER TABLE mcbs
    ADD COLUMN IF NOT EXISTS status      breaker_status NOT NULL DEFAULT 'in_service',
    ADD COLUMN IF NOT EXISTS assigned_to TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS status_note TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS status_at   TIMESTAMPTZ;
