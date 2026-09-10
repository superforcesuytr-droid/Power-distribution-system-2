-- An incomer is not always fed from off the drawing: on a site with more than
-- one 22kV board, one board's incomer taps a way on the board beside it. The
-- way it is fed from is named here, so the two can be drawn joined.
ALTER TABLE hv_feeders ADD COLUMN IF NOT EXISTS source_way_id bigint
  REFERENCES hv_ways(id) ON DELETE SET NULL;
CREATE INDEX IF NOT EXISTS hv_feeders_source_idx ON hv_feeders(source_way_id);
