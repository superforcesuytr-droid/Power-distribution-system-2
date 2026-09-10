-- How long the busbar is drawn. Null means as long as what is on it needs,
-- which is how every section starts; a length set here is the section's own,
-- so a bar can be run out past its columns to leave room for more.
ALTER TABLE hv_sections ADD COLUMN IF NOT EXISTS width numeric(7,2);
