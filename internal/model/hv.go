package model

import "time"

// Device kinds that can sit on an outgoing way, in the order a drawing would
// show them (mirrors the hv_device_kind enum in PostgreSQL).
const (
	DeviceSwitchgear  = "switchgear"
	DeviceIsolator    = "isolator"
	DeviceRCCB        = "rccb"
	DeviceELR         = "elr"
	DeviceELCB        = "elcb"
	DeviceTransformer = "transformer"
	DeviceFuse        = "fuse"
	DeviceMeter       = "meter"
)

// DeviceKinds lists every kind with the label a drawing gives it.
var DeviceKinds = []struct{ Kind, Label string }{
	{DeviceSwitchgear, "Switchgear"},
	{DeviceIsolator, "Isolator"},
	{DeviceRCCB, "RCCB"},
	{DeviceELR, "ELR"},
	{DeviceELCB, "ELCB"},
	{DeviceTransformer, "Transformer"},
	{DeviceFuse, "Fuse"},
	{DeviceMeter, "Meter"},
}

// ValidDeviceKind reports whether k is a kind of device that can sit on a way.
func ValidDeviceKind(k string) bool {
	for _, d := range DeviceKinds {
		if d.Kind == k {
			return true
		}
	}
	return false
}

// IsProtection reports whether a device kind is earth-leakage protection,
// which is what the overview counts as "protected".
func IsProtection(k string) bool {
	return k == DeviceRCCB || k == DeviceELR || k == DeviceELCB
}

// HVDevice is one item on a way, drawn in position order down the conductor.
type HVDevice struct {
	ID       int64    `json:"id"`
	WayID    int64    `json:"way_id"`
	Kind     string   `json:"kind"`
	Name     string   `json:"name"`
	RatingA  *float64 `json:"rating_a"`
	KVA      *float64 `json:"kva"`
	Ratio    string   `json:"ratio"`
	Notes    string   `json:"notes"`
	Position int      `json:"position"`
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
	ID        int64  `json:"id"`
	NetworkID int64  `json:"network_id"`
	Name      string `json:"name"`
	// Switchgear is the designation written beside the breaker symbol, such as
	// 22SGI1, as distinct from the feeder's own name.
	Switchgear string    `json:"switchgear"`
	Voltage    string    `json:"voltage"`
	Source     string    `json:"source"`
	RatingA    *float64  `json:"rating_a"`
	Position   int       `json:"position"`
	Ways       []HVWay   `json:"ways"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// HVWay is an outgoing way from a feeder's switchgear: optionally protected,
// optionally through a transformer, landing on a board or a named destination.
type HVWay struct {
	ID       int64    `json:"id"`
	FeederID int64    `json:"feeder_id"`
	Name     string   `json:"name"`
	RatingA  *float64 `json:"rating_a"`

	// Devices are everything fitted on this way, in the order they appear down
	// the conductor.
	Devices []HVDevice `json:"devices"`

	// Where the way lands. A board reference makes the destination clickable;
	// the label carries it when the destination is not a board in this system.
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
			protected := false
			for _, d := range w.Devices {
				switch {
				case d.Kind == DeviceTransformer:
					n.TransformerCount++
				case IsProtection(d.Kind):
					protected = true
				}
			}
			if protected {
				n.ProtectedCount++
			}
			if w.DestBoardID != nil {
				n.LinkedCount++
			}
		}
	}
}
