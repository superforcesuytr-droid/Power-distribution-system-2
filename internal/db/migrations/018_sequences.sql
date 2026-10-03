-- The first drawing was put down with its id written out, which left the
-- counter that numbers drawings behind it: on a database built from scratch the
-- next drawing added was given the same number and refused. Every counter is
-- brought up to what is already in its table, so none can fall behind again.
DO $$
DECLARE
  col record;
BEGIN
  FOR col IN
    SELECT c.table_name, c.column_name
    FROM information_schema.columns c
    WHERE c.table_schema = current_schema()
      AND c.column_default LIKE 'nextval(%'
  LOOP
    EXECUTE format(
      'SELECT setval(pg_get_serial_sequence(%L, %L), coalesce((SELECT max(%I) FROM %I), 0) + 1, false)',
      col.table_name, col.column_name, col.column_name, col.table_name);
  END LOOP;
END $$;
