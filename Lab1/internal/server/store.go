package server

import (
	"database/sql"

	_ "modernc.org/sqlite"
)

type StoredPlayer struct {
	Name string
	X    int
	Y    int
	HP   int
}

type Store interface {
	LoadPlayer(name string) (*StoredPlayer, error)
	SavePlayer(p StoredPlayer) error
	DeletePlayer(name string) error
}

type SqliteStore struct {
	db *sql.DB
}

func NewSqliteStore(path string) (*SqliteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	if err := initSchema(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &SqliteStore{db: db}, nil
}

func initSchema(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS players (
			name TEXT PRIMARY KEY,
			x INTEGER NOT NULL,
			y INTEGER NOT NULL,
			hp INTEGER NOT NULL
		);
	`)
	return err
}

func (s *SqliteStore) LoadPlayer(name string) (*StoredPlayer, error) {
	row := s.db.QueryRow("SELECT name, x, y, hp FROM players WHERE name = ?", name)
	var p StoredPlayer
	if err := row.Scan(&p.Name, &p.X, &p.Y, &p.HP); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &p, nil
}

func (s *SqliteStore) SavePlayer(p StoredPlayer) error {
	_, err := s.db.Exec(
		"INSERT INTO players (name, x, y, hp) VALUES (?, ?, ?, ?) ON CONFLICT(name) DO UPDATE SET x = excluded.x, y = excluded.y, hp = excluded.hp",
		p.Name, p.X, p.Y, p.HP,
	)
	return err
}

func (s *SqliteStore) DeletePlayer(name string) error {
	_, err := s.db.Exec("DELETE FROM players WHERE name = ?", name)
	return err
}
