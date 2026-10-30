-- How a low-voltage board is supplied. A board like PE 1 is fed from both
-- ends: an incomer on each half of the bus, each through its own transformer,
-- with a bus section switch between the halves. Every MCCB hangs off one half
-- or the other, and says what it feeds and where its panel stands, so the
-- diagram can be read against the room.

CREATE TABLE IF NOT EXISTS board_incomers (
    id          BIGSERIAL PRIMARY KEY,
    board_id    BIGINT NOT NULL REFERENCES boards(id) ON DELETE CASCADE,
    section     TEXT NOT NULL DEFAULT 'A' CHECK (section IN ('A', 'B')),
    name        TEXT NOT NULL DEFAULT '',
    panel       TEXT NOT NULL DEFAULT '',
    transformer TEXT NOT NULL DEFAULT '',
    ratio       TEXT NOT NULL DEFAULT '',
    rating_a    NUMERIC(10,2),
    source      TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (board_id, section)
);

ALTER TABLE boards
    ADD COLUMN IF NOT EXISTS bus_section_name     TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS bus_section_panel    TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS bus_section_rating_a NUMERIC(10,2),
    ADD COLUMN IF NOT EXISTS bus_section_closed   BOOLEAN NOT NULL DEFAULT false;

ALTER TABLE mccbs
    ADD COLUMN IF NOT EXISTS section  TEXT NOT NULL DEFAULT 'A',
    ADD COLUMN IF NOT EXISTS feeds    TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS location TEXT NOT NULL DEFAULT '';

DO $$ BEGIN
    ALTER TABLE mccbs ADD CONSTRAINT mccbs_section_check CHECK (section IN ('A', 'B'));
EXCEPTION WHEN duplicate_object THEN NULL; END $$;
