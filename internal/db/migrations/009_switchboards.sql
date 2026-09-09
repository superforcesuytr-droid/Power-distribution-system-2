-- A site has more than one switchboard: 22kV feeding transformers down into
-- 6.6kV, that one feeding 400V, each with its own rating and its own sections.
-- Bus sections now belong to a switchboard, a way can land on the switchboard
-- below instead of on a board, and an incomer can be a generator.

CREATE TABLE hv_switchboards (
  id          bigserial PRIMARY KEY,
  network_id  bigint NOT NULL REFERENCES hv_networks(id) ON DELETE CASCADE,
  name        text NOT NULL,
  voltage     text NOT NULL DEFAULT '',
  phases      text NOT NULL DEFAULT '',
  frequency   text NOT NULL DEFAULT '',
  current_a   numeric(10,2),
  fault_ka    numeric(10,2),
  position    int NOT NULL DEFAULT 0,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX hv_switchboards_network_idx ON hv_switchboards (network_id, position);

ALTER TABLE hv_sections ADD COLUMN switchboard_id bigint REFERENCES hv_switchboards(id) ON DELETE CASCADE;
ALTER TABLE hv_ways ADD COLUMN dest_switchboard_id bigint REFERENCES hv_switchboards(id) ON DELETE SET NULL;
ALTER TABLE hv_feeders ADD COLUMN kind text NOT NULL DEFAULT 'supply';

-- Everything drawn so far is one switchboard at the network's own voltage.
DO $$
DECLARE net RECORD; board_id bigint;
BEGIN
  FOR net IN SELECT id, voltage FROM hv_networks ORDER BY id LOOP
    INSERT INTO hv_switchboards (network_id, name, voltage, phases, frequency, position)
      VALUES (net.id, coalesce(nullif(net.voltage, ''), 'HV') || ' Switchboard',
              coalesce(nullif(net.voltage, ''), ''), '3p', '50Hz', 0)
      RETURNING id INTO board_id;
    UPDATE hv_sections SET switchboard_id = board_id WHERE network_id = net.id;
  END LOOP;
END $$;

ALTER TABLE hv_sections ALTER COLUMN switchboard_id SET NOT NULL;
CREATE INDEX hv_sections_switchboard_idx ON hv_sections (switchboard_id, position);
