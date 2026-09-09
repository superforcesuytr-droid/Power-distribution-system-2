package db

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/superforcesuytr-droid/power-distribution-system/internal/model"
)

// HVNetwork loads the whole high-voltage overview: the busbar sections in
// order, the feeders backing each and the ways tapping it, and the couplers
// between sections.
func (s *Store) HVNetwork(ctx context.Context) (*model.HVNetwork, error) {
	pool, err := s.getPool()
	if err != nil {
		return nil, err
	}
	var n model.HVNetwork
	err = pool.QueryRow(ctx, `SELECT id, name, voltage FROM hv_networks ORDER BY id LIMIT 1`).
		Scan(&n.ID, &n.Name, &n.Voltage)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	n.Sections = []model.HVSection{}
	n.Couplers = []model.HVCoupler{}

	secAt := map[int64]int{}
	srows, err := pool.Query(ctx, `SELECT id, network_id, name, position FROM hv_sections
		WHERE network_id = $1 ORDER BY position, id`, n.ID)
	if err != nil {
		return nil, err
	}
	for srows.Next() {
		var sec model.HVSection
		if err := srows.Scan(&sec.ID, &sec.NetworkID, &sec.Name, &sec.Position); err != nil {
			srows.Close()
			return nil, err
		}
		sec.Feeders = []model.HVFeeder{}
		sec.Ways = []model.HVWay{}
		secAt[sec.ID] = len(n.Sections)
		n.Sections = append(n.Sections, sec)
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return nil, err
	}

	feederAt := map[int64][2]int{}
	rows, err := pool.Query(ctx, `SELECT id, network_id, coalesce(section_id, 0), name, switchgear, voltage, source,
		rating_a, position, created_at, updated_at
		FROM hv_feeders WHERE network_id = $1 ORDER BY position, name`, n.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var f model.HVFeeder
		if err := rows.Scan(&f.ID, &f.NetworkID, &f.SectionID, &f.Name, &f.Switchgear, &f.Voltage, &f.Source,
			&f.RatingA, &f.Position, &f.CreatedAt, &f.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		f.Devices = []model.HVDevice{}
		if i, ok := secAt[f.SectionID]; ok {
			n.Sections[i].Feeders = append(n.Sections[i].Feeders, f)
			feederAt[f.ID] = [2]int{i, len(n.Sections[i].Feeders) - 1}
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Ways, with the destination board resolved so the diagram can link to it.
	wayAt := map[int64][2]int{}
	wrows, err := pool.Query(ctx, `SELECT w.id, coalesce(w.section_id, 0), w.name, w.rating_a,
		w.dest_board_id, w.dest_label, w.dest_detail, w.notes, w.position, w.created_at, w.updated_at,
		coalesce(bo.code, ''), coalesce(b.name, '')
		FROM hv_ways w
		JOIN hv_sections sec ON sec.id = w.section_id
		LEFT JOIN boards bo ON bo.id = w.dest_board_id
		LEFT JOIN buildings b ON b.id = bo.building_id
		WHERE sec.network_id = $1 ORDER BY w.position, w.name`, n.ID)
	if err != nil {
		return nil, err
	}
	for wrows.Next() {
		var w model.HVWay
		if err := wrows.Scan(&w.ID, &w.SectionID, &w.Name, &w.RatingA,
			&w.DestBoardID, &w.DestLabel, &w.DestDetail, &w.Notes, &w.Position, &w.CreatedAt, &w.UpdatedAt,
			&w.DestBoardCode, &w.DestBuildingName); err != nil {
			wrows.Close()
			return nil, err
		}
		w.Devices = []model.HVDevice{}
		if i, ok := secAt[w.SectionID]; ok {
			n.Sections[i].Ways = append(n.Sections[i].Ways, w)
			wayAt[w.ID] = [2]int{i, len(n.Sections[i].Ways) - 1}
		}
	}
	wrows.Close()
	if err := wrows.Err(); err != nil {
		return nil, err
	}

	drows, err := pool.Query(ctx, `SELECT d.id, coalesce(d.way_id, 0), coalesce(d.feeder_id, 0),
		d.kind::text, d.name, d.rating_a, d.kva, d.ratio, d.notes, d.position
		FROM hv_devices d
		LEFT JOIN hv_ways w ON w.id = d.way_id
		LEFT JOIN hv_feeders f ON f.id = d.feeder_id
		WHERE coalesce(w.section_id, 0) IN (SELECT id FROM hv_sections WHERE network_id = $1)
		   OR coalesce(f.network_id, 0) = $1
		ORDER BY d.position, d.id`, n.ID)
	if err != nil {
		return nil, err
	}
	for drows.Next() {
		var d model.HVDevice
		if err := drows.Scan(&d.ID, &d.WayID, &d.FeederID, &d.Kind, &d.Name, &d.RatingA, &d.KVA, &d.Ratio,
			&d.Notes, &d.Position); err != nil {
			drows.Close()
			return nil, err
		}
		if d.FeederID != 0 {
			if p, ok := feederAt[d.FeederID]; ok {
				f := &n.Sections[p[0]].Feeders[p[1]]
				f.Devices = append(f.Devices, d)
			}
			continue
		}
		if p, ok := wayAt[d.WayID]; ok {
			w := &n.Sections[p[0]].Ways[p[1]]
			w.Devices = append(w.Devices, d)
		}
	}
	drows.Close()
	if err := drows.Err(); err != nil {
		return nil, err
	}

	crows, err := pool.Query(ctx, `SELECT id, network_id, name, left_section_id, right_section_id, closed,
		rating_a, created_at, updated_at
		FROM hv_couplers WHERE network_id = $1 ORDER BY id`, n.ID)
	if err != nil {
		return nil, err
	}
	for crows.Next() {
		var c model.HVCoupler
		if err := crows.Scan(&c.ID, &c.NetworkID, &c.Name, &c.LeftSectionID, &c.RightSectionID, &c.Closed,
			&c.RatingA, &c.CreatedAt, &c.UpdatedAt); err != nil {
			crows.Close()
			return nil, err
		}
		n.Couplers = append(n.Couplers, c)
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return nil, err
	}

	// Feeders and ways read left to right in the order they are arranged on the
	// bar, falling back to the same natural name order used everywhere else.
	for i := range n.Sections {
		sec := &n.Sections[i]
		sort.SliceStable(sec.Feeders, func(a, b int) bool {
			if sec.Feeders[a].Position != sec.Feeders[b].Position {
				return sec.Feeders[a].Position < sec.Feeders[b].Position
			}
			return model.NaturalLess(sec.Feeders[a].Name, sec.Feeders[b].Name)
		})
		sort.SliceStable(sec.Ways, func(a, b int) bool {
			if sec.Ways[a].Position != sec.Ways[b].Position {
				return sec.Ways[a].Position < sec.Ways[b].Position
			}
			return model.NaturalLess(sec.Ways[a].Name, sec.Ways[b].Name)
		})
		for j := range sec.Feeders {
			d := sec.Feeders[j].Devices
			sort.SliceStable(d, func(a, b int) bool { return d[a].Position < d[b].Position })
		}
		for j := range sec.Ways {
			d := sec.Ways[j].Devices
			sort.SliceStable(d, func(a, b int) bool { return d[a].Position < d[b].Position })
		}
	}
	n.Compute()
	return &n, nil
}

// defaultSection returns the section a new feeder or way should land on when
// the caller does not name one, creating the first section if there is none.
func (s *Store) defaultSection(ctx context.Context, tx pgx.Tx, networkID int64) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `SELECT id FROM hv_sections WHERE network_id = $1 ORDER BY position, id LIMIT 1`, networkID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `INSERT INTO hv_sections (network_id, name, position) VALUES ($1, 'Section A', 0) RETURNING id`,
			networkID).Scan(&id)
	}
	return id, err
}

// UpdateHVNetwork renames the overview.
func (s *Store) UpdateHVNetwork(ctx context.Context, role string, id int64, name, voltage string) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE hv_networks SET name = $2, voltage = $3, updated_at = now() WHERE id = $1`,
			id, name, voltage)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return s.audit(ctx, tx, role, "update", "hv_network", id, "Renamed the distribution overview to "+name)
	})
}

// Sections -----------------------------------------------------------------

func (s *Store) CreateHVSection(ctx context.Context, role string, networkID int64, name string) (int64, error) {
	var id int64
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO hv_sections (network_id, name, position)
			VALUES ($1, $2, (SELECT coalesce(max(position),-1)+1 FROM hv_sections WHERE network_id = $1))
			RETURNING id`, networkID, name).Scan(&id); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "create", "hv_section", id, "Added bus section "+name)
	})
	return id, err
}

func (s *Store) UpdateHVSection(ctx context.Context, role string, id int64, name string) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE hv_sections SET name = $2, updated_at = now() WHERE id = $1`, id, name)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return s.audit(ctx, tx, role, "update", "hv_section", id, "Renamed bus section to "+name)
	})
}

func (s *Store) DeleteHVSection(ctx context.Context, role string, id int64) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var name string
		if err := tx.QueryRow(ctx, `DELETE FROM hv_sections WHERE id = $1 RETURNING name`, id).Scan(&name); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		return s.audit(ctx, tx, role, "delete", "hv_section", id,
			"Deleted bus section "+name+", its feeders and its ways")
	})
}

// Feeders -----------------------------------------------------------------

func (s *Store) CreateHVFeeder(ctx context.Context, role string, f model.HVFeeder) (int64, error) {
	var id int64
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		section := f.SectionID
		if section == 0 {
			var err error
			if section, err = s.defaultSection(ctx, tx, f.NetworkID); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(ctx, `INSERT INTO hv_feeders (network_id, section_id, name, switchgear, voltage, source, rating_a, position)
			VALUES ($1,$2,$3,$4,$5,$6,$7,(SELECT coalesce(max(position),-1)+1 FROM hv_feeders WHERE network_id = $1))
			RETURNING id`, f.NetworkID, section, f.Name, f.Switchgear, f.Voltage, f.Source, f.RatingA).Scan(&id); err != nil {
			return err
		}
		// A feeder is drawn from its switchgear down, so a new one starts with one.
		name := f.Switchgear
		if name == "" {
			name = f.Name
		}
		if _, err := tx.Exec(ctx, `INSERT INTO hv_devices (feeder_id, kind, name, rating_a, position)
			VALUES ($1, 'switchgear', $2, $3, 0)`, id, name, f.RatingA); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "create", "hv_feeder", id, "Added feeder "+f.Name)
	})
	return id, err
}

func (s *Store) UpdateHVFeeder(ctx context.Context, role string, f model.HVFeeder) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE hv_feeders SET name = $2, switchgear = $3, voltage = $4, source = $5,
			rating_a = $6, section_id = coalesce(nullif($7::bigint, 0), section_id), updated_at = now()
			WHERE id = $1`, f.ID, f.Name, f.Switchgear, f.Voltage, f.Source, f.RatingA, f.SectionID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return s.audit(ctx, tx, role, "update", "hv_feeder", f.ID, "Updated feeder "+f.Name)
	})
}

func (s *Store) DeleteHVFeeder(ctx context.Context, role string, id int64) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var name string
		if err := tx.QueryRow(ctx, `DELETE FROM hv_feeders WHERE id = $1 RETURNING name`, id).Scan(&name); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		return s.audit(ctx, tx, role, "delete", "hv_feeder", id, "Deleted feeder "+name)
	})
}

// MoveHVFeeder shifts a feeder one place left or right along the busbar.
func (s *Store) MoveHVFeeder(ctx context.Context, role string, id int64, delta int) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var networkID int64
		var pos int
		var name string
		if err := tx.QueryRow(ctx, `SELECT network_id, position, name FROM hv_feeders WHERE id = $1`, id).
			Scan(&networkID, &pos, &name); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if _, err := tx.Exec(ctx, `WITH ordered AS (
			SELECT id, row_number() OVER (ORDER BY position, name) - 1 AS rn
			FROM hv_feeders WHERE network_id = $1)
			UPDATE hv_feeders f SET position = o.rn FROM ordered o WHERE f.id = o.id`, networkID); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `SELECT position FROM hv_feeders WHERE id = $1`, id).Scan(&pos); err != nil {
			return err
		}
		target := pos + delta
		var otherID int64
		err := tx.QueryRow(ctx, `SELECT id FROM hv_feeders WHERE network_id = $1 AND position = $2`, networkID, target).Scan(&otherID)
		if errors.Is(err, pgx.ErrNoRows) {
			return &UserError{"That feeder is already at the end."}
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE hv_feeders SET position = $2, updated_at = now() WHERE id = $1`, otherID, pos); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE hv_feeders SET position = $2, updated_at = now() WHERE id = $1`, id, target); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "update", "hv_feeder", id, fmt.Sprintf("Moved feeder %s along the busbar", name))
	})
}

// PlaceHVFeeder moves an incoming feeder to a position on a bus section, which
// may be a different section from the one backing it now. Feeder positions run
// across the whole network, so the whole run is renumbered section by section
// to keep the drawing and the stored order the same.
func (s *Store) PlaceHVFeeder(ctx context.Context, role string, id, sectionID int64, index int) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var networkID, from int64
		var name string
		if err := tx.QueryRow(ctx, `SELECT network_id, section_id, name FROM hv_feeders WHERE id = $1`, id).
			Scan(&networkID, &from, &name); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		to := sectionID
		if to == 0 {
			to = from
		}
		var secName string
		if err := tx.QueryRow(ctx, `SELECT name FROM hv_sections WHERE id = $1 AND network_id = $2`,
			to, networkID).Scan(&secName); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return &UserError{"That bus section is not on this network."}
			}
			return err
		}

		order, err := sectionOrder(ctx, tx, networkID)
		if err != nil {
			return err
		}
		held := map[int64][]int64{}
		rows, err := tx.Query(ctx, `SELECT f.id, f.section_id FROM hv_feeders f
			JOIN hv_sections s ON s.id = f.section_id
			WHERE f.network_id = $1 AND f.id <> $2
			ORDER BY s.position, s.id, f.position, f.name`, networkID, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var fid, sid int64
			if err := rows.Scan(&fid, &sid); err != nil {
				rows.Close()
				return err
			}
			held[sid] = append(held[sid], fid)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		pos := 0
		for _, sid := range order {
			run := held[sid]
			if sid == to {
				run = insertAt(run, id, index)
			}
			for _, fid := range run {
				section := sid
				if _, err := tx.Exec(ctx, `UPDATE hv_feeders SET section_id = $2, position = $3, updated_at = now()
					WHERE id = $1`, fid, section, pos); err != nil {
					return err
				}
				pos++
			}
		}
		return s.audit(ctx, tx, role, "update", "hv_feeder", id, "Moved feeder "+name+" to "+secName)
	})
}

// sectionOrder lists a network's bus sections left to right.
func sectionOrder(ctx context.Context, tx pgx.Tx, networkID int64) ([]int64, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM hv_sections WHERE network_id = $1 ORDER BY position, id`, networkID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// insertAt puts id into run at index, clamping an index off either end.
func insertAt(run []int64, id int64, index int) []int64 {
	if index < 0 {
		index = 0
	}
	if index > len(run) {
		index = len(run)
	}
	out := make([]int64, 0, len(run)+1)
	out = append(out, run[:index]...)
	out = append(out, id)
	out = append(out, run[index:]...)
	return out
}

// Ways --------------------------------------------------------------------

func (s *Store) CreateHVWay(ctx context.Context, role string, w model.HVWay, networkID int64) (int64, error) {
	var id int64
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		section := w.SectionID
		if section == 0 {
			var err error
			if section, err = s.defaultSection(ctx, tx, networkID); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(ctx, `INSERT INTO hv_ways (section_id, name, rating_a, dest_board_id, dest_label, dest_detail, notes, position)
			VALUES ($1,$2,$3,$4,$5,$6,$7,
			(SELECT coalesce(max(position),-1)+1 FROM hv_ways WHERE section_id = $1)) RETURNING id`,
			section, w.Name, w.RatingA, w.DestBoardID, w.DestLabel, w.DestDetail, w.Notes).Scan(&id); err != nil {
			return err
		}
		// A way is drawn from its switchgear down, so a new one starts with one.
		if _, err := tx.Exec(ctx, `INSERT INTO hv_devices (way_id, kind, name, rating_a, position)
			VALUES ($1, 'switchgear', $2, $3, 0)`, id, w.Name, w.RatingA); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "create", "hv_way", id, "Added way "+w.Name+" feeding "+w.Destination())
	})
	return id, err
}

func (s *Store) UpdateHVWay(ctx context.Context, role string, w model.HVWay) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE hv_ways SET section_id = coalesce(nullif($2::bigint, 0), section_id),
			name = $3, rating_a = $4, dest_board_id = $5, dest_label = $6, dest_detail = $7, notes = $8,
			updated_at = now() WHERE id = $1`,
			w.ID, w.SectionID, w.Name, w.RatingA, w.DestBoardID, w.DestLabel, w.DestDetail, w.Notes)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return s.audit(ctx, tx, role, "update", "hv_way", w.ID, "Updated way "+w.Name)
	})
}

func (s *Store) DeleteHVWay(ctx context.Context, role string, id int64) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var name string
		if err := tx.QueryRow(ctx, `DELETE FROM hv_ways WHERE id = $1 RETURNING name`, id).Scan(&name); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		return s.audit(ctx, tx, role, "delete", "hv_way", id, "Deleted way "+name)
	})
}

// PlaceHVWay moves an outgoing way to a position along a bus section, which may
// be a different section from the one it taps now.
func (s *Store) PlaceHVWay(ctx context.Context, role string, id, sectionID int64, index int) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var networkID, from int64
		var name string
		if err := tx.QueryRow(ctx, `SELECT s.network_id, w.section_id, w.name FROM hv_ways w
			JOIN hv_sections s ON s.id = w.section_id WHERE w.id = $1`, id).
			Scan(&networkID, &from, &name); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		to := sectionID
		if to == 0 {
			to = from
		}
		var secName string
		if err := tx.QueryRow(ctx, `SELECT name FROM hv_sections WHERE id = $1 AND network_id = $2`,
			to, networkID).Scan(&secName); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return &UserError{"That bus section is not on this network."}
			}
			return err
		}

		run, err := wayOrder(ctx, tx, to, id)
		if err != nil {
			return err
		}
		for i, wid := range insertAt(run, id, index) {
			if _, err := tx.Exec(ctx, `UPDATE hv_ways SET section_id = $2, position = $3, updated_at = now()
				WHERE id = $1`, wid, to, i); err != nil {
				return err
			}
		}
		if to != from {
			left, err := wayOrder(ctx, tx, from, 0)
			if err != nil {
				return err
			}
			for i, wid := range left {
				if _, err := tx.Exec(ctx, `UPDATE hv_ways SET position = $2 WHERE id = $1`, wid, i); err != nil {
					return err
				}
			}
		}
		return s.audit(ctx, tx, role, "update", "hv_way", id, "Moved way "+name+" to "+secName)
	})
}

// wayOrder lists the ways on a bus section left to right, skipping one.
func wayOrder(ctx context.Context, tx pgx.Tx, sectionID, skip int64) ([]int64, error) {
	rows, err := tx.Query(ctx, `SELECT id FROM hv_ways WHERE section_id = $1 AND id <> $2
		ORDER BY position, name`, sectionID, skip)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Couplers ----------------------------------------------------------------

func (s *Store) CreateHVCoupler(ctx context.Context, role string, c model.HVCoupler) (int64, error) {
	var id int64
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO hv_couplers (network_id, name, left_section_id, right_section_id, closed, rating_a)
			VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
			c.NetworkID, c.Name, c.LeftSectionID, c.RightSectionID, c.Closed, c.RatingA).Scan(&id); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "create", "hv_coupler", id, "Added coupler "+c.Name)
	})
	return id, err
}

func (s *Store) UpdateHVCoupler(ctx context.Context, role string, c model.HVCoupler) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE hv_couplers SET name = $2, left_section_id = $3, right_section_id = $4,
			closed = $5, rating_a = $6, updated_at = now() WHERE id = $1`,
			c.ID, c.Name, c.LeftSectionID, c.RightSectionID, c.Closed, c.RatingA)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		state := "open"
		if c.Closed {
			state = "closed"
		}
		return s.audit(ctx, tx, role, "update", "hv_coupler", c.ID, "Set coupler "+c.Name+" "+state)
	})
}

func (s *Store) DeleteHVCoupler(ctx context.Context, role string, id int64) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var name string
		if err := tx.QueryRow(ctx, `DELETE FROM hv_couplers WHERE id = $1 RETURNING name`, id).Scan(&name); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		return s.audit(ctx, tx, role, "delete", "hv_coupler", id, "Deleted coupler "+name)
	})
}

// Devices ------------------------------------------------------------------

// devOwner is the conductor a device sits on: an outgoing way or an incoming
// feeder, never both.
type devOwner struct{ WayID, FeederID int64 }

func (o devOwner) column() string {
	if o.FeederID != 0 {
		return "feeder_id"
	}
	return "way_id"
}

func (o devOwner) id() int64 {
	if o.FeederID != 0 {
		return o.FeederID
	}
	return o.WayID
}

func (o devOwner) valid() bool {
	return (o.WayID != 0) != (o.FeederID != 0)
}

// renumberDevices makes the positions on one conductor dense and in order, so
// an insert or a move has somewhere unambiguous to land.
func renumberDevices(ctx context.Context, tx pgx.Tx, o devOwner) error {
	_, err := tx.Exec(ctx, `WITH ordered AS (
		SELECT id, row_number() OVER (ORDER BY position, id) - 1 AS rn
		FROM hv_devices WHERE `+o.column()+` = $1)
		UPDATE hv_devices d SET position = o.rn FROM ordered o WHERE d.id = o.id`, o.id())
	return err
}

// DevTarget names the conductor a device is being placed on. Exactly one of
// the two is expected; both zero means "leave it where it is".
func DevTarget(wayID, feederID int64) devOwner {
	return devOwner{WayID: wayID, FeederID: feederID}
}

// deviceOwner reads which conductor a device is on.
func deviceOwner(ctx context.Context, tx pgx.Tx, id int64) (devOwner, string, string, error) {
	var o devOwner
	var kind, name string
	err := tx.QueryRow(ctx, `SELECT coalesce(way_id, 0), coalesce(feeder_id, 0), kind::text, name
		FROM hv_devices WHERE id = $1`, id).Scan(&o.WayID, &o.FeederID, &kind, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, "", "", ErrNotFound
	}
	return o, kind, name, err
}

// CreateHVDevice fits a device on a conductor. With afterID set it lands
// immediately below that device, which is what "add another one after this"
// means on a drawing; otherwise it goes on the end.
func (s *Store) CreateHVDevice(ctx context.Context, role string, d model.HVDevice, afterID int64) (int64, error) {
	owner := devOwner{WayID: d.WayID, FeederID: d.FeederID}
	if !owner.valid() {
		return 0, &UserError{"A device sits on either a way or a feeder."}
	}
	var id int64
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if err := renumberDevices(ctx, tx, owner); err != nil {
			return err
		}
		pos := -1
		if afterID > 0 {
			at, atOwner, err := devicePosition(ctx, tx, afterID)
			if err != nil {
				return err
			}
			if atOwner != owner {
				return &UserError{"That device is on a different conductor."}
			}
			pos = at + 1
			if _, err := tx.Exec(ctx, `UPDATE hv_devices SET position = position + 1
				WHERE `+owner.column()+` = $1 AND position >= $2`, owner.id(), pos); err != nil {
				return err
			}
		} else {
			if err := tx.QueryRow(ctx, `SELECT coalesce(max(position),-1)+1 FROM hv_devices
				WHERE `+owner.column()+` = $1`, owner.id()).Scan(&pos); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(ctx, `INSERT INTO hv_devices (way_id, feeder_id, kind, name, rating_a, kva, ratio, notes, position)
			VALUES (nullif($1::bigint,0), nullif($2::bigint,0), $3::hv_device_kind, $4, $5, $6, $7, $8, $9) RETURNING id`,
			d.WayID, d.FeederID, d.Kind, d.Name, d.RatingA, d.KVA, d.Ratio, d.Notes, pos).Scan(&id); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "create", "hv_device", id,
			fmt.Sprintf("Fitted %s %s", d.Kind, d.Name))
	})
	return id, err
}

// devicePosition reads where a device sits and what it sits on.
func devicePosition(ctx context.Context, tx pgx.Tx, id int64) (int, devOwner, error) {
	var pos int
	var o devOwner
	err := tx.QueryRow(ctx, `SELECT position, coalesce(way_id, 0), coalesce(feeder_id, 0)
		FROM hv_devices WHERE id = $1`, id).Scan(&pos, &o.WayID, &o.FeederID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, o, ErrNotFound
	}
	return pos, o, err
}

func (s *Store) UpdateHVDevice(ctx context.Context, role string, d model.HVDevice) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE hv_devices SET kind = $2::hv_device_kind, name = $3, rating_a = $4,
			kva = $5, ratio = $6, notes = $7, updated_at = now() WHERE id = $1`,
			d.ID, d.Kind, d.Name, d.RatingA, d.KVA, d.Ratio, d.Notes)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return s.audit(ctx, tx, role, "update", "hv_device", d.ID,
			fmt.Sprintf("Updated %s %s", d.Kind, d.Name))
	})
}

func (s *Store) DeleteHVDevice(ctx context.Context, role string, id int64) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		owner, kind, name, err := deviceOwner(ctx, tx, id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM hv_devices WHERE id = $1`, id); err != nil {
			return err
		}
		if err := renumberDevices(ctx, tx, owner); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "delete", "hv_device", id,
			fmt.Sprintf("Removed %s %s", kind, name))
	})
}

// MoveHVDevice shifts a device one place up or down its conductor.
func (s *Store) MoveHVDevice(ctx context.Context, role string, id int64, delta int) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		owner, kind, _, err := deviceOwner(ctx, tx, id)
		if err != nil {
			return err
		}
		if err := renumberDevices(ctx, tx, owner); err != nil {
			return err
		}
		pos, _, err := devicePosition(ctx, tx, id)
		if err != nil {
			return err
		}
		target := pos + delta
		var otherID int64
		err = tx.QueryRow(ctx, `SELECT id FROM hv_devices WHERE `+owner.column()+` = $1 AND position = $2`,
			owner.id(), target).Scan(&otherID)
		if errors.Is(err, pgx.ErrNoRows) {
			return &UserError{"That device is already at the end."}
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE hv_devices SET position = $2, updated_at = now() WHERE id = $1`, otherID, pos); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE hv_devices SET position = $2, updated_at = now() WHERE id = $1`, id, target); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "update", "hv_device", id, "Moved a "+kind+" along its conductor")
	})
}

// PlaceHVDevice moves a device to a position on a conductor, which may be a
// different one from the one it is on. afterID names the device it should sit
// below; zero puts it at the head. This is what a drag onto the diagram
// resolves to, where a move one place at a time cannot express it.
func (s *Store) PlaceHVDevice(ctx context.Context, role string, id int64, target devOwner, afterID int64) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		from, kind, name, err := deviceOwner(ctx, tx, id)
		if err != nil {
			return err
		}
		if !target.valid() {
			target = from
		}
		if afterID == id {
			return nil // dropped back where it came from
		}
		var exists bool
		table := "hv_ways"
		if target.FeederID != 0 {
			table = "hv_feeders"
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM `+table+` WHERE id = $1)`, target.id()).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return &UserError{"That conductor no longer exists."}
		}

		// Take it out of its current chain first, so positions either side
		// close up whether or not the conductor is changing.
		if _, err := tx.Exec(ctx, `UPDATE hv_devices SET position = -1 WHERE id = $1`, id); err != nil {
			return err
		}
		if err := renumberDevices(ctx, tx, from); err != nil {
			return err
		}

		pos := 0
		if afterID > 0 {
			at, atOwner, err := devicePosition(ctx, tx, afterID)
			if err != nil {
				return err
			}
			if atOwner != target {
				return &UserError{"Drop it onto a place on the same conductor."}
			}
			pos = at + 1
		}
		if _, err := tx.Exec(ctx, `UPDATE hv_devices SET position = position + 1
			WHERE `+target.column()+` = $1 AND position >= $2 AND id <> $3`, target.id(), pos, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE hv_devices SET way_id = nullif($2::bigint,0), feeder_id = nullif($3::bigint,0),
			position = $4, updated_at = now() WHERE id = $1`, id, target.WayID, target.FeederID, pos); err != nil {
			return err
		}
		if err := renumberDevices(ctx, tx, target); err != nil {
			return err
		}
		if from != target {
			if err := renumberDevices(ctx, tx, from); err != nil {
				return err
			}
		}
		what := kind
		if name != "" {
			what += " " + name
		}
		return s.audit(ctx, tx, role, "update", "hv_device", id, "Moved "+what+" to a new place on the diagram")
	})
}
