package db

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"

	"github.com/superforcesuytr-droid/power-distribution-system/internal/model"
)

// HVNetwork loads the whole high-voltage overview: feeders in order, the ways
// under each, and the couplers between them.
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
	n.Feeders = []model.HVFeeder{}
	n.Couplers = []model.HVCoupler{}

	rows, err := pool.Query(ctx, `SELECT id, network_id, name, switchgear, voltage, source, rating_a, position, created_at, updated_at
		FROM hv_feeders WHERE network_id = $1 ORDER BY position, name`, n.ID)
	if err != nil {
		return nil, err
	}
	index := map[int64]int{}
	for rows.Next() {
		var f model.HVFeeder
		if err := rows.Scan(&f.ID, &f.NetworkID, &f.Name, &f.Switchgear, &f.Voltage, &f.Source, &f.RatingA, &f.Position,
			&f.CreatedAt, &f.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		f.Ways = []model.HVWay{}
		f.Devices = []model.HVDevice{}
		index[f.ID] = len(n.Feeders)
		n.Feeders = append(n.Feeders, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Ways, with the destination board resolved so the diagram can link to it.
	wayAt := map[int64][2]int{}
	wrows, err := pool.Query(ctx, `SELECT w.id, w.feeder_id, w.name, w.rating_a,
		w.dest_board_id, w.dest_label, w.dest_detail, w.notes, w.position, w.created_at, w.updated_at,
		coalesce(bo.code, ''), coalesce(b.name, '')
		FROM hv_ways w
		JOIN hv_feeders f ON f.id = w.feeder_id
		LEFT JOIN boards bo ON bo.id = w.dest_board_id
		LEFT JOIN buildings b ON b.id = bo.building_id
		WHERE f.network_id = $1 ORDER BY w.position, w.name`, n.ID)
	if err != nil {
		return nil, err
	}
	for wrows.Next() {
		var w model.HVWay
		if err := wrows.Scan(&w.ID, &w.FeederID, &w.Name, &w.RatingA,
			&w.DestBoardID, &w.DestLabel, &w.DestDetail, &w.Notes, &w.Position, &w.CreatedAt, &w.UpdatedAt,
			&w.DestBoardCode, &w.DestBuildingName); err != nil {
			wrows.Close()
			return nil, err
		}
		w.Devices = []model.HVDevice{}
		if i, ok := index[w.FeederID]; ok {
			n.Feeders[i].Ways = append(n.Feeders[i].Ways, w)
			wayAt[w.ID] = [2]int{i, len(n.Feeders[i].Ways) - 1}
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
		WHERE coalesce(w.feeder_id, d.feeder_id) IN (SELECT id FROM hv_feeders WHERE network_id = $1)
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
			if i, ok := index[d.FeederID]; ok {
				n.Feeders[i].Devices = append(n.Feeders[i].Devices, d)
			}
			continue
		}
		if p, ok := wayAt[d.WayID]; ok {
			way := &n.Feeders[p[0]].Ways[p[1]]
			way.Devices = append(way.Devices, d)
		}
	}
	drows.Close()
	if err := drows.Err(); err != nil {
		return nil, err
	}

	crows, err := pool.Query(ctx, `SELECT id, network_id, name, left_id, right_id, closed, rating_a, created_at, updated_at
		FROM hv_couplers WHERE network_id = $1 ORDER BY id`, n.ID)
	if err != nil {
		return nil, err
	}
	for crows.Next() {
		var c model.HVCoupler
		if err := crows.Scan(&c.ID, &c.NetworkID, &c.Name, &c.LeftID, &c.RightID, &c.Closed, &c.RatingA,
			&c.CreatedAt, &c.UpdatedAt); err != nil {
			crows.Close()
			return nil, err
		}
		n.Couplers = append(n.Couplers, c)
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return nil, err
	}

	// Same reading order as everywhere else: F2 before F10, W2 before W10.
	sort.SliceStable(n.Feeders, func(i, j int) bool {
		if n.Feeders[i].Position != n.Feeders[j].Position {
			return n.Feeders[i].Position < n.Feeders[j].Position
		}
		return model.NaturalLess(n.Feeders[i].Name, n.Feeders[j].Name)
	})
	for i := range n.Feeders {
		fd := n.Feeders[i].Devices
		sort.SliceStable(fd, func(a, b int) bool { return fd[a].Position < fd[b].Position })
		ways := n.Feeders[i].Ways
		sort.SliceStable(ways, func(a, b int) bool { return model.NaturalLess(ways[a].Name, ways[b].Name) })
		for j := range ways {
			devs := ways[j].Devices
			sort.SliceStable(devs, func(a, b int) bool { return devs[a].Position < devs[b].Position })
		}
	}
	n.Compute()
	return &n, nil
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

// Feeders -----------------------------------------------------------------

func (s *Store) CreateHVFeeder(ctx context.Context, role string, f model.HVFeeder) (int64, error) {
	var id int64
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO hv_feeders (network_id, name, switchgear, voltage, source, rating_a, position)
			VALUES ($1,$2,$3,$4,$5,$6,(SELECT coalesce(max(position),-1)+1 FROM hv_feeders WHERE network_id = $1))
			RETURNING id`, f.NetworkID, f.Name, f.Switchgear, f.Voltage, f.Source, f.RatingA).Scan(&id); err != nil {
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
			rating_a = $6, updated_at = now() WHERE id = $1`, f.ID, f.Name, f.Switchgear, f.Voltage, f.Source, f.RatingA)
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
		return s.audit(ctx, tx, role, "delete", "hv_feeder", id, "Deleted feeder "+name+", its ways and any couplers on it")
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
		// Renumber first so positions are dense, then swap with the neighbour.
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

// Ways --------------------------------------------------------------------

func (s *Store) CreateHVWay(ctx context.Context, role string, w model.HVWay) (int64, error) {
	var id int64
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO hv_ways (feeder_id, name, rating_a, dest_board_id, dest_label, dest_detail, notes, position)
			VALUES ($1,$2,$3,$4,$5,$6,$7,
			(SELECT coalesce(max(position),-1)+1 FROM hv_ways WHERE feeder_id = $1)) RETURNING id`,
			w.FeederID, w.Name, w.RatingA, w.DestBoardID, w.DestLabel, w.DestDetail, w.Notes).Scan(&id); err != nil {
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
		tag, err := tx.Exec(ctx, `UPDATE hv_ways SET feeder_id = coalesce(nullif($2::bigint, 0), feeder_id),
			name = $3, rating_a = $4, dest_board_id = $5, dest_label = $6, dest_detail = $7, notes = $8,
			updated_at = now() WHERE id = $1`,
			w.ID, w.FeederID, w.Name, w.RatingA, w.DestBoardID, w.DestLabel, w.DestDetail, w.Notes)
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

// Couplers ----------------------------------------------------------------

func (s *Store) CreateHVCoupler(ctx context.Context, role string, c model.HVCoupler) (int64, error) {
	var id int64
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `INSERT INTO hv_couplers (network_id, name, left_id, right_id, closed, rating_a)
			VALUES ($1,$2,$3,$4,$5,$6) RETURNING id`,
			c.NetworkID, c.Name, c.LeftID, c.RightID, c.Closed, c.RatingA).Scan(&id); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "create", "hv_coupler", id, "Added coupler "+c.Name)
	})
	return id, err
}

func (s *Store) UpdateHVCoupler(ctx context.Context, role string, c model.HVCoupler) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE hv_couplers SET name = $2, left_id = $3, right_id = $4, closed = $5,
			rating_a = $6, updated_at = now() WHERE id = $1`, c.ID, c.Name, c.LeftID, c.RightID, c.Closed, c.RatingA)
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
