package model

import "time"

// Device kinds that can sit on a conductor, in the order a drawing would show
// them (mirrors the hv_device_kind enum in PostgreSQL).
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

// ValidDeviceKind reports whether k is a kind of device that can sit on a
// conductor.
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

// HVDevice is one item on a conductor, drawn in position order down it.
type HVDevice struct {
	ID int64 `json:"id"`
	// Exactly one of WayID and FeederID is set: a device sits on an outgoing
	// way or on an incoming feeder.
	WayID    int64    `json:"way_id"`
	FeederID int64    `json:"feeder_id"`
	Kind     string   `json:"kind"`
	Name     string   `json:"name"`
	RatingA  *float64 `json:"rating_a"`
	KVA      *float64 `json:"kva"`
	Ratio    string   `json:"ratio"`
	Notes    string   `json:"notes"`
	Position int      `json:"position"`
}

// HVNetwork is the site-wide high-voltage distribution overview: the busbar
// sections, what backs each one, and the couplers between them.
type HVNetwork struct {
	ID       int64       `json:"id"`
	Name     string      `json:"name"`
	Voltage  string      `json:"voltage"`
	Sections []HVSection `json:"sections"`
	Couplers []HVCoupler `json:"couplers"`

	// Derived
	SectionCount     int `json:"section_count"`
	FeederCount      int `json:"feeder_count"`
	WayCount         int `json:"way_count"`
	TransformerCount int `json:"transformer_count"`
	ProtectedCount   int `json:"protected_count"`
	LinkedCount      int `json:"linked_count"`
}

// HVSection is one length of busbar: the incomers that back it and the ways
// that tap it. A coupler is what divides one section from the next, which is
// why a way belongs to a section rather than to any one feeder.
type HVSection struct {
	ID        int64      `json:"id"`
	NetworkID int64      `json:"network_id"`
	Name      string     `json:"name"`
	Position  int        `json:"position"`
	Feeders   []HVFeeder `json:"feeders"`
	Ways      []HVWay    `json:"ways"`
}

// Backing names the feeders that hold a section up, for labels and tooltips.
func (s HVSection) Backing() string {
	out := ""
	for i, f := range s.Feeders {
		if i > 0 {
			out += ", "
		}
		out += f.Name
	}
	return out
}

// HVFeeder is one incoming supply landing on a bus section.
type HVFeeder struct {
	ID        int64  `json:"id"`
	NetworkID int64  `json:"network_id"`
	SectionID int64  `json:"section_id"`
	Name      string `json:"name"`
	// Switchgear is the designation written beside the breaker symbol, such as
	// 22SGI1, as distinct from the feeder's own name.
	Switchgear string     `json:"switchgear"`
	Voltage    string     `json:"voltage"`
	Source     string     `json:"source"`
	RatingA    *float64   `json:"rating_a"`
	Position   int        `json:"position"`
	Devices    []HVDevice `json:"devices"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// HVWay is an outgoing way tapping a bus section: whatever is fitted on it,
// landing on a board or a named destination.
type HVWay struct {
	ID        int64    `json:"id"`
	SectionID int64    `json:"section_id"`
	Name      string   `json:"name"`
	RatingA   *float64 `json:"rating_a"`

	// Devices are everything fitted on this way, in the order they appear down
	// the conductor.
	Devices []HVDevice `json:"devices"`

	// Where the way lands. A board reference makes the destination clickable;
	// the label carries it when the destination is not a board in this system.
	DestBoardID *int64 `json:"dest_board_id"`
	DestLabel   string `json:"dest_label"`
	// DestDetail names the point at the destination the way terminates on,
	// such as TX15 at FAC1.
	DestDetail string `json:"dest_detail"`
	Notes      string `json:"notes"`
	Position   int    `json:"position"`

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

// HVCoupler ties two bus sections together.
type HVCoupler struct {
	ID             int64     `json:"id"`
	NetworkID      int64     `json:"network_id"`
	Name           string    `json:"name"`
	LeftSectionID  int64     `json:"left_section_id"`
	RightSectionID int64     `json:"right_section_id"`
	Closed         bool      `json:"closed"`
	RatingA        *float64  `json:"rating_a"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// Compute fills in the roll-up counts shown above the diagram.
func (n *HVNetwork) Compute() {
	n.SectionCount = len(n.Sections)
	n.FeederCount, n.WayCount = 0, 0
	n.TransformerCount, n.ProtectedCount, n.LinkedCount = 0, 0, 0
	for i := range n.Sections {
		sec := &n.Sections[i]
		n.FeederCount += len(sec.Feeders)
		for _, f := range sec.Feeders {
			for _, d := range f.Devices {
				if d.Kind == DeviceTransformer {
					n.TransformerCount++
				}
			}
		}
		for _, w := range sec.Ways {
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
