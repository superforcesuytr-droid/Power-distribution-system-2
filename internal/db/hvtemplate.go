package db

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Ready-made drawings: a site's high-tension layout written out once, so it
// can be put on a new drawing in one go instead of a switchboard, a section
// and a way at a time. Each is a JSON file under templates/, carried inside
// the executable so a machine with no network has them too.
//
//go:embed templates/*.json
var templateFS embed.FS

// HVTemplate is a whole drawing: its switchboards in the order they are set
// out, each with its bus sections, and on each section the incomers backing it
// and the ways tapping it, left to right as the drawing shows them.
type HVTemplate struct {
	Key         string          `json:"key"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Tier        string          `json:"tier"`
	Voltage     string          `json:"voltage"`
	Boards      []templateBoard `json:"boards,omitempty"`
	Counts      map[string]int  `json:"counts,omitempty"`
}

type templateBoard struct {
	Name      string            `json:"name"`
	Voltage   string            `json:"voltage"`
	Phases    string            `json:"phases"`
	Frequency string            `json:"frequency"`
	CurrentA  *float64          `json:"current_a"`
	FaultKA   *float64          `json:"fault_ka"`
	Sections  []templateSection `json:"sections"`
	Couplers  []templateCoupler `json:"couplers"`
}

type templateSection struct {
	Name    string           `json:"name"`
	Width   *float64         `json:"width"`
	PadLeft float64          `json:"pad_left"`
	Feeders []templateFeeder `json:"feeders"`
	Ways    []templateWay    `json:"ways"`
}

type templateFeeder struct {
	Name       string           `json:"name"`
	Kind       string           `json:"kind"`
	Switchgear string           `json:"switchgear"`
	Voltage    string           `json:"voltage"`
	Source     string           `json:"source"`
	OffsetX    *float64         `json:"offset_x"`
	Taps       string           `json:"taps"`
	Devices    []templateDevice `json:"devices"`
}

type templateWay struct {
	Name       string           `json:"name"`
	Head       string           `json:"head"`
	HeadName   *string          `json:"head_name"`
	DestLabel  string           `json:"dest_label"`
	DestDetail string           `json:"dest_detail"`
	Feeds      string           `json:"feeds"`
	Devices    []templateDevice `json:"devices"`
}

type templateDevice struct {
	Kind  string   `json:"kind"`
	Name  string   `json:"name"`
	KVA   *float64 `json:"kva"`
	Ratio string   `json:"ratio"`
}

type templateCoupler struct {
	Name   string `json:"name"`
	Left   string `json:"left"`
	Right  string `json:"right"`
	Closed bool   `json:"closed"`
}

func loadTemplates() ([]HVTemplate, error) {
	files, err := fs.Glob(templateFS, "templates/*.json")
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	out := make([]HVTemplate, 0, len(files))
	for _, f := range files {
		raw, err := templateFS.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var t HVTemplate
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, fmt.Errorf("template %s: %w", f, err)
		}
		out = append(out, t)
	}
	return out, nil
}

// HVTemplates lists the ready-made drawings, with how much is on each so the
// choice can say what it will put down, but without the drawing itself.
func HVTemplates() ([]HVTemplate, error) {
	all, err := loadTemplates()
	if err != nil {
		return nil, err
	}
	for i := range all {
		c := map[string]int{"switchboards": len(all[i].Boards)}
		for _, b := range all[i].Boards {
			c["couplers"] += len(b.Couplers)
			for _, s := range b.Sections {
				c["sections"]++
				c["feeders"] += len(s.Feeders)
				c["ways"] += len(s.Ways)
			}
		}
		all[i].Counts = c
		all[i].Boards = nil
	}
	return all, nil
}

func findTemplate(key string) (*HVTemplate, error) {
	all, err := loadTemplates()
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].Key == key {
			return &all[i], nil
		}
	}
	return nil, &UserError{"There is no ready-made drawing called " + key + "."}
}

// CreateHVNetworkFromTemplate puts a ready-made drawing down as a new drawing,
// all of it or none of it. It never touches a drawing that is already there,
// so it can be compared with one built by hand before either is let go.
func (s *Store) CreateHVNetworkFromTemplate(ctx context.Context, role, key, name string) (int64, error) {
	t, err := findTemplate(key)
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(name) == "" {
		name = t.Name
	}
	var netID int64
	err = s.withTx(ctx, func(tx pgx.Tx) error {
		// Put down beside a drawing of the same title - one built by hand, say -
		// it says which is which, so the two tabs can be told apart.
		var clash bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM hv_networks WHERE lower(name) = lower($1))`,
			name).Scan(&clash); err != nil {
			return err
		}
		if clash {
			name += " (full layout)"
		}
		if err := tx.QueryRow(ctx, `INSERT INTO hv_networks (name, voltage, tier, position)
			VALUES ($1, $2, $3, (SELECT coalesce(max(position),-1)+1 FROM hv_networks)) RETURNING id`,
			name, t.Voltage, t.Tier).Scan(&netID); err != nil {
			return err
		}

		boardID := map[string]int64{}        // switchboard by name
		wayID := map[string]int64{}          // way by designation, across the whole drawing
		feeds := map[int64]string{}          // way -> the switchboard it runs down to
		taps := map[int64]string{}           // incomer -> the way it taps
		secPos, wayPos, feederPos := 0, 0, 0 // positions run across the drawing, in drawing order

		device := func(ownerCol string, owner int64, pos int, d templateDevice) error {
			_, err := tx.Exec(ctx, `INSERT INTO hv_devices (`+ownerCol+`, kind, name, kva, ratio, position)
				VALUES ($1, $2::hv_device_kind, $3, $4, $5, $6)`, owner, d.Kind, d.Name, d.KVA, d.Ratio, pos)
			if err != nil {
				return fmt.Errorf("%s on %s: %w", d.Kind, d.Name, err)
			}
			return nil
		}

		for bi, b := range t.Boards {
			var bid int64
			if err := tx.QueryRow(ctx, `INSERT INTO hv_switchboards
				(network_id, name, voltage, phases, frequency, current_a, fault_ka, position)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
				netID, b.Name, b.Voltage, b.Phases, b.Frequency, b.CurrentA, b.FaultKA, bi).Scan(&bid); err != nil {
				return fmt.Errorf("switchboard %s: %w", b.Name, err)
			}
			boardID[b.Name] = bid
			sectionID := map[string]int64{}

			for _, sec := range b.Sections {
				var sid int64
				if err := tx.QueryRow(ctx, `INSERT INTO hv_sections
					(network_id, switchboard_id, name, position, width, pad_left)
					VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
					netID, bid, sec.Name, secPos, sec.Width, sec.PadLeft).Scan(&sid); err != nil {
					return fmt.Errorf("%s %s: %w", b.Name, sec.Name, err)
				}
				secPos++
				sectionID[sec.Name] = sid

				for _, f := range sec.Feeders {
					kind := f.Kind
					if kind == "" {
						kind = "supply"
					}
					var fid int64
					if err := tx.QueryRow(ctx, `INSERT INTO hv_feeders
						(network_id, section_id, name, switchgear, kind, voltage, source, position, offset_x)
						VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) RETURNING id`,
						netID, sid, f.Name, f.Switchgear, kind, f.Voltage, f.Source, feederPos, f.OffsetX).Scan(&fid); err != nil {
						return fmt.Errorf("incomer %s: %w", f.Name, err)
					}
					feederPos++
					// An incomer lands through its switchgear unless it says otherwise.
					devs := f.Devices
					if len(devs) == 0 {
						devs = []templateDevice{{Kind: "switchgear", Name: f.Switchgear}}
					}
					for i, d := range devs {
						if err := device("feeder_id", fid, i, d); err != nil {
							return err
						}
					}
					if f.Taps != "" {
						taps[fid] = f.Taps
					}
				}

				for _, w := range sec.Ways {
					var wid int64
					if err := tx.QueryRow(ctx, `INSERT INTO hv_ways
						(section_id, name, position, dest_label, dest_detail)
						VALUES ($1,$2,$3,$4,$5) RETURNING id`,
						sid, w.Name, wayPos, w.DestLabel, w.DestDetail).Scan(&wid); err != nil {
						return fmt.Errorf("way %s: %w", w.Name, err)
					}
					wayPos++
					if _, dup := wayID[w.Name]; dup {
						return &UserError{"The drawing names " + w.Name + " twice."}
					}
					wayID[w.Name] = wid
					// What is at the bar comes first: switchgear carries the way's
					// own designation, a switch only what it is given.
					pos := 0
					if w.Head != "" {
						headName := ""
						if w.Head == "switchgear" {
							headName = w.Name
						}
						if w.HeadName != nil {
							headName = *w.HeadName
						}
						if err := device("way_id", wid, pos, templateDevice{Kind: w.Head, Name: headName}); err != nil {
							return err
						}
						pos++
					}
					for _, d := range w.Devices {
						if err := device("way_id", wid, pos, d); err != nil {
							return err
						}
						pos++
					}
					if w.Feeds != "" {
						feeds[wid] = w.Feeds
					}
				}
			}

			for _, c := range b.Couplers {
				l, r := sectionID[c.Left], sectionID[c.Right]
				if l == 0 || r == 0 {
					return &UserError{"Coupler " + c.Name + " ties a section " + b.Name + " does not have."}
				}
				if _, err := tx.Exec(ctx, `INSERT INTO hv_couplers
					(network_id, name, left_section_id, right_section_id, closed)
					VALUES ($1,$2,$3,$4,$5)`, netID, c.Name, l, r, c.Closed); err != nil {
					return fmt.Errorf("coupler %s: %w", c.Name, err)
				}
			}
		}

		// Now that everything has an id, the connections between boards: a way
		// running down to the board it feeds, and an incomer tapping a way on
		// the board beside it.
		for wid, board := range feeds {
			bid, ok := boardID[board]
			if !ok {
				return &UserError{"A way feeds " + board + ", which is not on the drawing."}
			}
			if _, err := tx.Exec(ctx, `UPDATE hv_ways SET dest_switchboard_id = $2 WHERE id = $1`, wid, bid); err != nil {
				return err
			}
		}
		for fid, way := range taps {
			wid, ok := wayID[way]
			if !ok {
				return &UserError{"An incomer taps " + way + ", which is not on the drawing."}
			}
			if _, err := tx.Exec(ctx, `UPDATE hv_feeders SET source_way_id = $2 WHERE id = $1`, fid, wid); err != nil {
				return err
			}
		}
		return s.audit(ctx, tx, role, "create", "hv_network", netID,
			fmt.Sprintf("Added the drawing %s from the ready-made %s layout", name, t.Name))
	})
	return netID, err
}
