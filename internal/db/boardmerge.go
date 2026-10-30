package db

import (
	"context"
	"fmt"
	"log"

	"github.com/jackc/pgx/v5"
)

// Board codes are written by hand off panel labels, so the same board turns
// up as "PE1", "PE 1" or "pe-1". These compare codes and breaker names with
// spaces, dashes, dots and case taken out, so each room has one board.
const (
	boardKey = `upper(regexp_replace(code, '[^[:alnum:]]', '', 'g'))`
	nameKey  = `upper(regexp_replace(name, '[^[:alnum:]]', '', 'g'))`
)

// boardLike finds the board whose code reads the same as code, other than
// the board except.
func boardLike(ctx context.Context, tx pgx.Tx, code string, except int64) (int64, string, error) {
	var id int64
	var found string
	err := tx.QueryRow(ctx, `SELECT id, code FROM boards WHERE `+boardKey+` = upper(regexp_replace($1, '[^[:alnum:]]', '', 'g')) AND id <> $2
		ORDER BY id LIMIT 1`, code, except).Scan(&id, &found)
	if err == pgx.ErrNoRows {
		return 0, "", nil
	}
	return id, found, err
}

// refuseLookalike stops a second board being made under a code that reads
// the same as one already there.
func refuseLookalike(ctx context.Context, tx pgx.Tx, code string, except int64) error {
	id, found, err := boardLike(ctx, tx, code, except)
	if err != nil || id == 0 {
		return err
	}
	return &UserError{fmt.Sprintf("There is already a board called %s. Open that one instead of adding another.", found)}
}

// mergeLookalikeBoards folds boards whose codes read the same into the one
// made first. It runs on start so a room already entered twice, by hand and
// from its ready-made layout, ends up as one board with both lots of detail.
// A breaker on both keeps what the first board has and takes only what it
// lacks; a duplicate that still holds anything that would clash is left be.
func (s *Store) mergeLookalikeBoards(ctx context.Context) {
	pool, err := s.getPool()
	if err != nil {
		return
	}
	rows, err := pool.Query(ctx, `SELECT d.id, k.id FROM boards d
		JOIN LATERAL (SELECT id FROM boards k WHERE `+
		`upper(regexp_replace(k.code, '[^[:alnum:]]', '', 'g')) = upper(regexp_replace(d.code, '[^[:alnum:]]', '', 'g'))
		ORDER BY k.id LIMIT 1) k ON k.id <> d.id ORDER BY d.id`)
	if err != nil {
		log.Printf("warning: could not look for duplicate boards: %v", err)
		return
	}
	var pairs [][2]int64
	for rows.Next() {
		var p [2]int64
		if err := rows.Scan(&p[0], &p[1]); err == nil {
			pairs = append(pairs, p)
		}
	}
	rows.Close()
	for _, p := range pairs {
		if err := s.withTx(ctx, func(tx pgx.Tx) error { return s.mergeBoard(ctx, tx, p[0], p[1]) }); err != nil {
			log.Printf("warning: could not merge board %d into %d: %v", p[0], p[1], err)
		}
	}
}

func (s *Store) mergeBoard(ctx context.Context, tx pgx.Tx, dup, keep int64) error {
	var dupCode, keepCode string
	if err := tx.QueryRow(ctx, `SELECT (SELECT code FROM boards WHERE id = $1), (SELECT code FROM boards WHERE id = $2)`,
		dup, keep).Scan(&dupCode, &keepCode); err != nil {
		return err
	}
	steps := []string{
		// Supply: an incomer for a half the kept board has no incomer on yet.
		`UPDATE board_incomers SET board_id = $2 WHERE board_id = $1
			AND section NOT IN (SELECT section FROM board_incomers WHERE board_id = $2)`,
		`UPDATE boards k SET bus_section_name = d.bus_section_name, bus_section_panel = d.bus_section_panel,
			bus_section_rating_a = d.bus_section_rating_a, bus_section_closed = d.bus_section_closed
			FROM boards d WHERE k.id = $2 AND d.id = $1 AND k.bus_section_name = ''`,
		`UPDATE boards k SET voltage = CASE WHEN k.voltage = '' THEN d.voltage ELSE k.voltage END,
			phases = CASE WHEN k.phases = '' THEN d.phases ELSE k.phases END,
			location = CASE WHEN k.location = '' THEN d.location ELSE k.location END, updated_at = now()
			FROM boards d WHERE k.id = $2 AND d.id = $1`,
		// A breaker on both: fill in what the kept one lacks, then bring its
		// MCBs across where the names are free.
		`UPDATE mccbs k SET feeds = CASE WHEN k.feeds = '' THEN d.feeds ELSE k.feeds END,
			location = CASE WHEN k.location = '' THEN d.location ELSE k.location END,
			section = d.section, updated_at = now()
			FROM mccbs d WHERE k.board_id = $2 AND d.board_id = $1
			AND upper(regexp_replace(k.name, '[^[:alnum:]]', '', 'g')) = upper(regexp_replace(d.name, '[^[:alnum:]]', '', 'g'))`,
		`UPDATE mcbs b SET mccb_id = k.id FROM mccbs d, mccbs k WHERE b.mccb_id = d.id AND d.board_id = $1 AND k.board_id = $2
			AND upper(regexp_replace(k.name, '[^[:alnum:]]', '', 'g')) = upper(regexp_replace(d.name, '[^[:alnum:]]', '', 'g'))
			AND NOT EXISTS (SELECT 1 FROM mcbs x WHERE x.mccb_id = k.id AND x.name = b.name)`,
		`DELETE FROM mccbs d USING mccbs k WHERE d.board_id = $1 AND k.board_id = $2
			AND upper(regexp_replace(k.name, '[^[:alnum:]]', '', 'g')) = upper(regexp_replace(d.name, '[^[:alnum:]]', '', 'g'))
			AND NOT EXISTS (SELECT 1 FROM mcbs WHERE mccb_id = d.id)`,
		// A breaker only on the duplicate moves across whole, after the rest.
		`UPDATE mccbs d SET board_id = $2, position = d.position + (SELECT coalesce(max(position),-1)+1 FROM mccbs WHERE board_id = $2)
			WHERE d.board_id = $1 AND NOT EXISTS (SELECT 1 FROM mccbs k WHERE k.board_id = $2
			AND upper(regexp_replace(k.name, '[^[:alnum:]]', '', 'g')) = upper(regexp_replace(d.name, '[^[:alnum:]]', '', 'g')))`,
		`UPDATE hv_ways SET dest_board_id = $2 WHERE dest_board_id = $1`,
	}
	for _, q := range steps {
		if _, err := tx.Exec(ctx, q, dup, keep); err != nil {
			return err
		}
	}
	var left int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM mccbs WHERE board_id = $1`, dup).Scan(&left); err != nil {
		return err
	}
	if left > 0 {
		log.Printf("warning: board %s reads the same as %s but %d of its breakers clash; left as it is", dupCode, keepCode, left)
		return s.audit(ctx, tx, "system", "update", "board", keep,
			fmt.Sprintf("Merged what could be merged of %s into %s; %d breakers on %s clash and stay there", dupCode, keepCode, left, dupCode))
	}
	if _, err := tx.Exec(ctx, `DELETE FROM boards WHERE id = $1`, dup); err != nil {
		return err
	}
	log.Printf("merged board %s into %s", dupCode, keepCode)
	return s.audit(ctx, tx, "system", "update", "board", keep, fmt.Sprintf("Merged board %s into %s", dupCode, keepCode))
}
