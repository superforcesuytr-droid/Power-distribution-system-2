-- Work carried out on any piece of equipment - an inspection, a repair, a
-- part replaced, a test, an incident attended - recorded against it by the
-- engineers who did it, so the history of an asset can be read in one place.
-- Records are kept even if the equipment is later deleted, as the history of
-- what was done is worth keeping.

CREATE TABLE IF NOT EXISTS maintenance_records (
    id           BIGSERIAL PRIMARY KEY,
    entity       TEXT NOT NULL CHECK (entity IN ('board', 'mccb', 'mcb', 'circuit',
                     'hv_switchboard', 'hv_feeder', 'hv_way', 'hv_device')),
    entity_id    BIGINT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('inspection', 'repair', 'replacement', 'test', 'incident', 'other')),
    done_on      DATE NOT NULL DEFAULT current_date,
    performed_by TEXT NOT NULL DEFAULT '',
    description  TEXT NOT NULL CHECK (description <> ''),
    actor_role   TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS maintenance_entity_idx ON maintenance_records (entity, entity_id, done_on DESC);
CREATE INDEX IF NOT EXISTS audit_entity_idx ON audit_log (entity, entity_id);
