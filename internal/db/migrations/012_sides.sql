-- A low-tension board is fed from both ends: one transformer backs the left of
-- the bar, another the right, and every outgoing way belongs to one side or the
-- other. Marking the side is what lets the drawing group the ways along the bar
-- instead of hanging them off the point an incomer lands on.

ALTER TABLE hv_feeders ADD COLUMN IF NOT EXISTS side text NOT NULL DEFAULT '';
ALTER TABLE hv_ways    ADD COLUMN IF NOT EXISTS side text NOT NULL DEFAULT '';
