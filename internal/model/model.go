// Package model defines the domain types shared by the database layer, the
// HTTP API and the embedded web UI, together with the load calculations that
// drive every utilisation figure shown on screen.
package model

import (
	"math"
	"time"
)

// Roles, ordered from least to most privileged.
const (
	RoleViewer     = "viewer"
	RoleTechnician = "technician"
	RoleSupervisor = "supervisor"
)

// RoleRank maps a role to its privilege level so handlers can compare roles.
var RoleRank = map[string]int{
	RoleViewer:     1,
	RoleTechnician: 2,
	RoleSupervisor: 3,
}

// Circuit status values (mirrors the circuit_status enum in PostgreSQL).
const (
	StatusActive      = "active"
	StatusMaintenance = "maintenance"
	StatusInactive    = "inactive"
)

// ValidStatus reports whether s is one of the circuit status values.
func ValidStatus(s string) bool {
	return s == StatusActive || s == StatusMaintenance || s == StatusInactive
}

// Settings are the tunable engineering parameters stored in app_settings.
type Settings struct {
	// TripFactor multiplies a breaker's rated current to give the "capacity"
	// figure used for utilisation. 1.14 approximates the IEC 60898
	// conventional non-tripping current (1.13 In) - the point at which a
	// thermal-magnetic breaker is guaranteed *not* to trip within the
	// conventional time. Loads above it are shown as critical.
	TripFactor float64 `json:"trip_factor"`
	// WarnPct and CritPct are the utilisation thresholds (in percent) that
	// switch a bar from green to amber and from amber to red.
	WarnPct int `json:"warn_pct"`
	CritPct int `json:"crit_pct"`
}

// DefaultSettings are used when the database has not been configured yet.
func DefaultSettings() Settings {
	return Settings{TripFactor: 1.14, WarnPct: 65, CritPct: 85}
}

// Capacity returns the usable capacity for a breaker of the given rating.
func (s Settings) Capacity(ratingA float64) float64 {
	// Round to 3 decimals first so binary artefacts such as 25*1.14 =
	// 28.499999999999996 still round half-up to 29.
	return math.Round(math.Round(ratingA*s.TripFactor*1000) / 1000)
}

// Percent returns utilisation as a whole percentage, capped at 100.
func (s Settings) Percent(current, capacity float64) int {
	if capacity <= 0 {
		return 0
	}
	p := int(math.Round(current / capacity * 100))
	if p > 100 {
		p = 100
	}
	if p < 0 {
		p = 0
	}
	return p
}

// Level classifies a utilisation percentage.
func (s Settings) Level(pct int) string {
	switch {
	case pct > s.CritPct:
		return "critical"
	case pct >= s.WarnPct:
		return "warning"
	default:
		return "normal"
	}
}

// Building groups distribution boards by physical location.
type Building struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Icon      string    `json:"icon"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`

	// Derived
	BoardCount          int `json:"board_count"`
	ActiveCircuits      int `json:"active_circuits"`
	MaintenanceCircuits int `json:"maintenance_circuits"`
	InactiveCircuits    int `json:"inactive_circuits"`
}

// Board is a distribution board / panel (e.g. FAC5, 400V 3PH).
type Board struct {
	ID         int64     `json:"id"`
	BuildingID int64     `json:"building_id"`
	Code       string    `json:"code"`
	Voltage    string    `json:"voltage"`
	Phases     string    `json:"phases"`
	Level      string    `json:"level"`
	Location   string    `json:"location"`
	Technician string    `json:"technician"`
	Position   int       `json:"position"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`

	// Derived
	BuildingName string `json:"building_name"`
}

// BoardSummary is a Board with roll-up figures for list views.
type BoardSummary struct {
	Board
	MCCBCount           int     `json:"mccb_count"`
	MCBCount            int     `json:"mcb_count"`
	CircuitCount        int     `json:"circuit_count"`
	MaintenanceCircuits int     `json:"maintenance_circuits"`
	TotalCurrent        float64 `json:"total_current"`
	MCBCapacity         float64 `json:"mcb_capacity"`
	UtilPct             int     `json:"util_pct"`
	UtilLevel           string  `json:"util_level"`
}

// MCCB is a moulded-case circuit breaker feeding a group of MCBs.
type MCCB struct {
	ID       int64   `json:"id"`
	BoardID  int64   `json:"board_id"`
	Name     string  `json:"name"`
	RatingA  float64 `json:"rating_a"`
	Position int     `json:"position"`
	MCBs     []MCB   `json:"mcbs"`

	// Derived
	Current             float64 `json:"current"`
	Capacity            float64 `json:"capacity"`
	Pct                 int     `json:"pct"`
	Level               string  `json:"level"`
	MCBCount            int     `json:"mcb_count"`
	CircuitCount        int     `json:"circuit_count"`
	MaintenanceCircuits int     `json:"maintenance_circuits"`
}

// MCB is a miniature circuit breaker feeding final circuits.
type MCB struct {
	ID       int64     `json:"id"`
	MCCBID   int64     `json:"mccb_id"`
	Name     string    `json:"name"`
	RatingA  float64   `json:"rating_a"`
	Position int       `json:"position"`
	Circuits []Circuit `json:"circuits"`

	// Derived
	Current             float64 `json:"current"`
	Capacity            float64 `json:"capacity"`
	Pct                 int     `json:"pct"`
	Level               string  `json:"level"`
	CircuitCount        int     `json:"circuit_count"`
	MaintenanceCircuits int     `json:"maintenance_circuits"`
}

// Circuit is a final circuit supplying one or more pieces of equipment.
type Circuit struct {
	ID             int64     `json:"id"`
	MCBID          int64     `json:"mcb_id"`
	Name           string    `json:"name"`
	Code           string    `json:"code"`
	LoadA          float64   `json:"load_a"`
	Status         string    `json:"status"`
	EquipmentCount int       `json:"equipment_count"`
	Service        string    `json:"service"`
	Notes          string    `json:"notes"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`

	// Derived (filled when the parent MCB is known)
	Capacity float64 `json:"capacity"`
	Pct      int     `json:"pct"`
	Level    string  `json:"level"`
}

// CircuitRow is a flattened circuit with its full ancestry for the explorer.
type CircuitRow struct {
	Circuit
	MCBName      string  `json:"mcb_name"`
	MCBRating    float64 `json:"mcb_rating"`
	MCCBID       int64   `json:"mccb_id"`
	MCCBName     string  `json:"mccb_name"`
	BoardID      int64   `json:"board_id"`
	BoardCode    string  `json:"board_code"`
	BuildingID   int64   `json:"building_id"`
	BuildingName string  `json:"building_name"`
}

// BoardTotals are the roll-up figures shown in the "current totals" strip.
type BoardTotals struct {
	TotalCurrent        float64 `json:"total_current"`
	MCBCapacity         float64 `json:"mcb_capacity"`
	MCCBCapacity        float64 `json:"mccb_capacity"`
	ActiveCircuits      int     `json:"active_circuits"`
	MaintenanceCircuits int     `json:"maintenance_circuits"`
	InactiveCircuits    int     `json:"inactive_circuits"`
	MCCBCount           int     `json:"mccb_count"`
	MCBCount            int     `json:"mcb_count"`
	CircuitCount        int     `json:"circuit_count"`
	UtilPct             int     `json:"util_pct"`
	UtilLevel           string  `json:"util_level"`
}

// BoardDetail is the full breaker tree for one board with all metrics.
type BoardDetail struct {
	Board
	MCCBs  []MCCB      `json:"mccbs"`
	Totals BoardTotals `json:"totals"`
}

// Compute fills in every derived field of the tree in place.
func (d *BoardDetail) Compute(s Settings) {
	t := BoardTotals{}
	for i := range d.MCCBs {
		m := &d.MCCBs[i]
		m.Current, m.CircuitCount, m.MaintenanceCircuits = 0, 0, 0
		m.MCBCount = len(m.MCBs)
		m.Capacity = s.Capacity(m.RatingA)
		for j := range m.MCBs {
			b := &m.MCBs[j]
			b.Current, b.MaintenanceCircuits = 0, 0
			b.Capacity = s.Capacity(b.RatingA)
			b.CircuitCount = len(b.Circuits)
			for k := range b.Circuits {
				c := &b.Circuits[k]
				c.Capacity = b.Capacity
				c.Pct = s.Percent(c.LoadA, b.Capacity)
				c.Level = s.Level(c.Pct)
				b.Current += c.LoadA
				switch c.Status {
				case StatusActive:
					t.ActiveCircuits++
				case StatusMaintenance:
					t.MaintenanceCircuits++
					b.MaintenanceCircuits++
				default:
					t.InactiveCircuits++
				}
			}
			b.Pct = s.Percent(b.Current, b.Capacity)
			b.Level = s.Level(b.Pct)
			m.Current += b.Current
			m.CircuitCount += b.CircuitCount
			m.MaintenanceCircuits += b.MaintenanceCircuits
			t.MCBCapacity += b.RatingA
			t.MCBCount++
		}
		m.Pct = s.Percent(m.Current, m.Capacity)
		m.Level = s.Level(m.Pct)
		t.TotalCurrent += m.Current
		t.MCCBCapacity += m.RatingA
		t.CircuitCount += m.CircuitCount
		t.MCCBCount++
	}
	t.UtilPct = s.Percent(t.TotalCurrent, t.MCBCapacity)
	t.UtilLevel = s.Level(t.UtilPct)
	d.Totals = t
}

// AuditEntry records who changed what.
type AuditEntry struct {
	ID        int64     `json:"id"`
	At        time.Time `json:"at"`
	ActorRole string    `json:"actor_role"`
	Action    string    `json:"action"`
	Entity    string    `json:"entity"`
	EntityID  int64     `json:"entity_id"`
	Summary   string    `json:"summary"`
}

// SearchHit is one row of the global search.
type SearchHit struct {
	Kind     string `json:"kind"` // building | board | mccb | mcb | circuit
	ID       int64  `json:"id"`
	BoardID  int64  `json:"board_id"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
}
