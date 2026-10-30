-- A switch room board stops at its MCCBs: each feeds a panel or a piece of
-- process equipment, not MCBs and final circuits. Such a board is marked
-- MCCBs only, and its load is read off each MCCB rather than added up from
-- circuits below it.
ALTER TABLE boards ADD COLUMN IF NOT EXISTS mccb_only BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE mccbs ADD COLUMN IF NOT EXISTS load_a NUMERIC(10,2);
ALTER TABLE mccbs DROP CONSTRAINT IF EXISTS mccbs_load_check;
ALTER TABLE mccbs ADD CONSTRAINT mccbs_load_check CHECK (load_a IS NULL OR load_a >= 0);

-- Every board entered so far with no MCB on it is one of these.
UPDATE boards bo SET mccb_only = true
 WHERE NOT EXISTS (SELECT 1 FROM mccbs m JOIN mcbs c ON c.mccb_id = m.id WHERE m.board_id = bo.id);
