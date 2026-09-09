package api

import (
	"net/http"
	"strings"

	"github.com/superforcesuytr-droid/power-distribution-system/internal/db"
	"github.com/superforcesuytr-droid/power-distribution-system/internal/model"
)

// routesHV registers the high-voltage overview. Feeders and couplers are the
// shape of the site, so only supervisors may add or remove them; technicians
// may work on the outgoing ways.
func (s *Server) routesHV(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/hv", s.handleHVGet)
	mux.HandleFunc("PUT /api/hv", s.requireRole(model.RoleSupervisor, s.handleHVUpdate))

	mux.HandleFunc("POST /api/hv/feeders", s.requireRole(model.RoleSupervisor, s.handleHVFeederCreate))
	mux.HandleFunc("PUT /api/hv/feeders/{id}", s.requireRole(model.RoleSupervisor, s.handleHVFeederUpdate))
	mux.HandleFunc("DELETE /api/hv/feeders/{id}", s.requireRole(model.RoleSupervisor, s.handleHVFeederDelete))
	mux.HandleFunc("POST /api/hv/feeders/{id}/move", s.requireRole(model.RoleSupervisor, s.handleHVFeederMove))

	mux.HandleFunc("POST /api/hv/ways", s.requireRole(model.RoleTechnician, s.handleHVWayCreate))
	mux.HandleFunc("PUT /api/hv/ways/{id}", s.requireRole(model.RoleTechnician, s.handleHVWayUpdate))
	mux.HandleFunc("DELETE /api/hv/ways/{id}", s.requireRole(model.RoleSupervisor, s.handleHVWayDelete))

	mux.HandleFunc("POST /api/hv/devices", s.requireRole(model.RoleTechnician, s.handleHVDeviceCreate))
	mux.HandleFunc("PUT /api/hv/devices/{id}", s.requireRole(model.RoleTechnician, s.handleHVDeviceUpdate))
	mux.HandleFunc("DELETE /api/hv/devices/{id}", s.requireRole(model.RoleTechnician, s.handleHVDeviceDelete))
	mux.HandleFunc("POST /api/hv/devices/{id}/move", s.requireRole(model.RoleTechnician, s.handleHVDeviceMove))

	mux.HandleFunc("POST /api/hv/couplers", s.requireRole(model.RoleSupervisor, s.handleHVCouplerCreate))
	mux.HandleFunc("PUT /api/hv/couplers/{id}", s.requireRole(model.RoleSupervisor, s.handleHVCouplerUpdate))
	mux.HandleFunc("DELETE /api/hv/couplers/{id}", s.requireRole(model.RoleSupervisor, s.handleHVCouplerDelete))
}

func (s *Server) handleHVGet(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.HVNetwork(ctx(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, n)
}

type hvNetworkInput struct {
	Name    string `json:"name"`
	Voltage string `json:"voltage"`
}

func (s *Server) handleHVUpdate(w http.ResponseWriter, r *http.Request) {
	var in hvNetworkInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := required("Name", in.Name); err != nil {
		fail(w, err)
		return
	}
	n, err := s.Store.HVNetwork(ctx(r))
	if err != nil {
		fail(w, err)
		return
	}
	voltage := strings.TrimSpace(in.Voltage)
	if voltage == "" {
		voltage = n.Voltage
	}
	if err := s.Store.UpdateHVNetwork(ctx(r), roleOf(r), n.ID, strings.TrimSpace(in.Name), voltage); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// Feeders -----------------------------------------------------------------

type hvFeederInput struct {
	Name       string   `json:"name"`
	Switchgear string   `json:"switchgear"`
	Voltage    string   `json:"voltage"`
	Source     string   `json:"source"`
	RatingA    *float64 `json:"rating_a"`
}

func (in hvFeederInput) toModel(id, networkID int64, defVoltage string) (model.HVFeeder, error) {
	f := model.HVFeeder{
		ID: id, NetworkID: networkID,
		Name:       strings.ToUpper(strings.TrimSpace(in.Name)),
		Switchgear: strings.ToUpper(strings.TrimSpace(in.Switchgear)),
		Voltage:    strings.TrimSpace(in.Voltage),
		Source:     strings.TrimSpace(in.Source),
		RatingA:    in.RatingA,
	}
	if err := required("Feeder name", f.Name); err != nil {
		return f, err
	}
	if f.Voltage == "" {
		f.Voltage = defVoltage
	}
	if f.RatingA != nil && (*f.RatingA <= 0 || *f.RatingA > 100000) {
		return f, &db.UserError{Msg: "Rating must be between 0 and 100000 A."}
	}
	return f, nil
}

func (s *Server) handleHVFeederCreate(w http.ResponseWriter, r *http.Request) {
	var in hvFeederInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	n, err := s.Store.HVNetwork(ctx(r))
	if err != nil {
		fail(w, err)
		return
	}
	f, err := in.toModel(0, n.ID, n.Voltage)
	if err != nil {
		fail(w, err)
		return
	}
	id, err := s.Store.CreateHVFeeder(ctx(r), roleOf(r), f)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, map[string]int64{"id": id})
}

func (s *Server) handleHVFeederUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var in hvFeederInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	f, err := in.toModel(id, 0, "22kV")
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.UpdateHVFeeder(ctx(r), roleOf(r), f); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleHVFeederDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.DeleteHVFeeder(ctx(r), roleOf(r), id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleHVFeederMove(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var in struct {
		Delta int `json:"delta"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if in.Delta != 1 && in.Delta != -1 {
		fail(w, &db.UserError{Msg: "Move one place at a time."})
		return
	}
	if err := s.Store.MoveHVFeeder(ctx(r), roleOf(r), id, in.Delta); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// Ways --------------------------------------------------------------------

type hvWayInput struct {
	FeederID int64    `json:"feeder_id"`
	Name     string   `json:"name"`
	RatingA  *float64 `json:"rating_a"`

	DestBoardID *int64 `json:"dest_board_id"`
	DestLabel   string `json:"dest_label"`
	Notes       string `json:"notes"`
}

func (in hvWayInput) toModel(id int64) (model.HVWay, error) {
	w := model.HVWay{
		ID: id, FeederID: in.FeederID,
		Name:        strings.TrimSpace(in.Name),
		RatingA:     in.RatingA,
		DestBoardID: in.DestBoardID,
		DestLabel:   strings.TrimSpace(in.DestLabel),
		Notes:       strings.TrimSpace(in.Notes),
	}
	if err := required("Way name", w.Name); err != nil {
		return w, err
	}
	if w.RatingA != nil && (*w.RatingA <= 0 || *w.RatingA > 100000) {
		return w, &db.UserError{Msg: "Rating must be between 0 and 100000 A."}
	}
	// A destination board carries its own name, so a separate label would only
	// contradict it.
	if w.DestBoardID != nil && *w.DestBoardID == 0 {
		w.DestBoardID = nil
	}
	if w.DestBoardID != nil {
		w.DestLabel = ""
	}
	return w, nil
}

func (s *Server) handleHVWayCreate(w http.ResponseWriter, r *http.Request) {
	var in hvWayInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	way, err := in.toModel(0)
	if err != nil {
		fail(w, err)
		return
	}
	if way.FeederID <= 0 {
		fail(w, &db.UserError{Msg: "Feeder is required."})
		return
	}
	id, err := s.Store.CreateHVWay(ctx(r), roleOf(r), way)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, map[string]int64{"id": id})
}

func (s *Server) handleHVWayUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var in hvWayInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	way, err := in.toModel(id)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.UpdateHVWay(ctx(r), roleOf(r), way); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleHVWayDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.DeleteHVWay(ctx(r), roleOf(r), id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// Couplers ----------------------------------------------------------------

type hvCouplerInput struct {
	Name    string   `json:"name"`
	AfterID int64    `json:"after_id"`
	Closed  bool     `json:"closed"`
	RatingA *float64 `json:"rating_a"`
}

// couplerModel places a coupler in the gap that follows a feeder. Every feeder
// sits on one busbar, so where the coupler goes is a single choice; the section
// on its right is whichever feeder comes next, and is resolved here rather than
// asked for, which keeps the two from ever disagreeing.
func (s *Server) couplerModel(r *http.Request, in hvCouplerInput, id int64) (model.HVCoupler, error) {
	c := model.HVCoupler{
		ID:      id,
		Name:    strings.ToUpper(strings.TrimSpace(in.Name)),
		Closed:  in.Closed,
		RatingA: in.RatingA,
	}
	if err := required("Coupler name", c.Name); err != nil {
		return c, err
	}
	n, err := s.Store.HVNetwork(ctx(r))
	if err != nil {
		return c, err
	}
	c.NetworkID = n.ID
	if in.AfterID <= 0 {
		return c, &db.UserError{Msg: "Choose where on the busbar the coupler sits."}
	}
	for i, f := range n.Feeders {
		if f.ID != in.AfterID {
			continue
		}
		if i+1 >= len(n.Feeders) {
			return c, &db.UserError{Msg: "There is no feeder after " + f.Name + ", so a coupler cannot sit there."}
		}
		c.LeftID = f.ID
		c.RightID = n.Feeders[i+1].ID
		return c, nil
	}
	return c, &db.UserError{Msg: "That feeder is no longer on the busbar."}
}

func (s *Server) handleHVCouplerCreate(w http.ResponseWriter, r *http.Request) {
	var in hvCouplerInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	c, err := s.couplerModel(r, in, 0)
	if err != nil {
		fail(w, err)
		return
	}
	id, err := s.Store.CreateHVCoupler(ctx(r), roleOf(r), c)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, map[string]int64{"id": id})
}

func (s *Server) handleHVCouplerUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var in hvCouplerInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	c, err := s.couplerModel(r, in, id)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.UpdateHVCoupler(ctx(r), roleOf(r), c); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleHVCouplerDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.DeleteHVCoupler(ctx(r), roleOf(r), id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// Devices ------------------------------------------------------------------

type hvDeviceInput struct {
	WayID   int64    `json:"way_id"`
	AfterID int64    `json:"after_id"`
	Kind    string   `json:"kind"`
	Name    string   `json:"name"`
	RatingA *float64 `json:"rating_a"`
	KVA     *float64 `json:"kva"`
	Ratio   string   `json:"ratio"`
	Notes   string   `json:"notes"`
}

func (in hvDeviceInput) toModel(id int64) (model.HVDevice, error) {
	d := model.HVDevice{
		ID: id, WayID: in.WayID,
		Kind:    strings.ToLower(strings.TrimSpace(in.Kind)),
		Name:    strings.TrimSpace(in.Name),
		RatingA: in.RatingA,
		KVA:     in.KVA,
		Ratio:   strings.TrimSpace(in.Ratio),
		Notes:   strings.TrimSpace(in.Notes),
	}
	if !model.ValidDeviceKind(d.Kind) {
		return d, &db.UserError{Msg: "Choose what kind of device this is."}
	}
	if d.RatingA != nil && (*d.RatingA <= 0 || *d.RatingA > 100000) {
		return d, &db.UserError{Msg: "Rating must be between 0 and 100000 A."}
	}
	if d.KVA != nil && (*d.KVA <= 0 || *d.KVA > 1000000) {
		return d, &db.UserError{Msg: "Transformer rating must be between 0 and 1000000 kVA."}
	}
	return d, nil
}

func (s *Server) handleHVDeviceCreate(w http.ResponseWriter, r *http.Request) {
	var in hvDeviceInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	d, err := in.toModel(0)
	if err != nil {
		fail(w, err)
		return
	}
	if d.WayID <= 0 {
		fail(w, &db.UserError{Msg: "Way is required."})
		return
	}
	id, err := s.Store.CreateHVDevice(ctx(r), roleOf(r), d, in.AfterID)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, map[string]int64{"id": id})
}

func (s *Server) handleHVDeviceUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var in hvDeviceInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	d, err := in.toModel(id)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.UpdateHVDevice(ctx(r), roleOf(r), d); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleHVDeviceDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.DeleteHVDevice(ctx(r), roleOf(r), id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleHVDeviceMove(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var in struct {
		Delta int `json:"delta"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if in.Delta != 1 && in.Delta != -1 {
		fail(w, &db.UserError{Msg: "Move one place at a time."})
		return
	}
	if err := s.Store.MoveHVDevice(ctx(r), roleOf(r), id, in.Delta); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
