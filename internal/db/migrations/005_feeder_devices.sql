-- Devices belonged to outgoing ways only, so an incoming feeder could carry
-- nothing but its switchgear. A feeder is a conductor like any other and takes
-- the same chain, so a device now belongs to either a way or a feeder.

ALTER TABLE hv_devices ALTER COLUMN way_id DROP NOT NULL;
ALTER TABLE hv_devices ADD COLUMN IF NOT EXISTS feeder_id BIGINT REFERENCES hv_feeders(id) ON DELETE CASCADE;

DO $$ BEGIN
    ALTER TABLE hv_devices ADD CONSTRAINT hv_devices_one_owner
        CHECK (num_nonnulls(way_id, feeder_id) = 1);
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

CREATE INDEX IF NOT EXISTS hv_devices_feeder_idx ON hv_devices(feeder_id, position);

-- The switchgear each feeder already showed becomes the head of its chain, so
-- what is on the diagram today is what is in the table.
INSERT INTO hv_devices (feeder_id, kind, name, rating_a, position)
SELECT f.id, 'switchgear', coalesce(nullif(f.switchgear, ''), f.name), f.rating_a, 0
FROM hv_feeders f
WHERE NOT EXISTS (SELECT 1 FROM hv_devices d WHERE d.feeder_id = f.id);
