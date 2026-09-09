// Package api exposes the store over a small JSON HTTP API consumed by the
// embedded web UI. The server only ever listens on the loopback interface.
package api

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/superforcesuytr-droid/power-distribution-system/internal/config"
	"github.com/superforcesuytr-droid/power-distribution-system/internal/db"
	"github.com/superforcesuytr-droid/power-distribution-system/internal/model"
)

// Server bundles the dependencies the handlers need.
type Server struct {
	Store   *db.Store
	Config  *config.Config
	Static  fs.FS
	Version string
	LogPath string
	Dev     bool

	// windows counts the interface windows currently on screen, and seen
	// records whether one has ever connected. A window holds an event-stream
	// connection open for as long as it is displayed, which is what lets the
	// application know when it has been closed. That cannot be inferred from
	// the browser process itself: the browser showing the window is often one
	// the operator already had running, so it neither starts nor exits with us.
	windows atomic.Int64
	seen    atomic.Bool
}

// Windows reports how many interface windows are open, and whether one has
// ever connected.
func (s *Server) Windows() (live int64, seen bool) {
	return s.windows.Load(), s.seen.Load()
}

// Handler builds the router.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Meta
	mux.HandleFunc("GET /api/status", s.handleStatus)
	mux.HandleFunc("GET /api/session", s.handleSession)
	mux.HandleFunc("GET /api/setup", s.handleSetupGet)
	mux.HandleFunc("POST /api/setup/test", s.handleSetupTest)
	mux.HandleFunc("POST /api/setup", s.handleSetupSave)
	mux.HandleFunc("GET /api/settings", s.handleSettingsGet)
	mux.HandleFunc("PUT /api/settings", s.requireRole(model.RoleSupervisor, s.handleSettingsPut))

	// Reads
	mux.HandleFunc("GET /api/overview", s.handleOverview)
	mux.HandleFunc("GET /api/boards", s.handleBoards)
	mux.HandleFunc("GET /api/boards/{id}", s.handleBoard)
	mux.HandleFunc("GET /api/circuits", s.handleCircuits)
	mux.HandleFunc("GET /api/circuits.csv", s.handleCircuitsCSV)
	mux.HandleFunc("GET /api/mcbs/{id}/next-code", s.handleNextCode)
	mux.HandleFunc("GET /api/search", s.handleSearch)
	mux.HandleFunc("GET /api/audit", s.handleAudit)

	// Writes. Buildings and boards are structural, so only supervisors may
	// touch them. Technicians may add/edit breakers and circuits but never
	// delete anything.
	mux.HandleFunc("POST /api/buildings", s.requireRole(model.RoleSupervisor, s.handleBuildingCreate))
	mux.HandleFunc("PUT /api/buildings/{id}", s.requireRole(model.RoleSupervisor, s.handleBuildingUpdate))
	mux.HandleFunc("DELETE /api/buildings/{id}", s.requireRole(model.RoleSupervisor, s.handleBuildingDelete))

	mux.HandleFunc("POST /api/boards", s.requireRole(model.RoleSupervisor, s.handleBoardCreate))
	mux.HandleFunc("PUT /api/boards/{id}", s.requireRole(model.RoleSupervisor, s.handleBoardUpdate))
	mux.HandleFunc("DELETE /api/boards/{id}", s.requireRole(model.RoleSupervisor, s.handleBoardDelete))

	mux.HandleFunc("POST /api/mccbs", s.requireRole(model.RoleTechnician, s.handleMCCBCreate))
	mux.HandleFunc("PUT /api/mccbs/{id}", s.requireRole(model.RoleTechnician, s.handleMCCBUpdate))
	mux.HandleFunc("DELETE /api/mccbs/{id}", s.requireRole(model.RoleSupervisor, s.handleMCCBDelete))

	mux.HandleFunc("POST /api/mcbs", s.requireRole(model.RoleTechnician, s.handleMCBCreate))
	mux.HandleFunc("PUT /api/mcbs/{id}", s.requireRole(model.RoleTechnician, s.handleMCBUpdate))
	mux.HandleFunc("DELETE /api/mcbs/{id}", s.requireRole(model.RoleSupervisor, s.handleMCBDelete))

	mux.HandleFunc("POST /api/circuits", s.requireRole(model.RoleTechnician, s.handleCircuitCreate))
	mux.HandleFunc("PUT /api/circuits/{id}", s.requireRole(model.RoleTechnician, s.handleCircuitUpdate))
	mux.HandleFunc("DELETE /api/circuits/{id}", s.requireRole(model.RoleSupervisor, s.handleCircuitDelete))

	s.routesHV(mux)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "unknown API route")
	})

	// Static UI: everything else serves the embedded single page app.
	fileServer := http.FileServer(http.FS(s.Static))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if _, err := fs.Stat(s.Static, path); err != nil {
			// Unknown path: hash-routed SPA, serve index.
			r.URL.Path = "/"
		}
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	})

	return s.logging(noCache(mux))
}

// ---------------------------------------------------------------- helpers

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		// The session stream stays open for the life of the window, so logging
		// it on completion would only ever record its teardown.
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/session" {
			log.Printf("%s %s -> %d (%s)", r.Method, r.URL.RequestURI(), rec.status, time.Since(start).Round(time.Millisecond))
		}
	})
}

func noCache(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// Flush keeps the wrapper transparent to streaming responses.
func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// handleSession holds a connection open for as long as the interface window
// is on screen. The application watches the number of open sessions to decide
// when its window has gone and it should stop.
func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming is not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	s.windows.Add(1)
	s.seen.Store(true)
	defer s.windows.Add(-1)

	fmt.Fprint(w, ": open\n\n")
	flusher.Flush()

	ticker := time.NewTicker(20 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func roleOf(r *http.Request) string {
	role := strings.ToLower(strings.TrimSpace(r.Header.Get("X-Role")))
	if _, ok := model.RoleRank[role]; !ok {
		return model.RoleViewer
	}
	return role
}

func (s *Server) requireRole(min string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		role := roleOf(r)
		if model.RoleRank[role] < model.RoleRank[min] {
			writeError(w, http.StatusForbidden, fmt.Sprintf("This action requires the %s role (current role: %s).", min, role))
			return
		}
		h(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

// fail maps store errors to HTTP statuses.
func fail(w http.ResponseWriter, err error) {
	var ue *db.UserError
	switch {
	case errors.Is(err, db.ErrNotReady):
		writeError(w, http.StatusServiceUnavailable, "Database is not connected. Open Settings to configure the connection.")
	case errors.Is(err, db.ErrNotFound):
		writeError(w, http.StatusNotFound, "Not found.")
	case errors.As(err, &ue):
		writeError(w, http.StatusBadRequest, ue.Msg)
	default:
		log.Printf("error: %v", err)
		writeError(w, http.StatusInternalServerError, "Unexpected error: "+err.Error())
	}
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return &db.UserError{Msg: "Invalid request body: " + err.Error()}
	}
	return nil
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, &db.UserError{Msg: "Invalid id."}
	}
	return id, nil
}

func queryInt64(r *http.Request, key string) int64 {
	v, _ := strconv.ParseInt(r.URL.Query().Get(key), 10, 64)
	return v
}

func required(field, value string) error {
	if strings.TrimSpace(value) == "" {
		return &db.UserError{Msg: field + " is required."}
	}
	return nil
}

func ctx(r *http.Request) context.Context { return r.Context() }

// ---------------------------------------------------------------- meta

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	dsn, fromEnv := s.Config.EffectiveDSN()
	writeJSON(w, 200, map[string]any{
		"version":      s.Version,
		"db":           s.Store.Status(),
		"settings":     s.Store.Settings(),
		"config_path":  s.Config.Path(),
		"log_path":     s.LogPath,
		"dsn_from_env": fromEnv && dsn != "",
		"dev":          s.Dev,
		"role":         roleOf(r),
		"server_time":  time.Now(),
	})
}

func (s *Server) handleSetupGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{
		"database":    s.Config.Database.Redacted(),
		"config_path": s.Config.Path(),
		"status":      s.Store.Status(),
	})
}

type setupRequest struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Database string `json:"database"`
	User     string `json:"user"`
	Password string `json:"password"`
	SSLMode  string `json:"sslmode"`
}

func (s *Server) setupToConfig(req setupRequest) (config.Database, error) {
	d := config.Database{
		Host: strings.TrimSpace(req.Host), Port: req.Port, Name: strings.TrimSpace(req.Database),
		User: strings.TrimSpace(req.User), Password: req.Password, SSLMode: strings.TrimSpace(req.SSLMode),
	}
	if d.Port == 0 {
		d.Port = 5432
	}
	if d.SSLMode == "" {
		d.SSLMode = "prefer"
	}
	// Keep the saved password when the form leaves it blank (edit flow).
	if d.Password == "" && d.Host == s.Config.Database.Host && d.User == s.Config.Database.User {
		d.Password = s.Config.Database.Password
	}
	if err := required("Host", d.Host); err != nil {
		return d, err
	}
	if err := required("Database", d.Name); err != nil {
		return d, err
	}
	if err := required("User", d.User); err != nil {
		return d, err
	}
	switch d.SSLMode {
	case "disable", "allow", "prefer", "require", "verify-ca", "verify-full":
	default:
		return d, &db.UserError{Msg: "Invalid SSL mode."}
	}
	return d, nil
}

func (s *Server) handleSetupTest(w http.ResponseWriter, r *http.Request) {
	var req setupRequest
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	d, err := s.setupToConfig(req)
	if err != nil {
		fail(w, err)
		return
	}
	ver, err := db.Test(ctx(r), d.DSN())
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "server": ver})
}

func (s *Server) handleSetupSave(w http.ResponseWriter, r *http.Request) {
	var req setupRequest
	if err := decode(r, &req); err != nil {
		fail(w, err)
		return
	}
	d, err := s.setupToConfig(req)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.Connect(ctx(r), d.DSN()); err != nil {
		fail(w, err)
		return
	}
	s.Config.Database = d
	if err := s.Config.Save(); err != nil {
		log.Printf("warning: could not save config: %v", err)
		writeJSON(w, 200, map[string]any{"ok": true, "status": s.Store.Status(),
			"warning": "Connected, but the settings could not be saved: " + err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "status": s.Store.Status(), "config_path": s.Config.Path()})
}

func (s *Server) handleSettingsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.Store.Settings())
}

func (s *Server) handleSettingsPut(w http.ResponseWriter, r *http.Request) {
	var st model.Settings
	if err := decode(r, &st); err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.UpdateSettings(ctx(r), st); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, s.Store.Settings())
}

// ---------------------------------------------------------------- reads

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	buildings, totals, err := s.Store.Overview(ctx(r))
	if err != nil {
		fail(w, err)
		return
	}
	boards, err := s.Store.ListBoards(ctx(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"buildings": buildings, "boards": boards, "totals": totals, "settings": s.Store.Settings()})
}

func (s *Server) handleBoards(w http.ResponseWriter, r *http.Request) {
	boards, err := s.Store.ListBoards(ctx(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, boards)
}

func (s *Server) handleBoard(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	d, err := s.Store.GetBoard(ctx(r), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, d)
}

func circuitFilter(r *http.Request) db.CircuitFilter {
	q := r.URL.Query()
	return db.CircuitFilter{
		BoardID:    queryInt64(r, "board_id"),
		BuildingID: queryInt64(r, "building_id"),
		Status:     q.Get("status"),
		Query:      q.Get("q"),
	}
}

func (s *Server) handleCircuits(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.ListCircuits(ctx(r), circuitFilter(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, rows)
}

func (s *Server) handleCircuitsCSV(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Store.ListCircuits(ctx(r), circuitFilter(r))
	if err != nil {
		fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="circuits-`+time.Now().Format("2006-01-02")+`.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"Building", "Board", "MCCB", "MCB", "MCB rating (A)", "Circuit", "Code", "Load (A)", "Capacity (A)", "Utilisation (%)", "Status", "Equipment", "Service", "Notes", "Updated"})
	for _, c := range rows {
		_ = cw.Write([]string{c.BuildingName, c.BoardCode, c.MCCBName, c.MCBName, fmtA(c.MCBRating), c.Name, c.Code, fmtA(c.LoadA),
			fmtA(c.Capacity), strconv.Itoa(c.Pct), c.Status, strconv.Itoa(c.EquipmentCount), c.Service, c.Notes, c.UpdatedAt.Format(time.RFC3339)})
	}
	cw.Flush()
}

func fmtA(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func (s *Server) handleNextCode(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	code, err := s.Store.NextCircuitCode(ctx(r), id)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]string{"code": code})
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	hits, err := s.Store.Search(ctx(r), r.URL.Query().Get("q"), int(queryInt64(r, "limit")))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, hits)
}

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	entries, err := s.Store.Audit(ctx(r), int(queryInt64(r, "limit")))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, entries)
}

// ---------------------------------------------------------------- writes

type buildingInput struct {
	Name string `json:"name"`
	Icon string `json:"icon"`
}

func (s *Server) handleBuildingCreate(w http.ResponseWriter, r *http.Request) {
	var in buildingInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := required("Name", in.Name); err != nil {
		fail(w, err)
		return
	}
	if in.Icon == "" {
		in.Icon = "🏭"
	}
	id, err := s.Store.CreateBuilding(ctx(r), roleOf(r), model.Building{Name: strings.TrimSpace(in.Name), Icon: in.Icon})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, map[string]int64{"id": id})
}

func (s *Server) handleBuildingUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var in buildingInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := required("Name", in.Name); err != nil {
		fail(w, err)
		return
	}
	if in.Icon == "" {
		in.Icon = "🏭"
	}
	if err := s.Store.UpdateBuilding(ctx(r), roleOf(r), model.Building{ID: id, Name: strings.TrimSpace(in.Name), Icon: in.Icon}); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleBuildingDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.DeleteBuilding(ctx(r), roleOf(r), id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

type boardInput struct {
	BuildingID int64  `json:"building_id"`
	Code       string `json:"code"`
	Voltage    string `json:"voltage"`
	Phases     string `json:"phases"`
	Level      string `json:"level"`
	Location   string `json:"location"`
	Technician string `json:"technician"`
}

func (in boardInput) toModel(id int64) (model.Board, error) {
	b := model.Board{
		ID: id, BuildingID: in.BuildingID, Code: strings.ToUpper(strings.TrimSpace(in.Code)),
		Voltage: strings.TrimSpace(in.Voltage), Phases: strings.TrimSpace(in.Phases),
		Level: strings.TrimSpace(in.Level), Location: strings.TrimSpace(in.Location), Technician: strings.TrimSpace(in.Technician),
	}
	if b.BuildingID <= 0 {
		return b, &db.UserError{Msg: "Building is required."}
	}
	if err := required("Board code", b.Code); err != nil {
		return b, err
	}
	if b.Voltage == "" {
		b.Voltage = "400V"
	}
	if b.Phases == "" {
		b.Phases = "3PH"
	}
	return b, nil
}

func (s *Server) handleBoardCreate(w http.ResponseWriter, r *http.Request) {
	var in boardInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	b, err := in.toModel(0)
	if err != nil {
		fail(w, err)
		return
	}
	id, err := s.Store.CreateBoard(ctx(r), roleOf(r), b)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, map[string]int64{"id": id})
}

func (s *Server) handleBoardUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var in boardInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	b, err := in.toModel(id)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.UpdateBoard(ctx(r), roleOf(r), b); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleBoardDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.DeleteBoard(ctx(r), roleOf(r), id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

type breakerInput struct {
	BoardID int64   `json:"board_id,omitempty"`
	MCCBID  int64   `json:"mccb_id,omitempty"`
	Name    string  `json:"name"`
	RatingA float64 `json:"rating_a"`
}

func (in breakerInput) validate(kind string) error {
	if err := required(kind+" name", in.Name); err != nil {
		return err
	}
	if in.RatingA <= 0 || in.RatingA > 10000 {
		return &db.UserError{Msg: "Rating must be between 0 and 10000 A."}
	}
	return nil
}

func (s *Server) handleMCCBCreate(w http.ResponseWriter, r *http.Request) {
	var in breakerInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := in.validate("MCCB"); err != nil {
		fail(w, err)
		return
	}
	if in.BoardID <= 0 {
		fail(w, &db.UserError{Msg: "Board is required."})
		return
	}
	id, err := s.Store.CreateMCCB(ctx(r), roleOf(r), model.MCCB{BoardID: in.BoardID, Name: strings.TrimSpace(in.Name), RatingA: in.RatingA})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, map[string]int64{"id": id})
}

func (s *Server) handleMCCBUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var in breakerInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := in.validate("MCCB"); err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.UpdateMCCB(ctx(r), roleOf(r), model.MCCB{ID: id, Name: strings.TrimSpace(in.Name), RatingA: in.RatingA}); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleMCCBDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.DeleteMCCB(ctx(r), roleOf(r), id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleMCBCreate(w http.ResponseWriter, r *http.Request) {
	var in breakerInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := in.validate("MCB"); err != nil {
		fail(w, err)
		return
	}
	if in.MCCBID <= 0 {
		fail(w, &db.UserError{Msg: "MCCB is required."})
		return
	}
	id, err := s.Store.CreateMCB(ctx(r), roleOf(r), model.MCB{MCCBID: in.MCCBID, Name: strings.TrimSpace(in.Name), RatingA: in.RatingA})
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, map[string]int64{"id": id})
}

func (s *Server) handleMCBUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var in breakerInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := in.validate("MCB"); err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.UpdateMCB(ctx(r), roleOf(r), model.MCB{ID: id, MCCBID: in.MCCBID, Name: strings.TrimSpace(in.Name), RatingA: in.RatingA}); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleMCBDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.DeleteMCB(ctx(r), roleOf(r), id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

type circuitInput struct {
	MCBID          int64   `json:"mcb_id"`
	Name           string  `json:"name"`
	Code           string  `json:"code"`
	LoadA          float64 `json:"load_a"`
	Status         string  `json:"status"`
	EquipmentCount int     `json:"equipment_count"`
	Service        string  `json:"service"`
	Notes          string  `json:"notes"`
}

func (in circuitInput) toModel(id int64) (model.Circuit, error) {
	c := model.Circuit{
		ID: id, MCBID: in.MCBID, Name: strings.TrimSpace(in.Name), Code: strings.ToUpper(strings.TrimSpace(in.Code)),
		LoadA: in.LoadA, Status: strings.ToLower(strings.TrimSpace(in.Status)), EquipmentCount: in.EquipmentCount,
		Service: strings.TrimSpace(in.Service), Notes: strings.TrimSpace(in.Notes),
	}
	if c.MCBID <= 0 {
		return c, &db.UserError{Msg: "MCB is required."}
	}
	if err := required("Circuit name", c.Name); err != nil {
		return c, err
	}
	if err := required("Circuit code", c.Code); err != nil {
		return c, err
	}
	if c.LoadA < 0 || c.LoadA > 10000 {
		return c, &db.UserError{Msg: "Load must be between 0 and 10000 A."}
	}
	if c.Status == "" {
		c.Status = model.StatusActive
	}
	if !model.ValidStatus(c.Status) {
		return c, &db.UserError{Msg: "Status must be active, maintenance or inactive."}
	}
	if c.EquipmentCount < 0 {
		return c, &db.UserError{Msg: "Equipment count cannot be negative."}
	}
	return c, nil
}

func (s *Server) handleCircuitCreate(w http.ResponseWriter, r *http.Request) {
	var in circuitInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if strings.TrimSpace(in.Code) == "" && in.MCBID > 0 {
		code, err := s.Store.NextCircuitCode(ctx(r), in.MCBID)
		if err != nil {
			fail(w, err)
			return
		}
		in.Code = code
	}
	c, err := in.toModel(0)
	if err != nil {
		fail(w, err)
		return
	}
	id, err := s.Store.CreateCircuit(ctx(r), roleOf(r), c)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"id": id, "code": c.Code})
}

func (s *Server) handleCircuitUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var in circuitInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	c, err := in.toModel(id)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.UpdateCircuit(ctx(r), roleOf(r), c); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleCircuitDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.DeleteCircuit(ctx(r), roleOf(r), id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
