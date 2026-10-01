package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"

	"github.com/ofabiodev/osmose/types"
	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS users (
	osmium_id       TEXT PRIMARY KEY,
	lastfm_username TEXT NOT NULL,
	updated_at      INTEGER NOT NULL DEFAULT (unixepoch())
);`

// store persists links between Osmium users and Last.fm accounts.
type store struct {
	db *sql.DB
}

func openStore(path string) (*store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// SQLite allows a single writer; one connection avoids "database is locked".
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &store{db: db}, nil
}

func (s *store) Close() error { return s.db.Close() }

// SetLastfmUsername links an Osmium user to a Last.fm username, replacing any
// previous link.
func (s *store) SetLastfmUsername(ctx context.Context, id types.ID, username string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO users (osmium_id, lastfm_username) VALUES (?, ?)
		ON CONFLICT (osmium_id) DO UPDATE SET
			lastfm_username = excluded.lastfm_username,
			updated_at = unixepoch()`,
		formatID(id), username)
	return err
}

// LastfmUsername returns the Last.fm username linked to an Osmium user, or ""
// when there is none.
func (s *store) LastfmUsername(ctx context.Context, id types.ID) (string, error) {
	var username string
	err := s.db.QueryRowContext(ctx,
		`SELECT lastfm_username FROM users WHERE osmium_id = ?`, formatID(id)).Scan(&username)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return username, err
}

// link is an Osmium user and the Last.fm account they attached.
type link struct {
	OsmiumID       types.ID
	LastfmUsername string
}

// Links returns every attached account.
func (s *store) Links(ctx context.Context) ([]link, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT osmium_id, lastfm_username FROM users`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var links []link
	for rows.Next() {
		var id, username string
		if err := rows.Scan(&id, &username); err != nil {
			return nil, err
		}
		n, err := strconv.ParseUint(id, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid osmium_id %q: %w", id, err)
		}
		links = append(links, link{OsmiumID: types.ID(n), LastfmUsername: username})
	}
	return links, rows.Err()
}

// formatID stores IDs as text because Osmium IDs are uint64 and SQLite
// integers are signed.
func formatID(id types.ID) string { return strconv.FormatUint(id.Uint64(), 10) }
