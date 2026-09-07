-- Power distribution schema. Applied once by the application on first start.

CREATE TABLE IF NOT EXISTS buildings (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    icon        TEXT NOT NULL DEFAULT '🏭',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS boards (
    id          BIGSERIAL PRIMARY KEY,
    building_id BIGINT NOT NULL REFERENCES buildings(id) ON DELETE CASCADE,
    code        TEXT NOT NULL UNIQUE,
    voltage     TEXT NOT NULL DEFAULT '400V',
    phases      TEXT NOT NULL DEFAULT '3PH',
    level       TEXT NOT NULL DEFAULT '',
    location    TEXT NOT NULL DEFAULT '',
    technician  TEXT NOT NULL DEFAULT '',
    position    INT  NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS boards_building_idx ON boards(building_id);

CREATE TABLE IF NOT EXISTS mccbs (
    id          BIGSERIAL PRIMARY KEY,
    board_id    BIGINT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    rating_a    NUMERIC(8,2) NOT NULL CHECK (rating_a > 0),
    position    INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (board_id, name)
);

CREATE TABLE IF NOT EXISTS mcbs (
    id          BIGSERIAL PRIMARY KEY,
    mccb_id     BIGINT NOT NULL REFERENCES mccbs(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    rating_a    NUMERIC(8,2) NOT NULL CHECK (rating_a > 0),
    position    INT NOT NULL DEFAULT 0,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (mccb_id, name)
);
CREATE INDEX IF NOT EXISTS mcbs_mccb_idx ON mcbs(mccb_id);

DO $$ BEGIN
    CREATE TYPE circuit_status AS ENUM ('active', 'maintenance', 'inactive');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

CREATE TABLE IF NOT EXISTS circuits (
    id              BIGSERIAL PRIMARY KEY,
    mcb_id          BIGINT NOT NULL REFERENCES mcbs(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    code            TEXT NOT NULL UNIQUE,
    load_a          NUMERIC(8,2) NOT NULL DEFAULT 0 CHECK (load_a >= 0),
    status          circuit_status NOT NULL DEFAULT 'active',
    equipment_count INT NOT NULL DEFAULT 1 CHECK (equipment_count >= 0),
    service         TEXT NOT NULL DEFAULT '',
    notes           TEXT NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS circuits_mcb_idx ON circuits(mcb_id);

CREATE TABLE IF NOT EXISTS audit_log (
    id          BIGSERIAL PRIMARY KEY,
    at          TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor_role  TEXT NOT NULL,
    action      TEXT NOT NULL,
    entity      TEXT NOT NULL,
    entity_id   BIGINT,
    summary     TEXT NOT NULL,
    details     JSONB
);
CREATE INDEX IF NOT EXISTS audit_log_at_idx ON audit_log(at DESC);

CREATE TABLE IF NOT EXISTS app_settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
INSERT INTO app_settings (key, value) VALUES
    ('trip_factor', '1.14'),
    ('warn_pct', '65'),
    ('crit_pct', '85')
ON CONFLICT (key) DO NOTHING;
