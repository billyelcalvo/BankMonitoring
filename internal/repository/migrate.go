package repository

import (
	"database/sql"
	"embed"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Migrate applies every pending migration. golang-migrate takes a PostgreSQL
// advisory lock, so several replicas starting at once won't step on each other.
// It returns the schema version the database ended up at.
func Migrate(databaseURL string) (uint, error) {
	source, err := iofs.New(migrationFiles, "migrations")
	if err != nil {
		return 0, fmt.Errorf("open embedded migrations: %w", err)
	}
	connConfig, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return 0, errors.New("DATABASE_URL is invalid")
	}
	db := stdlib.OpenDB(*connConfig)
	defer func(db *sql.DB) { _ = db.Close() }(db)

	driver, err := pgxmigrate.WithInstance(db, &pgxmigrate.Config{})
	if err != nil {
		return 0, fmt.Errorf("prepare migration driver: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", source, "pgx5", driver)
	if err != nil {
		return 0, fmt.Errorf("prepare migrations: %w", err)
	}
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return 0, fmt.Errorf("apply migrations: %w", err)
	}
	version, _, err := m.Version()
	if err != nil {
		return 0, fmt.Errorf("read migration version: %w", err)
	}
	return version, nil
}
