package model

import (
	"strconv"
	"time"
)

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
	DeviceChiller     = "chiller"
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
	{DeviceChiller, "Chiller"},
}

// IsHead reports whether a device kind can stand at the head of a way, drawn
// on the conductor where it taps the bus: a switchgear X, or an open switch.
func IsHead(k string) bool {
	return k == DeviceSwitchgear || k == DeviceIsolator
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

// Feeder kinds: what stands behind an incomer, which decides the symbol drawn
// above its switchgear.
const (
	FeederSupply    = "supply"
	FeederGenerator = "generator"
)

// FeederKinds lists every kind of incomer with the label a form gives it.
var FeederKinds = []struct{ Kind, Label string }{
	{FeederSupply, "Incoming supply"},
	{FeederGenerator, "Generator"},
}

// ValidFeederKind reports whether k is a kind of incomer.
func ValidFeederKind(k string) bool {
	for _, f := range FeederKinds {
		if f.Kind == k {
			return true
		}
	}
	return false
}

// HVNetwork is the site-wide high-voltage distribution overview: the
// switchboards, drawn top to bottom in the order the site steps down through
// them.
type HVNetwork struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Voltage string `json:"voltage"`
	// Tier says which drawing this is: the high-tension side of the site or the
	// low-tension side. It is what the picker above the diagram labels.
	Tier         string          `json:"tier"`
	Position     int             `json:"position"`
	Switchboards []HVSwitchboard `json:"switchboards"`

	// Derived
	BoardCount       int `json:"board_count"`
	SectionCount     int `json:"section_count"`
	FeederCount      int `json:"feeder_count"`
	WayCount         int `json:"way_count"`
	TransformerCount int `json:"transformer_count"`
	ProtectedCount   int `json:"protected_count"`
	LinkedCount      int `json:"linked_count"`
}

// Tiers a drawing can be at.
const (
	TierHT = "ht"
	TierLT = "lt"
)

// Tiers lists every tier with the label the picker gives it.
var Tiers = []struct{ Tier, Label string }{
	{TierHT, "High tension"},
	{TierLT, "Low tension"},
}

// ValidTier reports whether t names a tier a drawing can be at.
func ValidTier(t string) bool {
	for _, x := range Tiers {
		if x.Tier == t {
			return true
		}
	}
	return false
}

// HVSwitchboard is one switchboard: its rating, the bus sections it is split
// into and the couplers between them. A way on the board above lands on it.
type HVSwitchboard struct {
	ID        int64    `json:"id"`
	NetworkID int64    `json:"network_id"`
	Name      string   `json:"name"`
	Voltage   string   `json:"voltage"`
	Phases    string   `json:"phases"`
	Frequency string   `json:"frequency"`
	CurrentA  *float64 `json:"current_a"`
	FaultKA   *float64 `json:"fault_ka"`
	Position  int      `json:"position"`

	Sections  []HVSection `json:"sections"`
	Couplers  []HVCoupler `json:"couplers"`
	CreatedAt time.Time   `json:"created_at"`
	UpdatedAt time.Time   `json:"updated_at"`

	// Derived, and only filled when switchboards are listed across the whole
	// site: which drawing this one is on.
	NetworkName     string `json:"network_name,omitempty"`
	NetworkTier     string `json:"network_tier,omitempty"`
	NetworkPosition int    `json:"network_position,omitempty"`
}

// Rating is the line written under a switchboard's name, in the form a
// drawing writes it: 3p, 50Hz 1250A/25kA.
func (b HVSwitchboard) Rating() string {
	out := ""
	add := func(s string) {
		if s == "" {
			return
		}
		if out != "" {
			out += ", "
		}
		out += s
	}
	add(b.Phases)
	add(b.Frequency)
	amps := ""
	if b.CurrentA != nil {
		amps = trimNum(*b.CurrentA) + "A"
	}
	if b.FaultKA != nil {
		if amps != "" {
			amps += "/"
		}
		amps += trimNum(*b.FaultKA) + "kA"
	}
	if amps != "" {
		if out != "" {
			out += " "
		}
		out += amps
	}
	return out
}

// trimNum writes a rating without a pointless decimal tail.
func trimNum(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	return s
}

// HVSection is one length of busbar: the incomers that back it and the ways
// that tap it. A coupler is what divides one section from the next, which is
// why a way belongs to a section rather than to any one feeder.
type HVSection struct {
	ID            int64      `json:"id"`
	NetworkID     int64      `json:"network_id"`
	SwitchboardID int64      `json:"switchboard_id"`
	Name          string     `json:"name"`
	Position      int        `json:"position"`
	Feeders       []HVFeeder `json:"feeders"`
	Ways          []HVWay    `json:"ways"`
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
	Switchgear string `json:"switchgear"`
	// Kind decides the symbol above the switchgear: a plain supply or a
	// generator.
	Kind      string     `json:"kind"`
	Voltage   string     `json:"voltage"`
	Source    string     `json:"source"`
	RatingA   *float64   `json:"rating_a"`
	Position  int        `json:"position"`
	Devices   []HVDevice `json:"devices"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
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
	// DestSwitchboardID lands the way on a switchboard drawn below this one
	// instead of in a destination box, the way a transformer feeds the next
	// voltage down.
	DestSwitchboardID *int64 `json:"dest_switchboard_id"`
	DestLabel         string `json:"dest_label"`
	// DestDetail names the point at the destination the way terminates on,
	// such as TX15 at FAC1.
	DestDetail string `json:"dest_detail"`
	Notes      string `json:"notes"`
	Position   int    `json:"position"`

	// Derived: filled from the linked board so the diagram can label and link
	// the destination without a second lookup.
	DestBoardCode    string `json:"dest_board_code"`
	DestBuildingName string `json:"dest_building_name"`
	// DestSwitchboardName and DestNetworkID describe the switchboard a way
	// lands on. When that board is on another drawing the way cannot be run
	// down to it, so the destination box carries a link across instead.
	DestSwitchboardName string    `json:"dest_switchboard_name"`
	DestNetworkID       int64     `json:"dest_network_id"`
	DestNetworkName     string    `json:"dest_network_name"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// Destination is what the diagram writes in the box at the foot of a way.
func (w HVWay) Destination() string {
	if w.DestSwitchboardName != "" {
		return w.DestSwitchboardName
	}
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
	SwitchboardID  int64     `json:"switchboard_id"`
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
	n.BoardCount = len(n.Switchboards)
	n.SectionCount, n.FeederCount, n.WayCount = 0, 0, 0
	n.TransformerCount, n.ProtectedCount, n.LinkedCount = 0, 0, 0
	for b := range n.Switchboards {
		board := &n.Switchboards[b]
		n.SectionCount += len(board.Sections)
		for i := range board.Sections {
			sec := &board.Sections[i]
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
				if w.DestBoardID != nil || w.DestSwitchboardID != nil {
					n.LinkedCount++
				}
			}
		}
	}
}
