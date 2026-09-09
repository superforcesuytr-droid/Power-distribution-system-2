-- Ways hung off a single feeder, so an outgoing switchgear appeared to be fed
-- by one incomer. On a sectionalised busbar it is fed by the section, which
-- several incomers back. Sections become a thing in their own right: feeders
-- land on one, ways tap one, and a coupler ties two together.

CREATE TABLE IF NOT EXISTS hv_sections (
    id         BIGSERIAL PRIMARY KEY,
    network_id BIGINT NOT NULL REFERENCES hv_networks(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    position   INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS hv_sections_network_idx ON hv_sections(network_id, position);

ALTER TABLE hv_feeders ADD COLUMN IF NOT EXISTS section_id BIGINT REFERENCES hv_sections(id) ON DELETE CASCADE;
ALTER TABLE hv_ways    ADD COLUMN IF NOT EXISTS section_id BIGINT REFERENCES hv_sections(id) ON DELETE CASCADE;
ALTER TABLE hv_couplers ADD COLUMN IF NOT EXISTS left_section_id  BIGINT REFERENCES hv_sections(id) ON DELETE CASCADE;
ALTER TABLE hv_couplers ADD COLUMN IF NOT EXISTS right_section_id BIGINT REFERENCES hv_sections(id) ON DELETE CASCADE;

-- Rebuild the sections the couplers already implied: walk the feeders in order
-- and start a new section after each feeder a coupler sits behind.
DO $$
DECLARE
    net RECORD;
    f   RECORD;
    sec BIGINT;
    cuts INT[];
    n   INT;
BEGIN
    FOR net IN SELECT id FROM hv_networks LOOP
        SELECT coalesce(array_agg(lf.position), '{}') INTO cuts
        FROM hv_couplers c JOIN hv_feeders lf ON lf.id = c.left_id
        WHERE c.network_id = net.id;

        n := 1;
        sec := NULL;
        FOR f IN SELECT * FROM hv_feeders WHERE network_id = net.id ORDER BY position, name LOOP
            IF sec IS NULL THEN
                INSERT INTO hv_sections (network_id, name, position)
                VALUES (net.id, 'Section ' || chr(64 + n), n - 1)
                RETURNING id INTO sec;
            END IF;
            UPDATE hv_feeders SET section_id = sec WHERE id = f.id;
            UPDATE hv_ways SET section_id = sec WHERE feeder_id = f.id;
            IF f.position = ANY(cuts) THEN
                sec := NULL;
                n := n + 1;
            END IF;
        END LOOP;

        -- A network with no feeders still needs somewhere for the first one.
        IF NOT EXISTS (SELECT 1 FROM hv_sections WHERE network_id = net.id) THEN
            INSERT INTO hv_sections (network_id, name, position) VALUES (net.id, 'Section A', 0);
        END IF;
    END LOOP;
END $$;

UPDATE hv_couplers c
SET left_section_id = lf.section_id, right_section_id = rf.section_id
FROM hv_feeders lf, hv_feeders rf
WHERE lf.id = c.left_id AND rf.id = c.right_id;

-- A coupler that cannot be resolved to two sections has nothing to tie.
DELETE FROM hv_couplers WHERE left_section_id IS NULL OR right_section_id IS NULL;

ALTER TABLE hv_couplers DROP COLUMN left_id, DROP COLUMN right_id;
ALTER TABLE hv_couplers ALTER COLUMN left_section_id SET NOT NULL;
ALTER TABLE hv_couplers ALTER COLUMN right_section_id SET NOT NULL;
ALTER TABLE hv_ways DROP COLUMN feeder_id;
