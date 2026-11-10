// Package db kapselt den Postgres-Zugriff und die nummerierten Migrationen.
package db

import (
	"context"
	"embed"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

func Open(ctx context.Context, url string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	if cfg.MaxConns < 20 {
		cfg.MaxConns = 20
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	if _, set := cfg.ConnConfig.RuntimeParams["statement_timeout"]; !set {
		cfg.ConnConfig.RuntimeParams["statement_timeout"] = "60000" // ms: eine Abfrage darf nie ewig laufen
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// Migrate führt alle noch nicht angewendeten Dateien aus migrations/ der Reihe nach aus.
// Jede Datei läuft in einer eigenen Transaktion; der Stand steht in schema_migrations.
func Migrate(ctx context.Context, pool *pgxpool.Pool) ([]string, error) {
	// Nur ein Programm gleichzeitig migriert (mehrere Pods starten oft gemeinsam): Sperre auf einer eigenen Verbindung, die die anderen warten lässt.
	lock, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer lock.Release()
	if _, err := lock.Exec(ctx, `SELECT pg_advisory_lock(727003)`); err != nil {
		return nil, err
	}
	defer func() { _, _ = lock.Exec(context.Background(), `SELECT pg_advisory_unlock(727003)`) }()
	if _, err := pool.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return nil, err
	}
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	// Schutz vor „Datenbank neuer als Programm“ (z. B. ein älteres Image startet auf einer schon aktualisierten Datenbank): nicht starten.
	known := map[string]bool{}
	for _, n := range names {
		known[n] = true
	}
	rows, err := pool.Query(ctx, `SELECT name FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	var unknown []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil && !known[n] {
			unknown = append(unknown, n)
		}
	}
	rows.Close()
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return nil, fmt.Errorf("die Datenbank ist neuer als dieses Programm (unbekannte Migration %s) – bitte die passende oder eine neuere Version starten", unknown[0])
	}

	var applied []string
	for _, name := range names {
		var done bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE name=$1)`, name).Scan(&done); err != nil {
			return applied, err
		}
		if done {
			continue
		}
		sql, err := migrationFiles.ReadFile("migrations/" + name)
		if err != nil {
			return applied, err
		}
		tx, err := pool.Begin(ctx)
		if err != nil {
			return applied, err
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			_ = tx.Rollback(ctx)
			return applied, fmt.Errorf("%s: %w", name, err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO schema_migrations(name) VALUES ($1)`, name); err != nil {
			_ = tx.Rollback(ctx)
			return applied, err
		}
		if err := tx.Commit(ctx); err != nil {
			return applied, err
		}
		applied = append(applied, name)
	}
	return applied, nil
}

// MigrationNames: alle Migrationen, die in diese Programmdatei eingebaut sind (sortiert)
func MigrationNames() []string {
	entries, _ := migrationFiles.ReadDir("migrations")
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}
