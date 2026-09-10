-- A way on a low-tension board carries nothing where it leaves the bar: the
-- conductor simply goes. Ways added before that was understood were given a
-- switchgear at the top by default, which is not on the drawing, so take it
-- off them and close up the order of whatever else is on the conductor.

DELETE FROM hv_devices d
USING hv_ways w, hv_sections s, hv_networks n
WHERE d.way_id = w.id
  AND w.section_id = s.id
  AND s.network_id = n.id
  AND n.tier = 'lt'
  AND d.position = 0
  AND d.kind = 'switchgear';

WITH ordered AS (
  SELECT id, row_number() OVER (PARTITION BY way_id ORDER BY position, id) - 1 AS rn
  FROM hv_devices WHERE way_id IS NOT NULL)
UPDATE hv_devices d SET position = o.rn FROM ordered o WHERE d.id = o.id;
