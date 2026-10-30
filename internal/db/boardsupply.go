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

	"github.com/superforcesuytr-droid/power-distribution-system/internal/model"
)

// sectionOr keeps a breaker on the left half of the bus unless it is placed on
// the right: a board fed from one end has only the one half.
func sectionOr(s string) string {
	if strings.EqualFold(strings.TrimSpace(s), "B") {
		return "B"
	}
	return "A"
}

// SetBoardSupply replaces how a board is supplied: its incomers, one per half
// of the bus, and the bus section switch between the halves. A board with a
// single incomer keeps everything on section A and has no bus section.
func (s *Store) SetBoardSupply(ctx context.Context, role string, boardID int64, incomers []model.BoardIncomer, bs model.BusSection) error {
	seen := map[string]bool{}
	for i := range incomers {
		incomers[i].Section = sectionOr(incomers[i].Section)
		if seen[incomers[i].Section] {
			return &UserError{"A board has at most one incomer on each half of its bus."}
		}
		seen[incomers[i].Section] = true
	}
	if !seen["B"] {
		bs = model.BusSection{}
	}
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var code string
		if err := tx.QueryRow(ctx, `UPDATE boards SET bus_section_name = $2, bus_section_panel = $3,
			bus_section_rating_a = $4, bus_section_closed = $5, updated_at = now() WHERE id = $1 RETURNING code`,
			boardID, bs.BusSectionName, bs.BusSectionPanel, bs.BusSectionRatingA, bs.BusSectionClosed).Scan(&code); err != nil {
			if err == pgx.ErrNoRows {
				return ErrNotFound
			}
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM board_incomers WHERE board_id = $1`, boardID); err != nil {
			return err
		}
		for _, in := range incomers {
			if _, err := tx.Exec(ctx, `INSERT INTO board_incomers
				(board_id, section, name, panel, transformer, ratio, rating_a, source)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8)`, boardID, in.Section, in.Name, in.Panel,
				in.Transformer, in.Ratio, in.RatingA, in.Source); err != nil {
				return err
			}
		}
		// A board no longer split keeps its breakers, all on the one bus.
		if !seen["B"] {
			if _, err := tx.Exec(ctx, `UPDATE mccbs SET section = 'A' WHERE board_id = $1`, boardID); err != nil {
				return err
			}
		}
		summary := fmt.Sprintf("Set the supply to %s: %d incomer%s", code, len(incomers), map[bool]string{true: "", false: "s"}[len(incomers) == 1])
		if bs.BusSectionName != "" {
			state := "open"
			if bs.BusSectionClosed {
				state = "closed"
			}
			summary += fmt.Sprintf(", bus section %s %s", bs.BusSectionName, state)
		}
		return s.audit(ctx, tx, role, "update", "board", boardID, summary)
	})
}

// Ready-made boards: a switch room's board written out once from its panel
// labels, so it can be added to a building in one go. Each is a JSON file
// under boardtemplates/, carried inside the executable.
//
//go:embed boardtemplates/*.json
var boardTemplateFS embed.FS

type BoardTemplate struct {
	Key         string               `json:"key"`
	Code        string               `json:"code"`
	Description string               `json:"description"`
	Voltage     string               `json:"voltage"`
	Phases      string               `json:"phases"`
	Location    string               `json:"location"`
	Incomers    []model.BoardIncomer `json:"incomers,omitempty"`
	BusSection  *model.BusSection    `json:"bus_section,omitempty"`
	MCCBs       []templateMCCB       `json:"mccbs,omitempty"`
	Counts      map[string]int       `json:"counts,omitempty"`
}

type templateMCCB struct {
	Name     string  `json:"name"`
	RatingA  float64 `json:"rating_a"`
	Section  string  `json:"section"`
	Feeds    string  `json:"feeds"`
	Location string  `json:"location"`
}

func loadBoardTemplates() ([]BoardTemplate, error) {
	files, err := fs.Glob(boardTemplateFS, "boardtemplates/*.json")
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	out := make([]BoardTemplate, 0, len(files))
	for _, f := range files {
		raw, err := boardTemplateFS.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var t BoardTemplate
		if err := json.Unmarshal(raw, &t); err != nil {
			return nil, fmt.Errorf("board template %s: %w", f, err)
		}
		out = append(out, t)
	}
	return out, nil
}

// BoardTemplates lists the ready-made boards with how much is on each.
func BoardTemplates() ([]BoardTemplate, error) {
	all, err := loadBoardTemplates()
	if err != nil {
		return nil, err
	}
	for i := range all {
		all[i].Counts = map[string]int{"incomers": len(all[i].Incomers), "mccbs": len(all[i].MCCBs)}
		all[i].Incomers, all[i].MCCBs, all[i].BusSection = nil, nil, nil
	}
	return all, nil
}

// CreateBoardFromTemplate adds a ready-made board to a building, with its
// supply and every outgoing breaker, all of it or none of it. When the room's
// board is already there under a code that reads the same ("PE1" for "PE 1"),
// the layout fills that board in instead: its own code, building and anything
// already entered stay, and only what is missing is added.
func (s *Store) CreateBoardFromTemplate(ctx context.Context, role, key string, buildingID int64) (int64, bool, error) {
	all, err := loadBoardTemplates()
	if err != nil {
		return 0, false, err
	}
	var t *BoardTemplate
	for i := range all {
		if all[i].Key == key {
			t = &all[i]
		}
	}
	if t == nil {
		return 0, false, &UserError{"There is no ready-made board called " + key + "."}
	}
	var id int64
	var filled bool
	err = s.withTx(ctx, func(tx pgx.Tx) error {
		existing, code, err := boardLike(ctx, tx, t.Code, 0)
		if err != nil {
			return err
		}
		bs := model.BusSection{}
		if t.BusSection != nil {
			bs = *t.BusSection
		}
		if existing != 0 {
			id, filled = existing, true
			if _, err := tx.Exec(ctx, `UPDATE boards SET
				voltage = CASE WHEN voltage = '' THEN $2 ELSE voltage END,
				phases = CASE WHEN phases = '' THEN $3 ELSE phases END,
				location = CASE WHEN location = '' THEN $4 ELSE location END,
				bus_section_name = CASE WHEN bus_section_name = '' THEN $5 ELSE bus_section_name END,
				bus_section_panel = CASE WHEN bus_section_name = '' THEN $6 ELSE bus_section_panel END,
				bus_section_rating_a = CASE WHEN bus_section_name = '' THEN $7 ELSE bus_section_rating_a END,
				bus_section_closed = CASE WHEN bus_section_name = '' THEN $8 ELSE bus_section_closed END,
				updated_at = now() WHERE id = $1`, id, t.Voltage, t.Phases, t.Location,
				bs.BusSectionName, bs.BusSectionPanel, bs.BusSectionRatingA, bs.BusSectionClosed); err != nil {
				return err
			}
		} else {
			code = t.Code
			if err := tx.QueryRow(ctx, `INSERT INTO boards (building_id, code, voltage, phases, location, position,
				bus_section_name, bus_section_panel, bus_section_rating_a, bus_section_closed)
				VALUES ($1,$2,$3,$4,$5,(SELECT coalesce(max(position),-1)+1 FROM boards WHERE building_id = $1),$6,$7,$8,$9)
				RETURNING id`, buildingID, t.Code, t.Voltage, t.Phases, t.Location,
				bs.BusSectionName, bs.BusSectionPanel, bs.BusSectionRatingA, bs.BusSectionClosed).Scan(&id); err != nil {
				return err
			}
		}
		for _, in := range t.Incomers {
			if _, err := tx.Exec(ctx, `INSERT INTO board_incomers
				(board_id, section, name, panel, transformer, ratio, rating_a, source)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (board_id, section) DO NOTHING`, id, sectionOr(in.Section), in.Name, in.Panel,
				in.Transformer, in.Ratio, in.RatingA, in.Source); err != nil {
				return fmt.Errorf("incomer %s: %w", in.Name, err)
			}
		}
		added := 0
		for _, m := range t.MCCBs {
			// A breaker already entered keeps its name, rating and MCBs and
			// takes only the half of the bus, what it feeds and where it is.
			tag, err := tx.Exec(ctx, `UPDATE mccbs SET section = $3,
				feeds = CASE WHEN feeds = '' THEN $4 ELSE feeds END,
				location = CASE WHEN location = '' THEN $5 ELSE location END, updated_at = now()
				WHERE board_id = $1 AND `+nameKey+` = upper(regexp_replace($2, '[^[:alnum:]]', '', 'g'))`,
				id, m.Name, sectionOr(m.Section), m.Feeds, m.Location)
			if err != nil {
				return fmt.Errorf("breaker %s: %w", m.Name, err)
			}
			if tag.RowsAffected() > 0 {
				continue
			}
			if _, err := tx.Exec(ctx, `INSERT INTO mccbs (board_id, name, rating_a, section, feeds, location, position)
				VALUES ($1,$2,$3,$4,$5,$6,(SELECT coalesce(max(position),-1)+1 FROM mccbs WHERE board_id = $1))`,
				id, m.Name, m.RatingA, sectionOr(m.Section), m.Feeds, m.Location); err != nil {
				return fmt.Errorf("breaker %s: %w", m.Name, err)
			}
			added++
		}
		if filled {
			return s.audit(ctx, tx, role, "update", "board", id,
				fmt.Sprintf("Filled in %s from its ready-made layout: %d breakers added, %d already there", code, added, len(t.MCCBs)-added))
		}
		return s.audit(ctx, tx, role, "create", "board", id,
			fmt.Sprintf("Added board %s from its ready-made layout, with %d outgoing breakers", code, len(t.MCCBs)))
	})
	return id, filled, err
}
