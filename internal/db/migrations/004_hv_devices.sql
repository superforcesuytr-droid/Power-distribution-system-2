-- A way was a fixed sequence: one breaker, optionally one protection device,
-- optionally one transformer. A real conductor carries whatever is on it, in
-- whatever order, so devices become rows on the way rather than columns of it.

DO $$ BEGIN
    CREATE TYPE hv_device_kind AS ENUM
        ('switchgear', 'isolator', 'rccb', 'elr', 'elcb', 'transformer', 'fuse', 'meter');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

CREATE TABLE IF NOT EXISTS hv_devices (
    id         BIGSERIAL PRIMARY KEY,
    way_id     BIGINT NOT NULL REFERENCES hv_ways(id) ON DELETE CASCADE,
    kind       hv_device_kind NOT NULL,
    name       TEXT NOT NULL DEFAULT '',
    rating_a   NUMERIC(8,2),
    kva        NUMERIC(10,2),
    ratio      TEXT NOT NULL DEFAULT '',
    notes      TEXT NOT NULL DEFAULT '',
    position   INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS hv_devices_way_idx ON hv_devices(way_id, position);

-- Carry the existing ways over in the order they were drawn: the breaker, then
-- any protection, then any transformer.
INSERT INTO hv_devices (way_id, kind, name, rating_a, position)
SELECT id, 'switchgear', name, rating_a, 0 FROM hv_ways;

INSERT INTO hv_devices (way_id, kind, notes, position)
SELECT id, protection::text::hv_device_kind, protection_note, 1
FROM hv_ways WHERE protection <> 'none';

INSERT INTO hv_devices (way_id, kind, name, kva, ratio, position)
SELECT id, 'transformer', transformer_name, transformer_kva, transformer_ratio, 2
FROM hv_ways WHERE has_transformer;

ALTER TABLE hv_ways
    DROP COLUMN protection,
    DROP COLUMN protection_note,
    DROP COLUMN has_transformer,
    DROP COLUMN transformer_name,
    DROP COLUMN transformer_kva,
    DROP COLUMN transformer_ratio;

DROP TYPE IF EXISTS hv_protection;
