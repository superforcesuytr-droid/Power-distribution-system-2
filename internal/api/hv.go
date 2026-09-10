package api

import (
	"net/http"
	"strconv"
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
	mux.HandleFunc("GET /api/hv/networks", s.handleHVNetworks)
	mux.HandleFunc("GET /api/hv/switchboards", s.handleHVBoardList)
	mux.HandleFunc("GET /api/hv/refs", s.handleHVRefs)
	mux.HandleFunc("POST /api/hv/networks", s.requireRole(model.RoleSupervisor, s.handleHVNetworkCreate))
	mux.HandleFunc("DELETE /api/hv/networks/{id}", s.requireRole(model.RoleSupervisor, s.handleHVNetworkDelete))

	mux.HandleFunc("POST /api/hv/switchboards", s.requireRole(model.RoleSupervisor, s.handleHVBoardCreate))
	mux.HandleFunc("PUT /api/hv/switchboards/{id}", s.requireRole(model.RoleSupervisor, s.handleHVBoardUpdate))
	mux.HandleFunc("DELETE /api/hv/switchboards/{id}", s.requireRole(model.RoleSupervisor, s.handleHVBoardDelete))
	mux.HandleFunc("POST /api/hv/switchboards/{id}/move", s.requireRole(model.RoleSupervisor, s.handleHVBoardMove))

	mux.HandleFunc("POST /api/hv/sections", s.requireRole(model.RoleSupervisor, s.handleHVSectionCreate))
	mux.HandleFunc("PUT /api/hv/sections/{id}", s.requireRole(model.RoleSupervisor, s.handleHVSectionUpdate))
	mux.HandleFunc("DELETE /api/hv/sections/{id}", s.requireRole(model.RoleSupervisor, s.handleHVSectionDelete))
	mux.HandleFunc("POST /api/hv/sections/{id}/span", s.requireRole(model.RoleTechnician, s.handleHVSectionSpan))

	mux.HandleFunc("POST /api/hv/feeders", s.requireRole(model.RoleSupervisor, s.handleHVFeederCreate))
	mux.HandleFunc("PUT /api/hv/feeders/{id}", s.requireRole(model.RoleSupervisor, s.handleHVFeederUpdate))
	mux.HandleFunc("DELETE /api/hv/feeders/{id}", s.requireRole(model.RoleSupervisor, s.handleHVFeederDelete))
	mux.HandleFunc("POST /api/hv/feeders/{id}/move", s.requireRole(model.RoleSupervisor, s.handleHVFeederMove))
	mux.HandleFunc("POST /api/hv/feeders/{id}/place", s.requireRole(model.RoleSupervisor, s.handleHVFeederPlace))

	mux.HandleFunc("POST /api/hv/ways", s.requireRole(model.RoleTechnician, s.handleHVWayCreate))
	mux.HandleFunc("PUT /api/hv/ways/{id}", s.requireRole(model.RoleTechnician, s.handleHVWayUpdate))
	mux.HandleFunc("DELETE /api/hv/ways/{id}", s.requireRole(model.RoleSupervisor, s.handleHVWayDelete))
	mux.HandleFunc("POST /api/hv/ways/{id}/place", s.requireRole(model.RoleTechnician, s.handleHVWayPlace))

	mux.HandleFunc("POST /api/hv/devices", s.requireRole(model.RoleTechnician, s.handleHVDeviceCreate))
	mux.HandleFunc("PUT /api/hv/devices/{id}", s.requireRole(model.RoleTechnician, s.handleHVDeviceUpdate))
	mux.HandleFunc("DELETE /api/hv/devices/{id}", s.requireRole(model.RoleTechnician, s.handleHVDeviceDelete))
	mux.HandleFunc("POST /api/hv/devices/{id}/move", s.requireRole(model.RoleTechnician, s.handleHVDeviceMove))
	mux.HandleFunc("POST /api/hv/devices/{id}/place", s.requireRole(model.RoleTechnician, s.handleHVDevicePlace))

	mux.HandleFunc("POST /api/hv/couplers", s.requireRole(model.RoleSupervisor, s.handleHVCouplerCreate))
	mux.HandleFunc("PUT /api/hv/couplers/{id}", s.requireRole(model.RoleSupervisor, s.handleHVCouplerUpdate))
	mux.HandleFunc("DELETE /api/hv/couplers/{id}", s.requireRole(model.RoleSupervisor, s.handleHVCouplerDelete))
}

// queryID reads a numeric query parameter, which is how a request says which
// drawing it means. Zero means the first one.
func queryID(r *http.Request, name string) int64 {
	v, err := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get(name)), 10, 64)
	if err != nil || v < 0 {
		return 0
	}
	return v
}

func (s *Server) handleHVGet(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.HVNetwork(ctx(r), queryID(r, "network"))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, n)
}

// handleHVNetworks lists the drawings for the picker above the diagram.
func (s *Server) handleHVNetworks(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.HVNetworks(ctx(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, list)
}

type hvNetworkInput struct {
	Name    string `json:"name"`
	Voltage string `json:"voltage"`
	Tier    string `json:"tier"`
}

func (in hvNetworkInput) toModel(id int64) (model.HVNetwork, error) {
	n := model.HVNetwork{
		ID:      id,
		Name:    strings.TrimSpace(in.Name),
		Voltage: strings.TrimSpace(in.Voltage),
		Tier:    strings.TrimSpace(in.Tier),
	}
	if err := required("Name", n.Name); err != nil {
		return n, err
	}
	if n.Tier == "" {
		n.Tier = model.TierLT
	}
	if !model.ValidTier(n.Tier) {
		return n, &db.UserError{Msg: "A drawing is either the high-tension or the low-tension side."}
	}
	return n, nil
}

func (s *Server) handleHVUpdate(w http.ResponseWriter, r *http.Request) {
	var in hvNetworkInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	cur, err := s.Store.HVNetwork(ctx(r), queryID(r, "network"))
	if err != nil {
		fail(w, err)
		return
	}
	if in.Tier == "" {
		in.Tier = cur.Tier
	}
	n, err := in.toModel(cur.ID)
	if err != nil {
		fail(w, err)
		return
	}
	if n.Voltage == "" {
		n.Voltage = cur.Voltage
	}
	if err := s.Store.UpdateHVNetwork(ctx(r), roleOf(r), n); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleHVNetworkCreate(w http.ResponseWriter, r *http.Request) {
	var in hvNetworkInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	n, err := in.toModel(0)
	if err != nil {
		fail(w, err)
		return
	}
	id, err := s.Store.CreateHVNetwork(ctx(r), roleOf(r), n)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, map[string]int64{"id": id})
}

func (s *Server) handleHVNetworkDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.DeleteHVNetwork(ctx(r), roleOf(r), id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// Feeders -----------------------------------------------------------------

type hvFeederInput struct {
	Name       string `json:"name"`
	Switchgear string `json:"switchgear"`
	Kind       string `json:"kind"`
	// Only read when an incomer is created: what it lands through, and the
	// designation of the transformer when it lands through one.
	Arrangement string `json:"arrangement"`
	Transformer string `json:"transformer"`
	// Side is the end of the bar it backs, on a board fed from both ends.
	Side      string   `json:"side"`
	SectionID int64    `json:"section_id"`
	Voltage   string   `json:"voltage"`
	Source    string   `json:"source"`
	RatingA   *float64 `json:"rating_a"`
	// SourceWayID is the way on another switchboard this incomer taps its
	// supply from. Zero, or absent, for a supply from off the drawing.
	SourceWayID int64 `json:"source_way_id"`
}

func (in hvFeederInput) toModel(id, networkID int64, defVoltage string) (model.HVFeeder, error) {
	f := model.HVFeeder{
		ID: id, NetworkID: networkID, SectionID: in.SectionID,
		Name:       strings.ToUpper(strings.TrimSpace(in.Name)),
		Switchgear: strings.ToUpper(strings.TrimSpace(in.Switchgear)),
		Kind:       strings.TrimSpace(in.Kind),
		Side:       strings.TrimSpace(in.Side),
		Voltage:    strings.TrimSpace(in.Voltage),
		Source:     strings.TrimSpace(in.Source),
		RatingA:    in.RatingA,
	}
	if in.SourceWayID > 0 {
		way := in.SourceWayID
		f.SourceWayID = &way
	}
	if err := required("Feeder name", f.Name); err != nil {
		return f, err
	}
	if f.Kind == "" {
		f.Kind = model.FeederSupply
	}
	if !model.ValidFeederKind(f.Kind) {
		return f, &db.UserError{Msg: "An incomer is either an incoming supply or a generator."}
	}
	if !model.ValidSide(f.Side) {
		return f, &db.UserError{Msg: "A side is either the left or the right of the bar."}
	}
	if f.Voltage == "" {
		f.Voltage = defVoltage
	}
	if f.RatingA != nil && (*f.RatingA <= 0 || *f.RatingA > 100000) {
		return f, &db.UserError{Msg: "Rating must be between 0 and 100000 A."}
	}
	return f, nil
}

// networkFor says which drawing a new feeder, way, coupler or section belongs
// on: the one its bus section is already on, falling back to the drawing named
// in the query, and then to the first there is.
func (s *Server) networkFor(r *http.Request, sectionID int64) (*model.HVNetwork, error) {
	id := queryID(r, "network")
	if sectionID > 0 {
		on, err := s.Store.NetworkOfSection(ctx(r), sectionID)
		if err != nil {
			return nil, err
		}
		id = on
	}
	return s.Store.HVNetwork(ctx(r), id)
}

func (s *Server) handleHVFeederCreate(w http.ResponseWriter, r *http.Request) {
	var in hvFeederInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	n, err := s.networkFor(r, in.SectionID)
	if err != nil {
		fail(w, err)
		return
	}
	f, err := in.toModel(0, n.ID, n.Voltage)
	if err != nil {
		fail(w, err)
		return
	}
	id, err := s.Store.CreateHVFeeder(ctx(r), roleOf(r), f,
		strings.TrimSpace(in.Arrangement), strings.ToUpper(strings.TrimSpace(in.Transformer)))
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

// hvPlaceInput is where a dragged column was dropped: the bus section it landed
// on and how many of that section's columns now sit to its left.
type hvPlaceInput struct {
	SectionID int64 `json:"section_id"`
	Index     int   `json:"index"`
	// Side is the end of the bar the column was dropped on.
	Side string `json:"side"`
	// OffsetX places the column at a point of its own along the section, as a
	// fraction of that section's width. When it is given, the index and the
	// side are not read: the column simply goes where it was dropped.
	OffsetX *float64 `json:"offset_x"`
	// Auto hands a column back to the automatic spacing.
	Auto bool `json:"auto"`
}

func (s *Server) handleHVFeederPlace(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var in hvPlaceInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if in.Auto || in.OffsetX != nil {
		if in.OffsetX != nil && (*in.OffsetX < 0 || *in.OffsetX > 1) {
			fail(w, &db.UserError{Msg: "A place on the busbar is a fraction of its width."})
			return
		}
		if in.Auto {
			in.OffsetX = nil
		}
		if err := s.Store.PlaceHVFeederAt(ctx(r), roleOf(r), id, in.SectionID, in.OffsetX); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if !model.ValidSide(in.Side) {
		fail(w, &db.UserError{Msg: "A side is either the left or the right of the bar."})
		return
	}
	if err := s.Store.PlaceHVFeeder(ctx(r), roleOf(r), id, in.SectionID, in.Index, in.Side); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// Ways --------------------------------------------------------------------

type hvWayInput struct {
	SectionID int64    `json:"section_id"`
	Name      string   `json:"name"`
	RatingA   *float64 `json:"rating_a"`

	DestBoardID       *int64 `json:"dest_board_id"`
	DestSwitchboardID *int64 `json:"dest_switchboard_id"`
	DestLabel         string `json:"dest_label"`
	DestDetail        string `json:"dest_detail"`
	Notes             string `json:"notes"`

	// Only read when a way is created: what stands where it taps the bus, and
	// the machine at the foot of it when the way runs straight into a load.
	HeadKind string `json:"head_kind"`
	Chiller  string `json:"chiller"`
	// Side is the end of the bar it taps, on a board fed from both ends.
	Side string `json:"side"`
}

func (in hvWayInput) toModel(id int64) (model.HVWay, error) {
	w := model.HVWay{
		ID: id, SectionID: in.SectionID,
		Name:              strings.TrimSpace(in.Name),
		RatingA:           in.RatingA,
		Side:              strings.TrimSpace(in.Side),
		DestBoardID:       in.DestBoardID,
		DestSwitchboardID: in.DestSwitchboardID,
		DestLabel:         strings.TrimSpace(in.DestLabel),
		DestDetail:        strings.TrimSpace(in.DestDetail),
		Notes:             strings.TrimSpace(in.Notes),
	}
	if err := required("Way name", w.Name); err != nil {
		return w, err
	}
	if w.RatingA != nil && (*w.RatingA <= 0 || *w.RatingA > 100000) {
		return w, &db.UserError{Msg: "Rating must be between 0 and 100000 A."}
	}
	if !model.ValidSide(w.Side) {
		return w, &db.UserError{Msg: "A side is either the left or the right of the bar."}
	}
	// A way lands in one place. A destination carries its own name, so a
	// separate label, or a second destination, would only contradict it.
	if w.DestBoardID != nil && *w.DestBoardID == 0 {
		w.DestBoardID = nil
	}
	if w.DestSwitchboardID != nil && *w.DestSwitchboardID == 0 {
		w.DestSwitchboardID = nil
	}
	if w.DestSwitchboardID != nil {
		w.DestBoardID, w.DestLabel = nil, ""
	} else if w.DestBoardID != nil {
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
	n, err := s.networkFor(r, in.SectionID)
	if err != nil {
		fail(w, err)
		return
	}
	id, err := s.Store.CreateHVWay(ctx(r), roleOf(r), way, n.ID,
		strings.TrimSpace(in.HeadKind), strings.ToUpper(strings.TrimSpace(in.Chiller)))
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

// couplerModel places a coupler in the gap that follows a bus section. Sections
// sit in one row, so where the coupler goes is a single choice; the section on
// its right is whichever comes next, and is resolved here rather than asked
// for, which keeps the two from ever disagreeing.
func (s *Server) couplerModel(r *http.Request, in hvCouplerInput, id int64) (model.HVCoupler, error) {
	c := model.HVCoupler{
		ID:     id,
		Name:   strings.ToUpper(strings.TrimSpace(in.Name)),
		Closed: in.Closed, RatingA: in.RatingA,
	}
	if err := required("Coupler name", c.Name); err != nil {
		return c, err
	}
	if in.AfterID <= 0 {
		return c, &db.UserError{Msg: "Choose where on the busbar the coupler sits."}
	}
	n, err := s.networkFor(r, in.AfterID)
	if err != nil {
		return c, err
	}
	c.NetworkID = n.ID
	// A coupler ties two sections of the same switchboard, so the section on
	// its right is the next one along that board's own bus.
	for _, board := range n.Switchboards {
		for i, sec := range board.Sections {
			if sec.ID != in.AfterID {
				continue
			}
			if i+1 >= len(board.Sections) {
				return c, &db.UserError{Msg: "There is no section after " + sec.Name + " on " + board.Name +
					", so a coupler cannot sit there."}
			}
			c.LeftSectionID = sec.ID
			c.RightSectionID = board.Sections[i+1].ID
			return c, nil
		}
	}
	return c, &db.UserError{Msg: "That section is no longer on the busbar."}
}

func (s *Server) handleHVCouplerCreate(w http.ResponseWriter, r *http.Request) {
	var in hvCouplerInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	// Asked for at the end of a bus, a coupler divides it: the section on its
	// far side is created here so a new switchboard can be given a coupler
	// without having to be split by hand first.
	if in.AfterID > 0 {
		if _, err := s.Store.SplitHVBusAfter(ctx(r), roleOf(r), in.AfterID); err != nil {
			fail(w, err)
			return
		}
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

func (s *Server) handleHVWayPlace(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var in hvPlaceInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if in.Auto || in.OffsetX != nil {
		if in.OffsetX != nil && (*in.OffsetX < 0 || *in.OffsetX > 1) {
			fail(w, &db.UserError{Msg: "A place on the busbar is a fraction of its width."})
			return
		}
		if in.Auto {
			in.OffsetX = nil
		}
		if err := s.Store.PlaceHVWayAt(ctx(r), roleOf(r), id, in.SectionID, in.OffsetX); err != nil {
			fail(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}
	if !model.ValidSide(in.Side) {
		fail(w, &db.UserError{Msg: "A side is either the left or the right of the bar."})
		return
	}
	if err := s.Store.PlaceHVWay(ctx(r), roleOf(r), id, in.SectionID, in.Index, in.Side); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// Devices ------------------------------------------------------------------

type hvDeviceInput struct {
	WayID    int64    `json:"way_id"`
	FeederID int64    `json:"feeder_id"`
	AfterID  int64    `json:"after_id"`
	Kind     string   `json:"kind"`
	Name     string   `json:"name"`
	RatingA  *float64 `json:"rating_a"`
	KVA      *float64 `json:"kva"`
	Ratio    string   `json:"ratio"`
	Notes    string   `json:"notes"`
}

func (in hvDeviceInput) toModel(id int64) (model.HVDevice, error) {
	d := model.HVDevice{
		ID: id, WayID: in.WayID, FeederID: in.FeederID,
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
	if (d.WayID > 0) == (d.FeederID > 0) {
		fail(w, &db.UserError{Msg: "A device sits on either a way or a feeder."})
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

func (s *Server) handleHVDevicePlace(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var in struct {
		WayID    int64 `json:"way_id"`
		FeederID int64 `json:"feeder_id"`
		AfterID  int64 `json:"after_id"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.PlaceHVDevice(ctx(r), roleOf(r), id, db.DevTarget(in.WayID, in.FeederID), in.AfterID); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// Switchboards -------------------------------------------------------------

type hvBoardInput struct {
	Name      string   `json:"name"`
	Voltage   string   `json:"voltage"`
	Phases    string   `json:"phases"`
	Frequency string   `json:"frequency"`
	CurrentA  *float64 `json:"current_a"`
	FaultKA   *float64 `json:"fault_ka"`
}

func (in hvBoardInput) toModel(id, networkID int64) (model.HVSwitchboard, error) {
	b := model.HVSwitchboard{
		ID: id, NetworkID: networkID,
		Name:      strings.TrimSpace(in.Name),
		Voltage:   strings.TrimSpace(in.Voltage),
		Phases:    strings.TrimSpace(in.Phases),
		Frequency: strings.TrimSpace(in.Frequency),
		CurrentA:  in.CurrentA,
		FaultKA:   in.FaultKA,
	}
	if err := required("Switchboard name", b.Name); err != nil {
		return b, err
	}
	if b.CurrentA != nil && (*b.CurrentA <= 0 || *b.CurrentA > 100000) {
		return b, &db.UserError{Msg: "Rated current must be between 0 and 100000 A."}
	}
	if b.FaultKA != nil && (*b.FaultKA <= 0 || *b.FaultKA > 1000) {
		return b, &db.UserError{Msg: "Fault rating must be between 0 and 1000 kA."}
	}
	return b, nil
}

// handleHVRefs names every way and incomer on the site, so a designation on
// one drawing can point at the same designation on another.
func (s *Server) handleHVRefs(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.HVCrossRefs(ctx(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, list)
}

// handleHVBoardList names every switchboard on the site, whichever drawing it
// is on, so a way can be pointed at one across drawings.
func (s *Server) handleHVBoardList(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.HVSwitchboardList(ctx(r))
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, list)
}

func (s *Server) handleHVBoardCreate(w http.ResponseWriter, r *http.Request) {
	var in hvBoardInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	n, err := s.Store.HVNetwork(ctx(r), queryID(r, "network"))
	if err != nil {
		fail(w, err)
		return
	}
	b, err := in.toModel(0, n.ID)
	if err != nil {
		fail(w, err)
		return
	}
	id, err := s.Store.CreateHVSwitchboard(ctx(r), roleOf(r), b)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, map[string]int64{"id": id})
}

func (s *Server) handleHVBoardUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var in hvBoardInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	b, err := in.toModel(id, 0)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.UpdateHVSwitchboard(ctx(r), roleOf(r), b); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleHVBoardDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.DeleteHVSwitchboard(ctx(r), roleOf(r), id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleHVBoardMove(w http.ResponseWriter, r *http.Request) {
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
	if err := s.Store.MoveHVSwitchboard(ctx(r), roleOf(r), id, in.Delta); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// Sections -----------------------------------------------------------------

type hvSectionInput struct {
	Name          string `json:"name"`
	SwitchboardID int64  `json:"switchboard_id"`
}

func (s *Server) handleHVSectionCreate(w http.ResponseWriter, r *http.Request) {
	var in hvSectionInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	name := strings.TrimSpace(in.Name)
	if err := required("Section name", name); err != nil {
		fail(w, err)
		return
	}
	on := queryID(r, "network")
	if in.SwitchboardID > 0 {
		var err error
		if on, err = s.Store.NetworkOfSwitchboard(ctx(r), in.SwitchboardID); err != nil {
			fail(w, err)
			return
		}
	}
	n, err := s.Store.HVNetwork(ctx(r), on)
	if err != nil {
		fail(w, err)
		return
	}
	id, err := s.Store.CreateHVSection(ctx(r), roleOf(r), n.ID, in.SwitchboardID, name)
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 201, map[string]int64{"id": id})
}

// handleHVSectionSpan runs a busbar out to a given length, or back to
// automatic when the request says so.
func (s *Server) handleHVSectionSpan(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var in struct {
		Width *float64 `json:"width"`
		Auto  bool     `json:"auto"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	width := in.Width
	if in.Auto {
		width = nil
	} else if width == nil || *width <= 0 {
		fail(w, &db.UserError{Msg: "A busbar needs a length to be drawn to."})
		return
	} else if *width > 40000 {
		fail(w, &db.UserError{Msg: "That is longer than a busbar can be drawn."})
		return
	}
	if err := s.Store.SetHVSectionWidth(ctx(r), roleOf(r), id, width); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleHVSectionUpdate(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	var in hvSectionInput
	if err := decode(r, &in); err != nil {
		fail(w, err)
		return
	}
	name := strings.TrimSpace(in.Name)
	if err := required("Section name", name); err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.UpdateHVSection(ctx(r), roleOf(r), id, name); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleHVSectionDelete(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return
	}
	if err := s.Store.DeleteHVSection(ctx(r), roleOf(r), id); err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
