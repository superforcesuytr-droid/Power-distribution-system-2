package db

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/superforcesuytr-droid/power-distribution-system/internal/model"
)

// HVNetworks lists the drawings, high tension first, without loading what is
// on any of them. It is what the picker above the diagram is built from.
func (s *Store) HVNetworks(ctx context.Context) ([]model.HVNetwork, error) {
	pool, err := s.getPool()
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, `SELECT id, name, voltage, tier, position
		FROM hv_networks ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.HVNetwork{}
	for rows.Next() {
		var n model.HVNetwork
		if err := rows.Scan(&n.ID, &n.Name, &n.Voltage, &n.Tier, &n.Position); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// HVSwitchboardList names every switchboard on the site with the drawing it is
// on, so a way can be pointed at one on another drawing.
func (s *Store) HVSwitchboardList(ctx context.Context) ([]model.HVSwitchboard, error) {
	pool, err := s.getPool()
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, `SELECT b.id, b.network_id, b.name, b.voltage, b.position,
		n.name, n.tier, n.position
		FROM hv_switchboards b JOIN hv_networks n ON n.id = b.network_id
		ORDER BY n.position, n.id, b.position, b.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.HVSwitchboard{}
	for rows.Next() {
		var b model.HVSwitchboard
		if err := rows.Scan(&b.ID, &b.NetworkID, &b.Name, &b.Voltage, &b.Position,
			&b.NetworkName, &b.NetworkTier, &b.NetworkPosition); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// HVCrossRef is one designation on a drawing that another drawing may name
// too: the same circuit is a way where it leaves one board and an incomer
// where it lands on the next.
type HVCrossRef struct {
	ID          int64  `json:"id"`
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	NetworkID   int64  `json:"network_id"`
	NetworkName string `json:"network_name"`
	BoardName   string `json:"board_name"`
}

// HVCrossRefs names every way and incomer on the site with the drawing and the
// switchboard it is on, so one drawing can point at the same designation on
// another. A designation is sometimes written on the switchgear at the bar
// rather than on the way itself, so those names count too and point at the
// column carrying them.
func (s *Store) HVCrossRefs(ctx context.Context) ([]HVCrossRef, error) {
	pool, err := s.getPool()
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, `
		SELECT w.id, 'way', w.name, n.id, n.name, b.name
		FROM hv_ways w
		JOIN hv_sections sec ON sec.id = w.section_id
		JOIN hv_switchboards b ON b.id = sec.switchboard_id
		JOIN hv_networks n ON n.id = sec.network_id
		WHERE w.name <> ''
		UNION ALL
		SELECT f.id, 'feeder', f.name, n.id, n.name, b.name
		FROM hv_feeders f
		JOIN hv_sections sec ON sec.id = f.section_id
		JOIN hv_switchboards b ON b.id = sec.switchboard_id
		JOIN hv_networks n ON n.id = f.network_id
		WHERE f.name <> ''
		UNION ALL
		SELECT w.id, 'way', d.name, n.id, n.name, b.name
		FROM hv_devices d
		JOIN hv_ways w ON w.id = d.way_id
		JOIN hv_sections sec ON sec.id = w.section_id
		JOIN hv_switchboards b ON b.id = sec.switchboard_id
		JOIN hv_networks n ON n.id = sec.network_id
		WHERE d.position = 0 AND d.name <> '' AND d.name <> w.name
		  AND d.kind IN ('switchgear', 'isolator')
		UNION ALL
		SELECT f.id, 'feeder', d.name, n.id, n.name, b.name
		FROM hv_devices d
		JOIN hv_feeders f ON f.id = d.feeder_id
		JOIN hv_sections sec ON sec.id = f.section_id
		JOIN hv_switchboards b ON b.id = sec.switchboard_id
		JOIN hv_networks n ON n.id = f.network_id
		WHERE d.position = 0 AND d.name <> '' AND d.name <> f.name
		  AND d.kind IN ('switchgear', 'isolator')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HVCrossRef{}
	for rows.Next() {
		var r HVCrossRef
		if err := rows.Scan(&r.ID, &r.Kind, &r.Name, &r.NetworkID, &r.NetworkName, &r.BoardName); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// NetworkOfSection says which drawing a bus section belongs to, so a feeder or
// a way added to it lands on the same one.
func (s *Store) NetworkOfSection(ctx context.Context, sectionID int64) (int64, error) {
	pool, err := s.getPool()
	if err != nil {
		return 0, err
	}
	var id int64
	err = pool.QueryRow(ctx, `SELECT network_id FROM hv_sections WHERE id = $1`, sectionID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// NetworkOfSwitchboard says which drawing a switchboard is on.
func (s *Store) NetworkOfSwitchboard(ctx context.Context, boardID int64) (int64, error) {
	pool, err := s.getPool()
	if err != nil {
		return 0, err
	}
	var id int64
	err = pool.QueryRow(ctx, `SELECT network_id FROM hv_switchboards WHERE id = $1`, boardID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	return id, err
}

// HVNetwork loads one whole drawing: the switchboards top to bottom, the bus
// sections each is split into, the feeders backing each section and the ways
// tapping it, and the couplers between sections. An id of zero loads the first
// drawing there is.
func (s *Store) HVNetwork(ctx context.Context, id int64) (*model.HVNetwork, error) {
	pool, err := s.getPool()
	if err != nil {
		return nil, err
	}
	var n model.HVNetwork
	err = pool.QueryRow(ctx, `SELECT id, name, voltage, tier, position FROM hv_networks
		WHERE $1 = 0 OR id = $1 ORDER BY position, id LIMIT 1`, id).
		Scan(&n.ID, &n.Name, &n.Voltage, &n.Tier, &n.Position)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	n.Switchboards = []model.HVSwitchboard{}

	// Everything is read flat and assembled at the end, so a device only ever
	// has to find its own way or feeder rather than an index into a tree.
	boardAt := map[int64]int{}
	brows, err := pool.Query(ctx, `SELECT id, network_id, name, voltage, phases, frequency,
		current_a, fault_ka, position, created_at, updated_at
		FROM hv_switchboards WHERE network_id = $1 ORDER BY position, id`, n.ID)
	if err != nil {
		return nil, err
	}
	for brows.Next() {
		var b model.HVSwitchboard
		if err := brows.Scan(&b.ID, &b.NetworkID, &b.Name, &b.Voltage, &b.Phases, &b.Frequency,
			&b.CurrentA, &b.FaultKA, &b.Position, &b.CreatedAt, &b.UpdatedAt); err != nil {
			brows.Close()
			return nil, err
		}
		b.Sections = []model.HVSection{}
		b.Couplers = []model.HVCoupler{}
		boardAt[b.ID] = len(n.Switchboards)
		n.Switchboards = append(n.Switchboards, b)
	}
	brows.Close()
	if err := brows.Err(); err != nil {
		return nil, err
	}

	var sections []model.HVSection
	secAt := map[int64]int{}
	srows, err := pool.Query(ctx, `SELECT id, network_id, switchboard_id, name, position FROM hv_sections
		WHERE network_id = $1 ORDER BY position, id`, n.ID)
	if err != nil {
		return nil, err
	}
	for srows.Next() {
		var sec model.HVSection
		if err := srows.Scan(&sec.ID, &sec.NetworkID, &sec.SwitchboardID, &sec.Name, &sec.Position); err != nil {
			srows.Close()
			return nil, err
		}
		sec.Feeders = []model.HVFeeder{}
		sec.Ways = []model.HVWay{}
		secAt[sec.ID] = len(sections)
		sections = append(sections, sec)
	}
	srows.Close()
	if err := srows.Err(); err != nil {
		return nil, err
	}

	var feeders []model.HVFeeder
	feederAt := map[int64]int{}
	rows, err := pool.Query(ctx, `SELECT f.id, f.network_id, coalesce(f.section_id, 0), f.name, f.switchgear, f.kind, f.side,
		f.voltage, f.source, f.rating_a, f.position, f.created_at, f.updated_at, f.offset_x,
		f.source_way_id, coalesce(sw.name, ''), coalesce(sb.name, '')
		FROM hv_feeders f
		LEFT JOIN hv_ways sw ON sw.id = f.source_way_id
		LEFT JOIN hv_sections ssec ON ssec.id = sw.section_id
		LEFT JOIN hv_switchboards sb ON sb.id = ssec.switchboard_id
		WHERE f.network_id = $1 ORDER BY f.position, f.name`, n.ID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var f model.HVFeeder
		if err := rows.Scan(&f.ID, &f.NetworkID, &f.SectionID, &f.Name, &f.Switchgear, &f.Kind, &f.Side,
			&f.Voltage, &f.Source, &f.RatingA, &f.Position, &f.CreatedAt, &f.UpdatedAt, &f.OffsetX,
			&f.SourceWayID, &f.SourceWayName, &f.SourceBoardName); err != nil {
			rows.Close()
			return nil, err
		}
		f.Devices = []model.HVDevice{}
		feederAt[f.ID] = len(feeders)
		feeders = append(feeders, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Ways, with the destination resolved so the diagram can link to it.
	var ways []model.HVWay
	wayAt := map[int64]int{}
	wrows, err := pool.Query(ctx, `SELECT w.id, coalesce(w.section_id, 0), w.name, w.rating_a, w.side,
		w.dest_board_id, w.dest_switchboard_id, w.dest_label, w.dest_detail, w.notes, w.position,
		w.created_at, w.updated_at, w.offset_x,
		coalesce(bo.code, ''), coalesce(b.name, ''), coalesce(sb.name, ''),
		coalesce(sb.network_id, 0), coalesce(sbn.name, '')
		FROM hv_ways w
		JOIN hv_sections sec ON sec.id = w.section_id
		LEFT JOIN boards bo ON bo.id = w.dest_board_id
		LEFT JOIN buildings b ON b.id = bo.building_id
		LEFT JOIN hv_switchboards sb ON sb.id = w.dest_switchboard_id
		LEFT JOIN hv_networks sbn ON sbn.id = sb.network_id
		WHERE sec.network_id = $1 ORDER BY w.position, w.name`, n.ID)
	if err != nil {
		return nil, err
	}
	for wrows.Next() {
		var w model.HVWay
		if err := wrows.Scan(&w.ID, &w.SectionID, &w.Name, &w.RatingA, &w.Side,
			&w.DestBoardID, &w.DestSwitchboardID, &w.DestLabel, &w.DestDetail, &w.Notes, &w.Position,
			&w.CreatedAt, &w.UpdatedAt, &w.OffsetX,
			&w.DestBoardCode, &w.DestBuildingName, &w.DestSwitchboardName,
			&w.DestNetworkID, &w.DestNetworkName); err != nil {
			wrows.Close()
			return nil, err
		}
		w.Devices = []model.HVDevice{}
		wayAt[w.ID] = len(ways)
		ways = append(ways, w)
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
			if i, ok := feederAt[d.FeederID]; ok {
				feeders[i].Devices = append(feeders[i].Devices, d)
			}
			continue
		}
		if i, ok := wayAt[d.WayID]; ok {
			ways[i].Devices = append(ways[i].Devices, d)
		}
	}
	drows.Close()
	if err := drows.Err(); err != nil {
		return nil, err
	}

	var couplers []model.HVCoupler
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
		couplers = append(couplers, c)
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return nil, err
	}

	// Assemble. Feeders and ways read left to right in the order they are
	// arranged on the bar, falling back to the natural name order used
	// everywhere else; a device reads down its own conductor.
	for i := range feeders {
		d := feeders[i].Devices
		sort.SliceStable(d, func(a, b int) bool { return d[a].Position < d[b].Position })
		if j, ok := secAt[feeders[i].SectionID]; ok {
			sections[j].Feeders = append(sections[j].Feeders, feeders[i])
		}
	}
	for i := range ways {
		d := ways[i].Devices
		sort.SliceStable(d, func(a, b int) bool { return d[a].Position < d[b].Position })
		if j, ok := secAt[ways[i].SectionID]; ok {
			sections[j].Ways = append(sections[j].Ways, ways[i])
		}
	}
	for i := range sections {
		sec := &sections[i]
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
		if j, ok := boardAt[sec.SwitchboardID]; ok {
			n.Switchboards[j].Sections = append(n.Switchboards[j].Sections, *sec)
		}
	}
	// A coupler belongs to the switchboard of the sections it ties.
	for _, c := range couplers {
		i, ok := secAt[c.LeftSectionID]
		if !ok {
			continue
		}
		j, ok := boardAt[sections[i].SwitchboardID]
		if !ok {
			continue
		}
		c.SwitchboardID = sections[i].SwitchboardID
		n.Switchboards[j].Couplers = append(n.Switchboards[j].Couplers, c)
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
		board, berr := s.defaultSwitchboard(ctx, tx, networkID)
		if berr != nil {
			return 0, berr
		}
		err = tx.QueryRow(ctx, `INSERT INTO hv_sections (network_id, switchboard_id, name, position)
			VALUES ($1, $2, 'Section A', 0) RETURNING id`, networkID, board).Scan(&id)
	}
	return id, err
}

// defaultSwitchboard returns the switchboard a new section belongs on when the
// caller does not name one, creating the first board if there is none.
func (s *Store) defaultSwitchboard(ctx context.Context, tx pgx.Tx, networkID int64) (int64, error) {
	var id int64
	err := tx.QueryRow(ctx, `SELECT id FROM hv_switchboards WHERE network_id = $1 ORDER BY position, id LIMIT 1`,
		networkID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `INSERT INTO hv_switchboards (network_id, name, voltage, phases, frequency, position)
			SELECT $1, coalesce(nullif(voltage, ''), 'HV') || ' Switchboard', voltage, '3p', '50Hz', 0
			FROM hv_networks WHERE id = $1 RETURNING id`, networkID).Scan(&id)
	}
	return id, err
}

// UpdateHVNetwork renames the overview.
func (s *Store) UpdateHVNetwork(ctx context.Context, role string, n model.HVNetwork) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE hv_networks SET name = $2, voltage = $3, tier = $4,
			updated_at = now() WHERE id = $1`, n.ID, n.Name, n.Voltage, n.Tier)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return s.audit(ctx, tx, role, "update", "hv_network", n.ID, "Renamed a drawing to "+n.Name)
	})
}

// CreateHVNetwork adds a drawing: the low-tension side of a site, or any other
// tension worth a picture of its own.
func (s *Store) CreateHVNetwork(ctx context.Context, role string, n model.HVNetwork) (int64, error) {
	var id int64
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO hv_networks (name, voltage, tier, position)
			VALUES ($1, $2, $3, (SELECT coalesce(max(position),-1)+1 FROM hv_networks)) RETURNING id`,
			n.Name, n.Voltage, n.Tier).Scan(&id); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "create", "hv_network", id, "Added the drawing "+n.Name)
	})
	return id, err
}

// DeleteHVNetwork removes a drawing, which is refused while anything is still
// drawn on it so a whole site cannot go in one click.
func (s *Store) DeleteHVNetwork(ctx context.Context, role string, id int64) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var name string
		var boards, left int
		if err := tx.QueryRow(ctx, `SELECT n.name,
			(SELECT count(*) FROM hv_switchboards WHERE network_id = n.id),
			(SELECT count(*) FROM hv_networks)
			FROM hv_networks n WHERE n.id = $1`, id).Scan(&name, &boards, &left); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if left < 2 {
			return &UserError{"This is the only drawing there is."}
		}
		if boards > 0 {
			return &UserError{"Delete the switchboards on " + name + " first, so nothing is thrown away by mistake."}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM hv_networks WHERE id = $1`, id); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "delete", "hv_network", id, "Deleted the drawing "+name)
	})
}

// Switchboards -------------------------------------------------------------

func (s *Store) CreateHVSwitchboard(ctx context.Context, role string, b model.HVSwitchboard) (int64, error) {
	var id int64
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO hv_switchboards
			(network_id, name, voltage, phases, frequency, current_a, fault_ka, position)
			VALUES ($1,$2,$3,$4,$5,$6,$7,
			(SELECT coalesce(max(position),-1)+1 FROM hv_switchboards WHERE network_id = $1)) RETURNING id`,
			b.NetworkID, b.Name, b.Voltage, b.Phases, b.Frequency, b.CurrentA, b.FaultKA).Scan(&id); err != nil {
			return err
		}
		// A switchboard is nothing without a bus to hang things off, so it
		// starts with one section the way the first board did.
		if _, err := tx.Exec(ctx, `INSERT INTO hv_sections (network_id, switchboard_id, name, position)
			VALUES ($1, $2, 'Section A', (SELECT coalesce(max(position),-1)+1 FROM hv_sections WHERE network_id = $1))`,
			b.NetworkID, id); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "create", "hv_switchboard", id, "Added switchboard "+b.Name)
	})
	return id, err
}

func (s *Store) UpdateHVSwitchboard(ctx context.Context, role string, b model.HVSwitchboard) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE hv_switchboards SET name = $2, voltage = $3, phases = $4,
			frequency = $5, current_a = $6, fault_ka = $7, updated_at = now() WHERE id = $1`,
			b.ID, b.Name, b.Voltage, b.Phases, b.Frequency, b.CurrentA, b.FaultKA)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return s.audit(ctx, tx, role, "update", "hv_switchboard", b.ID, "Updated switchboard "+b.Name)
	})
}

func (s *Store) DeleteHVSwitchboard(ctx context.Context, role string, id int64) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var name string
		var sections int
		if err := tx.QueryRow(ctx, `SELECT b.name, (SELECT count(*) FROM hv_sections WHERE switchboard_id = b.id)
			FROM hv_switchboards b WHERE b.id = $1`, id).Scan(&name, &sections); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if sections > 0 {
			return &UserError{"Delete the bus sections on " + name + " first, so nothing is thrown away by mistake."}
		}
		if _, err := tx.Exec(ctx, `DELETE FROM hv_switchboards WHERE id = $1`, id); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "delete", "hv_switchboard", id, "Deleted switchboard "+name)
	})
}

// MoveHVSwitchboard shifts a switchboard one place up or down the drawing.
func (s *Store) MoveHVSwitchboard(ctx context.Context, role string, id int64, delta int) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var networkID int64
		var name string
		if err := tx.QueryRow(ctx, `SELECT network_id, name FROM hv_switchboards WHERE id = $1`, id).
			Scan(&networkID, &name); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if _, err := tx.Exec(ctx, `WITH ordered AS (
			SELECT id, row_number() OVER (ORDER BY position, id) - 1 AS rn
			FROM hv_switchboards WHERE network_id = $1)
			UPDATE hv_switchboards b SET position = o.rn FROM ordered o WHERE b.id = o.id`, networkID); err != nil {
			return err
		}
		var pos int
		if err := tx.QueryRow(ctx, `SELECT position FROM hv_switchboards WHERE id = $1`, id).Scan(&pos); err != nil {
			return err
		}
		var otherID int64
		err := tx.QueryRow(ctx, `SELECT id FROM hv_switchboards WHERE network_id = $1 AND position = $2`,
			networkID, pos+delta).Scan(&otherID)
		if errors.Is(err, pgx.ErrNoRows) {
			return &UserError{"That switchboard is already at the end."}
		}
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE hv_switchboards SET position = $2, updated_at = now() WHERE id = $1`,
			otherID, pos); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE hv_switchboards SET position = $2, updated_at = now() WHERE id = $1`,
			id, pos+delta); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "update", "hv_switchboard", id, "Moved switchboard "+name)
	})
}

// Sections -----------------------------------------------------------------

func (s *Store) CreateHVSection(ctx context.Context, role string, networkID, switchboardID int64, name string) (int64, error) {
	var id int64
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		board := switchboardID
		if board == 0 {
			var err error
			if board, err = s.defaultSwitchboard(ctx, tx, networkID); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(ctx, `INSERT INTO hv_sections (network_id, switchboard_id, name, position)
			VALUES ($1, $2, $3, (SELECT coalesce(max(position),-1)+1 FROM hv_sections WHERE network_id = $1))
			RETURNING id`, networkID, board, name).Scan(&id); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "create", "hv_section", id, "Added bus section "+name)
	})
	return id, err
}

// SplitHVBusAfter divides a switchboard's bus when a coupler is asked for at
// the end of it. A coupler is what separates one section from the next, so
// putting one there means the bus gains a section on its far side. It reports
// whether it split anything: with a section already following, there is
// nothing to do.
func (s *Store) SplitHVBusAfter(ctx context.Context, role string, sectionID int64) (bool, error) {
	split := false
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		var networkID, boardID int64
		var pos int
		if err := tx.QueryRow(ctx, `SELECT network_id, switchboard_id, position FROM hv_sections WHERE id = $1`,
			sectionID).Scan(&networkID, &boardID, &pos); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		var after int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM hv_sections
			WHERE switchboard_id = $1 AND (position, id) > ($2, $3)`, boardID, pos, sectionID).Scan(&after); err != nil {
			return err
		}
		if after > 0 {
			return nil
		}
		var on int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM hv_sections WHERE switchboard_id = $1`, boardID).Scan(&on); err != nil {
			return err
		}
		name := "Section " + string(rune('A'+min(on, 25)))
		var id int64
		if err := tx.QueryRow(ctx, `INSERT INTO hv_sections (network_id, switchboard_id, name, position)
			VALUES ($1, $2, $3, (SELECT coalesce(max(position),-1)+1 FROM hv_sections WHERE network_id = $1))
			RETURNING id`, networkID, boardID, name).Scan(&id); err != nil {
			return err
		}
		split = true
		return s.audit(ctx, tx, role, "create", "hv_section", id, "Split the bus and added "+name)
	})
	return split, err
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

// CreateHVFeeder adds an incomer to a bus section. Arrangement says what it
// lands through: switchgear alone, as a high-tension board is drawn, or a
// transformer and then the switchgear, as a low-tension board is.
func (s *Store) CreateHVFeeder(ctx context.Context, role string, f model.HVFeeder, arrangement, transformer string) (int64, error) {
	var id int64
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		section := f.SectionID
		if section == 0 {
			var err error
			if section, err = s.defaultSection(ctx, tx, f.NetworkID); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(ctx, `INSERT INTO hv_feeders (network_id, section_id, name, switchgear, kind, side, voltage, source, rating_a, source_way_id, position)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,(SELECT coalesce(max(position),-1)+1 FROM hv_feeders WHERE network_id = $1))
			RETURNING id`, f.NetworkID, section, f.Name, f.Switchgear, f.Kind, f.Side, f.Voltage, f.Source, f.RatingA, f.SourceWayID).Scan(&id); err != nil {
			return err
		}
		// A feeder is drawn from its first symbol down. A low-tension board is
		// fed through a transformer first, so that goes on ahead of the
		// switchgear. The switchgear's designation is left as given: blank on a
		// low-tension board means it is named after the board itself.
		pos := 0
		if arrangement == "transformer" {
			if _, err := tx.Exec(ctx, `INSERT INTO hv_devices (feeder_id, kind, name, position)
				VALUES ($1, 'transformer', $2, 0)`, id, transformer); err != nil {
				return err
			}
			pos = 1
		}
		if _, err := tx.Exec(ctx, `INSERT INTO hv_devices (feeder_id, kind, name, rating_a, position)
			VALUES ($1, 'switchgear', $2, $3, $4)`, id, f.Switchgear, f.RatingA, pos); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "create", "hv_feeder", id, "Added feeder "+f.Name)
	})
	return id, err
}

func (s *Store) UpdateHVFeeder(ctx context.Context, role string, f model.HVFeeder) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE hv_feeders SET name = $2, switchgear = $3, kind = $4, side = $5,
			voltage = $6, source = $7, rating_a = $8, source_way_id = $10,
			section_id = coalesce(nullif($9::bigint, 0), section_id), updated_at = now()
			WHERE id = $1`, f.ID, f.Name, f.Switchgear, f.Kind, f.Side, f.Voltage, f.Source, f.RatingA, f.SectionID, f.SourceWayID)
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
func (s *Store) PlaceHVFeeder(ctx context.Context, role string, id, sectionID int64, index int, side string) error {
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

		if _, err := tx.Exec(ctx, `UPDATE hv_feeders SET side = $2 WHERE id = $1`, id, side); err != nil {
			return err
		}
		pos := 0
		for _, sid := range order {
			run := held[sid]
			// A section's incomers are stored left side first, then right, so an
			// index within a side is an index into that side's own run.
			left, right, err := splitBySide(ctx, tx, `hv_feeders`, run)
			if err != nil {
				return err
			}
			if sid == to {
				if side == model.SideRight {
					right = insertAt(right, id, index)
				} else {
					left = insertAt(left, id, index)
				}
			}
			run = append(left, right...)
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

// CreateHVWay adds a way to a bus section. HeadKind is what stands where it
// taps the bus - a switchgear X or an open switch - and Chiller, when named,
// puts the machine at the foot of it so the way runs from the switch straight
// into the load.
func (s *Store) CreateHVWay(ctx context.Context, role string, w model.HVWay, networkID int64, headKind, chiller string) (int64, error) {
	var id int64
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		section := w.SectionID
		if section == 0 {
			var err error
			if section, err = s.defaultSection(ctx, tx, networkID); err != nil {
				return err
			}
		}
		if err := tx.QueryRow(ctx, `INSERT INTO hv_ways (section_id, name, rating_a, side, dest_board_id, dest_switchboard_id,
			dest_label, dest_detail, notes, position)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,
			(SELECT coalesce(max(position),-1)+1 FROM hv_ways WHERE section_id = $1)) RETURNING id`,
			section, w.Name, w.RatingA, w.Side, w.DestBoardID, w.DestSwitchboardID, w.DestLabel, w.DestDetail, w.Notes).Scan(&id); err != nil {
			return err
		}
		// A way is drawn from where it taps the bus down, so a new one starts
		// with whatever stands at that tap. On a low-tension board nothing
		// does: the way simply leaves the bar.
		pos := 0
		if headKind != "none" {
			if !model.IsHead(headKind) {
				headKind = model.DeviceSwitchgear
			}
			if _, err := tx.Exec(ctx, `INSERT INTO hv_devices (way_id, kind, name, rating_a, position)
				VALUES ($1, $2::hv_device_kind, $3, $4, 0)`, id, headKind, w.Name, w.RatingA); err != nil {
				return err
			}
			pos = 1
		}
		if chiller != "" {
			if _, err := tx.Exec(ctx, `INSERT INTO hv_devices (way_id, kind, name, position)
				VALUES ($1, 'chiller', $2, $3)`, id, chiller, pos); err != nil {
				return err
			}
		}
		return s.audit(ctx, tx, role, "create", "hv_way", id, "Added way "+w.Name+" feeding "+w.Destination())
	})
	return id, err
}

func (s *Store) UpdateHVWay(ctx context.Context, role string, w model.HVWay) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE hv_ways SET section_id = coalesce(nullif($2::bigint, 0), section_id),
			name = $3, rating_a = $4, side = $5, dest_board_id = $6, dest_switchboard_id = $7, dest_label = $8,
			dest_detail = $9, notes = $10, updated_at = now() WHERE id = $1`,
			w.ID, w.SectionID, w.Name, w.RatingA, w.Side, w.DestBoardID, w.DestSwitchboardID, w.DestLabel,
			w.DestDetail, w.Notes)
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
// PlaceHVWayAt puts a way at a place of its own along a bus section, as a
// fraction of that section's width, rather than in the row of evenly spaced
// slots. A nil offset hands it back to the automatic spacing.
func (s *Store) PlaceHVWayAt(ctx context.Context, role string, id, sectionID int64, offset *float64) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var name string
		if err := tx.QueryRow(ctx, `SELECT name FROM hv_ways WHERE id = $1`, id).Scan(&name); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE hv_ways SET offset_x = $2,
			section_id = coalesce(nullif($3::bigint, 0), section_id), updated_at = now()
			WHERE id = $1`, id, offset, sectionID); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "update", "hv_way", id, "Placed way "+name+" on the busbar")
	})
}

// PlaceHVFeederAt does the same for an incomer.
func (s *Store) PlaceHVFeederAt(ctx context.Context, role string, id, sectionID int64, offset *float64) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var name string
		if err := tx.QueryRow(ctx, `SELECT name FROM hv_feeders WHERE id = $1`, id).Scan(&name); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE hv_feeders SET offset_x = $2,
			section_id = coalesce(nullif($3::bigint, 0), section_id), updated_at = now()
			WHERE id = $1`, id, offset, sectionID); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "update", "hv_feeder", id, "Placed feeder "+name+" on the busbar")
	})
}

func (s *Store) PlaceHVWay(ctx context.Context, role string, id, sectionID int64, index int, side string) error {
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

		if _, err := tx.Exec(ctx, `UPDATE hv_ways SET side = $2 WHERE id = $1`, id, side); err != nil {
			return err
		}
		// The ways on a section are stored left side first, then right, so an
		// index within a side is an index into that side's own run.
		run, err := wayOrder(ctx, tx, to, id)
		if err != nil {
			return err
		}
		left, right, err := splitBySide(ctx, tx, `hv_ways`, run)
		if err != nil {
			return err
		}
		if side == model.SideRight {
			right = insertAt(right, id, index)
		} else {
			left = insertAt(left, id, index)
		}
		for i, wid := range append(left, right...) {
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

// splitBySide divides a run of ids into the ones on the left of a bar and the
// ones on the right, keeping the order they came in. Anything with no side of
// its own belongs to the left, which is where a bar that is not split into
// sides puts everything.
func splitBySide(ctx context.Context, tx pgx.Tx, table string, ids []int64) ([]int64, []int64, error) {
	var left, right []int64
	for _, id := range ids {
		var side string
		if err := tx.QueryRow(ctx, `SELECT side FROM `+table+` WHERE id = $1`, id).Scan(&side); err != nil {
			return nil, nil, err
		}
		if side == model.SideRight {
			right = append(right, id)
		} else {
			left = append(left, id)
		}
	}
	return left, right, nil
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
