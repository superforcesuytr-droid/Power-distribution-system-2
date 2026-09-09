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

	rows, err := pool.Query(ctx, `SELECT id, network_id, name, voltage, source, rating_a, position, created_at, updated_at
		FROM hv_feeders WHERE network_id = $1 ORDER BY position, name`, n.ID)
	if err != nil {
		return nil, err
	}
	index := map[int64]int{}
	for rows.Next() {
		var f model.HVFeeder
		if err := rows.Scan(&f.ID, &f.NetworkID, &f.Name, &f.Voltage, &f.Source, &f.RatingA, &f.Position,
			&f.CreatedAt, &f.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		f.Ways = []model.HVWay{}
		index[f.ID] = len(n.Feeders)
		n.Feeders = append(n.Feeders, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Ways, with the destination board resolved so the diagram can link to it.
	wrows, err := pool.Query(ctx, `SELECT w.id, w.feeder_id, w.name, w.rating_a, w.protection::text, w.protection_note,
		w.has_transformer, w.transformer_name, w.transformer_kva, w.transformer_ratio,
		w.dest_board_id, w.dest_label, w.notes, w.position, w.created_at, w.updated_at,
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
		if err := wrows.Scan(&w.ID, &w.FeederID, &w.Name, &w.RatingA, &w.Protection, &w.ProtectionNote,
			&w.HasTransformer, &w.TransformerName, &w.TransformerKVA, &w.TransformerRatio,
			&w.DestBoardID, &w.DestLabel, &w.Notes, &w.Position, &w.CreatedAt, &w.UpdatedAt,
			&w.DestBoardCode, &w.DestBuildingName); err != nil {
			wrows.Close()
			return nil, err
		}
		if i, ok := index[w.FeederID]; ok {
			n.Feeders[i].Ways = append(n.Feeders[i].Ways, w)
		}
	}
	wrows.Close()
	if err := wrows.Err(); err != nil {
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
		ways := n.Feeders[i].Ways
		sort.SliceStable(ways, func(a, b int) bool { return model.NaturalLess(ways[a].Name, ways[b].Name) })
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
		if err := tx.QueryRow(ctx, `INSERT INTO hv_feeders (network_id, name, voltage, source, rating_a, position)
			VALUES ($1,$2,$3,$4,$5,(SELECT coalesce(max(position),-1)+1 FROM hv_feeders WHERE network_id = $1))
			RETURNING id`, f.NetworkID, f.Name, f.Voltage, f.Source, f.RatingA).Scan(&id); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "create", "hv_feeder", id, "Added feeder "+f.Name)
	})
	return id, err
}

func (s *Store) UpdateHVFeeder(ctx context.Context, role string, f model.HVFeeder) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE hv_feeders SET name = $2, voltage = $3, source = $4, rating_a = $5,
			updated_at = now() WHERE id = $1`, f.ID, f.Name, f.Voltage, f.Source, f.RatingA)
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
		if err := tx.QueryRow(ctx, `INSERT INTO hv_ways (feeder_id, name, rating_a, protection, protection_note,
			has_transformer, transformer_name, transformer_kva, transformer_ratio, dest_board_id, dest_label, notes, position)
			VALUES ($1,$2,$3,$4::hv_protection,$5,$6,$7,$8,$9,$10,$11,$12,
			(SELECT coalesce(max(position),-1)+1 FROM hv_ways WHERE feeder_id = $1)) RETURNING id`,
			w.FeederID, w.Name, w.RatingA, w.Protection, w.ProtectionNote, w.HasTransformer, w.TransformerName,
			w.TransformerKVA, w.TransformerRatio, w.DestBoardID, w.DestLabel, w.Notes).Scan(&id); err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "create", "hv_way", id, "Added way "+w.Name+" feeding "+w.Destination())
	})
	return id, err
}

func (s *Store) UpdateHVWay(ctx context.Context, role string, w model.HVWay) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `UPDATE hv_ways SET feeder_id = coalesce(nullif($2::bigint, 0), feeder_id),
			name = $3, rating_a = $4, protection = $5::hv_protection, protection_note = $6,
			has_transformer = $7, transformer_name = $8, transformer_kva = $9, transformer_ratio = $10,
			dest_board_id = $11, dest_label = $12, notes = $13, updated_at = now() WHERE id = $1`,
			w.ID, w.FeederID, w.Name, w.RatingA, w.Protection, w.ProtectionNote, w.HasTransformer,
			w.TransformerName, w.TransformerKVA, w.TransformerRatio, w.DestBoardID, w.DestLabel, w.Notes)
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
