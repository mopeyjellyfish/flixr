// Package sqlite opens Flixr's local SQLite database with a serialized writer and bounded readers.
package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"sort"

	"github.com/spf13/afero"
	modernsqlite "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

const (
	writerConnections = 1
	readerConnections = 4
	// MinimumSchemaVersion is the oldest database state Open can upgrade. Zero
	// means a new database or one without any applied migrations.
	MinimumSchemaVersion = 0
	// LatestSchemaVersion is the newest embedded schema this binary understands.
	LatestSchemaVersion = 29
)

// ErrIncompatibleSchema identifies a database containing migrations that this
// binary does not understand.
var ErrIncompatibleSchema = errors.New("database schema is not supported")

// SchemaCompatibilityError reports an applied migration that this binary does
// not recognize. Starting a newer Flixr version or restoring a compatible
// pre-upgrade backup is safe; attempting to downgrade the schema in place is not.
type SchemaCompatibilityError struct {
	// FoundVersion is the unknown applied migration, or zero when migration
	// history is absent from a database that already contains user objects.
	FoundVersion int
	// SupportedVersion is the latest migration embedded in this binary.
	SupportedVersion int
	// MissingMigrationHistory distinguishes an untracked nonempty database from
	// an unsupported applied migration.
	MissingMigrationHistory bool
}

func (e *SchemaCompatibilityError) Error() string {
	if e.MissingMigrationHistory {
		return fmt.Sprintf("database has user schema objects but no Flixr migration history; this Flixr binary supports empty databases and embedded migrations through %d; choose an empty data directory or stop Flixr and restore a compatible backup", e.SupportedVersion)
	}
	return fmt.Sprintf("database contains unsupported schema migration %d; this Flixr binary supports new databases and embedded migrations through %d; update Flixr or stop it and restore a compatible backup created before the unsupported upgrade", e.FoundVersion, e.SupportedVersion)
}

func (e *SchemaCompatibilityError) Unwrap() error { return ErrIncompatibleSchema }

// DB routes mutations through one connection and reads through a separate bounded pool.
type DB struct {
	writer *sql.DB
	reader *sql.DB
	dir    string
}

func Open(dir string) (*DB, error) {
	return OpenContext(context.Background(), dir)
}

// OpenContext opens and migrates Flixr's database. Cancellation rolls back an
// in-progress migration before OpenContext returns.
func OpenContext(ctx context.Context, dir string) (*DB, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
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
	if err := migrate(ctx, writer); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate database: %w", err)
	}
	if err := db.configure(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func databaseDSN(path string) string {
	// _pragma is applied as every modernc connection is opened, unlike one startup PRAGMA.
	return "file:" + url.PathEscape(path) + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
}

func (d *DB) configure(ctx context.Context) error {
	for _, db := range []*sql.DB{d.writer, d.reader} {
		if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL"); err != nil {
			return fmt.Errorf("configure sqlite: %w", err)
		}
	}
	return nil
}

func (d *DB) Exec(query string, args ...any) (sql.Result, error) {
	return d.writer.Exec(query, args...)
}
func (d *DB) Begin() (*sql.Tx, error) { return d.writer.Begin() }
func (d *DB) BeginTx(ctx context.Context) (*sql.Tx, error) {
	return d.writer.BeginTx(ctx, nil)
}
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

// OnlineBackup writes a transactionally consistent SQLite snapshot without
// copying the live database or its WAL files.
func (d *DB) OnlineBackup(ctx context.Context, destination string) error {
	return d.onlineBackup(ctx, destination, nil)
}

func (d *DB) onlineBackup(ctx context.Context, destination string, afterStep func(bool)) error {
	conn, err := d.writer.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open sqlite backup connection: %w", err)
	}
	defer conn.Close()
	type backuper interface {
		NewBackup(string) (*modernsqlite.Backup, error)
	}
	if err := conn.Raw(func(driverConn any) error {
		provider, ok := driverConn.(backuper)
		if !ok {
			return errors.New("sqlite driver does not support online backup")
		}
		backup, err := provider.NewBackup(destination)
		if err != nil {
			return fmt.Errorf("start sqlite backup: %w", err)
		}
		for {
			if err := ctx.Err(); err != nil {
				return errors.Join(err, backup.Finish())
			}
			more, err := backup.Step(128)
			if err != nil {
				return errors.Join(fmt.Errorf("copy sqlite backup pages: %w", err), backup.Finish())
			}
			if afterStep != nil {
				afterStep(more)
			}
			if !more {
				if err := backup.Finish(); err != nil {
					return fmt.Errorf("finish sqlite backup: %w", err)
				}
				return nil
			}
		}
	}); err != nil {
		return err
	}
	return nil
}

func (d *DB) SchemaVersion() (int, error) {
	var version int
	if err := d.QueryRow(`SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&version); err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return version, nil
}

type migration struct {
	version int
	name    string
}

func migrationFiles(source fs.FS) ([]migration, map[int]struct{}, error) {
	entries, err := fs.ReadDir(source, "migrations")
	if err != nil {
		return nil, nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	files := make([]migration, 0, len(entries))
	supported := make(map[int]struct{}, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		var version int
		if _, err = fmt.Sscanf(entry.Name(), "%d_", &version); err != nil {
			return nil, nil, fmt.Errorf("migration name %q: %w", entry.Name(), err)
		}
		if _, exists := supported[version]; exists {
			return nil, nil, fmt.Errorf("duplicate migration version %d", version)
		}
		supported[version] = struct{}{}
		files = append(files, migration{version: version, name: entry.Name()})
	}
	return files, supported, nil
}

func migrate(ctx context.Context, db *sql.DB) error {
	files, supported, err := migrationFiles(migrations)
	if err != nil {
		return err
	}
	if len(files) == 0 || files[len(files)-1].version != LatestSchemaVersion {
		return fmt.Errorf("latest embedded migration does not match schema version %d", LatestSchemaVersion)
	}
	return applyMigrations(ctx, db, migrations, files, supported, LatestSchemaVersion)
}

func migrateFS(ctx context.Context, db *sql.DB, source fs.FS) error {
	files, supported, err := migrationFiles(source)
	if err != nil {
		return err
	}
	supportedVersion := MinimumSchemaVersion
	if len(files) > 0 {
		supportedVersion = files[len(files)-1].version
	}
	return applyMigrations(ctx, db, source, files, supported, supportedVersion)
}

func applyMigrations(ctx context.Context, db *sql.DB, source fs.FS, files []migration, supported map[int]struct{}, supportedVersion int) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var migrationTable string
	err = tx.QueryRowContext(ctx, `SELECT name FROM sqlite_schema WHERE type='table' AND name='schema_migrations'`).Scan(&migrationTable)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		rows, queryErr := tx.QueryContext(ctx, `SELECT version FROM schema_migrations ORDER BY version`)
		if queryErr != nil {
			return fmt.Errorf("inspect applied schema migrations: %w", queryErr)
		}
		applied := 0
		for rows.Next() {
			var version int
			if scanErr := rows.Scan(&version); scanErr != nil {
				rows.Close()
				return fmt.Errorf("inspect applied schema migrations: %w", scanErr)
			}
			if _, ok := supported[version]; !ok {
				rows.Close()
				return &SchemaCompatibilityError{FoundVersion: version, SupportedVersion: supportedVersion}
			}
			applied++
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			rows.Close()
			return fmt.Errorf("inspect applied schema migrations: %w", rowsErr)
		}
		rows.Close()
		if applied == 0 {
			if err = rejectUntrackedSchema(ctx, tx, supportedVersion, true); err != nil {
				return err
			}
		}
	} else {
		if err = rejectUntrackedSchema(ctx, tx, supportedVersion, false); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY)"); err != nil {
		return err
	}
	for _, migration := range files {
		var done int
		err = tx.QueryRowContext(ctx, "SELECT version FROM schema_migrations WHERE version=?", migration.version).Scan(&done)
		if err == nil {
			continue
		}
		if err != sql.ErrNoRows {
			return err
		}
		body, readErr := fs.ReadFile(source, "migrations/"+migration.name)
		if readErr != nil {
			return readErr
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, string(body)); err != nil {
			return fmt.Errorf("migration %s: %w", migration.name, err)
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO schema_migrations(version) VALUES(?)", migration.version); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func rejectUntrackedSchema(ctx context.Context, tx *sql.Tx, supportedVersion int, historyTablePresent bool) error {
	query := `SELECT COUNT(*) FROM sqlite_schema WHERE name NOT GLOB 'sqlite_*'`
	if historyTablePresent {
		query += ` AND name <> 'schema_migrations'`
	}
	var userObjects int
	if err := tx.QueryRowContext(ctx, query).Scan(&userObjects); err != nil {
		return fmt.Errorf("inspect database schema: %w", err)
	}
	if userObjects == 0 {
		return nil
	}
	return &SchemaCompatibilityError{
		FoundVersion:            0,
		SupportedVersion:        supportedVersion,
		MissingMigrationHistory: true,
	}
}
