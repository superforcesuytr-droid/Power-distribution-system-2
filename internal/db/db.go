// Package db is the PostgreSQL persistence layer. A Store may exist before a
// database has been configured; Ready() reports whether queries can run.
package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/superforcesuytr-droid/power-distribution-system/internal/model"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// ErrNotReady is returned when no database connection has been established.
var ErrNotReady = errors.New("database not configured")

// ErrNotFound is returned when a row does not exist.
var ErrNotFound = errors.New("not found")

// UserError wraps a message that is safe and useful to show to the operator.
type UserError struct{ Msg string }

func (e *UserError) Error() string { return e.Msg }

// Store wraps a connection pool that can be (re)configured at runtime.
type Store struct {
	mu       sync.RWMutex
	pool     *pgxpool.Pool
	settings model.Settings
	dsn      string
	lastErr  string
	server   string
}

// New returns an unconnected store.
func New() *Store {
	return &Store{settings: model.DefaultSettings()}
}

// Status is a snapshot of the connection state for the UI.
type Status struct {
	State   string `json:"state"` // ready | unconfigured | error
	Message string `json:"message,omitempty"`
	Server  string `json:"server,omitempty"`
}

// Status reports the current connection state.
func (s *Store) Status() Status {
	s.mu.RLock()
	defer s.mu.RUnlock()
	switch {
	case s.pool != nil:
		return Status{State: "ready", Server: s.server}
	case s.lastErr != "":
		return Status{State: "error", Message: s.lastErr}
	default:
		return Status{State: "unconfigured"}
	}
}

// Ready reports whether a pool is available.
func (s *Store) Ready() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.pool != nil
}

// Settings returns the cached engineering settings.
func (s *Store) Settings() model.Settings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.settings
}

func (s *Store) getPool() (*pgxpool.Pool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.pool == nil {
		return nil, ErrNotReady
	}
	return s.pool, nil
}

// Close releases the pool.
func (s *Store) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pool != nil {
		s.pool.Close()
		s.pool = nil
	}
}

// Test opens a short-lived connection to verify the DSN without adopting it.
// It returns the server version string.
func Test(ctx context.Context, dsn string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		if created, cerr := createDatabaseIfMissing(ctx, dsn, err); created && cerr == nil {
			conn, err = pgx.Connect(ctx, dsn)
		} else if cerr != nil {
			return "", cerr
		}
		if err != nil {
			return "", friendlyConnError(err)
		}
	}
	defer conn.Close(ctx)
	var ver string
	if err := conn.QueryRow(ctx, `SELECT version()`).Scan(&ver); err != nil {
		return "", err
	}
	return shortVersion(ver), nil
}

// Connect establishes the pool, applies migrations and seeds demo data.
func (s *Store) Connect(ctx context.Context, dsn string) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		s.setErr("invalid connection settings: " + err.Error())
		return &UserError{"Invalid connection settings: " + err.Error()}
	}
	cfg.MaxConns = 8
	cfg.MinConns = 1
	cfg.MaxConnLifetime = time.Hour
	cfg.HealthCheckPeriod = 30 * time.Second

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err == nil {
		err = pool.Ping(ctx)
	}
	if err != nil {
		if pool != nil {
			pool.Close()
		}
		created, cerr := createDatabaseIfMissing(ctx, dsn, err)
		if cerr != nil {
			s.setErr(cerr.Error())
			return cerr
		}
		if !created {
			ferr := friendlyConnError(err)
			s.setErr(ferr.Error())
			return ferr
		}
		pool, err = pgxpool.NewWithConfig(ctx, cfg)
		if err == nil {
			err = pool.Ping(ctx)
		}
		if err != nil {
			ferr := friendlyConnError(err)
			s.setErr(ferr.Error())
			return ferr
		}
	}

	if err := migrate(ctx, pool); err != nil {
		pool.Close()
		s.setErr("migration failed: " + err.Error())
		return fmt.Errorf("migration failed: %w", err)
	}

	var ver string
	_ = pool.QueryRow(ctx, `SELECT version()`).Scan(&ver)

	s.mu.Lock()
	if s.pool != nil {
		s.pool.Close()
	}
	s.pool = pool
	s.dsn = dsn
	s.lastErr = ""
	s.server = shortVersion(ver)
	s.mu.Unlock()

	if err := s.reloadSettings(ctx); err != nil {
		log.Printf("warning: could not load settings: %v", err)
	}
	log.Printf("database connected: %s", s.server)
	return nil
}

func (s *Store) setErr(msg string) {
	s.mu.Lock()
	s.lastErr = msg
	s.mu.Unlock()
}

// createDatabaseIfMissing handles SQLSTATE 3D000 (invalid_catalog_name) by
// connecting to the maintenance database and creating the requested one.
func createDatabaseIfMissing(ctx context.Context, dsn string, connErr error) (bool, error) {
	var pgErr *pgconn.PgError
	if !errors.As(connErr, &pgErr) || pgErr.Code != "3D000" {
		return false, nil
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return false, nil
	}
	target := strings.TrimPrefix(u.Path, "/")
	if target == "" || target == "postgres" {
		return false, nil
	}
	u.Path = "/postgres"
	admin, err := pgx.Connect(ctx, u.String())
	if err != nil {
		return false, &UserError{fmt.Sprintf("Database %q does not exist and it could not be created automatically (%s). Create it with: CREATE DATABASE %s;", target, friendlyConnError(err), pgx.Identifier{target}.Sanitize())}
	}
	defer admin.Close(ctx)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{target}.Sanitize()); err != nil {
		return false, &UserError{fmt.Sprintf("Database %q does not exist and creating it failed: %s", target, err)}
	}
	log.Printf("created database %q", target)
	return true, nil
}

func friendlyConnError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "28P01":
			return &UserError{"Password authentication failed - check the user name and password."}
		case "28000":
			if strings.Contains(pgErr.Message, "does not exist") {
				return &UserError{"Authentication failed: " + pgErr.Message + "."}
			}
			return &UserError{"Connection rejected by the server's authentication rules (pg_hba.conf): " + pgErr.Message}
		case "3D000":
			return &UserError{"Database does not exist: " + pgErr.Message}
		}
		return &UserError{pgErr.Message}
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"):
		return &UserError{"Connection refused - is PostgreSQL running on that host and port, and is it listening on TCP (listen_addresses)?"}
	case strings.Contains(msg, "no such host"):
		return &UserError{"Host not found - check the host name."}
	case strings.Contains(msg, "timeout") || strings.Contains(msg, "deadline exceeded"):
		return &UserError{"Timed out connecting to the server - check the host, port and firewall."}
	}
	return &UserError{msg}
}

func shortVersion(v string) string {
	if i := strings.Index(v, " on "); i > 0 {
		return v[:i]
	}
	if len(v) > 40 {
		return v[:40]
	}
	return v
}

// migrate applies embedded SQL files in name order, once each.
func migrate(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	names, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		version := strings.TrimSuffix(strings.TrimPrefix(name, "migrations/"), ".sql")
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version = $1)`, version).Scan(&exists); err != nil {
			return err
		}
		if exists {
			continue
		}
		sqlText, err := migrationFS.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, string(sqlText)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("%s: %w", version, err)
		}
		if version == "001_init" {
			if err := seedDemo(ctx, tx); err != nil {
				_ = tx.Rollback(ctx)
				return fmt.Errorf("seed: %w", err)
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations (version) VALUES ($1)`, version); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		log.Printf("applied migration %s", version)
	}
	return nil
}

// ---------------------------------------------------------------- settings

func (s *Store) reloadSettings(ctx context.Context) error {
	pool, err := s.getPool()
	if err != nil {
		return err
	}
	rows, err := pool.Query(ctx, `SELECT key, value FROM app_settings`)
	if err != nil {
		return err
	}
	defer rows.Close()
	st := model.DefaultSettings()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return err
		}
		switch k {
		case "trip_factor":
			if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
				st.TripFactor = f
			}
		case "warn_pct":
			if n, err := strconv.Atoi(v); err == nil {
				st.WarnPct = n
			}
		case "crit_pct":
			if n, err := strconv.Atoi(v); err == nil {
				st.CritPct = n
			}
		}
	}
	s.mu.Lock()
	s.settings = st
	s.mu.Unlock()
	return rows.Err()
}

// UpdateSettings validates and persists new engineering parameters.
func (s *Store) UpdateSettings(ctx context.Context, st model.Settings) error {
	if st.TripFactor < 0.5 || st.TripFactor > 2 {
		return &UserError{"Trip factor must be between 0.5 and 2.0"}
	}
	if st.WarnPct < 1 || st.WarnPct >= st.CritPct || st.CritPct > 100 {
		return &UserError{"Thresholds must satisfy 1 <= warning < critical <= 100"}
	}
	pool, err := s.getPool()
	if err != nil {
		return err
	}
	batch := &pgx.Batch{}
	for k, v := range map[string]string{
		"trip_factor": strconv.FormatFloat(st.TripFactor, 'f', -1, 64),
		"warn_pct":    strconv.Itoa(st.WarnPct),
		"crit_pct":    strconv.Itoa(st.CritPct),
	} {
		batch.Queue(`INSERT INTO app_settings (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, k, v)
	}
	if err := pool.SendBatch(ctx, batch).Close(); err != nil {
		return err
	}
	return s.reloadSettings(ctx)
}

// ---------------------------------------------------------------- reads

// Overview returns every building with counts plus site-wide totals.
func (s *Store) Overview(ctx context.Context) ([]model.Building, map[string]int, error) {
	pool, err := s.getPool()
	if err != nil {
		return nil, nil, err
	}
	rows, err := pool.Query(ctx, `
		SELECT b.id, b.name, b.icon, b.created_at, b.updated_at,
		       (SELECT count(*) FROM boards bo WHERE bo.building_id = b.id),
		       (SELECT count(*) FROM circuits c JOIN mcbs m ON m.id = c.mcb_id JOIN mccbs mm ON mm.id = m.mccb_id
		          JOIN boards bo ON bo.id = mm.board_id WHERE bo.building_id = b.id AND c.status = 'active'),
		       (SELECT count(*) FROM circuits c JOIN mcbs m ON m.id = c.mcb_id JOIN mccbs mm ON mm.id = m.mccb_id
		          JOIN boards bo ON bo.id = mm.board_id WHERE bo.building_id = b.id AND c.status = 'maintenance'),
		       (SELECT count(*) FROM circuits c JOIN mcbs m ON m.id = c.mcb_id JOIN mccbs mm ON mm.id = m.mccb_id
		          JOIN boards bo ON bo.id = mm.board_id WHERE bo.building_id = b.id AND c.status = 'inactive')
		FROM buildings b ORDER BY b.id`)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var out []model.Building
	for rows.Next() {
		var b model.Building
		if err := rows.Scan(&b.ID, &b.Name, &b.Icon, &b.CreatedAt, &b.UpdatedAt,
			&b.BoardCount, &b.ActiveCircuits, &b.MaintenanceCircuits, &b.InactiveCircuits); err != nil {
			return nil, nil, err
		}
		out = append(out, b)
	}
	if out == nil {
		out = []model.Building{}
	}
	totals := map[string]int{}
	var nb, nbo, nmccb, nmcb, nc int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM buildings), (SELECT count(*) FROM boards),
		(SELECT count(*) FROM mccbs), (SELECT count(*) FROM mcbs), (SELECT count(*) FROM circuits)`).
		Scan(&nb, &nbo, &nmccb, &nmcb, &nc); err != nil {
		return nil, nil, err
	}
	totals["buildings"], totals["boards"], totals["mccbs"], totals["mcbs"], totals["circuits"] = nb, nbo, nmccb, nmcb, nc
	return out, totals, rows.Err()
}

// loadTrees loads boards (optionally a single one) with their full breaker trees.
func (s *Store) loadTrees(ctx context.Context, boardID int64) ([]model.BoardDetail, error) {
	pool, err := s.getPool()
	if err != nil {
		return nil, err
	}
	where := ""
	var args []any
	if boardID > 0 {
		where = "WHERE bo.id = $1"
		args = append(args, boardID)
	}
	rows, err := pool.Query(ctx, `SELECT bo.id, bo.building_id, bo.code, bo.voltage, bo.phases, bo.level, bo.location,
		bo.technician, bo.position, bo.created_at, bo.updated_at, b.name
		FROM boards bo JOIN buildings b ON b.id = bo.building_id `+where+` ORDER BY bo.building_id, bo.position, bo.code`, args...)
	if err != nil {
		return nil, err
	}
	var boards []model.BoardDetail
	index := map[int64]int{}
	for rows.Next() {
		var d model.BoardDetail
		if err := rows.Scan(&d.ID, &d.BuildingID, &d.Code, &d.Voltage, &d.Phases, &d.Level, &d.Location,
			&d.Technician, &d.Position, &d.CreatedAt, &d.UpdatedAt, &d.BuildingName); err != nil {
			rows.Close()
			return nil, err
		}
		d.MCCBs = []model.MCCB{}
		index[d.ID] = len(boards)
		boards = append(boards, d)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(boards) == 0 {
		return []model.BoardDetail{}, nil
	}

	ids := make([]int64, 0, len(boards))
	for _, b := range boards {
		ids = append(ids, b.ID)
	}

	// MCCBs
	mrows, err := pool.Query(ctx, `SELECT id, board_id, name, rating_a, position FROM mccbs WHERE board_id = ANY($1) ORDER BY position, name`, ids)
	if err != nil {
		return nil, err
	}
	mccbIndex := map[int64][2]int{} // mccb id -> (board idx, mccb idx)
	for mrows.Next() {
		var m model.MCCB
		if err := mrows.Scan(&m.ID, &m.BoardID, &m.Name, &m.RatingA, &m.Position); err != nil {
			mrows.Close()
			return nil, err
		}
		m.MCBs = []model.MCB{}
		bi := index[m.BoardID]
		boards[bi].MCCBs = append(boards[bi].MCCBs, m)
		mccbIndex[m.ID] = [2]int{bi, len(boards[bi].MCCBs) - 1}
	}
	mrows.Close()
	if err := mrows.Err(); err != nil {
		return nil, err
	}

	// MCBs
	brows, err := pool.Query(ctx, `SELECT m.id, m.mccb_id, m.name, m.rating_a, m.position FROM mcbs m
		JOIN mccbs mm ON mm.id = m.mccb_id WHERE mm.board_id = ANY($1) ORDER BY m.position, m.name`, ids)
	if err != nil {
		return nil, err
	}
	mcbIndex := map[int64][3]int{}
	for brows.Next() {
		var mb model.MCB
		if err := brows.Scan(&mb.ID, &mb.MCCBID, &mb.Name, &mb.RatingA, &mb.Position); err != nil {
			brows.Close()
			return nil, err
		}
		mb.Circuits = []model.Circuit{}
		p := mccbIndex[mb.MCCBID]
		mccb := &boards[p[0]].MCCBs[p[1]]
		mccb.MCBs = append(mccb.MCBs, mb)
		mcbIndex[mb.ID] = [3]int{p[0], p[1], len(mccb.MCBs) - 1}
	}
	brows.Close()
	if err := brows.Err(); err != nil {
		return nil, err
	}

	// Circuits
	crows, err := pool.Query(ctx, `SELECT c.id, c.mcb_id, c.name, c.code, c.load_a, c.status::text, c.equipment_count, c.service, c.notes,
		c.created_at, c.updated_at FROM circuits c JOIN mcbs m ON m.id = c.mcb_id JOIN mccbs mm ON mm.id = m.mccb_id
		WHERE mm.board_id = ANY($1) ORDER BY c.code`, ids)
	if err != nil {
		return nil, err
	}
	for crows.Next() {
		var c model.Circuit
		if err := crows.Scan(&c.ID, &c.MCBID, &c.Name, &c.Code, &c.LoadA, &c.Status, &c.EquipmentCount, &c.Service, &c.Notes,
			&c.CreatedAt, &c.UpdatedAt); err != nil {
			crows.Close()
			return nil, err
		}
		p := mcbIndex[c.MCBID]
		mcb := &boards[p[0]].MCCBs[p[1]].MCBs[p[2]]
		mcb.Circuits = append(mcb.Circuits, c)
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return nil, err
	}

	// Order everything the way an engineer reads a schedule - FAC1, FAC2, FAC10 -
	// rather than by when each row happened to be created, so a board added
	// today takes its place in the sequence instead of landing at the bottom.
	sort.SliceStable(boards, func(i, j int) bool {
		if boards[i].BuildingID != boards[j].BuildingID {
			return boards[i].BuildingID < boards[j].BuildingID
		}
		return model.NaturalLess(boards[i].Code, boards[j].Code)
	})
	for i := range boards {
		bd := &boards[i]
		sort.SliceStable(bd.MCCBs, func(a, b int) bool {
			return model.NaturalLess(bd.MCCBs[a].Name, bd.MCCBs[b].Name)
		})
		for j := range bd.MCCBs {
			m := &bd.MCCBs[j]
			sort.SliceStable(m.MCBs, func(a, b int) bool {
				return model.NaturalLess(m.MCBs[a].Name, m.MCBs[b].Name)
			})
			for k := range m.MCBs {
				mb := &m.MCBs[k]
				sort.SliceStable(mb.Circuits, func(a, b int) bool {
					return model.NaturalLess(mb.Circuits[a].Code, mb.Circuits[b].Code)
				})
			}
		}
	}

	st := s.Settings()
	for i := range boards {
		boards[i].Compute(st)
	}
	return boards, nil
}

// ListBoards returns summaries for every board.
func (s *Store) ListBoards(ctx context.Context) ([]model.BoardSummary, error) {
	trees, err := s.loadTrees(ctx, 0)
	if err != nil {
		return nil, err
	}
	out := make([]model.BoardSummary, 0, len(trees))
	for _, t := range trees {
		out = append(out, model.BoardSummary{
			Board:               t.Board,
			MCCBCount:           t.Totals.MCCBCount,
			MCBCount:            t.Totals.MCBCount,
			CircuitCount:        t.Totals.CircuitCount,
			MaintenanceCircuits: t.Totals.MaintenanceCircuits,
			TotalCurrent:        t.Totals.TotalCurrent,
			MCBCapacity:         t.Totals.MCBCapacity,
			UtilPct:             t.Totals.UtilPct,
			UtilLevel:           t.Totals.UtilLevel,
		})
	}
	return out, nil
}

// GetBoard returns one board's full tree.
func (s *Store) GetBoard(ctx context.Context, id int64) (*model.BoardDetail, error) {
	trees, err := s.loadTrees(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(trees) == 0 {
		return nil, ErrNotFound
	}
	return &trees[0], nil
}

// CircuitFilter narrows the explorer listing.
type CircuitFilter struct {
	BoardID    int64
	BuildingID int64
	Status     string
	Query      string
}

// ListCircuits returns flattened circuit rows for the explorer / CSV export.
func (s *Store) ListCircuits(ctx context.Context, f CircuitFilter) ([]model.CircuitRow, error) {
	pool, err := s.getPool()
	if err != nil {
		return nil, err
	}
	var conds []string
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, fmt.Sprintf(cond, len(args)))
	}
	if f.BoardID > 0 {
		add("bo.id = $%d", f.BoardID)
	}
	if f.BuildingID > 0 {
		add("b.id = $%d", f.BuildingID)
	}
	if model.ValidStatus(f.Status) {
		add("c.status = $%d::circuit_status", f.Status)
	}
	if q := strings.TrimSpace(f.Query); q != "" {
		add("(c.name ILIKE $%[1]d OR c.code ILIKE $%[1]d OR c.service ILIKE $%[1]d OR m.name ILIKE $%[1]d OR mm.name ILIKE $%[1]d OR bo.code ILIKE $%[1]d)", "%"+q+"%")
	}
	where := ""
	if len(conds) > 0 {
		where = "WHERE " + strings.Join(conds, " AND ")
	}
	rows, err := pool.Query(ctx, `SELECT c.id, c.mcb_id, c.name, c.code, c.load_a, c.status::text, c.equipment_count, c.service, c.notes,
		c.created_at, c.updated_at, m.name, m.rating_a, mm.id, mm.name, bo.id, bo.code, b.id, b.name
		FROM circuits c JOIN mcbs m ON m.id = c.mcb_id JOIN mccbs mm ON mm.id = m.mccb_id
		JOIN boards bo ON bo.id = mm.board_id JOIN buildings b ON b.id = bo.building_id `+where+`
		ORDER BY bo.code, mm.position, m.position, c.code`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	st := s.Settings()
	out := []model.CircuitRow{}
	for rows.Next() {
		var r model.CircuitRow
		if err := rows.Scan(&r.ID, &r.MCBID, &r.Name, &r.Code, &r.LoadA, &r.Status, &r.EquipmentCount, &r.Service, &r.Notes,
			&r.CreatedAt, &r.UpdatedAt, &r.MCBName, &r.MCBRating, &r.MCCBID, &r.MCCBName, &r.BoardID, &r.BoardCode, &r.BuildingID, &r.BuildingName); err != nil {
			return nil, err
		}
		r.Capacity = st.Capacity(r.MCBRating)
		r.Pct = st.Percent(r.LoadA, r.Capacity)
		r.Level = st.Level(r.Pct)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].BoardCode != out[j].BoardCode {
			return model.NaturalLess(out[i].BoardCode, out[j].BoardCode)
		}
		if out[i].MCCBName != out[j].MCCBName {
			return model.NaturalLess(out[i].MCCBName, out[j].MCCBName)
		}
		if out[i].MCBName != out[j].MCBName {
			return model.NaturalLess(out[i].MCBName, out[j].MCBName)
		}
		return model.NaturalLess(out[i].Code, out[j].Code)
	})
	return out, nil
}

// Search finds boards, breakers and circuits whose names or codes match q.
func (s *Store) Search(ctx context.Context, q string, limit int) ([]model.SearchHit, error) {
	pool, err := s.getPool()
	if err != nil {
		return nil, err
	}
	q = strings.TrimSpace(q)
	if q == "" {
		return []model.SearchHit{}, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	pat := "%" + q + "%"
	rows, err := pool.Query(ctx, `
		SELECT * FROM (
		  SELECT 'building' AS kind, b.id, 0::bigint AS board_id, b.name AS title, (SELECT count(*)::text || ' boards' FROM boards WHERE building_id = b.id) AS subtitle, 1 AS rank
		    FROM buildings b WHERE b.name ILIKE $1
		  UNION ALL
		  SELECT 'board', bo.id, bo.id, bo.code, bo.voltage || ' ' || bo.phases || ' · ' || b.name, 2
		    FROM boards bo JOIN buildings b ON b.id = bo.building_id WHERE bo.code ILIKE $1 OR bo.location ILIKE $1
		  UNION ALL
		  SELECT 'mccb', mm.id, mm.board_id, mm.name, bo.code || ' · ' || mm.rating_a::int || ' A', 3
		    FROM mccbs mm JOIN boards bo ON bo.id = mm.board_id WHERE mm.name ILIKE $1
		  UNION ALL
		  SELECT 'mcb', m.id, mm.board_id, m.name, bo.code || ' · ' || mm.name || ' · ' || m.rating_a::int || ' A', 4
		    FROM mcbs m JOIN mccbs mm ON mm.id = m.mccb_id JOIN boards bo ON bo.id = mm.board_id WHERE m.name ILIKE $1
		  UNION ALL
		  SELECT 'circuit', c.id, mm.board_id, c.name, c.code || ' · ' || bo.code || ' / ' || mm.name || ' / ' || m.name, 5
		    FROM circuits c JOIN mcbs m ON m.id = c.mcb_id JOIN mccbs mm ON mm.id = m.mccb_id JOIN boards bo ON bo.id = mm.board_id
		    WHERE c.name ILIKE $1 OR c.code ILIKE $1 OR c.service ILIKE $1
		) x ORDER BY rank, title LIMIT $2`, pat, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.SearchHit{}
	for rows.Next() {
		var h model.SearchHit
		var rank int
		if err := rows.Scan(&h.Kind, &h.ID, &h.BoardID, &h.Title, &h.Subtitle, &rank); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// Audit lists the most recent audit entries.
func (s *Store) Audit(ctx context.Context, limit int) ([]model.AuditEntry, error) {
	pool, err := s.getPool()
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := pool.Query(ctx, `SELECT id, at, actor_role, action, entity, coalesce(entity_id, 0), summary FROM audit_log ORDER BY id DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.AuditEntry{}
	for rows.Next() {
		var e model.AuditEntry
		if err := rows.Scan(&e.ID, &e.At, &e.ActorRole, &e.Action, &e.Entity, &e.EntityID, &e.Summary); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------- writes

func (s *Store) audit(ctx context.Context, tx pgx.Tx, role, action, entity string, id int64, summary string) error {
	_, err := tx.Exec(ctx, `INSERT INTO audit_log (actor_role, action, entity, entity_id, summary) VALUES ($1,$2,$3,$4,$5)`,
		role, action, entity, id, summary)
	return err
}

// withTx runs fn inside a transaction and translates constraint errors.
func (s *Store) withTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	pool, err := s.getPool()
	if err != nil {
		return err
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return translate(err)
	}
	return tx.Commit(ctx)
}

func translate(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			switch pgErr.ConstraintName {
			case "buildings_name_key":
				return &UserError{"A building with that name already exists."}
			case "boards_code_key":
				return &UserError{"A board with that code already exists."}
			case "circuits_code_key":
				return &UserError{"A circuit with that code already exists."}
			case "mccbs_board_id_name_key":
				return &UserError{"An MCCB with that name already exists on this board."}
			case "mcbs_mccb_id_name_key":
				return &UserError{"An MCB with that name already exists under this MCCB."}
			case "hv_feeders_network_id_name_key":
				return &UserError{"A feeder with that name already exists."}
			case "hv_ways_feeder_id_name_key":
				return &UserError{"A way with that name already exists on this feeder."}
			}
			return &UserError{"Duplicate value: " + pgErr.Detail}
		case "23503":
			return &UserError{"Referenced parent does not exist."}
		case "23514":
			return &UserError{"Value out of range: " + pgErr.ConstraintName}
		}
	}
	return err
}

// Buildings ---------------------------------------------------------------

func (s *Store) CreateBuilding(ctx context.Context, role string, b model.Building) (int64, error) {
	var id int64
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO buildings (name, icon) VALUES ($1, $2) RETURNING id`, b.Name, b.Icon).Scan(&id); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "create", "building", id, "Added building "+b.Name)
	})
	return id, err
}

func (s *Store) UpdateBuilding(ctx context.Context, role string, b model.Building) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE buildings SET name = $2, icon = $3, updated_at = now() WHERE id = $1`, b.ID, b.Name, b.Icon)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return s.audit(ctx, tx, role, "update", "building", b.ID, "Updated building "+b.Name)
	})
}

func (s *Store) DeleteBuilding(ctx context.Context, role string, id int64) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var name string
		if err := tx.QueryRow(ctx, `DELETE FROM buildings WHERE id = $1 RETURNING name`, id).Scan(&name); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		return s.audit(ctx, tx, role, "delete", "building", id, "Deleted building "+name+" and everything beneath it")
	})
}

// Boards ------------------------------------------------------------------

func (s *Store) CreateBoard(ctx context.Context, role string, b model.Board) (int64, error) {
	var id int64
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO boards (building_id, code, voltage, phases, level, location, technician, position)
			VALUES ($1,$2,$3,$4,$5,$6,$7, (SELECT coalesce(max(position),-1)+1 FROM boards WHERE building_id = $1)) RETURNING id`,
			b.BuildingID, b.Code, b.Voltage, b.Phases, b.Level, b.Location, b.Technician).Scan(&id); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "create", "board", id, "Added board "+b.Code)
	})
	return id, err
}

func (s *Store) UpdateBoard(ctx context.Context, role string, b model.Board) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE boards SET building_id = $2, code = $3, voltage = $4, phases = $5, level = $6, location = $7,
			technician = $8, updated_at = now() WHERE id = $1`,
			b.ID, b.BuildingID, b.Code, b.Voltage, b.Phases, b.Level, b.Location, b.Technician)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return s.audit(ctx, tx, role, "update", "board", b.ID, "Updated board "+b.Code)
	})
}

func (s *Store) DeleteBoard(ctx context.Context, role string, id int64) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var code string
		if err := tx.QueryRow(ctx, `DELETE FROM boards WHERE id = $1 RETURNING code`, id).Scan(&code); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		return s.audit(ctx, tx, role, "delete", "board", id, "Deleted board "+code+" and everything beneath it")
	})
}

// MCCBs -------------------------------------------------------------------

func (s *Store) CreateMCCB(ctx context.Context, role string, m model.MCCB) (int64, error) {
	var id int64
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO mccbs (board_id, name, rating_a, position)
			VALUES ($1,$2,$3,(SELECT coalesce(max(position),-1)+1 FROM mccbs WHERE board_id = $1)) RETURNING id`,
			m.BoardID, m.Name, m.RatingA).Scan(&id); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "create", "mccb", id, fmt.Sprintf("Added %s (%g A)", m.Name, m.RatingA))
	})
	return id, err
}

func (s *Store) UpdateMCCB(ctx context.Context, role string, m model.MCCB) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE mccbs SET name = $2, rating_a = $3, updated_at = now() WHERE id = $1`, m.ID, m.Name, m.RatingA)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return s.audit(ctx, tx, role, "update", "mccb", m.ID, fmt.Sprintf("Updated %s (%g A)", m.Name, m.RatingA))
	})
}

func (s *Store) DeleteMCCB(ctx context.Context, role string, id int64) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var name string
		if err := tx.QueryRow(ctx, `DELETE FROM mccbs WHERE id = $1 RETURNING name`, id).Scan(&name); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		return s.audit(ctx, tx, role, "delete", "mccb", id, "Deleted "+name+" and its MCBs and circuits")
	})
}

// MCBs --------------------------------------------------------------------

func (s *Store) CreateMCB(ctx context.Context, role string, m model.MCB) (int64, error) {
	var id int64
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO mcbs (mccb_id, name, rating_a, position)
			VALUES ($1,$2,$3,(SELECT coalesce(max(position),-1)+1 FROM mcbs WHERE mccb_id = $1)) RETURNING id`,
			m.MCCBID, m.Name, m.RatingA).Scan(&id); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "create", "mcb", id, fmt.Sprintf("Added %s (%g A)", m.Name, m.RatingA))
	})
	return id, err
}

// UpdateMCB renames, re-rates and optionally re-parents an MCB. A zero MCCBID
// leaves the MCB where it is, so callers that only rename need not know it.
func (s *Store) UpdateMCB(ctx context.Context, role string, m model.MCB) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var oldParent, newParent string
		err := tx.QueryRow(ctx, `SELECT old.name, new.name FROM mcbs mb
			JOIN mccbs old ON old.id = mb.mccb_id
			JOIN mccbs new ON new.id = coalesce(nullif($2::bigint, 0), mb.mccb_id)
			WHERE mb.id = $1`, m.ID, m.MCCBID).Scan(&oldParent, &newParent)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		tag, err := tx.Exec(ctx, `UPDATE mcbs SET mccb_id = coalesce(nullif($2::bigint, 0), mccb_id),
			name = $3, rating_a = $4, updated_at = now() WHERE id = $1`, m.ID, m.MCCBID, m.Name, m.RatingA)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		summary := fmt.Sprintf("Updated %s (%g A)", m.Name, m.RatingA)
		if oldParent != newParent {
			summary = fmt.Sprintf("Moved %s (%g A) from %s to %s", m.Name, m.RatingA, oldParent, newParent)
		}
		return s.audit(ctx, tx, role, "update", "mcb", m.ID, summary)
	})
}

func (s *Store) DeleteMCB(ctx context.Context, role string, id int64) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var name string
		if err := tx.QueryRow(ctx, `DELETE FROM mcbs WHERE id = $1 RETURNING name`, id).Scan(&name); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		return s.audit(ctx, tx, role, "delete", "mcb", id, "Deleted "+name+" and its circuits")
	})
}

// Circuits ----------------------------------------------------------------

// NextCircuitCode suggests a code like FAC5-MCB01-C003 for a new circuit on an MCB.
func (s *Store) NextCircuitCode(ctx context.Context, mcbID int64) (string, error) {
	pool, err := s.getPool()
	if err != nil {
		return "", err
	}
	var boardCode, mcbName string
	var n int
	err = pool.QueryRow(ctx, `SELECT bo.code, m.name, (SELECT count(*) FROM circuits WHERE mcb_id = m.id)
		FROM mcbs m JOIN mccbs mm ON mm.id = m.mccb_id JOIN boards bo ON bo.id = mm.board_id WHERE m.id = $1`, mcbID).
		Scan(&boardCode, &mcbName, &n)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	compact := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(mcbName, "-", ""), " ", ""))
	for i := n + 1; i < n+1000; i++ {
		code := fmt.Sprintf("%s-%s-C%03d", strings.ToUpper(boardCode), compact, i)
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM circuits WHERE code = $1)`, code).Scan(&exists); err != nil {
			return "", err
		}
		if !exists {
			return code, nil
		}
	}
	return "", &UserError{"Could not generate a unique circuit code."}
}

func (s *Store) CreateCircuit(ctx context.Context, role string, c model.Circuit) (int64, error) {
	var id int64
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO circuits (mcb_id, name, code, load_a, status, equipment_count, service, notes)
			VALUES ($1,$2,$3,$4,$5::circuit_status,$6,$7,$8) RETURNING id`,
			c.MCBID, c.Name, c.Code, c.LoadA, c.Status, c.EquipmentCount, c.Service, c.Notes).Scan(&id); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "create", "circuit", id, fmt.Sprintf("Added circuit %s (%s, %g A)", c.Name, c.Code, c.LoadA))
	})
	return id, err
}

func (s *Store) UpdateCircuit(ctx context.Context, role string, c model.Circuit) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE circuits SET mcb_id = $2, name = $3, code = $4, load_a = $5, status = $6::circuit_status,
			equipment_count = $7, service = $8, notes = $9, updated_at = now() WHERE id = $1`,
			c.ID, c.MCBID, c.Name, c.Code, c.LoadA, c.Status, c.EquipmentCount, c.Service, c.Notes)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return s.audit(ctx, tx, role, "update", "circuit", c.ID, fmt.Sprintf("Updated circuit %s (%s, %g A, %s)", c.Name, c.Code, c.LoadA, c.Status))
	})
}

func (s *Store) DeleteCircuit(ctx context.Context, role string, id int64) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var name, code string
		if err := tx.QueryRow(ctx, `DELETE FROM circuits WHERE id = $1 RETURNING name, code`, id).Scan(&name, &code); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		return s.audit(ctx, tx, role, "delete", "circuit", id, "Deleted circuit "+name+" ("+code+")")
	})
}
