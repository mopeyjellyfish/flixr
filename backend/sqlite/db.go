// Package sqlite opens Flixr's local SQLite database with a serialized writer and bounded readers.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"sort"

	"github.com/spf13/afero"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

const (
	writerConnections = 1
	readerConnections = 4
)

// DB routes mutations through one connection and reads through a separate bounded pool.
type DB struct {
	writer *sql.DB
	reader *sql.DB
	dir    string
}

func Open(dir string) (*DB, error) {
	if err := afero.NewOsFs().MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	dsn := databaseDSN(filepath.Join(dir, "flixr.db"))
	writer, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	writer.SetMaxOpenConns(writerConnections)
	writer.SetMaxIdleConns(writerConnections)
	reader, err := sql.Open("sqlite", dsn)
	if err != nil {
		writer.Close()
		return nil, err
	}
	reader.SetMaxOpenConns(readerConnections)
	reader.SetMaxIdleConns(readerConnections)
	db := &DB{writer: writer, reader: reader, dir: dir}
	if err := db.configure(); err != nil {
		db.Close()
		return nil, err
	}
	if err := migrate(writer); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func databaseDSN(path string) string {
	// _pragma is applied as every modernc connection is opened, unlike one startup PRAGMA.
	return "file:" + url.PathEscape(path) + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
}

func (d *DB) configure() error {
	for _, db := range []*sql.DB{d.writer, d.reader} {
		if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
			return fmt.Errorf("configure sqlite: %w", err)
		}
	}
	return nil
}

func (d *DB) Exec(query string, args ...any) (sql.Result, error) {
	return d.writer.Exec(query, args...)
}
func (d *DB) Begin() (*sql.Tx, error) { return d.writer.Begin() }
func (d *DB) Query(query string, args ...any) (*sql.Rows, error) {
	return d.reader.Query(query, args...)
}
func (d *DB) QueryRow(query string, args ...any) *sql.Row { return d.reader.QueryRow(query, args...) }
func (d *DB) Close() error {
	if err := d.reader.Close(); err != nil {
		d.writer.Close()
		return err
	}
	return d.writer.Close()
}
func (d *DB) Writer() *sql.DB         { return d.writer }
func (d *DB) Reader() *sql.DB         { return d.reader }
func (d *DB) WriterMaxOpenConns() int { return writerConnections }
func (d *DB) ReaderMaxOpenConns() int { return readerConnections }
func (d *DB) DataDir() string         { return d.dir }

func migrate(db *sql.DB) error {
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec("CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)"); err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		var version int
		if _, err = fmt.Sscanf(entry.Name(), "%d_", &version); err != nil {
			return fmt.Errorf("migration name %q: %w", entry.Name(), err)
		}
		var done int
		err = tx.QueryRow("SELECT version FROM schema_migrations WHERE version=?", version).Scan(&done)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}
		body, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return err
		}
		if _, err = tx.Exec(string(body)); err != nil {
			return fmt.Errorf("migration %s: %w", entry.Name(), err)
		}
		if _, err = tx.Exec("INSERT INTO schema_migrations(version) VALUES(?)", version); err != nil {
			return err
		}
	}
	return tx.Commit()
}
