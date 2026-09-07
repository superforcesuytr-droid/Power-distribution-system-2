package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// seedDemo populates an empty database with a small, realistic site so the
// application is usable immediately after first connection. It is a no-op
// when any building already exists.
func seedDemo(ctx context.Context, tx pgx.Tx) error {
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM buildings`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}

	type circuit struct {
		name, code string
		load       float64
		status     string
		equip      int
		service    string
	}
	type mcb struct {
		name     string
		rating   float64
		circuits []circuit
	}
	type mccb struct {
		name   string
		rating float64
		mcbs   []mcb
	}
	type board struct {
		code, voltage, phases, level, location, technician string
		mccbs                                              []mccb
	}
	type building struct {
		name, icon string
		boards     []board
	}

	site := []building{
		{name: "Main FAB", icon: "🏭", boards: []board{
			{code: "FAC1", voltage: "400V", phases: "3PH", level: "Level 1", location: "Panel Room A", technician: "Technician A", mccbs: []mccb{
				{name: "MCCB-A", rating: 160, mcbs: []mcb{{name: "MCB-01", rating: 63}, {name: "MCB-02", rating: 40}}},
			}},
			{code: "FAC5", voltage: "400V", phases: "3PH", level: "Level 1", location: "Panel Room A", technician: "Technician A", mccbs: []mccb{
				{name: "MCCB-A", rating: 200, mcbs: []mcb{
					{name: "MCB-01", rating: 63, circuits: []circuit{
						{"AHU-1A", "FAC5-MCB01-C001", 65, "active", 1, "Facility 5"},
						{"AHU-2B", "FAC5-MCB01-C002", 17, "active", 1, "Facility 5"},
					}},
					{name: "MCB-02", rating: 40, circuits: []circuit{
						{"Lighting-GF", "FAC5-MCB02-C001", 12, "active", 1, "Facility 5"},
						{"Pump-CW1", "FAC5-MCB02-C002", 0, "maintenance", 1, "Facility 5"},
					}},
				}},
				{name: "MCCB-B", rating: 100, mcbs: []mcb{
					{name: "MCB-03", rating: 32, circuits: []circuit{
						{"Server-UPS1", "FAC5-MCB03-C001", 40, "active", 1, "Facility 5"},
					}},
					{name: "MCB-04", rating: 25},
				}},
			}},
		}},
		{name: "Main FAB Cup", icon: "🏢", boards: []board{
			{code: "FAC2", voltage: "400V", phases: "3PH", level: "Level 2", location: "Panel Room B", technician: "Technician B", mccbs: []mccb{
				{name: "MCCB-A", rating: 125, mcbs: []mcb{{name: "MCB-01", rating: 32}}},
			}},
			{code: "FAC3", voltage: "400V", phases: "3PH", level: "Level 2", location: "Panel Room B", technician: "Technician B", mccbs: []mccb{
				{name: "MCCB-A", rating: 125, mcbs: []mcb{{name: "MCB-01", rating: 32}, {name: "MCB-02", rating: 20}}},
			}},
		}},
		{name: "Annex 10", icon: "🏗️"},
		{name: "Annex 10 Cup", icon: "🏬"},
	}

	for _, b := range site {
		var bID int64
		if err := tx.QueryRow(ctx, `INSERT INTO buildings (name, icon) VALUES ($1, $2) RETURNING id`, b.name, b.icon).Scan(&bID); err != nil {
			return fmt.Errorf("seed building %s: %w", b.name, err)
		}
		for bi, bd := range b.boards {
			var boardID int64
			if err := tx.QueryRow(ctx, `INSERT INTO boards (building_id, code, voltage, phases, level, location, technician, position)
				VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
				bID, bd.code, bd.voltage, bd.phases, bd.level, bd.location, bd.technician, bi).Scan(&boardID); err != nil {
				return fmt.Errorf("seed board %s: %w", bd.code, err)
			}
			for mi, m := range bd.mccbs {
				var mccbID int64
				if err := tx.QueryRow(ctx, `INSERT INTO mccbs (board_id, name, rating_a, position) VALUES ($1,$2,$3,$4) RETURNING id`,
					boardID, m.name, m.rating, mi).Scan(&mccbID); err != nil {
					return fmt.Errorf("seed mccb %s: %w", m.name, err)
				}
				for ci, mb := range m.mcbs {
					var mcbID int64
					if err := tx.QueryRow(ctx, `INSERT INTO mcbs (mccb_id, name, rating_a, position) VALUES ($1,$2,$3,$4) RETURNING id`,
						mccbID, mb.name, mb.rating, ci).Scan(&mcbID); err != nil {
						return fmt.Errorf("seed mcb %s: %w", mb.name, err)
					}
					for _, c := range mb.circuits {
						if _, err := tx.Exec(ctx, `INSERT INTO circuits (mcb_id, name, code, load_a, status, equipment_count, service)
							VALUES ($1,$2,$3,$4,$5,$6,$7)`,
							mcbID, c.name, c.code, c.load, c.status, c.equip, c.service); err != nil {
							return fmt.Errorf("seed circuit %s: %w", c.code, err)
						}
					}
				}
			}
		}
	}
	_, err := tx.Exec(ctx, `INSERT INTO audit_log (actor_role, action, entity, summary)
		VALUES ('system', 'seed', 'database', 'Loaded demo site data into an empty database')`)
	return err
}
