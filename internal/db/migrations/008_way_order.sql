-- Ways were drawn in name order. They are now arranged by hand, so their
-- stored position decides the order instead. Number the positions to match the
-- order the diagram reads today, so nothing on screen moves until it is dragged.

CREATE FUNCTION pds_natural_key(t text) RETURNS text AS $$
  SELECT coalesce(string_agg(
    CASE WHEN m[1] ~ '^[0-9]+$' THEN lpad(m[1], 12, '0') ELSE lower(m[1]) END, '' ORDER BY o), '')
  FROM regexp_matches(replace(t, ' ', ''), '[0-9]+|[^0-9]+', 'g') WITH ORDINALITY AS r(m, o);
$$ LANGUAGE sql IMMUTABLE;

WITH ordered AS (
  SELECT id, row_number() OVER (PARTITION BY section_id ORDER BY pds_natural_key(name), id) - 1 AS rn
  FROM hv_ways)
UPDATE hv_ways w SET position = o.rn FROM ordered o WHERE w.id = o.id;

DROP FUNCTION pds_natural_key(text);
