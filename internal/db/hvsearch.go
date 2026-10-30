package db

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/superforcesuytr-droid/power-distribution-system/internal/model"
)

// HVSearch finds equipment on the high-voltage drawings: switchgear,
// transformers, chillers and the other devices on a way or an incomer,
// generators and incoming supplies, ways that carry no switchgear of their own,
// and the switchboards themselves. A device is found by its own designation,
// by what it is ("transformer"), or by the way or incomer it stands on, so
// searching 22SG11 finds the switchgear and the transformer under it. What the
// search names directly comes before what only stands on something it names.
func (s *Store) HVSearch(ctx context.Context, q string, limit int) ([]model.HVSearchHit, error) {
	pool, err := s.getPool()
	if err != nil {
		return nil, err
	}
	q = strings.TrimSpace(q)
	if q == "" {
		return []model.HVSearchHit{}, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 30
	}
	rows, err := pool.Query(ctx, `
		SELECT x.kind, x.title, x.subtitle, x.network_id, n.name, x.focus FROM (
		  SELECT d.kind::text AS kind,
		         CASE WHEN d.name <> '' THEN d.name ELSE coalesce(w.name, f.name) END AS title,
		         concat_ws(' · ',
		           CASE WHEN d.name <> '' AND d.name <> coalesce(w.name, f.name) THEN 'on ' || coalesce(w.name, f.name) END,
		           nullif(concat_ws(' ', CASE WHEN d.kva IS NOT NULL THEN trim_scale(d.kva)::text || ' kVA' END, nullif(d.ratio, '')), ''),
		           CASE WHEN d.rating_a IS NOT NULL THEN trim_scale(d.rating_a)::text || ' A' END,
		           sb.name) AS subtitle,
		         sec.network_id,
		         CASE WHEN d.way_id IS NOT NULL THEN 'hv-way-' || d.way_id ELSE 'hv-feeder-' || d.feeder_id END AS focus,
		         CASE WHEN d.name ILIKE $1 OR d.kind::text ILIKE $1 THEN 0 ELSE 1 END AS direct,
		         CASE d.kind WHEN 'switchgear' THEN 1 WHEN 'transformer' THEN 2 WHEN 'chiller' THEN 4 ELSE 5 END AS rank
		    FROM hv_devices d
		    LEFT JOIN hv_ways w ON w.id = d.way_id
		    LEFT JOIN hv_feeders f ON f.id = d.feeder_id
		    JOIN hv_sections sec ON sec.id = coalesce(w.section_id, f.section_id)
		    JOIN hv_switchboards sb ON sb.id = sec.switchboard_id
		   WHERE d.name ILIKE $1 OR d.kind::text ILIKE $1 OR d.ratio ILIKE $1
		      OR coalesce(w.name, f.name) ILIKE $1 OR coalesce(w.dest_label, '') ILIKE $1
		  UNION ALL
		  SELECT CASE WHEN f.kind = 'generator' THEN 'generator' ELSE 'incomer' END,
		         f.name,
		         concat_ws(' · ', nullif(f.source, ''), CASE WHEN f.rating_a IS NOT NULL THEN trim_scale(f.rating_a)::text || ' A' END, sb.name),
		         f.network_id, 'hv-feeder-' || f.id, 0,
		         CASE WHEN f.kind = 'generator' THEN 3 ELSE 6 END
		    FROM hv_feeders f
		    JOIN hv_sections sec ON sec.id = f.section_id
		    JOIN hv_switchboards sb ON sb.id = sec.switchboard_id
		   WHERE f.name ILIKE $1 OR f.source ILIKE $1 OR f.kind ILIKE $1
		      OR (f.kind <> 'generator' AND 'incoming supply' ILIKE $1)
		  UNION ALL
		  SELECT 'way', w.name,
		         concat_ws(' · ', nullif(w.dest_label, ''), sb.name),
		         sec.network_id, 'hv-way-' || w.id, 0, 7
		    FROM hv_ways w
		    JOIN hv_sections sec ON sec.id = w.section_id
		    JOIN hv_switchboards sb ON sb.id = sec.switchboard_id
		   WHERE (w.name ILIKE $1 OR w.dest_label ILIKE $1)
		     AND NOT EXISTS (SELECT 1 FROM hv_devices d WHERE d.way_id = w.id)
		  UNION ALL
		  SELECT 'switchboard', sb.name, concat_ws(' ', nullif(sb.voltage, ''), nullif(sb.phases, '')),
		         sb.network_id, 'hv-board-' || sb.id, 0, 8
		    FROM hv_switchboards sb
		   WHERE sb.name ILIKE $1 OR 'switchboard' ILIKE $1
		) x JOIN hv_networks n ON n.id = x.network_id
		ORDER BY x.direct, x.rank, x.title, n.position, n.id LIMIT $2`, "%"+q+"%", limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.HVSearchHit{}
	for rows.Next() {
		var h model.HVSearchHit
		if err := rows.Scan(&h.Kind, &h.Title, &h.Subtitle, &h.NetworkID, &h.Drawing, &h.Focus); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// BoardFeed finds where a board is fed from on the HV drawings: the way
// that lands on it, picked by the board or by a label that reads as its code.
// Of several, the one through the named transformer comes first, then one on
// a high-tension drawing. It returns nil when no way lands on the board.
func (s *Store) BoardFeed(ctx context.Context, boardID int64, transformer string) (*model.HVSearchHit, error) {
	pool, err := s.getPool()
	if err != nil {
		return nil, err
	}
	var h model.HVSearchHit
	err = pool.QueryRow(ctx, `
		SELECT 'way', w.name, concat_ws(' · ', nullif(w.dest_label, ''), nullif(w.dest_detail, ''), sb.name),
		       n.id, n.name, 'hv-way-' || w.id
		  FROM hv_ways w
		  JOIN hv_sections sec ON sec.id = w.section_id
		  JOIN hv_switchboards sb ON sb.id = sec.switchboard_id
		  JOIN hv_networks n ON n.id = sec.network_id
		  JOIN boards bo ON bo.id = $1
		 WHERE w.dest_board_id = bo.id
		    OR (w.dest_board_id IS NULL AND w.dest_switchboard_id IS NULL AND w.dest_label ~ '[[:alnum:]]'
		        AND upper(regexp_replace(w.dest_label, '[^[:alnum:]]', '', 'g')) = upper(regexp_replace(bo.code, '[^[:alnum:]]', '', 'g')))
		 ORDER BY CASE WHEN $2 <> '' AND (upper(w.dest_detail) = upper($2)
		            OR EXISTS (SELECT 1 FROM hv_devices d WHERE d.way_id = w.id AND upper(d.name) = upper($2))) THEN 0 ELSE 1 END,
		          CASE WHEN n.tier = 'ht' THEN 0 ELSE 1 END, n.position, n.id, w.position
		 LIMIT 1`, boardID, strings.TrimSpace(transformer)).Scan(&h.Kind, &h.Title, &h.Subtitle, &h.NetworkID, &h.Drawing, &h.Focus)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// MCCBNames lists every MCCB by name with the board it is on, for naming what
// an incomer is fed from: a low-voltage board is often supplied from an MCCB
// on another board rather than straight from a switchboard.
func (s *Store) MCCBNames(ctx context.Context) ([]model.MCCBName, error) {
	pool, err := s.getPool()
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, `SELECT bo.code, mm.name, mm.rating_a::float8
		FROM mccbs mm JOIN boards bo ON bo.id = mm.board_id
		ORDER BY bo.code, mm.position, mm.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []model.MCCBName{}
	for rows.Next() {
		var m model.MCCBName
		if err := rows.Scan(&m.Board, &m.Name, &m.RatingA); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
