package model

import "time"

// Protection fitted on an outgoing high-voltage way (mirrors the hv_protection
// enum in PostgreSQL).
const (
	ProtectionNone = "none"
	ProtectionRCCB = "rccb" // residual current circuit breaker
	ProtectionELR  = "elr"  // earth leakage relay
	ProtectionELCB = "elcb" // earth leakage circuit breaker
)

// ValidProtection reports whether p is one of the protection values.
func ValidProtection(p string) bool {
	switch p {
	case ProtectionNone, ProtectionRCCB, ProtectionELR, ProtectionELCB:
		return true
	}
	return false
}

// ProtectionLabel is how a protection type is written on the diagram.
func ProtectionLabel(p string) string {
	switch p {
	case ProtectionRCCB:
		return "RCCB"
	case ProtectionELR:
		return "ELR"
	case ProtectionELCB:
		return "ELCB"
	}
	return ""
}

// HVNetwork is the site-wide high-voltage distribution overview: the incoming
// feeders, the switchgear they land on, and the couplers tying them together.
type HVNetwork struct {
	ID       int64       `json:"id"`
	Name     string      `json:"name"`
	Voltage  string      `json:"voltage"`
	Feeders  []HVFeeder  `json:"feeders"`
	Couplers []HVCoupler `json:"couplers"`

	// Derived
	FeederCount      int `json:"feeder_count"`
	WayCount         int `json:"way_count"`
	TransformerCount int `json:"transformer_count"`
	ProtectedCount   int `json:"protected_count"`
	LinkedCount      int `json:"linked_count"`
}

// HVFeeder is one incoming supply and the switchgear bus it feeds.
type HVFeeder struct {
	ID        int64     `json:"id"`
	NetworkID int64     `json:"network_id"`
	Name      string    `json:"name"`
	Voltage   string    `json:"voltage"`
	Source    string    `json:"source"`
	RatingA   *float64  `json:"rating_a"`
	Position  int       `json:"position"`
	Ways      []HVWay   `json:"ways"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// HVWay is an outgoing way from a feeder's switchgear: optionally protected,
// optionally through a transformer, landing on a board or a named destination.
type HVWay struct {
	ID       int64    `json:"id"`
	FeederID int64    `json:"feeder_id"`
	Name     string   `json:"name"`
	RatingA  *float64 `json:"rating_a"`

	Protection     string `json:"protection"`
	ProtectionNote string `json:"protection_note"`

	HasTransformer   bool     `json:"has_transformer"`
	TransformerName  string   `json:"transformer_name"`
	TransformerKVA   *float64 `json:"transformer_kva"`
	TransformerRatio string   `json:"transformer_ratio"`

	DestBoardID *int64 `json:"dest_board_id"`
	DestLabel   string `json:"dest_label"`
	Notes       string `json:"notes"`
	Position    int    `json:"position"`

	// Derived: filled from the linked board so the diagram can label and link
	// the destination without a second lookup.
	DestBoardCode    string    `json:"dest_board_code"`
	DestBuildingName string    `json:"dest_building_name"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// Destination is what the diagram writes in the box at the foot of a way.
func (w HVWay) Destination() string {
	if w.DestLabel != "" {
		return w.DestLabel
	}
	if w.DestBoardCode != "" {
		return w.DestBoardCode
	}
	return "Not assigned"
}

// HVCoupler ties two feeders' busbars together.
type HVCoupler struct {
	ID        int64     `json:"id"`
	NetworkID int64     `json:"network_id"`
	Name      string    `json:"name"`
	LeftID    int64     `json:"left_id"`
	RightID   int64     `json:"right_id"`
	Closed    bool      `json:"closed"`
	RatingA   *float64  `json:"rating_a"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Compute fills in the roll-up counts shown above the diagram.
func (n *HVNetwork) Compute() {
	n.FeederCount = len(n.Feeders)
	n.WayCount, n.TransformerCount, n.ProtectedCount, n.LinkedCount = 0, 0, 0, 0
	for i := range n.Feeders {
		for _, w := range n.Feeders[i].Ways {
			n.WayCount++
			if w.HasTransformer {
				n.TransformerCount++
			}
			if w.Protection != ProtectionNone && w.Protection != "" {
				n.ProtectedCount++
			}
			if w.DestBoardID != nil {
				n.LinkedCount++
			}
		}
	}
}
