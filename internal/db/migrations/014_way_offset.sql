-- A way taps the bar wherever the drawing says it does, not at one of a row of
-- evenly spaced slots. An offset is where along its bus section a way or an
-- incomer sits, as a fraction of that section's width. Null means it has never
-- been placed by hand and is spaced automatically with the rest.

ALTER TABLE hv_ways    ADD COLUMN IF NOT EXISTS offset_x numeric(6,5);
ALTER TABLE hv_feeders ADD COLUMN IF NOT EXISTS offset_x numeric(6,5);
