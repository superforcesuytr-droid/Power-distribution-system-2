-- A way lands on a board, but the drawing needs to say where at that board:
-- the transformer or panel it actually terminates on, such as TX15 at FAC1.

ALTER TABLE hv_ways ADD COLUMN IF NOT EXISTS dest_detail TEXT NOT NULL DEFAULT '';
