-- Bare busbar kept at the left-hand end, so a bar can be run out either way and
-- not only to the right. The length in hv_sections.width covers the whole bar,
-- this much of it included.
ALTER TABLE hv_sections ADD COLUMN IF NOT EXISTS pad_left numeric(7,2) NOT NULL DEFAULT 0;
