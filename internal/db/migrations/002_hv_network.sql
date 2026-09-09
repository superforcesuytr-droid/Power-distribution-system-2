-- The high-voltage network that sits above the distribution boards: incoming
-- feeders, the switchgear each one lands on, bus couplers between them, and the
-- outgoing ways that carry supply down to a board.

CREATE TABLE IF NOT EXISTS hv_networks (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL,
    voltage     TEXT NOT NULL DEFAULT '22kV',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS hv_feeders (
    id          BIGSERIAL PRIMARY KEY,
    network_id  BIGINT NOT NULL REFERENCES hv_networks(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    voltage     TEXT NOT NULL DEFAULT '22kV',
    source      TEXT NOT NULL DEFAULT '',
    rating_a    NUMERIC(8,2),
    position    INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (network_id, name)
);
CREATE INDEX IF NOT EXISTS hv_feeders_network_idx ON hv_feeders(network_id);

-- Protection fitted on an outgoing way, downstream of the switchgear.
DO $$ BEGIN
    CREATE TYPE hv_protection AS ENUM ('none', 'rccb', 'elr', 'elcb');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

CREATE TABLE IF NOT EXISTS hv_ways (
    id              BIGSERIAL PRIMARY KEY,
    feeder_id       BIGINT NOT NULL REFERENCES hv_feeders(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    rating_a        NUMERIC(8,2),
    protection      hv_protection NOT NULL DEFAULT 'none',
    protection_note TEXT NOT NULL DEFAULT '',
    -- A transformer sits between the way and what it feeds when one is fitted.
    has_transformer BOOLEAN NOT NULL DEFAULT false,
    transformer_name TEXT NOT NULL DEFAULT '',
    transformer_kva  NUMERIC(10,2),
    transformer_ratio TEXT NOT NULL DEFAULT '',
    -- Where the way lands. A board reference makes the destination clickable;
    -- the label carries it when the destination is not a board in this system.
    dest_board_id   BIGINT REFERENCES boards(id) ON DELETE SET NULL,
    dest_label      TEXT NOT NULL DEFAULT '',
    notes           TEXT NOT NULL DEFAULT '',
    position        INT NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (feeder_id, name)
);
CREATE INDEX IF NOT EXISTS hv_ways_feeder_idx ON hv_ways(feeder_id);
CREATE INDEX IF NOT EXISTS hv_ways_board_idx ON hv_ways(dest_board_id);

-- A coupler ties the busbars of two feeders together. Ordering follows the
-- feeders it joins, so it is drawn between them.
CREATE TABLE IF NOT EXISTS hv_couplers (
    id          BIGSERIAL PRIMARY KEY,
    network_id  BIGINT NOT NULL REFERENCES hv_networks(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    left_id     BIGINT NOT NULL REFERENCES hv_feeders(id) ON DELETE CASCADE,
    right_id    BIGINT NOT NULL REFERENCES hv_feeders(id) ON DELETE CASCADE,
    closed      BOOLEAN NOT NULL DEFAULT false,
    rating_a    NUMERIC(8,2),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (left_id <> right_id)
);
CREATE INDEX IF NOT EXISTS hv_couplers_network_idx ON hv_couplers(network_id);

-- Seed the described site: four 22kV feeders with a bus coupler in the middle.
INSERT INTO hv_networks (id, name, voltage)
SELECT 1, 'SSMC Electrical Distribution Overview', '22kV'
WHERE NOT EXISTS (SELECT 1 FROM hv_networks);

INSERT INTO hv_feeders (network_id, name, voltage, source, position)
SELECT 1, v.name, '22kV', v.source, v.pos
FROM (VALUES
    ('F1', 'Incoming supply 1', 0),
    ('F2', 'Incoming supply 2', 1),
    ('F3', 'Incoming supply 3', 2),
    ('F4', 'Incoming supply 4', 3)
) AS v(name, source, pos)
WHERE EXISTS (SELECT 1 FROM hv_networks WHERE id = 1)
  AND NOT EXISTS (SELECT 1 FROM hv_feeders WHERE network_id = 1);

INSERT INTO hv_couplers (network_id, name, left_id, right_id, closed)
SELECT 1, 'BC-1', l.id, r.id, false
FROM hv_feeders l, hv_feeders r
WHERE l.network_id = 1 AND l.name = 'F2'
  AND r.network_id = 1 AND r.name = 'F3'
  AND NOT EXISTS (SELECT 1 FROM hv_couplers WHERE network_id = 1);
