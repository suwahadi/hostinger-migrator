package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	sqlmigrations "hostinger-migrator/migrations/sqlite"
)

var ErrNotFound = errors.New("data tidak ditemukan")

type Store struct {
	db  *sql.DB
	now func() time.Time
}

func Open(ctx context.Context, path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("path database tidak valid: %w", err)
	}
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "synchronous(NORMAL)")
	dsn := "file:" + filepath.ToSlash(abs) + "?" + q.Encode()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("gagal membuka database: %w", err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db, now: func() time.Time { return time.Now().UTC() }}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("gagal membuka database %s: %w", abs, err)
	}
	if err := s.Migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) SetClock(now func() time.Time) {
	s.now = now
}

func (s *Store) timestamp() string {
	return s.now().UTC().Format(time.RFC3339)
}

func (s *Store) Pragma(ctx context.Context, name string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, "PRAGMA "+name).Scan(&v)
	return v, err
}

func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		return fmt.Errorf("gagal menyiapkan tabel migrasi: %w", err)
	}
	names, err := fs.Glob(sqlmigrations.Files, "*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for _, name := range names {
		version := strings.TrimSuffix(name, ".sql")
		var exists int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(1) FROM schema_migrations WHERE version = ?`, version).Scan(&exists); err != nil {
			return err
		}
		if exists > 0 {
			continue
		}
		body, err := sqlmigrations.Files.ReadFile(name)
		if err != nil {
			return err
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migrasi %s gagal: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`, version, s.timestamp()); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("migrasi %s gagal disimpan: %w", name, err)
		}
	}
	return nil
}

func (s *Store) AppliedMigrations(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
