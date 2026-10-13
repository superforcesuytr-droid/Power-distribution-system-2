package db

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/superforcesuytr-droid/power-distribution-system/internal/model"
)

// How each kind of asset is named, so a maintenance record or a history can
// say what it is about. The query is given the asset's id.
var assetNameSQL = map[string]string{
	"board":          `SELECT code FROM boards WHERE id = $1`,
	"mccb":           `SELECT name FROM mccbs WHERE id = $1`,
	"mcb":            `SELECT name FROM mcbs WHERE id = $1`,
	"circuit":        `SELECT name || ' (' || code || ')' FROM circuits WHERE id = $1`,
	"hv_switchboard": `SELECT name FROM hv_switchboards WHERE id = $1`,
	"hv_feeder":      `SELECT name FROM hv_feeders WHERE id = $1`,
	"hv_way":         `SELECT name FROM hv_ways WHERE id = $1`,
	// A device is named by its designation, or by what it is when it carries
	// none of its own - switchgear named after its way is "Switchgear on 22SG11".
	"hv_device": `SELECT CASE WHEN d.name <> '' AND d.name <> coalesce(w.name, f.name, '') THEN d.name
		ELSE initcap(d.kind::text) END
		|| coalesce(' on ' || w.name, ' on ' || f.name, '')
		FROM hv_devices d LEFT JOIN hv_ways w ON w.id = d.way_id LEFT JOIN hv_feeders f ON f.id = d.feeder_id
		WHERE d.id = $1`,
}

// ValidAssetEntity reports whether maintenance can be recorded against this
// kind of asset.
func ValidAssetEntity(entity string) bool {
	_, ok := assetNameSQL[entity]
	return ok
}

func assetName(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, entity string, id int64) (string, error) {
	query, ok := assetNameSQL[entity]
	if !ok {
		return "", &UserError{"Work cannot be recorded against that."}
	}
	var name string
	if err := q.QueryRow(ctx, query, id).Scan(&name); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", err
	}
	return name, nil
}

// AddMaintenance records a piece of work carried out on an asset. It goes in
// the activity log against that asset too, so it shows in the asset's history
// alongside the changes made to its record.
func (s *Store) AddMaintenance(ctx context.Context, role string, r model.MaintenanceRecord) (int64, error) {
	var id int64
	err := s.withTx(ctx, func(tx pgx.Tx) error {
		name, err := assetName(ctx, tx, r.Entity, r.EntityID)
		if err != nil {
			return err
		}
		var doneOn any
		if strings.TrimSpace(r.DoneOn) != "" {
			d, err := time.Parse("2006-01-02", r.DoneOn)
			if err != nil {
				return &UserError{"The date must be a date, such as 2026-10-13."}
			}
			if d.After(time.Now().Add(24 * time.Hour)) {
				return &UserError{"Work cannot be recorded for a date that has not come yet."}
			}
			doneOn = d
		}
		if err := tx.QueryRow(ctx, `INSERT INTO maintenance_records
			(entity, entity_id, kind, done_on, performed_by, description, actor_role)
			VALUES ($1, $2, $3, coalesce($4::date, current_date), $5, $6, $7) RETURNING id`,
			r.Entity, r.EntityID, r.Kind, doneOn, r.PerformedBy, r.Description, role).Scan(&id); err != nil {
			return err
		}
		summary := fmt.Sprintf("%s on %s: %s", model.MaintenanceKindLabel[r.Kind], name, r.Description)
		if r.PerformedBy != "" {
			summary += " (by " + r.PerformedBy + ")"
		}
		return s.audit(ctx, tx, role, "maintenance", r.Entity, r.EntityID, summary)
	})
	return id, err
}

// DeleteMaintenance removes a maintenance record entered in error. The removal
// is itself logged, so the history still shows that it was there.
func (s *Store) DeleteMaintenance(ctx context.Context, role string, id int64) error {
	return s.withTx(ctx, func(tx pgx.Tx) error {
		var entity, kind, desc string
		var entityID int64
		if err := tx.QueryRow(ctx, `DELETE FROM maintenance_records WHERE id = $1
			RETURNING entity, entity_id, kind, description`, id).Scan(&entity, &entityID, &kind, &desc); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		name, err := assetName(ctx, tx, entity, entityID)
		if errors.Is(err, ErrNotFound) {
			name = "a removed item"
		} else if err != nil {
			return err
		}
		return s.audit(ctx, tx, role, "delete", entity, entityID,
			fmt.Sprintf("Removed the %s record on %s: %s", strings.ToLower(model.MaintenanceKindLabel[kind]), name, desc))
	})
}

// AssetHistory gathers the work recorded against an asset and every change
// made to its record, most recent first.
func (s *Store) AssetHistory(ctx context.Context, entity string, id int64) (*model.AssetHistory, error) {
	pool, err := s.getPool()
	if err != nil {
		return nil, err
	}
	name, err := assetName(ctx, pool, entity, id)
	if err != nil {
		return nil, err
	}
	h := &model.AssetHistory{Entity: entity, EntityID: id, Name: name,
		Maintenance: []model.MaintenanceRecord{}, Changes: []model.AuditEntry{}}

	rows, err := pool.Query(ctx, `SELECT id, entity, entity_id, kind, to_char(done_on, 'YYYY-MM-DD'),
		performed_by, description, actor_role, created_at
		FROM maintenance_records WHERE entity = $1 AND entity_id = $2
		ORDER BY done_on DESC, id DESC`, entity, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r model.MaintenanceRecord
		if err := rows.Scan(&r.ID, &r.Entity, &r.EntityID, &r.Kind, &r.DoneOn,
			&r.PerformedBy, &r.Description, &r.ActorRole, &r.CreatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		h.Maintenance = append(h.Maintenance, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = pool.Query(ctx, `SELECT id, at, actor_role, action, entity, coalesce(entity_id, 0), summary
		FROM audit_log WHERE entity = $1 AND entity_id = $2 ORDER BY id DESC LIMIT 200`, entity, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e model.AuditEntry
		if err := rows.Scan(&e.ID, &e.At, &e.ActorRole, &e.Action, &e.Entity, &e.EntityID, &e.Summary); err != nil {
			return nil, err
		}
		h.Changes = append(h.Changes, e)
	}
	if len(h.Changes) > 0 {
		t := h.Changes[0].At
		h.LastChanged = &t
	}
	return h, rows.Err()
}
