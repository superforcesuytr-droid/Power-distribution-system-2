-- A site is drawn at more than one tension. The 22kV and 6.6kV boards are the
-- high-tension drawing; the 400V boards are a drawing of their own. Each is a
-- network of its own, and a way on one can land on a switchboard in another,
-- which is what makes the two drawings one system.

ALTER TABLE hv_networks ADD COLUMN IF NOT EXISTS tier text NOT NULL DEFAULT '';
ALTER TABLE hv_networks ADD COLUMN IF NOT EXISTS position int NOT NULL DEFAULT 0;

-- What is drawn so far is the high-tension side.
UPDATE hv_networks SET tier = 'ht' WHERE tier = '';

WITH ordered AS (SELECT id, row_number() OVER (ORDER BY id) - 1 AS rn FROM hv_networks)
UPDATE hv_networks n SET position = o.rn FROM ordered o WHERE n.id = o.id;

CREATE INDEX IF NOT EXISTS hv_networks_position_idx ON hv_networks (position, id);
