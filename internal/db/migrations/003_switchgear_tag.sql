-- On a real single line diagram the incoming feeder carries two labels: the
-- feeder itself (F1) at the top, and the designation of the switchgear it
-- lands on (22SGI1) written alongside the breaker symbol. They were one field.

ALTER TABLE hv_feeders ADD COLUMN IF NOT EXISTS switchgear TEXT NOT NULL DEFAULT '';

-- Give the feeders already on file the designation they would carry on a
-- drawing, so the diagram is labelled without anyone retyping it.
UPDATE hv_feeders
SET switchgear = '22SGI' || substring(name from '[0-9]+')
WHERE switchgear = '' AND name ~ '[0-9]';
